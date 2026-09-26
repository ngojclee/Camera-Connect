package tray

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultStatusFileName = "tray_status.json"

// StatusPayload is the shared status snapshot consumed by tray host.
type StatusPayload struct {
	StatusText     string `json:"status_text"`
	TrayColor      string `json:"tray_color"`
	SyncInProgress bool   `json:"sync_in_progress"`
	SyncPaused     bool   `json:"sync_paused"`
	AutoSync       bool   `json:"auto_sync"`
	UpdatedAt      string `json:"updated_at"`
}

// DefaultStatusPath returns %LOCALAPPDATA%\CameraConnect\tray_status.json.
func DefaultStatusPath() (string, error) {
	localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if localAppData == "" {
		return "", fmt.Errorf("LOCALAPPDATA environment variable not set")
	}
	return filepath.Join(localAppData, "CameraConnect", defaultStatusFileName), nil
}

// WriteStatus writes tray status atomically for external tray host processes.
func WriteStatus(path string, payload StatusPayload) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("tray status path is empty")
	}

	payload.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal tray status payload: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create tray status directory: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write tray status temp file: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("remove previous tray status file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace tray status file: %w", err)
	}

	return nil
}
