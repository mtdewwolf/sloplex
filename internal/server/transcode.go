package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

// Transcoding endpoints; see docs/spec/transcode.md.

const transcodeBase = "/video/:/transcode/universal"

func (s *Server) transcodeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+transcodeBase+"/start.m3u8", s.handleTranscodeHLS)
	mux.HandleFunc("GET "+transcodeBase+"/start.mp4", s.handleTranscodeMP4)
	mux.HandleFunc("GET "+transcodeBase+"/session/{sid}/base/{file}", s.handleTranscodeFile)
	for _, m := range []string{"GET ", "PUT "} {
		mux.HandleFunc(m+transcodeBase+"/stop", s.handleTranscodeStop)
		mux.HandleFunc(m+transcodeBase+"/ping", s.handleTranscodePing)
	}
	mux.HandleFunc("GET /transcode/sessions", s.requireAdmin(s.handleTranscodeSessions))
}

// transcodeTarget resolves path, mediaIndex and partIndex into a request.
func (s *Server) transcodeTarget(w http.ResponseWriter, r *http.Request) (library.Item, transcode.Request, bool) {
	q := r.URL.Query()
	id, err := strconv.Atoi(strings.TrimPrefix(q.Get("path"), "/library/metadata/"))
	if err != nil {
		http.Error(w, "path must be /library/metadata/{id}", http.StatusBadRequest)
		return library.Item{}, transcode.Request{}, false
	}
	it, err := s.store.Item(id)
	if err != nil || !s.visibility(r)(it.SectionID) {
		http.NotFound(w, r)
		return library.Item{}, transcode.Request{}, false
	}
	if !library.IsLeaf(it.Type) {
		http.Error(w, "only movies and episodes can be transcoded", http.StatusBadRequest)
		return library.Item{}, transcode.Request{}, false
	}
	mi, _ := strconv.Atoi(q.Get("mediaIndex"))
	pi, _ := strconv.Atoi(q.Get("partIndex"))
	if mi < 0 || mi >= len(it.Media) || pi < 0 || pi >= len(it.Media[mi].Parts) {
		http.NotFound(w, r)
		return library.Item{}, transcode.Request{}, false
	}
	media := it.Media[mi]
	req := transcode.Request{Input: media.Parts[pi].File, Source: media.Info, NoDirectStream: q.Get("directStream") == "0"}
	if v := q.Get("offset"); v != "" {
		off, err := strconv.ParseFloat(v, 64)
		if err != nil || off < 0 {
			http.Error(w, "offset must be a non-negative number of seconds", http.StatusBadRequest)
			return library.Item{}, transcode.Request{}, false
		}
		req.Offset = off
	}
	if v := q.Get("videoResolution"); v != "" {
		ws, hs, ok := strings.Cut(strings.ToLower(v), "x")
		w1, err1 := strconv.Atoi(ws)
		h1, err2 := strconv.Atoi(hs)
		if !ok || err1 != nil || err2 != nil || w1 < 16 || h1 < 16 {
			http.Error(w, "videoResolution must be WIDTHxHEIGHT", http.StatusBadRequest)
			return library.Item{}, transcode.Request{}, false
		}
		req.MaxWidth, req.MaxHeight = w1, h1
	}
	if v := q.Get("maxVideoBitrate"); v != "" {
		br, err := strconv.Atoi(v)
		if err != nil || br < 64 {
			http.Error(w, "maxVideoBitrate must be at least 64 (kbps)", http.StatusBadRequest)
			return library.Item{}, transcode.Request{}, false
		}
		req.MaxBitrate = br
	}
	return it, req, true
}

// transcodeError writes the response for a failed start.
func transcodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, transcode.ErrBusy):
		w.Header().Set("Retry-After", "10")
		http.Error(w, "too many transcodes in progress", http.StatusServiceUnavailable)
	case errors.Is(err, transcode.ErrUnavailable):
		http.Error(w, "transcoding is not available (ffmpeg not found)", http.StatusServiceUnavailable)
	default:
		http.Error(w, "transcode failed", http.StatusInternalServerError)
	}
}

func (s *Server) handleTranscodeHLS(w http.ResponseWriter, r *http.Request) {
	it, req, ok := s.transcodeTarget(w, r)
	if !ok {
		return
	}
	sess, err := s.tc.StartHLS(s.account(r), r.URL.Query().Get("session"), it.ID, req)
	if err != nil {
		if !errors.Is(err, transcode.ErrBusy) && !errors.Is(err, transcode.ErrUnavailable) {
			s.log.Error("start transcode", "item", it.ID, "err", err)
		}
		transcodeError(w, err)
		return
	}
	p := sess.Plan
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:BANDWIDTH=%d", p.Bandwidth)
	if p.Width > 0 && p.Height > 0 {
		fmt.Fprintf(w, ",RESOLUTION=%dx%d", p.Width, p.Height)
	}
	fmt.Fprintf(w, "\nsession/%s/base/index.m3u8\n", sess.ID)
}

func (s *Server) handleTranscodeFile(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.tc.Session(r.PathValue("sid"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	var path, ctype string
	var err error
	if file := r.PathValue("file"); file == "index.m3u8" {
		path, err = s.tc.Playlist(r.Context(), sess)
		ctype = "application/vnd.apple.mpegurl"
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		path, err = s.tc.Segment(r.Context(), sess, file)
		ctype = "video/mp2t"
	}
	switch {
	case errors.Is(err, transcode.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		http.Error(w, "transcode failed", http.StatusInternalServerError)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

func (s *Server) handleTranscodeMP4(w http.ResponseWriter, r *http.Request) {
	it, req, ok := s.transcodeTarget(w, r)
	if !ok {
		return
	}
	started := false
	err := s.tc.Stream(r.Context(), it.ID, req, w, func(transcode.Plan) {
		started = true
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
	})
	switch {
	case err == nil:
	case !started:
		transcodeError(w, err)
	default:
		s.log.Warn("transcode stream ended with error", "item", it.ID, "err", err)
	}
}

func (s *Server) handleTranscodeStop(w http.ResponseWriter, r *http.Request) {
	s.tc.Stop(s.account(r), r.URL.Query().Get("session"))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTranscodePing(w http.ResponseWriter, r *http.Request) {
	s.tc.Ping(s.account(r), r.URL.Query().Get("session"))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTranscodeSessions(w http.ResponseWriter, r *http.Request) {
	mc := MediaContainer{}
	for _, info := range s.tc.List() {
		v, a := transcode.Decisions(info.Plan)
		mc.TranscodeSession = append(mc.TranscodeSession, TranscodeSession{
			Key: info.ID, RatingKey: strconv.Itoa(info.ItemID), Protocol: info.Protocol,
			VideoDecision: v, AudioDecision: a, Width: info.Plan.Width, Height: info.Plan.Height,
			Complete: Bool(info.Complete),
		})
	}
	writeContainer(w, r, mc)
}
