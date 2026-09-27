//go:build !windows

package tray

import "context"

// NativeOpts is a placeholder on non-Windows builds.
type NativeOpts struct {
	AppName    string
	Icons      map[string][]byte
	StatusFunc func() (text string, color string, syncing bool, paused bool)
	OnOpenUI   func()
	OnSyncNow  func()
	OnExit     func()
}

// RunNative is a no-op off Windows; the agent only ships for Windows today.
func RunNative(context.Context, NativeOpts) {}
