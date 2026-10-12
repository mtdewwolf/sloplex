package server

import (
	"cmp"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mtdewwolf/sloplex/internal/library"
)

func (s *Server) homeHubRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /hubs", s.handleHomeHubs)
	mux.HandleFunc("GET /hubs/sections/{id}", s.handleHomeHubs)
	mux.HandleFunc("GET /hubs/home/continueWatching", s.handleContinueItems)
	mux.HandleFunc("GET /hubs/continueWatching/items", s.handleContinueItems)
	mux.HandleFunc("GET /hubs/sections/{id}/continueWatching/items", s.handleContinueItems)
	mux.HandleFunc("GET /hubs/continueWatching", s.handleContinueHub)
	mux.HandleFunc("GET /hubs/home/onDeck", s.handleHomeOnDeck)
	mux.HandleFunc("GET /hubs/home/recentlyAdded", s.handleHomeRecent)
}

// hubCount caps each inline hub, independently of container paging.
func hubCount(w http.ResponseWriter, r *http.Request) (int, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["count"]) > 1 {
		http.Error(w, "invalid hub query", http.StatusBadRequest)
		return 0, false
	}
	if query.Has("hubIdentifiers") {
		http.Error(w, "hub identifier filtering is unsupported", http.StatusBadRequest)
		return 0, false
	}
	count := 10
	if query.Has("count") {
		count, err = strconv.Atoi(query.Get("count"))
		if err != nil || count < 0 || count > 100 {
			http.Error(w, "count must be between 0 and 100", http.StatusBadRequest)
			return 0, false
		}
	}
	return count, true
}

func (s *Server) visiblePlayable(r *http.Request, items []library.Item) []library.Item {
	see := s.visibility(r)
	return slices.DeleteFunc(items, func(it library.Item) bool { return !see(it.SectionID) || it.Unavailable })
}

// continueItems uses local resume state. Plex's recommendation policy is not
// reconstructed; this selection policy is documented as Sloplex behavior.
func (s *Server) continueItems(r *http.Request, section int) []library.Item {
	states := s.play.All(s.account(r))
	items := s.visiblePlayable(r, s.store.Items(section, library.TypeMovie, library.TypeEpisode))
	items = slices.DeleteFunc(items, func(it library.Item) bool { return states[it.ID].ViewOffset <= 0 })
	slices.SortFunc(items, func(a, b library.Item) int {
		return cmp.Or(cmp.Compare(states[b.ID].LastViewedAt, states[a.ID].LastViewedAt), cmp.Compare(a.ID, b.ID))
	})
	return items
}

func (s *Server) recentHubItems(r *http.Request, section int, typ string) []library.Item {
	items := s.visiblePlayable(r, s.store.Items(section, typ))
	s.sortItems(items, "addedAt:desc", "")
	return items
}

func (s *Server) makeHomeHub(r *http.Request, id, title, typ, key, context string, items []library.Item, count int) Hub {
	items = s.visiblePlayable(r, items)
	hub := Hub{HubIdentifier: id, Title: title, Type: typ, Key: key, Context: context, More: Bool(len(items) > count)}
	var keys []string
	for _, item := range items[:min(count, len(items))] {
		hub.Metadata = append(hub.Metadata, s.metadata(item, s.account(r)))
		keys = append(keys, strconv.Itoa(item.ID))
	}
	hub.Size = len(hub.Metadata)
	if len(keys) > 0 {
		hub.HubKey = "/library/metadata/" + strings.Join(keys, ",")
	}
	return hub
}

