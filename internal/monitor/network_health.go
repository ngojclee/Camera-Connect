package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BreakerState is the state of a circuit breaker.
type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// BreakerConfig controls circuit breaker behavior.
type BreakerConfig struct {
	FailureThreshold int
	OpenTimeout      time.Duration
}

type stateChangeHook func(from, to BreakerState, cause error)

// CircuitBreaker protects operations during unstable periods.
type CircuitBreaker struct {
	mu            sync.Mutex
	state         BreakerState
	failures      int
	openedAt      time.Time
	cfg           BreakerConfig
	onStateChange stateChangeHook
}

// NewCircuitBreaker creates a circuit breaker with sane defaults.
func NewCircuitBreaker(cfg BreakerConfig, onStateChange stateChangeHook) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 10 * time.Second
	}
	return &CircuitBreaker{
		state:         BreakerClosed,
		cfg:           cfg,
		onStateChange: onStateChange,
	}
}

// Allow returns whether the protected operation is allowed right now.
// When open timeout has elapsed, state transitions to half-open and one probe is allowed.
func (b *CircuitBreaker) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state != BreakerOpen {
		return true
	}
	if now.Sub(b.openedAt) < b.cfg.OpenTimeout {
		return false
	}
	b.transitionLocked(BreakerHalfOpen, nil)
	return true
}

// RecordSuccess marks a successful protected operation.
func (b *CircuitBreaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	prev := b.state
	b.failures = 0
	b.state = BreakerClosed
	if prev != BreakerClosed && b.onStateChange != nil {
		b.onStateChange(prev, BreakerClosed, nil)
	}
}

// RecordFailure marks a failed protected operation.
func (b *CircuitBreaker) RecordFailure(now time.Time, cause error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case BreakerHalfOpen:
		b.openLocked(now, cause)
	case BreakerClosed:
		b.failures++
		if b.failures >= b.cfg.FailureThreshold {
			b.openLocked(now, cause)
		}
	case BreakerOpen:
		// Already open; no-op.
	}
}

// State returns current breaker state.
func (b *CircuitBreaker) State() BreakerState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *CircuitBreaker) openLocked(now time.Time, cause error) {
	b.openedAt = now
	b.failures = 0
	b.transitionLocked(BreakerOpen, cause)
}

func (b *CircuitBreaker) transitionLocked(to BreakerState, cause error) {
	from := b.state
	b.state = to
	if from != to && b.onStateChange != nil {
		b.onStateChange(from, to, cause)
	}
}

// NetworkProbe validates network share health.
type NetworkProbe func(ctx context.Context) error

// ShareHealthConfig controls share health monitoring loop.
type ShareHealthConfig struct {
	CheckInterval    time.Duration
	ProbeTimeout     time.Duration
	FailureThreshold int
	OpenTimeout      time.Duration
}

// ShareHealthHooks receives network outage/recovery events.
type ShareHealthHooks struct {
	OnNetworkLost      func(err error)
	OnNetworkRecovered func()
}

// ShareHealthMonitor probes share accessibility with circuit breaker behavior.
type ShareHealthMonitor struct {
	cfg     ShareHealthConfig
	breaker *CircuitBreaker
	probe   NetworkProbe
	hooks   ShareHealthHooks
}

// NewShareHealthMonitor constructs a share health monitor.
func NewShareHealthMonitor(cfg ShareHealthConfig, probe NetworkProbe, hooks ShareHealthHooks) *ShareHealthMonitor {
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 5 * time.Second
	}
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = 2 * time.Second
	}
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 10 * time.Second
	}

	m := &ShareHealthMonitor{
		cfg:   cfg,
		probe: probe,
		hooks: hooks,
	}

	m.breaker = NewCircuitBreaker(BreakerConfig{
		FailureThreshold: cfg.FailureThreshold,
		OpenTimeout:      cfg.OpenTimeout,
	}, func(from, to BreakerState, cause error) {
		if to == BreakerOpen && m.hooks.OnNetworkLost != nil {
			m.hooks.OnNetworkLost(cause)
			return
		}
		if from == BreakerHalfOpen && to == BreakerClosed && m.hooks.OnNetworkRecovered != nil {
			m.hooks.OnNetworkRecovered()
		}
	})

	return m
}

// Run executes health checks until context cancellation.
func (m *ShareHealthMonitor) Run(ctx context.Context) {
	m.checkOnce(ctx)

	ticker := time.NewTicker(m.cfg.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkOnce(ctx)
		}
	}
}

func (m *ShareHealthMonitor) checkOnce(parent context.Context) {
	now := time.Now().UTC()
	if !m.breaker.Allow(now) {
		return
	}
	if m.probe == nil {
		return
	}

	probeCtx, cancel := context.WithTimeout(parent, m.cfg.ProbeTimeout)
	defer cancel()

	if err := m.probe(probeCtx); err != nil {
		m.breaker.RecordFailure(now, err)
		return
	}
	m.breaker.RecordSuccess()
}

// NewPathProbe returns a probe that checks all paths are accessible.
func NewPathProbe(paths []string) NetworkProbe {
	cleaned := uniqueCleanPaths(paths)
	return func(ctx context.Context) error {
		for _, path := range cleaned {
			if err := ctx.Err(); err != nil {
				return err
			}
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("stat path %s: %w", path, err)
			}
			if info.IsDir() {
				if _, err := os.ReadDir(path); err != nil {
					return fmt.Errorf("read dir %s: %w", path, err)
				}
			}
		}
		return nil
	}
}

func uniqueCleanPaths(paths []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, clean)
	}
	return out
}
