package media

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syncplayer/internal/diagnostics"
	"syscall"
	"time"
)

func localCover(video string) string {
	stem := strings.TrimSuffix(video, filepath.Ext(video))
	candidates := []string{}
	for _, ext := range []string{".jpg", ".jpeg", ".png"} {
		candidates = append(candidates, stem+ext)
	}
	for _, name := range []string{"cover", "folder"} {
		for _, ext := range []string{".jpg", ".jpeg", ".png"} {
			candidates = append(candidates, filepath.Join(filepath.Dir(video), name+ext))
		}
	}
	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		_, _, err = image.DecodeConfig(f)
		f.Close()
		if err == nil {
			return path
		}
	}
	return ""
}

func validCachedThumbnail(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = png.DecodeConfig(f)
	return err == nil
}

// Publish a complete image beside the video without overwriting an existing file.
func saveCoverImage(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".syncplayer-cover-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(tmp, path); os.IsExist(err) && validCachedThumbnail(path) {
		return nil
	}
	return err
}

func (s *Server) serveThumbnail(w http.ResponseWriter, r *http.Request, video string) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	if cover := localCover(video); cover != "" {
		w.Header().Set("Cache-Control", "private, max-age=600")
		http.ServeFile(w, r, cover)
		return
	}
	cachePath := strings.TrimSuffix(video, filepath.Ext(video)) + ".png"
	if !validCachedThumbnail(cachePath) {
		// Deduplicate each video and limit extraction to two single-threaded processes.
		s.thumbnailOnce.Do(func() { s.thumbnailSlots = make(chan struct{}, 2) })
		result := s.thumbnailGroup.DoChan(cachePath, func() (any, error) {
			s.thumbnailSlots <- struct{}{}
			defer func() { <-s.thumbnailSlots }()
			if validCachedThumbnail(cachePath) {
				return nil, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, ToolPath("ffmpeg"), "-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-i", video, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale=480:-2", "-filter_threads", "1", "-threads", "1", "-f", "image2pipe", "-vcodec", "png", "pipe:1")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			data, err := cmd.Output()
			if err == nil {
				_, err = png.DecodeConfig(bytes.NewReader(data))
			}
			if err != nil {
				diagnostics.Event("media.thumbnail.error", map[string]any{"failed": true, "stage": "extract", "timeout": ctx.Err() != nil})
				return nil, err
			}
			if err = saveCoverImage(cachePath, data); err != nil {
				return nil, err
			}
			return nil, nil
		})
		select {
		case <-r.Context().Done():
			return
		case ready := <-result:
			if ready.Err != nil {
				http.Error(w, "无法读取视频缩略图", 503)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=600")
	http.ServeFile(w, r, cachePath)
}
