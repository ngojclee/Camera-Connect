package coordinator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrQueueFull indicates the sync worker queue is saturated.
	ErrQueueFull = errors.New("sync queue is full")
	// ErrInvalidJob indicates caller provided an empty job.
	ErrInvalidJob = errors.New("invalid sync job")
)

// SyncJob represents one sync task to be executed by the single worker.
type SyncJob struct {
	Name           string
	OperationID    string
	MaxRunDuration time.Duration
	Execute        func(ctx context.Context) error
}

// SyncResult is emitted as event payload on completion or failure.
type SyncResult struct {
	JobName string
	Error   string
}

// SyncWorker enforces one-at-a-time sync execution.
type SyncWorker struct {
	state    *AppState
	bus      *EventBus
	queue    chan SyncJob
	watchdog *Watchdog

	pauseMu  sync.RWMutex
	paused   bool
	resumeCh chan struct{}
}

// NewSyncWorker creates a one-at-a-time worker with bounded queue.
func NewSyncWorker(queueSize int, state *AppState, bus *EventBus) *SyncWorker {
	if queueSize <= 0 {
		queueSize = 16
	}
	resumeCh := make(chan struct{})
	close(resumeCh)
	return &SyncWorker{
		state:    state,
		bus:      bus,
		queue:    make(chan SyncJob, queueSize),
		resumeCh: resumeCh,
	}
}

// SetWatchdog attaches an optional operation watchdog for timeout alerts.
func (w *SyncWorker) SetWatchdog(wd *Watchdog) {
	w.watchdog = wd
}

// Pause prevents new sync jobs from starting until Resume is called.
func (w *SyncWorker) Pause() {
	w.pauseMu.Lock()
	defer w.pauseMu.Unlock()
	if w.paused {
		return
	}

	w.paused = true
	w.resumeCh = make(chan struct{})
	w.state.SetSyncPaused(true)
}

// Resume allows queued sync jobs to continue processing.
func (w *SyncWorker) Resume() {
	w.pauseMu.Lock()
	defer w.pauseMu.Unlock()
	if !w.paused {
		return
	}

	w.paused = false
	close(w.resumeCh)
	w.state.SetSyncPaused(false)
}

// IsPaused reports whether the worker is currently paused.
func (w *SyncWorker) IsPaused() bool {
	w.pauseMu.RLock()
	defer w.pauseMu.RUnlock()
	return w.paused
}

// Enqueue attempts to queue a sync job without blocking.
func (w *SyncWorker) Enqueue(job SyncJob) error {
	if job.Execute == nil || job.Name == "" {
		return ErrInvalidJob
	}

	select {
	case w.queue <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// Run processes jobs until ctx is cancelled.
func (w *SyncWorker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-w.queue:
			if !w.waitUntilResumed(ctx) {
				return
			}
			w.process(ctx, job)
		}
	}
}

func (w *SyncWorker) waitUntilResumed(ctx context.Context) bool {
	for {
		w.pauseMu.RLock()
		paused := w.paused
		resumeCh := w.resumeCh
		w.pauseMu.RUnlock()

		if !paused {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-resumeCh:
		}
	}
}

func (w *SyncWorker) process(ctx context.Context, job SyncJob) {
	w.state.SetSyncing(true)
	w.state.SetCurrentJobName(job.Name)
	w.state.SetProgress(0)
	w.bus.Emit(InternalEvent{Type: EvtSyncRequested, Payload: job.Name})

	stopWatch := func() {}
	if w.watchdog != nil {
		opID := job.OperationID
		if opID == "" {
			opID = fmt.Sprintf("%s-%d", job.Name, time.Now().UTC().UnixNano())
		}
		timeout := job.MaxRunDuration
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		stopWatch = w.watchdog.Start(opID, job.Name, timeout)
	}
	defer stopWatch()

	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		err = job.Execute(ctx)
	}()
	if err != nil {
		w.bus.Emit(InternalEvent{
			Type: EvtSyncFailed,
			Payload: SyncResult{
				JobName: job.Name,
				Error:   err.Error(),
			},
		})
		w.state.SetSyncing(false)
		w.state.SetProgress(-1)
		w.state.SetCurrentJobName("")
		return
	}

	w.bus.Emit(InternalEvent{
		Type: EvtSyncCompleted,
		Payload: SyncResult{
			JobName: job.Name,
		},
	})
	w.state.SetSyncing(false)
	w.state.SetProgress(-1)
	w.state.SetCurrentJobName("")
}
