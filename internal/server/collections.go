package server

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
)

// Collections; see docs/spec/collections.md.

func (s *Server) collectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /library/sections/{id}/collections", s.handleSectionCollections)
	mux.HandleFunc("GET /library/collections/{id}", s.handleCollection)
	mux.HandleFunc("GET /library/collections/{id}/children", s.handleCollectionChildren)
	mux.HandleFunc("GET /library/collections/{id}/items", s.handleCollectionChildren)
	mux.HandleFunc("POST /library/collections", s.requireAdmin(s.handleCreateCollection))
	mux.HandleFunc("PUT /library/collections/{id}", s.requireAdmin(s.handleRenameCollection))
	mux.HandleFunc("DELETE /library/collections/{id}", s.requireAdmin(s.handleDeleteCollection))
	mux.HandleFunc("DELETE /library/sections/{id}/collection/{collection}", s.requireAdmin(s.handleDeleteSectionCollection))
	mux.HandleFunc("PUT /library/collections/{id}/items", s.requireAdmin(s.handleAddToCollection))
	mux.HandleFunc("DELETE /library/collections/{id}/items/{item}", s.requireAdmin(s.handleRemoveFromCollection))
}

// collection resolves the {id} path value to a visible collection. It
// writes nothing, so callers can fall back to items.
func (s *Server) collection(r *http.Request) (library.Collection, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return library.Collection{}, false
	}
	c, err := s.store.Collection(id)
	if err != nil || !s.visibility(r)(c.SectionID) {
		return library.Collection{}, false
	}
	return c, true
}

// collectionChildren returns a collection's members in display order.
func (s *Server) collectionChildren(c library.Collection) []library.Item {
	var out []library.Item
	for _, id := range c.ItemIDs {
		if it, err := s.store.Item(id); err == nil {
			out = append(out, it)
		}
	}
	slices.SortStableFunc(out, func(a, b library.Item) int {
		if a.Year != b.Year {
			return a.Year - b.Year
		}
		if c := strings.Compare(a.SortKey(), b.SortKey()); c != 0 {
			return c
		}
		return a.ID - b.ID
	})
	return out
}

func (s *Server) collectionMetadata(c library.Collection) Metadata {
	rk := strconv.Itoa(c.ID)
	kids := s.collectionChildren(c)
	subtype := library.TypeMovie
	if sec, err := s.store.Section(c.SectionID); err == nil && sec.Type == library.TypeShow {
		subtype = library.TypeShow
	}
	md := Metadata{
		XMLName: xmlName("collection"), RatingKey: rk, Key: "/library/collections/" + rk + "/children",
		GUID: "sloplex://collection/" + rk, Type: "collection", Subtype: subtype, Title: c.Title,
		ChildCount: len(kids), CollectionSource: c.Source, AddedAt: c.AddedAt, UpdatedAt: c.UpdatedAt,
		LibrarySectionID: c.SectionID, LibrarySectionKey: fmt.Sprintf("/library/sections/%d", c.SectionID),
	}
	if st := library.SortTitle(c.Title); st != strings.ToLower(c.Title) {
		md.TitleSort = st
	}
	if sec, err := s.store.Section(c.SectionID); err == nil {
		md.LibrarySectionTitle = sec.Title
	}
	for _, it := range kids {
		if thumb := s.imageURL(it, library.ImageThumb); thumb != "" {
			md.Thumb = thumb
			break
		}
	}
	return md
}

func (s *Server) handleSectionCollections(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.section(w, r)
	if !ok {
		return
	}
	mc := MediaContainer{Title1: sec.Title, LibrarySectionID: sec.ID, LibrarySectionTitle: sec.Title, ViewGroup: "collection"}
	cols := s.store.Collections(sec.ID)
	start, end, paged := pageBounds(r, len(cols), -1)
	for _, c := range cols[start:end] {
		mc.Metadata = append(mc.Metadata, s.collectionMetadata(c))
	}
	if total := len(cols); paged {
		mc.TotalSize, mc.Offset = &total, &start
	}
	writeContainer(w, r, mc)
}

func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.collection(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.writeCollection(w, r, c)
}

