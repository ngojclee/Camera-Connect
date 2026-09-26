package coordinator

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyncWorker_ProcessesJobsOneAtATime(t *testing.T) {
	bus := NewEventBus(16)
	state := NewAppState("test-machine", "dev")
	worker := NewSyncWorker(16, state, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go worker.Run(ctx)

	var wg sync.WaitGroup
	var running int32
	var maxRunning int32

	for i := 0; i < 3; i++ {
		wg.Add(1)
		err := worker.Enqueue(SyncJob{
			Name: "job",
			Execute: func(ctx context.Context) error {
				defer wg.Done()
				cur := atomic.AddInt32(&running, 1)
				for {
					prev := atomic.LoadInt32(&maxRunning)
					if cur <= prev || atomic.CompareAndSwapInt32(&maxRunning, prev, cur) {
						break
					}
				}

				select {
				case <-ctx.Done():
					atomic.AddInt32(&running, -1)
					return ctx.Err()
				case <-time.After(30 * time.Millisecond):
				}

				atomic.AddInt32(&running, -1)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("enqueue failed: %v", err)
		}
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("jobs did not complete in time")
	}

	if got := atomic.LoadInt32(&maxRunning); got != 1 {
		t.Fatalf("max concurrent running jobs = %d, want 1", got)
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		snap := state.Snapshot()
		if !snap.SyncInProgress {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("sync state should be false after all jobs completed")
}

func TestSyncWorker_RejectsInvalidJobs(t *testing.T) {
	worker := NewSyncWorker(1, NewAppState("test-machine", "dev"), NewEventBus(4))

	if err := worker.Enqueue(SyncJob{}); !errors.Is(err, ErrInvalidJob) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidJob)
	}
}

func TestSyncWorker_ReturnsQueueFull(t *testing.T) {
	worker := NewSyncWorker(1, NewAppState("test-machine", "dev"), NewEventBus(4))

	// Fill queue without starting the worker run loop.
	if err := worker.Enqueue(SyncJob{Name: "a", Execute: func(context.Context) error { return nil }}); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	if err := worker.Enqueue(SyncJob{Name: "b", Execute: func(context.Context) error { return nil }}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v, want %v", err, ErrQueueFull)
	}
}

func TestSyncWorker_WatchdogAlertsOnTimeout(t *testing.T) {
	alertCh := make(chan WatchdogAlert, 1)
	wd := NewWatchdog(5*time.Millisecond, func(alert WatchdogAlert) {
		select {
		case alertCh <- alert:
		default:
		}
	})

	worker := NewSyncWorker(2, NewAppState("test-machine", "dev"), NewEventBus(4))
	worker.SetWatchdog(wd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wd.Run(ctx)
	go worker.Run(ctx)

	err := worker.Enqueue(SyncJob{
		Name:           "slow-sync",
		OperationID:    "op-timeout-1",
		MaxRunDuration: 20 * time.Millisecond,
		Execute: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(90 * time.Millisecond):
				return nil
			}
		},
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	select {
	case alert := <-alertCh:
		if alert.OperationID != "op-timeout-1" {
			t.Fatalf("alert.OperationID = %q, want %q", alert.OperationID, "op-timeout-1")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected watchdog timeout alert")
	}
}

func TestSyncWorker_PauseResumeBlocksQueueProcessing(t *testing.T) {
	bus := NewEventBus(16)
	state := NewAppState("test-machine", "dev")
	worker := NewSyncWorker(4, state, bus)

	worker.Pause()
	if !worker.IsPaused() {
		t.Fatal("expected worker to be paused")
	}
	if !state.Snapshot().SyncPaused {
		t.Fatal("expected app state sync_paused to be true after pause")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)

	executedCh := make(chan struct{}, 1)
	err := worker.Enqueue(SyncJob{
		Name: "paused-job",
		Execute: func(ctx context.Context) error {
			select {
			case executedCh <- struct{}{}:
			default:
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	select {
	case <-executedCh:
		t.Fatal("job should not run while worker is paused")
	case <-time.After(120 * time.Millisecond):
	}

	worker.Resume()
	if worker.IsPaused() {
		t.Fatal("expected worker to be resumed")
	}
	if state.Snapshot().SyncPaused {
		t.Fatal("expected app state sync_paused to be false after resume")
	}

	select {
	case <-executedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("job did not run after resume")
	}
}

func TestSyncWorker_RecoversAfterStalledSyncJob(t *testing.T) {
	bus := NewEventBus(16)
	state := NewAppState("test-machine", "dev")
	worker := NewSyncWorker(4, state, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Run(ctx)

	releaseStall := make(chan struct{})
	firstStarted := make(chan struct{}, 1)
	firstDone := make(chan struct{}, 1)
	secondDone := make(chan struct{}, 1)

	err := worker.Enqueue(SyncJob{
		Name: "stalled-job",
		Execute: func(ctx context.Context) error {
			select {
			case firstStarted <- struct{}{}:
			default:
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-releaseStall:
			}
			select {
			case firstDone <- struct{}{}:
			default:
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue stalled job failed: %v", err)
	}

	err = worker.Enqueue(SyncJob{
		Name: "post-resume-job",
		Execute: func(ctx context.Context) error {
			select {
			case secondDone <- struct{}{}:
			default:
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("enqueue second job failed: %v", err)
	}

	select {
	case <-firstStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stalled job did not start")
	}

	select {
	case <-secondDone:
		t.Fatal("second job should wait while first job is stalled")
	case <-time.After(120 * time.Millisecond):
	}

	close(releaseStall)

	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("stalled job did not complete after release")
	}

	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("second job did not execute after stalled job resumed")
	}

	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !state.Snapshot().SyncInProgress {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("sync state should be false after recovery flow completed")
}
