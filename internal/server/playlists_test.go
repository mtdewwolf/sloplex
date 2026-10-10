package server

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestPlaylists(t *testing.T) {
	f := newTVFixture(t)
	shows := f.getJSON(t, "/library/sections/1/all").Metadata
	lost, wire := shows[0], shows[1]
	seasons := f.getJSON(t, lost.Key).Metadata // Specials, Season 1, Season 2
	wireEp := f.getJSON(t, "/library/metadata/"+wire.RatingKey+"/allLeaves").Metadata[0]

	expect := func(method, path, token string, want int) {
		t.Helper()
		if resp := f.api(t, method, path, token, nil, nil); resp.StatusCode != want {
			t.Errorf("%s %s = %d, want %d", method, path, resp.StatusCode, want)
		}
	}
	items := func(key, token string) (string, []Metadata) {
		t.Helper()
		var res containerResp
		f.api(t, "GET", key, token, nil, &res)
		var names []string
		for _, m := range res.MediaContainer.Metadata {
			names = append(names, fmt.Sprintf("%s%d", m.Title, m.PlaylistItemID))
		}
		return strings.Join(names, ","), res.MediaContainer.Metadata
	}
	order := func(key string) string {
		t.Helper()
		_, mds := items(key, testToken)
		var out []string
		for _, m := range mds {
			out = append(out, m.Title)
		}
		return strings.Join(out, ",")
	}

	// A show expands to its episodes, specials last.
	uri := url.QueryEscape("server://abc123/com.plexapp.plugins.library/library/metadata/" + lost.RatingKey)
	var created containerResp
	resp := f.api(t, "POST", "/playlists?type=video&smart=0&title=Binge&uri="+uri, testToken, nil, &created)
	if resp.StatusCode != 200 || len(created.MediaContainer.Metadata) != 1 {
		t.Fatalf("create = %d %+v", resp.StatusCode, created)
	}
	pl := created.MediaContainer.Metadata[0]
	if pl.Type != "playlist" || pl.PlaylistType != "video" || pl.Smart != "0" || pl.LeafCount != 4 ||
		pl.Duration != 4*7_200_000 || pl.Key != "/playlists/"+pl.RatingKey+"/items" {
		t.Errorf("playlist = %+v", pl)
	}
	if got := order(pl.Key); got != "Pilot,Episode 2,Man of Science,Recap" {
		t.Errorf("show expansion = %s", got)
	}
	base := "/playlists/" + pl.RatingKey

	// A season adds its episodes; duplicates get their own entries.
	expect("PUT", base+"/items?ids="+seasons[1].RatingKey+","+wireEp.RatingKey, testToken, 200)
	_, mds := items(pl.Key, testToken)
	if len(mds) != 7 || mds[4].Title != "Pilot" || mds[4].PlaylistItemID == mds[0].PlaylistItemID || mds[6].Title != "The Target" {
		t.Fatalf("after add = %v", order(pl.Key))
	}
	var page containerResp
	f.api(t, "GET", pl.Key+"?X-Plex-Container-Start=6&X-Plex-Container-Size=1", testToken, nil, &page)
	if pm := page.MediaContainer; len(pm.Metadata) != 1 || pm.Metadata[0].Title != "The Target" || *pm.TotalSize != 7 || pm.Title1 != "Binge" {
		t.Errorf("paged items = %+v", pm)
	}

	// Move The Target to the top, then Recap after Pilot; remove the
	// second Pilot.
	entry := func(i int) string { return fmt.Sprint(mds[i].PlaylistItemID) }
	expect("PUT", base+"/items/"+entry(6)+"/move", testToken, 200)
	expect("PUT", base+"/items/"+entry(3)+"/move?after="+entry(0), testToken, 200)
	expect("DELETE", base+"/items/"+entry(4), testToken, 200)
	expect("DELETE", base+"/items/"+entry(4), testToken, 404)
	if got := order(pl.Key); got != "The Target,Pilot,Recap,Episode 2,Man of Science,Episode 2" {
		t.Errorf("after moves = %s", got)
	}

	// Renaming and listing, sorted by title without the article.
	expect("PUT", base+"?title="+url.QueryEscape("The Long Night"), testToken, 200)
	f.api(t, "POST", "/playlists?title=Action", testToken, nil, nil)
	var list containerResp
	f.api(t, "GET", "/playlists", testToken, nil, &list)
	if got := titles(list.MediaContainer); got != "Action,The Long Night" || list.MediaContainer.Size != 2 {
		t.Errorf("list = %s", got)
	}
	var audio containerResp
	f.api(t, "GET", "/playlists?playlistType=audio", testToken, nil, &audio)
	if len(audio.MediaContainer.Metadata) != 0 {
		t.Errorf("audio playlists = %+v", audio.MediaContainer.Metadata)
	}
	_, _, xml := f.raw(t, base, testToken)
	if !strings.Contains(string(xml), `<Playlist ratingKey="`+pl.RatingKey+`"`) || !strings.Contains(string(xml), `playlistType="video"`) ||
		!strings.Contains(string(xml), `leafCount="6"`) {
		t.Errorf("XML:\n%s", xml)
	}
	_, _, itemsXML := f.raw(t, pl.Key, testToken)
	if !strings.Contains(string(itemsXML), `playlistItemID="`) {
		t.Errorf("items XML lacks playlistItemID:\n%s", itemsXML)
	}

	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/playlists?title=%20", 400},
		{"POST", "/playlists?title=X&type=audio", 400},
		{"POST", "/playlists?title=X&smart=1", 400},
		{"POST", "/playlists?title=X&ids=999999", 400},
		{"POST", "/playlists?title=X&ids=abc", 400},
		{"PUT", base + "/items/" + entry(0) + "/move?after=999999", 400},
		{"PUT", base + "/items/" + entry(0) + "/move?after=-1", 400},
		{"PUT", base + "/items/999999/move", 404},
		{"GET", "/playlists/999999", 404},
		{"GET", "/playlists/abc/items", 404},
	} {
		expect(c.method, c.path, testToken, c.want)
	}

	// Other accounts cannot see or touch the playlist, admins included.
	f.createUser(t, "bob", "builder99", true)
	bob := f.login(t, "bob", "builder99")
	for _, c := range []struct{ method, path string }{
		{"GET", base}, {"GET", pl.Key}, {"PUT", base + "?title=Mine"}, {"DELETE", base},
		{"PUT", base + "/items?ids=" + wireEp.RatingKey}, {"DELETE", base + "/items/" + entry(0)},
	} {
		expect(c.method, c.path, bob, 404)
	}
	var bobs containerResp
	f.api(t, "GET", "/playlists", bob, nil, &bobs)
	if len(bobs.MediaContainer.Metadata) != 0 {
		t.Errorf("bob sees %+v", bobs.MediaContainer.Metadata)
	}

	// Items the account cannot see are left out, and cannot be added.
	var kid accountView
	f.api(t, "POST", "/sloplex/accounts", testToken, map[string]any{"name": "kid", "password": "cartoons1"}, &kid)
	kidTok := f.login(t, "kid", "cartoons1")
	var kids containerResp
	f.api(t, "POST", "/playlists?title=Mine&ids="+wireEp.RatingKey, kidTok, nil, &kids)
	kidPL := kids.MediaContainer.Metadata[0]
	f.api(t, "PATCH", fmt.Sprintf("/sloplex/accounts/%d", kid.ID), testToken, map[string]any{"libraries": []int{}}, nil)
	if got, _ := items(kidPL.Key, kidTok); got != "" {
		t.Errorf("hidden items shown: %s", got)
	}
	var hidden containerResp
	f.api(t, "GET", "/playlists/"+kidPL.RatingKey, kidTok, nil, &hidden)
	if hidden.MediaContainer.Metadata[0].LeafCount != 0 || hidden.MediaContainer.Metadata[0].Duration != 0 {
		t.Errorf("hidden items counted: %+v", hidden.MediaContainer.Metadata[0])
	}
	expect("PUT", "/playlists/"+kidPL.RatingKey+"/items?ids="+wireEp.RatingKey, kidTok, 400)
	// Access restored: the entry is back.
	f.api(t, "PATCH", fmt.Sprintf("/sloplex/accounts/%d", kid.ID), testToken, map[string]any{"libraries": nil}, nil)
	if got, _ := items(kidPL.Key, kidTok); !strings.HasPrefix(got, "The Target") {
		t.Errorf("after access restored: %q", got)
	}

	expect("DELETE", base, testToken, 200)
	expect("GET", base, testToken, 404)
}
