package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"syncplayer/internal/diagnostics"
	"time"
)

func (s *Service) FetchAIModels(provider string) ([]string, error) {
	s.ai.mu.Lock()
	if s.ai.loadErr != nil {
		err := s.ai.loadErr
		s.ai.mu.Unlock()
		return nil, err
	}
	profile, ok := s.ai.data.Profiles[provider]
	s.ai.mu.Unlock()
	if !ok || profile.Key == "" {
		return nil, errors.New("请先保存当前服务商的 API Key")
	}
	base, err := normalizeBase(profile.BaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := base + "/models"
	seen := map[string]bool{}
	for page := 0; page < 50; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, errors.New("模型列表请求创建失败")
		}
		req.Header.Set("Authorization", "Bearer "+profile.Key)
		req.Header.Set("Accept", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return nil, errors.New("获取模型失败，请检查 API 地址和网络")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		response.Body.Close()
		diagnostics.Event("ai.models.result", map[string]any{"provider": provider, "status": response.StatusCode, "page": page + 1})
		switch response.StatusCode {
		case 401:
			return nil, errors.New("API Key 无效，无法获取模型")
		case 403:
			return nil, errors.New("账号无权获取模型列表")
		case 404, 405:
			return nil, errors.New("该 API 未提供 OpenAI 模型列表接口")
		case 429:
			return nil, errors.New("模型列表请求过于频繁，请稍后重试")
		}
		if readErr != nil || response.StatusCode != 200 {
			return nil, fmt.Errorf("获取模型失败（HTTP %d）", response.StatusCode)
		}
		var list struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if json.Unmarshal(data, &list) != nil || list.Data == nil {
			return nil, errors.New("模型列表不是 OpenAI 格式")
		}
		added := 0
		for _, model := range list.Data {
			id := strings.TrimSpace(model.ID)
			if id != "" && len(id) <= 256 && !seen[id] {
				seen[id] = true
				added++
			}
		}
		if !list.HasMore {
			models := make([]string, 0, len(seen))
			for id := range seen {
				models = append(models, id)
			}
			sort.Strings(models)
			if len(models) == 0 {
				return nil, errors.New("该 API 未返回可选模型")
			}
			diagnostics.Event("ai.models.loaded", map[string]any{"provider": provider, "count": len(models)})
			return models, nil
		}
		if added == 0 || len(list.Data) == 0 {
			return nil, errors.New("模型列表分页异常，请重试")
		}
		cursor := list.LastID
		if cursor == "" {
			cursor = list.Data[len(list.Data)-1].ID
		}
		u, _ := url.Parse(base + "/models")
		q := u.Query()
		q.Set("after", cursor)
		u.RawQuery = q.Encode()
		endpoint = u.String()
	}
	return nil, errors.New("模型列表分页过多，获取未完成")
}
