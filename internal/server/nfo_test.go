package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
)

func TestNFOMetadata(t *testing.T) {
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
	write("Heat (1995)/Heat (1995).mkv", "x")
	write("Heat (1995)/Heat (1995).nfo", `<movie><title>Heat</title><originaltitle>Hitze</originaltitle><sorttitle>Aaa</sorttitle>
		<plot>Bank robbers.</plot><tagline>A Los Angeles crime saga</tagline><mpaa>R</mpaa><rating>8.3</rating>
		<studio>Warner Bros.</studio><premiered>1995-12-15</premiered><genre>Crime</genre><director>Michael Mann</director>
		<credits>Michael Mann</credits><country>USA</country><set>Mann</set>
		<actor><name>Al Pacino</name><role>Lt. Vincent Hanna</role></actor></movie>`)
	write("Alien (1979)/Alien (1979).mkv", "x")
	store, _ := library.Open("")
	if err := store.SyncSections([]library.SectionDef{{Title: "Movies", Type: library.TypeMovie, Paths: []string{root}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, fakeProber{}, nil)
	if _, err := sc.Scan(context.Background(), store.Sections()[0].ID); err != nil {
		t.Fatal(err)
	}
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	f := &fixture{srv: startServer(t, opts, store, sc, ""), store: store, root: root}

	// The sort title puts Heat first.
	list := f.getJSON(t, "/library/sections/1/all?sort=titleSort")
	if titles(list) != "Heat,Alien" {
		t.Fatalf("order = %s", titles(list))
	}
	heat := list.Metadata[0]
	if heat.Summary != "Bank robbers." || heat.Tagline != "A Los Angeles crime saga" || heat.ContentRating != "R" ||
		heat.Rating != 8.3 || heat.Studio != "Warner Bros." || heat.OriginalTitle != "Hitze" || heat.TitleSort != "aaa" ||
		heat.OriginallyAvailableAt != "1995-12-15" || len(heat.Genre) != 1 || heat.Genre[0].Tag != "Crime" ||
		heat.Director[0].Tag != "Michael Mann" || heat.Writer[0].Tag != "Michael Mann" || heat.Country[0].Tag != "USA" ||
		heat.Collection[0].Tag != "Mann" {
		t.Errorf("list item = %+v", heat)
	}
	if len(heat.Role) != 0 {
		t.Errorf("lists carry roles: %+v", heat.Role)
	}
	detail := f.getJSON(t, heat.Key).Metadata[0]
	if len(detail.Role) != 1 || detail.Role[0] != (Tag{Tag: "Al Pacino", Role: "Lt. Vincent Hanna"}) {
		t.Errorf("detail roles = %+v", detail.Role)
	}
	_, _, xmlBody := f.raw(t, heat.Key, testToken)
	for _, want := range []string{`summary="Bank robbers."`, `rating="8.3"`, `contentRating="R"`, `<Genre tag="Crime"></Genre>`,
		`<Role tag="Al Pacino" role="Lt. Vincent Hanna"></Role>`, `<Collection tag="Mann"></Collection>`} {
		if !strings.Contains(string(xmlBody), want) {
			t.Errorf("XML lacks %s:\n%s", want, xmlBody)
		}
	}
	// The original title is searchable.
	if hubs := f.getJSON(t, "/hubs/search?query=hitze"); len(hubs.Hub) == 0 || titles(MediaContainer{Metadata: hubs.Hub[0].Metadata}) != "Heat" {
		t.Errorf("search for the original title = %+v", hubs.Hub)
	}
}
