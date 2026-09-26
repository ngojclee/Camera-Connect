package monitor

import (
	"context"
	"time"
)

// ResumeHooks receives callbacks from resume detector.
type ResumeHooks struct {
	OnResume func(gap time.Duration)
}

// ResumeDetector detects sleep/resume by observing oversized timer gaps.
type ResumeDetector struct {
	interval time.Duration
	maxGap   time.Duration
	hooks    ResumeHooks
	nowFn    func() time.Time
}

// NewResumeDetector creates a resume detector loop.
func NewResumeDetector(interval, maxGap time.Duration, hooks ResumeHooks) *ResumeDetector {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if maxGap <= 0 {
		maxGap = interval * 3
	}
	return &ResumeDetector{
		interval: interval,
		maxGap:   maxGap,
		hooks:    hooks,
		nowFn:    func() time.Time { return time.Now().UTC() },
	}
}

// Run blocks until context cancellation.
func (d *ResumeDetector) Run(ctx context.Context) {
	last := d.nowFn()

	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := d.nowFn()
			gap := now.Sub(last)
			if gap > d.maxGap && d.hooks.OnResume != nil {
				d.hooks.OnResume(gap)
			}
			last = now
		}
	}
}
