package main

import (
	"context"
	"errors"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syncplayer/internal/deovr"
	"syncplayer/internal/device"
	"syncplayer/internal/diagnostics"
	"syncplayer/internal/handy"
	"syncplayer/internal/library"
	"syncplayer/internal/media"
	"syncplayer/internal/player"
	"syncplayer/internal/script"
	"syncplayer/internal/subtitles"
	"time"
)

type App struct {
	ctx               context.Context
	device            *device.Controller
	handy             *handy.Client
	deviceMode        string
	mu                sync.Mutex
	script            script.Script
	media             *media.Server
	lastCycle         int
	cycleUntil        time.Time
	fastOutput        bool
	subtitles         *subtitles.Service
	player            *player.Player
	deo               *deovr.Client
	playbackMode      string
	audioOnly         bool
	mediaLoaded       bool
	libraryOpen       bool
	deoScriptSource   string
	library           *library.Store
	deoConnectionMode string
	deoAutoCancel     context.CancelFunc
	deoAutoDone       chan struct{}
}

func NewApp() (*App, error) {
	server, e := media.New()
	if e != nil {
		return nil, e
	}
	exe, e := os.Executable()
	if e != nil {
		server.Close()
		return nil, e
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(exe), "..", ".."))
	a := &App{device: device.New(), media: server, lastCycle: -1, subtitles: subtitles.New(root), player: player.New(root), playbackMode: "mpv", deoConnectionMode: "remote"}
	a.handy = handy.New()
	a.deviceMode = "bluetooth"
	a.library = library.New(filepath.Join(filepath.Dir(exe), "settings", "library.json"))
	a.deo = deovr.New(func(reason string) {
		if reason == "disconnect" || reason == "video.changed" {
			a.mu.Lock()
			if reason == "disconnect" || a.deoScriptSource != a.deo.Snapshot().Source {
				a.script = script.Script{}
				a.deoScriptSource = ""
			}
			a.mu.Unlock()
		}
		a.Stop()
	})
	return a, nil
}
func (a *App) startup(ctx context.Context) { a.ctx = ctx; diagnostics.Event("app.ui_ready", nil) }
func (a *App) shutdown(ctx context.Context) {
	diagnostics.Event("app.shutdown.begin", nil)
	a.stopDeoAuto()
	a.subtitles.Stop()
	a.library.Cancel()
	a.deo.Disconnect()
	a.player.Close()
	a.device.Disconnect()
	a.handy.Close()
	a.media.Close()
	diagnostics.Event("app.shutdown.end", nil)
}
func (a *App) Connect(address string) error                        { return a.device.Connect(address) }
func (a *App) DiscoverDevices() ([]device.DiscoveredDevice, error) { return a.device.Discover() }
func (a *App) ConnectDevice(name, identity string) error {
	return a.device.ConnectDevice(name, identity)
}
func (a *App) Disconnect() error { return errors.Join(a.device.Disconnect(), a.handy.Disconnect()) }
func (a *App) DeviceState() device.State {
	a.mu.Lock()
	mode := a.deviceMode
	a.mu.Unlock()
	if mode == "internet" {
		s := a.handy.State()
		return device.State{Connected: s.Connected, Message: s.Message, Kind: "handy-internet"}
	}
	return a.device.State()
}
func (a *App) SetDeviceMode(mode string) error {
	if mode != "bluetooth" && mode != "internet" {
		return errors.New("设备连接方式无效")
	}
	if e := a.Disconnect(); e != nil {
		return e
	}
	a.mu.Lock()
	a.deviceMode = mode
	a.mu.Unlock()
	return nil
}
func (a *App) ConnectHandy(key string) error {
	a.mu.Lock()
	mode := a.deviceMode
	a.mu.Unlock()
	if mode != "internet" {
		return errors.New("请先选择互联网模式")
	}
	if err := a.handy.Connect(key); err != nil {
		return err
	}
	path, err := handyKeyPath()
	if err == nil {
		err = handy.SaveKey(path, []byte(strings.TrimSpace(key)))
	}
	if err != nil {
		a.handy.Disconnect()
		return errors.New("Handy key 保存失败")
	}
	return nil
}

