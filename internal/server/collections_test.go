package server

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
)

type containerResp struct{ MediaContainer MediaContainer }

// newCollectionsFixture: section 1 Movies, where Heat (1995, with a poster)
// and Thief (1981) share the .nfo set "Michael Mann" and Ronin (1998) has
// none; section 2 TV with Lost.
func newCollectionsFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("movies/Heat (1995).mkv", "x")
	write("movies/Heat (1995).nfo", `<movie><title>Heat</title><set>Michael Mann</set></movie>`)
	write("movies/Heat (1995)-poster.jpg", "jpg")
	write("movies/Thief (1981).mkv", "x")
	write("movies/Thief (1981).nfo", `<movie><title>Thief</title><set>Michael Mann</set></movie>`)
	write("movies/Ronin (1998).mkv", "x")
	write("tv/Lost/Season 1/Lost - S01E01.mkv", "x")
	store, _ := library.Open("")
	if err := store.SyncSections([]library.SectionDef{
		{Title: "Movies", Type: library.TypeMovie, Paths: []string{filepath.Join(root, "movies")}},
		{Title: "TV", Type: library.TypeShow, Paths: []string{filepath.Join(root, "tv")}},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, fakeProber{}, nil)
	for _, sec := range store.Sections() {
		if _, err := sc.Scan(context.Background(), sec.ID); err != nil {
			t.Fatal(err)
		}
	}
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	return &fixture{srv: startServer(t, opts, store, sc, ""), store: store, root: root}
}

func TestCollectionsBrowse(t *testing.T) {
	f := newCollectionsFixture(t)
	movies := f.getJSON(t, "/library/sections/1/all").Metadata
	ids := map[string]string{}
	for _, m := range movies {
		ids[m.Title] = m.RatingKey
	}

	cols := f.getJSON(t, "/library/sections/1/collections")
	if cols.ViewGroup != "collection" || cols.Title1 != "Movies" || len(cols.Metadata) != 1 {
		t.Fatalf("collections = %+v", cols)
	}
	mann := cols.Metadata[0]
	if mann.Type != "collection" || mann.Subtype != "movie" || mann.Title != "Michael Mann" || mann.ChildCount != 2 ||
		mann.CollectionSource != "nfo" || mann.Key != "/library/collections/"+mann.RatingKey+"/children" ||
		mann.LibrarySectionTitle != "Movies" {
		t.Errorf("collection = %+v", mann)
	}
	// Thief comes first by year but has no poster, so the thumb is Heat's.
	if !strings.HasPrefix(mann.Thumb, "/library/metadata/"+ids["Heat"]+"/thumb/") {
		t.Errorf("thumb = %q, want Heat's (%s)", mann.Thumb, ids["Heat"])
	}
	if titles(f.getJSON(t, mann.Key)) != "Thief,Heat" {
		t.Errorf("children = %s, want release order", titles(f.getJSON(t, mann.Key)))
	}
	// The metadata routes reach collections too.
	if md := f.getJSON(t, "/library/metadata/"+mann.RatingKey).Metadata; len(md) != 1 || md[0].Type != "collection" {
		t.Errorf("metadata route = %+v", md)
	}
	if got := titles(f.getJSON(t, "/library/metadata/"+mann.RatingKey+"/children")); got != "Thief,Heat" {
		t.Errorf("metadata children = %s", got)
	}
	// Paging.
	page := f.getJSON(t, mann.Key+"?X-Plex-Container-Start=1&X-Plex-Container-Size=5")
	if titles(page) != "Heat" || page.TotalSize == nil || *page.TotalSize != 2 {
		t.Errorf("paged children = %s total %v", titles(page), page.TotalSize)
	}
	// Members carry the tag; others do not.
	heat := f.getJSON(t, "/library/metadata/"+ids["Heat"]).Metadata[0]
	if len(heat.Collection) != 1 || heat.Collection[0].Tag != "Michael Mann" {
		t.Errorf("Heat collections = %+v", heat.Collection)
	}
	if ronin := f.getJSON(t, "/library/metadata/"+ids["Ronin"]).Metadata[0]; len(ronin.Collection) != 0 {
		t.Errorf("Ronin collections = %+v", ronin.Collection)
	}
	_, _, xml := f.raw(t, "/library/sections/1/collections", testToken)
	if !strings.Contains(string(xml), `<Directory ratingKey="`+mann.RatingKey+`"`) || !strings.Contains(string(xml), `collectionSource="nfo"`) {
		t.Errorf("XML:\n%s", xml)
	}
	if code, _, _ := f.raw(t, "/library/collections/999999", testToken); code != 404 {
		t.Errorf("unknown collection = %d", code)
	}
	if got := f.getJSON(t, "/library/sections/2/collections"); len(got.Metadata) != 0 {
		t.Errorf("TV collections = %+v", got.Metadata)
	}
}

