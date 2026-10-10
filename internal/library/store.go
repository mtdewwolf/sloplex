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
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

// Section and item types.
const (
	TypeMovie   = "movie"
	TypeShow    = "show"
	TypeSeason  = "season"
	TypeEpisode = "episode"
)

// IsLeaf reports whether items of the given type carry media.
func IsLeaf(itemType string) bool {
	return itemType == TypeMovie || itemType == TypeEpisode
}

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

// Item is a movie, show, season or episode. Leaf items (movies, episodes)
// have one or more Media versions; shows and seasons have children instead.
type Item struct {
	ID        int    `json:"id"`
	SectionID int    `json:"sectionId"`
	Type      string `json:"type"`
	// ParentID is the show of a season, or the season of an episode.
	ParentID int `json:"parentId,omitempty"`
	// Index is the season number of a season or the episode number of an
	// episode.
	Index int    `json:"index,omitempty"`
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	// Aired is an episode's air date (YYYY-MM-DD), when known.
	Aired     string  `json:"aired,omitempty"`
	AddedAt   int64   `json:"addedAt"`
	UpdatedAt int64   `json:"updatedAt"`
	Media     []Media `json:"media,omitempty"`
	// Images maps an artwork kind (ImageThumb, ImageArt, ImageBanner) to a
	// local image file.
	Images map[string]Image `json:"images,omitempty"`
	// Meta is metadata from a local .nfo file, if any.
	Meta *Meta `json:"meta,omitempty"`
}

// SortKey is the lower-case key items are sorted by: the .nfo sort title,
// else the title without a leading article.
func (it Item) SortKey() string {
	if it.Meta != nil && it.Meta.SortTitle != "" {
		return strings.ToLower(it.Meta.SortTitle)
	}
	return SortTitle(it.Title)
}

// Artwork kinds.
const (
	ImageThumb  = "thumb"
	ImageArt    = "art"
	ImageBanner = "banner"
)

// Image is a local artwork file.
type Image struct {
	Path    string `json:"path"`
	ModTime int64  `json:"modTime"`
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
	// Streams are the file's embedded streams followed by its sidecar
	// subtitles; see docs/spec/streams.md.
	Streams []Stream `json:"streams,omitempty"`
}

// Stream is an embedded stream or a sidecar subtitle of a part.
type Stream struct {
	ID int `json:"id"`
	probe.Stream
	// File is the sidecar subtitle path; empty for embedded streams.
	File string `json:"file,omitempty"`
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
	// Title and Year describe a movie, or Title the episode title.
	Title string
	Year  int
	// Show, ShowYear, Season and Episode are set for episodes.
	Show     string
	ShowYear int
	Season   int
	Episode  int
	// Aired is the air date of a date-based episode (YYYY-MM-DD).
	Aired string
	Info  probe.Info
	// Sidecars are subtitle files next to the video (ID unset).
	Sidecars []Stream
	// NFO is the file's .nfo metadata, and ShowNFO its show's (episodes).
	NFO, ShowNFO *Meta
	// Changed is set when the file is new or was modified since the last scan.
	Changed bool
}

// ScanStats summarises the effect of a scan on leaf items.
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
	NextStreamID  int        `json:"nextStreamId,omitempty"`
	Sections      []*Section `json:"sections"`
	Items         []*Item    `json:"items"`
}

type fileRef struct {
	item  *Item
	media *Media
	part  *Part
}

// Store is the library database. It is kept in memory and saved as JSON
// after every change. All methods are safe for concurrent use and return
// copies.
type Store struct {
	mu   sync.RWMutex
	path string
	d    storeData

	// Indexes, rebuilt by reindexLocked after every change.
	byID     map[int]*Item
	children map[int][]*Item // ordered by Index, then ID
	parts    map[int]fileRef
	files    map[fileKey]fileRef
	streams  map[int]streamRef
}

type streamRef struct {
	fileRef
	stream *Stream
}

type fileKey struct {
	section int
	path    string
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
	s.reindexLocked()
	return s, nil
}

func (s *Store) reindexLocked() {
	s.byID = make(map[int]*Item, len(s.d.Items))
	s.children = map[int][]*Item{}
	s.parts = map[int]fileRef{}
	s.files = map[fileKey]fileRef{}
	s.streams = map[int]streamRef{}
	for _, it := range s.d.Items {
		s.byID[it.ID] = it
		if it.ParentID != 0 {
			s.children[it.ParentID] = append(s.children[it.ParentID], it)
		}
		for mi := range it.Media {
			m := &it.Media[mi]
			for pi := range m.Parts {
				ref := fileRef{it, m, &m.Parts[pi]}
				s.parts[ref.part.ID] = ref
				s.files[fileKey{it.SectionID, ref.part.File}] = ref
				for si := range ref.part.Streams {
					s.streams[ref.part.Streams[si].ID] = streamRef{ref, &ref.part.Streams[si]}
				}
			}
		}
	}
	for _, kids := range s.children {
		sort.Slice(kids, func(i, j int) bool {
			if kids[i].Index != kids[j].Index {
				return kids[i].Index < kids[j].Index
			}
			return kids[i].ID < kids[j].ID
		})
	}
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
	s.reindexLocked()
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

// Items returns the items of a section (all sections if sectionID is 0)
// whose type matches one of types (any type if none are given).
func (s *Store) Items(sectionID int, types ...string) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Item
	for _, it := range s.d.Items {
		if (sectionID == 0 || it.SectionID == sectionID) && (len(types) == 0 || slices.Contains(types, it.Type)) {
			out = append(out, it.clone())
		}
	}
	return out
}