func handyKeyPath() (string, error) {
	exe, err := os.Executable()
	return filepath.Join(filepath.Dir(exe), "settings", "handy-key.bin"), err
}

func (a *App) SavedHandyKey() (string, error) {
	path, err := handyKeyPath()
	if err != nil {
		return "", errors.New("Handy key 无法读取")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}
	data, err := handy.LoadKey(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
func (a *App) Stop() error {
	a.mu.Lock()
	a.lastCycle = -1
	a.cycleUntil = time.Time{}
	a.fastOutput = false
	a.mu.Unlock()
	return errors.Join(a.device.Stop(), a.handy.Stop())
}
func (a *App) Level(level int, mode string) error { return a.device.Level(level, mode) }
func (a *App) TimedTest(level int, mode string, seconds float64) error {
	return a.device.TimedTest(level, mode, seconds)
}

func (a *App) TestHandyPosition(position int) error {
	if a.DeviceState().Kind != "handy-bluetooth" {
		return errors.New("请先连接 Handy 蓝牙")
	}
	s := a.PlaybackState()
	if s.Loaded && !s.Paused {
		return errors.New("请先暂停播放")
	}
	return a.device.TestHandyPosition(position)
}
func (a *App) TickDeo(offset int64, stroke, vibration bool) error {
	if !a.isDeo() {
		return errors.New("当前不是 DeoVR 模式")
	}
	state := a.deo.Snapshot()
	ms := int64(math.Round(state.Time*1000)) + offset
	if ms < 0 {
		ms = 0
	}
	return a.Tick(ms, stroke, vibration)
}
func (a *App) Tick(ms int64, stroke bool, vibration bool) error {
	if ms < 0 {
		return errors.New("播放参数无效")
	}

	rate := 1.0
	a.mu.Lock()
	vr := a.playbackMode == "deovr"

	matchedSource := a.deoScriptSource
	a.mu.Unlock()
	if vr {
		state := a.deo.Snapshot()
		if matchedSource != state.Source || !state.Connected || !state.Loaded || state.Paused || state.Seeking || state.Buffering {
			return a.Stop()
		}
		rate = state.Rate
		if rate <= 0 {
			return a.Stop()
		}
	}
	a.mu.Lock()
	deviceMode := a.deviceMode
	actions := a.script
	a.mu.Unlock()
	if deviceMode == "internet" {
		return a.handy.Tick(actions, ms, rate, stroke && len(actions.Actions) > 1 && ms < actions.Actions[len(actions.Actions)-1].At)
	}
	if a.device.State().Kind == "handy-bluetooth" {
		if !stroke || len(actions.Actions) < 2 {
			return a.device.Stop()
		}
		if ms >= actions.Actions[len(actions.Actions)-1].At {
			return a.device.FinishHandyScript()
		}
		return a.device.FollowScript(actions.Actions, ms, rate)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	vibrationLevel := 0
	if vibration {
		vibrationLevel = a.script.Level(ms)
	}
	if e := a.device.Vibration(vibrationLevel); e != nil {
		return e
	}
	if !stroke {
		a.lastCycle = -1
		a.cycleUntil = time.Time{}
		a.fastOutput = false
		return a.device.StopStroke()
	}
	i, cycle, ok := a.script.CycleAt(ms)
	if ok && float64(cycle.End-cycle.Start)/rate < script.CycleMS[10] {
		if i != a.lastCycle || !a.fastOutput {
			diagnostics.Event("playback.cycle.saturated", map[string]any{"index": i, "script_period_ms": cycle.End - cycle.Start, "level": 10})
		}
		a.lastCycle = i
		a.fastOutput = true
		a.cycleUntil = time.Time{}
		return a.device.Motion(10, "stroke")
	}
	wasFast := a.fastOutput
	if wasFast {
		a.fastOutput = false
		a.lastCycle = -1
		if e := a.device.StopStroke(); e != nil {
			return e
		}
	}
	// Complete an already-started stroke before considering another cycle.
	if time.Now().Before(a.cycleUntil) {
		return a.device.CycleHeartbeat()
	}
	if !ok || i == a.lastCycle {
		return nil
	}
	a.lastCycle = i
	// On seeking/resuming mid-cycle, wait for the next boundary rather than
	// executing a shortened stroke from an unknown physical position.
	if !wasFast && float64(ms-cycle.Start)/rate > 150 {
		return nil
	}
	level := script.SelectLevel(int64(float64(cycle.End-cycle.Start)/rate), 10)
	runMS := script.CycleMS[level]
	diagnostics.Event("playback.cycle", map[string]any{"index": i, "script_start_ms": cycle.Start, "script_period_ms": cycle.End - cycle.Start, "level": level, "run_ms": runMS, "too_fast": runMS > float64(cycle.End-cycle.Start)})
	if e := a.device.BeginCycle(level, runMS/1000); e != nil {
		return e
	}
	a.cycleUntil = time.Now().Add(time.Duration(runMS * float64(time.Millisecond)))
	return nil
}

func (a *App) FinishPlaybackOutput() error {
	if a.DeviceState().Kind == "handy-bluetooth" {
		return a.device.FinishHandyScript()
	}
	return a.Stop()
}
func (a *App) OpenScript() (result map[string]interface{}, err error) {
	diagnostics.Event("script.select.begin", nil)
	defer func() {
		diagnostics.Event("script.select.end", map[string]any{"error": err, "selected": result != nil})
	}()
	p, e := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "选择 funscript", Filters: []wr.FileFilter{{DisplayName: "Funscript", Pattern: "*.funscript"}}})
	if e != nil || p == "" {
		return nil, e
	}
	data, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	s, e := script.Parse(data)
	if e != nil {
		return nil, e
	}
	a.Stop()
	a.mu.Lock()
	a.script = s
	if a.playbackMode == "deovr" {
		a.deoScriptSource = a.deo.Snapshot().Source
	}
	a.mu.Unlock()
	diagnostics.Event("script.loaded", map[string]any{"actions": len(s.Actions), "duration_ms": s.Actions[len(s.Actions)-1].At})
	return map[string]interface{}{"name": filepath.Base(p), "actions": len(s.Actions), "duration": s.Actions[len(s.Actions)-1].At, "points": s.Actions}, nil
}

type VideoSelection struct {
	Kind    string                 `json:"kind"`
	Name    string                 `json:"name"`
	URL     string                 `json:"url"`
	Script  map[string]interface{} `json:"script"`
	Warning string                 `json:"warning"`
}

func (a *App) OpenVideo() (result *VideoSelection, err error) {
	diagnostics.Event("video.select.begin", nil)
	defer func() { diagnostics.Event("video.select.end", map[string]any{"error": err, "selected": result != nil}) }()
	videoPattern := "*.mp4;*.webm;*.mkv;*.mov;*.avi;*.m4v;*.wmv;*.ts;*.mts;*.m2ts"
	audioPattern := "*.mp3;*.wav;*.flac;*.m4a;*.ogg;*.opus;*.aac;*.wma;*.aiff;*.aif"
	options := wr.OpenDialogOptions{Title: "选择媒体", Filters: []wr.FileFilter{{DisplayName: "视频和音声", Pattern: videoPattern + ";" + audioPattern}, {DisplayName: "音声", Pattern: audioPattern}, {DisplayName: "视频", Pattern: videoPattern}}}
	if a.isDeo() {
		options.Title = "选择视频"
		options.Filters = []wr.FileFilter{{DisplayName: "视频", Pattern: videoPattern}}
	}
	p, e := wr.OpenFileDialog(a.ctx, options)
	if e != nil || p == "" {
		return nil, e
	}
	return a.loadVideo(p)
}
func (a *App) loadVideo(p string) (*VideoSelection, error) {
	audio := library.IsAudio(p)
	if a.isDeo() && audio {
		return nil, errors.New("音声请使用本地播放")
	}
	a.Stop()
	a.mu.Lock()
	a.script = script.Script{}
	a.mu.Unlock()
	ext := strings.ToLower(filepath.Ext(p))
	if (ext == ".ts" || ext == ".mts" || ext == ".m2ts") && !library.IsTransportStream(p) {
		diagnostics.Event("video.invalid_transport", nil)
		return nil, errors.New("此文件未识别为 TS 视频，可能是 TypeScript 源码或文件不完整")
	}
	selection := &VideoSelection{Name: filepath.Base(p), URL: "mpv", Kind: "video"}
	if audio {
		selection.Kind = "audio"
	}
	data, e := os.ReadFile(library.ScriptPath(p))
	if e == nil {
		s, parseErr := script.Parse(data)
		if parseErr == nil {
			a.mu.Lock()
			a.script = s
			a.mu.Unlock()
			selection.Script = map[string]interface{}{"name": filepath.Base(library.ScriptPath(p)), "points": s.Actions}
		} else {
			selection.Warning = "同名脚本无法解析，设备同步已关闭"
		}
	} else if !os.IsNotExist(e) {
		selection.Warning = "同名脚本无法读取，设备同步已关闭"
	}
	var loadErr error
	if a.isDeo() {

		var streamURL string
		streamURL, loadErr = a.media.Select(p)
		if loadErr == nil {
			loadErr = a.deo.Load(streamURL, filepath.Base(p), p)
		}
	} else {
		loadErr = a.player.Load(p)
	}
	if loadErr != nil {
		a.mu.Lock()
		a.script = script.Script{}
		a.mu.Unlock()
		return nil, loadErr
	}
	a.subtitles.Select(p)
	a.mu.Lock()
	a.audioOnly = audio
	a.mediaLoaded = true
	a.mu.Unlock()
	if !a.isDeo() {
		a.player.Visible(!audio)
	}
	diagnostics.Event("library.video.loaded", map[string]any{"has_script": selection.Script != nil, "script_warning": selection.Warning != ""})
	return selection, nil
}
func (a *App) LibraryState() library.State { return a.library.Snapshot() }
func (a *App) ScanLibrary(choose bool) (*library.State, error) {
	dir := a.library.Snapshot().Directory
	if choose {
		selected, e := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "选择媒体目录"})
		if e != nil || selected == "" {
			return nil, e
		}
		dir = selected
	}
	if dir == "" {
		return nil, errors.New("请先选择目录")
	}
	diagnostics.Event("library.scan.begin", nil)
	s, e := a.library.Scan(dir)
	if e == nil && a.isDeo() && a.media.Address() != "" {
		a.refreshDeoCatalog()
	}
	diagnostics.Event("library.scan.end", map[string]any{"items": len(s.Items), "skipped": s.Skipped, "failed": e != nil})
	if e != nil {
		return nil, e
	}
	return &s, nil
}
func (a *App) CancelLibraryScan() { a.library.Cancel() }
func (a *App) OpenLibraryVideo(id string) (*VideoSelection, error) {
	p, e := a.library.Path(id)
	if e != nil {
		return nil, e
	}
	return a.loadVideo(p)
}

