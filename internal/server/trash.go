package server

import (
	"net/http"
	"time"
)

// handleEmptyTrash implements the public admin operation; see trash.md.
func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.section(w, r)
	if !ok {
		return
	}
	if _, err := s.store.EmptyTrash(sec.ID, time.Now()); err != nil {
		s.log.Error("empty library trash", "section", sec.ID, "err", err)
		http.Error(w, "could not empty library trash", http.StatusInternalServerError)
		return
	}
	s.revokeSessions()
	w.WriteHeader(http.StatusOK)
}
