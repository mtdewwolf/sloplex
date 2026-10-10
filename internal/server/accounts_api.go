package server

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mtdewwolf/sloplex/internal/accounts"
)

// The JSON account API is described in docs/spec/accounts.md.

func (s *Server) accountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /sloplex/auth/login", s.handleLogin)
	mux.HandleFunc("POST /sloplex/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /sloplex/account", s.handleMe)
	mux.HandleFunc("PUT /sloplex/account/password", s.handleChangePassword)
	mux.HandleFunc("GET /sloplex/tokens", s.handleMyTokens)
	mux.HandleFunc("DELETE /sloplex/tokens/{id}", s.handleRevokeToken)

	mux.HandleFunc("GET /sloplex/accounts", s.requireAdmin(s.handleListAccounts))
	mux.HandleFunc("POST /sloplex/accounts", s.requireAdmin(s.handleCreateAccount))
	mux.HandleFunc("PATCH /sloplex/accounts/{id}", s.requireAdmin(s.handleUpdateAccount))
	mux.HandleFunc("DELETE /sloplex/accounts/{id}", s.requireAdmin(s.handleDeleteAccount))
	mux.HandleFunc("GET /sloplex/accounts/{id}/tokens", s.requireAdmin(s.handleAccountTokens))
	mux.HandleFunc("POST /sloplex/accounts/{id}/tokens", s.requireAdmin(s.handleIssueToken))

	mux.HandleFunc("GET /accounts", s.handleCompatAccounts)
}

type accountView struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Admin       bool   `json:"admin"`
	Libraries   *[]int `json:"libraries"`
	HasPassword bool   `json:"hasPassword"`
	CreatedAt   int64  `json:"createdAt"`
}

func viewAccount(a accounts.Account) accountView {
	return accountView{ID: a.ID, Name: a.Name, Admin: a.Admin, Libraries: a.Libraries, HasPassword: a.HasPassword(), CreatedAt: a.CreatedAt}
}

// parseLibraries decodes a "libraries" field: absent (raw == nil) leaves
// the setting alone, null means every library, and a list must name
// existing sections.
func (s *Server) parseLibraries(raw json.RawMessage) (set bool, libs *[]int, err error) {
	if raw == nil {
		return false, nil, nil
	}
	if string(raw) == "null" {
		return true, nil, nil
	}
	var ids []int
	if err := json.Unmarshal(raw, &ids); err != nil {
		return false, nil, fmt.Errorf("libraries must be null or a list of section IDs")
	}
	for _, id := range ids {
		if _, err := s.store.Section(id); err != nil {
			return false, nil, fmt.Errorf("unknown library section %d", id)
		}
	}
	return true, &ids, nil
}

type tokenView struct {
	ID        int    `json:"id"`
	Device    string `json:"device"`
	CreatedAt int64  `json:"createdAt"`
	Token     string `json:"token,omitempty"`
}

func viewTokens(ts []accounts.Token) []tokenView {
	out := make([]tokenView, len(ts))
	for i, t := range ts {
		out[i] = tokenView{ID: t.ID, Device: t.Device, CreatedAt: t.CreatedAt}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

const maxJSONBody = 64 << 10

// readJSON decodes the request body into v, writing a 400 on failure.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// storeError maps an accounts error to a response.
func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		jsonError(w, http.StatusNotFound, "not found")
	case errors.Is(err, accounts.ErrExists):
		jsonError(w, http.StatusConflict, err.Error())
	case errors.Is(err, accounts.ErrInvalid), errors.Is(err, accounts.ErrOwner):
		jsonError(w, http.StatusBadRequest, err.Error())
	default:
		s.log.Error("accounts", "err", err)
		jsonError(w, http.StatusInternalServerError, "internal error")
	}
}

// caller returns the authenticated account, writing a 401 if there is none.
func (s *Server) caller(w http.ResponseWriter, r *http.Request) (accounts.Account, principal, bool) {
	p, ok := principalOf(r)
	if ok {
		if a, err := s.accounts.Account(p.AccountID); err == nil {
			return a, p, true
		}
	}
	jsonError(w, http.StatusUnauthorized, "not authenticated")
	return accounts.Account{}, principal{}, false
}

// visibility returns which library sections the request's account may see;
// see "Library access" in docs/spec/accounts.md. Requests without an account
// see nothing.
func (s *Server) visibility(r *http.Request) func(sectionID int) bool {
	if p, ok := principalOf(r); ok {
		if a, err := s.accounts.Account(p.AccountID); err == nil {
			return a.CanSee
		}
	}
	return func(int) bool { return false }
}

