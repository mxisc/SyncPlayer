package deovr

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"syncplayer/internal/diagnostics"
	"syncplayer/internal/player"
	"time"
)

const staleAfter = 2500 * time.Millisecond

type State struct {
	Fullscreen bool `json:"fullscreen"`
	player.State
	Connected   bool    `json:"connected"`
	Name        string  `json:"name"`
	Source      string  `json:"source"`
	Rate        float64 `json:"rate"`
	AgeMS       int64   `json:"age_ms"`
	SyncEnabled bool    `json:"sync_enabled"`
}
type packet struct {
	Path     *string  `json:"path"`
	Duration *float64 `json:"duration"`
	Time     *float64 `json:"currentTime"`
	Rate     *float64 `json:"playbackSpeed"`
	Status   *int     `json:"playerState"`
}
type Client struct {
	mu             sync.Mutex
	write          sync.Mutex
	life           sync.Mutex
	conn           net.Conn
	state          State
	path           string
	stamp          time.Time
	lastPacket     time.Time
	pending        bool
	target         *float64
	desired        *int
	loadPath       string
	loadName       string
	loadFile       string
	loadStarted    time.Time
	lastDiagnostic time.Time
	onStop         func(string)
}

func New(onStop func(string)) *Client {
	return &Client{state: State{State: player.State{Paused: true}, Rate: 1}, onStop: onStop}
}
func (c *Client) Connect(host string) error {
	c.life.Lock()
	defer c.life.Unlock()
	c.disconnect("已断开 DeoVR")
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, "23554"), 3*time.Second)
	if err != nil {
		return errors.New("无法连接 DeoVR，请开启 Enable remote control 并打开一个视频")
	}
	c.mu.Lock()
	c.conn = conn
	c.path = ""
	c.stamp = time.Time{}
	c.lastPacket = time.Now()
	c.pending = false
	c.target = nil
	c.desired = nil
	c.loadPath = ""
	c.loadName = ""
	c.loadStarted = time.Time{}
	c.lastDiagnostic = time.Time{}
	c.state = State{State: player.State{Paused: true, Message: "已连接 DeoVR，等待播放状态"}, Connected: true, Rate: 1}
	c.mu.Unlock()
	diagnostics.Event("deovr.connected", nil)
	go c.read(conn)
	go c.heartbeat(conn)
	return nil
}
func (c *Client) Disconnect() { c.life.Lock(); defer c.life.Unlock(); c.disconnect("DeoVR 已断开") }

// LocalFile resolves the currently reported PC file without exposing its path to the UI.
func (c *Client) LocalFile(source string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || source == "" || c.state.Source != source {
		return ""
	}
	if c.path == c.loadPath {
		return c.loadFile
	}
	p := c.path
	if strings.HasPrefix(strings.ToLower(p), "file:") {
		u, err := url.Parse(p)
		if err != nil {
			return ""
		}
		p = u.Path
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			p = "//" + u.Host + p
		}
		if len(p) > 2 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
	}
	if strings.Contains(p, "://") || !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}
