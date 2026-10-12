package server

import (
	"encoding/xml"
	"fmt"
	"testing"
	"time"
)

func TestHomeHubDiscoveryAndPaging(t *testing.T) {
	f := newFixture(t, Options{})
	mc := f.getJSON(t, "/hubs?count=1")
	if mc.Size != 4 || len(mc.Hub) != 4 {
		t.Fatalf("home hubs: %+v", mc)
	}
	recent := mc.Hub[2]
	if recent.HubIdentifier != "home.movies.recent" || recent.Context != "hub.home.movies.recent" || recent.Size != 1 || !bool(recent.More) || recent.HubKey != recent.Metadata[0].Key {
		t.Fatalf("recent hub: %+v", recent)
	}
	// Every advertised detail key must lead to a real local result.
	for _, hub := range mc.Hub {
		detail := f.getJSON(t, hub.Key)
		if hub.HubIdentifier == "home.movies.recent" && len(detail.Metadata) < 2 {
			t.Fatal("recent detail missing movies")
		}
	}
	zero := f.getJSON(t, "/hubs?count=0")
	if zero.Hub[2].Size != 0 || zero.Hub[2].HubKey != "" || !bool(zero.Hub[2].More) {
		t.Fatalf("zero count: %+v", zero.Hub[2])
	}
	section := f.getJSON(t, fmt.Sprintf("/hubs/sections/%d?count=1", f.section))
	if section.LibrarySectionID != f.section || section.Size != 2 || section.Hub[1].Size != 1 || !bool(section.Hub[1].More) {
		t.Fatalf("section: %+v", section)
	}
	page := f.getJSON(t, recent.Key+"&X-Plex-Container-Start=1&X-Plex-Container-Size=1")
	if page.Size != 1 || page.TotalSize == nil || *page.TotalSize < 2 || page.Metadata[0].RatingKey == recent.Metadata[0].RatingKey {
		t.Fatalf("detail paging: %+v", page)
	}
	_, _, body := f.raw(t, "/hubs?count=1", testToken)
	var decoded MediaContainer
	if err := xml.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Hub[2].Key != recent.Key || decoded.Hub[2].Context != recent.Context || decoded.Hub[2].Size != 1 {
		t.Fatalf("XML: %s", body)
	}
	for _, path := range []string{"/hubs?count=-1", "/hubs?count=101", "/hubs?count=abc", "/hubs?count=1&count=2", "/hubs?count=%ZZ", "/hubs/home/recentlyAdded?type=8", "/hubs/home/recentlyAdded?personal=1"} {
		if resp := f.api(t, "GET", path, testToken, nil, nil); resp.StatusCode != 400 {
			t.Errorf("%s returned %d", path, resp.StatusCode)
		}
	}
	if resp := f.api(t, "GET", "/hubs/sections/999999", testToken, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("unknown section: %d", resp.StatusCode)
	}
}

func TestHomeHubAccountStateAndVisibility(t *testing.T) {
	f := newFixture(t, Options{})
	movies := f.getJSON(t, fmt.Sprintf("/library/sections/%d/all", f.section)).Metadata
	if resp := f.api(t, "GET", "/:/progress?ratingKey="+movies[0].RatingKey+"&time=1000", testToken, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("progress: %d", resp.StatusCode)
	}
	watch := f.getJSON(t, "/hubs/continueWatching")
	if watch.Size != 1 || watch.Hub[0].Size != 1 || watch.Hub[0].Metadata[0].RatingKey != movies[0].RatingKey || watch.Hub[0].Metadata[0].ViewOffset != 1000 {
		t.Fatalf("resume hub: %+v", watch)
	}
	if items := f.getJSON(t, watch.Hub[0].Key); items.Size != 1 || items.Metadata[0].RatingKey != movies[0].RatingKey {
		t.Fatalf("resume items: %+v", items)
	}
	bob := f.createUser(t, "bob", "builder99", false)
	token := f.login(t, "bob", "builder99")
	var response containerResp
	f.api(t, "GET", "/hubs", token, nil, &response)
	if response.MediaContainer.Hub[0].Size != 0 || response.MediaContainer.Hub[2].Size == 0 {
		t.Fatal("resume state crossed account boundary")
	}
	f.api(t, "PATCH", fmt.Sprintf("/sloplex/accounts/%d", bob.ID), testToken, map[string]any{"libraries": []int{}}, nil)
	response = containerResp{}
	f.api(t, "GET", "/hubs", token, nil, &response)
	for _, hub := range response.MediaContainer.Hub {
		if hub.Size != 0 || hub.HubKey != "" || bool(hub.More) {
			t.Fatalf("hidden library leaked hub data: %+v", hub)
		}
	}
	for _, path := range []string{fmt.Sprintf("/hubs/sections/%d", f.section), fmt.Sprintf("/hubs/sections/%d/continueWatching/items", f.section)} {
		if resp := f.api(t, "GET", path, token, nil, nil); resp.StatusCode != 404 {
			t.Fatalf("hidden section: %d", resp.StatusCode)
		}
	}
	if _, err := f.store.ApplyScan(f.section, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, hub := range f.getJSON(t, "/hubs").Hub {
		if hub.Size != 0 || hub.HubKey != "" || bool(hub.More) {
			t.Fatalf("unavailable media exposed: %+v", hub)
		}
	}
}

func TestHomeHubTVOnDeck(t *testing.T) {
	f := newTVFixture(t)
	shows := f.getJSON(t, "/library/sections/1/all").Metadata
	episodes := f.getJSON(t, "/library/metadata/"+shows[0].RatingKey+"/allLeaves").Metadata
	f.api(t, "GET", "/:/scrobble?ratingKey="+episodes[0].RatingKey, testToken, nil, nil)
	deck := f.getJSON(t, "/hubs/home/onDeck")
	if deck.Size != 1 || deck.Metadata[0].Type != "episode" {
		t.Fatalf("TV next episode: %+v", deck)
	}
	home := f.getJSON(t, "/hubs")
	if home.Hub[1].Size != 1 || home.Hub[3].Size == 0 {
		t.Fatalf("TV home hubs: %+v", home.Hub)
	}
}