func (a *App) PlaybackLayout(x, y, w, h, viewport, overlay float64) error {
	if a.isDeo() {
		return nil
	}
	err := a.player.Layout(x, y, w, h, viewport, overlay)
	a.mu.Lock()
	show := a.mediaLoaded && !a.audioOnly && !a.libraryOpen
	a.mu.Unlock()
	a.player.Visible(show)
	return err
}
func (a *App) isDeo() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.playbackMode == "deovr" }
func (a *App) SetLibraryExpanded(expanded bool) {
	a.mu.Lock()
	a.libraryOpen = expanded
	show := a.playbackMode == "mpv" && a.mediaLoaded && !a.audioOnly && !expanded
	a.mu.Unlock()
	a.player.Visible(show)
}
func (a *App) SetPlaybackMode(mode string) error {
	if mode != "mpv" && mode != "deovr" {
		return errors.New("播放模式无效")
	}
	a.stopDeoAuto()
	a.mu.Lock()
	target := a.deoConnectionMode
	a.mu.Unlock()
	a.media.Close()
	if mode == "deovr" && target == "remote" {
		if err := a.media.StartLAN(); err != nil {
			return err
		}
		a.refreshDeoCatalog()
	}

	a.Stop()
	a.subtitles.Stop()
	a.player.Command("set_property", "pause", true)
	a.deo.Disconnect()
	a.mu.Lock()
	a.playbackMode = mode
	a.audioOnly = false
	a.mediaLoaded = false
	a.mu.Unlock()
	a.player.Visible(false)
	if mode == "deovr" {
		a.startDeoAuto()
	}
	diagnostics.Event("playback.backend", map[string]any{"mode": mode})
	return nil
}
func (a *App) ConnectDeo(host string) error {
	if !a.isDeo() {
		return errors.New("请先选择 DeoVR 模式")
	}
	if err := a.deo.Connect(host); err != nil {
		return err
	}
	a.mu.Lock()
	a.mu.Unlock()
	return nil
}

