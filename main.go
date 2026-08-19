package main

import (
	"embed"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"immich-desktop-sync/backend"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

func setupLogging() {
	logPath := backend.LogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err == nil {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, f))
		}
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

func main() {
	setupLogging()

	app := application.New(application.Options{
		Name: "Immich Desktop Sync",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	svc := NewApp(app)
	app.RegisterService(application.NewService(svc))

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Immich Desktop Sync",
		Width:            1200,
		Height:           800,
		MinWidth:         800,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(17, 17, 27),
		EnableFileDrop:   true,
	})
	window.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		svc.handleFileDrop(e.Context().DroppedFiles())
	})

	setupTray(app, window)

	app.OnShutdown(svc.shutdown)

	if err := app.Run(); err != nil {
		log.Fatalf("wails: %v", err)
	}
}

func setupTray(app *application.App, window application.Window) {
	tray := app.SystemTray.New()
	tray.SetLabel("Immich Sync")
	tray.SetTooltip("Immich Desktop Sync")
	if len(trayIcon) > 0 {
		tray.SetIcon(trayIcon)
	}
	menu := app.NewMenu()
	menu.Add("Open").OnClick(func(ctx *application.Context) {
		window.Show()
		window.Focus()
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(ctx *application.Context) {
		app.Quit()
	})
	tray.SetMenu(menu)
}
