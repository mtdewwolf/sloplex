package server

import (
	"cmp"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/mtdewwolf/sloplex/internal/library"
)

// typeNumbers maps numeric item types to type names.
var typeNumbers = map[string]string{
	"1": library.TypeMovie,
	"2": library.TypeShow,
	"3": library.TypeSeason,
	"4": library.TypeEpisode,
}

// sectionTypes lists the item types each section type can hold.
var sectionTypes = map[string][]string{
	library.TypeMovie: {library.TypeMovie},
	library.TypeShow:  {library.TypeShow, library.TypeSeason, library.TypeEpisode},
}

// childTypes is the type of an item's children.
var childTypes = map[string]string{
	library.TypeShow:   library.TypeSeason,
	library.TypeSeason: library.TypeEpisode,
}

func (s *Server) handleSectionAll(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.section(w, r)
	if !ok {
		return
	}
	typ := r.URL.Query().Get("type")
	if name, ok := typeNumbers[typ]; ok {
		typ = name
	}
	if typ == "" {
		typ = sec.Type
	}
	mc := MediaContainer{
		Title1:              sec.Title,
		LibrarySectionID:    sec.ID,
		LibrarySectionTitle: sec.Title,
		ViewGroup:           typ,
	}
	var items []library.Item
	if slices.Contains(sectionTypes[sec.Type], typ) {
		items = s.store.Items(sec.ID, typ)
	}
	if r.URL.Query().Get("unwatched") == "1" {
		items = s.unwatched(s.account(r), items)
	}
	def := "titleSort"
	if typ == library.TypeSeason || typ == library.TypeEpisode {
		def = "index"
	}
	s.sortItems(items, r.URL.Query().Get("sort"), def)
	s.page(r, &mc, items, -1)
	writeContainer(w, r, mc)
}

func (s *Server) handleRecentlyAdded(w http.ResponseWriter, r *http.Request) {
	sectionID := 0
	mc := MediaContainer{Title1: "Recently Added"}
	if r.PathValue("id") != "" {
		sec, ok := s.section(w, r)
		if !ok {
			return
		}
		sectionID = sec.ID
		mc.LibrarySectionID, mc.LibrarySectionTitle = sec.ID, sec.Title
	}
	items := s.store.Items(sectionID, library.TypeMovie, library.TypeEpisode)
	s.sortItems(items, "addedAt:desc", "")
	s.page(r, &mc, items, 50)
	writeContainer(w, r, mc)
}

// item resolves the {id} path value, writing a 404 if it is unknown.
func (s *Server) item(w http.ResponseWriter, r *http.Request) (library.Item, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err == nil {
		if it, err := s.store.Item(id); err == nil {
			return it, true
		}
	}
	http.NotFound(w, r)
	return library.Item{}, false
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(w, r)
	if !ok {
		return
	}
	mc := MediaContainer{Metadata: []Metadata{s.metadata(it, s.account(r))}}
	s.setSection(&mc, it.SectionID)
	writeContainer(w, r, mc)
}

func (s *Server) handleChildren(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(w, r)
	if !ok {
		return
	}
	mc := MediaContainer{Title2: it.Title, ViewGroup: childTypes[it.Type]}
	s.setSection(&mc, it.SectionID)
	mc.Title1 = mc.LibrarySectionTitle
	if parent, err := s.store.Item(it.ParentID); err == nil {
		mc.Title1 = parent.Title
	}
	s.page(r, &mc, s.store.Children(it.ID), -1)
	writeContainer(w, r, mc)
}

func (s *Server) handleAllLeaves(w http.ResponseWriter, r *http.Request) {
	it, ok := s.item(w, r)
	if !ok {
		return
	}
	mc := MediaContainer{Title2: it.Title}
	s.setSection(&mc, it.SectionID)
	mc.Title1 = mc.LibrarySectionTitle
	if !library.IsLeaf(it.Type) {
		mc.ViewGroup = library.TypeEpisode
	}
	s.page(r, &mc, s.store.Leaves(it.ID), -1)
	writeContainer(w, r, mc)
}

