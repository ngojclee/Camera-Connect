package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

func launchWailsRuntime(pipeName string) error {
	assetsFS, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return fmt.Errorf("failed to get sub FS for dist: %w", err)
	}

	app := NewWailsApp(pipeName)
	appOptions := &options.App{
		Title:     "Camera Connect",
		MinWidth:  980,
		MinHeight: 700,
		Width:     1240,
		Height:    900,
		AssetServer: &assetserver.Options{
			Assets: assetsFS,
		},
		Bind: []interface{}{
			app,
		},
		OnStartup: app.Startup,
		// Hide window to tray on close (X button) when minimize_to_tray is enabled.
		// If forceQuit is set (via ExitApplication/QuitUIOnly), allow close.
		OnBeforeClose: func(ctx context.Context) bool {
			if app.ShouldPreventClose() {
				log.Println("[INFO] Close intercepted — hiding to tray (minimize_to_tray enabled)")
				wailsruntime.WindowHide(app.ctx)
				return true // prevent close
			}
			return false // allow normal close
		},
		// When a second instance is launched (e.g., tray "Open UI"), show the existing window.
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "CameraConnectUI-9f8a7b6c",
			OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
				log.Println("[INFO] Second UI instance detected — showing existing window")
				if app.ctx != nil {
					wailsruntime.WindowShow(app.ctx)
					wailsruntime.WindowUnminimise(app.ctx)
				}
			},
		},
	}
	applyWailsPlatformOptions(appOptions)

	log.Printf("[INFO] Launching embedded Wails runtime")
	if err := wails.Run(appOptions); err != nil {
		return fmt.Errorf("run embedded wails app: %w", err)
	}
	return nil
}
