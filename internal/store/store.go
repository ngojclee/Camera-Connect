// Package store persists sync history and the upload retry queue in SQLite.
// The filesystem remains the source of truth for "is this file synced" —
// this DB is history + durable job state (survives restarts/unplug).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// Job states.
const (
	JobPending   = "pending"
	JobRunning   = "running"
	JobDone      = "done"
	JobFailed    = "failed"
	JobRetryWait = "retry_wait"
)

// Job kinds.
const (
	KindUpload = "upload" // payload: {"files":[...], "remote":"gdrive", "remote_path":"..."}
)

// Job is a durable background task row.
type Job struct {
	ID        int64
	Kind      string
	Payload   string // JSON
	State     string
	Attempts  int
	NextRetry time.Time
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) cache.db inside dir and applies the schema.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "cache.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS files (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  camera_path TEXT NOT NULL,
  camera      TEXT NOT NULL,
  profile     TEXT NOT NULL DEFAULT '',
  size        INTEGER NOT NULL DEFAULT 0,
  sha256      TEXT NOT NULL DEFAULT '',
  dest        TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL DEFAULT 'done',
  synced_at   TEXT NOT NULL,
  UNIQUE(camera_path, camera)
);
CREATE INDEX IF NOT EXISTS idx_files_camera ON files(camera);

CREATE TABLE IF NOT EXISTS jobs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL DEFAULT '{}',
  state      TEXT NOT NULL DEFAULT 'pending',
  attempts   INTEGER NOT NULL DEFAULT 0,
  next_retry TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs(state, next_retry);

CREATE TABLE IF NOT EXISTS kv (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

// ---------- Files (history) ----------

// RecordFile upserts a synced-file history row.
func (s *Store) RecordFile(ctx context.Context, cameraPath, camera, profile string, size int64, dest string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO files (camera_path, camera, profile, size, dest, state, synced_at)
VALUES (?,?,?,?,?,'done',?)
ON CONFLICT(camera_path, camera) DO UPDATE SET
  profile=excluded.profile, size=excluded.size, dest=excluded.dest, synced_at=excluded.synced_at`,
		cameraPath, camera, profile, size, dest, time.Now().UTC().Format(time.RFC3339))
	return err
}

// FileCount returns total synced files recorded.
func (s *Store) FileCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE state='done'`).Scan(&n)
	return n, err
}

// FileRecord is one synced-file history row for the UI.
type FileRecord struct {
	CameraPath string `json:"camera_path"`
	Camera     string `json:"camera"`
	Profile    string `json:"profile"`
	Size       int64  `json:"size"`
	Dest       string `json:"dest"`
	SyncedAt   string `json:"synced_at"`
}

// ListFiles returns the most recent synced files (newest first).
func (s *Store) ListFiles(ctx context.Context, limit int) ([]FileRecord, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT camera_path, camera, profile, size, dest, synced_at
FROM files ORDER BY synced_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FileRecord{}
	for rows.Next() {
		var r FileRecord
		if err := rows.Scan(&r.CameraPath, &r.Camera, &r.Profile, &r.Size, &r.Dest, &r.SyncedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- Jobs (durable retry queue) ----------

// EnqueueJob inserts a pending job and returns its ID.
func (s *Store) EnqueueJob(ctx context.Context, kind, payload string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx, `
INSERT INTO jobs (kind, payload, state, created_at, updated_at) VALUES (?,?,?,?,?)`,
		kind, payload, JobPending, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DueJobs returns pending/retry-wait jobs whose next_retry has passed.
func (s *Store) DueJobs(ctx context.Context, now time.Time) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, kind, payload, state, attempts, next_retry, last_error, created_at, updated_at
FROM jobs
WHERE state IN (?, ?) AND (next_retry = '' OR next_retry <= ?)
ORDER BY id`, JobPending, JobRetryWait, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		var j Job
		var nr, created, updated string
		if err := rows.Scan(&j.ID, &j.Kind, &j.Payload, &j.State, &j.Attempts, &nr, &j.LastError, &created, &updated); err != nil {
			return nil, err
		}
		j.NextRetry, _ = time.Parse(time.RFC3339, nr)
		j.CreatedAt, _ = time.Parse(time.RFC3339, created)
		j.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, j)
	}
	return out, rows.Err()
}

// MarkJobRunning marks a job in-progress.
func (s *Store) MarkJobRunning(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state=?, attempts=attempts+1, updated_at=? WHERE id=?`,
		JobRunning, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// MarkJobDone removes a completed job.
func (s *Store) MarkJobDone(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id=?`, id)
	return err
}

// MarkJobFailed schedules a retry with backoff (attempts^2 minutes, max 30).
func (s *Store) MarkJobFailed(ctx context.Context, id int64, errText string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
UPDATE jobs SET state=?, last_error=?,
  next_retry=?, attempts=attempts+1, updated_at=?
WHERE id=?`,
		JobRetryWait, errText,
		now.Add(backoffFor(id)).Format(time.RFC3339),
		now.Format(time.RFC3339), id)
	return err
}

// backoffFor is a placeholder until attempts-aware backoff is needed;
// fixed 5-minute retry keeps it simple and predictable.
func backoffFor(_ int64) time.Duration { return 5 * time.Minute }

// RequeueJobs forces all retry_wait/failed jobs back to pending now.
func (s *Store) RequeueJobs(ctx context.Context, kind string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state=?, next_retry='', updated_at=? WHERE kind=? AND state IN (?, ?)`,
		JobPending, time.Now().UTC().Format(time.RFC3339), kind, JobRetryWait, JobFailed)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// JobCounts returns (pendingOrWaiting, running, failed-ish) for UI badges.
func (s *Store) JobCounts(ctx context.Context) (pending int, failed int, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE state IN (?, ?, ?)`,
		JobPending, JobRunning, JobRetryWait).Scan(&pending)
	if err != nil {
		return 0, 0, err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE state = ?`, JobFailed).Scan(&failed)
	return pending, failed, err
}

// ---------- KV ----------

// SetKV stores a small persistent value.
func (s *Store) SetKV(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

// GetKV fetches a value ("" if absent).
func (s *Store) GetKV(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}
