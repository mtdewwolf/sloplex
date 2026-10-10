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

func TestDeleteAccount(t *testing.T) {
	s, _ := Open("")
	_ = s.Update(1, []int{10}, func(st *State) { st.ViewCount = 1 })
	_ = s.Update(2, []int{10}, func(st *State) { st.ViewCount = 3 })
	if err := s.DeleteAccount(2); err != nil {
		t.Fatal(err)
	}
	if len(s.All(2)) != 0 || s.Get(1, 10).ViewCount != 1 {
		t.Errorf("after delete: account 2 = %v, account 1 = %+v", s.All(2), s.Get(1, 10))
	}
}
