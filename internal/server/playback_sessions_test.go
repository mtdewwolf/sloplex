package server

import (
	"fmt"
	"testing"
)

type sessionRow struct {
	RatingKey, SessionKey, State, Client string
	Offset                               int64
}

func (f *fixture) sessions(t *testing.T) []sessionRow {
	t.Helper()
	var rows []sessionRow
	for _, md := range f.getJSON(t, "/status/sessions").Metadata {
		if md.Player == nil || md.User == nil || len(md.Media) != 1 || len(md.Media[0].Part) != 1 {
			t.Fatalf("session %s lacks player, user or a single part: %+v", md.SessionKey, md)
		}
		rows = append(rows, sessionRow{md.RatingKey, md.SessionKey, md.Player.State, md.Player.MachineIdentifier, md.ViewOffset})
	}
	return rows
}

// TestPlaybackSessionIdentity replays the identity sequence observed on PMS
// 1.43.4 (docs/evidence/plex-1.43.4-sessions.json).
func TestPlaybackSessionIdentity(t *testing.T) {
	f := newFixture(t, Options{})
	alien := f.find(t, "/library/sections/1/all", "Alien").RatingKey
	heat := f.find(t, "/library/sections/1/all", "Heat").RatingKey
	report := func(method, key, state, time string, hdr map[string]string, extra string) {
		t.Helper()
		path := fmt.Sprintf("/:/timeline?ratingKey=%s&state=%s&duration=4000%s", key, state, extra)
		if time != "" {
			path += "&time=" + time
		}
		if code, _, body := f.wire(t, method, path, testToken, hdr); code != 200 {
			t.Fatalf("%s %s = %d %q", method, path, code, body)
		}
	}
	expect := func(label string, want ...sessionRow) {
		t.Helper()
		got := f.sessions(t)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s:\n got %v\nwant %v", label, got, want)
		}
	}
	probe := map[string]string{"X-Plex-Client-Identifier": "probe"}
	expect("initial")

	report("POST", alien, "playing", "1000", probe, "")
	expect("client-keyed", sessionRow{alien, "1", "playing", "probe", 1000})
	// Query-string session parameters do not create a separate identity.
	report("POST", alien, "playing", "1500", probe, "&session=s1&sessionIdentifier=s1")
	expect("query session ignored", sessionRow{alien, "1", "playing", "probe", 1500})

	h1 := map[string]string{"X-Plex-Client-Identifier": "probe", "X-Plex-Session-Identifier": "h1"}
	h2 := map[string]string{"X-Plex-Client-Identifier": "probe", "X-Plex-Session-Identifier": "h2"}
	report("GET", alien, "playing", "2000", h1, "")
	report("GET", heat, "playing", "1200", h2, "")
	expect("header sessions",
		sessionRow{alien, "1", "playing", "probe", 1500},
		sessionRow{alien, "2", "playing", "probe", 2000},
		sessionRow{heat, "3", "playing", "probe", 1200})

	// The session header owns the identity: another client and item replace it.
	second := map[string]string{"X-Plex-Client-Identifier": "second", "X-Plex-Session-Identifier": "h2"}
	report("GET", alien, "paused", "1300", second, "")
	expect("replaced",
		sessionRow{alien, "1", "playing", "probe", 1500},
		sessionRow{alien, "2", "playing", "probe", 2000},
		sessionRow{alien, "3", "paused", "second", 1300})
	report("GET", alien, "stopped", "", second, "")
	expect("stopped without time",
		sessionRow{alien, "1", "playing", "probe", 1500},
		sessionRow{alien, "2", "playing", "probe", 2000})

	for _, state := range []string{"paused", "buffering"} {
		report("POST", alien, state, "1800", probe, "")
		expect(state,
			sessionRow{alien, "1", state, "probe", 1800},
			sessionRow{alien, "2", "playing", "probe", 2000})
	}
	report("POST", alien, "stopped", "1800", probe, "")
	expect("stopped", sessionRow{alien, "2", "playing", "probe", 2000})

	// Reports without a client identifier or known state create no session.
	report("GET", heat, "whatever", "100", h1, "")
	report("GET", heat, "playing", "100", nil, "")
	expect("untracked", sessionRow{alien, "2", "playing", "probe", 2000})
}

func TestPlaybackSessionAccess(t *testing.T) {
	f := newFixture(t, Options{})
	alien := f.find(t, "/library/sections/1/all", "Alien").RatingKey
	f.createUser(t, "viewer", "builder99", false)
	viewer := f.login(t, "viewer", "builder99")
	if code, _, _ := f.wire(t, "POST", "/:/timeline?state=playing&time=10&ratingKey="+alien, viewer, clientID); code != 200 {
		t.Fatalf("viewer timeline: %d", code)
	}
	if code, _, _ := f.wire(t, "GET", "/status/sessions", viewer, nil); code != 403 {
		t.Fatalf("non-admin sessions: %d", code)
	}
	rows := f.getJSON(t, "/status/sessions").Metadata
	if len(rows) != 1 || rows[0].User.Title != "viewer" || rows[0].Player.UserID != rows[0].User.ID {
		t.Fatalf("viewer session: %+v", rows)
	}
	for _, path := range []string{"&mediaIndex=1", "&partIndex=1", "&mediaIndex=-1", "&time=-5"} {
		if code, _, _ := f.wire(t, "POST", "/:/timeline?state=playing&ratingKey="+alien+path, testToken, clientID); code != 400 {
			t.Errorf("%s accepted: %d", path, code)
		}
	}
	if n := len(f.getJSON(t, "/status/sessions").Metadata); n != 1 {
		t.Fatalf("rejected reports created sessions: %d", n)
	}
}
