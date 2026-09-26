// Package backup implements the upload pipeline v2:
//
//	camera → <base>/_staging/<profile>/<destRel>  (same volume, crash-safe .part copy)
//	      → rclone copy --files-from <batch>     (scoped — never the whole base root)
//	      → rclone lsjson verify (size+name)      (remote must contain every file)
//	      → move staged → final dest             (or delete staged when free_space)
//
// Jobs live in the SQLite `jobs` table → survive restarts. Retries use the
// store's retry_wait/backoff; the worker is serialized (one rclone at a time).
package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ngojclee/camera-connect/internal/config"
	"github.com/ngojclee/camera-connect/internal/store"
)

// UploadJobPayload is the JSON payload stored in jobs.payload.
type UploadJobPayload struct {
	ProfileID  string   `json:"profile_id"`
	Remote     string   `json:"remote"`      // rclone remote name, e.g. "gdrive"
	RemotePath string   `json:"remote_path"` // e.g. "CameraBackup"
	FreeSpace  bool     `json:"free_space"`  // delete staged file after verified upload
	Files      []string `json:"files"`       // rel paths inside staging dir
	StagingDir string   `json:"staging_dir"` // absolute staging root for this batch
	BasePath   string   `json:"base_path"`   // profile base path (finalize target)
}

// Worker polls the jobs table and runs uploads serially.
type Worker struct {
	db         *store.Store
	cfg        *config.Manager
	logf       func(string, ...any)
	rclone     string // resolved rclone path
	rcloneConf string // optional --config path

	mu       sync.Mutex
	running  bool
	wake     chan struct{}
	interval time.Duration
	timeout  time.Duration
	// runner executes rclone; injectable for tests. Defaults to exec.
	runner func(ctx context.Context, bin string, args ...string) ([]byte, error)
}

// Options configures the upload worker.
type Options struct {
	Interval   time.Duration // poll period (default 10s)
	JobTimeout time.Duration // per-rclone timeout (default 2h)
	RclonePath string        // explicit rclone.exe override
	RcloneConf string        // explicit rclone.conf override
	Logf       func(string, ...any)
}

// NewWorker creates an upload worker. Returns nil if db is nil.
func NewWorker(db *store.Store, cfg *config.Manager, opts Options) *Worker {
	if db == nil {
		return nil
	}
	w := &Worker{
		db:         db,
		cfg:        cfg,
		wake:       make(chan struct{}, 1),
		interval:   opts.Interval,
		timeout:    opts.JobTimeout,
		rclone:     opts.RclonePath,
		rcloneConf: opts.RcloneConf,
		logf:       opts.Logf,
	}
	if w.interval <= 0 {
		w.interval = 10 * time.Second
	}
	if w.timeout <= 0 {
		w.timeout = 2 * time.Hour
	}
	if w.logf == nil {
		w.logf = log.Printf
	}
	return w
}

// Wake nudges the worker to check the queue now (e.g., after enqueue).
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run processes upload jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.processDue(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
			w.processDue(ctx)
		case <-time.After(w.interval):
			w.processDue(ctx)
		}
	}
}

// processDue drains due upload jobs one at a time (serialized).
func (w *Worker) processDue(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.running = false
		w.mu.Unlock()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		jobs, err := w.db.DueJobs(ctx, time.Now())
		if err != nil {
			w.logf("[WARN] upload queue read failed: %v", err)
			return
		}
		var job *store.Job
		for i := range jobs {
			if jobs[i].Kind == store.KindUpload {
				job = &jobs[i]
				break
			}
		}
		if job == nil {
			return
		}
		w.runJob(ctx, *job)
	}
}

// runJob executes one upload batch: rclone copy → verify → finalize.
func (w *Worker) runJob(ctx context.Context, job store.Job) {
	_ = w.db.MarkJobRunning(ctx, job.ID)

	var p UploadJobPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		w.fail(ctx, job.ID, fmt.Sprintf("corrupt payload: %v", err))
		return
	}
	if len(p.Files) == 0 {
		_ = w.db.MarkJobDone(ctx, job.ID)
		return
	}

	// Drop files whose staged copy vanished but already reached final dest
	// (crash between verify and finalize) — treat as done, keep the rest.
	var remaining []string
	for _, rel := range p.Files {
		staged := filepath.Join(p.StagingDir, filepath.FromSlash(rel))
		if _, err := os.Stat(staged); err == nil {
			remaining = append(remaining, rel)
			continue
		}
		final := filepath.Join(p.BasePath, filepath.FromSlash(rel))
		if _, err := os.Stat(final); err == nil {
			continue // already finalized — nothing to do
		}
		w.logf("[WARN] staged file missing and no final copy: %s", rel)
	}
	if len(remaining) == 0 {
		_ = w.db.MarkJobDone(ctx, job.ID)
		return
	}
	p.Files = remaining

	rcloneBin := w.resolveRclone()
	if rcloneBin == "" {
		if w.runner != nil {
			rcloneBin = "rclone" // injected runner doesn't need a real binary
		} else {
			w.fail(ctx, job.ID, "rclone executable not found (set local.rclone_path or place rclone.exe next to the app)")
			return
		}
	}

	remote := p.Remote + ":" + strings.Trim(p.RemotePath, "/")
	if err := w.uploadBatch(ctx, rcloneBin, p, remote); err != nil {
		w.logf("[ERROR] upload job %d failed: %v", job.ID, err)
		w.fail(ctx, job.ID, err.Error())
		return
	}
	if err := w.verifyBatch(ctx, rcloneBin, p, remote); err != nil {
		w.logf("[ERROR] upload job %d verify failed: %v", job.ID, err)
		w.fail(ctx, job.ID, "verify: "+err.Error())
		return
	}

	// Verified remotely → finalize locally.
	for _, rel := range p.Files {
		staged := filepath.Join(p.StagingDir, filepath.FromSlash(rel))
		if p.FreeSpace {
			if err := os.Remove(staged); err != nil {
				w.logf("[WARN] free-space delete failed %s: %v", rel, err)
			}
			continue
		}
		final := filepath.Join(p.BasePath, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
			w.logf("[WARN] finalize mkdir failed %s: %v", rel, err)
			continue
		}
		if err := os.Rename(staged, final); err != nil {
			w.logf("[WARN] finalize move failed %s → %s: %v", staged, final, err)
		}
	}
	pruneEmptyDirs(p.StagingDir)
	_ = w.db.MarkJobDone(ctx, job.ID)
	w.logf("[INFO] upload job %d done: %d file(s) → %s", job.ID, len(p.Files), remote)
}

