package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_LegacyPythonFixture verifies Go can parse the Python config shape.
func TestLoad_LegacyPythonFixture(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "CameraConnectConfig.yaml")

	legacyYAML := `cameras:
  - name: Sony NEX-5R
    protocol: MTP
    usb_pid: "0x0C00"
destination:
  base_path: 'F:/2.Studio/Products_LuxeClaw'
  photo_template: '{camera}/{yyyy}/{yyyy}-{mm}-{dd}'
  video_template: '{camera}/{yyyy}/{yyyy}-{mm}-{dd}'
file_types:
  photos: [ARW, JPG, JPEG, HEIF]
  videos: [MP4, MTS, AVCHD]
general:
  scan_mode: once
  poll_interval: 3
  sync_mode: copy
  overwrite_existing: true
  start_with_windows: true
  minimize_to_tray: true
  startup_auto_sync: false
backup:
  enabled: true
  remote_name: gdrive
  remote_path: Backup/Photos
  delete_local_after_upload: false
notifications:
  on_camera_connect: true
  on_copy_complete: batch
`
	if err := os.WriteFile(legacyPath, []byte(legacyYAML), 0o644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	mgr := NewManager(tmpDir)
	migrated, source, err := mgr.MigrateFromLegacyPaths([]string{legacyPath})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !migrated {
		t.Fatal("expected migrated=true")
	}
	if source != legacyPath {
		t.Fatalf("source = %q", source)
	}

	shared := mgr.Shared()
	if !shared.General.StartWithWindows {
		t.Error("start_with_windows should be true")
	}
	if shared.General.PollInterval != 3 {
		t.Errorf("poll_interval = %d", shared.General.PollInterval)
	}
	if len(shared.Profiles) != 1 {
		t.Fatalf("expected 1 migrated profile, got %d", len(shared.Profiles))
	}
	p := shared.Profiles[0]
	if p.ID != "default" {
		t.Errorf("profile id = %q", p.ID)
	}
	if p.BasePath != "F:/2.Studio/Products_LuxeClaw" {
		t.Errorf("profile base_path = %q", p.BasePath)
	}
	if !p.Backup.Enabled {
		t.Error("profile backup should be enabled")
	}
	if p.Backup.RemoteName != "gdrive" {
		t.Errorf("remote_name = %q", p.Backup.RemoteName)
	}
	if p.Backup.FreeSpace {
		t.Error("free_space must be forced OFF on migration (safe default)")
	}
	if shared.ActiveProfile != "default" {
		t.Errorf("active_profile = %q", shared.ActiveProfile)
	}
	if len(shared.FileTypes.Photos) != 4 {
		t.Errorf("photos len = %d", len(shared.FileTypes.Photos))
	}

	// Legacy file must be backed up
	backups, err := filepath.Glob(filepath.Join(tmpDir, "legacy_backup_*_CameraConnectConfig.yaml"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected 1 legacy backup, got %v err=%v", backups, err)
	}
}

// TestSaveLoad_Roundtrip verifies shared+local survive save→load.
func TestSaveLoad_Roundtrip(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir)

	if err := mgr.UpdateShared(func(s *SharedConfig) {
		s.General.AutoSync = true
		s.ActiveProfile = "studio"
		s.Profiles = []Profile{{
			ID:            "studio",
			Name:          "Studio",
			BasePath:      `\\NAS\Photos`,
			PhotoTemplate: "{camera}/{yyyy}/{mm}",
			Backup:        BackupConfig{Enabled: true, RemoteName: "gdrive", RemotePath: "Cam"},
		}}
	}); err != nil {
		t.Fatalf("update shared: %v", err)
	}
	if err := mgr.UpdateLocal(func(l *LocalConfig) {
		l.ProfilePaths["studio"] = `D:\LocalOverride`
	}); err != nil {
		t.Fatalf("update local: %v", err)
	}

	mgr2 := NewManager(tmpDir)
	if err := mgr2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	shared := mgr2.Shared()
	if !shared.General.AutoSync {
		t.Error("auto_sync should survive roundtrip")
	}
	if len(shared.Profiles) != 1 || shared.Profiles[0].ID != "studio" {
		t.Fatalf("profiles = %+v", shared.Profiles)
	}
	// Machine-local override wins over shared base_path
	if got := mgr2.ResolvedBasePath("studio"); got != `D:\LocalOverride` {
		t.Errorf("resolved base_path = %q, want local override", got)
	}
}

// TestLoad_MissingFiles_UsesDefaults verifies defaults when no config exists.
func TestLoad_MissingFiles_UsesDefaults(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	shared := mgr.Shared()
	if !shared.General.MinimizeToTray {
		t.Error("default minimize_to_tray should be true")
	}
	if shared.General.PollInterval != 3 {
		t.Errorf("default poll_interval = %d", shared.General.PollInterval)
	}
	if len(shared.FileTypes.Photos) == 0 {
		t.Error("default photo extensions should be non-empty")
	}
}

// TestMigrate_SkipsWhenSharedExists verifies no overwrite of existing config.
func TestMigrate_SkipsWhenSharedExists(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "legacy", "CameraConnectConfig.yaml")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("general:\n  auto_sync: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tmpDir)
	if err := mgr.UpdateShared(func(s *SharedConfig) { s.General.AutoSync = false }); err != nil {
		t.Fatalf("write target config: %v", err)
	}

	migrated, _, err := mgr.MigrateFromLegacyPaths([]string{legacyPath})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if migrated {
		t.Fatal("expected migrated=false when shared config exists")
	}
	if mgr.Shared().General.AutoSync {
		t.Fatal("existing config must be preserved")
	}
}

// TestIsFileTypeAllowed checks extension filtering.
func TestIsFileTypeAllowed(t *testing.T) {
	mgr := NewManager(t.TempDir())
	if err := mgr.Load(); err != nil {
		t.Fatal(err)
	}
	if !mgr.IsFileTypeAllowed("", "IMG_1234.ARW") {
		t.Error("ARW should be allowed")
	}
	if !mgr.IsFileTypeAllowed("", "clip.mp4") {
		t.Error("mp4 (lowercase) should be allowed")
	}
	if mgr.IsFileTypeAllowed("", "notes.txt") {
		t.Error("txt should not be allowed")
	}
}

// TestResolvedBasePath_NoOverride uses shared base_path.
func TestResolvedBasePath_NoOverride(t *testing.T) {
	mgr := NewManager(t.TempDir())
	_ = mgr.UpdateShared(func(s *SharedConfig) {
		s.Profiles = []Profile{{ID: "p1", BasePath: `E:\Shared`}}
	})
	if got := mgr.ResolvedBasePath("p1"); got != `E:\Shared` {
		t.Errorf("resolved = %q", got)
	}
	if got := mgr.ResolvedBasePath("missing"); got != "" {
		t.Errorf("unknown profile should resolve to empty, got %q", got)
	}
}

// TestLegacyPaths_Unique ensures no duplicate candidates.
func TestLegacyPaths_Unique(t *testing.T) {
	paths := LegacyPaths()
	seen := map[string]bool{}
	for _, p := range paths {
		key := strings.ToLower(filepath.Clean(p))
		if seen[key] {
			t.Errorf("duplicate legacy path: %s", p)
		}
		seen[key] = true
	}
}
