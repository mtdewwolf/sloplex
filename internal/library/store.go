// Package library holds library sections and their items, and scans media
// folders into them.
package library

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/probe"
)

// TypeMovie is the library and item type for movies.
const TypeMovie = "movie"

// Section is one library section, such as "Movies".
type Section struct {
	ID        int      `json:"id"`
	UUID      string   `json:"uuid"`
	Title     string   `json:"title"`
	Type      string   `json:"type"`
	Paths     []string `json:"paths"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
	ScannedAt int64    `json:"scannedAt,omitempty"`
}

// Item is one playable title, such as a movie. It may have several Media
// versions.
type Item struct {
	ID        int     `json:"id"`
	SectionID int     `json:"sectionId"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Year      int     `json:"year,omitempty"`
	AddedAt   int64   `json:"addedAt"`
	UpdatedAt int64   `json:"updatedAt"`
	Media     []Media `json:"media"`
}

// Media is one version of an item.
type Media struct {
	ID int `json:"id"`
	probe.Info
	Parts []Part `json:"parts"`
}

// Part is one file of a Media.
type Part struct {
	ID        int    `json:"id"`
	File      string `json:"file"`
	Size      int64  `json:"size"`
	ModTime   int64  `json:"modTime"`
	Duration  int64  `json:"duration,omitempty"`
	Container string `json:"container,omitempty"`
}

// SectionDef is the configured shape of a section.
type SectionDef struct {
	Title string
	Type  string
	Paths []string
}

// ScannedFile is one media file found by a scan.
type ScannedFile struct {
	Path    string
	Size    int64
	ModTime int64
	Title   string
	Year    int
	Info    probe.Info
	// Changed is set when the file is new or was modified since the last scan.
	Changed bool
}

// ScanStats summarises the effect of a scan.
type ScanStats struct {
	Files, Added, Updated, Removed int
}

// ErrNotFound is returned for unknown sections, items and parts.
var ErrNotFound = errors.New("not found")

const storeVersion = 1

type storeData struct {
	Version       int        `json:"version"`
	NextSectionID int        `json:"nextSectionId"`
	NextItemID    int        `json:"nextItemId"`
	NextMediaID   int        `json:"nextMediaId"`
	NextPartID    int        `json:"nextPartId"`
	Sections      []*Section `json:"sections"`
	Items         []*Item    `json:"items"`
}

// Store is the library database. It is kept in memory and saved as JSON
// after every change. All methods are safe for concurrent use and return
// copies.
type Store struct {
	mu   sync.RWMutex
	path string
	d    storeData
}

// Open loads the store from path. A missing file yields an empty store; an
// empty path yields an in-memory store that is never saved.
func Open(path string) (*Store, error) {
	s := &Store{path: path, d: storeData{Version: storeVersion}}
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &s.d); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			if s.d.Version != storeVersion {
				return nil, fmt.Errorf("%s: unsupported store version %d", path, s.d.Version)
			}
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
	}
	return s, nil
}

func next(counter *int) int {
	*counter++
	return *counter
}

// SyncSections makes the stored sections match defs, matched by title and
// type. Existing sections keep their IDs; sections not in defs are removed
// together with their items.
func (s *Store) SyncSections(defs []SectionDef, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	byKey := map[string]*Section{}
	for _, sec := range s.d.Sections {
		byKey[sec.Type+"\x00"+sec.Title] = sec
	}
	ts := now.Unix()
	var out []*Section
	keep := map[int]bool{}
	for _, def := range defs {
		sec := byKey[def.Type+"\x00"+def.Title]
		if sec == nil {
			sec = &Section{
				ID: next(&s.d.NextSectionID), UUID: newUUID(),
				Title: def.Title, Type: def.Type, CreatedAt: ts, UpdatedAt: ts,
			}
		}
		if !slices.Equal(sec.Paths, def.Paths) {
			sec.Paths = slices.Clone(def.Paths)
			sec.UpdatedAt = ts
		}
		keep[sec.ID] = true
		out = append(out, sec)
	}
	s.d.Sections = out
	s.d.Items = slices.DeleteFunc(s.d.Items, func(it *Item) bool { return !keep[it.SectionID] })
	return s.saveLocked()
}

// Sections returns all sections in configuration order.
func (s *Store) Sections() []Section {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Section, len(s.d.Sections))
	for i, sec := range s.d.Sections {
		out[i] = sec.clone()
	}
	return out
}

// Section returns the section with the given ID.
func (s *Store) Section(id int) (Section, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sec := s.sectionLocked(id); sec != nil {
		return sec.clone(), nil
	}
	return Section{}, ErrNotFound
}

func (s *Store) sectionLocked(id int) *Section {
	for _, sec := range s.d.Sections {
		if sec.ID == id {
			return sec
		}
	}
	return nil
}

