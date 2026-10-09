// Package playstate stores per-account watched and resume state for leaf
// items. See docs/spec/playstate.md.
package playstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/mtdewwolf/sloplex/internal/atomicfile"
)

// State is the play state of one item for one account.
type State struct {
	ViewCount    int   `json:"viewCount,omitempty"`
	ViewOffset   int64 `json:"viewOffset,omitempty"` // milliseconds
	LastViewedAt int64 `json:"lastViewedAt,omitempty"`
}

const version = 1

type data struct {
	Version  int                   `json:"version"`
	Accounts map[int]map[int]State `json:"accounts"`
}

// Store holds play state in memory and saves it as JSON after every change.
// It is safe for concurrent use.
type Store struct {
	mu   sync.RWMutex
	path string
	d    data
}

// Open loads the store from path. A missing file yields an empty store; an
// empty path yields an in-memory store that is never saved.
func Open(path string) (*Store, error) {
	s := &Store{path: path, d: data{Version: version}}
	if path != "" {
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
	}
	if s.d.Accounts == nil {
		s.d.Accounts = map[int]map[int]State{}
	}
	return s, nil
}

// Get returns the state of one item.
func (s *Store) Get(account, item int) State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.d.Accounts[account][item]
}

// All returns a copy of every stored state of an account.
func (s *Store) All(account int) map[int]State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[int]State, len(s.d.Accounts[account]))
	for id, st := range s.d.Accounts[account] {
		out[id] = st
	}
	return out
}

// Update applies fn to the state of each item and saves the result. States
// that become zero are dropped.
func (s *Store) Update(account int, items []int, fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.d.Accounts[account]
	if m == nil {
		m = map[int]State{}
		s.d.Accounts[account] = m
	}
	for _, id := range items {
		st := m[id]
		fn(&st)
		if st == (State{}) {
			delete(m, id)
		} else {
			m[id] = st
		}
	}
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(&s.d)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, b, 0o644)
}
