package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func showStartupError(err error) {
	message, _ := windows.UTF16PtrFromString(
		"SyncPlayer 启动失败：\n" + err.Error() +
			"\n\n请确认绿色包已完整解压，并已安装 Microsoft Edge WebView2 Runtime。",
	)
	title, _ := windows.UTF16PtrFromString("SyncPlayer")
	windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(
		0,
		uintptr(unsafe.Pointer(message)),
		uintptr(unsafe.Pointer(title)),
		0x10,
	)
}
