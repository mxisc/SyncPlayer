// Package handy implements the documented Handy v2 HSSP cloud transport.
package handy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syncplayer/internal/diagnostics"
	"syncplayer/internal/script"
	"time"
)

type Client struct {
	mu                          sync.Mutex
	http                        *http.Client
	base, upload, key, prepared string
	connected, playing          bool
	message                     string
	offset, anchor              int64
	stamp, heartbeat            time.Time
	closed                      chan struct{}
	cachedActions               []script.Action
	cachedRate                  float64
	cachedData                  []byte
	cachedHash                  string
}
type State struct {
	Connected bool   `json:"connected"`
	Message   string `json:"message"`
}

func New() *Client {
	c := &Client{http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: "https://www.handyfeeling.com/api/handy/v2", upload: "https://scripts01.handyfeeling.com/api/script/v0/temp/upload", closed: make(chan struct{}), message: "Handy 未连接"}
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-c.closed:
				return
			case <-t.C:
				c.mu.Lock()
				if c.playing && time.Since(c.heartbeat) > 1500*time.Millisecond {
					_ = c.stop()
					c.message = "播放状态超时，已停止同步"
				}
				c.mu.Unlock()
			}
		}
	}()
	return c
}
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return State{c.connected, c.message}
}

// Never surface response bodies or transport errors containing private URLs/keys.
func (c *Client) request(method, path string, body any, out any) error {
	var data io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		data = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, method, c.base+path, data)
	if e != nil {
		return errors.New("Handy 请求地址无效")
	}
	req.Header.Set("X-Connection-Key", c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return errors.New("Handy 服务请求失败，请检查网络")
	}
	defer resp.Body.Close()
	diagnostics.Event("handy.request", map[string]any{"operation": strings.Split(path, "?")[0], "status": resp.StatusCode})
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if e != nil {
		return errors.New("Handy 响应读取失败")
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Handy 服务返回 HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Error  *struct{ Code int } `json:"error"`
		Result *int                `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return errors.New("Handy 服务返回了无效 JSON")
	}
	if envelope.Error != nil {
		return fmt.Errorf("Handy 服务错误 %d", envelope.Error.Code)
	}
	if envelope.Result != nil && *envelope.Result < 0 {
		return errors.New("Handy 未接受同步指令")
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func (c *Client) Connect(key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.stop(); e != nil {
		return e
	}
	c.connected = false
	c.prepared = ""
	c.key = ""
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return errors.New("请输入有效的 Handy key")
	}
	c.key = key
	var online struct{ Connected bool }
	if e := c.request("GET", "/connected", nil, &online); e != nil {
		c.key = ""
		return e
	}
	if !online.Connected {
		c.key = ""
		return errors.New("Handy 不在线，请检查 Wi-Fi 和 key")
	}
	var best time.Duration = 1<<63 - 1
	for i := 0; i < 5; i++ {
		start := time.Now()
		var clock struct{ ServerTime int64 }
		if e := c.request("GET", "/servertime", nil, &clock); e != nil {
			c.key = ""
			return e
		}
		rtt := time.Since(start)
		if clock.ServerTime <= 0 {
			c.key = ""
			return errors.New("Handy 时钟响应无效")
		}
		if rtt < best {
			best = rtt
			c.offset = clock.ServerTime + int64(rtt/time.Millisecond)/2 - time.Now().UnixMilli()
		}
	}
	if e := c.request("GET", "/hstp/sync?syncCount=10&outliers=2", nil, nil); e != nil {
		c.key = ""
		return e
	}
	c.connected = true
	c.message = "Handy 已通过互联网连接"
	return nil
}
func (c *Client) stop() error {
	if !c.playing {
		return nil
	}
	// Keep playing=true on failure so the watchdog retries the stop operation.
	if e := c.request("PUT", "/hssp/stop", nil, nil); e != nil {
		return e
	}
	c.playing = false
	return nil
}
func (c *Client) Stop() error { c.mu.Lock(); defer c.mu.Unlock(); return c.stop() }
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.stop(); e != nil {
		return e
	}
	c.key = ""
	c.connected = false
	c.prepared = ""
	c.message = "Handy 已断开"
	return nil
}
func (c *Client) Close() { _ = c.Disconnect(); close(c.closed) }
func csv(s script.Script, rate float64) ([]byte, error) {
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) || len(s.Actions) < 2 {
		return nil, errors.New("Handy 需要有效速度和至少两个脚本动作")
	}
	var b strings.Builder
	b.WriteString("# SyncPlayer\n")
	last := int64(-1)
	for _, a := range s.Actions {
		at := int64(math.Round(float64(a.At) / rate))
		if at <= last {
			continue
		}
		fmt.Fprintf(&b, "%d,%d\n", at, a.Pos)
		last = at
	}
	if b.Len() > 524288 {
		return nil, errors.New("Handy 脚本超过 512 KiB 限制")
	}
	return []byte(b.String()), nil
}
func (c *Client) host(data []byte) (string, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	f, e := w.CreateFormFile("file", "sync.csv")
	if e != nil {
		return "", e
	}
	f.Write(data)
	w.Close()
	req, e := http.NewRequest("POST", c.upload, &b)
	if e != nil {
		return "", errors.New("脚本上传地址无效")
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, e := c.http.Do(req)
	if e != nil {
		return "", errors.New("Handy 临时脚本上传失败")
	}
	defer resp.Body.Close()
	var result struct {
		URL string `json:"url"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil {
		return "", errors.New("Handy 临时脚本上传失败")
	}
	u, e := url.Parse(result.URL)
	if e != nil || u.Scheme != "https" || !(u.Hostname() == "handyfeeling.com" || strings.HasSuffix(u.Hostname(), ".handyfeeling.com")) {
		return "", errors.New("脚本托管地址校验失败")
	}
	return result.URL, nil
}
func (c *Client) Tick(s script.Script, ms int64, rate float64, enabled bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !enabled {
		return c.stop()
	}
	if !c.connected {
		return errors.New("Handy 未连接")
	}
	c.heartbeat = time.Now()
	if len(s.Actions) < 2 {
		return errors.New("Handy 需要至少两个脚本动作")
	}
	if len(c.cachedActions) != len(s.Actions) || len(c.cachedActions) == 0 || &c.cachedActions[0] != &s.Actions[0] || c.cachedRate != rate {
		data, e := csv(s, rate)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		c.cachedActions = s.Actions
		c.cachedRate = rate
		c.cachedData = data
		c.cachedHash = hex.EncodeToString(sum[:])
	}
	data, hash := c.cachedData, c.cachedHash
	var e error
	if hash != c.prepared {
		if e = c.stop(); e != nil {
			return e
		}
		link, e := c.host(data)
		if e != nil {
			return e
		}
		if e = c.request("PUT", "/mode", map[string]int{"mode": 1}, nil); e != nil {
			return e
		}
		if e = c.request("PUT", "/hssp/setup", map[string]string{"url": link, "sha256": hash}, nil); e != nil {
			return e
		}
		c.prepared = hash
		c.message = "Handy 脚本已准备，等待最新播放进度"
		return nil
	}
	position := int64(math.Round(float64(ms) / rate))
	expected := c.anchor + time.Since(c.stamp).Milliseconds()
	if !c.playing || math.Abs(float64(position-expected)) > 250 {
		now := time.Now()
		if e = c.request("PUT", "/hssp/play", map[string]int64{"startTime": position, "estimatedServerTime": now.UnixMilli() + c.offset}, nil); e != nil {
			// A lost response does not prove the device rejected the command.
			c.playing = true
			return e
		}
		c.anchor = position
		c.stamp = now
		c.playing = true
		c.message = "Handy 正在同步"
	}
	return nil
}
