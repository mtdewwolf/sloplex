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

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/artwork"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/playlists"
	"github.com/mtdewwolf/sloplex/internal/playstate"
	"github.com/mtdewwolf/sloplex/internal/preferences"
	"github.com/mtdewwolf/sloplex/internal/subtitles"
	"github.com/mtdewwolf/sloplex/internal/transcode"
	"github.com/mtdewwolf/sloplex/internal/truenas"
	"github.com/mtdewwolf/sloplex/internal/updates"
	"github.com/mtdewwolf/sloplex/internal/web"
)

// Options configures a Server.
type Options struct {
	FriendlyName    string
	MachineID       string
	Version         string
	Tokens          []string
	AllowedNetworks []netip.Prefix
	// AutoUpdateCheck says whether the daily update check is on.
	AutoUpdateCheck bool
}

// Deps are the services a Server uses.
type Deps struct {
	Store      *library.Store
	Scanner    *library.Scanner
	Play       *playstate.Store
	Art        *artwork.Service
	Accounts   *accounts.Store
	Transcoder *transcode.Manager
	Subtitles  *subtitles.Service
	// Playlists is optional; without it playlists are kept in memory.
	Playlists *playlists.Store
	// Updates is optional; without it updates are looked up at the official
	// repository on request.
	Updates *updates.Checker
	// TrueNAS is optional; without it TrueNAS settings are kept in memory.
	TrueNAS *truenas.Store
	// Prefs is optional; without it preferences are kept in memory and
	// default to Options.FriendlyName.
	Prefs *preferences.Store
	Log   *slog.Logger // optional
}

// Server serves the HTTP API.
type Server struct {
	opts      Options
	store     *library.Store
	scanner   *library.Scanner
	play      *playstate.Store
	art       *artwork.Service
	accounts  *accounts.Store
	tc        *transcode.Manager
	subs      *subtitles.Service
	playlists *playlists.Store
	queues    *queueStore
	updates   *updates.Checker
	truenas   *truenas.Store
	prefs     *preferences.Store
	tn        trueNAS
	logins    *loginLimiter
	log       *slog.Logger
	auth      *authenticator
	// baseCtx is the parent of background work started by requests, such as
	// scans, which must outlive the request.
	baseCtx context.Context
	started time.Time
}

// New returns a Server. Background scans started by requests use ctx.
func New(ctx context.Context, opts Options, d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Playlists == nil {
		d.Playlists, _ = playlists.Open("") // in memory; cannot fail
	}
	if d.Updates == nil {
		d.Updates = updates.New(opts.Version, "", d.Log)
	}
	if d.TrueNAS == nil {
		d.TrueNAS, _ = truenas.Open("") // in memory; cannot fail
	}
	if d.Prefs == nil {
		d.Prefs, _ = preferences.Open("", preferences.Bootstrap{FriendlyName: opts.FriendlyName}) // in memory; cannot fail
	}
	auth := newAuthenticator(opts.Tokens, opts.AllowedNetworks, d.Accounts)
	return &Server{
		queues:    newQueueStore(),
		playlists: d.Playlists, updates: d.Updates, truenas: d.TrueNAS, prefs: d.Prefs, tn: realTrueNAS{},
		opts: opts, store: d.Store, scanner: d.Scanner, play: d.Play, art: d.Art, log: d.Log,
		accounts: d.Accounts, tc: d.Transcoder, subs: d.Subtitles, logins: newLoginLimiter(),
		auth:    auth,
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
	mux.HandleFunc("GET /library/sections/{id}/refresh", s.requireAdmin(s.handleRefresh))
	mux.HandleFunc("PUT /library/sections/{id}/refresh", s.requireAdmin(s.handleRefresh))
	mux.HandleFunc("POST /library/sections/{id}/refresh", s.requireAdmin(s.handleRefreshPost))
	mux.HandleFunc("PUT /library/sections/{id}/emptyTrash", s.requireAdmin(s.handleEmptyTrash))
	mux.HandleFunc("GET /library/recentlyAdded", s.handleRecentlyAdded)
	mux.HandleFunc("GET /library/metadata/{id}", s.handleMetadata)
	mux.HandleFunc("GET /library/metadata/{id}/children", s.handleChildren)
	mux.HandleFunc("GET /library/metadata/{id}/allLeaves", s.handleAllLeaves)
	mux.HandleFunc("GET /library/parts/{id}/{ts}/{file}", s.handlePart)
	for _, kind := range []string{library.ImageThumb, library.ImageArt, library.ImageBanner} {
		mux.HandleFunc("GET /library/metadata/{id}/"+kind, s.handleImage(kind))
		mux.HandleFunc("GET /library/metadata/{id}/"+kind+"/{ts}", s.handleImage(kind))
	}
	mux.HandleFunc("GET /photo/:/transcode", s.handlePhotoTranscode)
	s.accountRoutes(mux)
	s.transcodeRoutes(mux)
	s.streamRoutes(mux)
	s.collectionRoutes(mux)
	s.playlistRoutes(mux)
	s.playQueueRoutes(mux)
	s.updateRoutes(mux)
	s.preferenceRoutes(mux)
	mux.Handle("GET /web/", web.Handler())
	mux.HandleFunc("GET /web", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/web/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /hubs/search", s.handleHubSearch)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /library/sections/{id}/search", s.handleSearch)
	mux.HandleFunc("GET /library/onDeck", s.handleOnDeck)
	mux.HandleFunc("GET /library/sections/{id}/onDeck", s.handleOnDeck)
	for path, h := range map[string]http.HandlerFunc{
		"/:/scrobble":   s.handleScrobble,
		"/:/unscrobble": s.handleUnscrobble,
		"/:/progress":   s.handleProgress,
		"/:/timeline":   s.handleTimeline,
	} {
		mux.HandleFunc("GET "+path, h)
		mux.HandleFunc("PUT "+path, h)
	}
	mux.HandleFunc("POST /:/timeline", s.handleTimelinePost)
	return logRequests(s.log, cors(browserHome(s.auth.wrap(mux))))
}

func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request) {
	writeContainer(w, r, MediaContainer{MachineIdentifier: s.opts.MachineID, Version: s.opts.Version})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeContainer(w, r, MediaContainer{
		FriendlyName:      s.prefs.FriendlyName(),
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
	see := s.visibility(r)
	for _, sec := range s.store.Sections() {
		if see(sec.ID) {
			mc.Directory = append(mc.Directory, s.sectionDirectory(sec))
		}
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
		if sec, err := s.store.Section(id); err == nil && s.visibility(r)(sec.ID) {
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

// handleRefreshPost is the documented POST form of refresh; see
// docs/spec/wire-conformance.md. Unsupported scan modes fail explicitly
// instead of silently starting a broader or weaker scan than requested.
func (s *Server) handleRefreshPost(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if f := q.Get("force"); f != "" && f != "0" && f != "1" {
		http.Error(w, "force must be 0 or 1", http.StatusBadRequest)
		return
	}
	if q.Get("force") == "1" || q.Get("path") != "" {
		http.Error(w, "forced and partial-path scans are not implemented", http.StatusNotImplemented)
		return
	}
	s.handleRefresh(w, r)
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
	part, sectionID, err := s.store.Part(id)
	if err != nil || !s.visibility(r)(sectionID) || !s.store.PartAvailable(id) {
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
