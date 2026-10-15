package main

import (
	"embed"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"log"
	"syncplayer/internal/diagnostics"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if e := diagnostics.Init(); e != nil {
		log.Fatal("无法创建诊断日志：", e)
	}
	defer diagnostics.Close()
	app, e := NewApp()
	if e != nil {
		log.Fatal(e)
	}
	defer app.media.Close()
	if e := wails.Run(&options.App{Title: "SyncPlayer", Width: 1360, Height: 860, MinWidth: 850, MinHeight: 650, AssetServer: &assetserver.Options{Assets: assets}, OnStartup: app.startup, OnShutdown: app.shutdown, Bind: []interface{}{app}, Windows: &windows.Options{
		Theme: windows.Dark,
		CustomTheme: &windows.ThemeSettings{
			DarkModeTitleBar:          0x232323,
			DarkModeTitleBarInactive:  0x232323,
			DarkModeTitleText:         0xc4c4c4,
			DarkModeTitleTextInactive: 0x999999,
			DarkModeBorder:            0x434241,
			DarkModeBorderInactive:    0x434241,
		},
	}}); e != nil {
		log.Fatal(e)
	}
}
