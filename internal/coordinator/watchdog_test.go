package coordinator

import (
	"context"
	"testing"
	"time"
)

func TestWatchdog_EmitsAlertOnTimeout(t *testing.T) {
	alertCh := make(chan WatchdogAlert, 1)
	wd := NewWatchdog(10*time.Millisecond, func(alert WatchdogAlert) {
		select {
		case alertCh <- alert:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wd.Run(ctx)

	_ = wd.Start("op-1", "sync", 20*time.Millisecond)

	select {
	case alert := <-alertCh:
		if alert.OperationID != "op-1" {
			t.Fatalf("alert.OperationID = %q, want op-1", alert.OperationID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected watchdog alert")
	}
}

func TestWatchdog_StopPreventsAlert(t *testing.T) {
	alertCh := make(chan WatchdogAlert, 1)
	wd := NewWatchdog(10*time.Millisecond, func(alert WatchdogAlert) {
		select {
		case alertCh <- alert:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go wd.Run(ctx)

	stop := wd.Start("op-2", "sync", 30*time.Millisecond)
	stop()

	select {
	case alert := <-alertCh:
		t.Fatalf("unexpected alert: %+v", alert)
	case <-time.After(120 * time.Millisecond):
	}
}
