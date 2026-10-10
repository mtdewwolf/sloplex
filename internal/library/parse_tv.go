package library

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Episode is what a TV file path says about itself.
type Episode struct {
	Show     string
	ShowYear int
	Season   int
	Episode  int
	Title    string
	// Aired is the air date (YYYY-MM-DD) of a date-based episode.
	Aired string
}

var (
	// seRE matches S01E02, S01 E02, S01E01E02 and S01E01-E02.
	seRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,3})[ .\-]?e(\d{1,4})(?:-?e\d{1,4})*`)
	// xRE matches 1x02.
	xRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[^a-z0-9]|$)`)
	// epOnlyRE matches E02, Ep02 and Episode 2, used inside season folders.
	epOnlyRE = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:e|ep|episode)\s?(\d{1,4})(?:[^a-z0-9]|$)`)
	// leadingNumRE matches a leading episode number such as "02 - Title".
	leadingNumRE = regexp.MustCompile(`^(\d{1,3})(?:[ .\-]|$)`)
	seasonDirRE  = regexp.MustCompile(`(?i)^(?:season|series|s)\s*(\d{1,3})$`)
	// ymdRE and dmyRE match air dates such as 2024-03-15 and 15.03.2024.
	ymdRE = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d\d)[-. _](\d\d)[-. _](\d\d)(?:[^0-9]|$)`)
	dmyRE = regexp.MustCompile(`(?:^|[^0-9])(\d\d)[-. _](\d\d)[-. _]((?:19|20)\d\d)(?:[^0-9]|$)`)
)

// airDate finds an air date in s and returns it with the indexes of the
// whole match (including the delimiting characters).
func airDate(s string) (date time.Time, start, end int, ok bool) {
	for _, c := range []struct {
		re      *regexp.Regexp
		y, m, d int
	}{{ymdRE, 1, 2, 3}, {dmyRE, 3, 2, 1}} {
		m := c.re.FindStringSubmatchIndex(s)
		if m == nil {
			continue
		}
		num := func(g int) int { n, _ := strconv.Atoi(s[m[2*g]:m[2*g+1]]); return n }
		y, mo, d := num(c.y), num(c.m), num(c.d)
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
		if t.Year() != y || int(t.Month()) != mo || t.Day() != d {
			continue // no such day
		}
		// Keep the delimiters out of the match, except leading separators.
		start, end = m[2*min(c.y, c.d)], m[2*max(c.y, c.d)+1]
		return t, start, end, true
	}
	return time.Time{}, 0, 0, false
}

// seasonDir reports whether a folder name denotes a season, and which.
func seasonDir(name string) (int, bool) {
	name = strings.TrimSpace(normalize(name))
	if strings.EqualFold(name, "specials") {
		return 0, true
	}
	if m := seasonDirRE.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	return 0, false
}

// ParseEpisode derives show, season and episode from a file path below
// root. ok is false when no episode number can be found. See the "Show
// sections" rules in docs/spec/http-api.md.
func ParseEpisode(root, path string) (ep Episode, ok bool) {
	var dirs []string
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		if d := filepath.Dir(rel); d != "." {
			dirs = strings.Split(filepath.ToSlash(d), "/")
		}
	}
	base := normalize(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))

	var prefix, rest string
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	if m := seRE.FindStringSubmatchIndex(base); m != nil {
		ep.Season, ep.Episode = atoi(base[m[2]:m[3]]), atoi(base[m[4]:m[5]])
		prefix, rest, ok = base[:m[0]], base[m[1]:], true
	} else if m := xRE.FindStringSubmatchIndex(base); m != nil {
		ep.Season, ep.Episode = atoi(base[m[2]:m[3]]), atoi(base[m[4]:m[5]])
		prefix, rest, ok = base[:m[0]], base[m[1]:], true
	} else if d, start, end, found := airDate(base); found {
		ep.Season, ep.Episode, ep.Aired = d.Year(), int(d.Month())*100+d.Day(), d.Format(time.DateOnly)
		prefix, rest, ok = base[:start], base[end:], true
	} else if len(dirs) > 0 {
		if season, isSeason := seasonDir(dirs[len(dirs)-1]); isSeason {
			if m := epOnlyRE.FindStringSubmatchIndex(base); m != nil {
				ep.Season, ep.Episode = season, atoi(base[m[2]:m[3]])
				prefix, rest, ok = base[:m[0]], base[m[1]:], true
			} else if m := leadingNumRE.FindStringSubmatchIndex(base); m != nil {
				ep.Season, ep.Episode = season, atoi(base[m[2]:m[3]])
				rest, ok = base[m[1]:], true
			}
		}
	}
	if !ok {
		return Episode{}, false
	}

	if len(dirs) > 0 {
		if _, isSeason := seasonDir(dirs[0]); !isSeason {
			ep.Show, ep.ShowYear = parseName(dirs[0])
		}
	}
	if ep.Show == "" {
		ep.Show, ep.ShowYear = parseName(prefix)
	}
	if ep.Show == "" {
		return Episode{}, false
	}
	ep.Title = episodeTitle(rest)
	return ep, true
}

func episodeTitle(s string) string {
	s = strings.TrimLeft(s, " -.")
	if loc := junkRE.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}
	return strings.TrimRight(strings.TrimSpace(s), " -([{")
}

// SeasonTitle is the display title of a season. Seasons of date-based
// shows are numbered by year and titled with it.
func SeasonTitle(index int) string {
	switch {
	case index == 0:
		return "Specials"
	case index >= 1000:
		return strconv.Itoa(index)
	}
	return fmt.Sprintf("Season %d", index)
}