// FollowDeoMedia loads only the adjacent script; DeoVR keeps control of playback.
func (a *App) FollowDeoMedia(source string) *VideoSelection {
	result := &VideoSelection{Kind: "video", URL: source}
	if !a.isDeo() {
		return result
	}
	a.Stop()
	a.mu.Lock()
	a.script = script.Script{}
	a.deoScriptSource = ""
	a.mu.Unlock()
	path := a.media.Resolve(a.deo.ReportedPath(source))
	if path == "" && a.deo.IsLocal() {
		path = a.deo.LocalFile(source)
	}
	if path == "" {
		raw := a.deo.ReportedPath(source)
		if u, err := url.Parse(raw); err == nil {
			raw = u.Path
		}
		path = a.library.UniqueName(filepath.Base(strings.ReplaceAll(raw, "\\", "/")))
	}
	if path == "" {
		result.Warning = "未找到本地视频路径，设备动作已停止"
		return result
	}
	result.Name = filepath.Base(path)
	data, err := os.ReadFile(library.ScriptPath(path))
	if err != nil {
		result.Warning = "未找到同名脚本，设备动作已停止"
		if !os.IsNotExist(err) {
			result.Warning = "同名脚本无法读取，设备动作已停止"
		}
		diagnostics.Event("deovr.script.match", map[string]any{"matched": false, "unreadable": !os.IsNotExist(err)})
		return result
	}
	s, err := script.Parse(data)
	if err != nil {
		result.Warning = "同名脚本无法解析，设备动作已停止"
		return result
	}
	a.mu.Lock()
	if a.playbackMode == "deovr" && a.deo.Snapshot().Source == source {
		a.script = s
		a.deoScriptSource = source
		result.Script = map[string]interface{}{"name": filepath.Base(library.ScriptPath(path)), "points": s.Actions}
	}
	a.mu.Unlock()
	diagnostics.Event("deovr.script.match", map[string]any{"matched": result.Script != nil, "actions": len(s.Actions)})
	return result
}
func (a *App) DisconnectDeo() { a.stopDeoAuto(); a.deo.Disconnect() }
func (a *App) PlaybackState() deovr.State {
	if a.isDeo() {
		s := a.deo.Snapshot()
		s.SyncEnabled = s.Connected
		s.Fullscreen = a.fullscreenState()
		return s
	}
	return deovr.State{State: a.player.Snapshot(), Rate: 1, Fullscreen: a.fullscreenState()}
}
func (a *App) PlaybackPause(paused bool) error {
	if paused {
		a.Stop()
	}
	if a.isDeo() {
		return a.deo.Pause(paused)
	}
	return a.player.Command("set_property", "pause", paused)
}
func (a *App) PlaybackSeek(seconds float64) error {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return errors.New("跳转时间无效")
	}
	a.Stop()
	if a.isDeo() {
		return a.deo.Seek(seconds)
	}
	diagnostics.Event("mpv.seek.begin", map[string]any{"target_ms": math.Round(seconds * 1000)})
	err := a.player.Seek(seconds)
	diagnostics.Event("mpv.seek.end", map[string]any{"target_ms": math.Round(seconds * 1000), "actual_ms": math.Round(a.player.Snapshot().Time * 1000), "error": err})
	return err
}
func (a *App) PlaybackVolume(volume float64) error {
	if a.isDeo() {
		return errors.New("请在 DeoVR 中调整音量")
	}
	return a.player.Command("set_property", "volume", volume)
}
func (a *App) PlaybackSubtitle(text string) error {
	if a.isDeo() {
		return nil
	}
	return a.player.Subtitle(text)
}
func (a *App) PlaybackFullscreen(full bool) {
	if full {
		wr.WindowFullscreen(a.ctx)
	} else {
		wr.WindowUnfullscreen(a.ctx)
	}
}