// Items returns the items of a section, or of all sections if sectionID is 0.
func (s *Store) Items(sectionID int) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Item
	for _, it := range s.d.Items {
		if sectionID == 0 || it.SectionID == sectionID {
			out = append(out, it.clone())
		}
	}
	return out
}

// Item returns the item with the given ID.
func (s *Store) Item(id int) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.d.Items {
		if it.ID == id {
			return it.clone(), nil
		}
	}
	return Item{}, ErrNotFound
}

// Part returns the part with the given ID.
func (s *Store) Part(id int) (Part, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.d.Items {
		for _, m := range it.Media {
			for _, p := range m.Parts {
				if p.ID == id {
					return p, nil
				}
			}
		}
	}
	return Part{}, ErrNotFound
}

// KnownFile returns the stored size, modification time and media info of a
// file in a section, so scans can skip re-probing unchanged files.
func (s *Store) KnownFile(sectionID int, path string) (size, modTime int64, info probe.Info, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.d.Items {
		if it.SectionID != sectionID {
			continue
		}
		for _, m := range it.Media {
			for _, p := range m.Parts {
				if p.File == path {
					return p.Size, p.ModTime, m.Info, true
				}
			}
		}
	}
	return 0, 0, probe.Info{}, false
}

type prevPart struct {
	item  *Item
	media Media
	part  Part
}

// ApplyScan replaces the contents of a section with the scanned files.
// Files with the same title and year become one item. Item, media and part
// IDs are kept for file paths that were already known.
func (s *Store) ApplyScan(sectionID int, files []ScannedFile, now time.Time) (ScanStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sec := s.sectionLocked(sectionID)
	if sec == nil {
		return ScanStats{}, ErrNotFound
	}
	ts := now.Unix()

	prev := map[string]prevPart{}
	var others, old []*Item
	for _, it := range s.d.Items {
		if it.SectionID != sectionID {
			others = append(others, it)
			continue
		}
		old = append(old, it)
		for _, m := range it.Media {
			for _, p := range m.Parts {
				prev[p.File] = prevPart{it, m, p}
			}
		}
	}

	groups := map[string][]ScannedFile{}
	for _, f := range files {
		k := strings.ToLower(f.Title) + "\x00" + strconv.Itoa(f.Year)
		groups[k] = append(groups[k], f)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	stats := ScanStats{Files: len(files)}
	claimed := map[int]bool{}
	items := make([]*Item, 0, len(keys))
	for _, k := range keys {
		group := groups[k]
		sort.Slice(group, func(i, j int) bool { return group[i].Path < group[j].Path })

		var base *Item
		for _, f := range group {
			if p, ok := prev[f.Path]; ok && !claimed[p.item.ID] {
				base = p.item
				break
			}
		}
		it := &Item{SectionID: sectionID, Type: sec.Type, Title: group[0].Title, Year: group[0].Year}
		changed := false
		if base != nil {
			claimed[base.ID] = true
			it.ID, it.AddedAt, it.UpdatedAt = base.ID, base.AddedAt, base.UpdatedAt
		} else {
			it.ID, it.AddedAt = next(&s.d.NextItemID), ts
			stats.Added++
		}

		for _, f := range group {
			m := Media{Info: f.Info}
			pt := Part{File: f.Path, Size: f.Size, ModTime: f.ModTime, Duration: f.Info.Duration, Container: f.Info.Container}
			if p, ok := prev[f.Path]; ok {
				m.ID, pt.ID = p.media.ID, p.part.ID
				changed = changed || p.item.ID != it.ID
			} else {
				m.ID, pt.ID = next(&s.d.NextMediaID), next(&s.d.NextPartID)
				changed = true
			}
			changed = changed || f.Changed
			m.Parts = []Part{pt}
			it.Media = append(it.Media, m)
		}
		if base != nil {
			changed = changed || len(base.Media) != len(it.Media) || base.Title != it.Title || base.Year != it.Year
			if changed {
				stats.Updated++
			}
		}
		if changed {
			it.UpdatedAt = ts
		}
		items = append(items, it)
	}
	for _, it := range old {
		if !claimed[it.ID] {
			stats.Removed++
		}
	}

	s.d.Items = append(others, items...)
	sec.ScannedAt = ts
	return stats, s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(&s.d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".library-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (sec *Section) clone() Section {
	c := *sec
	c.Paths = slices.Clone(sec.Paths)
	return c
}

func (it *Item) clone() Item {
	c := *it
	c.Media = make([]Media, len(it.Media))
	for i, m := range it.Media {
		m.Parts = slices.Clone(m.Parts)
		c.Media[i] = m
	}
	return c
}

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
