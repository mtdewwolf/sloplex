package server

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/playstate"
)

// account returns the account a request is authenticated as, or 0 for an
// unauthenticated request to a public endpoint.
func (s *Server) account(r *http.Request) int {
	p, _ := principalOf(r)
	return p.AccountID
}

// completeRatio is the share of the duration after which an item counts as
// watched.
const completeRatio = 0.9

// playTarget resolves the ratingKey or key query parameter.
func (s *Server) playTarget(w http.ResponseWriter, r *http.Request) (library.Item, bool) {
	q := r.URL.Query()
	v := q.Get("ratingKey")
	if v == "" {
		v = strings.TrimPrefix(q.Get("key"), "/library/metadata/")
	}
	id, err := strconv.Atoi(v)
	if err != nil {
		http.Error(w, "missing or invalid key", http.StatusBadRequest)
		return library.Item{}, false
	}
	it, err := s.store.Item(id)
	if err != nil || !s.visibility(r)(it.SectionID) {
		http.NotFound(w, r)
		return library.Item{}, false
	}
	return it, true
}

// leafIDs returns the item itself if it is a leaf, or all its leaves.
func (s *Server) leafIDs(it library.Item) []int {
	if library.IsLeaf(it.Type) {
		return []int{it.ID}
	}
	leaves := s.store.Leaves(it.ID)
	ids := make([]int, len(leaves))
	for i, l := range leaves {
		ids[i] = l.ID
	}
	return ids
}

func (s *Server) updatePlay(w http.ResponseWriter, r *http.Request, ids []int, fn func(*playstate.State)) {
	if s.savePlay(w, r, ids, fn) {
		w.WriteHeader(http.StatusOK)
	}
}

// savePlay applies fn to the requesting account's state of ids. It writes
// only an error, and reports whether the change was saved.
func (s *Server) savePlay(w http.ResponseWriter, r *http.Request, ids []int, fn func(*playstate.State)) bool {
	if err := s.play.Update(s.account(r), ids, fn); err != nil {
		s.log.Error("save play state", "err", err)
		http.Error(w, "could not save play state", http.StatusInternalServerError)
		return false
	}
	return true
}

func (s *Server) handleScrobble(w http.ResponseWriter, r *http.Request) {
	it, ok := s.playTarget(w, r)
	if !ok {
		return
	}
	now := time.Now().Unix()
	s.updatePlay(w, r, s.leafIDs(it), func(st *playstate.State) {
		st.ViewCount++
		st.ViewOffset = 0
		st.LastViewedAt = now
	})
}

func (s *Server) handleUnscrobble(w http.ResponseWriter, r *http.Request) {
	it, ok := s.playTarget(w, r)
	if !ok {
		return
	}
	s.updatePlay(w, r, s.leafIDs(it), func(st *playstate.State) { *st = playstate.State{} })
}

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	if s.reportPosition(w, r, true) {
		w.WriteHeader(http.StatusOK)
	}
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if s.reportTimeline(w, r) {
		w.WriteHeader(http.StatusOK)
	}
}

// timelineStates are the documented values of state on POST /:/timeline.
var timelineStates = map[string]bool{"stopped": true, "buffering": true, "playing": true, "paused": true}

// handleTimelinePost is the documented POST form of /:/timeline; see
// docs/spec/wire-conformance.md. It needs a client identifier, checks
// state, and answers with an empty MediaContainer.
func (s *Server) handleTimelinePost(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if r.Header.Get("X-Plex-Client-Identifier") == "" && q.Get("X-Plex-Client-Identifier") == "" {
		http.Error(w, "X-Plex-Client-Identifier is required", http.StatusBadRequest)
		return
	}
	if st := q.Get("state"); st != "" && !timelineStates[st] {
		http.Error(w, "state must be stopped, buffering, playing or paused", http.StatusBadRequest)
		return
	}
	if s.reportTimeline(w, r) {
		writeContainer(w, r, MediaContainer{})
	}
}

