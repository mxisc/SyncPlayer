package media

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"hash/crc32"
	"html/template"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syncplayer/internal/diagnostics"
	"syncplayer/internal/lanhttps"
	"time"
)

type CatalogEntry struct {
	Name string
	Path string
}

// DeoVR parses video IDs as signed 32-bit integers. Keep file-routing IDs
// unchanged and assign the same numeric ID to feed entries and descriptions.
func deoVideoIDs(entries map[string]CatalogEntry) map[string]int32 {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ids := make(map[string]int32, len(keys))
	used := make(map[int32]bool, len(keys))
	for _, key := range keys {
		id := int32(crc32.ChecksumIEEE([]byte(key)) & 0x7fffffff)
		for id == 0 || used[id] {
			id = int32((uint32(id) + 1) & 0x7fffffff)
		}
		ids[key] = id
		used[id] = true
	}
	return ids
}

func (s *Server) start(host string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		return nil
	}
	port := "0"
	lan := !net.ParseIP(host).IsLoopback()
	if lan {
		port = "8787"
	}
	var httpsManager *lanhttps.Manager
	if lan {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		httpsManager, err = lanhttps.Open(filepath.Join(filepath.Dir(exe), "settings", "https"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if httpsManager != nil {
			port = "443"
			if err := httpsManager.Ensure(); err != nil && !httpsManager.HasValidCertificate() {
				return err
			}
		}
	}
	l, err := net.Listen("tcp4", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	s.baseURL = "http://" + l.Addr().String()
	if httpsManager != nil {
		s.baseURL = "https://" + httpsManager.Domain()
		l = tls.NewListener(l, httpsManager.TLSConfig())
		ctx, cancel := context.WithCancel(context.Background())
		s.tlsCancel = cancel
		go httpsManager.Renew(ctx, func(err error) {
			diagnostics.Event("media.https.renew", map[string]any{"success": err == nil})
		})
	}
	s.lan = lan
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	server := s.server
	go server.Serve(l)
	return nil
}
func (s *Server) StartLAN() error {
	conn, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return errors.New("未找到局域网地址")
	}
	ip := conn.LocalAddr().(*net.UDPAddr).IP
	conn.Close()
	if !ip.IsPrivate() {
		return errors.New("媒体服务需要家庭局域网地址")
	}
	return s.start(ip.String())
}
func (s *Server) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.server == nil {
		return ""
	}
	if s.lan {
		return s.baseURL
	}
	return s.baseURL + "/" + s.token + "/"
}
func (s *Server) SetCatalog(entries map[string]CatalogEntry) {
	s.mu.Lock()
	s.catalog = entries
	s.mu.Unlock()
	s.probeCatalog(entries)
}
func (s *Server) Resolve(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.server == nil {
		return ""
	}
	base, _ := url.Parse(s.baseURL)
	if u.Host != base.Host {
		return ""
	}
	if s.path != "" && u.Path == s.route() {
		return s.path
	}
	prefix := s.catalogPrefix() + "file/"
	if strings.HasPrefix(u.Path, prefix) {
		id := strings.Split(strings.TrimPrefix(u.Path, prefix), "/")[0]
		return s.catalog[id].Path
	}
	return ""
}
func (s *Server) serveCatalog(w http.ResponseWriter, r *http.Request) bool {
	s.mu.RLock()
	prefix := s.catalogPrefix()
	base := s.baseURL
	entries := s.catalog
	s.mu.RUnlock()

	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	route := strings.TrimPrefix(r.URL.Path, prefix)
	if route == "web" {
		s.portal(w, r, base, prefix, entries)
		return true
	}
	if route == "" || route == "deovr" || route == "deovr/" || route == "deovr.json" || route == "catalog.json" {
		if !s.awaitMetadata(r.Context()) {
			http.Error(w, "视频信息读取中，请稍后重试", 503)
			return true
		}
		numericIDs := deoVideoIDs(entries)
		list := []any{}
		ids := make([]string, 0, len(entries))
		for id := range entries {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			return strings.ToLower(entries[ids[i]].Name) < strings.ToLower(entries[ids[j]].Name)
		})
		for _, id := range ids {
			e := entries[id]
			m, ok := s.videoMetadata(id)
			if !ok {
				continue
			}
			list = append(list, map[string]any{"id": numericIDs[id], "title": e.Name, "videoLength": int(math.Ceil(m.Duration)), "video_url": base + prefix + "item/" + id + ".json", "thumbnailUrl": base + prefix + "thumbnail/" + id + ".png"})
		}
		if len(entries) > 0 && len(list) == 0 {
			http.Error(w, "视频信息读取失败，请查看播放器诊断日志", 503)
			return true
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]any{"scenes": []any{map[string]any{"name": "媒体库", "list": list}}, "authorized": "0"})
		return true
	}

	if strings.HasPrefix(route, "thumbnail/") {
		id := strings.TrimSuffix(strings.TrimPrefix(route, "thumbnail/"), ".png")
		e, ok := entries[id]
		if !ok {
			http.NotFound(w, r)
			return true
		}
		s.serveThumbnail(w, r, e.Path)
		return true
	}

	if strings.HasPrefix(route, "item/") {
		id := strings.TrimSuffix(strings.TrimPrefix(route, "item/"), ".json")
		e, ok := entries[id]
		if !ok {
			http.NotFound(w, r)
			return true
		}
		if !s.awaitMetadata(r.Context()) {
			http.Error(w, "视频信息读取中，请稍后重试", 503)
			return true
		}
		metadata, ready := s.videoMetadata(id)
		if !ready {
			http.Error(w, "无法读取真实视频信息", 503)
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		numericIDs := deoVideoIDs(entries)
		description := map[string]any{"id": numericIDs[id], "title": e.Name, "videoLength": int(math.Ceil(metadata.Duration)), "encodings": []any{map[string]any{"name": metadata.Codec, "videoSources": []any{map[string]any{"resolution": metadata.Height, "url": base + prefix + "file/" + id + "/" + url.PathEscape(filepath.Base(e.Path))}}}}, "thumbnailUrl": base + prefix + "thumbnail/" + id + ".png", "is3d": true}
		name := strings.ToLower(e.Name)
		if strings.Contains(name, "_sbs") || strings.Contains(name, "_lr_") {
			description["stereoMode"] = "sbs"
		}
		if strings.Contains(name, "_tb_") {
			description["stereoMode"] = "tb"
		}
		if strings.Contains(name, "_180") {
			description["screenType"] = "dome"
		} else if strings.Contains(name, "_360") {
			description["screenType"] = "sphere"
		} else if strings.Contains(name, "flat") {
			description["screenType"] = "flat"
			description["stereoMode"] = "off"
		}
		json.NewEncoder(w).Encode(description)
		return true
	}
	if strings.HasPrefix(route, "file/") {
		id := strings.Split(strings.TrimPrefix(route, "file/"), "/")[0]
		e, ok := entries[id]
		if !ok {
			http.NotFound(w, r)
			return true
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return true
		}
		s.recordVideoClient(r)
		http.ServeFile(w, r, e.Path)
		return true
	}
	return false
}

