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
	mux.HandleFunc("GET /library/metadata/{id}/children", s.handleChildren)
	mux.HandleFunc("GET /library/metadata/{id}/allLeaves", s.handleAllLeaves)
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

var scannerNames = map[string]string{
	library.TypeMovie: "Sloplex Movie Scanner",
	library.TypeShow:  "Sloplex TV Scanner",
}

func (s *Server) sectionDirectory(sec library.Section) Directory {
	refreshing := Bool(s.scanner.Refreshing(sec.ID))
	d := Directory{
		Key:        strconv.Itoa(sec.ID),
		Title:      sec.Title,
		Type:       sec.Type,
		Agent:      "sloplex.local",
		Scanner:    scannerNames[sec.Type],
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

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.section(w, r)
	if !ok {
		return
	}
	s.scanner.Start(s.baseCtx, sec.ID)
	w.WriteHeader(http.StatusOK)
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
