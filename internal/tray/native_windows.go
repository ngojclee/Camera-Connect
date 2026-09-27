//go:build windows

package tray

import (
	"context"
	"fmt"
	"time"

	"github.com/getlantern/systray"
)

// NativeOpts wires the native Win32 tray icon to agent state and actions.
type NativeOpts struct {
	AppName    string
	Icons      map[string][]byte // "green"|"orange"|"red"|"gray" -> .ico bytes
	StatusFunc func() (text string, color string, syncing bool, paused bool)
	OnOpenUI   func()
	OnSyncNow  func()
	OnExit     func()
}

// RunNative hosts a real Win32 tray icon — the menu renders with the OS style
// (rounded corners on Windows 11) instead of the old PowerShell WinForms host.
// Blocks until ctx is cancelled or OnExit fires; safe to call on a goroutine,
// systray locks its own OS thread internally.
func RunNative(ctx context.Context, opts NativeOpts) {
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	systray.Run(func() {
		if ico := opts.Icons["green"]; len(ico) > 0 {
			systray.SetIcon(ico)
		}
		systray.SetTitle(opts.AppName)
		systray.SetTooltip(opts.AppName)

		mStatus := systray.AddMenuItem(opts.AppName, "Current status")
		mStatus.Disable()
		systray.AddSeparator()
		mOpen := systray.AddMenuItem("Open UI", "Show the Camera Connect window")
		mSync := systray.AddMenuItem("Sync Now", "Import from connected cameras")
		systray.AddSeparator()
		mExit := systray.AddMenuItem("Exit App", "Stop the agent and remove the icon")

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-mOpen.ClickedCh:
					if opts.OnOpenUI != nil {
						opts.OnOpenUI()
					}
				case <-mSync.ClickedCh:
					if opts.OnSyncNow != nil {
						opts.OnSyncNow()
					}
				case <-mExit.ClickedCh:
					if opts.OnExit != nil {
						opts.OnExit()
					}
					return
				}
			}
		}()

		// Poll status once a second — cheap, and icon state stays current.
		go func() {
			last := ""
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				}
				if opts.StatusFunc == nil {
					continue
				}
				text, color, _, _ := opts.StatusFunc()
				key := color + "|" + text
				if key == last {
					continue
				}
				last = key
				mStatus.SetTitle(fmt.Sprintf("Status: %s", text))
				systray.SetTooltip(fmt.Sprintf("%s — %s", opts.AppName, text))
				if ico := opts.Icons[color]; len(ico) > 0 {
					systray.SetIcon(ico)
				}
			}
		}()
	}, func() {})
}
