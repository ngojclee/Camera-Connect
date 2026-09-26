//go:build windows

package main

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	optionswindows "github.com/wailsapp/wails/v2/pkg/options/windows"
)

func applyWailsPlatformOptions(appOptions *options.App) {
	appOptions.Windows = &optionswindows.Options{
		DisablePinchZoom: true,
	}
}
