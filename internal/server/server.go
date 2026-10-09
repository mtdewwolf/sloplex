// Package server implements the Sloplex HTTP API described in
// docs/spec/http-api.md.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
)

// Options configures a Server.
type Options struct {
	FriendlyName    string
	MachineID       string
	Version         string
	Tokens          []string
	AllowedNetworks []netip.Prefix
}

// Server serves the HTTP API.
type Server struct {
	opts    Options
	store   *library.Store
	scanner *library.Scanner
	log     *slog.Logger
	auth    *authenticator
	// baseCtx is the parent of background work started by requests, such as
	// scans, which must outlive the request.
	baseCtx context.Context
	started time.Time
}

// New returns a Server. Background scans started by requests use ctx.
func New(ctx context.Context, opts Options, store *library.Store, scanner *library.Scanner, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{
		opts: opts, store: store, scanner: scanner, log: log,
		auth:    newAuthenticator(opts.Tokens, opts.AllowedNetworks),
		baseCtx: ctx, started: time.Now(),
	}
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /identity", s.handleIdentity)
	mux.HandleFunc("GET /library", s.handleLibrary)
	mux.HandleFunc("GET /library/sections", s.handleSections)
	mux.HandleFunc("GET /library/sections/{id}", s.handleSection)
	mux.HandleFunc("GET /library/sections/{id}/all", s.handleSectionAll)
	mux.HandleFunc("GET /library/sections/{id}/recentlyAdded", s.handleRecentlyAdded)
	mux.HandleFunc("GET /library/sections/{id}/refresh", s.handleRefresh)
	mux.HandleFunc("PUT /library/sections/{id}/refresh", s.handleRefresh)
	mux.HandleFunc("GET /library/recentlyAdded", s.handleRecentlyAdded)
	mux.HandleFunc("GET /library/metadata/{id}", s.handleMetadata)
	mux.HandleFunc("GET /library/parts/{id}/{ts}/{file}", s.handlePart)
	return logRequests(s.log, cors(s.auth.wrap(mux)))
}

func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request) {
	writeContainer(w, r, MediaContainer{MachineIdentifier: s.opts.MachineID, Version: s.opts.Version})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeContainer(w, r, MediaContainer{
		FriendlyName:      s.opts.FriendlyName,
		MachineIdentifier: s.opts.MachineID,
		Version:           s.opts.Version,
		Platform:          runtime.GOOS,
		PlatformVersion:   runtime.GOARCH,
		UpdatedAt:         s.started.Unix(),
		Directory:         []Directory{{Key: "library", Title: "library"}},
	})
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	writeContainer(w, r, MediaContainer{
		Title1: "Library",
		Directory: []Directory{
			{Key: "sections", Title: "Library Sections"},
			{Key: "recentlyAdded", Title: "Recently Added Content"},
		},
	})
}

func (s *Server) handleSections(w http.ResponseWriter, r *http.Request) {
	mc := MediaContainer{Title1: "Library Sections"}
	for _, sec := range s.store.Sections() {
		mc.Directory = append(mc.Directory, s.sectionDirectory(sec))
	}
	writeContainer(w, r, mc)
}

func (s *Server) sectionDirectory(sec library.Section) Directory {
	refreshing := Bool(s.scanner.Refreshing(sec.ID))
	d := Directory{
		Key:        strconv.Itoa(sec.ID),
		Title:      sec.Title,
		Type:       sec.Type,
		Agent:      "sloplex.local",
		Scanner:    "Sloplex Movie Scanner",
		Language:   "en",
		UUID:       sec.UUID,
		Refreshing: &refreshing,
		CreatedAt:  sec.CreatedAt,
		UpdatedAt:  sec.UpdatedAt,
		ScannedAt:  sec.ScannedAt,
	}
	for i, p := range sec.Paths {
		d.Location = append(d.Location, Location{ID: i + 1, Path: p})
	}
	return d
}

// section resolves the {id} path value, writing a 404 if it is unknown.
func (s *Server) section(w http.ResponseWriter, r *http.Request) (library.Section, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err == nil {
		if sec, err := s.store.Section(id); err == nil {
			return sec, true
		}
	}
	http.NotFound(w, r)
	return library.Section{}, false
}

