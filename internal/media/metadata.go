package media

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syncplayer/internal/diagnostics"
	"syscall"
	"time"
)

type VideoMetadata struct {
	ID       string  `json:"id"`
	Codec    string  `json:"codec"`
	Height   int     `json:"height"`
	Width    int     `json:"width"`
	FPS      float64 `json:"fps"`
	Duration float64 `json:"duration"`
}

func parseMetadata(data []byte) (VideoMetadata, error) {
	var p struct {
		Streams []struct {
			Codec    string `json:"codec_name"`
			Height   int    `json:"height"`
			Width    int    `json:"width"`
			Rate     string `json:"r_frame_rate"`
			Duration string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return VideoMetadata{}, err
	}
	if len(p.Streams) == 0 {
		return VideoMetadata{}, errors.New("no video")
	}
	v := p.Streams[0]
	d, _ := strconv.ParseFloat(p.Format.Duration, 64)
	if d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		d, _ = strconv.ParseFloat(v.Duration, 64)
	}
	if v.Height <= 0 || v.Codec == "" || d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return VideoMetadata{}, errors.New("invalid metadata")
	}
	if v.Codec == "hevc" {
		v.Codec = "h265"
	}
	var fps float64
	if parts := strings.Split(v.Rate, "/"); len(parts) == 2 {
		n, _ := strconv.ParseFloat(parts[0], 64)
		den, _ := strconv.ParseFloat(parts[1], 64)
		if den > 0 {
			fps = n / den
		}
	}
	return VideoMetadata{Codec: v.Codec, Height: v.Height, Width: v.Width, FPS: fps, Duration: d}, nil
}
func (s *Server) videoMetadata(id string) (VideoMetadata, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.metadata[id]
	return m, ok && m.Height > 0 && m.Duration > 0 && m.Codec != ""
}
func (s *Server) probeCatalog(entries map[string]CatalogEntry) {
	s.mu.Lock()
	if s.metadataCancel != nil {
		s.metadataCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	s.metadataCancel = cancel
	s.metadataReady = ready
	s.metadata = map[string]VideoMetadata{}
	s.metadataGeneration++
	generation := s.metadataGeneration
	s.mu.Unlock()
	go func() {
		defer close(ready)
		defer cancel()
		jobs := make(chan string)
		var wg sync.WaitGroup
		count := 0
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := range jobs {
					pc, stop := context.WithTimeout(ctx, 15*time.Second)
					cmd := exec.CommandContext(pc, ToolPath("ffprobe"), "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name,width,height,r_frame_rate,duration:format=duration", "-of", "json", entries[id].Path)
					cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
					data, err := cmd.Output()
					stop()
					if err != nil {
						continue
					}
					m, err := parseMetadata(data)
					if err != nil {
						continue
					}
					m.ID = id
					s.mu.Lock()
					if generation == s.metadataGeneration && ctx.Err() == nil {
						s.metadata[id] = m
						count++
					}
					s.mu.Unlock()
				}
			}()
		}
		for id := range entries {
			select {
			case jobs <- id:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return
			}
		}
		close(jobs)
		wg.Wait()
		diagnostics.Event("media.metadata.complete", map[string]any{"total": len(entries), "ready": count, "failed": count != len(entries)})
	}()
}
