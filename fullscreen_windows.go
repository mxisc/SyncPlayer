package main

import (
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
	"syscall"
	"unsafe"
)

func (a *App) fullscreenState() bool { return wr.WindowIsFullscreen(a.ctx) }

// Native video does not deliver input to the WebView. Sample independently
// from playback state requests, and scope every action to our own windows.
func (a *App) PlaybackPointerState() map[string]any {
	user := syscall.NewLazyDLL("user32.dll")
	var point struct{ X, Y int32 }
	ok, _, _ := user.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	result := map[string]any{"x": point.X, "y": point.Y, "inside": false, "video": false, "native_focus": false}
	foreground, _, _ := user.NewProc("GetForegroundWindow").Call()
	root, _, _ := user.NewProc("GetAncestor").Call(foreground, 2)
	if root == 0 {
		root = foreground
	}
	var pid uint32
	user.NewProc("GetWindowThreadProcessId").Call(root, uintptr(unsafe.Pointer(&pid)))
	active := pid == uint32(os.Getpid()) || a.player.OwnsWindow(foreground)
	var rect struct{ Left, Top, Right, Bottom int32 }
	valid, _, _ := user.NewProc("GetWindowRect").Call(root, uintptr(unsafe.Pointer(&rect)))
	result["inside"] = active && ok != 0 && valid != 0 && point.X >= rect.Left && point.X < rect.Right && point.Y >= rect.Top && point.Y < rect.Bottom
	if valid != 0 && rect.Right > rect.Left && rect.Bottom > rect.Top {
		result["relative_x"] = float64(point.X-rect.Left) / float64(rect.Right-rect.Left)
		result["relative_y"] = float64(point.Y-rect.Top) / float64(rect.Bottom-rect.Top)
	}
	window, _, _ := user.NewProc("WindowFromPoint").Call(uintptr(uint32(point.X)) | uintptr(uint32(point.Y))<<32)
	result["video"] = active && a.player.OwnsWindow(window)
	var info struct {
		Size, Flags                                        uint32
		Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
		Rect                                               struct{ Left, Top, Right, Bottom int32 }
	}
	info.Size = uint32(unsafe.Sizeof(info))
	valid, _, _ = user.NewProc("GetGUIThreadInfo").Call(0, uintptr(unsafe.Pointer(&info)))
	result["native_focus"] = active && valid != 0 && a.player.OwnsWindow(info.Focus)
	for name, key := range map[string]uintptr{"left": 1, "space": 0x20, "escape": 0x1B, "fullscreen_key": 0x46} {
		state, _, _ := user.NewProc("GetAsyncKeyState").Call(key)
		result[name] = active && state&0x8001 != 0
	}
	return result
}
