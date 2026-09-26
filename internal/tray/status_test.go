package tray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteStatus_WritesAtomicPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "tray_status.json")

	payload := StatusPayload{
		StatusText:     "Sẵn sàng",
		TrayColor:      "green",
		SyncInProgress: false,
		SyncPaused:     false,
		AutoSync:       true,
	}

	if err := WriteStatus(path, payload); err != nil {
		t.Fatalf("WriteStatus() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}

	var got StatusPayload
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal status file: %v", err)
	}

	if got.StatusText != payload.StatusText {
		t.Fatalf("status_text = %q want=%q", got.StatusText, payload.StatusText)
	}
	if got.TrayColor != payload.TrayColor {
		t.Fatalf("tray_color = %q want=%q", got.TrayColor, payload.TrayColor)
	}
	if got.UpdatedAt == "" {
		t.Fatalf("updated_at should be set")
	}
}
