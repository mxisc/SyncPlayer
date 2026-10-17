package player

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Microsoft/go-winio"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syncplayer/internal/diagnostics"
	"syscall"
	"time"
	"unsafe"
)

type State struct {
	Time      float64 `json:"time"`
	Duration  float64 `json:"duration"`
	Paused    bool    `json:"paused"`
	Seeking   bool    `json:"seeking"`
	Buffering bool    `json:"buffering"`
	Loaded    bool    `json:"loaded"`
	Ended     bool    `json:"ended"`
	Message   string  `json:"message"`
}
type Player struct {
	mu           sync.Mutex
	write        sync.Mutex
	start        sync.Mutex
	conn         net.Conn
	cmd          *exec.Cmd
	child        uintptr
	cursorProc   uintptr
	cursorHidden bool // Only accessed on the native UI thread.
	visible      bool
	state        State
	done         chan struct{}
	root         string
	subtitle     string
	ui           chan func()
	request      int
	pending      chan string
	subMu        sync.Mutex
}

var user = syscall.NewLazyDLL("user32.dll")
var findWindow = user.NewProc("FindWindowW")
var createWindow = user.NewProc("CreateWindowExW")
var setPosition = user.NewProc("SetWindowPos")
var getRect = user.NewProc("GetClientRect")