// Called only while the server mutex is held.
func (s *Server) catalogPrefix() string {
	if s.lan {
		return "/"
	}
	return "/" + s.token + "/"
}

func (s *Server) recordVideoClient(r *http.Request) {
	if r.Method != "GET" {
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() {
		return
	}
	s.mu.Lock()
	if s.lan {
		s.videoClient = ip.String()
	}
	s.mu.Unlock()
}
func (s *Server) VideoClient() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.videoClient }

func (s *Server) awaitMetadata(ctx context.Context) bool {
	s.mu.RLock()
	ready := s.metadataReady
	s.mu.RUnlock()
	if ready == nil {
		return true
	}
	select {
	case <-ready:
		return true
	case <-ctx.Done():
		return false
	}
}
func (s *Server) portal(w http.ResponseWriter, r *http.Request, base, prefix string, entries map[string]CatalogEntry) {
	type item struct{ Name, URL, Open string }
	items := []item{}
	for id, e := range entries {
		items = append(items, item{e.Name, base + prefix + "file/" + id + "/" + url.PathEscape(filepath.Base(e.Path)), "deovr://" + base + prefix + "item/" + id + ".json"})
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	page := template.Must(template.New("portal").Parse(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>SyncPlayer 媒体库</title><style>body{background:#10141d;color:#e2e8f2;font:18px system-ui;margin:0;padding:24px}main{max-width:1000px;margin:auto}h1{font-size:26px}a{color:#a9e6db;text-decoration:none}.open{display:inline-block;background:#2c4b48;padding:16px;border-radius:12px;margin-bottom:20px}.item{background:#1c2432;border-radius:12px;margin:10px 0;padding:18px;display:flex;gap:16px;justify-content:space-between;align-items:center}.title{overflow-wrap:anywhere}.action{flex-shrink:0;font-size:15px}small{display:block;color:#a6b2c6;margin-top:6px}</style><main><h1>媒体库</h1><a class="open" href="{{.Open}}">在 DeoVR 打开媒体库</a><a href="{{.Catalog}}"> JSON 接口</a>{{range .Items}}<div class="item"><a class="title" href="{{.URL}}">{{.Name}}</a><a class="action" href="{{.Open}}">DeoVR 播放</a></div>{{end}}</main></html>`))
	// Custom URI scheme is deliberately generated by this server, never from filenames.
	type viewItem struct {
		Name, URL string
		Open      template.URL
	}
	view := []viewItem{}
	for _, i := range items {
		view = append(view, viewItem{i.Name, i.URL, template.URL(i.Open)})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page.Execute(w, map[string]any{"Items": view, "Open": template.URL("deovr://" + base + prefix + "deovr"), "Catalog": base + prefix + "deovr"})
}
