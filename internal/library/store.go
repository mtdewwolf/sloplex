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
	"path/filepath"
	"regexp"
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
	ID   int    `json:"id"`
	UUID string `json:"uuid"`
	// ConfigID is the optional owner-selected configuration identity. It is
	// separate from the numeric API ID and UUID, and survives display edits.
	ConfigID  string   `json:"configId,omitempty"`
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
	// ScanKey is a show's parsed title/year identity before NFO overrides.
	// Older stores recover it from known episode paths on their next scan.
	// It is local scanner state, not an HTTP metadata attribute.
	ScanKey string `json:"scanKey,omitempty"`
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
	// Unavailable is derived from retained parts/descendants; see trash.md.
	Unavailable bool `json:"unavailable,omitempty"`
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
	ID          int    `json:"id"`
	Unavailable bool   `json:"unavailable,omitempty"`
	File        string `json:"file"`
	Size        int64  `json:"size"`
	ModTime     int64  `json:"modTime"`
	Duration    int64  `json:"duration,omitempty"`
	Container   string `json:"container,omitempty"`
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
	ConfigID string
	Title    string
	Type     string
	Paths    []string
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
	Files, Added, Updated, Removed, Unavailable int
}

// ErrNotFound is returned for unknown sections, items and parts.
var ErrNotFound = errors.New("not found")

// ErrInvalidSections marks an invalid or ambiguous configuration sync.
var ErrInvalidSections = errors.New("invalid section configuration")

var sectionConfigIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

const storeVersion = 1