// reportPosition applies a playback position using the completion rule in
// docs/spec/playstate.md. requireTime makes a missing time a 400 rather than
// a no-op. It writes only errors, and reports whether the request
// succeeded.
func (s *Server) reportPosition(w http.ResponseWriter, r *http.Request, requireTime bool) bool {
	it, ok := s.playTarget(w, r)
	if !ok {
		return false
	}
	if !library.IsLeaf(it.Type) {
		http.Error(w, "progress applies to movies and episodes only", http.StatusBadRequest)
		return false
	}
	q := r.URL.Query()
	t, err := strconv.ParseInt(q.Get("time"), 10, 64)
	if err != nil {
		if requireTime || q.Get("time") != "" {
			http.Error(w, "missing or invalid time", http.StatusBadRequest)
			return false
		}
		return true
	}
	d, err := strconv.ParseInt(q.Get("duration"), 10, 64)
	if err != nil || d <= 0 {
		d = 0
		if len(it.Media) > 0 {
			d = it.Media[0].Duration
		}
	}
	now := time.Now().Unix()
	return s.savePlay(w, r, []int{it.ID}, func(st *playstate.State) {
		switch {
		case d > 0 && float64(t) >= completeRatio*float64(d):
			if st.ViewOffset > 0 || st.ViewCount == 0 {
				st.ViewCount++
			}
			st.ViewOffset, st.LastViewedAt = 0, now
		case t > 0:
			st.ViewOffset, st.LastViewedAt = t, now
		}
	})
}

type rollup struct {
	leaves, viewed int
	lastViewedAt   int64
}

// rollup derives a show's or season's play state from its episodes.
func (s *Server) rollup(acct, id int) rollup {
	var r rollup
	for _, l := range s.store.Leaves(id) {
		st := s.play.Get(acct, l.ID)
		r.leaves++
		if st.ViewCount > 0 {
			r.viewed++
		}
		r.lastViewedAt = max(r.lastViewedAt, st.LastViewedAt)
	}
	return r
}

// unwatched keeps leaves never watched and containers not fully watched.
func (s *Server) unwatched(acct int, items []library.Item) []library.Item {
	return slices.DeleteFunc(items, func(it library.Item) bool {
		if library.IsLeaf(it.Type) {
			return s.play.Get(acct, it.ID).ViewCount > 0
		}
		r := s.rollup(acct, it.ID)
		return r.viewed >= r.leaves
	})
}

func (s *Server) handleOnDeck(w http.ResponseWriter, r *http.Request) {
	sectionID := 0
	mc := MediaContainer{Title1: "On Deck"}
	if r.PathValue("id") != "" {
		sec, ok := s.section(w, r)
		if !ok {
			return
		}
		sectionID = sec.ID
		mc.LibrarySectionID, mc.LibrarySectionTitle = sec.ID, sec.Title
	}
	s.page(r, &mc, s.onDeck(s.account(r), sectionID), -1)
	writeContainer(w, r, mc)
}

// onDeck lists in-progress items and the next episode of shows being
// watched, newest activity first. See docs/spec/playstate.md.
func (s *Server) onDeck(acct, sectionID int) []library.Item {
	states := s.play.All(acct)
	type entry struct {
		item library.Item
		at   int64
	}
	var deck []entry
	busyShows := map[int]bool{}
	showOf := func(ep library.Item) (show, season int) {
		sn, _ := s.store.Item(ep.ParentID)
		return sn.ParentID, sn.Index
	}

	for _, it := range s.store.Items(sectionID, library.TypeMovie, library.TypeEpisode) {
		if st := states[it.ID]; st.ViewOffset > 0 {
			deck = append(deck, entry{it, st.LastViewedAt})
			if it.Type == library.TypeEpisode {
				show, _ := showOf(it)
				busyShows[show] = true
			}
		}
	}

	for _, show := range s.store.Items(sectionID, library.TypeShow) {
		if busyShows[show.ID] {
			continue
		}
		eps := s.store.Leaves(show.ID)
		last, lastAt := -1, int64(0)
		for i, ep := range eps {
			if st := states[ep.ID]; st.ViewCount > 0 && st.LastViewedAt >= lastAt {
				last, lastAt = i, st.LastViewedAt
			}
		}
		if last < 0 {
			continue
		}
		for _, ep := range eps[last+1:] {
			if _, season := showOf(ep); season != 0 && states[ep.ID].ViewCount == 0 {
				deck = append(deck, entry{ep, lastAt})
				break
			}
		}
	}

	slices.SortStableFunc(deck, func(a, b entry) int {
		return cmp.Or(cmp.Compare(b.at, a.at), cmp.Compare(a.item.ID, b.item.ID))
	})
	out := make([]library.Item, len(deck))
	for i, e := range deck {
		out[i] = e.item
	}
	return out
}