func (c *Client) IsLocal() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return false
	}
	address, ok := c.conn.RemoteAddr().(*net.TCPAddr)
	return ok && address.IP.IsLoopback()
}
func (c *Client) disconnect(message string) {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.state.Connected = false
	c.state.Paused = true
	c.state.Loaded = false
	c.state.Message = message
	c.pending = false
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
		diagnostics.Event("deovr.disconnected", map[string]any{"reason": message})
	}
	if c.onStop != nil {
		c.onStop("disconnect")
	}
}
func (c *Client) fail(conn net.Conn, reason string) {
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	c.conn = nil
	c.state.Connected = false
	c.state.Paused = true
	c.state.Loaded = false
	c.state.Message = reason
	c.pending = false
	c.mu.Unlock()
	conn.Close()
	diagnostics.Event("deovr.disconnected", map[string]any{"reason": reason})
	if c.onStop != nil {
		c.onStop("disconnect")
	}
}
func (c *Client) sendTo(conn net.Conn, data []byte) error {
	c.write.Lock()
	defer c.write.Unlock()
	frame := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	for len(frame) > 0 {
		n, e := conn.Write(frame)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
func (c *Client) Send(value any) error {
	data, e := json.Marshal(value)
	if e != nil {
		return e
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("DeoVR 未连接")
	}
	if e = c.sendTo(conn, data); e != nil {
		c.fail(conn, "DeoVR 指令发送失败")
		return errors.New("DeoVR 指令发送失败")
	}
	return nil
}
func (c *Client) heartbeat(conn net.Conn) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		current := c.conn == conn
		stamp := c.lastPacket
		c.mu.Unlock()
		if !current {
			return
		}
		if !stamp.IsZero() && time.Since(stamp) > staleAfter {
			c.fail(conn, "DeoVR 播放状态超时，请重新连接")
			return
		}
		if e := c.sendTo(conn, nil); e != nil {
			c.fail(conn, "DeoVR 心跳发送失败")
			return
		}
	}
}
func valid(value *float64) bool {
	return value == nil || (!math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0)
}
func (c *Client) read(conn net.Conn) {
	for {
		conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		var header [4]byte
		if _, e := io.ReadFull(conn, header[:]); e != nil {
			c.fail(conn, "DeoVR 连接中断或状态超时")
			return
		}
		size := binary.LittleEndian.Uint32(header[:])
		if size > 1<<20 {
			c.fail(conn, "DeoVR 数据长度异常")
			return
		}
		if size == 0 {
			c.update(conn, nil)
			continue
		}
		body := make([]byte, int(size))
		if _, e := io.ReadFull(conn, body); e != nil {
			c.fail(conn, "DeoVR 数据接收中断")
			return
		}
		var data packet
		if json.Unmarshal(body, &data) != nil || !valid(data.Time) || !valid(data.Duration) || !valid(data.Rate) || (data.Status != nil && *data.Status != 0 && *data.Status != 1) {
			c.fail(conn, "DeoVR 播放数据无效")
			return
		}
		c.update(conn, &data)
	}
}
func (c *Client) update(conn net.Conn, data *packet) {
	now := time.Now()
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	c.lastPacket = now
	c.mu.Unlock()
	// Official zero-length packets are keepalives, not a video-close event.
	if data == nil {
		return
	}
	reason := ""
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return
	}
	previous := c.state
	expected := c.state.Time
	if !c.stamp.IsZero() && !c.state.Paused {
		expected += now.Sub(c.stamp).Seconds() * c.state.Rate
	}
	if data == nil || (data.Path != nil && *data.Path == "") {
		if c.state.Loaded {
			reason = "video.changed"
		}
		c.path = ""
		c.state.Loaded = false
		c.state.Paused = true
		c.state.Name = ""
		c.state.Source = ""
		c.state.Time = 0
		c.state.Duration = 0
		c.state.Message = "DeoVR 中尚未打开视频"
	} else {
		if data.Path != nil && *data.Path != c.path {
			c.path = *data.Path
			c.state.Name = filepath.Base(strings.ReplaceAll(c.path, "\\", "/"))
			if c.path == c.loadPath {
				c.state.Name = c.loadName
			}
			hash := sha256.Sum256([]byte(c.path))
			c.state.Source = fmt.Sprintf("deovr-%x", hash[:8])
			c.state.Time = 0
			c.state.Duration = 0
			c.state.Paused = true
			reason = "video.changed"
		}
		if data.Time != nil {
			if previous.Loaded && math.Abs(*data.Time-expected) > 0.5 && reason == "" {
				reason = "seek"
			}
			c.state.Time = *data.Time
		}
		if data.Duration != nil {
			c.state.Duration = *data.Duration
		}
		if data.Rate != nil {
			if previous.Loaded && math.Abs(c.state.Rate-*data.Rate) > 0.001 && reason == "" {
				reason = "speed.changed"
			}
			c.state.Rate = *data.Rate
		}
		if data.Status != nil {
			c.state.Paused = *data.Status != 0
		}
		c.state.Loaded = c.path != "" && c.state.Duration > 0
		c.state.Message = ""
		if !c.loadStarted.IsZero() {
			if c.state.Loaded && c.path == c.loadPath {
				diagnostics.Event("deovr.load.confirmed", map[string]any{"duration_ms": math.Round(c.state.Duration * 1000), "elapsed_ms": now.Sub(c.loadStarted).Milliseconds()})
				c.loadStarted = time.Time{}
			} else if now.Sub(c.loadStarted) > 12*time.Second {
				c.state.Message = "DeoVR 未能打开视频，请查看头显中的加载或解码错误"
			}
		}
		if !previous.Paused && c.state.Paused && reason == "" {
			reason = "pause"
		}
	}
	if data == nil || data.Time != nil {
		c.stamp = now
	}
	if data == nil {
		c.target = nil
		c.desired = nil
		c.pending = false
	} else {
		if c.target != nil && data.Time != nil && math.Abs(*data.Time-*c.target) < 0.5 {
			c.target = nil
		}
		if c.desired != nil && data.Status != nil && *data.Status == *c.desired {
			c.desired = nil
		}
		c.pending = c.target != nil || c.desired != nil
	}
	snapshot := c.state
	if data != nil && now.Sub(c.lastDiagnostic) >= 5*time.Second {
		c.lastDiagnostic = now
		diagnostics.Event("deovr.state.received", map[string]any{"has_path": c.path != "", "requested_path": c.path == c.loadPath, "has_time": data.Time != nil, "has_duration": data.Duration != nil, "time_ms": math.Round(snapshot.Time * 1000), "duration_ms": math.Round(snapshot.Duration * 1000), "loaded": snapshot.Loaded, "paused": snapshot.Paused, "pending": c.pending})
	}
	c.mu.Unlock()
	if reason != "" {
		diagnostics.Event("deovr."+reason, map[string]any{"time_ms": math.Round(snapshot.Time * 1000), "paused": snapshot.Paused, "rate": snapshot.Rate})
		if c.onStop != nil {
			c.onStop(reason)
		}
	}
}
func (c *Client) Snapshot() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	if !c.loadStarted.IsZero() && !s.Loaded && time.Since(c.loadStarted) > 12*time.Second {
		s.Message = "DeoVR 未能打开视频，请查看头显中的加载或解码错误"
	}
	s.Seeking = c.pending
	if !c.stamp.IsZero() {
		age := time.Since(c.stamp)
		s.AgeMS = age.Milliseconds()
		if age > staleAfter {
			s.Paused = true
			s.Loaded = false
			s.Message = "DeoVR 播放状态已过期"
		} else if s.Loaded && !s.Paused && !c.pending {
			s.Time += age.Seconds() * s.Rate
		}
	}
	if s.Rate <= 0 {
		s.Paused = true
	}
	if s.Duration > 0 && s.Time >= s.Duration {
		s.Ended = true
		s.Time = s.Duration
		s.Paused = true
	}
	return s
}
func (c *Client) Pause(paused bool) error {
	state := 0
	if paused {
		state = 1
	}
	c.mu.Lock()
	c.desired = &state
	c.pending = true
	c.mu.Unlock()
	if e := c.Send(map[string]any{"playerState": state}); e != nil {
		return e
	}
	return c.await()
}
func (c *Client) Seek(seconds float64) error {
	s := c.Snapshot()
	if s.Duration > 0 {
		seconds = math.Min(seconds, math.Max(0, s.Duration-0.05))
	}
	c.mu.Lock()
	c.target = &seconds
	c.pending = true
	c.mu.Unlock()
	diagnostics.Event("deovr.seek.request", map[string]any{"target_ms": math.Round(seconds * 1000)})
	if e := c.Send(map[string]any{"currentTime": seconds}); e != nil {
		return e
	}
	return c.await()
}
func (c *Client) Load(path, name, file string) error {
	c.mu.Lock()
	c.target = nil
	c.desired = nil
	c.pending = false
	c.state.Loaded = false
	c.state.Paused = true
	c.state.Time = 0
	c.state.Duration = 0
	c.state.Message = "正在等待 DeoVR 加载视频"
	c.loadPath = path
	c.loadName = name
	c.loadFile = file
	c.loadStarted = time.Now()
	c.mu.Unlock()
	diagnostics.Event("deovr.load.request", map[string]any{"transport": "http"})
	return c.Send(map[string]any{"path": path})
}
func (c *Client) await() error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(4 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			s := c.Snapshot()
			if !s.Connected {
				return errors.New("DeoVR 已断开")
			}
			if !s.Seeking {
				return nil
			}
		case <-timeout.C:
			return errors.New("DeoVR 尚未确认操作，请在头显中检查")
		}
	}
}

func (c *Client) ReportedPath(source string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Source != source {
		return ""
	}
	return c.path
}

func (c *Client) Waiting(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		c.state.Message = message
	}
}
