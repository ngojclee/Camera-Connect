package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ngojclee/camera-connect/internal/ipc"
	"github.com/ngojclee/camera-connect/internal/uiapi"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// WailsApp is the Wave-1 backend entrypoint placeholder for future Wails bindings.
// It intentionally reuses the existing command envelope to preserve CLI parity.
type WailsApp struct {
	PipeName string
	service  *uiapi.Service
	ctx      context.Context

	// forceQuit bypasses OnBeforeClose hide-to-tray logic.
	forceQuit atomic.Bool

	// minimizeToTray cached from config; updated on save.
	minimizeToTray atomic.Bool
}

func NewWailsApp(pipeName string) *WailsApp {
	return &WailsApp{
		PipeName: pipeName,
		service:  uiapi.NewService(pipeName),
	}
}

func (a *WailsApp) Startup(ctx context.Context) {
	a.ctx = ctx
	// Fetch initial minimize_to_tray setting from Agent config.
	go a.refreshMinimizeToTray()
}

// --- Window lifecycle methods (called from JS) ---

// HideToTray hides the window to system tray.
func (a *WailsApp) HideToTray() {
	if a.ctx != nil {
		wailsruntime.WindowHide(a.ctx)
	}
}

// MinimiseWindow minimizes the window to the Windows taskbar.
func (a *WailsApp) MinimiseWindow() {
	if a.ctx != nil {
		wailsruntime.WindowMinimise(a.ctx)
	}
}

// ToggleMaximise switches between maximised and normal window state.
func (a *WailsApp) ToggleMaximise() {
	if a.ctx == nil {
		return
	}
	if wailsruntime.WindowIsMaximised(a.ctx) {
		wailsruntime.WindowUnmaximise(a.ctx)
	} else {
		wailsruntime.WindowMaximise(a.ctx)
	}
}

// ShowWindow brings a hidden window back.
func (a *WailsApp) ShowWindow() {
	if a.ctx != nil {
		wailsruntime.WindowShow(a.ctx)
	}
}

// ExitApplication sends shutdown command to Agent, then quits the UI.
func (a *WailsApp) ExitApplication() uiapi.ActionEnvelope {
	result := a.service.ShutdownAgent()
	// Give the Agent a moment to process
	time.Sleep(300 * time.Millisecond)
	a.forceQuit.Store(true)
	if a.ctx != nil {
		wailsruntime.Quit(a.ctx)
	}
	return result
}

// QuitUIOnly closes the UI window without stopping the Agent.
func (a *WailsApp) QuitUIOnly() {
	a.forceQuit.Store(true)
	if a.ctx != nil {
		wailsruntime.Quit(a.ctx)
	}
}

// ShouldPreventClose is used by OnBeforeClose to implement minimize-to-tray.
func (a *WailsApp) ShouldPreventClose() bool {
	if a.forceQuit.Load() {
		return false
	}
	return a.minimizeToTray.Load()
}

// IsAgentAlive does a fast IPC ping to check if the Agent is reachable.
func (a *WailsApp) IsAgentAlive() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	ok, err := ipc.Ping(ctx, a.PipeName)
	return ok && err == nil
}

// LaunchAgent tries to start the Agent process if it's not already running.
func (a *WailsApp) LaunchAgent() map[string]string {
	agentPath := resolveAgentExecutable()
	if agentPath == "" {
		return map[string]string{"ok": "false", "error": "agent executable not found"}
	}

	cmd := exec.Command(agentPath)
	cmd.Dir = filepath.Dir(agentPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // DETACHED_PROCESS
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return map[string]string{"ok": "false", "error": fmt.Sprintf("failed to start agent: %v", err)}
	}
	// Detach — don't wait for the process
	go func() { _ = cmd.Wait() }()

	// Wait a bit for agent to initialize IPC
	time.Sleep(2000 * time.Millisecond)

	alive := a.IsAgentAlive()
	return map[string]string{
		"ok":         fmt.Sprintf("%v", alive),
		"agent_path": agentPath,
		"agent_pid":  fmt.Sprintf("%d", cmd.Process.Pid),
	}
}

// GetMinimizeToTray returns the current minimize-to-tray setting.
func (a *WailsApp) GetMinimizeToTray() bool {
	return a.minimizeToTray.Load()
}

// SetMinimizeToTray updates the local cached value (called after save-config from JS).
func (a *WailsApp) SetMinimizeToTray(enabled bool) {
	a.minimizeToTray.Store(enabled)
}

// --- Original methods ---

func (a *WailsApp) SelectDirectory(title string) string {
	if a.ctx == nil {
		return ""
	}
	dir, _ := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: title,
	})
	return dir
}

func (a *WailsApp) SelectFile(title string, filters string) string {
	if a.ctx == nil {
		return ""
	}

	options := wailsruntime.OpenDialogOptions{
		Title: title,
	}

	if filters != "" {
		options.Filters = []wailsruntime.FileFilter{
			{DisplayName: filters, Pattern: filters},
		}
	}

	file, _ := wailsruntime.OpenFileDialog(a.ctx, options)
	return file
}

func (a *WailsApp) AppInfo() map[string]string {
	return map[string]string{
		"name":      "Camera Connect",
		"version":   Version,
		"pipe_name": a.PipeName,
		"runtime":   "wails",
		"server_ts": time.Now().Format(time.RFC3339),
	}
}

func (a *WailsApp) Ping() uiapi.ActionEnvelope {
	return a.service.Ping()
}

func (a *WailsApp) ExecuteAction(action, payload string) uiapi.ActionEnvelope {
	return a.service.ExecuteAction(action, payload)
}

func (a *WailsApp) ListCameras() uiapi.ActionEnvelope {
	return a.service.ListCameras()
}

// --- internal helpers ---

func (a *WailsApp) refreshMinimizeToTray() {
	time.Sleep(800 * time.Millisecond)
	env := a.service.GetConfig()
	if !env.OK || env.Data == nil {
		return
	}
	if data, ok := env.Data.(map[string]any); ok {
		if val, exists := data["minimize_to_tray"]; exists {
			if b, ok := val.(bool); ok {
				a.minimizeToTray.Store(b)
				log.Printf("[INFO] Loaded minimize_to_tray=%v from agent config", b)
			}
		}
		// "Start minimized to tray" = open straight into the tray with no
		// window popup; a second launch or tray "Open" shows it again.
		if val, exists := data["start_minimized"]; exists {
			if b, ok := val.(bool); ok && b {
				log.Printf("[INFO] start_minimized=true — hiding window to tray")
				wailsruntime.WindowHide(a.ctx)
			}
		}
	}
}

// resolveAgentExecutable finds the agent EXE relative to the UI EXE.
func resolveAgentExecutable() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exePath)
	candidates := []string{
		filepath.Join(dir, "CameraConnectAgent.exe"),
		filepath.Join(dir, "agent.exe"),
		filepath.Join(dir, "CameraConnectAgent"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	// Fallback: check if running in dev mode from cmd/ui
	devAgent := filepath.Join(dir, "..", "agent", "agent.exe")
	if abs, err := filepath.Abs(devAgent); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return strings.TrimSuffix(candidates[0], "")
}
