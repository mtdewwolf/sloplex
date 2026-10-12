package server

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPlayQueueLifecycle(t *testing.T) {
	f := newFixture(t, Options{})
	movies := f.getJSON(t, fmt.Sprintf("/library/sections/%d/all", f.section)).Metadata
	uri := url.QueryEscape("server://abc123/com.plexapp.plugins.library/library/metadata/" + movies[0].RatingKey + "," + movies[1].RatingKey + "," + movies[0].RatingKey)
	call := func(method, path string, want int) MediaContainer {
		t.Helper()
		var result containerResp
		if want == 200 {
			resp := f.api(t, method, path, testToken, nil, &result)
			if resp.StatusCode != want {
				t.Fatalf("%s %s: %d", method, path, resp.StatusCode)
			}
		} else if resp := f.api(t, method, path, testToken, nil, nil); resp.StatusCode != want {
			t.Fatalf("%s %s: %d, want %d", method, path, resp.StatusCode, want)
		}
		return result.MediaContainer
	}
	created := call("POST", "/playQueues?type=video&uri="+uri, 200)
	if created.PlayQueueInfo == nil || created.ID <= 0 || created.QueueVersion != 1 || created.TotalCount != 3 || created.Size != 3 {
		t.Fatalf("create: %+v", created)
	}
	first, second, duplicate := created.Metadata[0].PlayQueueItemID, created.Metadata[1].PlayQueueItemID, created.Metadata[2].PlayQueueItemID
	if first == 0 || first == duplicate || created.SelectedItemID != first || created.SelectedItemOffset != 0 {
		t.Fatalf("entry identity: %+v", created)
	}
	base := fmt.Sprintf("/playQueues/%d", created.ID)
	version := created.QueueVersion
	check := func(mc MediaContainer, order ...int) {
		t.Helper()
		version++
		if mc.QueueVersion != version || mc.Size != len(order) {
			t.Fatalf("version/size: %+v", mc)
		}
		for i, id := range order {
			if mc.Metadata[i].PlayQueueItemID != id {
				t.Fatalf("order: %+v", mc.Metadata)
			}
		}
	}
	moved := call("PUT", fmt.Sprintf("%s/items/%d/move", base, duplicate), 200)
	check(moved, duplicate, first, second)
	if moved.SelectedItemID != first || moved.SelectedItemOffset != 1 {
		t.Fatalf("move changed selection: %+v", moved)
	}
	// Invalid mutations must leave both ordering and version untouched.
	call("PUT", fmt.Sprintf("%s/items/%d/move?after=%d", base, first, first), 400)
	call("PUT", fmt.Sprintf("%s/items/%d/move?after=999999", base, first), 400)
	call("DELETE", base+"/items/999999", 404)
	call("PUT", base+"/shuffle?window=-1", 400)
	unchanged := call("GET", base, 200)
	if unchanged.QueueVersion != version {
		t.Fatal("failed mutation changed version")
	}
	shuffled := call("PUT", base+"/shuffle", 200)
	version++
	if !bool(shuffled.Shuffled) || shuffled.SelectedItemID != first || shuffled.QueueVersion != version {
		t.Fatalf("shuffle: %+v", shuffled)
	}
	restored := call("PUT", base+"/unshuffle", 200)
	check(restored, duplicate, first, second)
	removed := call("DELETE", fmt.Sprintf("%s/items/%d", base, first), 200)
	check(removed, duplicate, second)
	if removed.SelectedItemID != second {
		t.Fatalf("selected removal: %+v", removed)
	}
	reset := call("PUT", base+"/reset", 200)
	check(reset, duplicate, second)
	if reset.SelectedItemID != duplicate {
		t.Fatalf("reset: %+v", reset)
	}
	added := call("PUT", base+"?next=1&uri="+url.QueryEscape("/library/metadata/"+movies[2].RatingKey), 200)
	check(added, duplicate, added.Metadata[1].PlayQueueItemID, second)
	page := call("GET", base+"?window=0&center="+fmt.Sprint(second), 200)
	if page.Size != 0 || len(page.Metadata) != 0 || page.TotalCount != 3 || page.SelectedItemID != duplicate || page.SelectedItemOffset != 0 {
		t.Fatalf("window: %+v", page)
	}
	call("GET", base+"?window=-1", 400)
	call("GET", base+"?center=999999", 404)
	_, _, xml := f.raw(t, base, testToken)
	if !strings.Contains(string(xml), `playQueueID="`) || !strings.Contains(string(xml), `playQueueItemID="`) || !strings.Contains(string(xml), `playQueueSelectedItemOffset="0"`) || !strings.Contains(string(xml), `playQueueShuffled="0"`) {
		t.Fatalf("XML: %s", xml)
	}
	cleared := call("DELETE", base+"/items", 200)
	check(cleared, duplicate)
	if cleared.TotalCount != 1 || cleared.SelectedItemID != duplicate || cleared.SelectedItemOffset != 0 {
		t.Fatalf("clear: %+v", cleared)
	}
	empty := call("DELETE", fmt.Sprintf("%s/items/%d", base, duplicate), 200)
	check(empty)
	if empty.TotalCount != 0 || empty.SelectedItemID != 0 || empty.SelectedMetadataItemID != 0 {
		t.Fatalf("empty: %+v", empty)
	}
	call("GET", base, 200)
}