func (s *Server) setSection(mc *MediaContainer, sectionID int) {
	if sec, err := s.store.Section(sectionID); err == nil {
		mc.LibrarySectionID, mc.LibrarySectionTitle = sec.ID, sec.Title
	}
}

// page applies X-Plex-Container-Start/Size to items and stores the result in
// mc. defaultSize < 0 means no limit.
func (s *Server) page(r *http.Request, mc *MediaContainer, items []library.Item, defaultSize int) {
	start := pagingParam(r, "X-Plex-Container-Start", 0)
	size := pagingParam(r, "X-Plex-Container-Size", defaultSize)
	acct := s.account(r)
	total := len(items)
	paged := r.URL.Query().Has("X-Plex-Container-Start") || r.Header.Get("X-Plex-Container-Start") != "" ||
		size >= 0 && size < total
	start = min(start, total)
	end := total
	if size >= 0 {
		end = min(start+size, total)
	}
	for _, it := range items[start:end] {
		mc.Metadata = append(mc.Metadata, s.metadata(it, acct))
	}
	if paged {
		mc.TotalSize, mc.Offset = &total, &start
	}
}

func pagingParam(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		v = r.Header.Get(name)
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

var sortFields = map[string]bool{
	"titleSort": true, "title": true, "year": true, "addedAt": true, "updatedAt": true, "index": true,
}

// position locates an item in the show/season/episode hierarchy.
type position struct {
	show           string
	showID         int
	season, number int
}

func (p position) compare(q position) int {
	return cmp.Or(
		strings.Compare(p.show, q.show),
		cmp.Compare(p.showID, q.showID),
		cmp.Compare(p.season, q.season),
		cmp.Compare(p.number, q.number),
	)
}

func (s *Server) position(it library.Item) position {
	switch it.Type {
	case library.TypeSeason:
		show, _ := s.store.Item(it.ParentID)
		return position{library.SortTitle(show.Title), show.ID, it.Index, -1}
	case library.TypeEpisode:
		season, _ := s.store.Item(it.ParentID)
		show, _ := s.store.Item(season.ParentID)
		return position{library.SortTitle(show.Title), show.ID, season.Index, it.Index}
	}
	return position{library.SortTitle(it.Title), it.ID, -1, -1}
}

// sortItems orders items by spec ("field[:asc|:desc]"), using def when the
// field is missing or unknown. Ties break on ID.
func (s *Server) sortItems(items []library.Item, spec, def string) {
	field, dir, _ := strings.Cut(spec, ":")
	if !sortFields[field] {
		field = def
	}
	var pos map[int]position
	if field == "index" {
		pos = make(map[int]position, len(items))
		for _, it := range items {
			pos[it.ID] = s.position(it)
		}
	}
	compare := func(a, b library.Item) int {
		switch field {
		case "title":
			return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		case "year":
			return cmp.Compare(a.Year, b.Year)
		case "addedAt":
			return cmp.Compare(a.AddedAt, b.AddedAt)
		case "updatedAt":
			return cmp.Compare(a.UpdatedAt, b.UpdatedAt)
		case "index":
			return pos[a.ID].compare(pos[b.ID])
		default:
			return strings.Compare(library.SortTitle(a.Title), library.SortTitle(b.Title))
		}
	}
	desc := dir == "desc"
	slices.SortStableFunc(items, func(a, b library.Item) int {
		c := compare(a, b)
		if c == 0 {
			// Deterministic tie-break, independent of direction.
			return cmp.Compare(a.ID, b.ID)
		}
		if desc {
			return -c
		}
		return c
	})
}

func metadataKey(id int) string { return "/library/metadata/" + strconv.Itoa(id) }

func (s *Server) metadata(it library.Item, acct int) Metadata {
	rk := strconv.Itoa(it.ID)
	md := Metadata{
		XMLName:           xmlName(it.Type),
		RatingKey:         rk,
		Key:               metadataKey(it.ID),
		GUID:              fmt.Sprintf("sloplex://%s/%s", it.Type, rk),
		Type:              it.Type,
		Title:             it.Title,
		Year:              it.Year,
		AddedAt:           it.AddedAt,
		UpdatedAt:         it.UpdatedAt,
		LibrarySectionID:  it.SectionID,
		LibrarySectionKey: fmt.Sprintf("/library/sections/%d", it.SectionID),
	}
	if st := library.SortTitle(it.Title); st != strings.ToLower(it.Title) {
		md.TitleSort = st
	}
	if sec, err := s.store.Section(it.SectionID); err == nil {
		md.LibrarySectionTitle = sec.Title
	}

	if library.IsLeaf(it.Type) {
		st := s.play.Get(acct, it.ID)
		md.ViewCount, md.ViewOffset, md.LastViewedAt = st.ViewCount, st.ViewOffset, st.LastViewedAt
	} else {
		r := s.rollup(acct, it.ID)
		md.LeafCount, md.ViewedLeafCount, md.LastViewedAt = r.leaves, &r.viewed, r.lastViewedAt
	}

	md.Thumb = s.imageURL(it, library.ImageThumb)
	md.Art = s.imageURL(it, library.ImageArt)
	md.Banner = s.imageURL(it, library.ImageBanner)

	switch it.Type {
	case library.TypeShow:
		md.Key += "/children"
		md.ChildCount, _ = s.store.Counts(it.ID)
	case library.TypeSeason:
		md.Key += "/children"
		md.Index = &it.Index
		if show, err := s.store.Item(it.ParentID); err == nil {
			md.ParentRatingKey, md.ParentKey, md.ParentTitle = strconv.Itoa(show.ID), metadataKey(show.ID), show.Title
			md.ParentThumb = s.imageURL(show, library.ImageThumb)
			md.Thumb = cmp.Or(md.Thumb, md.ParentThumb)
			md.Art = cmp.Or(md.Art, s.imageURL(show, library.ImageArt))
		}
	case library.TypeEpisode:
		md.Index = &it.Index
		if season, err := s.store.Item(it.ParentID); err == nil {
			md.ParentRatingKey, md.ParentKey, md.ParentTitle = strconv.Itoa(season.ID), metadataKey(season.ID), season.Title
			md.ParentIndex = &season.Index
			md.ParentThumb = s.imageURL(season, library.ImageThumb)
			if show, err := s.store.Item(season.ParentID); err == nil {
				md.GrandparentRatingKey, md.GrandparentKey, md.GrandparentTitle = strconv.Itoa(show.ID), metadataKey(show.ID), show.Title
				md.GrandparentThumb = s.imageURL(show, library.ImageThumb)
				md.GrandparentArt = s.imageURL(show, library.ImageArt)
				md.ParentThumb = cmp.Or(md.ParentThumb, md.GrandparentThumb)
				md.Art = cmp.Or(md.Art, md.GrandparentArt)
			}
		}
	}

	for _, m := range it.Media {
		media := Media{
			ID: m.ID, Duration: m.Duration, Bitrate: m.Bitrate, Width: m.Width, Height: m.Height,
			AudioChannels: m.AudioChannels, AudioCodec: m.AudioCodec, VideoCodec: m.VideoCodec,
			VideoResolution: videoResolution(m.Width, m.Height), Container: m.Container,
		}
		for _, p := range m.Parts {
			media.Part = append(media.Part, Part{
				ID:        p.ID,
				Key:       fmt.Sprintf("/library/parts/%d/%d/file%s", p.ID, p.ModTime, strings.ToLower(filepath.Ext(p.File))),
				Duration:  p.Duration,
				File:      p.File,
				Size:      p.Size,
				Container: p.Container,
			})
		}
		md.Media = append(md.Media, media)
	}
	if len(md.Media) > 0 {
		md.Duration = md.Media[0].Duration
	}
	return md
}

func videoResolution(w, h int) string {
	switch {
	case w == 0 && h == 0:
		return ""
	case w >= 3200 || h >= 1800:
		return "4k"
	case w >= 1700 || h >= 1000:
		return "1080"
	case w >= 1100 || h >= 700:
		return "720"
	case h >= 400:
		return "480"
	default:
		return "sd"
	}
}
