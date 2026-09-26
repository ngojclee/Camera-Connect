package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ngojclee/camera-connect/internal/config"
	"github.com/ngojclee/camera-connect/internal/detect"
)

func TestRenderTemplate(t *testing.T) {
	date := time.Date(2025, 12, 5, 10, 30, 0, 0, time.Local)
	cases := []struct {
		tmpl, want string
	}{
		{"{camera}/{yyyy}/{yyyy}-{mm}-{dd}", `ZV-E10\2025\2025-12-05`},
		{"{yyyy}-{mm}-{dd}", `2025-12-05`},
		{"{camera}/{yyyy}/{m}/{d}", `ZV-E10\2025\12\5`},
		{"Video/{camera}/{yyyy}/{mm}", `Video\ZV-E10\2025\12`},
		{"{type}/{date}", `Photo\2025-12-05`},
		{"{camera}/{yy}/{mm}{dd}", `ZV-E10\25\1205`},
		{"{year}/{month}/{day}", `2025\12\05`},
	}
	for _, c := range cases {
		got := RenderTemplate(c.tmpl, "ZV-E10", "photo", date)
		if got != c.want {
			t.Errorf("RenderTemplate(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

// makeCard builds a fake SD card dir with DCIM + PRIVATE layout.
func makeCard(t *testing.T) string {
	t.Helper()
	card := t.TempDir()
	dcim := filepath.Join(card, "DCIM", "2025-12-26")
	if err := os.MkdirAll(dcim, 0o755); err != nil {
		t.Fatal(err)
	}
	clip := filepath.Join(card, "PRIVATE", "M4ROOT", "CLIP")
	if err := os.MkdirAll(clip, 0o755); err != nil {
		t.Fatal(err)
	}
	thmbnl := filepath.Join(card, "PRIVATE", "M4ROOT", "THMBNL")
	if err := os.MkdirAll(thmbnl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcim, "DSC0001.ARW"), []byte("rawdata-1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcim, "DSC0002.JPG"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dcim, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clip, "C0001.MP4"), []byte("video-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clip, "C0001M01.XML"), []byte("<x/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thmbnl, "C0001.THM"), []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	return card + string(filepath.Separator)
}

func makeProfileConfig(t *testing.T, basePath string) *config.Manager {
	t.Helper()
	mgr := config.NewManager(t.TempDir())
	if err := mgr.Load(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateShared(func(s *config.SharedConfig) {
		s.Profiles = []config.Profile{{
			ID:            "p1",
			Name:          "Test",
			BasePath:      basePath,
			PhotoTemplate: "{camera}/{yyyy}/{yyyy}-{mm}-{dd}",
			VideoTemplate: "{camera}/{yyyy}/{yyyy}-{mm}-{dd}",
		}}
		s.ActiveProfile = "p1"
		s.General.SyncMode = "copy"
	}); err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestSyncDevice_CopiesAndSkips(t *testing.T) {
	card := makeCard(t)
	dest := t.TempDir()
	mgr := makeProfileConfig(t, dest)

	dev := detect.Device{
		ID: "ms:" + card, Model: "TestCam", Mode: detect.ModeMassStorage, DriveLetter: card,
	}
	var uploaded []string
	deps := &Deps{
		Config:        mgr,
		PendingUpload: func(_ *config.Profile, rel, staged string) { uploaded = append(uploaded, staged) },
		Logf:          func(string, ...any) {},
	}

	res, err := SyncDevice(context.Background(), dev, deps)
	if err != nil {
		t.Fatalf("SyncDevice: %v", err)
	}
	// ARW + JPG + MP4 pass filetype filter; notes.txt filtered out.
	if res.Downloaded != 3 {
		t.Fatalf("Downloaded = %d, want 3", res.Downloaded)
	}
	if res.Failed != 0 {
		t.Fatalf("Failed = %d", res.Failed)
	}
	// Backup disabled in test profile → writes go straight to dest, no staging upload.
	if len(res.DestPaths) != 3 || len(uploaded) != 0 {
		t.Fatalf("DestPaths=%d uploaded=%d", len(res.DestPaths), len(uploaded))
	}

	// Verify layout: DCIM/2025-12-26 date folder → TestCam/2025/2025-12-26/
	wantARW := filepath.Join(dest, "TestCam", "2025", "2025-12-26", "DSC0001.ARW")
	if _, err := os.Stat(wantARW); err != nil {
		t.Fatalf("expected %s to exist: %v", wantARW, err)
	}
	// Second run: everything skipped, nothing downloaded.
	res2, err := SyncDevice(context.Background(), dev, deps)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Downloaded != 0 || res2.Skipped != 3 {
		t.Fatalf("second pass: downloaded=%d skipped=%d, want 0/3", res2.Downloaded, res2.Skipped)
	}
}

// Backup-enabled profiles write into _staging and enqueue uploads.
func TestSyncDevice_BackupEnabled_StagesFiles(t *testing.T) {
	card := makeCard(t)
	dest := t.TempDir()
	mgr := makeProfileConfig(t, dest)
	if err := mgr.UpdateShared(func(s *config.SharedConfig) {
		s.Profiles[0].Backup = config.BackupConfig{
			Enabled: true, RemoteName: "gdrive", RemotePath: "CamBackup",
		}
	}); err != nil {
		t.Fatal(err)
	}

	dev := detect.Device{ID: "ms:" + card, Model: "Cam", Mode: detect.ModeMassStorage, DriveLetter: card}
	var staged []string
	deps := &Deps{
		Config:        mgr,
		PendingUpload: func(_ *config.Profile, rel, path string) { staged = append(staged, path) },
		Logf:          func(string, ...any) {},
	}
	res, err := SyncDevice(context.Background(), dev, deps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Downloaded != 3 || len(staged) != 3 {
		t.Fatalf("Downloaded=%d staged=%d, want 3/3", res.Downloaded, len(staged))
	}
	// Files landed under _staging/<profile>/<destRel>, not in final dest.
	stagedARW := filepath.Join(dest, "_staging", "p1", "Cam", "2025", "2025-12-26", "DSC0001.ARW")
	if _, err := os.Stat(stagedARW); err != nil {
		t.Fatalf("expected staged file %s: %v", stagedARW, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "Cam", "2025", "2025-12-26", "DSC0001.ARW")); !os.IsNotExist(err) {
		t.Error("final dest should NOT have file until upload verified")
	}
	// Second sync: staged file already exists → skipped, no re-copy.
	res2, err := SyncDevice(context.Background(), dev, deps)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Downloaded != 0 {
		t.Fatalf("staged file should be skipped, downloaded=%d", res2.Downloaded)
	}
}

func TestSyncDevice_MoveDeletesAfterVerifiedCopy(t *testing.T) {
	card := makeCard(t)
	dest := t.TempDir()
	mgr := makeProfileConfig(t, dest)
	if err := mgr.UpdateShared(func(s *config.SharedConfig) {
		s.General.SyncMode = "move"
	}); err != nil {
		t.Fatal(err)
	}

	dev := detect.Device{ID: "ms:" + card, Model: "Cam", Mode: detect.ModeMassStorage, DriveLetter: card}
	deps := &Deps{Config: mgr, Logf: func(string, ...any) {}}

	res, err := SyncDevice(context.Background(), dev, deps)
	if err != nil {
		t.Fatal(err)
	}
	if res.DeletedSrc != 3 {
		t.Fatalf("DeletedSrc = %d, want 3", res.DeletedSrc)
	}
	// Sources gone, including sidecars.
	if _, err := os.Stat(filepath.Join(card, "DCIM", "2025-12-26", "DSC0001.ARW")); !os.IsNotExist(err) {
		t.Error("ARW should be deleted in move mode")
	}
	if _, err := os.Stat(filepath.Join(card, "PRIVATE", "M4ROOT", "CLIP", "C0001M01.XML")); !os.IsNotExist(err) {
		t.Error("XML sidecar should be deleted")
	}
	if _, err := os.Stat(filepath.Join(card, "PRIVATE", "M4ROOT", "THMBNL", "C0001.THM")); !os.IsNotExist(err) {
		t.Error("THM thumbnail should be deleted")
	}
	// Dest intact — MP4 has no date folder so it lands under mtime's date dir.
	found := false
	_ = filepath.Walk(dest, func(p string, _ os.FileInfo, _ error) error {
		if filepath.Base(p) == "C0001.MP4" {
			found = true
		}
		return nil
	})
	if !found {
		t.Error("MP4 should exist in dest")
	}
}

func TestSyncDevice_NoProfile(t *testing.T) {
	card := makeCard(t)
	mgr := config.NewManager(t.TempDir())
	_ = mgr.Load()
	dev := detect.Device{ID: "ms:" + card, Model: "Cam", Mode: detect.ModeMassStorage, DriveLetter: card}
	_, err := SyncDevice(context.Background(), dev, &Deps{Config: mgr})
	if err == nil {
		t.Fatal("expected error when no profile configured")
	}
}
