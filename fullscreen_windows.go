package main

import (
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

func (a *App) fullscreenState() bool { return wr.WindowIsFullscreen(a.ctx) }

type fullscreenRect struct{ Left, Top, Right, Bottom int32 }

type fullscreenWindowRestore struct {
	mu        sync.Mutex
	hwnd      uintptr
	rect      fullscreenRect
	maximized bool
}

func (a *App) setPlaybackFullscreen(full bool) {
	s := &a.fullscreenRestore
	s.mu.Lock()
	defer s.mu.Unlock()
	if full == a.fullscreenState() {
		return
	}
	user := syscall.NewLazyDLL("user32.dll")
	getRect := user.NewProc("GetWindowRect")
	if full {
		s.hwnd = 0
		title, _ := syscall.UTF16PtrFromString("SyncPlayer")
		hwnd, _, _ := user.NewProc("FindWindowW").Call(0, uintptr(unsafe.Pointer(title)))
		var pid uint32
		user.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == uint32(os.Getpid()) {
			if ok, _, _ := getRect.Call(hwnd, uintptr(unsafe.Pointer(&s.rect))); ok != 0 {
				s.hwnd = hwnd
				zoomed, _, _ := user.NewProc("IsZoomed").Call(hwnd)
				s.maximized = zoomed != 0
			}
		}
		wr.WindowFullscreen(a.ctx)
		return
	}
	wr.WindowUnfullscreen(a.ctx)
	hwnd := s.hwnd
	s.hwnd = 0
	if hwnd == 0 {
		return
	}
	defer refreshFullscreenClientSize(user, hwnd)
	var restored fullscreenRect
	if ok, _, _ := getRect.Call(hwnd, uintptr(unsafe.Pointer(&restored))); ok == 0 || restored == s.rect {
		return
	}
	if s.maximized {
		// Reapply maximization so Windows sizes the window to the current work area.
		show := user.NewProc("ShowWindow")
		show.Call(hwnd, 9) // SW_RESTORE
		show.Call(hwnd, 3) // SW_MAXIMIZE
		return
	}
	r := s.rect
	user.NewProc("SetWindowPos").Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), 0x0214) // No owner z-order, activation, or z-order changes.
}

// The frame can restore without a final WebView resize when returning to a
// maximized window. Queue the size notification after placement is complete.
func refreshFullscreenClientSize(user *syscall.LazyDLL, hwnd uintptr) {
	var client fullscreenRect
	ok, _, _ := user.NewProc("GetClientRect").Call(hwnd, uintptr(unsafe.Pointer(&client)))
	if ok == 0 || client.Right <= 0 || client.Bottom <= 0 {
		return
	}
	var kind uintptr // SIZE_RESTORED
	if zoomed, _, _ := user.NewProc("IsZoomed").Call(hwnd); zoomed != 0 {
		kind = 2 // SIZE_MAXIMIZED
	}
	size := uintptr(uint16(client.Right)) | uintptr(uint16(client.Bottom))<<16
	user.NewProc("PostMessageW").Call(hwnd, 0x0005, kind, size) // WM_SIZE
}

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
	for name, key := range map[string]uintptr{"left": 1, "space": 0x20, "escape": 0x1B, "fullscreen_key": 0x46, "arrow_left": 0x25, "arrow_right": 0x27} {
		state, _, _ := user.NewProc("GetAsyncKeyState").Call(key)
		result[name] = active && state&0x8001 != 0
	}
	return result
}