func (w *Worker) fail(ctx context.Context, id int64, why string) {
	if err := w.db.MarkJobFailed(ctx, id, why); err != nil {
		w.logf("[WARN] mark job failed: %v", err)
	}
}

// uploadBatch runs `rclone copy <staging> <remote> --files-from <list>`.
func (w *Worker) uploadBatch(ctx context.Context, rcloneBin string, p UploadJobPayload, remote string) error {
	listFile, err := w.writeFilesFrom(p)
	if err != nil {
		return err
	}
	defer os.Remove(listFile)

	args := []string{"copy", p.StagingDir, remote,
		"--files-from", listFile,
		"--create-empty-src-dirs",
		"--stats-one-line", "--stats", "30s", "-v",
	}
	args = w.confArgs(args)
	out, err := w.exec(ctx, rcloneBin, args...)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("rclone copy: %v — %s", err, tailLines(string(out), 5))
	}
	return nil
}

// verifyBatch runs `rclone lsjson` on remote paths to confirm each file
// exists remotely with the right size before any local finalize/delete.
func (w *Worker) verifyBatch(ctx context.Context, rcloneBin string, p UploadJobPayload, remote string) error {
	for _, rel := range p.Files {
		staged := filepath.Join(p.StagingDir, filepath.FromSlash(rel))
		info, err := os.Stat(staged)
		if err != nil {
			return fmt.Errorf("staged file vanished mid-verify: %s", rel)
		}
		remoteFile := remote + "/" + rel
		args := []string{"lsjson", remoteFile}
		args = w.confArgs(args)
		out, err := w.exec(ctx, rcloneBin, args...)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("remote missing %s: %v — %s", rel, err, tailLines(string(out), 3))
		}
		var entries []struct {
			Size  int64 `json:"Size"`
			IsDir bool  `json:"IsDir"`
		}
		if err := json.Unmarshal(out, &entries); err != nil || len(entries) == 0 {
			return fmt.Errorf("remote %s unreadable: %s", rel, string(out))
		}
		if entries[0].IsDir || entries[0].Size != info.Size() {
			return fmt.Errorf("remote %s size %d != local %d", rel, entries[0].Size, info.Size())
		}
	}
	return nil
}

// exec runs rclone via the injectable runner (real exec by default).
func (w *Worker) exec(ctx context.Context, bin string, args ...string) ([]byte, error) {
	if w.runner != nil {
		return w.runner(ctx, bin, args...)
	}
	return exec.CommandContext(ctx, bin, args...).CombinedOutput()
}

func (w *Worker) confArgs(args []string) []string {
	conf := strings.TrimSpace(w.rcloneConf)
	if conf == "" {
		return args
	}
	// Fall back to rclone's default config when the vault file hasn't been
	// pulled yet — an explicit --config pointing at a missing file would
	// break remotes that exist in the default location.
	if _, err := os.Stat(conf); err != nil {
		return args
	}
	return append(args, "--config", conf)
}

// writeFilesFrom dumps the batch file list to a temp file for --files-from.
func (w *Worker) writeFilesFrom(p UploadJobPayload) (string, error) {
	f, err := os.CreateTemp("", "cameraconnect-filesfrom-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	for _, rel := range p.Files {
		if _, err := f.WriteString(filepath.ToSlash(rel) + "\n"); err != nil {
			return "", err
		}
	}
	return f.Name(), nil
}

// resolveRclone finds rclone.exe: config override → next to exe → PATH.
func (w *Worker) resolveRclone() string {
	if w.rclone != "" {
		if _, err := os.Stat(w.rclone); err == nil {
			return w.rclone
		}
	}
	if local := w.cfg.Local(); strings.TrimSpace(local.RclonePath) != "" {
		if _, err := os.Stat(local.RclonePath); err == nil {
			return local.RclonePath
		}
	}
	if exe, err := os.Executable(); err == nil {
		for _, name := range []string{"rclone.exe", "rclone"} {
			cand := filepath.Join(filepath.Dir(exe), name)
			if _, err := os.Stat(cand); err == nil {
				return cand
			}
		}
	}
	if p, err := exec.LookPath("rclone"); err == nil {
		return p
	}
	return ""
}

// pruneEmptyDirs removes empty subdirs under root (depth-first).
func pruneEmptyDirs(root string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == root {
			return nil
		}
		entries, _ := os.ReadDir(path)
		if len(entries) == 0 {
			_ = os.Remove(path)
		}
		return nil
	})
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// StagingDir returns <basePath>/_staging/<profileID>.
func StagingDir(basePath, profileID string) string {
	return filepath.Join(basePath, "_staging", profileID)
}
