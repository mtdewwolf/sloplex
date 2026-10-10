package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mtdewwolf/sloplex/internal/lang"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/playstate"
	"github.com/mtdewwolf/sloplex/internal/probe"
	"github.com/mtdewwolf/sloplex/internal/subtitles"
)

// Audio tracks and subtitles; see docs/spec/streams.md.

func (s *Server) streamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /library/parts/{id}", s.handleSelectStreams)
	mux.HandleFunc("GET /library/streams/{id}", s.handleStreamFile)
}

var streamTypes = map[string]int{probe.StreamVideo: 1, probe.StreamAudio: 2, probe.StreamSubtitle: 3}

// isTextSub reports whether a subtitle stream can be served as text.
func isTextSub(st library.Stream) bool { return st.File != "" || subtitles.IsText(st.Codec) }

// selected applies the default-selection rules for an account and part.
func (s *Server) selected(acct int, part library.Part) (audio, sub *library.Stream) {
	sel := s.play.Selection(acct, part.ID)
	var firstAudio, defaultAudio, forcedSub *library.Stream
	for i := range part.Streams {
		st := &part.Streams[i]
		switch st.Type {
		case probe.StreamAudio:
			if st.ID == sel.Audio {
				audio = st
			}
			if firstAudio == nil {
				firstAudio = st
			}
			if st.Default && defaultAudio == nil {
				defaultAudio = st
			}
		case probe.StreamSubtitle:
			if st.ID == sel.Subtitle {
				sub = st
			}
			if st.Forced && forcedSub == nil {
				forcedSub = st
			}
		}
	}
	if audio == nil {
		audio = defaultAudio
		if audio == nil {
			audio = firstAudio
		}
	}
	if sub == nil && sel.Subtitle != playstate.NoSubtitle {
		sub = forcedSub
	}
	return audio, sub
}

var codecNames = map[string]string{
	"subrip": "SRT", "srt": "SRT", "ass": "ASS", "ssa": "SSA", "webvtt": "VTT", "vtt": "VTT", "mov_text": "TX3G",
	"hdmv_pgs_subtitle": "PGS", "dvd_subtitle": "VOBSUB", "dvb_subtitle": "DVB",
	"truehd": "TrueHD", "opus": "Opus", "vorbis": "Vorbis",
}

func codecName(c string) string {
	if n, ok := codecNames[c]; ok {
		return n
	}
	return strings.ToUpper(c)
}

