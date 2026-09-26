package monitor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestResumeDetector_TriggersOnLargeGap(t *testing.T) {
	var nowUnix int64
	base := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	atomic.StoreInt64(&nowUnix, base.UnixNano())

	triggered := make(chan time.Duration, 1)
	detector := NewResumeDetector(10*time.Millisecond, 50*time.Millisecond, ResumeHooks{
		OnResume: func(gap time.Duration) {
			select {
			case triggered <- gap:
			default:
			}
		},
	})
	detector.nowFn = func() time.Time {
		return time.Unix(0, atomic.LoadInt64(&nowUnix)).UTC()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go detector.Run(ctx)

	// normal progress
	atomic.StoreInt64(&nowUnix, base.Add(15*time.Millisecond).UnixNano())
	time.Sleep(15 * time.Millisecond)
	atomic.StoreInt64(&nowUnix, base.Add(30*time.Millisecond).UnixNano())
	time.Sleep(15 * time.Millisecond)

	// simulate resume gap
	atomic.StoreInt64(&nowUnix, base.Add(130*time.Millisecond).UnixNano())

	select {
	case gap := <-triggered:
		if gap <= 50*time.Millisecond {
			t.Fatalf("gap = %v, want > 50ms", gap)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected resume detector callback")
	}
}