func (s *Server) handleHomeHubs(w http.ResponseWriter, r *http.Request) {
	count, ok := hubCount(w, r)
	if !ok {
		return
	}
	mc := MediaContainer{Identifier: "com.plexapp.plugins.library"}
	if r.PathValue("id") != "" {
		section, ok := s.section(w, r)
		if !ok {
			return
		}
		mc.LibrarySectionID, mc.LibrarySectionTitle = section.ID, section.Title
		id := strconv.Itoa(section.ID)
		typ := library.TypeMovie
		if section.Type == library.TypeShow {
			typ = library.TypeEpisode
		}
		mc.Hub = []Hub{
			s.makeHomeHub(r, section.Type+".inprogress."+id, "Continue Watching", typ, "/hubs/sections/"+id+"/continueWatching/items", "hub."+section.Type+".inprogress", s.continueItems(r, section.ID), count),
			s.makeHomeHub(r, section.Type+".recentlyadded."+id, "Recently Added in "+section.Title, typ, "/library/sections/"+id+"/recentlyAdded", "hub."+section.Type+".recentlyadded", s.recentHubItems(r, section.ID, typ), count),
		}
	} else {
		deck := slices.DeleteFunc(s.onDeck(s.account(r), 0), func(it library.Item) bool { return it.Type != library.TypeEpisode })
		mc.Hub = []Hub{
			s.makeHomeHub(r, "home.continue", "Continue Watching", "mixed", "/hubs/home/continueWatching", "hub.home.continue", s.continueItems(r, 0), count),
			s.makeHomeHub(r, "home.ondeck", "On Deck", "episode", "/hubs/home/onDeck", "hub.home.ondeck", deck, count),
			s.makeHomeHub(r, "home.movies.recent", "Recently Added Movies", "movie", "/hubs/home/recentlyAdded?type=1", "hub.home.movies.recent", s.recentHubItems(r, 0, library.TypeMovie), count),
			s.makeHomeHub(r, "home.television.recent", "Recently Added TV", "mixed", "/hubs/home/recentlyAdded?type=2", "hub.home.television.recent", s.recentHubItems(r, 0, library.TypeEpisode), count),
		}
	}
	writeContainer(w, r, mc)
}

func (s *Server) handleContinueHub(w http.ResponseWriter, r *http.Request) {
	count, ok := hubCount(w, r)
	if !ok {
		return
	}
	hub := s.makeHomeHub(r, "continueWatching", "Continue Watching", "mixed", "/hubs/continueWatching/items", "hub.continueWatching", s.continueItems(r, 0), count)
	writeContainer(w, r, MediaContainer{Identifier: "com.plexapp.plugins.library", Hub: []Hub{hub}})
}

func (s *Server) handleContinueItems(w http.ResponseWriter, r *http.Request) {
	section := 0
	mc := MediaContainer{Identifier: "com.plexapp.plugins.library", Title1: "Continue Watching"}
	if r.PathValue("id") != "" {
		sec, ok := s.section(w, r)
		if !ok {
			return
		}
		section = sec.ID
		mc.LibrarySectionID, mc.LibrarySectionTitle = sec.ID, sec.Title
	}
	s.page(r, &mc, s.continueItems(r, section), 50)
	writeContainer(w, r, mc)
}

func (s *Server) handleHomeOnDeck(w http.ResponseWriter, r *http.Request) {
	items := s.visiblePlayable(r, s.onDeck(s.account(r), 0))
	items = slices.DeleteFunc(items, func(it library.Item) bool { return it.Type != library.TypeEpisode })
	mc := MediaContainer{Identifier: "com.plexapp.plugins.library", Title1: "On Deck"}
	s.page(r, &mc, items, 50)
	writeContainer(w, r, mc)
}

func (s *Server) handleHomeRecent(w http.ResponseWriter, r *http.Request) {
	typ := library.TypeMovie
	switch r.URL.Query().Get("type") {
	case "", "1":
	case "2":
		typ = library.TypeEpisode
	default:
		http.Error(w, "unsupported recently-added type", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("personal") != "" && r.URL.Query().Get("personal") != "0" {
		http.Error(w, "personal-video libraries are unsupported", http.StatusBadRequest)
		return
	}
	mc := MediaContainer{Identifier: "com.plexapp.plugins.library", Title1: "Recently Added"}
	s.page(r, &mc, s.recentHubItems(r, 0, typ), 50)
	writeContainer(w, r, mc)
}
