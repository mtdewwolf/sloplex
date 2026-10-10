package server

import (
	"net/http"
	"net/netip"
	"testing"
)

func TestStrictTrustedTokens(t *testing.T) {
	f := newFixture(t, Options{StrictTokenAuth: true, AllowedNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}})
	f.createUser(t, "alice", "wonderland", false)
	tok := f.login(t, "alice", "wonderland")
	var me accountView
	f.api(t, "GET", "/sloplex/account", tok, nil, &me)
	if me.Name != "alice" {
		t.Fatalf("valid token account = %+v", me)
	}
	f.api(t, "POST", "/sloplex/auth/logout", tok, nil, nil)
	for _, tc := range []struct {
		path, token string
		status      int
	}{
		{"/sloplex/account", tok, http.StatusUnauthorized},
		{"/library/sections", "invalid", http.StatusUnauthorized},
		{"/sloplex/account?X-Plex-Token=invalid", "", http.StatusUnauthorized},
		{"/library/sections", "", http.StatusOK},
		{"/sloplex/account", testToken, http.StatusOK},
		{"/identity", "invalid", http.StatusOK},
	} {
		req, err := http.NewRequest("GET", f.srv.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tc.token != "" {
			req.Header.Set(tokenHeader, tc.token)
		}
		resp, err := f.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("%s: got %d want %d", tc.path, resp.StatusCode, tc.status)
		}
	}
}