func TestPlayQueueManualRegion(t *testing.T) {
	f := newFixture(t, Options{})
	movies := f.getJSON(t, fmt.Sprintf("/library/sections/%d/all", f.section)).Metadata
	call := func(method, path string) MediaContainer {
		t.Helper()
		var result containerResp
		if resp := f.api(t, method, path, testToken, nil, &result); resp.StatusCode != 200 {
			t.Fatalf("%s %s: %d", method, path, resp.StatusCode)
		}
		return result.MediaContainer
	}
	source := func(ids ...string) string { return url.QueryEscape("/library/metadata/" + strings.Join(ids, ",")) }
	created := call("POST", "/playQueues?type=video&uri="+source(movies[0].RatingKey, movies[1].RatingKey, movies[0].RatingKey))
	base := fmt.Sprintf("/playQueues/%d", created.ID)
	first, second, third := created.Metadata[0].PlayQueueItemID, created.Metadata[1].PlayQueueItemID, created.Metadata[2].PlayQueueItemID
	check := func(mc MediaContainer, marker int, order ...int) {
		t.Helper()
		if mc.LastAddedItemID != marker || len(mc.Metadata) != len(order) {
			t.Fatalf("marker/size: %+v", mc)
		}
		for i, id := range order {
			if mc.Metadata[i].PlayQueueItemID != id {
				t.Fatalf("order: %+v", mc.Metadata)
			}
		}
	}
	added := call("PUT", base+"?uri="+source(movies[1].RatingKey))
	a := added.Metadata[1].PlayQueueItemID
	check(added, a, first, a, second, third)
	next := call("PUT", base+"?next=1&uri="+source(movies[0].RatingKey))
	b := next.Metadata[1].PlayQueueItemID
	check(next, a, first, b, a, second, third)
	batch := call("PUT", base+"?uri="+source(movies[1].RatingKey, movies[0].RatingKey))
	c, d := batch.Metadata[3].PlayQueueItemID, batch.Metadata[4].PlayQueueItemID
	check(batch, d, first, b, a, c, d, second, third)
	removed := call("DELETE", fmt.Sprintf("%s/items/%d", base, d))
	check(removed, c, first, b, a, c, second, third)
	_, _, xml := f.raw(t, base, testToken)
	if !strings.Contains(string(xml), fmt.Sprintf(`playQueueLastAddedItemID="%d"`, c)) {
		t.Fatalf("XML marker: %s", xml)
	}
	// Advancing beyond the manual region retires its marker without a version change.
	if resp := f.api(t, "POST", fmt.Sprintf("/:/timeline?ratingKey=%s&playQueueItemID=%d&state=playing&X-Plex-Client-Identifier=region-test", movies[1].RatingKey, second), testToken, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("timeline: %d", resp.StatusCode)
	}
	selected := call("GET", base)
	check(selected, 0, first, b, a, c, second, third)
	if selected.QueueVersion != removed.QueueVersion {
		t.Fatal("timeline advanced version")
	}
	tail := call("PUT", base+"?next=1&uri="+source(movies[0].RatingKey))
	e := tail.Metadata[5].PlayQueueItemID
	check(tail, e, first, b, a, c, second, e, third)
	cleared := call("DELETE", base+"/items")
	check(cleared, 0, second)

}

