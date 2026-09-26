package coordinator

import (
	"context"
	"sync"
	"time"
)

// WatchdogAlert is emitted when an operation exceeds its deadline.
type WatchdogAlert struct {
	OperationID   string
	OperationName string
	StartedAt     time.Time
	DeadlineAt    time.Time
}

type watchdogEntry struct {
	id         string
	name       string
	startedAt  time.Time
	deadlineAt time.Time
}

// Watchdog tracks operation deadlines and emits alerts on timeout.
type Watchdog struct {
	mu       sync.Mutex
	entries  map[string]watchdogEntry
	interval time.Duration
	onAlert  func(alert WatchdogAlert)
}

// NewWatchdog creates a watchdog scanner.
func NewWatchdog(scanInterval time.Duration, onAlert func(alert WatchdogAlert)) *Watchdog {
	if scanInterval <= 0 {
		scanInterval = 250 * time.Millisecond
	}
	return &Watchdog{
		entries:  make(map[string]watchdogEntry),
		interval: scanInterval,
		onAlert:  onAlert,
	}
}

// Start begins tracking an operation and returns a stop function.
func (w *Watchdog) Start(opID, operationName string, timeout time.Duration) func() {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	now := time.Now().UTC()
	entry := watchdogEntry{
		id:         opID,
		name:       operationName,
		startedAt:  now,
		deadlineAt: now.Add(timeout),
	}

	w.mu.Lock()
	w.entries[opID] = entry
	w.mu.Unlock()

	return func() {
		w.mu.Lock()
		delete(w.entries, opID)
		w.mu.Unlock()
	}
}

// Run checks deadlines until context cancellation.
func (w *Watchdog) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanExpired()
		}
	}
}

func (w *Watchdog) scanExpired() {
	now := time.Now().UTC()
	expired := make([]watchdogEntry, 0, 4)

	w.mu.Lock()
	for id, entry := range w.entries {
		if now.After(entry.deadlineAt) {
			expired = append(expired, entry)
			delete(w.entries, id)
		}
	}
	w.mu.Unlock()

	if w.onAlert == nil {
		return
	}
	for _, entry := range expired {
		w.onAlert(WatchdogAlert{
			OperationID:   entry.id,
			OperationName: entry.name,
			StartedAt:     entry.startedAt,
			DeadlineAt:    entry.deadlineAt,
		})
	}
}