func (s *Server) handleSection(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.section(w, r)
	if !ok {
		return
	}
	writeContainer(w, r, MediaContainer{
		Title1:              sec.Title,
		LibrarySectionID:    sec.ID,
		LibrarySectionTitle: sec.Title,
		ViewGroup:           "secondary",
		Directory: []Directory{
			{Key: "all", Title: "All " + sec.Title},
			{Key: "recentlyAdded", Title: "Recently Added"},
		},
	})
}

func (s *Server) handleSectionAll(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.section(w, r)
	if !ok {
		return
	}
	items := s.store.Items(sec.ID)
	sortItems(items, r.URL.Query().Get("sort"))
	mc := MediaContainer{
		Title1:              sec.Title,
		LibrarySectionID:    sec.ID,
		LibrarySectionTitle: sec.Title,
		ViewGroup:           sec.Type,
	}
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
	items := s.store.Items(sectionID)
	sortItems(items, "addedAt:desc")
	s.page(r, &mc, items, 50)
	writeContainer(w, r, mc)
}

// page applies X-Plex-Container-Start/Size to items and stores the result in
// mc. defaultSize < 0 means no limit.
func (s *Server) page(r *http.Request, mc *MediaContainer, items []library.Item, defaultSize int) {
	start := pagingParam(r, "X-Plex-Container-Start", 0)
	size := pagingParam(r, "X-Plex-Container-Size", defaultSize)
	total := len(items)
	paged := r.URL.Query().Has("X-Plex-Container-Start") || r.Header.Get("X-Plex-Container-Start") != "" ||
		size >= 0 && size < total
	start = min(start, total)
	end := total
	if size >= 0 {
		end = min(start+size, total)
	}
	for _, it := range items[start:end] {
		mc.Metadata = append(mc.Metadata, s.metadata(it))
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

func sortItems(items []library.Item, spec string) {
	field, dir, _ := strings.Cut(spec, ":")
	desc := dir == "desc"
	key := func(a, b library.Item) int {
		switch field {
		case "title":
			return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		case "year":
			return a.Year - b.Year
		case "addedAt":
			return cmpInt64(a.AddedAt, b.AddedAt)
		case "updatedAt":
			return cmpInt64(a.UpdatedAt, b.UpdatedAt)
		default:
			return strings.Compare(library.SortTitle(a.Title), library.SortTitle(b.Title))
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		c := key(items[i], items[j])
		if c == 0 {
			// Deterministic tie-break, independent of direction.
			return items[i].ID < items[j].ID
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.section(w, r)
	if !ok {
		return
	}
	s.scanner.Start(s.baseCtx, sec.ID)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	it, err := s.store.Item(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mc := MediaContainer{Metadata: []Metadata{s.metadata(it)}}
	if sec, err := s.store.Section(it.SectionID); err == nil {
		mc.LibrarySectionID, mc.LibrarySectionTitle = sec.ID, sec.Title
	}
	writeContainer(w, r, mc)
}

var contentTypes = map[string]string{
	".mkv":  "video/x-matroska",
	".mp4":  "video/mp4",
	".m4v":  "video/x-m4v",
	".avi":  "video/x-msvideo",
	".mov":  "video/quicktime",
	".wmv":  "video/x-ms-wmv",
	".ts":   "video/mp2t",
	".m2ts": "video/mp2t",
	".webm": "video/webm",
	".mpg":  "video/mpeg",
	".mpeg": "video/mpeg",
	".flv":  "video/x-flv",
}

func (s *Server) handlePart(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	part, err := s.store.Part(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(part.File)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("open part", "part", id, "err", err)
		}
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	name := filepath.Base(part.File)
	if ct, ok := contentTypes[strings.ToLower(filepath.Ext(name))]; ok {
		w.Header().Set("Content-Type", ct)
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	}
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func (s *Server) metadata(it library.Item) Metadata {
	rk := strconv.Itoa(it.ID)
	md := Metadata{
		XMLName:           xmlName(it.Type),
		RatingKey:         rk,
		Key:               "/library/metadata/" + rk,
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
