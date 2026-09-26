package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCircuitBreaker_OpenHalfOpenRecover(t *testing.T) {
	cb := NewCircuitBreaker(BreakerConfig{
		FailureThreshold: 2,
		OpenTimeout:      25 * time.Millisecond,
	}, nil)

	now := time.Now().UTC()
	if !cb.Allow(now) {
		t.Fatal("breaker should allow in closed state")
	}

	cb.RecordFailure(now, errors.New("e1"))
	if state := cb.State(); state != BreakerClosed {
		t.Fatalf("state = %s, want closed", state)
	}

	cb.RecordFailure(now, errors.New("e2"))
	if state := cb.State(); state != BreakerOpen {
		t.Fatalf("state = %s, want open", state)
	}

	if cb.Allow(now.Add(10 * time.Millisecond)) {
		t.Fatal("breaker should not allow before open timeout elapsed")
	}

	if !cb.Allow(now.Add(30 * time.Millisecond)) {
		t.Fatal("breaker should allow and move to half-open after timeout")
	}
	if state := cb.State(); state != BreakerHalfOpen {
		t.Fatalf("state = %s, want half_open", state)
	}

	cb.RecordSuccess()
	if state := cb.State(); state != BreakerClosed {
		t.Fatalf("state = %s, want closed after recovery", state)
	}
}

func TestShareHealthMonitor_EmitsLostAndRecovered(t *testing.T) {
	lostCh := make(chan error, 2)
	recoveredCh := make(chan struct{}, 2)

	attempt := 0
	probe := func(ctx context.Context) error {
		attempt++
		if attempt <= 2 {
			return errors.New("network down")
		}
		return nil
	}

	monitor := NewShareHealthMonitor(ShareHealthConfig{
		CheckInterval:    1 * time.Second,
		ProbeTimeout:     200 * time.Millisecond,
		FailureThreshold: 2,
		OpenTimeout:      10 * time.Millisecond,
	}, probe, ShareHealthHooks{
		OnNetworkLost: func(err error) {
			select {
			case lostCh <- err:
			default:
			}
		},
		OnNetworkRecovered: func() {
			select {
			case recoveredCh <- struct{}{}:
			default:
			}
		},
	})

	monitor.checkOnce(context.Background()) // fail #1
	monitor.checkOnce(context.Background()) // fail #2 => open => lost

	select {
	case <-lostCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected network lost callback")
	}

	time.Sleep(15 * time.Millisecond)       // wait open timeout
	monitor.checkOnce(context.Background()) // half-open probe success => recovered

	select {
	case <-recoveredCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected network recovered callback")
	}
}

func TestNewPathProbe_AccessibleAndMissing(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	okProbe := NewPathProbe([]string{sub})
	if err := okProbe(context.Background()); err != nil {
		t.Fatalf("probe should succeed: %v", err)
	}

	missingProbe := NewPathProbe([]string{filepath.Join(root, "missing")})
	if err := missingProbe(context.Background()); err == nil {
		t.Fatal("probe should fail for missing path")
	}
}
