package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngojclee/camera-connect/internal/config"
	"github.com/ngojclee/camera-connect/internal/store"
)

// fakeRclone simulates `rclone copy <src> <remote>:<path> --files-from f` and
// `rclone lsjson <remote>:<path>/<rel>` against a local "remote" directory.
type fakeRclone struct {
	remoteDir string
	failOn    string // "copy" | "lsjson" | ""
}

func (f *fakeRclone) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	var filesFrom string
	pos := 0
	for i, a := range args {
		if a == "--files-from" && i+1 < len(args) {
			filesFrom = args[i+1]
		}
	}
	switch args[0] {
	case "copy":
		if f.failOn == "copy" {
			return []byte("simulated failure"), fmt.Errorf("exit 1")
		}
		src := args[1]
		remote := args[2]
		remotePath := strings.TrimPrefix(remote, "gdrive:")
		data, err := os.ReadFile(filesFrom)
		if err != nil {
			return nil, err
		}
		for _, rel := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			rel = strings.TrimSpace(rel)
			if rel == "" {
				continue
			}
			s := filepath.Join(src, filepath.FromSlash(rel))
			d := filepath.Join(f.remoteDir, filepath.FromSlash(remotePath), filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(d), 0o755); err != nil {
				return nil, err
			}
			content, err := os.ReadFile(s)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(d, content, 0o644); err != nil {
				return nil, err
			}
		}
		_ = pos
		return []byte("ok"), nil
	case "lsjson":
		if f.failOn == "lsjson" {
			return []byte("not found"), fmt.Errorf("exit 3")
		}
		remoteFile := args[1]
		rel := strings.TrimPrefix(remoteFile, "gdrive:")
		local := filepath.Join(f.remoteDir, filepath.FromSlash(rel))
		info, err := os.Stat(local)
		if err != nil {
			return nil, fmt.Errorf("lsjson missing: %v", err)
		}
		out, _ := json.Marshal([]map[string]any{{"Size": info.Size(), "IsDir": info.IsDir(), "Name": info.Name()}})
		return out, nil
	}
	return nil, fmt.Errorf("unknown rclone command %q", args[0])
}

func setupWorker(t *testing.T, fake *fakeRclone) (*Worker, *store.Store, *config.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.NewManager(t.TempDir())
	_ = cfg.Load()
	w := NewWorker(db, cfg, Options{Logf: func(string, ...any) {}})
	w.rclone = "fake-rclone"
	w.runner = fake.run
	return w, db, cfg, dir
}

func enqueueUpload(t *testing.T, db *store.Store, p UploadJobPayload) int64 {
	t.Helper()
	payload, _ := json.Marshal(p)
	id, err := db.EnqueueJob(context.Background(), store.KindUpload, string(payload))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUploadJob_Success_FinalizesToDest(t *testing.T) {
	remote := t.TempDir()
	fake := &fakeRclone{remoteDir: remote}
	w, db, _, _ := setupWorker(t, fake)
	defer db.Close()

	base := t.TempDir()
	staging := StagingDir(base, "p1")
	rel := "Cam/2025/2025-12-26/DSC0001.ARW"
	staged := filepath.Join(staging, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("raw-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	enqueueUpload(t, db, UploadJobPayload{
		ProfileID: "p1", Remote: "gdrive", RemotePath: "Backup",
		Files: []string{rel}, StagingDir: staging, BasePath: base,
	})

	w.processDue(context.Background())

	// Job done (removed), staged moved to final dest, remote has the file.
	jobs, _ := db.DueJobs(context.Background(), time.Now().Add(time.Hour))
	if len(jobs) != 0 {
		t.Fatalf("job should be done, got %d remaining", len(jobs))
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("staged file should be moved to final dest")
	}
	final := filepath.Join(base, filepath.FromSlash(rel))
	if data, err := os.ReadFile(final); err != nil || string(data) != "raw-bytes" {
		t.Fatalf("final file missing/wrong: %v", err)
	}
	if _, err := os.Stat(filepath.Join(remote, "Backup", filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remote file missing: %v", err)
	}
}

func TestUploadJob_FreeSpace_DeletesStaged(t *testing.T) {
	remote := t.TempDir()
	fake := &fakeRclone{remoteDir: remote}
	w, db, _, _ := setupWorker(t, fake)
	defer db.Close()

	base := t.TempDir()
	staging := StagingDir(base, "p1")
	rel := "Cam/vid.MP4"
	staged := filepath.Join(staging, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(staged), 0o755)
	_ = os.WriteFile(staged, []byte("video"), 0o644)

	enqueueUpload(t, db, UploadJobPayload{
		ProfileID: "p1", Remote: "gdrive", RemotePath: "B", FreeSpace: true,
		Files: []string{rel}, StagingDir: staging, BasePath: base,
	})
	w.processDue(context.Background())

	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("free_space should delete staged file after verified upload")
	}
	if _, err := os.Stat(filepath.Join(remote, "B", "Cam", "vid.MP4")); err != nil {
		t.Error("remote file should exist")
	}
}

func TestUploadJob_Failure_GoesRetryWait(t *testing.T) {
	remote := t.TempDir()
	fake := &fakeRclone{remoteDir: remote, failOn: "copy"}
	w, db, _, _ := setupWorker(t, fake)
	defer db.Close()

	base := t.TempDir()
	staging := StagingDir(base, "p1")
	rel := "a.ARW"
	staged := filepath.Join(staging, rel)
	_ = os.MkdirAll(filepath.Dir(staged), 0o755)
	_ = os.WriteFile(staged, []byte("x"), 0o644)

	enqueueUpload(t, db, UploadJobPayload{
		ProfileID: "p1", Remote: "gdrive", RemotePath: "B",
		Files: []string{rel}, StagingDir: staging, BasePath: base,
	})
	w.processDue(context.Background())

	// Not due now (retry_wait), but due in future window.
	jobs, _ := db.DueJobs(context.Background(), time.Now())
	if len(jobs) != 0 {
		t.Fatal("failed job should not retry immediately")
	}
	jobs, _ = db.DueJobs(context.Background(), time.Now().Add(10*time.Minute))
	if len(jobs) != 1 {
		t.Fatalf("failed job should reappear in retry window, got %d", len(jobs))
	}
	if !strings.Contains(jobs[0].LastError, "rclone copy") {
		t.Fatalf("last_error = %q", jobs[0].LastError)
	}
	// Staged file untouched — never deleted on failure.
	if _, err := os.Stat(staged); err != nil {
		t.Error("staged file must survive failed upload")
	}
}