// requireAdmin lets only admin accounts through.
func (s *Server) requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, _ := principalOf(r)
		a, err := s.accounts.Account(p.AccountID)
		if err != nil || !a.Admin {
			if strings.HasPrefix(r.URL.Path, "/sloplex/") {
				jsonError(w, http.StatusForbidden, "admin only")
			} else {
				http.Error(w, "403 Forbidden", http.StatusForbidden)
			}
			return
		}
		h(w, r)
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func deviceName(r *http.Request, explicit string) string {
	return cmp.Or(strings.TrimSpace(explicit), r.Header.Get("X-Plex-Device-Name"), r.Header.Get("X-Plex-Product"))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Device   string `json:"device"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ip := remoteIP(r)
	if wait, blocked := s.logins.blocked(ip); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		jsonError(w, http.StatusTooManyRequests, "too many failed logins; try again later")
		return
	}
	acct, err := s.accounts.Authenticate(body.Username, body.Password)
	if err != nil {
		s.logins.fail(ip)
		s.log.Warn("login failed", "user", body.Username, "remote", ip)
		jsonError(w, http.StatusUnauthorized, accounts.ErrBadCredentials.Error())
		return
	}
	plain, tok, err := s.accounts.IssueToken(acct.ID, deviceName(r, body.Device))
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.log.Info("login", "user", acct.Name, "device", tok.Device, "remote", ip)
	writeJSON(w, http.StatusOK, map[string]any{
		"account": viewAccount(acct),
		"token":   tokenView{ID: tok.ID, Device: tok.Device, CreatedAt: tok.CreatedAt, Token: plain},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	_, p, ok := s.caller(w, r)
	if !ok {
		return
	}
	if p.TokenID == 0 {
		jsonError(w, http.StatusBadRequest, "this request was not made with a revocable token")
		return
	}
	if err := s.accounts.RevokeToken(p.AccountID, p.TokenID); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if a, _, ok := s.caller(w, r); ok {
		writeJSON(w, http.StatusOK, viewAccount(a))
	}
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	a, p, ok := s.caller(w, r)
	if !ok {
		return
	}
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if a.HasPassword() {
		if _, err := s.accounts.Authenticate(a.Name, body.Current); err != nil {
			jsonError(w, http.StatusBadRequest, "current password is wrong")
			return
		}
	}
	if _, err := s.accounts.Update(a.ID, accounts.Update{Password: &body.New, KeepToken: p.TokenID}); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMyTokens(w http.ResponseWriter, r *http.Request) {
	if a, _, ok := s.caller(w, r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"tokens": viewTokens(s.accounts.Tokens(a.ID))})
	}
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	a, _, ok := s.caller(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	scope := a.ID
	if a.Admin {
		scope = 0
	}
	if err := s.accounts.RevokeToken(scope, id); err != nil {
		s.storeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	all := s.accounts.Accounts()
	out := make([]accountView, len(all))
	for i, a := range all {
		out[i] = viewAccount(a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string          `json:"name"`
		Password  string          `json:"password"`
		Admin     bool            `json:"admin"`
		Libraries json.RawMessage `json:"libraries"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	setLibs, libs, err := s.parseLibraries(body.Libraries)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.accounts.Create(body.Name, body.Password, body.Admin)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if setLibs {
		if a, err = s.accounts.Update(a.ID, accounts.Update{SetLibraries: true, Libraries: libs}); err != nil {
			s.storeError(w, err)
			return
		}
	}
	s.log.Info("account created", "user", a.Name, "admin", a.Admin)
	writeJSON(w, http.StatusCreated, viewAccount(a))
}

func (s *Server) handleUpdateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name      *string         `json:"name"`
		Password  *string         `json:"password"`
		Admin     *bool           `json:"admin"`
		Libraries json.RawMessage `json:"libraries"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	setLibs, libs, err := s.parseLibraries(body.Libraries)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.accounts.Update(id, accounts.Update{
		Name: body.Name, Password: body.Password, Admin: body.Admin, SetLibraries: setLibs, Libraries: libs,
	})
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, viewAccount(a))
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.accounts.Delete(id); err != nil {
		s.storeError(w, err)
		return
	}
	if err := s.play.DeleteAccount(id); err != nil {
		s.log.Error("delete play state", "account", id, "err", err)
	}
	s.log.Info("account deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAccountTokens(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.accounts.Account(id); err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": viewTokens(s.accounts.Tokens(id))})
}

func (s *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Device string `json:"device"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	plain, tok, err := s.accounts.IssueToken(id, deviceName(r, body.Device))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, tokenView{ID: tok.ID, Device: tok.Device, CreatedAt: tok.CreatedAt, Token: plain})
}

func (s *Server) handleCompatAccounts(w http.ResponseWriter, r *http.Request) {
	me, _, ok := s.caller(w, r)
	if !ok {
		return
	}
	list := []accounts.Account{me}
	if me.Admin {
		list = s.accounts.Accounts()
	}
	mc := MediaContainer{}
	for _, a := range list {
		mc.Account = append(mc.Account, AccountElem{ID: a.ID, Key: fmt.Sprintf("/accounts/%d", a.ID), Name: a.Name})
	}
	writeContainer(w, r, mc)
}

// Failed-login limiting: see "POST /sloplex/auth/login" in
// docs/spec/accounts.md.
const (
	loginMaxFails = 10
	loginWindow   = 15 * time.Minute
)

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
	now   func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: map[string][]time.Time{}, now: time.Now}
}

// prune drops failures older than the window. It must hold l.mu.
func (l *loginLimiter) prune(key string, now time.Time) []time.Time {
	ts := l.fails[key]
	i := 0
	for i < len(ts) && now.Sub(ts[i]) >= loginWindow {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.fails, key)
	} else {
		l.fails[key] = ts
	}
	return ts
}

// blocked reports whether key is locked out and for how long.
func (l *loginLimiter) blocked(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	ts := l.prune(key, now)
	if len(ts) < loginMaxFails {
		return 0, false
	}
	return loginWindow - now.Sub(ts[len(ts)-loginMaxFails]), true
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.fails) > 10_000 {
		for k := range l.fails {
			l.prune(k, now)
		}
	}
	l.fails[key] = append(l.prune(key, now), now)
}
