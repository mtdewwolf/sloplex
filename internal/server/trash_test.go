package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mtdewwolf/sloplex/internal/library"
)

func TestTrashRetainedStatePlaybackAndRevival(t *testing.T) {
	f := newSessionFixture(t) // fake ffmpeg: no real media integration dependency
	key := f.clip.Key
	original := f.getJSON(t, key).Metadata[0]
	c, err := f.store.CreateCollection(f.section, "Keep", []int{trashItemID(t, original.RatingKey)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var playlist struct{ MediaContainer MediaContainer }
	if resp := f.api(t, "POST", "/playlists?title=Keep&ids="+original.RatingKey, testToken, nil, &playlist); resp.StatusCode != 200 {
		t.Fatalf("playlist: %s", resp.Status)
	}
	pl := playlist.MediaContainer.Metadata[0]
	if resp := f.api(t, "PUT", "/:/progress?key="+original.RatingKey+"&time=8000", testToken, nil, nil); resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	playback := f.startHLS(t, testToken, "trash-test")
	part := original.Media[0].Part[0]
	if err := os.Remove(part.File); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(f.store, fakeProber{}, nil)
	if stats, err := sc.Scan(context.Background(), f.section); err != nil || stats.Unavailable != 1 || stats.Removed != 0 {
		t.Fatalf("scan: %+v, %v", stats, err)
	}
	unavailable := f.getJSON(t, key).Metadata[0]
	if !unavailable.SloplexUnavailable || !unavailable.Media[0].SloplexUnavailable || !unavailable.Media[0].Part[0].SloplexUnavailable || unavailable.ViewOffset != 8000 {
		t.Fatalf("retained metadata/state: %+v", unavailable)
	}
	if unavailable.RatingKey != original.RatingKey || unavailable.Media[0].ID != original.Media[0].ID || unavailable.Media[0].Part[0].ID != part.ID {
		t.Fatal("lost identity")
	}
	if got, err := f.store.Collection(c.ID); err != nil || len(got.ItemIDs) != 1 {
		t.Fatal("lost collection reference")
	}
	if got := f.getJSON(t, pl.Key); len(got.Metadata) != 1 || got.Metadata[0].RatingKey != original.RatingKey || !got.Metadata[0].SloplexUnavailable {
		t.Fatalf("playlist lost unavailable item: %+v", got)
	}
	// Even a returned file cannot play until an authoritative scan revives it.
	if err := os.WriteFile(part.File, []byte("restored data"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{part.Key, transcodeBase + "/decision?path=" + key, transcodeBase + "/start.m3u8?path=" + key, transcodeBase + "/start.mp4?path=" + key, transcodeBase + "/start.webm?path=" + key} {
		if resp := f.get(t, path, nil); resp.StatusCode != 404 {
			resp.Body.Close()
			t.Errorf("unavailable %s: %s", path, resp.Status)
		} else {
			resp.Body.Close()
		}
	}
	for _, st := range unavailable.Media[0].Part[0].Stream {
		if st.Key != "" {
			if resp := f.get(t, st.Key, nil); resp.StatusCode != 404 {
				resp.Body.Close()
				t.Errorf("unavailable subtitle: %s", resp.Status)
			} else {
				resp.Body.Close()
			}
		}
	}
	f.check(t, "unavailable session", playback, 404)
	if f.running() != 0 {
		t.Fatal("all-missing item's session not revoked on next delivery")
	}
	resp := f.get(t, key, map[string]string{"Accept": "application/xml"})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `sloplexUnavailable="1"`) {
		t.Fatalf("missing XML extension: %s", body)
	}
	if _, err := sc.Scan(context.Background(), f.section); err != nil {
		t.Fatal(err)
	}
	revived := f.getJSON(t, key).Metadata[0]
	if revived.SloplexUnavailable || revived.ViewOffset != 8000 || revived.Media[0].ID != original.Media[0].ID || revived.Media[0].Part[0].ID != part.ID {
		t.Fatalf("revival lost IDs/state: %+v", revived)
	}
	if len(revived.Media[0].Part[0].Stream) != len(original.Media[0].Part[0].Stream) {
		t.Fatal("lost known streams")
	}
	for i, st := range revived.Media[0].Part[0].Stream {
		if st.ID != original.Media[0].Part[0].Stream[i].ID {
			t.Fatal("stream ID churn on revival")
		}
	}
}

func TestTrashPurgeAuthorizationAndDiskSafety(t *testing.T) {
	f := newFixture(t, Options{})
	leaf := f.getJSON(t, fmt.Sprintf("/library/sections/%d/all", f.section)).Metadata[0]
	id := trashItemID(t, leaf.RatingKey)
	collection, err := f.store.CreateCollection(f.section, "Keep", []int{id}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Direct authoritative reconciliation avoids changing fixture files: the
	// purge must operate on the catalog snapshot and never delete those files.
	if _, err := f.store.ApplyScan(f.section, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.createUser(t, "reader", "password", false)
	token := f.login(t, "reader", "password")
	hidden := f.createUser(t, "hidden", "password", false)
	hiddenToken := f.login(t, "hidden", "password")
	if resp := f.api(t, "PATCH", fmt.Sprintf("/sloplex/accounts/%d", hidden.ID), testToken, map[string]any{"libraries": []int{}}, nil); resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	route := fmt.Sprintf("/library/sections/%d/emptyTrash", f.section)
	for _, tok := range []string{token, hiddenToken} {
		for _, path := range []string{route, "/library/sections/999999/emptyTrash"} {
			if resp := f.api(t, "PUT", path, tok, nil, nil); resp.StatusCode != 403 {
				t.Errorf("nonadmin purge %s: %s", path, resp.Status)
			}
		}
	}
	for _, path := range []string{leaf.Key, leaf.Media[0].Part[0].Key, transcodeBase + "/decision?path=" + leaf.Key} {
		if resp := f.api(t, "GET", path, hiddenToken, nil, nil); resp.StatusCode != 404 {
			t.Errorf("hidden unavailable source %s: %s", path, resp.Status)
		}
	}
	if resp := f.api(t, "PUT", route, "", nil, nil); resp.StatusCode != 401 {
		t.Errorf("anonymous purge: %s", resp.Status)
	}
	if resp := f.api(t, "GET", route, testToken, nil, nil); resp.StatusCode != 405 {
		t.Errorf("GET purge: %s", resp.Status)
	}
	if _, err := f.store.Item(id); err != nil {
		t.Fatal("unauthorized request changed catalog")
	}
	if resp := f.api(t, "PUT", "/library/sections/999999/emptyTrash", testToken, nil, nil); resp.StatusCode != 404 {
		t.Errorf("unknown section: %s", resp.Status)
	}
	if resp := f.api(t, "PUT", route, testToken, nil, nil); resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	if _, err := f.store.Item(id); err != library.ErrNotFound {
		t.Fatal("purge did not remove metadata")
	}
	if got, err := f.store.Collection(collection.ID); err != nil || len(got.ItemIDs) != 0 {
		t.Fatal("purge lost manual collection or failed to prune membership")
	}
	for _, m := range leaf.Media {
		for _, p := range m.Part {
			if _, err := os.Stat(p.File); err != nil {
				t.Errorf("purge modified disk: %v", err)
			}
		}
	}
	if resp := f.api(t, "PUT", route, testToken, nil, nil); resp.StatusCode != http.StatusOK {
		t.Fatal("repeat purge")
	}
}

func trashItemID(t *testing.T, v string) int {
	t.Helper()
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTrashMixedVersionPlayback(t *testing.T) {
	f := newSessionFixture(t)
	before := f.getJSON(t, f.clip.Key).Metadata[0]
	playback := f.startHLS(t, testToken, "missing-version")
	part := before.Media[0].Part[0]
	healthyFile := strings.TrimSuffix(part.File, ".mkv") + ".mp4"
	if err := os.WriteFile(healthyFile, []byte("healthy version"), 0644); err != nil {
		t.Fatal(err)
	}
	sc := library.NewScanner(f.store, fakeProber{}, nil)
	if _, err := sc.Scan(context.Background(), f.section); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(part.File); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(context.Background(), f.section); err != nil {
		t.Fatal(err)
	}
	mixed := f.getJSON(t, f.clip.Key).Metadata[0]
	if mixed.SloplexUnavailable || len(mixed.Media) != 2 || mixed.Media[0].SloplexUnavailable || !mixed.Media[1].SloplexUnavailable {
		t.Fatalf("mixed availability: %+v", mixed)
	}
	for _, path := range []string{mixed.Media[0].Part[0].Key, transcodeBase + "/decision?path=" + f.clip.Key} {
		resp := f.get(t, path, nil)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("healthy version %s: %s", path, resp.Status)
		}
	}
	for _, path := range []string{part.Key, transcodeBase + "/decision?path=" + f.clip.Key + "&mediaIndex=1", transcodeBase + "/start.m3u8?path=" + f.clip.Key + "&mediaIndex=1"} {
		resp := f.get(t, path, nil)
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("unavailable version %s: %s", path, resp.Status)
		}
	}
	healthyPlayback := f.startHLS(t, testToken, "healthy-version")
	f.check(t, "unavailable version session", playback, 404)
	f.check(t, "healthy version session", healthyPlayback, 200)
	if f.running() != 1 {
		t.Fatal("source revocation did not preserve exactly the healthy session")
	}
	if resp := f.api(t, "PUT", fmt.Sprintf("/library/sections/%d/emptyTrash", f.section), testToken, nil, nil); resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	purged := f.getJSON(t, f.clip.Key).Metadata[0]
	if len(purged.Media) != 1 || purged.Media[0].ID != mixed.Media[0].ID || purged.SloplexUnavailable {
		t.Fatal("version purge hid healthy version")
	}
}
