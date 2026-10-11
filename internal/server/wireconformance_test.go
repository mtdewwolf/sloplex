package server

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/library"
	"github.com/mtdewwolf/sloplex/internal/playstate"
	"github.com/mtdewwolf/sloplex/internal/probe"
)

// The documented request forms of docs/spec/wire-conformance.md.

// wire sends a request with token ("" for none) and extra headers, and
// returns the status, headers and body.
func (f *fixture) wire(t *testing.T, method, path, token string, hdr map[string]string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

var clientID = map[string]string{"X-Plex-Client-Identifier": "test-client"}

func TestTimelinePost(t *testing.T) {
	f := newFixture(t, Options{})
	m := f.find(t, "/library/sections/1/all", "Alien")
	state := func() Metadata { return f.getJSON(t, m.Key).Metadata[0] }
	tl := "/:/timeline?ratingKey=" + m.RatingKey

	// XML by default: an empty MediaContainer.
	code, hdr, body := f.wire(t, "POST", tl+"&state=playing&time=60000", testToken, clientID)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/xml") {
		t.Fatalf("POST timeline = %d %q\n%s", code, hdr.Get("Content-Type"), body)
	}
	var mc MediaContainer
	if err := xml.Unmarshal([]byte(body), &mc); err != nil || mc.Size != 0 || !strings.Contains(body, `<MediaContainer size="0"`) {
		t.Errorf("XML body = %v\n%s", err, body)
	}
	if s := state(); s.ViewOffset != 60000 || s.ViewCount != 0 {
		t.Fatalf("in progress: %+v", s)
	}

	// JSON on request; the client identifier may be a query parameter. The
	// completion rule is the legacy one.
	code, hdr, body = f.wire(t, "POST", tl+"&state=stopped&time=6480000&X-Plex-Client-Identifier=q", testToken,
		map[string]string{"Accept": "application/json"})
	if code != 200 || hdr.Get("Content-Type") != "application/json" || strings.TrimSpace(body) != `{"MediaContainer":{"size":0}}` {
		t.Fatalf("POST timeline JSON = %d %q %s", code, hdr.Get("Content-Type"), body)
	}
	if s := state(); s.ViewOffset != 0 || s.ViewCount != 1 {
		t.Fatalf("completed: %+v", s)
	}

	// Without time it is a no-op that still answers with a container.
	if code, _, body := f.wire(t, "POST", tl+"&state=paused", testToken, clientID); code != 200 || !strings.Contains(body, "MediaContainer") {
		t.Errorf("POST without time = %d %s", code, body)
	}
	if s := state(); s.ViewCount != 1 || s.ViewOffset != 0 {
		t.Errorf("no-op changed state: %+v", s)
	}

	// Rejections leave state alone.
	for path, want := range map[string]int{
		tl + "&time=1000&state=rewinding":                400,
		tl + "&time=abc":                                 400,
		"/:/timeline?time=1000":                          400,
		"/:/timeline?ratingKey=abc&time=1000":            400,
		"/:/timeline?ratingKey=99999&time=1000":          404,
		"/:/timeline?key=/library/metadata/99999&time=1": 404,
	} {
		if code, _, _ := f.wire(t, "POST", path, testToken, clientID); code != want {
			t.Errorf("POST %s = %d, want %d", path, code, want)
		}
	}
	if code, _, _ := f.wire(t, "POST", tl+"&time=1000", testToken, nil); code != 400 {
		t.Errorf("POST without client identifier = %d, want 400", code)
	}
	if code, _, _ := f.wire(t, "POST", tl+"&time=1000", "", clientID); code != 401 {
		t.Errorf("POST without token = %d, want 401", code)
	}
	if s := state(); s.ViewCount != 1 || s.ViewOffset != 0 {
		t.Errorf("rejected requests changed state: %+v", s)
	}

	// The legacy methods are unchanged: no client identifier, empty body.
	code, _, body = f.wire(t, "GET", tl+"&time=2000&state=whatever", testToken, nil)
	if code != 200 || body != "" {
		t.Errorf("legacy GET = %d %q", code, body)
	}
	if s := state(); s.ViewOffset != 2000 {
		t.Errorf("legacy GET not applied: %+v", s)
	}
}

