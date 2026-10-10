package library

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Meta is metadata read from a local .nfo file; see docs/spec/metadata.md.
type Meta struct {
	Title         string   `json:"title,omitempty"`
	OriginalTitle string   `json:"originalTitle,omitempty"`
	SortTitle     string   `json:"sortTitle,omitempty"`
	Year          int      `json:"year,omitempty"`
	Date          string   `json:"date,omitempty"` // YYYY-MM-DD
	Summary       string   `json:"summary,omitempty"`
	Tagline       string   `json:"tagline,omitempty"`
	ContentRating string   `json:"contentRating,omitempty"`
	Rating        float64  `json:"rating,omitempty"`
	Studio        string   `json:"studio,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	Directors     []string `json:"directors,omitempty"`
	Writers       []string `json:"writers,omitempty"`
	Countries     []string `json:"countries,omitempty"`
	Roles         []Role   `json:"roles,omitempty"`
	Collections   []string `json:"collections,omitempty"`
}

// Role is an actor and the character they play.
type Role struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
}

// NFO root elements.
const (
	NFOMovie   = "movie"
	NFOShow    = "tvshow"
	NFOEpisode = "episodedetails"
)

// maxListValues caps every list in a Meta.
const maxListValues = 50

// ErrNotNFO is returned for files that are not a .nfo of the expected kind.
var ErrNotNFO = errors.New("not an nfo document of the expected kind")

type nfoRating struct {
	Name    string `xml:"name,attr"`
	Max     string `xml:"max,attr"`
	Default string `xml:"default,attr"`
	Value   string `xml:"value"`
}

type nfoActor struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Order string `xml:"order"`
}

type nfoSet struct {
	Text string `xml:",chardata"`
	Name string `xml:"name"`
}

type nfoDoc struct {
	XMLName       xml.Name
	Title         string      `xml:"title"`
	OriginalTitle string      `xml:"originaltitle"`
	SortTitle     string      `xml:"sorttitle"`
	Year          string      `xml:"year"`
	Premiered     string      `xml:"premiered"`
	Aired         string      `xml:"aired"`
	Plot          string      `xml:"plot"`
	Outline       string      `xml:"outline"`
	Tagline       string      `xml:"tagline"`
	MPAA          string      `xml:"mpaa"`
	Rating        string      `xml:"rating"`
	Ratings       []nfoRating `xml:"ratings>rating"`
	Studio        []string    `xml:"studio"`
	Genre         []string    `xml:"genre"`
	Director      []string    `xml:"director"`
	Credits       []string    `xml:"credits"`
	Country       []string    `xml:"country"`
	Actor         []nfoActor  `xml:"actor"`
	Set           []nfoSet    `xml:"set"`
}

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// dropEchoedTitle clears a title that only repeats name, which tools write
// for videos they could not identify; the year and date go too when the name
// has a year of its own. See docs/spec/metadata.md.
func (m *Meta) dropEchoedTitle(name string, nameYear int) {
	if m == nil || m.Title == "" || normalize(m.Title) != normalize(name) {
		return
	}
	m.Title = ""
	if nameYear != 0 {
		m.Year, m.Date = 0, ""
	}
}

// ReadNFO parses a .nfo file whose root must be kind.
func ReadNFO(path, kind string) (*Meta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseNFO(b, kind)
}

// ParseNFO parses .nfo content whose root must be kind.
func ParseNFO(b []byte, kind string) (*Meta, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.CharsetReader = charsetReader
	var doc nfoDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotNFO, err)
	}
	if doc.XMLName.Local != kind {
		return nil, fmt.Errorf("%w: root <%s>, want <%s>", ErrNotNFO, doc.XMLName.Local, kind)
	}
	t := strings.TrimSpace
	m := &Meta{
		Title:         t(doc.Title),
		SortTitle:     t(doc.SortTitle),
		Summary:       t(firstNonEmpty(doc.Plot, doc.Outline)),
		ContentRating: strings.TrimSpace(strings.TrimPrefix(t(doc.MPAA), "Rated ")),
		Rating:        rating(doc),
		Genres:        list(splitGenres(doc.Genre)),
		Directors:     list(doc.Director),
		Writers:       list(doc.Credits),
		Countries:     list(doc.Country),
	}
	if ot := t(doc.OriginalTitle); ot != m.Title {
		m.OriginalTitle = ot
	}
	if studios := list(doc.Studio); len(studios) > 0 {
		m.Studio = studios[0]
	}
	date := t(doc.Premiered)
	if kind == NFOEpisode {
		date = firstNonEmpty(t(doc.Aired), date)
	}
	if _, err := time.Parse(time.DateOnly, date); err == nil && dateRE.MatchString(date) {
		m.Date = date
	}
	if kind != NFOEpisode {
		if y, err := strconv.Atoi(t(doc.Year)); err == nil && y >= 1800 && y <= 2200 {
			m.Year = y
		} else if m.Date != "" {
			m.Year, _ = strconv.Atoi(m.Date[:4])
		}
	}
	if kind == NFOMovie {
		m.Tagline = t(doc.Tagline)
		var sets []string
		for _, s := range doc.Set {
			sets = append(sets, firstNonEmpty(t(s.Name), t(s.Text)))
		}
		m.Collections = list(sets)
	}
	m.Roles = roles(doc.Actor)
	return m, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// list trims, drops empty values and case-insensitive duplicates, and caps
// the length.
func list(vals []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		k := strings.ToLower(v)
		if v == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
		if len(out) == maxListValues {
			break
		}
	}
	return out
}

func splitGenres(vals []string) []string {
	var out []string
	for _, v := range vals {
		out = append(out, strings.Split(v, " / ")...)
	}
	return out
}

func roles(actors []nfoActor) []Role {
	type ordered struct {
		Role
		order, pos int
	}
	var all []ordered
	seen := map[string]bool{}
	for i, a := range actors {
		name := strings.TrimSpace(a.Name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		order, err := strconv.Atoi(strings.TrimSpace(a.Order))
		if err != nil {
			order = math.MaxInt
		}
		all = append(all, ordered{Role{Name: name, Role: strings.TrimSpace(a.Role)}, order, i})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].order < all[j].order })
	var out []Role
	for i, a := range all {
		if i == maxListValues {
			break
		}
		out = append(out, a.Role)
	}
	return out
}

// rating returns the item's rating on a 0-10 scale, or 0.
func rating(doc nfoDoc) float64 {
	scale := func(value, max string) float64 {
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0
		}
		m := 10.0
		if max = strings.TrimSpace(max); max != "" {
			if m, err = strconv.ParseFloat(max, 64); err != nil || m <= 0 {
				return 0
			}
		}
		if v < 0 || v > m || math.IsNaN(v) {
			return 0
		}
		return math.Round(v/m*100) / 10
	}
	if len(doc.Ratings) > 0 {
		pick := doc.Ratings[0]
		for _, r := range doc.Ratings {
			if strings.EqualFold(strings.TrimSpace(r.Default), "true") {
				pick = r
				break
			}
		}
		return scale(pick.Value, pick.Max)
	}
	return scale(doc.Rating, "")
}

// charsetReader accepts the encodings .nfo files declare in practice.
func charsetReader(label string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return r, nil
	case "iso-8859-1", "latin1", "latin-1":
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(b))
		for _, c := range b {
			out = utf8.AppendRune(out, rune(c))
		}
		return bytes.NewReader(out), nil
	}
	return nil, fmt.Errorf("unsupported encoding %q", label)
}

// nfoPath returns the first existing file among names (compared without
// regard to case) in dir.
func nfoPath(dirs dirCache, dir string, names ...string) string {
	files := dirs.files(dir)
	for _, n := range names {
		if real, ok := files[strings.ToLower(n)]; ok {
			return filepath.Join(dir, real)
		}
	}
	return ""
}
