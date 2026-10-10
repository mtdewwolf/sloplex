package library

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/mtdewwolf/sloplex/internal/fswatch"
)

// AutoOptions configures automatic scans; see "When scans run" in
// docs/spec/http-api.md.
type AutoOptions struct {
	// Watch scans sections when their folders change.
	Watch bool
	// Interval scans every section this often; 0 turns it off.
	Interval time.Duration
	// Quiet is how long changes must settle before a scan (default 5 s),
	// and MaxDelay the longest a scan waits after the first change
	// (default 60 s).
	Quiet, MaxDelay time.Duration
}

// pendingScan tracks unsettled changes in a section.
type pendingScan struct{ first, last time.Time }

// due is when the section's scan starts.
func (p *pendingScan) due(quiet, maxDelay time.Duration) time.Time {
	if settle, limit := p.last.Add(quiet), p.first.Add(maxDelay); settle.Before(limit) {
		return settle
	}
	return p.first.Add(maxDelay)
}

// Auto runs automatic scans until ctx is done. It returns once its watcher
// is set up; the work continues in the background and is waited for by
// Wait.
func (sc *Scanner) Auto(ctx context.Context, opts AutoOptions) {
	if opts.Quiet <= 0 {
		opts.Quiet = 5 * time.Second
	}
	if opts.MaxDelay <= 0 {
		opts.MaxDelay = 60 * time.Second
	}
	sections := sc.store.Sections()
	var events <-chan string
	if opts.Watch {
		w, err := fswatch.New(func(name string) bool { return skipDir(name) })
		switch {
		case errors.Is(err, fswatch.ErrUnsupported):
			sc.log.Info("not watching library folders; relying on interval scans", "reason", err)
		case err != nil:
			sc.log.Warn("not watching library folders", "err", err)
		default:
			for _, sec := range sections {
				for _, root := range sec.Paths {
					if err := w.AddTree(root); err != nil {
						sc.log.Warn("library folders not fully watched; interval scans cover the rest",
							"section", sec.Title, "path", root, "err", err)
					}
				}
			}
			events = w.Events()
			sc.wg.Add(1)
			go func() {
				defer sc.wg.Done()
				<-ctx.Done()
				w.Close()
			}()
		}
	}
	var tick <-chan time.Time
	if opts.Interval > 0 {
		t := time.NewTicker(opts.Interval)
		tick = t.C
		sc.wg.Add(1)
		go func() {
			defer sc.wg.Done()
			<-ctx.Done()
			t.Stop()
		}()
	}
	if events == nil && tick == nil {
		return
	}

	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		pending := map[int]*pendingScan{}
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		defer timer.Stop()
		// arm sets the timer for the earliest section due.
		arm := func() {
			timer.Stop()
			var next time.Time
			for _, p := range pending {
				due := p.due(opts.Quiet, opts.MaxDelay)
				if next.IsZero() || due.Before(next) {
					next = due
				}
			}
			if !next.IsZero() {
				timer.Reset(time.Until(next))
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case path, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				now := time.Now()
				for _, sec := range sections {
					if path != fswatch.Overflow && !underAny(path, sec.Paths) {
						continue
					}
					if p := pending[sec.ID]; p != nil {
						p.last = now
					} else {
						pending[sec.ID] = &pendingScan{first: now, last: now}
					}
				}
				arm()
			case <-timer.C:
				now := time.Now()
				for id, p := range pending {
					if !now.Before(p.due(opts.Quiet, opts.MaxDelay)) {
						delete(pending, id)
						sc.log.Info("library folders changed; scanning", "section", id, "title", titleOf(sections, id))
						sc.Start(ctx, id)
					}
				}
				arm()
			case <-tick:
				for _, sec := range sections {
					sc.Start(ctx, sec.ID)
				}
			}
		}
	}()
}

func titleOf(sections []Section, id int) string {
	for _, sec := range sections {
		if sec.ID == id {
			return sec.Title
		}
	}
	return ""
}

// underAny reports whether path is one of roots or inside one.
func underAny(path string, roots []string) bool {
	for _, r := range roots {
		r = filepath.Clean(r)
		if path == r || strings.HasPrefix(path, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
