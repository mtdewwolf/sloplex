package server

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/accounts"
)

var loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}

// send sends a request with optional headers and returns the response, whose
// body is closed at the end of the test.
func (f *fixture) send(t *testing.T, method, path, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// accountOf returns the account /sloplex/account reports, or the status.
func (f *fixture) accountOf(t *testing.T, hdr map[string]string) (accountView, int) {
	t.Helper()
	resp := f.send(t, "GET", "/sloplex/account", "", hdr)
	var me accountView
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
			t.Fatal(err)
		}
	}
	return me, resp.StatusCode
}

// An explicit token that does not resolve is refused on every protected
// route, whatever the network; see "Failure" in docs/spec/auth.md.
func TestInvalidTokensNeverOwner(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		opts := Options{}
		if trusted {
			opts.AllowedNetworks = loopback
		}
		f := newFixture(t, opts)
		f.createUser(t, "alice", "wonderland", false)
		revoked := f.login(t, "alice", "wonderland")
		f.api(t, "POST", "/sloplex/auth/logout", revoked, nil, nil)
		valid := f.login(t, "alice", "wonderland")
		f.createUser(t, "gone", "wonderland", false)
		deleted := f.login(t, "gone", "wonderland")
		var list struct{ Accounts []accountView }
		f.api(t, "GET", "/sloplex/accounts", testToken, nil, &list)
		for _, a := range list.Accounts {
			if a.Name == "gone" {
				f.api(t, "DELETE", "/sloplex/accounts/"+strconv.Itoa(a.ID), testToken, nil, nil)
			}
		}

		bad := map[string]string{
			"revoked": revoked, "deleted": deleted, "unknown": strings.Repeat("ab", 24),
			"malformed": "not a token!", "master with suffix": testToken + "x",
		}
		for name, tok := range bad {
			for _, path := range []string{"/sloplex/account", "/library/sections", "/"} {
				if code := status(f.send(t, "GET", path, "", map[string]string{tokenHeader: tok})); code != http.StatusUnauthorized {
					t.Errorf("trusted=%v %s token, %s = %d, want 401", trusted, name, path, code)
				}
			}
			if code := status(f.send(t, "GET", "/library/sections?X-Plex-Token="+url.QueryEscape(tok), "", nil)); code != http.StatusUnauthorized {
				t.Errorf("trusted=%v %s query token = %d, want 401", trusted, name, code)
			}
			// Public routes ignore a bad token rather than failing.
			for _, path := range []string{"/identity", "/web/"} {
				if code := status(f.send(t, "GET", path, "", map[string]string{tokenHeader: tok})); code != http.StatusOK {
					t.Errorf("trusted=%v %s token, public %s = %d", trusted, name, path, code)
				}
			}
		}

		me, code := f.accountOf(t, map[string]string{tokenHeader: valid})
		if code != 200 || me.Name != "alice" {
			t.Errorf("trusted=%v valid token = %d %+v", trusted, code, me)
		}
		me, code = f.accountOf(t, map[string]string{tokenHeader: testToken})
		if code != 200 || me.ID != accounts.OwnerID {
			t.Errorf("trusted=%v master token = %d %+v", trusted, code, me)
		}
		me, code = f.accountOf(t, nil)
		if trusted && (code != 200 || me.ID != accounts.OwnerID) {
			t.Errorf("trusted, no token = %d %+v", code, me)
		}
		if !trusted && code != http.StatusUnauthorized {
			t.Errorf("untrusted, no token = %d", code)
		}
	}
}