type storeData struct {
	Version       int           `json:"version"`
	NextSectionID int           `json:"nextSectionId"`
	NextItemID    int           `json:"nextItemId"`
	NextMediaID   int           `json:"nextMediaId"`
	NextPartID    int           `json:"nextPartId"`
	NextStreamID  int           `json:"nextStreamId,omitempty"`
	Sections      []*Section    `json:"sections"`
	Items         []*Item       `json:"items"`
	Collections   []*Collection `json:"collections,omitempty"`
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
	// writeFile is an optional per-store writer; nil uses atomicfile.Write.
	writeFile func(string, []byte, os.FileMode) error

	// Indexes, rebuilt by reindexLocked after every change.
	byID     map[int]*Item
	children map[int][]*Item // ordered by Index, then ID
	parts    map[int]fileRef
	files    map[fileKey]fileRef
	streams  map[int]streamRef
	memberOf map[int][]*Collection // item ID -> collections holding it
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
	s.reindexCollectionsLocked()
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

// SyncSections matches explicit configuration identities, with unambiguous
// exact title/type matching for legacy definitions. Omitted sections and
// their catalog/collections are removed. Invalid syncs leave state unchanged;
// see docs/spec/library-identity.md for staged adoption and rejection rules.
func (s *Store) SyncSections(defs []SectionDef, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateSectionDefs(defs); err != nil {
		return err
	}
	byConfigID := map[string]*Section{}
	byKey := map[string][]*Section{}
	for _, sec := range s.d.Sections {
		if sec.ConfigID != "" {
			if !sectionConfigIDPattern.MatchString(sec.ConfigID) || byConfigID[sec.ConfigID] != nil {
				return fmt.Errorf("%w: invalid or duplicate stored identity %q", ErrInvalidSections, sec.ConfigID)
			}
			byConfigID[sec.ConfigID] = sec
		}
		key := sec.Type + "\x00" + sec.Title
		byKey[key] = append(byKey[key], sec)
	}
	matched := make([]*Section, len(defs))
	keep := map[int]bool{}
	unresolved := map[string]int{}
	// Resolve explicit IDs first; migration decisions cannot depend on order.
	for i, def := range defs {
		if def.ConfigID != "" {
			if sec := byConfigID[def.ConfigID]; sec != nil {
				if sec.Type != def.Type {
					return fmt.Errorf("%w: identity %q cannot change type from %q to %q", ErrInvalidSections, def.ConfigID, sec.Type, def.Type)
				}
				if keep[sec.ID] {
					return fmt.Errorf("%w: duplicate stored numeric section ID %d", ErrInvalidSections, sec.ID)
				}
				matched[i], keep[sec.ID] = sec, true
				continue
			}
		}
		unresolved[def.Type+"\x00"+def.Title]++
	}
	for i, def := range defs {
		if matched[i] != nil {
			continue
		}
		key := def.Type + "\x00" + def.Title
		var candidates []*Section
		for _, sec := range byKey[key] {
			if def.ConfigID == "" || sec.ConfigID == "" {
				candidates = append(candidates, sec)
			}
		}
		if len(candidates) > 1 || len(candidates) != 0 && unresolved[key] > 1 {
			return fmt.Errorf("%w: ambiguous legacy match for %s library %q; use stored identities or adopt an ID before editing", ErrInvalidSections, def.Type, def.Title)
		}
		if len(candidates) == 1 {
			sec := candidates[0]
			if keep[sec.ID] {
				return fmt.Errorf("%w: section %q is referenced by more than one definition", ErrInvalidSections, sec.Title)
			}
			matched[i], keep[sec.ID] = sec, true
		}
	}
	for i, def := range defs {
		if matched[i] != nil {
			continue
		}
		for _, old := range s.d.Sections {
			if keep[old.ID] {
				continue
			}
			if old.Title == def.Title && old.Type != def.Type {
				return fmt.Errorf("%w: ambiguous type change for %q; remove the old library separately", ErrInvalidSections, def.Title)
			}
			if old.Type != def.Type {
				continue
			}
			if def.ConfigID != "" && old.ConfigID == "" {
				return fmt.Errorf("%w: cannot infer adoption of omitted legacy library %q; add its ID before changing its title", ErrInvalidSections, old.Title)
			}
			if old.Title == def.Title || sharedSectionRoot(old.Paths, def.Paths) {
				return fmt.Errorf("%w: possible rename or identity replacement of %q; retain its ID or adopt one before editing", ErrInvalidSections, old.Title)
			}
		}
	}

	ts := now.Unix()
	out := make([]*Section, 0, len(defs))
	nextID := s.d.NextSectionID
	for i, def := range defs {
		var sec *Section
		if old := matched[i]; old != nil {
			copy := old.clone()
			sec = &copy
		} else {
			sec = &Section{ID: next(&nextID), UUID: newUUID(), Type: def.Type, CreatedAt: ts, UpdatedAt: ts}
		}
		if def.ConfigID != "" {
			sec.ConfigID = def.ConfigID
		}
		if sec.Title != def.Title || !slices.Equal(sec.Paths, def.Paths) {
			sec.Title, sec.Paths, sec.UpdatedAt = def.Title, slices.Clone(def.Paths), ts
		}
		keep[sec.ID] = true
		out = append(out, sec)
	}
	previous := s.d
	s.d.NextSectionID = nextID
	s.d.Sections = out
	s.d.Items = slices.DeleteFunc(slices.Clone(s.d.Items), func(it *Item) bool { return !keep[it.SectionID] })
	s.d.Collections = slices.DeleteFunc(slices.Clone(s.d.Collections), func(c *Collection) bool { return !keep[c.SectionID] })
	s.reindexLocked()
	if err := s.saveLocked(); err != nil {
		var committed *atomicfile.CommittedError
		if !errors.As(err, &committed) {
			s.d = previous
			s.reindexLocked()
		}
		return err
	}
	return nil
}

func validateSectionDefs(defs []SectionDef) error {
	ids, names, legacy := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, def := range defs {
		if def.Title == "" || def.Type != TypeMovie && def.Type != TypeShow || len(def.Paths) == 0 {
			return fmt.Errorf("%w: each library needs a title, supported type and paths", ErrInvalidSections)
		}
		if def.ConfigID != "" {
			if !sectionConfigIDPattern.MatchString(def.ConfigID) || ids[def.ConfigID] {
				return fmt.Errorf("%w: invalid or duplicate identity %q", ErrInvalidSections, def.ConfigID)
			}
			ids[def.ConfigID] = true
		}
		key := def.Type + "\x00" + def.Title
		if names[key] && (def.ConfigID == "" || legacy[key]) {
			return fmt.Errorf("%w: duplicate legacy %s library %q", ErrInvalidSections, def.Type, def.Title)
		}
		names[key], legacy[key] = true, legacy[key] || def.ConfigID == ""
	}
	return nil
}

// sharedSectionRoot only rejects uncertain replacements; it never matches ID.
func sharedSectionRoot(a, b []string) bool {
	for _, x := range a {
		x, err := filepath.Abs(x)
		if err != nil {
			continue
		}
		for _, y := range b {
			y, err := filepath.Abs(y)
			if err == nil && x == y {
				return true
			}
		}
	}
	return false
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
// from images lose their artwork unless unavailable media is retained.
func (s *Store) SetImages(sectionID int, images map[int]map[string]Image) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.d.Items {
		if it.SectionID == sectionID {
			if it.Unavailable || slices.ContainsFunc(it.Media, func(m Media) bool { return !m.Available() }) {
				if it.Images == nil && len(images[it.ID]) > 0 {
					it.Images = map[string]Image{}
				}
				maps.Copy(it.Images, images[it.ID])
			} else {
				it.Images = maps.Clone(images[it.ID])
			}
		}
	}
	return s.saveLocked()
}

// ApplyScan reconciles a section with scanned files, retaining absent media,
// keeping item, media and part IDs for what was already known. See the
// scanning rules in docs/spec/http-api.md and docs/spec/scan-safety.md.
// The caller must provide an authoritative list, never a partial failed scan.
func (s *Store) ApplyScan(sectionID int, files []ScannedFile, now time.Time) (ScanStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stage := s.stagedLocked()
	sec := stage.sectionLocked(sectionID)
	if sec == nil {
		return ScanStats{}, ErrNotFound
	}
	a := newApplier(stage, sec, now)
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
	stage.reindexLocked()
	stage.syncCollectionsLocked(sec, now.Unix())
	if err := s.publishLocked(stage); err != nil {
		return ScanStats{}, err
	}
	return a.stats, nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(&s.d)
	if err != nil {
		return err
	}
	writeFile := s.writeFile
	if writeFile == nil {
		writeFile = atomicfile.Write
	}
	return writeFile(s.path, b, 0o644)
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
