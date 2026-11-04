package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app, err := NewApp()
	if err != nil { log.Fatal(err) }
	err = wails.Run(&options.App{
		Title: "学习时长统计", Width: 1100, Height: 760,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup: app.startup, OnShutdown: app.shutdown,
		Bind: []interface{}{app},
	})
	if err != nil { log.Fatal(err) }
}