func TestTimelinePostAccounts(t *testing.T) {
	f := newFixture(t, Options{})
	heat := f.find(t, "/library/sections/1/all", "Heat")
	f.api(t, "POST", "/sloplex/accounts", testToken, map[string]any{"name": "kid", "password": "cartoons1", "libraries": []int{}}, nil)
	kid := f.login(t, "kid", "cartoons1")
	f.createUser(t, "bob", "builder1", false)
	bob := f.login(t, "bob", "builder1")

	if code, _, _ := f.wire(t, "POST", "/:/timeline?time=5000&ratingKey="+heat.RatingKey, kid, clientID); code != 404 {
		t.Errorf("restricted account = %d, want 404", code)
	}
	if code, _, _ := f.wire(t, "POST", "/:/timeline?time=5000&ratingKey="+heat.RatingKey, bob, clientID); code != 200 {
		t.Errorf("bob = %d", code)
	}
	var got struct{ MediaContainer MediaContainer }
	f.api(t, "GET", heat.Key, bob, nil, &got)
	if got.MediaContainer.Metadata[0].ViewOffset != 5000 {
		t.Errorf("bob's state = %+v", got.MediaContainer.Metadata[0])
	}
	if s := f.getJSON(t, heat.Key).Metadata[0]; s.ViewOffset != 0 {
		t.Errorf("owner's state changed by others: %+v", s)
	}
}

func TestTimelinePostShowIsBadRequest(t *testing.T) {
	f := newTVFixture(t)
	show := f.getJSON(t, "/library/sections/1/all").Metadata[0]
	if code, _, _ := f.wire(t, "POST", "/:/timeline?time=5&key="+show.RatingKey, testToken, clientID); code != 400 {
		t.Errorf("POST timeline on a show = %d, want 400", code)
	}
}

