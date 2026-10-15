// Package media serves selected videos over a real loopback HTTP connection.
// Wails' Windows asset response buffers its complete body, so it must never
// carry a video file. net/http streams files and supports byte-range requests.
package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"golang.org/x/sync/singleflight"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syncplayer/internal/diagnostics"
	"time"
)

type Server struct {
	metadata           map[string]VideoMetadata
	metadataReady      chan struct{}
	metadataCancel     context.CancelFunc
	metadataGeneration int
	mu                 sync.RWMutex
	server             *http.Server
	tlsCancel          context.CancelFunc
	thumbnailGroup     singleflight.Group
	thumbnailOnce      sync.Once
	thumbnailSlots     chan struct{}
	baseURL            string
	token              string
	lan                bool
	videoClient        string
	catalog            map[string]CatalogEntry
	generation         uint64
	path               string
}

func New() (*Server, error) {
	var secret [24]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	s := &Server{token: hex.EncodeToString(secret[:]), catalog: map[string]CatalogEntry{}}

	return s, nil
}

func (s *Server) Close() error {
	s.mu.Lock()
	if s.tlsCancel != nil {
		s.tlsCancel()
		s.tlsCancel = nil
	}
	if s.metadataCancel != nil {
		s.metadataCancel()
		s.metadataCancel = nil
	}
	server := s.server
	s.server = nil
	s.baseURL = ""
	s.lan = false
	s.videoClient = ""
	s.path = ""
	s.generation++
	s.mu.Unlock()
	if server != nil {
		return server.Close()
	}
	return nil
}

func (s *Server) Select(path string) (string, error) {
	if err := s.start("127.0.0.1"); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	info, err := file.Stat()
	file.Close()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("所选视频不是普通文件")
	}
	if info.Size() == 0 {
		return "", errors.New("所选视频文件为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.path = path
	diagnostics.Event("video.file", map[string]any{"bytes": info.Size(), "extension": filepath.Ext(path), "generation": s.generation})
	return s.baseURL + s.route(), nil
}

// route is used only while holding mu. No user path appears in the URL.
func (s *Server) route() string {
	return "/" + s.token + "/" + strconv.FormatUint(s.generation, 10) + "/video" + filepath.Ext(s.path)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	route := s.requestKind(r.URL.Path)
	agent := strings.ReplaceAll(strings.ReplaceAll(r.UserAgent(), "\r", ""), "\n", "")
	if len(agent) > 180 {
		agent = agent[:180]
	}
	diagnostics.Event("media.request.begin", map[string]any{"route": route, "user_agent": agent, "method": r.Method, "range": r.Header.Get("Range")})
	response := &responseLog{ResponseWriter: w}
	w = response
	defer func() {
		diagnostics.Event("media.request", map[string]any{"route": route, "user_agent": agent, "method": r.Method, "range": r.Header.Get("Range"), "status": response.status, "bytes": response.bytes, "elapsed_ms": time.Since(started).Milliseconds(), "canceled": r.Context().Err() != nil, "error": response.err})
	}()
	if s.serveCatalog(w, r) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	path := s.path
	valid := path != "" && r.URL.Path == s.route()
	s.mu.RUnlock()
	if !valid {
		http.NotFound(w, r)
		return
	}
	s.recordVideoClient(r)
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "视频无法读取", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.Error(w, "视频信息无法读取", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	// ServeContent reads from the file on demand, handles HEAD and Range, and
	// stops copying when the media element cancels a request or seeks.
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

type responseLog struct {
	http.ResponseWriter
	status int
	bytes  int64
	err    error
}

func (w *responseLog) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseLog) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, e := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	if e != nil {
		w.err = e
	}
	return n, e
}

func (s *Server) requestKind(path string) string {
	s.mu.RLock()
	prefix := s.catalogPrefix()
	s.mu.RUnlock()
	route := strings.TrimPrefix(path, prefix)
	switch {
	case route == "" || route == "web":
		return "portal"
	case route == "deovr" || route == "deovr/" || route == "deovr.json" || route == "catalog.json":
		return "catalog"
	case strings.HasPrefix(route, "item/"):
		return "detail"
	case strings.HasPrefix(route, "file/"):
		return "video"
	case strings.HasPrefix(route, "thumbnail/"):
		return "thumbnail"
	default:
		return "unknown"
	}
}