func New(root string) *Player {
	p := &Player{root: root, state: State{Paused: true}, ui: make(chan func(), 16)}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var msg struct {
			Window  uintptr
			ID      uint32
			Wparam  uintptr
			Lparam  uintptr
			Time    uint32
			X, Y    int32
			Private uint32
		}
		peek := user.NewProc("PeekMessageW")
		translate := user.NewProc("TranslateMessage")
		dispatch := user.NewProc("DispatchMessageW")
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case job := <-p.ui:
				job()
			case <-ticker.C:
			}
			for {
				ok, _, _ := peek.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1)
				if ok == 0 {
					break
				}
				translate.Call(uintptr(unsafe.Pointer(&msg)))
				dispatch.Call(uintptr(unsafe.Pointer(&msg)))
			}
		}
	}()
	return p
}
func (p *Player) Layout(x, y, w, h, viewport, overlay float64) error {
	result := make(chan error, 1)
	p.ui <- func() { result <- p.layout(x, y, w, h, viewport, overlay) }
	return <-result
}
func (p *Player) Visible(visible bool) {
	done := make(chan struct{})
	p.ui <- func() {
		p.mu.Lock()
		p.visible = visible
		child := p.child
		p.mu.Unlock()
		if child != 0 {
			flag := uintptr(0)
			if visible {
				flag = 5
				// The WebView is a sibling window. Bring video above it without
				// stealing focus when returning from audio or the library dialog.
				setPosition.Call(child, 0, 0, 0, 0, 0, 0x0010|0x0001|0x0002|0x0040)
			}
			user.NewProc("ShowWindow").Call(child, flag)
		}
		close(done)
	}
	<-done
}
func (p *Player) layout(x, y, w, h, viewport, overlay float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	title, _ := syscall.UTF16PtrFromString("SyncPlayer")
	parent, _, _ := findWindow.Call(0, uintptr(unsafe.Pointer(title)))
	if parent == 0 {
		return errors.New("播放器窗口尚未就绪")
	}
	var pid uint32
	user.NewProc("GetWindowThreadProcessId").Call(parent, uintptr(unsafe.Pointer(&pid)))
	if pid != uint32(os.Getpid()) {
		return errors.New("播放器窗口匹配失败")
	}
	var r struct{ Left, Top, Right, Bottom int32 }
	getRect.Call(parent, uintptr(unsafe.Pointer(&r)))
	scale := 1.0
	if viewport > 0 {
		scale = float64(r.Right) / viewport
	}
	if p.child == 0 {
		class, _ := syscall.UTF16PtrFromString("STATIC")
		// SS_BLACKRECT keeps the native surface dark before mpv renders a frame.
		p.child, _, _ = createWindow.Call(0, uintptr(unsafe.Pointer(class)), 0, 0x40000000|0x02000000|0x00000004, 0, 0, 1, 1, parent, 0, 0, 0)
		if p.child == 0 {
			return errors.New("无法创建视频区域")
		}
		if err := p.installCursorHandler(); err != nil {
			return err
		}
	}
	flags := uintptr(0x0010) // Keep focus; HWND_TOP places video above the WebView.
	if p.visible {
		flags |= 0x0040
	}
	setPosition.Call(p.child, 0, uintptr(int(x*scale)), uintptr(int(y*scale)), uintptr(int(w*scale)), uintptr(int(h*scale)), flags)
	// Keep the mpv viewport full-sized. Expose the WebView's opaque controls
	// by clipping only the covered strip, rather than shrinking the video.
	if overlay > 0 {
		gdi := syscall.NewLazyDLL("gdi32.dll")
		region, _, _ := gdi.NewProc("CreateRectRgn").Call(0, 0, uintptr(max(1, int(w*scale))), uintptr(max(0, int((h-math.Min(h, overlay))*scale))))
		if region == 0 {
			return errors.New("无法设置播放控制区域")
		}
		ok, _, _ := user.NewProc("SetWindowRgn").Call(p.child, region, 1)
		if ok == 0 {
			gdi.NewProc("DeleteObject").Call(region)
			return errors.New("无法更新播放控制区域")
		}
	} else {
		user.NewProc("SetWindowRgn").Call(p.child, 0, 1)
	}
	return nil
}
func (p *Player) ensure() error {
	p.start.Lock()
	defer p.start.Unlock()
	p.mu.Lock()
	if p.conn != nil {
		p.mu.Unlock()
		return nil
	}
	child := p.child
	previous, previousDone := p.cmd, p.done
	p.mu.Unlock()
	if previous != nil {
		select {
		case <-previousDone:
		default:
			previous.Process.Kill()
			<-previousDone
		}
	}
	if child == 0 {
		return errors.New("视频区域尚未就绪")
	}
	binary := filepath.Join(p.root, "runtime", "mpv", "mpv.exe")
	if _, e := os.Stat(binary); e != nil {
		return errors.New("缺少 mpv 播放内核")
	}
	pipe := fmt.Sprintf(`\\.\pipe\syncplayer-mpv-%d-%d`, os.Getpid(), time.Now().UnixNano())
	cmd := exec.Command(binary, "--no-config", "--idle=yes", "--keep-open=yes", "--force-window=yes", "--pause=yes", "--terminal=no", "--osc=no", "--cursor-autohide=no", "--input-default-bindings=no", "--input-vo-keyboard=no", "--hwdec=auto-safe", "--vo=gpu", "--gpu-api=d3d11", "--gpu-context=d3d11", fmt.Sprintf("--wid=%d", uint32(child)), "--input-ipc-server="+pipe)
	cmd.Args = append(cmd.Args, "--sub-font=Microsoft YaHei", "--sub-font-size=36", "--sub-color=#F1F4FA", "--sub-border-color=#101722", "--sub-border-size=1.5", "--sub-shadow-offset=1", "--sub-margin-y=36")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if e := cmd.Start(); e != nil {
		return errors.New("mpv 启动失败")
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	var c net.Conn
	var err error
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		timeout := 200 * time.Millisecond
		c, err = winio.DialPipe(pipe, &timeout)
		if err == nil {
			break
		}
		select {
		case <-done:
			return errors.New("mpv 提前退出")
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	if c == nil {
		cmd.Process.Kill()
		return errors.New("mpv 控制通道连接失败")
	}
	p.mu.Lock()
	p.cmd = cmd
	p.done = done
	p.conn = c
	p.mu.Unlock()
	go p.read(c)
	for i, name := range []string{"time-pos", "duration", "pause", "seeking", "paused-for-cache", "idle-active", "eof-reached", "video-format", "hwdec-current", "width", "height"} {
		if err = p.Command("observe_property", i+1, name); err != nil {
			return err
		}
	}
	diagnostics.Event("mpv.started", nil)
	return nil
}
func (p *Player) Command(args ...any) error {
	p.write.Lock()
	defer p.write.Unlock()
	p.mu.Lock()
	c := p.conn
	p.mu.Unlock()
	if c == nil {
		return errors.New("mpv 未启动")
	}
	b, _ := json.Marshal(map[string]any{"command": args})
	p.mu.Lock()
	p.request++
	id := p.request
	reply := make(chan string, 1)
	p.pending = reply
	p.mu.Unlock()
	b, _ = json.Marshal(map[string]any{"command": args, "request_id": id})
	c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, e := c.Write(append(b, '\n'))
	if e != nil {
		return e
	}
	select {
	case result := <-reply:
		if result != "success" {
			diagnostics.Event("mpv.command.error", map[string]any{"command": args[0], "error": result})
			return fmt.Errorf("mpv 操作失败：%s", result)
		}
		return nil
	case <-time.After(3 * time.Second):
		return errors.New("mpv 操作超时")
	}
}
func (p *Player) Load(path string) error {
	if e := p.ensure(); e != nil {
		return e
	}
	p.mu.Lock()
	p.state = State{Paused: true}
	p.mu.Unlock()
	if e := p.Command("set_property", "pause", true); e != nil {
		return e
	}
	return p.Command("loadfile", path, "replace")
}
func (p *Player) Snapshot() State { p.mu.Lock(); defer p.mu.Unlock(); return p.state }

// OwnsWindow includes mpv's cross-process render window embedded in our host.
func (p *Player) OwnsWindow(window uintptr) bool {
	p.mu.Lock()
	child, visible := p.child, p.visible
	p.mu.Unlock()
	if child == 0 || window == 0 || !visible {
		return false
	}
	if window == child {
		return true
	}
	inside, _, _ := user.NewProc("IsChild").Call(child, window)
	return inside != 0
}
func (p *Player) Seek(seconds float64) error {
	s := p.Snapshot()
	if s.Duration > 0 {
		seconds = math.Min(seconds, math.Max(0, s.Duration-0.05))
	}
	if err := p.Command("seek", seconds, "absolute+exact"); err != nil {
		return err
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(8 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-ticker.C:
			s = p.Snapshot()
			if !s.Loaded {
				return errors.New("视频播放通道已关闭")
			}
			if !s.Seeking && math.Abs(s.Time-seconds) < 0.5 {
				return nil
			}
		case <-timeout.C:
			return errors.New("视频跳转尚未完成，请重试")
		}
	}
}
func (p *Player) read(c net.Conn) {
	scanner := bufio.NewScanner(c)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		var v struct {
			Event     string          `json:"event"`
			Name      string          `json:"name"`
			Data      json.RawMessage `json:"data"`
			Error     string          `json:"error"`
			Reason    string          `json:"reason"`
			RequestID int             `json:"request_id"`
		}
		if json.Unmarshal(scanner.Bytes(), &v) != nil {
			continue
		}
		p.mu.Lock()
		if v.RequestID != 0 && v.RequestID == p.request && p.pending != nil {
			select {
			case p.pending <- v.Error:
			default:
			}
		}
		switch v.Event {
		case "property-change":
			switch v.Name {
			case "time-pos":
				json.Unmarshal(v.Data, &p.state.Time)
			case "duration":
				json.Unmarshal(v.Data, &p.state.Duration)
			case "pause":
				json.Unmarshal(v.Data, &p.state.Paused)
			case "seeking":
				json.Unmarshal(v.Data, &p.state.Seeking)
			case "paused-for-cache":
				json.Unmarshal(v.Data, &p.state.Buffering)
			case "idle-active":
				var idle bool
				json.Unmarshal(v.Data, &idle)
				if idle {
					p.state.Loaded = false
				}
			case "eof-reached":
				var end bool
				json.Unmarshal(v.Data, &end)
				p.state.Ended = end
				if end {
					p.state.Paused = true
				}
			case "video-format", "hwdec-current", "width", "height":
				var value any
				if json.Unmarshal(v.Data, &value) == nil && value != nil {
					diagnostics.Event("mpv.decoder", map[string]any{"property": v.Name, "value": value})
				}
			}
		case "file-loaded":
			p.state.Loaded = true
		case "end-file":
			p.state.Paused = true
			if v.Reason == "error" {
				p.state.Message = "mpv 无法播放此视频"
			}
		}
		p.mu.Unlock()
		if v.Event == "file-loaded" || v.Event == "end-file" {
			diagnostics.Event("mpv."+v.Event, map[string]any{"reason": v.Reason})
		}
	}
	p.mu.Lock()
	if p.conn == c {
		if p.pending != nil {
			select {
			case p.pending <- "控制通道已关闭":
			default:
			}
		}
		p.conn = nil
		p.state.Paused = true
		p.state.Loaded = false
		p.state.Message = "mpv 播放通道已关闭"
	}
	p.mu.Unlock()
	c.Close()
	diagnostics.Event("mpv.disconnected", nil)
}
func (p *Player) Subtitle(text string) error {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	if text == "" {
		return p.Command("set_property", "sub-visibility", false)
	}
	p.mu.Lock()
	old := p.subtitle
	file := filepath.Join(p.root, "build", "bin", "subtitle-cache", fmt.Sprintf("mpv-%d-%d.srt", os.Getpid(), time.Now().UnixNano()))
	p.mu.Unlock()
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return e
	}
	if e := os.WriteFile(file, []byte(text), 0600); e != nil {
		return e
	}
	p.Command("set_property", "sub-visibility", true)
	if old != "" {
		p.Command("sub-remove")
	}
	if e := p.Command("sub-add", file, "select"); e != nil {
		os.Remove(file)
		return e
	}
	p.mu.Lock()
	p.subtitle = file
	p.mu.Unlock()
	if old != "" {
		os.Remove(old)
	}
	return nil
}
func (p *Player) Close() {
	p.start.Lock()
	defer p.start.Unlock()
	p.mu.Lock()
	cmd, done, c, sub := p.cmd, p.done, p.conn, p.subtitle
	p.mu.Unlock()
	if c != nil {
		p.Command("quit")
		c.Close()
	}
	if cmd != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
		}
	}
	if sub != "" {
		os.Remove(sub)
	}
}
