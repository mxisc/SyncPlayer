package subtitles

import (
	"context"
	"crypto/sha256"

	"encoding/json"
	"errors"
	"fmt"

	"os"

	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syncplayer/internal/diagnostics"

	"time"
)

type Cue struct {
	StartMS     int64  `json:"start_ms"`
	EndMS       int64  `json:"end_ms"`
	Original    string `json:"original"`
	Translation string `json:"translation"`
}
type State struct {
	Running     bool   `json:"running"`
	Message     string `json:"message"`
	ProcessedMS int64  `json:"processed_ms"`
	Cues        []Cue  `json:"cues"`
}
type cache struct {
	Cues   []Cue      `json:"cues"`
	Ranges [][2]int64 `json:"ranges"`
}
type Service struct {
	ai         *aiStore
	profile    storedProfile
	mu         sync.Mutex
	root       string
	path       string
	key        string
	language   string
	lowSpeech  bool
	position   int64
	start      int64
	generation uint64
	cancel     context.CancelFunc
	state      State
	cache      cache
	cachePath  string
}

func New(root string) *Service {
	return &Service{root: root, ai: newAIStore(root), state: State{Message: "未开启", Cues: []Cue{}}}
}
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.generation++
	s.state.Running = false
	s.state.Message = "已停止"
	s.key = ""
}
func (s *Service) Select(path string) {
	s.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path = path
	s.state = State{Message: "未开启", Cues: []Cue{}}
	s.cache = cache{}
}
func (s *Service) Start(key, language string, position int64, lowSpeech bool) error {
	if strings.TrimSpace(key) != "" {
		settings, err := s.ai.Snapshot()
		if err != nil {
			return err
		}
		for _, p := range settings.Profiles {
			if p.Provider == settings.Selected {
				if _, err = s.ai.Save(p, key); err != nil {
					return err
				}
				break
			}
		}
	}
	profile, err := s.ai.selected()
	if err != nil {
		return err
	}
	if language != "" && language != "ja" && language != "en" && language != "zh" {
		return errors.New("识别语言无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return errors.New("请先打开媒体")
	}
	if position < 0 {
		position = 0
	}
	if err := checkNativeRuntime(s.root); err != nil {
		return err
	}
	info, err := os.Stat(s.path)
	if err != nil {
		return errors.New("媒体文件无法读取")
	}
	fingerprintText := fmt.Sprintf("native-v1|%s|%d|%d|%s|small|%s|%s|%s|json=%t|low=%t", s.path, info.Size(), info.ModTime().UnixNano(), language, profile.Provider, profile.BaseURL, profile.Model, profile.JSONMode, lowSpeech)
	fingerprint := sha256.Sum256([]byte(fingerprintText))
	dir := filepath.Join(s.root, "build", "bin", "subtitle-cache")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("无法创建字幕缓存")
	}
	s.cachePath = filepath.Join(dir, fmt.Sprintf("%x.json", fingerprint))
	s.cache = cache{}
	if data, err := os.ReadFile(s.cachePath); err == nil {
		_ = json.Unmarshal(data, &s.cache)
	}
	s.key = profile.Key
	s.profile = profile
	s.language = language
	s.lowSpeech = lowSpeech
	s.position = position
	s.startJobLocked(position)
	return nil
}
func (s *Service) coveredLocked(position int64) bool {
	for _, r := range s.cache.Ranges {
		if position >= r[0] && position < r[1] {
			return true
		}
	}
	return false
}
func (s *Service) startJobLocked(position int64) {
	if s.cancel != nil {
		s.cancel()
	}
	s.generation++
	generation := s.generation
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	// Resume after cached contiguous audio to avoid charging twice.
	start := position
	for {
		next := start
		for _, r := range s.cache.Ranges {
			if start >= r[0] && start < r[1] && r[1] > next {
				next = r[1]
			}
		}
		if next == start {
			break
		}
		start = next
	}
	s.start = start
	s.state = State{Running: true, Message: "正在加载语音模型", ProcessedMS: start, Cues: append([]Cue{}, s.cache.Cues...)}
	path, profile, language, cachePath := s.path, s.profile, s.language, s.cachePath
	go s.run(ctx, generation, path, profile, language, cachePath, start, s.lowSpeech)
}
func (s *Service) Snapshot(position int64) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.position
	s.position = position
	if s.key != "" && (position < previous-30000 || position > previous+30000) && !s.coveredLocked(position) {
		s.startJobLocked(position)
	}
	state := s.state
	state.Cues = append([]Cue{}, s.state.Cues...)
	return state
}
func (s *Service) update(generation uint64, message string, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return
	}
	s.state.Message = message
	s.state.Running = running
}
func (s *Service) awaitBudget(ctx context.Context, end int64) bool {
	for {
		s.mu.Lock()
		position := s.position
		s.mu.Unlock()
		if end <= position+60000 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(250 * time.Millisecond):
		}
	}
}
func (s *Service) run(ctx context.Context, generation uint64, path string, profile storedProfile, language, cachePath string, start int64, lowSpeech bool) {
	diagnostics.Event("subtitle.job.begin", map[string]any{"generation": generation, "start_ms": start, "language": language, "provider": profile.Provider})
	defer diagnostics.Event("subtitle.job.end", map[string]any{"generation": generation})
	for offset := start; ctx.Err() == nil; offset += 29000 {
		if !s.awaitBudget(ctx, offset) {
			return
		}
		s.update(generation, "正在识别音频", true)
		event, err := recognizeChunk(ctx, s.root, path, language, offset, lowSpeech)
		if err != nil {
			s.update(generation, "本地识别失败（原生引擎）", false)
			diagnostics.Event("subtitle.recognition.error", map[string]any{"stage": "native"})
			return
		}
		if ctx.Err() != nil {
			return
		}
		switch event.Type {
		case "status":
			s.update(generation, event.Message, true)
		case "error":
			diagnostics.Event("subtitle.recognition.error", map[string]any{"code": event.Code})
			message := "本地识别失败：" + event.Code
			if event.Code == "no_audio" {
				message = "媒体没有音轨"
			}
			s.update(generation, message, false)
			return
		case "chunk":
			diagnostics.Event("subtitle.recognition.chunk", map[string]any{"start_ms": event.StartMS, "end_ms": event.EndMS, "segments": len(event.Cues), "input_seconds": event.InputSeconds, "speech_seconds": event.SpeechSeconds, "rms": event.RMS, "low_speech": event.LowSpeech})
			if !s.awaitBudget(ctx, event.StartMS) {
				return
			}
			s.mu.Lock()
			cached := s.coveredLocked(event.StartMS) && s.coveredLocked(event.EndMS-1)
			s.mu.Unlock()
			if !cached && len(event.Cues) > 0 {
				s.update(generation, "正在翻译字幕", true)
				if err := translate(ctx, profile, event.Cues); err != nil {
					s.update(generation, err.Error(), false)
					return
				}
			}
			s.mu.Lock()
			if s.generation != generation {
				s.mu.Unlock()
				return
			}
			if !cached {
				for _, cue := range event.Cues {
					duplicate := false
					for _, old := range s.cache.Cues {
						if cue.StartMS < old.EndMS && cue.EndMS > old.StartMS && (cue.Original == old.Original || cue.StartMS < event.StartMS+1000) {
							duplicate = true
							break
						}
					}
					if !duplicate {
						s.cache.Cues = append(s.cache.Cues, cue)
					}
				}
				s.cache.Ranges = append(s.cache.Ranges, [2]int64{event.StartMS, event.EndMS})
				sort.Slice(s.cache.Cues, func(i, j int) bool { return s.cache.Cues[i].StartMS < s.cache.Cues[j].StartMS })
			}
			s.state.Cues = append([]Cue{}, s.cache.Cues...)
			s.state.ProcessedMS = event.EndMS
			s.state.Message = "字幕已就绪"
			data, _ := json.Marshal(s.cache)
			// Hold the generation lock through the atomic cache replacement.
			if err := os.WriteFile(cachePath+".tmp", data, 0600); err == nil {
				if err = os.Rename(cachePath+".tmp", cachePath); err != nil {
					diagnostics.Event("subtitle.cache.error", map[string]any{"operation": "replace"})
				}
			}
			s.mu.Unlock()
			if event.EndMS-event.StartMS < 30000 {
				s.update(generation, "字幕已生成", false)
				return
			}
			if !s.awaitBudget(ctx, event.EndMS) {
				return
			}

		case "done":
			s.update(generation, "字幕已生成", false)
			return
		}
	}
	if ctx.Err() == nil {
		s.update(generation, "识别进程意外结束", false)
	}
}
