package main

import (
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
	"os"
	"syscall"
	"unsafe"
)

// Native mpv can hold keyboard focus, so Escape must also work outside WebView.
func (a *App) fullscreenState() bool {
	if !wr.WindowIsFullscreen(a.ctx) {
		return false
	}
	user := syscall.NewLazyDLL("user32.dll")
	foreground, _, _ := user.NewProc("GetForegroundWindow").Call()
	var pid uint32
	user.NewProc("GetWindowThreadProcessId").Call(foreground, uintptr(unsafe.Pointer(&pid)))
	key, _, _ := user.NewProc("GetAsyncKeyState").Call(0x1B)
	if pid == uint32(os.Getpid()) && key&0x8000 != 0 {
		wr.WindowUnfullscreen(a.ctx)
		return false
	}
	return true
}

// Read native pointer coordinates because the mpv child covers the WebView.
// Only report activity inside this application's foreground window.
func (a *App) PlaybackPointerState() map[string]any {
	user := syscall.NewLazyDLL("user32.dll")
	var point struct{ X, Y int32 }
	ok, _, _ := user.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
	result := map[string]any{"x": point.X, "y": point.Y, "inside": false}
	if ok == 0 {
		return result
	}
	foreground, _, _ := user.NewProc("GetForegroundWindow").Call()
	root, _, _ := user.NewProc("GetAncestor").Call(foreground, 2)
	if root == 0 {
		root = foreground
	}
	var pid uint32
	user.NewProc("GetWindowThreadProcessId").Call(root, uintptr(unsafe.Pointer(&pid)))
	if pid != uint32(os.Getpid()) {
		return result
	}
	var rect struct{ Left, Top, Right, Bottom int32 }
	ok, _, _ = user.NewProc("GetWindowRect").Call(root, uintptr(unsafe.Pointer(&rect)))
	result["inside"] = ok != 0 && point.X >= rect.Left && point.X < rect.Right && point.Y >= rect.Top && point.Y < rect.Bottom
	return result
}