func (s *Server) writeCollection(w http.ResponseWriter, r *http.Request, c library.Collection) {
	mc := MediaContainer{Metadata: []Metadata{s.collectionMetadata(c)}}
	s.setSection(&mc, c.SectionID)
	writeContainer(w, r, mc)
}

func (s *Server) handleCollectionChildren(w http.ResponseWriter, r *http.Request) {
	c, ok := s.collection(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	mc := MediaContainer{Title2: c.Title}
	s.setSection(&mc, c.SectionID)
	mc.Title1 = mc.LibrarySectionTitle
	kids := s.collectionChildren(c)
	if len(kids) > 0 {
		mc.ViewGroup = kids[0].Type
	}
	s.page(r, &mc, kids, -1)
	writeContainer(w, r, mc)
}

// itemIDs reads the items of a collection or playlist change: ids, or the
// IDs after the last /library/metadata/ in uri.
func itemIDs(r *http.Request) ([]int, error) {
	q := r.URL.Query()
	list := q.Get("ids")
	if u := q.Get("uri"); u != "" {
		i := strings.LastIndex(u, "/library/metadata/")
		if i < 0 {
			return nil, errors.New("uri must end in /library/metadata/<ids>")
		}
		list = u[i+len("/library/metadata/"):]
	}
	var out []int
	for _, v := range strings.Split(list, ",") {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		id, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("bad item ID %q", v)
		}
		out = append(out, id)
	}
	return out, nil
}

// collectionTitle reads and checks the title parameter.
func collectionTitle(r *http.Request) (string, error) {
	t := strings.TrimSpace(r.URL.Query().Get("title"))
	if t == "" || len([]rune(t)) > 200 {
		return "", errors.New("title must be 1 to 200 characters")
	}
	return t, nil
}

func (s *Server) collectionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, library.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, library.ErrReadOnly), errors.Is(err, library.ErrBadMember):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		s.log.Error("collection", "err", err)
		http.Error(w, "could not save the collection", http.StatusInternalServerError)
	}
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	secID, err := strconv.Atoi(r.URL.Query().Get("sectionId"))
	if err != nil {
		http.Error(w, "sectionId is required", http.StatusBadRequest)
		return
	}
	if _, err := s.store.Section(secID); err != nil || !s.visibility(r)(secID) {
		http.NotFound(w, r)
		return
	}
	title, err := collectionTitle(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids, err := itemIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, err := s.store.CreateCollection(secID, title, ids, time.Now())
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	s.writeCollection(w, r, c)
}

// editCollection runs a change on a visible collection and writes it.
func (s *Server) editCollection(w http.ResponseWriter, r *http.Request, f func(id int) (library.Collection, error)) {
	c, ok := s.collection(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	c, err := f(c.ID)
	if err != nil {
		s.collectionError(w, r, err)
		return
	}
	s.writeCollection(w, r, c)
}

func (s *Server) handleAddToCollection(w http.ResponseWriter, r *http.Request) {
	ids, err := itemIDs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.editCollection(w, r, func(id int) (library.Collection, error) { return s.store.AddToCollection(id, ids, time.Now()) })
}

func (s *Server) handleRemoveFromCollection(w http.ResponseWriter, r *http.Request) {
	item, err := strconv.Atoi(r.PathValue("item"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.editCollection(w, r, func(id int) (library.Collection, error) {
		return s.store.RemoveFromCollection(id, item, time.Now())
	})
}

func (s *Server) handleRenameCollection(w http.ResponseWriter, r *http.Request) {
	title, err := collectionTitle(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.editCollection(w, r, func(id int) (library.Collection, error) { return s.store.RenameCollection(id, title, time.Now()) })
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.collection(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.deleteCollection(w, r, c)
}

// handleDeleteSectionCollection is the documented section-scoped form of
// delete; see docs/spec/wire-conformance.md. A collection of another
// section is 404, so naming the wrong section deletes nothing.
func (s *Server) handleDeleteSectionCollection(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.section(w, r)
	if !ok {
		return
	}
	id, err := strconv.Atoi(r.PathValue("collection"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c, err := s.store.Collection(id)
	if err != nil || c.SectionID != sec.ID {
		http.NotFound(w, r)
		return
	}
	s.deleteCollection(w, r, c)
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request, c library.Collection) {
	if err := s.store.DeleteCollection(c.ID); err != nil {
		s.collectionError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
