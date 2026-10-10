package server

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mtdewwolf/sloplex/internal/lang"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/subtitles"
	"github.com/mtdewwolf/sloplex/internal/transcode"
)

// Transcoding endpoints; see docs/spec/transcode.md.

const transcodeBase = "/video/:/transcode/universal"

func (s *Server) transcodeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+transcodeBase+"/start.m3u8", s.handleTranscodeHLS)
	mux.HandleFunc("GET "+transcodeBase+"/start.mp4", s.handleTranscodeMP4)
	mux.HandleFunc("GET "+transcodeBase+"/start.webm", s.handleTranscodeWebM)
	mux.HandleFunc("GET "+transcodeBase+"/session/{sid}/base/{file}", s.handleTranscodeFile)
	mux.HandleFunc("GET "+transcodeBase+"/session/{sid}/subs/{stream}/{file}", s.handleTranscodeSubs)
	for _, m := range []string{"GET ", "PUT "} {
		mux.HandleFunc(m+transcodeBase+"/stop", s.handleTranscodeStop)
		mux.HandleFunc(m+transcodeBase+"/ping", s.handleTranscodePing)
	}
	mux.HandleFunc("GET /transcode/sessions", s.requireAdmin(s.handleTranscodeSessions))
	mux.HandleFunc("GET /sloplex/transcoder", s.requireAdmin(s.handleTranscoderInfo))
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
	if !s.applyStreams(w, r, media.Parts[pi], &req) {
		return library.Item{}, transcode.Request{}, false
	}
	return it, req, true
}