func TestPlayQueueWindowContract(t *testing.T) {
	f := newFixture(t, Options{})
	movies := f.getJSON(t, fmt.Sprintf("/library/sections/%d/all", f.section)).Metadata
	ids := []string{movies[0].RatingKey, movies[1].RatingKey, movies[0].RatingKey, movies[1].RatingKey, movies[0].RatingKey, movies[1].RatingKey, movies[0].RatingKey}
	var response containerResp
	f.api(t, "POST", "/playQueues?type=video&uri="+url.QueryEscape("/library/metadata/"+strings.Join(ids, ",")), testToken, nil, &response)
	created := response.MediaContainer
	base := fmt.Sprintf("/playQueues/%d?center=%d", created.ID, created.Metadata[3].PlayQueueItemID)
	for _, tc := range []struct {
		query      string
		start, end int
	}{
		{"&window=0", 3, 3}, {"&window=1", 2, 5}, {"&window=2", 1, 6}, {"&window=3", 0, 7}, {"&window=4", 0, 7},
		{"&window=1&includeBefore=0", 4, 5}, {"&window=1&includeAfter=0", 2, 3},
		{"&window=0&includeBefore=0", 3, 3}, {"&window=0&includeAfter=0", 3, 3},
	} {
		t.Run(tc.query, func(t *testing.T) {
			mc := f.getJSON(t, base+tc.query)
			if len(mc.Metadata) != tc.end-tc.start || mc.TotalCount != 7 || mc.SelectedItemID != created.SelectedItemID || mc.SelectedItemOffset != 0 || mc.QueueVersion != 1 {
				t.Fatalf("window: %+v", mc)
			}
			for i, md := range mc.Metadata {
				if md.PlayQueueItemID != created.Metadata[tc.start+i].PlayQueueItemID {
					t.Fatalf("window order: %+v", mc.Metadata)
				}
			}
		})
	}
	if resp := f.api(t, "GET", base+"&includeBefore=0&includeAfter=0", testToken, nil, nil); resp.StatusCode != 400 {
		t.Fatalf("both excluded: %d", resp.StatusCode)
	}
}

func TestPlayQueueSourcesAndAuthorization(t *testing.T) {
	f := newTVFixture(t)
	shows := f.getJSON(t, "/library/sections/1/all").Metadata
	source := url.QueryEscape("/library/metadata/" + shows[0].RatingKey)
	var created containerResp
	f.api(t, "POST", "/playQueues?type=video&uri="+source, testToken, nil, &created)
	queue := created.MediaContainer
	base := fmt.Sprintf("/playQueues/%d", queue.ID)
	if queue.Size != 4 {
		t.Fatalf("show expansion: %+v", queue)
	}
	bob := f.createUser(t, "bob", "builder99", false)
	token := f.login(t, "bob", "builder99")
	for _, op := range []struct{ method, path string }{{"GET", base}, {"PUT", base + "?uri=" + source}, {"DELETE", base + "/items"}, {"PUT", base + "/reset"}, {"PUT", base + "/shuffle"}, {"PUT", base + "/unshuffle"}, {"DELETE", base + "/items/" + fmt.Sprint(queue.Metadata[0].PlayQueueItemID)}, {"PUT", base + "/items/" + fmt.Sprint(queue.Metadata[0].PlayQueueItemID) + "/move"}} {
		if resp := f.api(t, op.method, op.path, token, nil, nil); resp.StatusCode != 404 {
			t.Errorf("other account: %s %s returned %d", op.method, op.path, resp.StatusCode)
		}
	}
	// Administrative privileges do not grant another account's queue ownership.
	f.createUser(t, "admin2", "builder99", true)
	adminToken := f.login(t, "admin2", "builder99")
	if resp := f.api(t, "GET", base, adminToken, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("administrator can read another account's queue: %d", resp.StatusCode)
	}
	// Create from a caller-owned playlist with duplicated entries.
	ep := queue.Metadata[0].RatingKey
	var playlist, fromPlaylist containerResp
	f.api(t, "POST", "/playlists?title=Duplicates&ids="+ep+","+ep, token, nil, &playlist)
	f.api(t, "POST", "/playQueues?type=video&playlistID="+playlist.MediaContainer.Metadata[0].RatingKey, token, nil, &fromPlaylist)
	if fromPlaylist.MediaContainer.Size != 2 || fromPlaylist.MediaContainer.Metadata[0].PlayQueueItemID == fromPlaylist.MediaContainer.Metadata[1].PlayQueueItemID {
		t.Fatal("playlist entries not preserved")
	}
	owned := fmt.Sprintf("/playQueues/%d", fromPlaylist.MediaContainer.ID)
	f.api(t, "PUT", owned+"?uri="+url.QueryEscape("/library/metadata/"+ep), token, nil, &fromPlaylist)
	if fromPlaylist.MediaContainer.LastAddedItemID == 0 {
		t.Fatal("missing manual marker")
	}
	f.api(t, "PATCH", fmt.Sprintf("/sloplex/accounts/%d", bob.ID), testToken, map[string]any{"libraries": []int{}}, nil)
	var hidden containerResp
	f.api(t, "GET", owned, token, nil, &hidden)
	if hidden.MediaContainer.Size != 0 || hidden.MediaContainer.TotalCount != 0 || hidden.MediaContainer.SelectedItemID != 0 || hidden.MediaContainer.SelectedMetadataItemID != 0 || hidden.MediaContainer.LastAddedItemID != 0 {
		t.Fatalf("revoked library leaked metadata: %+v", hidden)
	}
	if resp := f.api(t, "PUT", owned+"?uri="+url.QueryEscape("/library/metadata/"+ep), token, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("adding revoked item: %d", resp.StatusCode)
	}
	for _, suffix := range []string{"type=audio&uri=" + source, "type=video", "type=video&uri=" + source + "&playlistID=1", "type=video&uri=" + source + "&repeat=1", "type=video&uri=" + source + "&continuous=1", "type=video&uri=" + source + "&onDeck=1", "type=video&uri=" + source + "&shuffle=2", "type=video&uri=" + source + "&key=/library/metadata/999999", "type=video&uri=" + source + "&type=video", "type=video&uri=%ZZ", "type=video;uri=" + source, "type=video&uri=" + url.QueryEscape("https://example.invalid/library/metadata/1")} {
		if resp := f.api(t, "POST", "/playQueues?"+suffix, testToken, nil, nil); resp.StatusCode != 400 {
			t.Errorf("invalid request %s: %d", suffix, resp.StatusCode)
		}
	}
	// Encoded library:// URIs are decoded exactly once at each URL layer.
	sec := f.store.Sections()[0]
	var libraryURI containerResp
	encoded := url.QueryEscape("library://" + sec.UUID + "/item/" + url.PathEscape("/library/metadata/"+ep))
	if resp := f.api(t, "POST", "/playQueues?type=video&uri="+encoded, testToken, nil, &libraryURI); resp.StatusCode != 200 || libraryURI.MediaContainer.Size != 1 {
		t.Fatal("library URI not resolved")
	}
}

