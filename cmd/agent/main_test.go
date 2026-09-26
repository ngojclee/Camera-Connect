package main

import (
	"sync"
	"testing"
	"time"
)

func TestWaitGroupWithTimeout_CompletesBeforeDeadline(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(20 * time.Millisecond)
	}()

	ok := waitGroupWithTimeout(&wg, 300*time.Millisecond)
	if !ok {
		t.Fatal("expected waitGroupWithTimeout to return true")
	}
}

func TestWaitGroupWithTimeout_TimesOut(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	ok := waitGroupWithTimeout(&wg, 40*time.Millisecond)
	if ok {
		t.Fatal("expected waitGroupWithTimeout to return false on timeout")
	}

	// Cleanup goroutine accounting so test does not leak waitgroup state.
	wg.Done()
}
