package server

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestLibraryRestrictions checks that a restricted account cannot reach a
// hidden library through any endpoint (docs/spec/accounts.md, "Library
// access").
func TestLibraryRestrictions(t *testing.T) {
	f, _ := newArtFixture(t) // section 1: Movies (Heat), section 2: TV (Lost)

	// Look up hidden content as the owner.
	var movies struct{ MediaContainer MediaContainer }
	f.api(t, "GET", "/library/sections/1/all", testToken, nil, &movies)
	heat := movies.MediaContainer.Metadata[0]
	heatPart := heat.Media[0].Part[0].Key

	var kid accountView
	if resp := f.api(t, "POST", "/sloplex/accounts", testToken,
		map[string]any{"name": "kid", "password": "cartoons1", "libraries": []int{2}}, &kid); resp.StatusCode != 201 {
		t.Fatalf("create kid: %s", resp.Status)
	}
	if kid.Libraries == nil || len(*kid.Libraries) != 1 {
		t.Fatalf("kid = %+v", kid)
	}
	tok := f.login(t, "kid", "cartoons1")

	get := func(path string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", f.srv.URL+path, nil)
		req.Header.Set(tokenHeader, tok)
		req.Header.Set("Accept", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Lists leave hidden content out.
	var secs struct{ MediaContainer MediaContainer }
	f.api(t, "GET", "/library/sections", tok, nil, &secs)
	if secs.MediaContainer.Size != 1 || secs.MediaContainer.Directory[0].Key != "2" {
		t.Errorf("sections = %+v", secs.MediaContainer.Directory)
	}
	var ra struct{ MediaContainer MediaContainer }
	f.api(t, "GET", "/library/recentlyAdded", tok, nil, &ra)
	for _, m := range ra.MediaContainer.Metadata {
		if m.LibrarySectionID != 2 {
			t.Errorf("recentlyAdded leaked %q from section %d", m.Title, m.LibrarySectionID)
		}
	}
	if ra.MediaContainer.Size != 2 {
		t.Errorf("recentlyAdded size = %d, want the 2 episodes", ra.MediaContainer.Size)
	}

	// Every direct route to hidden content is a 404 that looks like a
	// missing item.
	_, missingBody := get("/library/metadata/999999")
	thumbURL := url.QueryEscape(heat.Thumb)
	for _, path := range []string{
		"/library/sections/1",
		"/library/sections/1/all",
		"/library/sections/1/recentlyAdded",
		"/library/sections/1/onDeck",
		"/library/sections/1/refresh",
		heat.Key,
		heat.Key + "/children",
		heat.Key + "/allLeaves",
		heat.Thumb,
		heatPart,
		"/photo/:/transcode?width=50&height=50&url=" + thumbURL,
		"/:/scrobble?key=" + heat.RatingKey,
		"/:/unscrobble?key=" + heat.RatingKey,
		"/:/progress?time=5&key=" + heat.RatingKey,
		"/:/timeline?time=5&key=" + heat.RatingKey,
	} {
		code, body := get(path)
		if path == "/library/sections/1/refresh" {
			if code != 403 {
				t.Errorf("GET %s = %d, want 403 (admin only)", path, code)
			}
			continue
		}
		if code != 404 {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
		if strings.HasPrefix(path, "/library/metadata/") && body != missingBody {
			t.Errorf("GET %s body %q differs from a missing item's %q", path, body, missingBody)
		}
	}

	// The allowed library works normally.
	var shows struct{ MediaContainer MediaContainer }
	f.api(t, "GET", "/library/sections/2/all?type=4", tok, nil, &shows)
	ep := shows.MediaContainer.Metadata[0]
	for _, path := range []string{ep.Key, ep.Media[0].Part[0].Key, ep.GrandparentThumb, "/:/scrobble?key=" + ep.RatingKey} {
		if code, _ := get(path); code != 200 {
			t.Errorf("allowed GET %s = %d", path, code)
		}
	}

	// Lifting the restriction restores access; unknown sections are refused.
	if resp := f.api(t, "PATCH", "/sloplex/accounts/"+itoa(kid.ID), testToken, map[string]any{"libraries": []int{99}}, nil); resp.StatusCode != 400 {
		t.Errorf("unknown section = %s", resp.Status)
	}
	if resp := f.api(t, "PATCH", "/sloplex/accounts/"+itoa(kid.ID), testToken, map[string]any{"name": "kiddo"}, &kid); resp.StatusCode != 200 || kid.Libraries == nil {
		t.Errorf("rename dropped restriction: %+v", kid)
	}
	if resp := f.api(t, "PATCH", "/sloplex/accounts/"+itoa(kid.ID), testToken, `{"libraries":null}`, &kid); resp.StatusCode != 200 || kid.Libraries != nil {
		t.Errorf("lift restriction = %s %+v", resp.Status, kid)
	}
	if code, _ := get(heat.Key); code != 200 {
		t.Errorf("after lifting, heat = %d", code)
	}

	// An empty list hides everything; admins ignore their list.
	f.api(t, "PATCH", "/sloplex/accounts/"+itoa(kid.ID), testToken, map[string]any{"libraries": []int{}}, nil)
	f.api(t, "GET", "/library/sections", tok, nil, &secs)
	if secs.MediaContainer.Size != 0 {
		t.Errorf("empty list sees %d sections", secs.MediaContainer.Size)
	}
	f.api(t, "PATCH", "/sloplex/accounts/"+itoa(kid.ID), testToken, map[string]any{"admin": true}, nil)
	f.api(t, "GET", "/library/sections", tok, nil, &secs)
	if secs.MediaContainer.Size != 2 {
		t.Errorf("admin with restriction sees %d sections", secs.MediaContainer.Size)
	}
}