// TestTimelinePostPersists checks that progress from the documented form
// is saved, by reopening the play state file.
func TestTimelinePostPersists(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Heat (1995).mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _ := library.Open("")
	if err := store.SyncSections([]library.SectionDef{{Title: "Movies", Type: library.TypeMovie, Paths: []string{root}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, fakeProber{}, nil)
	sec := store.Sections()[0].ID
	if _, err := sc.Scan(context.Background(), sec); err != nil {
		t.Fatal(err)
	}
	item := store.Items(sec)[0].ID
	path := filepath.Join(t.TempDir(), "playstate.json")
	play, err := playstate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	accts, _ := accounts.Open("")
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	srv := httptest.NewServer(New(context.Background(), opts, Deps{Store: store, Scanner: sc, Play: play, Accounts: accts}).Handler())
	f := &fixture{srv: srv}
	code, _, _ := f.wire(t, "POST", fmt.Sprintf("/:/timeline?state=playing&time=123456&key=/library/metadata/%d", item), testToken, clientID)
	srv.Close()
	if code != 200 {
		t.Fatalf("POST timeline = %d", code)
	}
	reopened, err := playstate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if st := reopened.Get(accounts.OwnerID, item); st.ViewOffset != 123456 || st.LastViewedAt == 0 {
		t.Errorf("after reopen = %+v", st)
	}
}

func TestRefreshPost(t *testing.T) {
	f := newFixture(t, Options{})
	if err := os.WriteFile(filepath.Join(f.root, "Ronin (1998).mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, body := f.wire(t, "POST", "/library/sections/1/refresh", testToken, nil)
	if code != 200 || body != "" {
		t.Fatalf("POST refresh = %d %q", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(f.store.Items(f.section)) != 4 {
		if time.Now().After(deadline) {
			t.Fatal("POST refresh did not pick up the new file")
		}
		time.Sleep(10 * time.Millisecond)
	}

	f.createUser(t, "bob", "builder1", false)
	bob := f.login(t, "bob", "builder1")
	for _, c := range []struct {
		method, path, token string
		want                int
	}{
		{"POST", "/library/sections/1/refresh?force=1", testToken, 501},
		{"POST", "/library/sections/1/refresh?force=0", testToken, 200},
		{"POST", "/library/sections/1/refresh?force=0&path=" + url.QueryEscape(f.root), testToken, 501},
		{"POST", "/library/sections/1/refresh?force=yes", testToken, 400},
		{"POST", "/library/sections/99/refresh", testToken, 404},
		{"POST", "/library/sections/abc/refresh", testToken, 404},
		{"POST", "/library/sections/1/refresh", bob, 403},
		{"POST", "/library/sections/1/refresh", "", 401},
		// Cancellation is not implemented; see wire-conformance.md.
		{"DELETE", "/library/sections/1/refresh", testToken, 405},
		// Legacy forms keep working and do not check force.
		{"GET", "/library/sections/1/refresh?force=yes", testToken, 200},
		{"PUT", "/library/sections/1/refresh", testToken, 200},
		{"PUT", "/library/sections/1/refresh", bob, 403},
	} {
		if code, _, _ := f.wire(t, c.method, c.path, c.token, nil); code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, code, c.want)
		}
	}
}

func TestCollectionItemsAlias(t *testing.T) {
	f := newCollectionsFixture(t)
	var cols containerResp
	f.api(t, "GET", "/library/sections/1/collections", testToken, nil, &cols)
	if len(cols.MediaContainer.Metadata) != 1 {
		t.Fatalf("collections = %+v", cols.MediaContainer.Metadata)
	}
	id := cols.MediaContainer.Metadata[0].RatingKey
	base := "/library/collections/" + id

	// JSON: identical to /children.
	var items, kids json.RawMessage
	f.api(t, "GET", base+"/items", testToken, nil, &items)
	f.api(t, "GET", base+"/children", testToken, nil, &kids)
	if string(items) != string(kids) {
		t.Errorf("items and children differ:\n%s\n%s", items, kids)
	}
	var got containerResp
	if err := json.Unmarshal(items, &got); err != nil || got.MediaContainer.Size != 2 ||
		got.MediaContainer.Metadata[0].Title != "Thief" || got.MediaContainer.Metadata[1].Title != "Heat" {
		t.Errorf("items = %v %s", err, items)
	}

	// XML by default.
	code, hdr, body := f.wire(t, "GET", base+"/items", testToken, nil)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/xml") ||
		strings.Count(body, "<Video ") != 2 || !strings.Contains(body, `title2="Michael Mann"`) {
		t.Errorf("XML items = %d %q\n%s", code, hdr.Get("Content-Type"), body)
	}

	// Paging.
	code, _, body = f.wire(t, "GET", base+"/items", testToken,
		map[string]string{"Accept": "application/json", "X-Plex-Container-Start": "1", "X-Plex-Container-Size": "1"})
	var page containerResp
	if err := json.Unmarshal([]byte(body), &page); code != 200 || err != nil || page.MediaContainer.Size != 1 ||
		page.MediaContainer.TotalSize == nil || *page.MediaContainer.TotalSize != 2 || page.MediaContainer.Metadata[0].Title != "Heat" {
		t.Errorf("paged items = %d %s", code, body)
	}

	// Unknown, malformed and hidden collections.
	f.api(t, "POST", "/sloplex/accounts", testToken, map[string]any{"name": "kid", "password": "cartoons1", "libraries": []int{2}}, nil)
	kid := f.login(t, "kid", "cartoons1")
	for _, c := range []struct {
		path, token string
		want        int
	}{
		{"/library/collections/99999/items", testToken, 404},
		{"/library/collections/abc/items", testToken, 404},
		{base + "/items", kid, 404},
		{base + "/items", "", 401},
	} {
		if code, _, _ := f.wire(t, "GET", c.path, c.token, nil); code != c.want {
			t.Errorf("GET %s = %d, want %d", c.path, code, c.want)
		}
	}
}

func TestSectionCollectionDelete(t *testing.T) {
	f := newCollectionsFixture(t)
	ronin := f.find(t, "/library/sections/1/all", "Ronin")
	var created containerResp
	if resp := f.api(t, "POST", "/library/collections?sectionId=1&title=Mine&ids="+ronin.RatingKey, testToken, nil, &created); resp.StatusCode != 200 {
		t.Fatalf("create: %s", resp.Status)
	}
	mine := created.MediaContainer.Metadata[0].RatingKey
	var cols containerResp
	f.api(t, "GET", "/library/sections/1/collections", testToken, nil, &cols)
	auto := ""
	for _, c := range cols.MediaContainer.Metadata {
		if c.Title == "Michael Mann" {
			auto = c.RatingKey
		}
	}
	f.createUser(t, "bob", "builder1", false)
	bob := f.login(t, "bob", "builder1")
	exists := func(id string) bool {
		t.Helper()
		code, _, _ := f.wire(t, "GET", "/library/collections/"+id, testToken, nil)
		return code == 200
	}

	for _, c := range []struct {
		path, token string
		want        int
	}{
		{"/library/sections/1/collection/" + mine, bob, 403},
		{"/library/sections/1/collection/" + mine, "", 401},
		{"/library/sections/2/collection/" + mine, testToken, 404}, // wrong section
		{"/library/sections/99/collection/" + mine, testToken, 404},
		{"/library/sections/abc/collection/" + mine, testToken, 404},
		{"/library/sections/1/collection/abc", testToken, 404},
		{"/library/sections/1/collection/99999", testToken, 404},
		{"/library/sections/1/collection/" + auto, testToken, 400}, // automatic
	} {
		if code, _, _ := f.wire(t, "DELETE", c.path, c.token, nil); code != c.want {
			t.Errorf("DELETE %s = %d, want %d", c.path, code, c.want)
		}
	}
	if !exists(mine) || !exists(auto) {
		t.Fatal("a rejected delete removed a collection")
	}

	code, _, body := f.wire(t, "DELETE", "/library/sections/1/collection/"+mine, testToken, nil)
	if code != 200 || body != "" {
		t.Fatalf("DELETE = %d %q", code, body)
	}
	if exists(mine) {
		t.Error("collection still exists")
	}
	if id, _ := strconv.Atoi(ronin.RatingKey); !hasItem(f.store, id) {
		t.Error("member was removed with the collection")
	}
	if code, _, _ := f.wire(t, "DELETE", "/library/sections/1/collection/"+mine, testToken, nil); code != 404 {
		t.Errorf("second DELETE = %d, want 404", code)
	}
}

func hasItem(store *library.Store, id int) bool {
	_, err := store.Item(id)
	return err == nil
}

// subProber reports a video, an audio and an image subtitle stream.
type subProber struct{}

func (subProber) Probe(_ context.Context, path string) (probe.Info, error) {
	return probe.Info{Container: probe.ContainerForPath(path), Duration: 60_000, VideoCodec: "h264", AudioCodec: "aac", Streams: []probe.Stream{
		{Index: 0, Type: probe.StreamVideo, Codec: "h264", Width: 1920, Height: 1080},
		{Index: 1, Type: probe.StreamAudio, Codec: "aac", Channels: 2, Language: "eng"},
		{Index: 2, Type: probe.StreamSubtitle, Codec: "hdmv_pgs_subtitle", Language: "eng"},
	}}, nil
}

const (
	srtText = "1\n00:00:01,000 --> 00:00:02,000\nHello\n"
	assText = "[Script Info]\nScriptType: v4.00+\n\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,Bonjour\n"
	vttText = "WEBVTT\n\n00:00:03.000 --> 00:00:04.000\nHallo\n"
)

// newSubtitleFixture serves one movie with SRT, ASS and VTT sidecars and
// embedded video, audio and PGS streams, without ffmpeg. It returns the
// stream IDs by codec.
func newSubtitleFixture(t *testing.T) (*fixture, map[string]int) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "Sub (2020)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"Sub (2020).mkv": "x", "Sub (2020).en.srt": srtText, "Sub (2020).fr.ass": assText, "Sub (2020).de.vtt": vttText,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := library.Open("")
	if err := store.SyncSections([]library.SectionDef{{Title: "Movies", Type: library.TypeMovie, Paths: []string{root}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(store, subProber{}, nil)
	if _, err := sc.Scan(context.Background(), store.Sections()[0].ID); err != nil {
		t.Fatal(err)
	}
	opts := Options{FriendlyName: "test", MachineID: "abc123", Version: "0.0.1", Tokens: []string{testToken}}
	f := &fixture{srv: startServer(t, opts, store, sc, ""), store: store, root: root}
	movie := f.getJSON(t, "/library/sections/1/all").Metadata[0]
	ids := map[string]int{}
	for _, st := range streamsOf(t, f, movie.Key, testToken) {
		ids[st.Codec] = st.ID
	}
	for _, c := range []string{"h264", "aac", "hdmv_pgs_subtitle", "srt", "ass", "vtt"} {
		if ids[c] == 0 {
			t.Fatalf("no %s stream in %v", c, ids)
		}
	}
	return f, ids
}

func TestStreamExtPath(t *testing.T) {
	f, ids := newSubtitleFixture(t)
	p := func(codec, rest string) string { return fmt.Sprintf("/library/streams/%d%s", ids[codec], rest) }

	// Served without conversion.
	for _, c := range []struct{ path, ctype, body string }{
		{p("srt", ".srt"), "application/x-subrip", srtText},
		{p("srt", ".SRT"), "application/x-subrip", srtText},
		{p("srt", ".srt?autoAdjustSubtitle=0"), "application/x-subrip", srtText},
		{p("ass", ".ass"), "text/x-ssa", assText},
		{p("ass", ".ssa"), "text/x-ssa", assText},
		{p("vtt", ".vtt"), "text/vtt; charset=utf-8", vttText},
		// format wins over the extension.
		{p("vtt", ".srt?format=vtt"), "text/vtt; charset=utf-8", vttText},
		// The legacy form is unchanged.
		{p("srt", ""), "application/x-subrip", srtText},
	} {
		code, hdr, body := f.wire(t, "GET", c.path, testToken, nil)
		if code != 200 || hdr.Get("Content-Type") != c.ctype || body != c.body {
			t.Errorf("GET %s = %d %q %q", c.path, code, hdr.Get("Content-Type"), body)
		}
	}
	code, _, body := f.wire(t, "GET", p("vtt", ".vtt?offset=2"), testToken, nil)
	if code != 200 || !strings.Contains(body, "00:00:01.000 --> 00:00:02.000\nHallo") {
		t.Errorf("shifted vtt = %d %q", code, body)
	}

	h := func(codec string) string { return fmt.Sprint(ids[codec]) }
	for path, want := range map[string]int{
		p("srt", ".srt?encoding=utf-8"):       501,
		p("srt", ".srt?autoAdjustSubtitle=1"): 501,
		// Conversion needs ffmpeg, which this fixture lacks.
		p("srt", ".vtt"):               503,
		p("srt", ".srt?format=ass"):    503,
		p("srt", ".srt?format=sub"):    400,
		p("srt", ".srt?offset=2"):      400,
		p("vtt", ".vtt?offset=-1"):     400,
		p("h264", ".srt"):              501,
		p("aac", ".srt"):               501,
		p("hdmv_pgs_subtitle", ".srt"): 501,
		p("hdmv_pgs_subtitle", ".sup"): 404,
		p("h264", ""):                  400, // legacy statuses stay
		p("hdmv_pgs_subtitle", ""):     400,
		// Malformed or unsupported suffixes.
		p("srt", ".sub"):                            404,
		p("srt", ".idx"):                            404,
		p("srt", ".txt"):                            404,
		p("srt", "."):                               404,
		p("srt", ".srt.srt"):                        404,
		p("srt", ".s%20rt"):                         404,
		"/library/streams/.srt":                     404,
		"/library/streams/0" + h("srt") + ".srt":    404,
		"/library/streams/+" + h("srt") + ".srt":    404,
		"/library/streams/-" + h("srt") + ".srt":    404,
		"/library/streams/abc.srt":                  404,
		"/library/streams/99999.srt":                404,
		"/library/streams/99999999999999999999.srt": 404,
	} {
		if code, _, _ := f.wire(t, "GET", path, testToken, nil); code != want {
			t.Errorf("GET %s = %d, want %d", path, code, want)
		}
	}

	// No inert PUT or DELETE.
	for _, m := range []string{"PUT", "DELETE"} {
		if code, _, _ := f.wire(t, m, p("srt", ".srt?offset=100"), testToken, nil); code != 405 {
			t.Errorf("%s = %d, want 405", m, code)
		}
	}

	// Hidden libraries look like unknown streams.
	f.api(t, "POST", "/sloplex/accounts", testToken, map[string]any{"name": "kid", "password": "cartoons1", "libraries": []int{}}, nil)
	kid := f.login(t, "kid", "cartoons1")
	if code, _, _ := f.wire(t, "GET", p("srt", ".srt"), kid, nil); code != 404 {
		t.Errorf("restricted = %d, want 404", code)
	}
	if code, _, _ := f.wire(t, "GET", p("srt", ".srt"), "", nil); code != 401 {
		t.Errorf("no token = %d, want 401", code)
	}
}

// TestStreamExtPathConverts covers conversion of an embedded subtitle
// through the documented form. It needs ffmpeg.
func TestStreamExtPathConverts(t *testing.T) {
	f, movie := newStreamsFixture(t)
	eng := byTitle(streamsOf(t, f, movie.Key, testToken))["English (SRT)"]
	code, hdr, body := f.wire(t, "GET", eng.Key+".vtt", testToken, nil)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/vtt") || !strings.HasPrefix(body, "WEBVTT") {
		t.Errorf("embedded as .vtt = %d %q\n%s", code, hdr.Get("Content-Type"), body)
	}
	code, _, body = f.wire(t, "GET", eng.Key+".srt", testToken, nil)
	if code != 200 || !strings.Contains(body, "General Kenobi") {
		t.Errorf("embedded as .srt = %d\n%s", code, body)
	}
}
