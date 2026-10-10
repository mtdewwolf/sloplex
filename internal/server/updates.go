package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/mtdewwolf/sloplex/internal/accounts"
	"github.com/mtdewwolf/sloplex/internal/truenas"
	"github.com/mtdewwolf/sloplex/internal/updates"
)

// Update checks and one-click updates; see docs/spec/updates.md.

// trueNAS talks to a TrueNAS server; tests replace it.
type trueNAS interface {
	Test(ctx context.Context, s truenas.Settings) (state string, err error)
	Update(ctx context.Context, s truenas.Settings) (job int, err error)
}

type realTrueNAS struct{}

func (realTrueNAS) Test(ctx context.Context, s truenas.Settings) (string, error) {
	return truenas.Test(ctx, s)
}

func (realTrueNAS) Update(ctx context.Context, s truenas.Settings) (int, error) {
	return truenas.Update(ctx, s)
}

func (s *Server) updateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /sloplex/updates", s.requireAdmin(s.handleUpdates))
	mux.HandleFunc("POST /sloplex/updates/check", s.requireAdmin(s.handleUpdateCheck))
	mux.HandleFunc("POST /sloplex/updates/install", s.requireAdmin(s.handleUpdateInstall))
	mux.HandleFunc("GET /sloplex/truenas", s.requireOwner(s.handleTrueNAS))
	mux.HandleFunc("PUT /sloplex/truenas", s.requireOwner(s.handleSetTrueNAS))
	mux.HandleFunc("DELETE /sloplex/truenas", s.requireOwner(s.handleDeleteTrueNAS))
	mux.HandleFunc("POST /sloplex/truenas/test", s.requireOwner(s.handleTestTrueNAS))
}

// requireOwner lets only the owner account through.
func (s *Server) requireOwner(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p, ok := principalOf(r); !ok || p.AccountID != accounts.OwnerID {
			jsonError(w, http.StatusForbidden, "owner only")
			return
		}
		h(w, r)
	}
}

type updateStatus struct {
	updates.Status
	AutoCheck bool `json:"autoCheck"`
	OneClick  bool `json:"oneClick"`
}

func (s *Server) updateStatus(st updates.Status) updateStatus {
	cfg, ok := s.truenas.Get()
	return updateStatus{Status: st, AutoCheck: s.opts.AutoUpdateCheck, OneClick: ok && cfg.APIKey != ""}
}

func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updateStatus(s.updates.Status()))
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.updateStatus(s.updates.Check(r.Context())))
}

func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.truenas.Get()
	if !ok || cfg.APIKey == "" {
		jsonError(w, http.StatusConflict, "one-click updates are not set up: the owner can add TrueNAS details on the Admin page")
		return
	}
	st := s.updates.Status()
	if !st.Available {
		jsonError(w, http.StatusConflict, "no newer release is known; check for updates first")
		return
	}
	job, err := s.tn.Update(r.Context(), cfg)
	if err != nil {
		s.log.Warn("update through TrueNAS failed", "err", err)
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.log.Info("TrueNAS is updating the app", "app", cfg.App, "job", job, "from", st.Current, "to", st.Latest)
	writeJSON(w, http.StatusAccepted, map[string]int{"job": job})
}

type trueNASView struct {
	URL        string `json:"url"`
	App        string `json:"app"`
	CertSHA256 string `json:"certSHA256"`
	KeySet     bool   `json:"keySet"`
}

func (s *Server) trueNASView() trueNASView {
	cfg, _ := s.truenas.Get()
	return trueNASView{URL: cfg.URL, App: cfg.App, CertSHA256: cfg.CertSHA256, KeySet: cfg.APIKey != ""}
}

type trueNASRequest struct {
	URL        string `json:"url"`
	App        string `json:"app"`
	APIKey     string `json:"apiKey"`
	CertSHA256 string `json:"certSHA256"`
}

// trueNASSettings reads a request body into the settings it asks for.
func (s *Server) trueNASSettings(w http.ResponseWriter, r *http.Request) (truenas.Settings, bool) {
	var req trueNASRequest
	if !readJSON(w, r, &req) {
		return truenas.Settings{}, false
	}
	old, _ := s.truenas.Get()
	cfg, err := truenas.Apply(old, req.URL, req.App, req.APIKey, req.CertSHA256)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return truenas.Settings{}, false
	}
	return cfg, true
}

func (s *Server) handleTrueNAS(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.trueNASView())
}

func (s *Server) handleSetTrueNAS(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.trueNASSettings(w, r)
	if !ok {
		return
	}
	if err := s.truenas.Set(cfg); err != nil {
		s.log.Error("saving TrueNAS settings", "err", err)
		jsonError(w, http.StatusInternalServerError, "could not save the settings")
		return
	}
	s.log.Info("TrueNAS settings saved", "url", cfg.URL, "app", cfg.App, "pinned", cfg.CertSHA256 != "")
	writeJSON(w, http.StatusOK, s.trueNASView())
}

func (s *Server) handleDeleteTrueNAS(w http.ResponseWriter, r *http.Request) {
	if err := s.truenas.Delete(); err != nil {
		s.log.Error("removing TrueNAS settings", "err", err)
		jsonError(w, http.StatusInternalServerError, "could not remove the settings")
		return
	}
	s.log.Info("TrueNAS settings removed")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestTrueNAS(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.trueNASSettings(w, r)
	if !ok {
		return
	}
	state, err := s.tn.Test(r.Context(), cfg)
	if err != nil {
		out := map[string]any{"ok": false, "error": err.Error()}
		var untrusted *truenas.UntrustedError
		if errors.As(err, &untrusted) {
			out["fingerprint"] = untrusted.Fingerprint
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": state})
}
