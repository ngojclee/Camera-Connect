//go:build windows

package main

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	optionswindows "github.com/wailsapp/wails/v2/pkg/options/windows"
)

func applyWailsPlatformOptions(appOptions *options.App) {
	// Fully transparent canvas + translucent window → the frameless shell can
	// paint its own rounded corners while Windows draws the system backdrop
	// (Mica on Windows 11, falls back gracefully on 10). This gives the OS-
	// adaptive look modern apps have instead of a flat opaque rectangle.
	appOptions.Windows = &optionswindows.Options{
		DisablePinchZoom:     true,
		WebviewIsTransparent: true,
		WindowIsTranslucent:  true,
		BackdropType:         optionswindows.Auto,
	}
	// Alpha 0 so the transparent corner regions actually let the backdrop
	// through instead of painting the opaque window color.
	appOptions.BackgroundColour = &options.RGBA{R: 0, G: 0, B: 0, A: 0}
}