func TestPlayQueueConcurrentAdds(t *testing.T) {
	f := newFixture(t, Options{})
	movie := f.getJSON(t, fmt.Sprintf("/library/sections/%d/all", f.section)).Metadata[0]
	source := url.QueryEscape("/library/metadata/" + movie.RatingKey)
	var created containerResp
	f.api(t, "POST", "/playQueues?type=video&uri="+source, testToken, nil, &created)
	base := fmt.Sprintf("/playQueues/%d", created.MediaContainer.ID)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if resp := f.api(t, "PUT", base+"?uri="+source, testToken, nil, nil); resp.StatusCode != 200 {
				t.Errorf("add: %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	result := f.getJSON(t, base)
	seen := map[int]bool{}
	if result.QueueVersion != 13 || result.Size != 13 {
		t.Fatalf("lost mutation: %+v", result)
	}
	for _, md := range result.Metadata {
		if seen[md.PlayQueueItemID] {
			t.Fatal("duplicate queue entry ID")
		}
		seen[md.PlayQueueItemID] = true
	}
}

func TestPlayQueueResourceLimits(t *testing.T) {
	store := newQueueStore()
	first, err := store.create(1, "", []int{7}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for range maxAccountQueues {
		if _, err := store.create(1, "", []int{7}, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.edit(1, first.ID, nil); !errors.Is(err, errQueueMissing) {
		t.Fatal("oldest queue was not evicted")
	}
	if len(store.queues) != maxAccountQueues {
		t.Fatal("account queue capacity exceeded")
	}
	if _, err := store.create(1, "", make([]int, maxQueueItems+1), 0, false); !errors.Is(err, errQueueLimit) {
		t.Fatal("oversized queue accepted")
	}
	if len(store.queues) != maxAccountQueues {
		t.Fatal("failed creation evicted a queue")
	}
	expired, err := store.create(2, "", []int{8}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	q := store.queues[expired.ID]
	q.Touched = time.Now().Add(-queueIdleLifetime)
	store.queues[q.ID] = q
	if _, err := store.edit(2, expired.ID, nil); !errors.Is(err, errQueueMissing) {
		t.Fatal("expired queue remained readable")
	}
	for owner := 3; owner < 3+maxServerQueues; owner++ {
		if _, err := store.create(owner, "", []int{owner}, 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.queues) != maxServerQueues {
		t.Fatal("server queue capacity exceeded")
	}
}

func TestPlayQueueTimelineSelection(t *testing.T) {
	f := newFixture(t, Options{})
	movies := f.getJSON(t, fmt.Sprintf("/library/sections/%d/all", f.section)).Metadata
	uri := url.QueryEscape("/library/metadata/" + movies[0].RatingKey + "," + movies[1].RatingKey + "," + movies[0].RatingKey)
	var created containerResp
	f.api(t, "POST", "/playQueues?type=video&uri="+uri, testToken, nil, &created)
	queue := created.MediaContainer
	base := fmt.Sprintf("/playQueues/%d", queue.ID)
	entry := queue.Metadata[2].PlayQueueItemID
	// The shipped client reports the occurrence ID without a queue ID.
	path := fmt.Sprintf("/:/timeline?ratingKey=%s&playQueueItemID=%d&state=playing&time=1000", movies[0].RatingKey, entry)
	if resp := f.api(t, "POST", path+"&X-Plex-Client-Identifier=queue-test", testToken, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("timeline: %d", resp.StatusCode)
	}
	selected := f.getJSON(t, base)
	if selected.SelectedItemID != entry || selected.SelectedItemOffset != 2 || selected.QueueVersion != 1 {
		t.Fatalf("selection: %+v", selected)
	}
	if md := f.getJSON(t, movies[0].Key).Metadata[0]; md.ViewOffset != 1000 {
		t.Fatalf("playstate not saved: %+v", md)
	}
	// Ordinary position updates on the same occurrence must not churn versions.
	if resp := f.api(t, "GET", strings.Replace(path, "time=1000", "time=2000", 1), testToken, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("repeat timeline: %d", resp.StatusCode)
	}
	if selected = f.getJSON(t, base); selected.QueueVersion != 1 {
		t.Fatal("same selection advanced queue version")
	}
	invalid := []struct {
		path string
		want int
	}{
		{fmt.Sprintf("/:/timeline?ratingKey=%s&playQueueItemID=%d&time=3000", movies[1].RatingKey, entry), 400},
		{path + "&playQueueID=999999", 404},
		{path + "&playQueueItemID=1", 400},
		{strings.Replace(path, "time=1000", "time=broken", 1), 400},
		{strings.Replace(path, "state=playing", "state=invalid", 1), 400},
		{"/:/timeline?ratingKey=" + movies[0].RatingKey + "&playQueueID=1", 400},
		{strings.Replace(path, fmt.Sprintf("playQueueItemID=%d", entry), "playQueueItemID=999999", 1), 404},
	}
	for _, tc := range invalid {
		if resp := f.api(t, "GET", tc.path, testToken, nil, nil); resp.StatusCode != tc.want {
			t.Errorf("%s: %d want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
	if selected = f.getJSON(t, base); selected.SelectedItemID != entry || selected.QueueVersion != 1 {
		t.Fatal("invalid timeline mutated selection")
	}
	if md := f.getJSON(t, movies[1].Key).Metadata[0]; md.ViewOffset != 0 {
		t.Fatal("mismatched entry mutated watch history")
	}
	f.createUser(t, "other", "builder99", true)
	other := f.login(t, "other", "builder99")
	if resp := f.api(t, "PUT", path, other, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("other account timeline: %d", resp.StatusCode)
	}
	first := queue.Metadata[0].PlayQueueItemID
	if resp := f.api(t, "PUT", strings.Replace(path, fmt.Sprint(entry), fmt.Sprint(first), 1)+"&playQueueID="+fmt.Sprint(queue.ID), testToken, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("explicit queue timeline: %d", resp.StatusCode)
	}
	if selected = f.getJSON(t, base); selected.SelectedItemID != first || selected.QueueVersion != 1 {
		t.Fatal("selection back to first occurrence failed")
	}
}

func TestPlayQueueTimelineSaveFailure(t *testing.T) {
	store := newQueueStore()
	q, err := store.create(1, "", []int{7, 8}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	err = store.commitTimeline(1, q.ID, q.Entries[1].ID, 8, func() bool { return false })
	if !errors.Is(err, errTimelineSave) {
		t.Fatal("failed save reported success")
	}
	actual, err := store.edit(1, q.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Selected != q.Selected || actual.Version != q.Version {
		t.Fatal("failed playstate save changed queue")
	}
}
