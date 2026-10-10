package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/mtdewwolf/sloplex/internal/accounts"
)

func init() { accounts.Iterations = 1000 } // fast hashing in tests

// api sends a JSON request authenticated with token ("" for none) and
// decodes a JSON response into out (if non-nil). It returns the response.
func (f *fixture) api(t *testing.T, method, path, token string, body any, out any) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			buf, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(buf)
		}
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp
}

type loginResp struct {
	Account accountView `json:"account"`
	Token   tokenView   `json:"token"`
}

func (f *fixture) login(t *testing.T, user, pw string) string {
	t.Helper()
	var lr loginResp
	if resp := f.api(t, "POST", "/sloplex/auth/login", "", map[string]string{"username": user, "password": pw, "device": "test"}, &lr); resp.StatusCode != 200 {
		t.Fatalf("login %s: %s", user, resp.Status)
	}
	if lr.Token.Token == "" || !strings.EqualFold(lr.Account.Name, user) {
		t.Fatalf("login response = %+v", lr)
	}
	return lr.Token.Token
}

func (f *fixture) createUser(t *testing.T, name, pw string, admin bool) accountView {
	t.Helper()
	var a accountView
	body := map[string]any{"name": name, "admin": admin}
	if pw != "" {
		body["password"] = pw
	}
	if resp := f.api(t, "POST", "/sloplex/accounts", testToken, body, &a); resp.StatusCode != 201 {
		t.Fatalf("create %s: %s", name, resp.Status)
	}
	return a
}

func status(resp *http.Response) int { return resp.StatusCode }

