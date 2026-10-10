package server

import (
	"encoding/xml"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/playlists"
)

// Playlists; see docs/spec/playlists.md.

func (s *Server) playlistRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /playlists", s.handlePlaylists)
	mux.HandleFunc("POST /playlists", s.handleCreatePlaylist)
	mux.HandleFunc("GET /playlists/{id}", s.handlePlaylist)
	mux.HandleFunc("PUT /playlists/{id}", s.handleRenamePlaylist)
	mux.HandleFunc("DELETE /playlists/{id}", s.handleDeletePlaylist)
	mux.HandleFunc("GET /playlists/{id}/items", s.handlePlaylistItems)
	mux.HandleFunc("PUT /playlists/{id}/items", s.handleAddToPlaylist)
	mux.HandleFunc("DELETE /playlists/{id}/items/{entry}", s.handleRemoveFromPlaylist)
	mux.HandleFunc("PUT /playlists/{id}/items/{entry}/move", s.handleMovePlaylistEntry)
}

// playlistEntry is an entry whose item the account can see.
type playlistEntry struct {
	entry int
	item  library.Item
}

// visibleEntries resolves a playlist's entries, leaving out items that are
// gone or hidden from the account.
func (s *Server) visibleEntries(r *http.Request, p playlists.Playlist) []playlistEntry {
	see := s.visibility(r)
	var out []playlistEntry
	for _, e := range p.Entries {
		if it, err := s.store.Item(e.ItemID); err == nil && see(it.SectionID) && library.IsLeaf(it.Type) {
			out = append(out, playlistEntry{e.ID, it})
		}
	}
	return out
}

func (s *Server) playlistMetadata(r *http.Request, p playlists.Playlist) Metadata {
	rk := strconv.Itoa(p.ID)
	md := Metadata{
		XMLName: xml.Name{Local: "Playlist"}, RatingKey: rk, Key: "/playlists/" + rk + "/items",
		GUID: "sloplex://playlist/" + rk, Type: "playlist", Title: p.Title, PlaylistType: "video", Smart: "0",
		AddedAt: p.AddedAt, UpdatedAt: p.UpdatedAt,
	}
	for _, e := range s.visibleEntries(r, p) {
		md.LeafCount++
		if len(e.item.Media) > 0 {
			md.Duration += e.item.Media[0].Duration
		}
		if md.Thumb == "" {
			md.Thumb = s.imageURL(e.item, library.ImageThumb)
		}
	}
	return md
}

// playlist resolves the {id} path value to one of the account's playlists,
// writing a 404 otherwise.
func (s *Server) playlist(w http.ResponseWriter, r *http.Request) (playlists.Playlist, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err == nil {
		if p, err := s.playlists.Get(s.account(r), id); err == nil {
			return p, true
		}
	}
	http.NotFound(w, r)
	return playlists.Playlist{}, false
}

func (s *Server) writePlaylist(w http.ResponseWriter, r *http.Request, p playlists.Playlist) {
	writeContainer(w, r, MediaContainer{Metadata: []Metadata{s.playlistMetadata(r, p)}})
}

func (s *Server) playlistError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, playlists.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, playlists.ErrLimit), errors.Is(err, playlists.ErrBadAfter):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		s.log.Error("playlist", "err", err)
		http.Error(w, "could not save the playlist", http.StatusInternalServerError)
	}
}

// playlistItems expands the uri or ids parameter into leaf item IDs the
// account can see: shows and seasons add their episodes, specials last.
func (s *Server) playlistItems(r *http.Request) ([]int, error) {
	ids, err := itemIDs(r)
	if err != nil {
		return nil, err
	}
	see := s.visibility(r)
	var out []int
	for _, id := range ids {
		it, err := s.store.Item(id)
		if err != nil || !see(it.SectionID) {
			return nil, errors.New("item " + strconv.Itoa(id) + " is not an item you can see")
		}
		switch it.Type {
		case library.TypeMovie, library.TypeEpisode:
			out = append(out, it.ID)
		case library.TypeSeason:
			for _, ep := range s.store.Children(it.ID) {
				out = append(out, ep.ID)
			}
		case library.TypeShow:
			var specials []int
			for _, season := range s.store.Children(it.ID) {
				for _, ep := range s.store.Children(season.ID) {
					if season.Index == 0 {
						specials = append(specials, ep.ID)
					} else {
						out = append(out, ep.ID)
					}
				}
			}
			out = append(out, specials...)
		default:
			return nil, errors.New("item " + strconv.Itoa(id) + " cannot be in a playlist")
		}
	}
	return out, nil
}

