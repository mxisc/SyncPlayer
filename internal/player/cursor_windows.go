package player

import (
	"errors"
	"syscall"
	"unsafe"
)

// The embedded mpv window is disabled for input. Its host, not the WebView,
// owns the pointer, so CSS alone cannot hide the video cursor.
func (p *Player) installCursorHandler() error {
	common := syscall.NewLazyDLL("comctl32.dll")
	def := common.NewProc("DefSubclassProc")
	remove := common.NewProc("RemoveWindowSubclass")
	p.cursorProc = syscall.NewCallback(func(window uintptr, message uint32, wparam, lparam, id, data uintptr) uintptr {
		if message == 0x20 && lparam&0xffff == 1 && p.cursorHidden { // WM_SETCURSOR / HTCLIENT
			user.NewProc("SetCursor").Call(0)
			return 1
		}
		if message == 0x82 { // WM_NCDESTROY
			remove.Call(window, p.cursorProc, id)
		}
		result, _, _ := def.Call(window, uintptr(message), wparam, lparam)
		return result
	})
	ok, _, _ := common.NewProc("SetWindowSubclass").Call(p.child, p.cursorProc, 1, 0)
	if ok == 0 {
		return errors.New("无法设置视频光标处理")
	}
	return nil
}

func (p *Player) CursorHidden(hidden bool) {
	p.ui <- func() {
		p.cursorHidden = hidden
		// Apply immediately even when the pointer hasn't moved. Do not affect
		// the cursor in another application or over an exposed WebView control.
		var point struct{ X, Y int32 }
		ok, _, _ := user.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&point)))
		if ok == 0 {
			return
		}
		window, _, _ := user.NewProc("WindowFromPoint").Call(uintptr(uint32(point.X)) | uintptr(uint32(point.Y))<<32)
		if !p.OwnsWindow(window) {
			return
		}
		foreground, _, _ := user.NewProc("GetForegroundWindow").Call()
		root, _, _ := user.NewProc("GetAncestor").Call(window, 2)
		activeRoot, _, _ := user.NewProc("GetAncestor").Call(foreground, 2)
		if root != activeRoot {
			return
		}
		cursor := uintptr(0)
		if !hidden {
			cursor, _, _ = user.NewProc("LoadCursorW").Call(0, 32512)
		}
		user.NewProc("SetCursor").Call(cursor)
	}
}
