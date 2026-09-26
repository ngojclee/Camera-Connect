package store

import (
	"context"
	"testing"
	"time"
)

func TestJobLifecycle(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	id, err := db.EnqueueJob(ctx, KindUpload, `{"files":["a.ARW"]}`)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	jobs, err := db.DueJobs(ctx, time.Now())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("due jobs = %v, err=%v", jobs, err)
	}
	if jobs[0].ID != id || jobs[0].State != JobPending {
		t.Fatalf("job = %+v", jobs[0])
	}

	if err := db.MarkJobRunning(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Running jobs aren't due.
	jobs, _ = db.DueJobs(ctx, time.Now())
	if len(jobs) != 0 {
		t.Fatalf("running job should not be due, got %d", len(jobs))
	}

	// Fail → retry_wait with next_retry in future → not due now.
	if err := db.MarkJobFailed(ctx, id, "network down"); err != nil {
		t.Fatal(err)
	}
	jobs, _ = db.DueJobs(ctx, time.Now())
	if len(jobs) != 0 {
		t.Fatalf("retry_wait job should not be due yet, got %d", len(jobs))
	}
	// But it IS due within the retry window.
	jobs, _ = db.DueJobs(ctx, time.Now().Add(10*time.Minute))
	if len(jobs) != 1 {
		t.Fatalf("retry_wait job should be due in future window, got %d", len(jobs))
	}

	// Requeue → pending → due now.
	if n, err := db.RequeueJobs(ctx, KindUpload); err != nil || n != 1 {
		t.Fatalf("requeue n=%d err=%v", n, err)
	}
	jobs, _ = db.DueJobs(ctx, time.Now())
	if len(jobs) != 1 {
		t.Fatal("requeued job should be due")
	}

	// Done → gone.
	if err := db.MarkJobDone(ctx, id); err != nil {
		t.Fatal(err)
	}
	jobs, _ = db.DueJobs(ctx, time.Now().Add(time.Hour))
	if len(jobs) != 0 {
		t.Fatal("done job should be removed")
	}
}

func TestFileHistoryAndKV(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := db.RecordFile(ctx, "DCIM/a.ARW", "Cam", "p1", 1234, "x/y/a.ARW"); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFile(ctx, "DCIM/a.ARW", "Cam", "p1", 1234, "x/y/a.ARW"); err != nil {
		t.Fatal(err) // upsert should not error
	}
	n, err := db.FileCount(ctx)
	if err != nil || n != 1 {
		t.Fatalf("FileCount = %d err=%v", n, err)
	}

	if err := db.SetKV(ctx, "appdb_revision", "42"); err != nil {
		t.Fatal(err)
	}
	v, err := db.GetKV(ctx, "appdb_revision")
	if err != nil || v != "42" {
		t.Fatalf("kv = %q err=%v", v, err)
	}
	v, _ = db.GetKV(ctx, "missing")
	if v != "" {
		t.Fatalf("missing key should be empty, got %q", v)
	}
}
