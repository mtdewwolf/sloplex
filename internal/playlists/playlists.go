// Package playlists stores per-account playlists of library items. See
// docs/spec/playlists.md.
package playlists

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

// Limits; variables so tests can lower them.
var (
	MaxEntries   = 5000
	MaxPlaylists = 500
)

var (
	// ErrNotFound is returned for unknown playlists and entries, and for
	// those of other accounts.
	ErrNotFound = errors.New("not found")
	// ErrLimit is wrapped when a limit would be exceeded.
	ErrLimit = errors.New("limit reached")
	// ErrBadAfter is returned when moving after an entry of another list.
	ErrBadAfter = errors.New("after is not an entry of this playlist")
)

// Entry is one position in a playlist.
type Entry struct {
	ID     int `json:"id"`
	ItemID int `json:"itemId"`
}

// Playlist is an ordered list of library items owned by one account.
type Playlist struct {
	ID        int     `json:"id"`
	Account   int     `json:"account"`
	Title     string  `json:"title"`
	Entries   []Entry `json:"entries,omitempty"`
	AddedAt   int64   `json:"addedAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

func (p *Playlist) clone() Playlist {
	c := *p
	c.Entries = slices.Clone(p.Entries)
	return c
}

const version = 1

type data struct {
	Version     int         `json:"version"`
	NextID      int         `json:"nextId"`
	NextEntryID int         `json:"nextEntryId"`
	Playlists   []*Playlist `json:"playlists"`
}

// Store holds playlists in memory and saves them as JSON after every
// change. It is safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	path string
	d    data
}

// Open loads the store from path. A missing file yields an empty store; an
// empty path yields an in-memory store that is never saved.
func Open(path string) (*Store, error) {
	s := &Store{path: path, d: data{Version: version}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &s.d); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if s.d.Version != version {
			return nil, fmt.Errorf("%s: unsupported version %d", path, s.d.Version)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(&s.d)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, b, 0o600)
}

func (s *Store) findLocked(account, id int) *Playlist {
	for _, p := range s.d.Playlists {
		if p.ID == id && p.Account == account {
			return p
		}
	}
	return nil
}

// List returns an account's playlists in creation order.
func (s *Store) List(account int) []Playlist {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Playlist
	for _, p := range s.d.Playlists {
		if p.Account == account {
			out = append(out, p.clone())
		}
	}
	return out
}

// Get returns one of an account's playlists.
func (s *Store) Get(account, id int) (Playlist, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.findLocked(account, id); p != nil {
		return p.clone(), nil
	}
	return Playlist{}, ErrNotFound
}

func (s *Store) entriesLocked(items []int) []Entry {
	out := make([]Entry, 0, len(items))
	for _, id := range items {
		s.d.NextEntryID++
		out = append(out, Entry{ID: s.d.NextEntryID, ItemID: id})
	}
	return out
}

// Create adds a playlist for an account.
func (s *Store) Create(account int, title string, items []int, now time.Time) (Playlist, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.d.Playlists {
		if p.Account == account {
			n++
		}
	}
	if n >= MaxPlaylists {
		return Playlist{}, fmt.Errorf("%w: at most %d playlists", ErrLimit, MaxPlaylists)
	}
	if len(items) > MaxEntries {
		return Playlist{}, fmt.Errorf("%w: at most %d entries", ErrLimit, MaxEntries)
	}
	s.d.NextID++
	p := &Playlist{ID: s.d.NextID, Account: account, Title: title, Entries: s.entriesLocked(items),
		AddedAt: now.Unix(), UpdatedAt: now.Unix()}
	s.d.Playlists = append(s.d.Playlists, p)
	return p.clone(), s.saveLocked()
}

// edit applies f to one of an account's playlists and saves it.
func (s *Store) edit(account, id int, now time.Time, f func(*Playlist) error) (Playlist, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findLocked(account, id)
	if p == nil {
		return Playlist{}, ErrNotFound
	}
	if err := f(p); err != nil {
		return Playlist{}, err
	}
	p.UpdatedAt = now.Unix()
	return p.clone(), s.saveLocked()
}

// Add appends items.
func (s *Store) Add(account, id int, items []int, now time.Time) (Playlist, error) {
	return s.edit(account, id, now, func(p *Playlist) error {
		if len(p.Entries)+len(items) > MaxEntries {
			return fmt.Errorf("%w: at most %d entries", ErrLimit, MaxEntries)
		}
		p.Entries = append(slices.Clone(p.Entries), s.entriesLocked(items)...)
		return nil
	})
}

func indexOf(entries []Entry, id int) int {
	return slices.IndexFunc(entries, func(e Entry) bool { return e.ID == id })
}

// Remove deletes one entry.
func (s *Store) Remove(account, id, entry int, now time.Time) (Playlist, error) {
	return s.edit(account, id, now, func(p *Playlist) error {
		i := indexOf(p.Entries, entry)
		if i < 0 {
			return ErrNotFound
		}
		p.Entries = slices.Delete(slices.Clone(p.Entries), i, i+1)
		return nil
	})
}

// Move puts an entry just after another, or first when after is 0.
func (s *Store) Move(account, id, entry, after int, now time.Time) (Playlist, error) {
	return s.edit(account, id, now, func(p *Playlist) error {
		i := indexOf(p.Entries, entry)
		if i < 0 {
			return ErrNotFound
		}
		if after != 0 && indexOf(p.Entries, after) < 0 {
			return ErrBadAfter
		}
		e := p.Entries[i]
		rest := slices.Delete(slices.Clone(p.Entries), i, i+1)
		at := 0
		if after != 0 && after != entry {
			at = indexOf(rest, after) + 1
		} else if after == entry {
			at = i // moving after itself leaves it in place
		}
		p.Entries = slices.Insert(rest, at, e)
		return nil
	})
}

// Rename retitles a playlist.
func (s *Store) Rename(account, id int, title string, now time.Time) (Playlist, error) {
	return s.edit(account, id, now, func(p *Playlist) error {
		p.Title = title
		return nil
	})
}

// Delete removes a playlist.
func (s *Store) Delete(account, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.findLocked(account, id) == nil {
		return ErrNotFound
	}
	s.d.Playlists = slices.DeleteFunc(s.d.Playlists, func(p *Playlist) bool { return p.ID == id })
	return s.saveLocked()
}

// DeleteAccount removes every playlist of an account.
func (s *Store) DeleteAccount(account int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Playlists = slices.DeleteFunc(s.d.Playlists, func(p *Playlist) bool { return p.Account == account })
	return s.saveLocked()
}
