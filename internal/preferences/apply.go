package preferences

import (
	"context"
	"log/slog"
	"time"
)

// scheduleUnit is the length of one ScheduledLibraryUpdateInterval unit;
// tests shorten it.
var scheduleUnit = time.Second

// RunScanSchedule calls scan every ScanInterval until ctx is done. The
// schedule restarts whenever the interval changes, and stops while interval
// scans are off. scan runs on this goroutine; RunScanSchedule returns once
// ctx is done and no call is in progress.
func RunScanSchedule(ctx context.Context, s *Store, scan func()) {
	changed, cancel := s.Subscribe()
	defer cancel()
	var (
		ticker  *time.Ticker
		tick    <-chan time.Time
		current time.Duration
	)
	reset := func() {
		every := s.ScanInterval()
		if ticker != nil && every == current {
			return
		}
		if ticker != nil {
			ticker.Stop()
			ticker, tick = nil, nil
		}
		current = every
		if every > 0 {
			ticker = time.NewTicker(every)
			tick = ticker.C
		}
	}
	reset()
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			reset()
		case <-tick:
			if ctx.Err() == nil {
				scan()
			}
		}
	}
}

// FollowLogLevel sets level from the logDebug preference now and after each
// change, until ctx is done.
func FollowLogLevel(ctx context.Context, s *Store, level *slog.LevelVar) {
	changed, cancel := s.Subscribe()
	defer cancel()
	for {
		if s.Debug() {
			level.Set(slog.LevelDebug)
		} else {
			level.Set(slog.LevelInfo)
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
	}
}