func layoutName(ch int) string {
	switch ch {
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return fmt.Sprintf("%d ch", ch)
}

// displayTitles builds displayTitle and extendedDisplayTitle.
func displayTitles(st library.Stream) (string, string) {
	_, name := lang.Normalize(st.Language)
	if name == "" {
		name = "Unknown"
	}
	var t string
	switch st.Type {
	case probe.StreamVideo:
		res := videoResolution(st.Width, st.Height)
		switch res {
		case "":
			res = "Video"
		case "4k":
			res = "4K"
		case "sd":
			res = "SD"
		default:
			res += "p"
		}
		t = fmt.Sprintf("%s (%s)", res, codecName(st.Codec))
	case probe.StreamAudio:
		t = fmt.Sprintf("%s (%s %s)", name, codecName(st.Codec), layoutName(st.Channels))
	default:
		marker := ""
		if st.Forced {
			marker += " Forced"
		}
		if st.HearingImpaired {
			marker += " SDH"
		}
		t = fmt.Sprintf("%s%s (%s)", name, marker, codecName(st.Codec))
	}
	if st.Title != "" {
		return t, t + " – " + st.Title
	}
	return t, t
}

// streamElems renders a part's streams for an account.
func (s *Server) streamElems(acct int, part library.Part) []StreamElem {
	audio, sub := s.selected(acct, part)
	out := make([]StreamElem, 0, len(part.Streams))
	for _, st := range part.Streams {
		code, name := lang.Normalize(st.Language)
		e := StreamElem{
			ID: st.ID, StreamType: streamTypes[st.Type], Codec: st.Codec,
			Language: name, LanguageCode: code, Title: st.Title,
			Channels: st.Channels, Width: st.Width, Height: st.Height,
			Default: Bool(st.Default), Forced: Bool(st.Forced), HearingImpaired: Bool(st.HearingImpaired),
			Selected: Bool((audio != nil && st.ID == audio.ID) || (sub != nil && st.ID == sub.ID)),
		}
		if st.Type == probe.StreamAudio && st.Channels > 0 {
			e.AudioChannelLayout = st.ChannelLayout
		}
		if st.File == "" {
			idx := st.Index
			e.Index = &idx
		}
		if st.Type == probe.StreamSubtitle && isTextSub(st) {
			e.Key = fmt.Sprintf("/library/streams/%d", st.ID)
		}
		e.DisplayTitle, e.ExtendedDisplayTitle = displayTitles(st)
		out = append(out, e)
	}
	return out
}

// attachStreams adds Stream elements to the parts of a rendered item.
func (s *Server) attachStreams(md *Metadata, it library.Item, acct int) {
	for mi := range md.Media {
		if mi >= len(it.Media) {
			break
		}
		for pi := range md.Media[mi].Part {
			if pi < len(it.Media[mi].Parts) {
				md.Media[mi].Part[pi].Stream = s.streamElems(acct, it.Media[mi].Parts[pi])
			}
		}
	}
}

// findStream returns the stream of a part with the given ID and type.
func findStream(part library.Part, id int, typ string) (*library.Stream, bool) {
	for i := range part.Streams {
		if part.Streams[i].ID == id && part.Streams[i].Type == typ {
			return &part.Streams[i], true
		}
	}
	return nil, false
}

func (s *Server) handleSelectStreams(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	part, sectionID, err := s.store.Part(id)
	if err != nil || !s.visibility(r)(sectionID) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	audio, sub := 0, 0
	if v := q.Get("audioStreamID"); v != "" {
		n, err := strconv.Atoi(v)
		if _, ok := findStream(part, n, probe.StreamAudio); err != nil || !ok {
			http.Error(w, "audioStreamID is not an audio stream of this part", http.StatusBadRequest)
			return
		}
		audio = n
	}
	if v := q.Get("subtitleStreamID"); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil:
			http.Error(w, "invalid subtitleStreamID", http.StatusBadRequest)
			return
		case n == 0:
			sub = playstate.NoSubtitle
		default:
			if _, ok := findStream(part, n, probe.StreamSubtitle); !ok {
				http.Error(w, "subtitleStreamID is not a subtitle stream of this part", http.StatusBadRequest)
				return
			}
			sub = n
		}
	}
	err = s.play.SetSelection(s.account(r), part.ID, func(sel *playstate.Selection) {
		if audio != 0 {
			sel.Audio = audio
		}
		if sub != 0 {
			sel.Subtitle = sub
		}
	})
	if err != nil {
		s.log.Error("save stream selection", "err", err)
		http.Error(w, "could not save selection", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// subtitleSource returns a text subtitle file and its format for a stream,
// extracting embedded streams into the cache.
func (s *Server) subtitleSource(r *http.Request, st library.Stream, part library.Part) (string, string, error) {
	if st.File != "" {
		return st.File, subtitles.NativeFormat(st.Codec), nil
	}
	path, err := s.subs.Extract(r.Context(), part.File, part.ModTime, part.ID, st.Index, st.Codec)
	return path, subtitles.NativeFormat(st.Codec), err
}

func (s *Server) handleStreamFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st, part, sectionID, err := s.store.Stream(id)
	if err != nil || !s.visibility(r)(sectionID) {
		http.NotFound(w, r)
		return
	}
	if st.Type != probe.StreamSubtitle {
		http.Error(w, "not a subtitle stream", http.StatusBadRequest)
		return
	}
	if !isTextSub(st) {
		http.Error(w, "image-based subtitles can only be burned in", http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	format := q.Get("format")
	if format != "" && !subtitles.ValidFormat(format) {
		http.Error(w, "format must be srt, vtt or ass", http.StatusBadRequest)
		return
	}
	var offset time.Duration
	if v := q.Get("offset"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || format != subtitles.VTT {
			http.Error(w, "offset needs format=vtt and a non-negative number of seconds", http.StatusBadRequest)
			return
		}
		offset = time.Duration(f * float64(time.Second))
	}

	src, srcFormat, err := s.subtitleSource(r, st, part)
	if err == nil && format != "" {
		src, err = s.subs.Convert(r.Context(), src, srcFormat, format)
		srcFormat = format
	}
	switch {
	case errors.Is(err, subtitles.ErrUnavailable):
		http.Error(w, "subtitle conversion needs ffmpeg", http.StatusServiceUnavailable)
		return
	case err != nil:
		s.log.Warn("subtitle", "stream", id, "err", err)
		http.Error(w, "could not read subtitle", http.StatusInternalServerError)
		return
	}
	b, err := os.ReadFile(src)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if offset > 0 {
		b = subtitles.ShiftVTT(b, offset)
	}
	ctype := subtitles.ContentType(srcFormat)
	if format == "" && st.File != "" && strings.EqualFold(filepath.Ext(st.File), ".ssa") {
		ctype = subtitles.ContentType(subtitles.ASS)
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
