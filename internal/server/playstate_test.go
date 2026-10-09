package server

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// do sends an authenticated request and returns the status code.
func (f *fixture) do(t *testing.T, method, path string) int {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(tokenHeader, testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// find returns the episode or movie with the given title from type=4/1 lists.
func (f *fixture) find(t *testing.T, path, title string) Metadata {
	t.Helper()
	for _, m := range f.getJSON(t, path).Metadata {
		if m.Title == title {
			return m
		}
	}
	t.Fatalf("%q not found in %s", title, path)
	return Metadata{}
}

func TestScrobbleUnscrobble(t *testing.T) {
	f := newFixture(t, Options{})
	m := f.find(t, "/library/sections/1/all", "Heat")
	if m.ViewCount != 0 || m.LastViewedAt != 0 {
		t.Fatalf("fresh item has play state: %+v", m)
	}
	if code := f.do(t, http.MethodGet, "/:/scrobble?identifier=com.plexapp.plugins.library&key="+m.RatingKey); code != 200 {
		t.Fatalf("scrobble: %d", code)
	}
	got := f.getJSON(t, m.Key).Metadata[0]
	if got.ViewCount != 1 || got.LastViewedAt == 0 {
		t.Errorf("after scrobble: %+v", got)
	}
	if code := f.do(t, http.MethodPut, "/:/unscrobble?key=/library/metadata/"+m.RatingKey); code != 200 {
		t.Fatalf("unscrobble: %d", code)
	}
	if got := f.getJSON(t, m.Key).Metadata[0]; got.ViewCount != 0 || got.LastViewedAt != 0 {
		t.Errorf("after unscrobble: %+v", got)
	}
}

func TestTimelineCompletion(t *testing.T) {
	f := newFixture(t, Options{})
	m := f.find(t, "/library/sections/1/all", "Alien")
	tl := func(ms string) {
		t.Helper()
		if code := f.do(t, http.MethodGet, "/:/timeline?state=playing&ratingKey="+m.RatingKey+"&time="+ms); code != 200 {
			t.Fatalf("timeline time=%s: %d", ms, code)
		}
	}
	state := func() Metadata { return f.getJSON(t, m.Key).Metadata[0] }

	tl("60000")
	if s := state(); s.ViewOffset != 60000 || s.ViewCount != 0 {
		t.Fatalf("in progress: %+v", s)
	}
	tl("6480000") // exactly 90% of 7_200_000
	if s := state(); s.ViewOffset != 0 || s.ViewCount != 1 {
		t.Fatalf("completed: %+v", s)
	}
	tl("7000000") // more updates near the end count once
	if s := state(); s.ViewCount != 1 {
		t.Fatalf("double counted: %+v", s)
	}
	tl("1000") // rewatch
	tl("6500000")
	if s := state(); s.ViewCount != 2 || s.ViewOffset != 0 {
		t.Fatalf("rewatch: %+v", s)
	}
	tl("0") // no-op
	if s := state(); s.ViewCount != 2 {
		t.Fatalf("time=0 changed state: %+v", s)
	}

	// An explicit duration overrides the stored one.
	if code := f.do(t, http.MethodGet, "/:/progress?key="+m.RatingKey+"&time=95&duration=100"); code != 200 {
		t.Fatalf("progress: %d", code)
	}
	if s := state(); s.ViewCount != 2 {
		t.Errorf("progress near end of watched item recounted: %+v", s)
	}
}

func TestPlayStateErrors(t *testing.T) {
	f := newTVFixture(t)
	show := f.getJSON(t, "/library/sections/1/all").Metadata[0]
	for path, want := range map[string]int{
		"/:/scrobble":                                   400,
		"/:/scrobble?key=abc":                           400,
		"/:/scrobble?key=9999":                          404,
		"/:/progress?key=" + show.RatingKey:             400,
		"/:/timeline?key=" + show.RatingKey:             400,
		"/:/progress?key=" + show.RatingKey + "&time=5": 400,
	} {
		if code := f.do(t, http.MethodGet, path); code != want {
			t.Errorf("GET %s = %d, want %d", path, code, want)
		}
	}
	ep := f.find(t, "/library/sections/1/all?type=4", "Pilot")
	if code := f.do(t, http.MethodGet, "/:/timeline?state=stopped&key="+ep.RatingKey); code != 200 {
		t.Errorf("timeline without time = %d, want 200", code)
	}
	if code := f.do(t, http.MethodGet, "/:/progress?key="+ep.RatingKey); code != 400 {
		t.Errorf("progress without time = %d, want 400", code)
	}
}

func TestScrobbleShowCascades(t *testing.T) {
	f := newTVFixture(t)
	lost := f.find(t, "/library/sections/1/all", "Lost")
	if lost.ViewedLeafCount == nil || *lost.ViewedLeafCount != 0 {
		t.Fatalf("viewedLeafCount = %v", lost.ViewedLeafCount)
	}
	b, _ := io.ReadAll(f.get(t, "/library/sections/1/all", nil).Body)
	if !strings.Contains(string(b), `viewedLeafCount="0"`) {
		t.Errorf("viewedLeafCount=0 not emitted:\n%s", b)
	}

	season1 := f.find(t, lost.Key, "Season 1")
	if code := f.do(t, http.MethodGet, "/:/scrobble?key="+season1.RatingKey); code != 200 {
		t.Fatalf("scrobble season: %d", code)
	}
	lost = f.find(t, "/library/sections/1/all", "Lost")
	if *lost.ViewedLeafCount != 2 || lost.LeafCount != 4 || lost.LastViewedAt == 0 {
		t.Errorf("show after season scrobble: %+v", lost)
	}
	if got := titles(f.getJSON(t, "/library/sections/1/all?type=3&unwatched=1")); got != "Specials,Season 2,Season 1" {
		t.Errorf("unwatched seasons = %s", got)
	}

	if code := f.do(t, http.MethodGet, "/:/scrobble?key="+lost.RatingKey); code != 200 {
		t.Fatalf("scrobble show: %d", code)
	}
	if got := titles(f.getJSON(t, "/library/sections/1/all?unwatched=1")); got != "The Wire" {
		t.Errorf("unwatched shows = %s", got)
	}
	if got := titles(f.getJSON(t, "/library/sections/1/all?type=4&unwatched=1")); got != "The Target" {
		t.Errorf("unwatched episodes = %s", got)
	}

	if code := f.do(t, http.MethodGet, "/:/unscrobble?key="+lost.RatingKey); code != 200 {
		t.Fatalf("unscrobble show: %d", code)
	}
	if lost = f.find(t, "/library/sections/1/all", "Lost"); *lost.ViewedLeafCount != 0 {
		t.Errorf("show after unscrobble: %+v", lost)
	}
}

func TestOnDeck(t *testing.T) {
	f := newTVFixture(t)
	ep := func(title string) Metadata { return f.find(t, "/library/sections/1/all?type=4", title) }
	deck := func() []string {
		var out []string
		for _, m := range f.getJSON(t, "/library/onDeck").Metadata {
			out = append(out, m.Title)
		}
		slices.Sort(out)
		return out
	}
	if d := deck(); len(d) != 0 {
		t.Fatalf("empty library state has deck %v", d)
	}

	f.do(t, http.MethodGet, "/:/scrobble?key="+ep("Pilot").RatingKey)
	if d := deck(); !slices.Equal(d, []string{"Episode 2"}) {
		t.Errorf("after Pilot: %v", d)
	}

	f.do(t, http.MethodGet, "/:/timeline?time=1000&key="+ep("The Target").RatingKey)
	f.do(t, http.MethodGet, "/:/scrobble?key="+ep("Episode 2").RatingKey)
	if d := deck(); !slices.Equal(d, []string{"Man of Science", "The Target"}) {
		t.Errorf("after Episode 2 + Target in progress: %v", d)
	}

	// An in-progress episode replaces the show's next-up entry.
	f.do(t, http.MethodGet, "/:/timeline?time=1000&key="+ep("Recap").RatingKey)
	if d := deck(); !slices.Equal(d, []string{"Recap", "The Target"}) {
		t.Errorf("with Lost episode in progress: %v", d)
	}

	// A fully watched show drops off; the section variant filters by section.
	f.do(t, http.MethodGet, "/:/unscrobble?key="+ep("Recap").RatingKey)
	f.do(t, http.MethodGet, "/:/scrobble?key="+ep("Man of Science").RatingKey)
	if d := deck(); !slices.Equal(d, []string{"The Target"}) {
		t.Errorf("after finishing Lost: %v", d)
	}
	if n := f.getJSON(t, "/library/sections/1/onDeck").Size; n != 1 {
		t.Errorf("section onDeck size = %d", n)
	}
	if code := f.do(t, http.MethodGet, "/library/sections/42/onDeck"); code != 404 {
		t.Errorf("unknown section onDeck = %d", code)
	}
}
