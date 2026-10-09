package playstate

import (
	"path/filepath"
	"testing"
)

func TestUpdatePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playstate.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(1, []int{10, 11}, func(st *State) { st.ViewCount++; st.LastViewedAt = 5 }); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(1, []int{12}, func(st *State) { st.ViewOffset = 3000 }); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(2, []int{10}, func(st *State) { st.ViewOffset = 1 }); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get(1, 10); got != (State{ViewCount: 1, LastViewedAt: 5}) {
		t.Errorf("Get(1, 10) = %+v", got)
	}
	if got := s2.Get(1, 12).ViewOffset; got != 3000 {
		t.Errorf("offset = %d", got)
	}
	if got := s2.Get(2, 10).ViewOffset; got != 1 {
		t.Errorf("accounts not separate: %d", got)
	}

	// Zero states are dropped.
	if err := s2.Update(1, []int{10}, func(st *State) { *st = State{} }); err != nil {
		t.Fatal(err)
	}
	if all := s2.All(1); len(all) != 2 {
		t.Errorf("All = %+v, want 2 entries", all)
	}
}
