package playlists

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0)

func order(p Playlist) string {
	out := ""
	for _, e := range p.Entries {
		out += fmt.Sprint(e.ItemID)
	}
	return out
}

func TestPlaylists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playlists.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Create(1, "Friday", []int{1, 2, 3}, t0)
	if err != nil || order(p) != "123" || p.Account != 1 {
		t.Fatalf("create = %+v, %v", p, err)
	}
	other, _ := s.Create(2, "Theirs", []int{9}, t0)
	if other.ID == p.ID {
		t.Error("playlist IDs repeat")
	}

	// Duplicates get their own entries.
	later := t0.Add(time.Minute)
	if p, err = s.Add(1, p.ID, []int{2}, later); err != nil || order(p) != "1232" || p.UpdatedAt != later.Unix() {
		t.Errorf("add = %+v, %v", p, err)
	}
	if p.Entries[1].ID == p.Entries[3].ID {
		t.Error("duplicate items share an entry ID")
	}
	ids := []int{p.Entries[0].ID, p.Entries[1].ID, p.Entries[2].ID, p.Entries[3].ID}
	e := func(i int) int { return ids[i] }

	for name, c := range map[string]struct {
		entry, after int
		want         string
	}{
		"to top":            {e(2), 0, "3122"},
		"after another":     {e(0), e(3), "2321"},
		"after itself":      {e(1), e(1), "1232"},
		"last after last":   {e(3), e(2), "1232"},
		"first after third": {e(0), e(2), "2312"},
	} {
		got, err := s.Move(1, p.ID, c.entry, c.after, later)
		if err != nil || order(got) != c.want {
			t.Errorf("%s: %s, %v; want %s", name, order(got), err, c.want)
		}
		// Undo by restoring the original order.
		s.mu.Lock()
		s.findLocked(1, p.ID).Entries = append([]Entry(nil), p.Entries...)
		s.mu.Unlock()
	}
	if _, err := s.Move(1, p.ID, e(0), other.Entries[0].ID, later); !errors.Is(err, ErrBadAfter) {
		t.Errorf("move after another playlist's entry: %v", err)
	}
	if _, err := s.Move(1, p.ID, 999, 0, later); !errors.Is(err, ErrNotFound) {
		t.Errorf("move unknown entry: %v", err)
	}
	if p, err = s.Remove(1, p.ID, e(1), later); err != nil || order(p) != "132" {
		t.Errorf("remove = %s, %v", order(p), err)
	}
	if _, err := s.Remove(1, p.ID, e(1), later); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove twice: %v", err)
	}
	if p, err = s.Rename(1, p.ID, "Saturday", later); err != nil || p.Title != "Saturday" {
		t.Errorf("rename = %+v, %v", p, err)
	}

	// Accounts see only their own playlists.
	if _, err := s.Get(2, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("other account Get: %v", err)
	}
	for name, err := range map[string]error{
		"add":    func() error { _, err := s.Add(2, p.ID, []int{1}, later); return err }(),
		"rename": func() error { _, err := s.Rename(2, p.ID, "x", later); return err }(),
		"delete": s.Delete(2, p.ID),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("other account %s: %v", name, err)
		}
	}
	if l := s.List(1); len(l) != 1 || l[0].Title != "Saturday" {
		t.Errorf("List(1) = %+v", l)
	}

	// Limits.
	MaxEntries, MaxPlaylists = 4, 2
	defer func() { MaxEntries, MaxPlaylists = 5000, 500 }()
	if _, err := s.Add(1, p.ID, []int{7, 8}, later); !errors.Is(err, ErrLimit) {
		t.Errorf("entry limit: %v", err)
	}
	if _, err := s.Create(1, "Second", nil, later); err != nil {
		t.Errorf("second playlist: %v", err)
	}
	if _, err := s.Create(1, "Third", nil, later); !errors.Is(err, ErrLimit) {
		t.Errorf("playlist limit: %v", err)
	}
	if _, err := s.Create(2, "Big", []int{1, 2, 3, 4, 5}, later); !errors.Is(err, ErrLimit) {
		t.Errorf("entries on create: %v", err)
	}

	// Everything survives a reload; IDs keep counting.
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(1, p.ID)
	if err != nil || order(got) != "132" || got.Title != "Saturday" {
		t.Errorf("reloaded = %+v, %v", got, err)
	}
	n, _ := r.Create(3, "New", []int{1}, later)
	if n.ID <= other.ID || n.Entries[0].ID <= p.Entries[len(p.Entries)-1].ID {
		t.Errorf("IDs reused after reload: %+v", n)
	}

	if err := r.DeleteAccount(1); err != nil {
		t.Fatal(err)
	}
	if l := r.List(1); len(l) != 0 {
		t.Errorf("after DeleteAccount: %+v", l)
	}
	if l := r.List(2); len(l) != 1 {
		t.Errorf("other account lost playlists: %+v", l)
	}
	if err := r.Delete(2, other.ID); err != nil {
		t.Error(err)
	}
	if _, err := r.Get(2, other.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted playlist: %v", err)
	}
}
