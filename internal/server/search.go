package server

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/search"
)

// Search endpoints; see docs/spec/search.md.

var hubTitles = []struct{ typ, title string }{
	{library.TypeMovie, "Movies"},
	{library.TypeShow, "Shows"},
	{library.TypeEpisode, "Episodes"},
}

// parseQuery reads the query parameter, writing a 400 if it is unusable.
func parseQuery(w http.ResponseWriter, r *http.Request) (search.Query, bool) {
	q, ok := search.Parse(r.URL.Query().Get("query"))
	if !ok {
		http.Error(w, "query is required and must be at most 200 characters", http.StatusBadRequest)
	}
	return q, ok
}

// context returns the extra searchable text of an item: the show (and
// season) title for episodes and seasons.
func (s *Server) searchContext(it library.Item) string {
	switch it.Type {
	case library.TypeSeason:
		show, _ := s.store.Item(it.ParentID)
		return show.Title
	case library.TypeEpisode:
		season, _ := s.store.Item(it.ParentID)
		show, _ := s.store.Item(season.ParentID)
		return show.Title + " " + season.Title
	}
	return ""
}

// searchItems returns the visible items of the given types in a section (0
// for all) that match q, best first.
func (s *Server) searchItems(r *http.Request, q search.Query, sectionID int, types ...string) []library.Item {
	see := s.visibility(r)
	type hit struct {
		item  library.Item
		score int
		sort  string
	}
	var hits []hit
	for _, it := range s.store.Items(sectionID, types...) {
		if !see(it.SectionID) {
			continue
		}
		score := q.Score(it.Title, s.searchContext(it), it.Year)
		if it.Meta != nil && it.Meta.OriginalTitle != "" {
			score = max(score, q.Score(it.Meta.OriginalTitle, s.searchContext(it), it.Year))
		}
		if score > 0 {
			hits = append(hits, hit{it, score, it.SortKey()})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.sort, b.sort), cmp.Compare(a.item.ID, b.item.ID))
	})
	out := make([]library.Item, len(hits))
	for i, h := range hits {
		out[i] = h.item
	}
	return out
}

func (s *Server) handleHubSearch(w http.ResponseWriter, r *http.Request) {
	q, ok := parseQuery(w, r)
	if !ok {
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			http.Error(w, "limit must be between 1 and 100", http.StatusBadRequest)
			return
		}
		limit = n
	}
	sectionID := 0
	if v := r.URL.Query().Get("sectionId"); v != "" {
		id, err := strconv.Atoi(v)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		sec, err := s.store.Section(id)
		if err != nil || !s.visibility(r)(sec.ID) {
			http.NotFound(w, r)
			return
		}
		sectionID = sec.ID
	}

	acct := s.account(r)
	mc := MediaContainer{}
	for _, h := range hubTitles {
		items := s.searchItems(r, q, sectionID, h.typ)
		if len(items) == 0 {
			continue
		}
		hub := Hub{HubIdentifier: h.typ, Type: h.typ, Title: h.title, More: Bool(len(items) > limit)}
		for _, it := range items[:min(limit, len(items))] {
			hub.Metadata = append(hub.Metadata, s.metadata(it, acct))
		}
		hub.Size = len(hub.Metadata)
		mc.Hub = append(mc.Hub, hub)
	}
	writeContainer(w, r, mc)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q, ok := parseQuery(w, r)
	if !ok {
		return
	}
	sectionID := 0
	allowed := []string{library.TypeMovie, library.TypeShow, library.TypeSeason, library.TypeEpisode}
	types := []string{library.TypeMovie, library.TypeShow, library.TypeEpisode}
	mc := MediaContainer{Title1: "Search"}
	if r.PathValue("id") != "" {
		sec, ok := s.section(w, r)
		if !ok {
			return
		}
		sectionID = sec.ID
		mc.LibrarySectionID, mc.LibrarySectionTitle = sec.ID, sec.Title
		allowed = sectionTypes[sec.Type]
		types = []string{library.TypeMovie}
		if sec.Type == library.TypeShow {
			types = []string{library.TypeShow, library.TypeEpisode}
		}
	}
	if typ := r.URL.Query().Get("type"); typ != "" {
		if name, ok := typeNumbers[typ]; ok {
			typ = name
		}
		types = nil
		if slices.Contains(allowed, typ) {
			types = []string{typ}
		}
	}
	var items []library.Item
	if len(types) > 0 {
		items = s.searchItems(r, q, sectionID, types...)
	}
	s.page(r, &mc, items, -1)
	writeContainer(w, r, mc)
}