func TestAccountsLifecycle(t *testing.T) {
	f := newFixture(t, Options{})
	alice := f.createUser(t, "alice", "wonderland", false)
	if alice.Admin || !alice.HasPassword {
		t.Fatalf("alice = %+v", alice)
	}
	tok := f.login(t, "Alice", "wonderland")

	var me accountView
	f.api(t, "GET", "/sloplex/account", tok, nil, &me)
	if me.ID != alice.ID || me.Name != "alice" {
		t.Errorf("me = %+v", me)
	}

	// Regular accounts browse and play but cannot administer.
	if code := status(f.api(t, "GET", "/library/sections", tok, nil, nil)); code != 200 {
		t.Errorf("alice browse = %d", code)
	}
	if code := status(f.api(t, "GET", "/library/sections/1/refresh", tok, nil, nil)); code != 403 {
		t.Errorf("alice refresh = %d", code)
	}
	if code := status(f.api(t, "GET", "/sloplex/accounts", tok, nil, nil)); code != 403 {
		t.Errorf("alice list accounts = %d", code)
	}
	if code := status(f.api(t, "GET", "/library/sections/1/refresh", testToken, nil, nil)); code != 200 {
		t.Errorf("owner refresh = %d", code)
	}

	// Duplicate names, bad input, and owner protections.
	if code := status(f.api(t, "POST", "/sloplex/accounts", testToken, map[string]any{"name": "ALICE"}, nil)); code != 409 {
		t.Errorf("duplicate = %d", code)
	}
	for _, body := range []any{`{"name":`, `{"name":"x","bogus":1}`, map[string]any{"name": "bad name"}, map[string]any{"name": "zed", "password": "short"}} {
		if code := status(f.api(t, "POST", "/sloplex/accounts", testToken, body, nil)); code != 400 {
			t.Errorf("create %v = %d, want 400", body, code)
		}
	}
	if code := status(f.api(t, "PATCH", "/sloplex/accounts/1", testToken, map[string]any{"admin": false}, nil)); code != 400 {
		t.Errorf("demote owner = %d", code)
	}
	if code := status(f.api(t, "DELETE", "/sloplex/accounts/1", testToken, nil, nil)); code != 400 {
		t.Errorf("delete owner = %d", code)
	}

	// Promote, then delete: the token stops working.
	var promoted accountView
	f.api(t, "PATCH", "/sloplex/accounts/"+itoa(alice.ID), testToken, map[string]any{"admin": true}, &promoted)
	if !promoted.Admin {
		t.Errorf("promote = %+v", promoted)
	}
	if code := status(f.api(t, "GET", "/sloplex/accounts", tok, nil, nil)); code != 200 {
		t.Errorf("promoted alice list = %d", code)
	}
	if code := status(f.api(t, "DELETE", "/sloplex/accounts/"+itoa(alice.ID), testToken, nil, nil)); code != 204 {
		t.Fatalf("delete alice = %d", code)
	}
	if code := status(f.api(t, "GET", "/library/sections", tok, nil, nil)); code != 401 {
		t.Errorf("deleted account's token = %d", code)
	}
	if code := status(f.api(t, "DELETE", "/sloplex/accounts/"+itoa(alice.ID), testToken, nil, nil)); code != 404 {
		t.Errorf("delete again = %d", code)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestPerAccountPlayState(t *testing.T) {
	f := newFixture(t, Options{})
	f.createUser(t, "alice", "wonderland", false)
	f.createUser(t, "bob", "builder1", false)
	alice, bob := f.login(t, "alice", "wonderland"), f.login(t, "bob", "builder1")

	heat := f.find(t, "/library/sections/1/all", "Heat")
	if code := status(f.api(t, "GET", "/:/scrobble?key="+heat.RatingKey, alice, nil, nil)); code != 200 {
		t.Fatalf("scrobble = %d", code)
	}
	f.api(t, "GET", "/:/timeline?time=60000&key="+heat.RatingKey, bob, nil, nil)

	view := func(tok string) Metadata {
		var body struct{ MediaContainer MediaContainer }
		f.api(t, "GET", heat.Key, tok, nil, &body)
		return body.MediaContainer.Metadata[0]
	}
	if m := view(alice); m.ViewCount != 1 || m.ViewOffset != 0 {
		t.Errorf("alice sees %+v", m)
	}
	if m := view(bob); m.ViewCount != 0 || m.ViewOffset != 60000 {
		t.Errorf("bob sees %+v", m)
	}
	if m := view(testToken); m.ViewCount != 0 || m.ViewOffset != 0 {
		t.Errorf("owner sees %+v", m)
	}
	var deck struct{ MediaContainer MediaContainer }
	f.api(t, "GET", "/library/onDeck", bob, nil, &deck)
	if deck.MediaContainer.Size != 1 {
		t.Errorf("bob onDeck size = %d", deck.MediaContainer.Size)
	}
	f.api(t, "GET", "/library/onDeck", alice, nil, &deck)
	if deck.MediaContainer.Size != 0 {
		t.Errorf("alice onDeck size = %d", deck.MediaContainer.Size)
	}
}

func TestTokensAndPasswords(t *testing.T) {
	f := newFixture(t, Options{})
	f.createUser(t, "alice", "wonderland", false)
	bob := f.createUser(t, "bob", "", false)
	laptop, phone := f.login(t, "alice", "wonderland"), f.login(t, "alice", "wonderland")

	var list struct{ Tokens []tokenView }
	f.api(t, "GET", "/sloplex/tokens", laptop, nil, &list)
	if len(list.Tokens) != 2 || list.Tokens[0].Token != "" {
		t.Fatalf("tokens = %+v", list.Tokens)
	}

	// Password change needs the current password and keeps only this token.
	if code := status(f.api(t, "PUT", "/sloplex/account/password", laptop, map[string]string{"current": "nope", "new": "looking-glass"}, nil)); code != 400 {
		t.Errorf("wrong current = %d", code)
	}
	if code := status(f.api(t, "PUT", "/sloplex/account/password", laptop, map[string]string{"current": "wonderland", "new": "looking-glass"}, nil)); code != 204 {
		t.Fatalf("change password = %d", code)
	}
	if code := status(f.api(t, "GET", "/sloplex/account", laptop, nil, nil)); code != 200 {
		t.Errorf("kept token = %d", code)
	}
	if code := status(f.api(t, "GET", "/sloplex/account", phone, nil, nil)); code != 401 {
		t.Errorf("other token after password change = %d", code)
	}
	f.login(t, "alice", "looking-glass")

	// Passwordless accounts use admin-issued device tokens.
	if code := status(f.api(t, "POST", "/sloplex/auth/login", "", map[string]string{"username": "bob", "password": ""}, nil)); code != 401 {
		t.Errorf("passwordless login = %d", code)
	}
	var issued tokenView
	if code := status(f.api(t, "POST", "/sloplex/accounts/"+itoa(bob.ID)+"/tokens", testToken, map[string]string{"device": "Kids Tablet"}, &issued)); code != 201 || issued.Token == "" {
		t.Fatalf("issue = %d %+v", code, issued)
	}
	var me accountView
	f.api(t, "GET", "/sloplex/account", issued.Token, nil, &me)
	if me.Name != "bob" {
		t.Errorf("issued token is %+v", me)
	}
	// Bob can set a first password without a current one.
	if code := status(f.api(t, "PUT", "/sloplex/account/password", issued.Token, map[string]string{"new": "builder1"}, nil)); code != 204 {
		t.Errorf("first password = %d", code)
	}

	// Revocation is scoped to the caller unless admin.
	if code := status(f.api(t, "DELETE", "/sloplex/tokens/"+itoa(issued.ID), laptop, nil, nil)); code != 404 {
		t.Errorf("alice revoking bob's token = %d", code)
	}
	if code := status(f.api(t, "DELETE", "/sloplex/tokens/"+itoa(issued.ID), testToken, nil, nil)); code != 204 {
		t.Errorf("admin revoke = %d", code)
	}
	if code := status(f.api(t, "GET", "/sloplex/account", issued.Token, nil, nil)); code != 401 {
		t.Errorf("revoked token = %d", code)
	}

	// Logout revokes the current token; master tokens cannot log out.
	if code := status(f.api(t, "POST", "/sloplex/auth/logout", laptop, nil, nil)); code != 204 {
		t.Errorf("logout = %d", code)
	}
	if code := status(f.api(t, "GET", "/sloplex/account", laptop, nil, nil)); code != 401 {
		t.Errorf("after logout = %d", code)
	}
	if code := status(f.api(t, "POST", "/sloplex/auth/logout", testToken, nil, nil)); code != 400 {
		t.Errorf("master logout = %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	f := newFixture(t, Options{})
	f.createUser(t, "alice", "wonderland", false)
	for i := 0; i < loginMaxFails; i++ {
		if code := status(f.api(t, "POST", "/sloplex/auth/login", "", map[string]string{"username": "alice", "password": "wrong"}, nil)); code != 401 {
			t.Fatalf("attempt %d = %d", i, code)
		}
	}
	resp := f.api(t, "POST", "/sloplex/auth/login", "", map[string]string{"username": "alice", "password": "wonderland"}, nil)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") == "" {
		t.Errorf("after %d failures = %s, Retry-After %q", loginMaxFails, resp.Status, resp.Header.Get("Retry-After"))
	}
}

func TestCompatAccountsAndTrustedNetwork(t *testing.T) {
	f := newFixture(t, Options{AllowedNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}})
	f.createUser(t, "alice", "wonderland", false)
	tok := f.login(t, "alice", "wonderland")

	var all, mine struct{ MediaContainer MediaContainer }
	f.api(t, "GET", "/accounts", testToken, nil, &all)
	f.api(t, "GET", "/accounts", tok, nil, &mine)
	if all.MediaContainer.Size != 2 || mine.MediaContainer.Size != 1 || mine.MediaContainer.Account[0].Name != "alice" {
		t.Errorf("/accounts owner=%+v alice=%+v", all.MediaContainer.Account, mine.MediaContainer.Account)
	}

	// A trusted network without a token is the owner; with a token it is
	// that token's account; an invalid token falls back to the owner.
	var me accountView
	f.api(t, "GET", "/sloplex/account", "", nil, &me)
	if me.ID != accounts.OwnerID {
		t.Errorf("trusted, no token = %+v", me)
	}
	f.api(t, "GET", "/sloplex/account", tok, nil, &me)
	if me.Name != "alice" {
		t.Errorf("trusted, alice token = %+v", me)
	}
	f.api(t, "GET", "/sloplex/account", "garbage", nil, &me)
	if me.ID != accounts.OwnerID {
		t.Errorf("trusted, bad token = %+v", me)
	}
}