// Item returns the item with the given ID.
func (s *Store) Item(id int) (Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if it := s.byID[id]; it != nil {
		return it.clone(), nil
	}
	return Item{}, ErrNotFound
}

// Children returns the direct children of an item, ordered by index.
func (s *Store) Children(id int) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneAll(s.children[id])
}

// Leaves returns all leaf descendants of an item in hierarchy order: a
// show's episodes by season and episode number, or a season's episodes.
func (s *Store) Leaves(id int) []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Item
	var walk func(id int)
	walk = func(id int) {
		for _, c := range s.children[id] {
			if IsLeaf(c.Type) {
				out = append(out, c.clone())
			} else {
				walk(c.ID)
			}
		}
	}
	walk(id)
	return out
}

// Counts returns the number of direct children and of leaf descendants of an
// item.
func (s *Store) Counts(id int) (children, leaves int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var walk func(id int) int
	walk = func(id int) int {
		n := 0
		for _, c := range s.children[id] {
			if IsLeaf(c.Type) {
				n++
			} else {
				n += walk(c.ID)
			}
		}
		return n
	}
	return len(s.children[id]), walk(id)
}

// Part returns the part with the given ID and the section it belongs to.
func (s *Store) Part(id int) (Part, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ref, ok := s.parts[id]; ok {
		p := *ref.part
		p.Streams = slices.Clone(p.Streams)
		return p, ref.item.SectionID, nil
	}
	return Part{}, 0, ErrNotFound
}

// KnownFile returns the stored size, modification time and media info of a
// file in a section, so scans can skip re-probing unchanged files.
func (s *Store) KnownFile(sectionID int, path string) (size, modTime int64, info probe.Info, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ref, ok := s.files[fileKey{sectionID, path}]
	if !ok {
		return 0, 0, probe.Info{}, false
	}
	info = ref.media.Info
	info.Streams = nil
	for _, st := range ref.part.Streams {
		if st.File == "" {
			info.Streams = append(info.Streams, st.Stream)
		}
	}
	return ref.part.Size, ref.part.ModTime, info, true
}

// Stream returns a stream by ID with its part and section.
func (s *Store) Stream(id int) (Stream, Part, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ref, ok := s.streams[id]; ok {
		p := *ref.part
		p.Streams = slices.Clone(p.Streams)
		return *ref.stream, p, ref.item.SectionID, nil
	}
	return Stream{}, Part{}, 0, ErrNotFound
}

// SetImages replaces the artwork of every item in a section. Items missing
// from images lose their artwork.
func (s *Store) SetImages(sectionID int, images map[int]map[string]Image) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.d.Items {
		if it.SectionID == sectionID {
			it.Images = maps.Clone(images[it.ID])
		}
	}
	return s.saveLocked()
}

// ApplyScan replaces the contents of a section with the scanned files,
// keeping item, media and part IDs for what was already known. See the
// scanning rules in docs/spec/http-api.md.
func (s *Store) ApplyScan(sectionID int, files []ScannedFile, now time.Time) (ScanStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sec := s.sectionLocked(sectionID)
	if sec == nil {
		return ScanStats{}, ErrNotFound
	}
	a := newApplier(s, sec, now)
	switch sec.Type {
	case TypeMovie:
		a.movies(files)
	case TypeShow:
		a.shows(files)
	default:
		return ScanStats{}, fmt.Errorf("section %q: unsupported type %q", sec.Title, sec.Type)
	}
	a.finish()
	sec.ScannedAt = now.Unix()
	s.reindexLocked()
	return a.stats, s.saveLocked()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(&s.d)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, b, 0o644)
}

func (sec *Section) clone() Section {
	c := *sec
	c.Paths = slices.Clone(sec.Paths)
	return c
}

func (it *Item) clone() Item {
	c := *it
	c.Images = maps.Clone(it.Images)
	if it.Media != nil {
		c.Media = make([]Media, len(it.Media))
		for i, m := range it.Media {
			m.Parts = slices.Clone(m.Parts)
			for j := range m.Parts {
				m.Parts[j].Streams = slices.Clone(m.Parts[j].Streams)
			}
			c.Media[i] = m
		}
	}
	return c
}

func cloneAll(items []*Item) []Item {
	out := make([]Item, len(items))
	for i, it := range items {
		out[i] = it.clone()
	}
	return out
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