func (a *App) PlaybackCursorHidden(hidden bool) { a.player.CursorHidden(hidden) }

func (a *App) StartSubtitles(key, language string, position int64, lowSpeech bool) error {
	if a.isDeo() {
		return errors.New("DeoVR 模式暂不支持实时翻译字幕")
	}
	return a.subtitles.Start(key, language, position, lowSpeech)
}
func (a *App) AISettings() (subtitles.AISettings, error) { return a.subtitles.AISettings() }
func (a *App) FetchAIModels(provider string) ([]string, error) {
	return a.subtitles.FetchAIModels(provider)
}
func (a *App) SaveAIProfile(profile subtitles.AIProfile, key string) (subtitles.AISettings, error) {
	return a.subtitles.SaveAIProfile(profile, key)
}
func (a *App) DeleteAIKey(provider string) (subtitles.AISettings, error) {
	return a.subtitles.DeleteAIKey(provider)
}
func (a *App) StopSubtitles()                               { a.subtitles.Stop() }
func (a *App) SubtitleState(position int64) subtitles.State { return a.subtitles.Snapshot(position) }

type PlaybackSnapshot struct {
	TimeMS           float64 `json:"time_ms"`
	DurationMS       float64 `json:"duration_ms"`
	Paused           bool    `json:"paused"`
	Seeking          bool    `json:"seeking"`
	ReadyState       int     `json:"ready_state"`
	NetworkState     int     `json:"network_state"`
	Armed            bool    `json:"armed"`
	Connected        bool    `json:"connected"`
	HasScript        bool    `json:"has_script"`
	StrokeEnabled    bool    `json:"stroke_enabled"`
	VibrationEnabled bool    `json:"vibration_enabled"`
	Output           int     `json:"output"`
	Mode             string  `json:"mode"`
	Cap              int     `json:"cap"`
	Offset           int     `json:"offset"`
	ErrorCode        int     `json:"error_code"`
	Message          string  `json:"message"`
}

func (a *App) ReportPlayback(event string, s PlaybackSnapshot) {
	switch event {
	case "play", "playing", "pause", "seeking", "seeked", "ended", "waiting", "error", "loadedmetadata", "parameters", "snapshot", "js_error", "sync.attached":
	default:
		return
	}
	if len(s.Message) > 2000 {
		s.Message = s.Message[:2000]
	}
	diagnostics.Event("playback."+event, map[string]any{"snapshot": s, "time_ms": s.TimeMS, "message": s.Message})
}

func (a *App) refreshDeoCatalog() {
	entries := map[string]media.CatalogEntry{}
	for _, item := range a.library.Snapshot().Items {
		if item.Kind == "audio" {
			continue
		}
		if path, err := a.library.Path(item.ID); err == nil {
			entries[item.ID] = media.CatalogEntry{Name: item.Name, Path: path}
		}
	}
	a.media.SetCatalog(entries)
}
func (a *App) DeoMediaAddress() string {
	if !a.isDeo() {
		return ""
	}
	return a.media.Address()
}

func (a *App) CopyDeoMediaAddress() {
	if address := a.DeoMediaAddress(); address != "" {
		wr.ClipboardSetText(a.ctx, address)
	}
}