// applyStreams sets the audio stream and any subtitle burn-in of a
// transcode from the request parameters and the account's selection; see
// "Transcoding" in docs/spec/streams.md.
func (s *Server) applyStreams(w http.ResponseWriter, r *http.Request, part library.Part, req *transcode.Request) bool {
	q := r.URL.Query()
	audio, sub := s.selected(s.account(r), part)
	if v := q.Get("audioStreamID"); v != "" {
		id, err := strconv.Atoi(v)
		st, ok := findStream(part, id, probe.StreamAudio)
		if err != nil || !ok {
			http.Error(w, "audioStreamID is not an audio stream of this part", http.StatusBadRequest)
			return false
		}
		audio = st
	}
	if v := q.Get("subtitleStreamID"); v != "" {
		id, err := strconv.Atoi(v)
		if err != nil {
			http.Error(w, "invalid subtitleStreamID", http.StatusBadRequest)
			return false
		}
		sub = nil
		if id != 0 {
			st, ok := findStream(part, id, probe.StreamSubtitle)
			if !ok {
				http.Error(w, "subtitleStreamID is not a subtitle stream of this part", http.StatusBadRequest)
				return false
			}
			sub = st
		}
	}
	mode := q.Get("subtitles")
	switch mode {
	case "":
		mode = "auto"
	case "auto", "burn", "none":
	default:
		http.Error(w, "subtitles must be auto, burn or none", http.StatusBadRequest)
		return false
	}

	req.PartID = part.ID
	if audio != nil && audio.Index >= 0 {
		req.SelectAudio, req.AudioIndex = true, audio.Index
		req.Source.AudioCodec, req.Source.AudioChannels = audio.Codec, audio.Channels
	}
	if sub == nil || mode == "none" {
		return true
	}
	text := isTextSub(*sub)
	switch {
	case !text:
		req.Burn = &transcode.Burn{Index: sub.Index}
	case mode == "burn":
		file, _, err := s.subtitleSource(r, *sub, part)
		if err != nil {
			s.log.Warn("subtitle for burn-in", "stream", sub.ID, "err", err)
			http.Error(w, "could not prepare the subtitle for burn-in", http.StatusInternalServerError)
			return false
		}
		req.Burn = &transcode.Burn{File: file}
	}
	return true
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
	var renditions []string
	if req.Burn == nil && r.URL.Query().Get("subtitles") != "none" {
		renditions = s.subtitleRenditions(r, sess.ID, req.PartID)
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, line := range renditions {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "#EXT-X-STREAM-INF:BANDWIDTH=%d", p.Bandwidth)
	if p.Width > 0 && p.Height > 0 {
		fmt.Fprintf(w, ",RESOLUTION=%dx%d", p.Width, p.Height)
	}
	if len(renditions) > 0 {
		fmt.Fprint(w, `,SUBTITLES="subs"`)
	}
	fmt.Fprintf(w, "\nsession/%s/base/index.m3u8\n", sess.ID)
}

// subtitleRenditions lists a part's text subtitles as HLS WebVTT
// renditions; see "Subtitles inside HLS" in docs/spec/streams.md.
func (s *Server) subtitleRenditions(r *http.Request, sid string, partID int) []string {
	part, _, err := s.store.Part(partID)
	if err != nil {
		return nil
	}
	chosen := s.subtitleChoice(r, part)
	names := map[string]int{}
	var out []string
	for _, st := range part.Streams {
		if st.Type != probe.StreamSubtitle || !isTextSub(st) {
			continue
		}
		name, _ := displayTitles(st)
		name = strings.ReplaceAll(name, `"`, "'")
		if names[name]++; names[name] > 1 {
			name = fmt.Sprintf("%s (%d)", name, names[name])
		}
		line := fmt.Sprintf(`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="%s"`, name)
		if code, _ := lang.Normalize(st.Language); code != "" {
			line += fmt.Sprintf(`,LANGUAGE="%s"`, lang.Tag(code))
		}
		yesNo := func(b bool) string {
			if b {
				return "YES"
			}
			return "NO"
		}
		line += fmt.Sprintf(`,DEFAULT=%s,AUTOSELECT=YES,FORCED=%s,URI="session/%s/subs/%d/index.m3u8"`,
			yesNo(chosen != nil && chosen.ID == st.ID), yesNo(st.Forced), sid, st.ID)
		out = append(out, line)
	}
	return out
}

// subtitleChoice is the subtitle a transcode request selects: the
// subtitleStreamID parameter (already validated), else the selection rules.
func (s *Server) subtitleChoice(r *http.Request, part library.Part) *library.Stream {
	if v := r.URL.Query().Get("subtitleStreamID"); v != "" {
		id, _ := strconv.Atoi(v)
		st, _ := findStream(part, id, probe.StreamSubtitle)
		return st
	}
	_, sub := s.selected(s.account(r), part)
	return sub
}

// handleTranscodeSubs serves a session's subtitle rendition playlist and
// WebVTT document.
func (s *Server) handleTranscodeSubs(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.tc.Session(r.PathValue("sid"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.Atoi(r.PathValue("stream"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st, part, _, err := s.store.Stream(id)
	if err != nil || part.ID != sess.Request.PartID || st.Type != probe.StreamSubtitle || !isTextSub(st) {
		http.NotFound(w, r)
		return
	}
	offset := sess.Request.Offset
	switch r.PathValue("file") {
	case "index.m3u8":
		dur := 1.0
		if it, err := s.store.Item(sess.ItemID); err == nil && len(it.Media) > 0 {
			dur = max(1, math.Ceil(float64(it.Media[0].Duration)/1000-offset))
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n"+
			"#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:%d.000,\nsubs.vtt\n#EXT-X-ENDLIST\n", int(dur), int(dur))
	case "subs.vtt":
		seg, err := s.tc.Segment(r.Context(), sess, "00000.ts")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(seg)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		pts, err := transcode.FirstPTS(f)
		f.Close()
		if err != nil {
			s.log.Warn("subtitle timestamp map", "session", sess.ID, "err", err)
			http.Error(w, "could not read the first segment", http.StatusInternalServerError)
			return
		}
		src, srcFormat, err := s.subtitleSource(r, st, part)
		if err == nil {
			src, err = s.subs.Convert(r.Context(), src, srcFormat, subtitles.VTT)
		}
		var vtt []byte
		if err == nil {
			vtt, err = os.ReadFile(src)
		}
		if err != nil {
			s.log.Warn("subtitle rendition", "stream", id, "err", err)
			http.Error(w, "could not read subtitle", http.StatusInternalServerError)
			return
		}
		vtt = subtitles.WithTimestampMap(subtitles.ShiftVTT(vtt, time.Duration(offset*float64(time.Second))), pts)
		w.Header().Set("Content-Type", subtitles.ContentType(subtitles.VTT))
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(vtt)
	default:
		http.NotFound(w, r)
	}
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
	s.transcodeStream(w, r, false)
}

func (s *Server) handleTranscodeWebM(w http.ResponseWriter, r *http.Request) {
	s.transcodeStream(w, r, true)
}

// transcodeStream serves a progressive transcode: fragmented MP4, or WebM.
func (s *Server) transcodeStream(w http.ResponseWriter, r *http.Request, webm bool) {
	it, req, ok := s.transcodeTarget(w, r)
	if !ok {
		return
	}
	req.WebM = webm
	ctype := "video/mp4"
	if webm {
		ctype = "video/webm"
	}
	started := false
	err := s.tc.Stream(r.Context(), it.ID, req, w, func(transcode.Plan) {
		started = true
		w.Header().Set("Content-Type", ctype)
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
			Complete:                Bool(info.Complete),
			TranscodeHwEncoding:     info.Pipeline.Accel,
			TranscodeHwFullPipeline: Bool(info.Pipeline.Accel != "" && info.Pipeline.Full),
		})
	}
	writeContainer(w, r, mc)
}

func (s *Server) handleTranscoderInfo(w http.ResponseWriter, r *http.Request) {
	hw := s.tc.HW()
	accel := hw.Accel
	if accel == "" {
		accel = "none"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available":     s.tc.Available(),
		"hwaccel":       accel,
		"encoder":       transcode.Encoder(hw.Accel),
		"device":        hw.Device,
		"requested":     s.tc.Requested(),
		"maxTranscodes": s.tc.MaxTranscodes(),
	})
}
