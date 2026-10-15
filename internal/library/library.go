package library

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syncplayer/internal/diagnostics"
	"time"
)

type Entry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Folder    string `json:"folder"`
	Kind      string `json:"kind"`
	HasScript bool   `json:"has_script"`
}
type State struct {
	Directory   string  `json:"directory"`
	Items       []Entry `json:"items"`
	Skipped     int     `json:"skipped"`
	Scanning    bool    `json:"scanning"`
	Directories int     `json:"directories"`
	Found       int     `json:"found"`
}
type Store struct {
	mu         sync.Mutex
	state      State
	paths      map[string]string
	settings   string
	cancel     context.CancelFunc
	generation int
}

func New(settings string) *Store {
	s := &Store{settings: settings, state: State{Items: []Entry{}}, paths: map[string]string{}}
	if data, e := os.ReadFile(settings); e == nil {
		var saved struct {
			State
			Paths map[string]string `json:"paths"`
		}
		if json.Unmarshal(data, &saved) == nil {
			s.state = saved.State
			s.state.Scanning = false
			if s.state.Items == nil {
				s.state.Items = []Entry{}
			}
			if saved.Paths != nil {
				s.paths = saved.Paths
			}
		}
	}
	return s
}
func (s *Store) Snapshot() State { s.mu.Lock(); defer s.mu.Unlock(); return s.state }
func (s *Store) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}
func (s *Store) Path(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.paths[id]
	if !ok {
		return "", errors.New("媒体不在当前列表，请重新扫描")
	}
	return p, nil
}
func IsAudio(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".wav", ".flac", ".m4a", ".ogg", ".opus", ".aac", ".wma", ".aiff", ".aif":
		return true
	}
	return false
}
func IsMedia(path string) bool { return IsAudio(path) || IsVideo(path) }
func IsVideo(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".m2ts":
		return IsTransportStream(path)
	case ".mp4", ".mkv", ".webm", ".mov", ".avi", ".m4v", ".wmv":
		return true
	}
	return false
}

// TypeScript source shares the .ts suffix. Recognize repeated transport packet
// sync bytes (188-byte TS, 192-byte M2TS, or 204-byte error-corrected packets).
func IsTransportStream(path string) bool {
	f, e := os.Open(path)
	if e != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 4096)
	n, e := io.ReadFull(f, b)
	if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
		return false
	}
	b = b[:n]
	for _, stride := range []int{188, 192, 204} {
		for offset := 0; offset < 1024 && offset+stride*2 < len(b); offset++ {
			if b[offset] == 0x47 && b[offset+stride] == 0x47 && b[offset+stride*2] == 0x47 {
				return true
			}
		}
	}
	return false
}
func ScriptPath(video string) string {
	return strings.TrimSuffix(video, filepath.Ext(video)) + ".funscript"
}
func (s *Store) Scan(directory string) (State, error) {
	if directory == "" {
		return s.Snapshot(), nil
	}
	root, e := filepath.Abs(directory)
	if e != nil {
		return State{}, errors.New("目录无效")
	}
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() {
		return State{}, errors.New("目录无法访问")
	}
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.generation++
	generation := s.generation
	s.mu.Unlock()
	defer cancel()
	s.mu.Lock()
	if generation == s.generation {
		s.state.Scanning = true
		s.state.Directories = 0
		s.state.Found = 0
	}
	s.mu.Unlock()
	started := time.Now()
	defer func() {
		s.mu.Lock()
		if generation == s.generation {
			s.state.Scanning = false
			s.cancel = nil
		}
		s.mu.Unlock()
	}()
	result := State{Directory: root, Items: []Entry{}}
	paths := map[string]string{}
	type directoryResult struct {
		children []string
		items    []Entry
		paths    map[string]string
		err      error
	}
	jobs := make(chan string)
	const scanWorkers = 16
	completed := make(chan directoryResult, scanWorkers)
	// Only the coordinator enqueues children so workers cannot deadlock.
	for i := 0; i < scanWorkers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case dir, ok := <-jobs:
					if !ok {
						return
					}
					r := directoryResult{paths: map[string]string{}}
					entries, err := os.ReadDir(dir)
					r.err = err
					if err == nil {
						scripts := map[string]bool{}
						for _, d := range entries {
							if d.Type().IsRegular() && strings.EqualFold(filepath.Ext(d.Name()), ".funscript") {
								scripts[strings.ToLower(d.Name())] = true
							}
						}
						for _, d := range entries {
							if ctx.Err() != nil {
								return
							}
							if d.Type()&os.ModeSymlink != 0 {
								continue
							}
							path := filepath.Join(dir, d.Name())
							if d.IsDir() {
								r.children = append(r.children, path)
								continue
							}
							if !d.Type().IsRegular() || !IsMedia(path) {
								continue
							}
							folder, _ := filepath.Rel(root, dir)
							if folder == "." {
								folder = ""
							}
							hash := sha256.Sum256([]byte(strings.ToLower(path)))
							id := fmt.Sprintf("%x", hash[:16])
							kind := "video"
							if IsAudio(path) {
								kind = "audio"
							}
							script := strings.ToLower(filepath.Base(ScriptPath(path)))
							r.items = append(r.items, Entry{ID: id, Name: d.Name(), Folder: folder, Kind: kind, HasScript: scripts[script]})
							r.paths[id] = path
						}
					}
					select {
					case completed <- r:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	defer close(jobs)
	queue := []string{root}
	active := 0
	for len(queue) > 0 || active > 0 {
		var send chan string
		var next string
		if len(queue) > 0 {
			send = jobs
			next = queue[0]
		}
		select {
		case <-ctx.Done():
			return State{}, errors.New("扫描已取消")
		case send <- next:
			queue = queue[1:]
			active++
		case r := <-completed:
			active--
			result.Directories++
			if r.err != nil {
				result.Skipped++
			}
			queue = append(queue, r.children...)
			result.Items = append(result.Items, r.items...)
			for id, path := range r.paths {
				paths[id] = path
			}
			result.Found = len(result.Items)
			s.mu.Lock()
			if generation == s.generation {
				s.state.Directories = result.Directories
				s.state.Found = result.Found
			}
			s.mu.Unlock()
		}
	}
	diagnostics.Event("library.scan.metrics", map[string]any{"duration_ms": time.Since(started).Milliseconds(), "directories": result.Directories, "items": len(result.Items), "workers": scanWorkers, "skipped": result.Skipped})

	sort.Slice(result.Items, func(i, j int) bool {
		a, b := result.Items[i], result.Items[j]
		an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if an != bn {
			return an < bn
		}
		return strings.ToLower(a.Folder) < strings.ToLower(b.Folder)
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation || ctx.Err() != nil {
		return State{}, errors.New("扫描已取消")
	}
	s.state = result
	s.paths = paths
	s.cancel = nil
	data, _ := json.Marshal(struct {
		State
		Paths map[string]string `json:"paths"`
	}{result, paths})
	if e := os.MkdirAll(filepath.Dir(s.settings), 0700); e == nil {
		temp := s.settings + ".tmp"
		if os.WriteFile(temp, data, 0600) == nil {
			os.Rename(temp, s.settings)
		}
	}
	return result, nil
}

func (s *Store) UniqueName(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := ""
	for _, path := range s.paths {
		if strings.EqualFold(filepath.Base(path), name) {
			if found != "" {
				return ""
			}
			found = path
		}
	}
	return found
}