// Tokenless trust is refused to pages of other origins, for reads, simple
// mutations and preflighted ones; see "Browser origins" in
// docs/spec/auth.md.
func TestTrustedNetworkOrigins(t *testing.T) {
	f := newFixture(t, Options{AllowedNetworks: loopback})
	self := f.srv.URL // http://127.0.0.1:port
	const evil = "http://evil.example"

	for _, tc := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"native client", nil, 200},
		{"same-origin fetch", map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": self}, 200},
		{"typed address", map[string]string{"Sec-Fetch-Site": "none"}, 200},
		{"old browser, same origin", map[string]string{"Origin": self}, 200},
		{"same origin, other case", map[string]string{"Origin": strings.ToUpper(self)}, 200},
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}, 401},
		{"cross-site image", map[string]string{"Sec-Fetch-Site": "cross-site"}, 401},
		{"same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, 401},
		{"unknown fetch site", map[string]string{"Sec-Fetch-Site": "elsewhere"}, 401},
		{"old browser, cross origin", map[string]string{"Origin": evil}, 401},
		{"opaque origin", map[string]string{"Origin": "null"}, 401},
		{"other port", map[string]string{"Origin": "http://127.0.0.1:1"}, 401},
		{"forwarded host", map[string]string{"Origin": evil, "X-Forwarded-Host": "evil.example", "Forwarded": "host=evil.example"}, 401},
		{"https origin, same host", map[string]string{"Origin": "https://" + strings.TrimPrefix(self, "http://"), "X-Forwarded-Proto": "https"}, 401},
		{"origin with path", map[string]string{"Origin": self + "/path"}, 401},
		{"origin with userinfo", map[string]string{"Origin": "http://user@" + strings.TrimPrefix(self, "http://")}, 401},
		{"origin with query", map[string]string{"Origin": self + "?query"}, 401},
	} {
		me, code := f.accountOf(t, tc.hdr)
		if code != tc.want || (code == 200 && me.ID != accounts.OwnerID) {
			t.Errorf("%s: %d %+v, want %d", tc.name, code, me, tc.want)
		}
	}
	if v := f.send(t, "GET", "/sloplex/account", "", nil).Header.Values("Vary"); !strings.Contains(strings.Join(v, ","), "Sec-Fetch-Site") {
		t.Errorf("tokenless trusted response Vary = %q", v)
	}

	crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": evil}
	// A simple GET mutation, as an <img> on another site would send it.
	clip := f.getJSON(t, "/library/sections/1/all").Metadata[0]
	scrobble := "/:/scrobble?identifier=com.plexapp.plugins.library&key=" + clip.RatingKey
	if code := status(f.send(t, "GET", scrobble, "", map[string]string{"Sec-Fetch-Site": "cross-site"})); code != 401 {
		t.Errorf("cross-site scrobble = %d", code)
	}
	if md := f.getJSON(t, clip.Key).Metadata[0]; md.ViewCount != 0 {
		t.Errorf("cross-site scrobble changed state: viewCount %d", md.ViewCount)
	}
	// A simple form POST that needs no preflight.
	form := map[string]string{"Content-Type": "text/plain", "Origin": evil}
	if code := status(f.send(t, "POST", "/sloplex/accounts", `{"name":"mallory","password":"wonderland","admin":true}`, form)); code != 401 {
		t.Errorf("cross-origin form POST = %d", code)
	}
	var list struct{ Accounts []accountView }
	f.api(t, "GET", "/sloplex/accounts", testToken, nil, &list)
	if len(list.Accounts) != 1 {
		t.Errorf("cross-origin POST created an account: %+v", list.Accounts)
	}

	// The preflight itself still succeeds, so clients with tokens keep
	// working from any origin; the tokenless request after it does not.
	pre := f.send(t, "OPTIONS", "/sloplex/accounts", "", map[string]string{
		"Origin": evil, "Access-Control-Request-Method": "POST",
		"Access-Control-Request-Headers": "content-type, x-plex-token",
	})
	if pre.StatusCode != 204 || pre.Header.Get("Access-Control-Allow-Origin") != "*" ||
		!strings.Contains(pre.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("preflight = %d %v", pre.StatusCode, pre.Header)
	}
	body := `{"name":"carol","password":"wonderland"}`
	json := map[string]string{"Content-Type": "application/json"}
	for k, v := range crossSite {
		json[k] = v
	}
	if code := status(f.send(t, "POST", "/sloplex/accounts", body, json)); code != 401 {
		t.Errorf("cross-origin tokenless JSON POST = %d", code)
	}
	json[tokenHeader] = testToken
	resp := f.send(t, "POST", "/sloplex/accounts", body, json)
	if resp.StatusCode != 201 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("cross-origin POST with token = %d %v", resp.StatusCode, resp.Header)
	}
	// Public routes stay public for other origins.
	if code := status(f.send(t, "GET", "/identity", "", crossSite)); code != 200 {
		t.Errorf("cross-site /identity = %d", code)
	}
}
