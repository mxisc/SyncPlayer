package subtitles

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Only public metadata crosses the Wails bridge. Credentials remain in Go.
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

func Providers() []Provider {
	return []Provider{
		{"deepseek", "DeepSeek", "https://api.deepseek.com/v1"},
		{"openai", "OpenAI", "https://api.openai.com/v1"},
		{"gemini", "Google Gemini", "https://generativelanguage.googleapis.com/v1beta/openai"},
		{"qwen", "通义千问", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"kimi", "Kimi / Moonshot", "https://api.moonshot.cn/v1"},
		{"glm", "智谱 GLM", "https://open.bigmodel.cn/api/paas/v4"},
		{"doubao", "豆包 / 火山方舟", "https://ark.cn-beijing.volces.com/api/v3"},
		{"grok", "xAI Grok", "https://api.x.ai/v1"},
		{"siliconflow", "硅基流动", "https://api.siliconflow.cn/v1"},
		{"openrouter", "OpenRouter", "https://openrouter.ai/api/v1"},
		{"custom", "自定义 API", ""},
	}
}

type AIProfile struct {
	Provider      string `json:"provider"`
	BaseURL       string `json:"base_url"`
	Model         string `json:"model"`
	JSONMode      bool   `json:"json_mode"`
	HasKey        bool   `json:"has_key"`
	ModelSelected bool   `json:"model_selected"`
}
type AISettings struct {
	Selected  string      `json:"selected"`
	Providers []Provider  `json:"providers"`
	Profiles  []AIProfile `json:"profiles"`
}
type storedProfile struct {
	AIProfile
	Key string `json:"key,omitempty"`
}
type storedSettings struct {
	Selected string                   `json:"selected"`
	Profiles map[string]storedProfile `json:"profiles"`
}
type aiStore struct {
	mu      sync.Mutex
	path    string
	legacy  string
	data    storedSettings
	loadErr error
}

func newAIStore(root string) *aiStore {
	s := &aiStore{path: filepath.Join(root, "build", "bin", "settings", "ai-providers.dat"), legacy: filepath.Join(root, "build", "bin", "settings", "deepseek-key.dat"), data: storedSettings{Selected: "deepseek", Profiles: map[string]storedProfile{}}}
	if _, err := os.Stat(s.path); err == nil {
		data, err := readProtected(s.path)
		if err != nil || json.Unmarshal(data, &s.data) != nil || s.data.Profiles == nil {
			s.loadErr = errors.New("AI 配置无法读取，请恢复设置文件")
			return s
		}
	} else if !os.IsNotExist(err) {
		s.loadErr = errors.New("AI 配置无法访问")
		return s
	} else if data, err := readProtected(s.legacy); err == nil {
		s.data.Profiles["deepseek"] = storedProfile{Key: strings.TrimSpace(string(data))}
	}
	for _, p := range Providers() {
		profile, ok := s.data.Profiles[p.ID]
		if !ok || profile.Provider == "" {
			key := profile.Key
			profile = storedProfile{AIProfile: AIProfile{Provider: p.ID, BaseURL: p.BaseURL, JSONMode: p.ID == "deepseek" || p.ID == "openai"}, Key: key}
		}
		s.data.Profiles[p.ID] = profile
	}
	if _, ok := s.data.Profiles[s.data.Selected]; !ok {
		s.data.Selected = "deepseek"
	}
	return s
}
func (s *aiStore) snapshotLocked() AISettings {
	r := AISettings{Selected: s.data.Selected, Providers: Providers(), Profiles: []AIProfile{}}
	for _, p := range r.Providers {
		profile := s.data.Profiles[p.ID]
		public := profile.AIProfile
		public.HasKey = profile.Key != ""
		r.Profiles = append(r.Profiles, public)
	}
	return r
}
func (s *aiStore) Snapshot() (AISettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(), s.loadErr
}
func normalizeBase(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("API 地址须为 HTTP 或 HTTPS 地址，不含 Key 或查询参数")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.Path = strings.TrimSuffix(u.Path, "/chat/completions")
	if u.Path == "" {
		u.Path = "/v1"
	}
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}
func (s *aiStore) Save(profile AIProfile, key string) (AISettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return AISettings{}, s.loadErr
	}
	old, ok := s.data.Profiles[profile.Provider]
	if !ok {
		return AISettings{}, errors.New("未知 API 服务商")
	}
	base, err := normalizeBase(profile.BaseURL)
	if err != nil {
		return AISettings{}, err
	}
	if profile.Provider != "custom" {
		for _, preset := range Providers() {
			if preset.ID == profile.Provider {
				base = preset.BaseURL
				break
			}
		}
	}
	profile.BaseURL = base
	profile.JSONMode = profile.Provider == "deepseek" || profile.Provider == "openai"
	profile.Model = strings.TrimSpace(profile.Model)
	profile.ModelSelected = profile.Model != ""
	if len(profile.Model) > 256 {
		return AISettings{}, errors.New("模型名称过长")
	}
	key = strings.TrimSpace(key)
	if len(key) > 4096 || strings.ContainsAny(key, "\r\n") {
		return AISettings{}, errors.New("API Key 无效")
	}
	if key == "" {
		oldBase, _ := normalizeBase(old.BaseURL)
		if old.Key != "" && oldBase != base {
			return AISettings{}, errors.New("更换 API 地址后，请重新填写对应 Key")
		}
		key = old.Key
	}
	profile.HasKey = false
	next := storedSettings{Selected: profile.Provider, Profiles: map[string]storedProfile{}}
	for id, p := range s.data.Profiles {
		next.Profiles[id] = p
	}
	next.Profiles[profile.Provider] = storedProfile{AIProfile: profile, Key: key}
	data, err := json.Marshal(next)
	if err != nil {
		return AISettings{}, errors.New("AI 配置保存失败")
	}
	if err = writeProtected(s.path, data); err != nil {
		return AISettings{}, err
	}
	s.data = next
	return s.snapshotLocked(), nil
}
func (s *aiStore) DeleteKey(id string) (AISettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return AISettings{}, s.loadErr
	}
	profile, ok := s.data.Profiles[id]
	if !ok {
		return AISettings{}, errors.New("未知 API 服务商")
	}
	previous := s.data.Profiles[id]
	profile.Key = ""
	s.data.Profiles[id] = profile
	data, _ := json.Marshal(s.data)
	if err := writeProtected(s.path, data); err != nil {
		s.data.Profiles[id] = previous
		return AISettings{}, err
	}
	if id == "deepseek" {
		if err := os.Remove(s.legacy); err != nil && !os.IsNotExist(err) {
			return AISettings{}, errors.New("旧版 Key 删除失败")
		}
	}
	return s.snapshotLocked(), nil
}
func (s *aiStore) selected() (storedProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return storedProfile{}, s.loadErr
	}
	p := s.data.Profiles[s.data.Selected]
	if p.Key == "" {
		return storedProfile{}, errors.New("请填写并保存当前服务商的 API Key")
	}
	base, err := normalizeBase(p.BaseURL)
	if err != nil {
		return storedProfile{}, err
	}
	p.BaseURL = base
	if p.Model == "" || !p.ModelSelected {
		return storedProfile{}, errors.New("请先获取并选择模型")
	}
	return p, nil
}
func (s *Service) AISettings() (AISettings, error) { return s.ai.Snapshot() }
func (s *Service) SaveAIProfile(profile AIProfile, key string) (AISettings, error) {
	return s.ai.Save(profile, key)
}
func (s *Service) DeleteAIKey(provider string) (AISettings, error) { return s.ai.DeleteKey(provider) }