func (s *Server) handlePlaylists(w http.ResponseWriter, r *http.Request) {
	mc := MediaContainer{}
	if t := r.URL.Query().Get("playlistType"); t == "" || t == "video" {
		list := s.playlists.List(s.account(r))
		slices.SortStableFunc(list, func(a, b playlists.Playlist) int {
			if c := strings.Compare(library.SortTitle(a.Title), library.SortTitle(b.Title)); c != 0 {
				return c
			}
			return a.ID - b.ID
		})
		for _, p := range list {
			mc.Metadata = append(mc.Metadata, s.playlistMetadata(r, p))
		}
	}
	mc.Size = len(mc.Metadata)
	writeContainer(w, r, mc)
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.playlist(w, r); ok {
		s.writePlaylist(w, r, p)
	}
}

func (s *Server) handlePlaylistItems(w http.ResponseWriter, r *http.Request) {
	p, ok := s.playlist(w, r)
	if !ok {
		return
	}
	acct := s.account(r)
	entries := s.visibleEntries(r, p)
	mc := MediaContainer{Title1: p.Title}
	start, end, paged := pageBounds(r, len(entries), -1)
	for _, e := range entries[start:end] {
		md := s.metadata(e.item, acct)
		md.PlaylistItemID = e.entry
		mc.Metadata = append(mc.Metadata, md)
	}
	if total := len(entries); paged {
		mc.TotalSize, mc.Offset = &total, &start
	}
	writeContainer(w, r, mc)
}

func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if t := q.Get("type"); t != "" && t != "video" {
		http.Error(w, "only video playlists are supported", http.StatusBadRequest)
		return
	}
	if q.Get("smart") == "1" {
		http.Error(w, "smart playlists are not supported", http.StatusBadRequest)
		return
	}
	title, err := collectionTitle(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.playlistItems(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, err := s.playlists.Create(s.account(r), title, items, time.Now())
	if err != nil {
		s.playlistError(w, r, err)
		return
	}
	s.writePlaylist(w, r, p)
}

// editPlaylist runs a change on one of the account's playlists.
func (s *Server) editPlaylist(w http.ResponseWriter, r *http.Request, f func(acct, id int) (playlists.Playlist, error)) {
	p, ok := s.playlist(w, r)
	if !ok {
		return
	}
	p, err := f(s.account(r), p.ID)
	if err != nil {
		s.playlistError(w, r, err)
		return
	}
	s.writePlaylist(w, r, p)
}

func (s *Server) handleAddToPlaylist(w http.ResponseWriter, r *http.Request) {
	items, err := s.playlistItems(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.editPlaylist(w, r, func(acct, id int) (playlists.Playlist, error) {
		return s.playlists.Add(acct, id, items, time.Now())
	})
}

func (s *Server) handleRemoveFromPlaylist(w http.ResponseWriter, r *http.Request) {
	entry, err := strconv.Atoi(r.PathValue("entry"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.editPlaylist(w, r, func(acct, id int) (playlists.Playlist, error) {
		return s.playlists.Remove(acct, id, entry, time.Now())
	})
}

func (s *Server) handleMovePlaylistEntry(w http.ResponseWriter, r *http.Request) {
	entry, err := strconv.Atoi(r.PathValue("entry"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	after := 0
	if v := r.URL.Query().Get("after"); v != "" {
		if after, err = strconv.Atoi(v); err != nil || after < 0 {
			http.Error(w, "after must be a playlistItemID", http.StatusBadRequest)
			return
		}
	}
	s.editPlaylist(w, r, func(acct, id int) (playlists.Playlist, error) {
		return s.playlists.Move(acct, id, entry, after, time.Now())
	})
}

func (s *Server) handleRenamePlaylist(w http.ResponseWriter, r *http.Request) {
	title, err := collectionTitle(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.editPlaylist(w, r, func(acct, id int) (playlists.Playlist, error) {
		return s.playlists.Rename(acct, id, title, time.Now())
	})
}

func (s *Server) handleDeletePlaylist(w http.ResponseWriter, r *http.Request) {
	p, ok := s.playlist(w, r)
	if !ok {
		return
	}
	if err := s.playlists.Delete(s.account(r), p.ID); err != nil {
		s.playlistError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
