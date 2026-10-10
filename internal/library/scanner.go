package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

// ErrScanInProgress is returned when a section is already being scanned.
var ErrScanInProgress = errors.New("scan already in progress")

// Scanner walks section paths and updates the store.
type Scanner struct {
	store  *Store
	prober probe.Prober
	log    *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	running map[int]bool
	again   map[int]bool // a scan was requested while one was running
	wg      sync.WaitGroup
}

// NewScanner returns a scanner writing to store. A nil prober reports only
// the container from the file extension.
func NewScanner(store *Store, prober probe.Prober, log *slog.Logger) *Scanner {
	if prober == nil {
		prober = probe.None{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Scanner{store: store, prober: prober, log: log, now: time.Now, running: map[int]bool{}, again: map[int]bool{}}
}

// Refreshing reports whether the section is being scanned.
func (sc *Scanner) Refreshing(sectionID int) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.running[sectionID]
}

// Start scans a section in the background. If that section is already
// being scanned, it is scanned once more afterwards.
func (sc *Scanner) Start(ctx context.Context, sectionID int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.running[sectionID] {
		sc.again[sectionID] = true
		return
	}
	sc.running[sectionID] = true
	sc.wg.Add(1)
	go func() {
		defer sc.wg.Done()
		for {
			if _, err := sc.scan(ctx, sectionID); err != nil {
				sc.log.Error("scan failed", "section", sectionID, "err", err)
			}
			sc.mu.Lock()
			again := sc.again[sectionID] && ctx.Err() == nil
			delete(sc.again, sectionID)
			if !again {
				delete(sc.running, sectionID)
				sc.mu.Unlock()
				return
			}
			sc.mu.Unlock()
		}
	}()
}

// Wait blocks until all scans started with Start have returned.
func (sc *Scanner) Wait() { sc.wg.Wait() }

// Scan scans one section and replaces its contents. If any configured root
// path cannot be read, the scan aborts and the section is left unchanged.
func (sc *Scanner) Scan(ctx context.Context, sectionID int) (ScanStats, error) {
	sc.mu.Lock()
	if sc.running[sectionID] {
		sc.mu.Unlock()
		return ScanStats{}, ErrScanInProgress
	}
	sc.running[sectionID] = true
	sc.mu.Unlock()
	defer func() {
		sc.mu.Lock()
		delete(sc.running, sectionID)
		sc.mu.Unlock()
	}()
	return sc.scan(ctx, sectionID)
}

// scan does the work of Scan; the caller marks the section running.
func (sc *Scanner) scan(ctx context.Context, sectionID int) (ScanStats, error) {
	sec, err := sc.store.Section(sectionID)
	if err != nil {
		return ScanStats{}, err
	}
	start := sc.now()
	sc.log.Info("scan started", "section", sec.ID, "title", sec.Title)

	var files []ScannedFile
	for _, root := range sec.Paths {
		found, err := sc.walk(ctx, sec, root)
		if err != nil {
			return ScanStats{}, err
		}
		files = append(files, found...)
	}

	stats, err := sc.store.ApplyScan(sec.ID, files, sc.now())
	if err != nil {
		return ScanStats{}, err
	}
	if err := sc.store.SetImages(sec.ID, findArtwork(sec, sc.store)); err != nil {
		return ScanStats{}, err
	}
	sc.log.Info("scan finished", "section", sec.ID, "title", sec.Title,
		"files", stats.Files, "added", stats.Added, "updated", stats.Updated,
		"removed", stats.Removed, "took", time.Since(start).Round(time.Millisecond))
	return stats, nil
}

func (sc *Scanner) walk(ctx context.Context, sec Section, root string) ([]ScannedFile, error) {
	fi, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("section %q: %w", sec.Title, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("section %q: %s is not a directory", sec.Title, root)
	}

	var files []ScannedFile
	dirs := dirCache{}
	shows := map[string]*Meta{} // tvshow.nfo by directory
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if path == root {
				return err
			}
			sc.log.Warn("skipping unreadable path", "path", path, "err", err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name[0] == '.' || !probe.IsVideo(name) || skipFile(name) || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			sc.log.Warn("skipping file", "path", path, "err", err)
			return nil
		}
		if f, ok := sc.scanFile(ctx, sec, root, path, info); ok {
			f.Sidecars = findSidecars(dirs, path)
			f.NFO, f.ShowNFO = sc.nfos(dirs, shows, sec, root, path)
			files = append(files, f)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("section %q: %w", sec.Title, err)
	}
	return files, nil
}

// nfos reads the .nfo metadata of a video and, for episodes, of its show;
// see docs/spec/metadata.md.
func (sc *Scanner) nfos(dirs dirCache, shows map[string]*Meta, sec Section, root, path string) (item, show *Meta) {
	read := func(p, kind string) *Meta {
		if p == "" {
			return nil
		}
		m, err := ReadNFO(p, kind)
		if err != nil {
			sc.log.Debug("ignoring nfo", "path", p, "err", err)
			return nil
		}
		return m
	}
	dir, st := filepath.Dir(path), stem(path)
	switch sec.Type {
	case TypeMovie:
		return read(nfoPath(dirs, dir, st+".nfo", "movie.nfo"), NFOMovie), nil
	case TypeShow:
		item = read(nfoPath(dirs, dir, st+".nfo"), NFOEpisode)
		root = filepath.Clean(root)
		for d := dir; ; d = filepath.Dir(d) {
			m, ok := shows[d]
			if !ok {
				m = read(nfoPath(dirs, d, "tvshow.nfo"), NFOShow)
				shows[d] = m
			}
			if m != nil || d == root || !strings.HasPrefix(d, root) || filepath.Dir(d) == d {
				return item, m
			}
		}
	}
	return nil, nil
}

// scanFile names and probes one video file. ok is false if the file does not
// fit the section's naming rules.
func (sc *Scanner) scanFile(ctx context.Context, sec Section, root, path string, fi fs.FileInfo) (f ScannedFile, ok bool) {
	f = ScannedFile{Path: path, Size: fi.Size(), ModTime: fi.ModTime().Unix()}
	switch sec.Type {
	case TypeMovie:
		f.Title, f.Year = ParseMovie(path)
	case TypeShow:
		ep, ok := ParseEpisode(root, path)
		if !ok {
			sc.log.Debug("skipping file without episode number", "path", path)
			return ScannedFile{}, false
		}
		f.Show, f.ShowYear, f.Season, f.Episode, f.Title, f.Aired = ep.Show, ep.ShowYear, ep.Season, ep.Episode, ep.Title, ep.Aired
	default:
		return ScannedFile{}, false
	}

	if size, mod, info, known := sc.store.KnownFile(sec.ID, path); known && size == f.Size && mod == f.ModTime {
		// Files probed by an older version are probed again once, unless
		// there is no real prober to do it.
		_, noProber := sc.prober.(probe.None)
		if info.ProbeVersion >= probe.Version || noProber {
			f.Info = info
			return f, true
		}
	}
	f.Changed = true
	info, err := sc.prober.Probe(ctx, path)
	if err != nil {
		sc.log.Warn("probe failed", "path", path, "err", err)
	}
	if info.Container == "" {
		info.Container = probe.ContainerForPath(path)
	}
	f.Info = info
	return f, true
}
