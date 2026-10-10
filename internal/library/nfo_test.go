package library

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const heatNFO = `<?xml version="1.0" encoding="UTF-8" standalone="yes" ?>
<movie>
  <title>Heat</title>
  <originaltitle>Heat</originaltitle>
  <sorttitle>Heat 1995</sorttitle>
  <ratings>
    <rating name="themoviedb" max="10"><value>7.9</value></rating>
    <rating name="imdb" max="100" default="true"><value>83</value></rating>
  </ratings>
  <year>1995</year>
  <premiered>1995-12-15</premiered>
  <plot>  A group of professional bank robbers start to feel the heat.  </plot>
  <outline>Short.</outline>
  <tagline>A Los Angeles crime saga</tagline>
  <mpaa>Rated R</mpaa>
  <genre>Crime / Drama</genre>
  <genre>Thriller</genre>
  <genre>crime</genre>
  <genre></genre>
  <country>United States of America</country>
  <credits>Michael Mann</credits>
  <director>Michael Mann</director>
  <studio>Warner Bros.</studio>
  <studio>Regency</studio>
  <set><name>Michael Mann Films</name><overview>…</overview></set>
  <set>Crime Classics</set>
  <actor><name>Robert De Niro</name><role>Neil McCauley</role><order>1</order><thumb>http://x/y.jpg</thumb></actor>
  <actor><name>Al Pacino</name><role>Lt. Vincent Hanna</role><order>0</order></actor>
  <actor><name>Val Kilmer</name></actor>
  <actor><role>Nobody</role></actor>
</movie>
https://www.themoviedb.org/movie/949`

func TestParseNFOMovie(t *testing.T) {
	m, err := ParseNFO([]byte(heatNFO), NFOMovie)
	if err != nil {
		t.Fatal(err)
	}
	want := &Meta{
		Title: "Heat", SortTitle: "Heat 1995", Year: 1995, Date: "1995-12-15",
		Summary: "A group of professional bank robbers start to feel the heat.",
		Tagline: "A Los Angeles crime saga", ContentRating: "R", Rating: 8.3, Studio: "Warner Bros.",
		Genres: []string{"Crime", "Drama", "Thriller"}, Directors: []string{"Michael Mann"},
		Writers: []string{"Michael Mann"}, Countries: []string{"United States of America"},
		Roles: []Role{
			{Name: "Al Pacino", Role: "Lt. Vincent Hanna"}, {Name: "Robert De Niro", Role: "Neil McCauley"}, {Name: "Val Kilmer"},
		},
		Collections: []string{"Michael Mann Films", "Crime Classics"},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("ParseNFO =\n%+v\nwant\n%+v", m, want)
	}
}

func TestParseNFOVariants(t *testing.T) {
	for name, c := range map[string]struct {
		doc, kind string
		check     func(*Meta) bool
	}{
		"plain rating, outline, year from premiered": {
			`<movie><title>A</title><rating>7.44</rating><outline>Out</outline><premiered>2001-05-02</premiered><originaltitle>Ä</originaltitle></movie>`, NFOMovie,
			func(m *Meta) bool {
				return m.Rating == 7.4 && m.Summary == "Out" && m.Year == 2001 && m.OriginalTitle == "Ä"
			},
		},
		"out of range and junk ratings and dates": {
			`<movie><rating>11</rating><premiered>15/12/1995</premiered><year>soon</year></movie>`, NFOMovie,
			func(m *Meta) bool { return m.Rating == 0 && m.Date == "" && m.Year == 0 },
		},
		"no default rating uses the first": {
			`<movie><ratings><rating max="5"><value>4</value></rating><rating><value>2</value></rating></ratings></movie>`, NFOMovie,
			func(m *Meta) bool { return m.Rating == 8 },
		},
		"episode aired, year and season ignored, no sets": {
			`<episodedetails><title>Pilot</title><season>9</season><episode>9</episode><year>1999</year><aired>2004-09-22</aired><premiered>2000-01-01</premiered><set>X</set></episodedetails>`, NFOEpisode,
			func(m *Meta) bool {
				return m.Title == "Pilot" && m.Date == "2004-09-22" && m.Year == 0 && m.Collections == nil
			},
		},
		"tvshow": {
			`<tvshow><title>Lost</title><year>2004</year><studio>ABC</studio><genre>Drama</genre></tvshow>`, NFOShow,
			func(m *Meta) bool {
				return m.Title == "Lost" && m.Year == 2004 && m.Studio == "ABC" && len(m.Genres) == 1
			},
		},
		"latin-1": {
			"<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><movie><title>Am\xe9lie</title></movie>", NFOMovie,
			func(m *Meta) bool { return m.Title == "Amélie" },
		},
	} {
		m, err := ParseNFO([]byte(c.doc), c.kind)
		if err != nil || !c.check(m) {
			t.Errorf("%s: %+v %v", name, m, err)
		}
	}

	for name, doc := range map[string]string{
		"url only":   "https://www.imdb.com/title/tt0113277/",
		"wrong root": "<tvshow><title>Lost</title></tvshow>",
		"encoding":   `<?xml version="1.0" encoding="Shift_JIS"?><movie><title>x</title></movie>`,
		"empty":      "",
	} {
		if _, err := ParseNFO([]byte(doc), NFOMovie); !errors.Is(err, ErrNotNFO) {
			t.Errorf("%s: err = %v, want ErrNotNFO", name, err)
		}
	}

	// Lists are capped.
	var b strings.Builder
	b.WriteString("<movie>")
	for i := 0; i < 80; i++ {
		b.WriteString("<actor><name>A" + strings.Repeat("x", i) + "</name></actor><genre>G" + strings.Repeat("y", i) + "</genre>")
	}
	b.WriteString("</movie>")
	if m, _ := ParseNFO([]byte(b.String()), NFOMovie); len(m.Roles) != maxListValues || len(m.Genres) != maxListValues {
		t.Errorf("caps: %d roles, %d genres", len(m.Roles), len(m.Genres))
	}
}