func TestCollectionsManage(t *testing.T) {
	f := newCollectionsFixture(t)
	ids := map[string]string{}
	for _, m := range f.getJSON(t, "/library/sections/1/all").Metadata {
		ids[m.Title] = m.RatingKey
	}
	lost := f.getJSON(t, "/library/sections/2/all").Metadata[0]
	mann := f.getJSON(t, "/library/sections/1/collections").Metadata[0]
	uri := func(keys ...string) string {
		return url.QueryEscape("server://abc123/com.plexapp.plugins.library/library/metadata/" + strings.Join(keys, ","))
	}
	var created containerResp
	resp := f.api(t, "POST", "/library/collections?type=1&sectionId=1&title="+url.QueryEscape("Heist Films")+"&uri="+uri(ids["Ronin"], ids["Heat"]), testToken, nil, &created)
	if resp.StatusCode != 200 || len(created.MediaContainer.Metadata) != 1 {
		t.Fatalf("create = %d %+v", resp.StatusCode, created)
	}
	heist := created.MediaContainer.Metadata[0]
	if heist.Title != "Heist Films" || heist.CollectionSource != "manual" || heist.ChildCount != 2 {
		t.Errorf("created = %+v", heist)
	}
	base := "/library/collections/" + heist.RatingKey

	expect := func(method, path, token string, want int) {
		t.Helper()
		if resp := f.api(t, method, path, token, nil, nil); resp.StatusCode != want {
			t.Errorf("%s %s = %d, want %d", method, path, resp.StatusCode, want)
		}
	}
	expect("PUT", base+"/items?ids="+ids["Thief"]+","+ids["Heat"], testToken, 200)
	if got := titles(f.getJSON(t, base+"/children")); got != "Thief,Heat,Ronin" {
		t.Errorf("after add = %s", got)
	}
	expect("DELETE", base+"/items/"+ids["Heat"], testToken, 200)
	expect("DELETE", base+"/items/"+ids["Heat"], testToken, 404)
	expect("PUT", base+"?title=Capers", testToken, 200)
	if got := f.getJSON(t, base).Metadata[0]; got.Title != "Capers" || got.ChildCount != 2 {
		t.Errorf("after rename = %+v", got)
	}
	if heat := f.getJSON(t, "/library/metadata/"+ids["Thief"]).Metadata[0]; len(heat.Collection) != 2 {
		t.Errorf("Thief tags = %+v", heat.Collection)
	}

	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/library/collections?sectionId=1&title=%20%20", 400},
		{"POST", "/library/collections?sectionId=1&title=" + strings.Repeat("x", 201), 400},
		{"POST", "/library/collections?title=X", 400},
		{"POST", "/library/collections?sectionId=9&title=X", 404},
		{"POST", "/library/collections?sectionId=1&title=X&ids=" + lost.RatingKey, 400},
		{"POST", "/library/collections?sectionId=2&title=X&ids=" + ids["Heat"], 400},
		{"POST", "/library/collections?sectionId=1&title=X&ids=1,x", 400},
		{"POST", "/library/collections?sectionId=1&title=X&uri=server%3A%2F%2Fabc%2Fnothing", 400},
		{"PUT", "/library/collections/" + mann.RatingKey + "/items?ids=" + ids["Ronin"], 400},
		{"DELETE", "/library/collections/" + mann.RatingKey, 400},
		{"PUT", "/library/collections/" + mann.RatingKey + "?title=Mine", 400},
		{"PUT", "/library/collections/999999/items?ids=" + ids["Ronin"], 404},
	} {
		expect(c.method, c.path, testToken, c.want)
	}

	// Only admins change collections; hidden sections are not found.
	f.createUser(t, "bob", "builder99", false)
	bob := f.login(t, "bob", "builder99")
	expect("POST", "/library/collections?sectionId=1&title=Bob", bob, 403)
	expect("PUT", base+"/items?ids="+ids["Heat"], bob, 403)
	expect("DELETE", base, bob, 403)
	expect("GET", base, bob, 200)
	f.api(t, "POST", "/sloplex/accounts", testToken, map[string]any{"name": "kid", "password": "cartoons1", "libraries": []int{2}}, nil)
	kid := f.login(t, "kid", "cartoons1")
	for _, p := range []string{base, base + "/children", "/library/metadata/" + heist.RatingKey, "/library/sections/1/collections"} {
		expect("GET", p, kid, 404)
	}

	expect("DELETE", base, testToken, 200)
	expect("GET", base, testToken, 404)
	if got := f.getJSON(t, "/library/sections/1/collections"); len(got.Metadata) != 1 {
		t.Errorf("after delete = %+v", got.Metadata)
	}
	if code := f.api(t, "GET", fmt.Sprintf("/library/metadata/%s", ids["Heat"]), testToken, nil, nil).StatusCode; code != 200 {
		t.Errorf("member item after delete = %d", code)
	}
}
