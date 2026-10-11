package library

import (
	"cmp"
	"errors"
	"slices"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

// Available reports whether every part of this version is present in the last
// authoritative scan. A partially missing multipart version is unplayable.
func (m Media) Available() bool {
	return len(m.Parts) > 0 && !slices.ContainsFunc(m.Parts, func(p Part) bool { return p.Unavailable })
}

// refreshAvailability derives leaf and hierarchy availability and orders healthy
// versions first. The model is original design; see docs/spec/trash.md.
func refreshAvailability(items []*Item) {
	children := map[int][]*Item{}
	for _, it := range items {
		children[it.ParentID] = append(children[it.ParentID], it)
	}
	var available func(*Item) bool
	available = func(it *Item) bool {
		if IsLeaf(it.Type) {
			slices.SortStableFunc(it.Media, func(a, b Media) int { return cmp.Compare(boolInt(!a.Available()), boolInt(!b.Available())) })
			it.Unavailable = !slices.ContainsFunc(it.Media, func(m Media) bool { return m.Available() })
		} else {
			it.Unavailable = true
			for _, child := range children[it.ID] {
				if available(child) {
					it.Unavailable = false
				}
			}
		}
		return !it.Unavailable
	}
	for _, it := range children[0] {
		available(it)
	}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// stagedLocked deep-copies mutable catalog records before an operation that can
// fail persistence. Indexes and the mutex are not copied.
func (s *Store) stagedLocked() *Store {
	d := s.d
	d.Sections = make([]*Section, len(s.d.Sections))
	for i, sec := range s.d.Sections {
		c := sec.clone()
		d.Sections[i] = &c
	}
	d.Items = make([]*Item, len(s.d.Items))
	for i, it := range s.d.Items {
		c := it.clone()
		d.Items[i] = &c
	}
	d.Collections = make([]*Collection, len(s.d.Collections))
	for i, c := range s.d.Collections {
		v := c.clone()
		d.Collections[i] = &v
	}
	stage := &Store{path: s.path, d: d, writeFile: s.writeFile}
	stage.reindexLocked()
	return stage
}

func (s *Store) publishLocked(stage *Store) error {
	err := stage.saveLocked()
	var committed *atomicfile.CommittedError
	if err != nil && !errors.As(err, &committed) {
		return err
	}
	s.d = stage.d
	s.reindexLocked()
	return err
}

// EmptyTrash removes unavailable versions and empty leaves/ancestors from one
// section's catalog. It never touches media files or other stores.
func (s *Store) EmptyTrash(sectionID int, now time.Time) (ScanStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stage := s.stagedLocked()
	sec := stage.sectionLocked(sectionID)
	if sec == nil {
		return ScanStats{}, ErrNotFound
	}
	var out []*Item
	stats := ScanStats{}
	for _, it := range stage.d.Items {
		if it.SectionID != sectionID || !IsLeaf(it.Type) {
			out = append(out, it)
			continue
		}
		old := len(it.Media)
		it.Media = slices.DeleteFunc(it.Media, func(m Media) bool { return !m.Available() })
		if len(it.Media) == 0 {
			stats.Removed++
			continue
		}
		if len(it.Media) != old {
			it.UpdatedAt = now.Unix()
			stats.Updated++
		}
		out = append(out, it)
	}
	// Seasons then shows become empty only after children have been removed.
	for _, typ := range []string{TypeSeason, TypeShow} {
		parents := map[int]bool{}
		for _, it := range out {
			parents[it.ParentID] = true
		}
		out = slices.DeleteFunc(out, func(it *Item) bool { return it.SectionID == sectionID && it.Type == typ && !parents[it.ID] })
	}
	stage.d.Items = out
	refreshAvailability(stage.d.Items)
	stage.reindexLocked()
	stage.syncCollectionsLocked(sec, now.Unix())
	if err := s.publishLocked(stage); err != nil {
		return ScanStats{}, err
	}
	return stats, nil
}

// PartAvailable also checks the whole media version for multipart completeness.
func (s *Store) PartAvailable(id int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ref, ok := s.parts[id]
	return ok && ref.media.Available()
}
