package api

import (
	"errors"
	"net/http"

	"github.com/spooknik/storykeeper/internal/auth"
)

// sessionListItem flags the session that made the current request.
type sessionListItem struct {
	auth.Session
	Current bool `json:"current"`
}

// GET /api/v1/sessions
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	u, sess := auth.FromContext(r.Context())
	sessions, err := s.Auth.ListSessions(r.Context(), u.ID)
	if err != nil {
		s.Log.Error("list sessions", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	out := make([]sessionListItem, 0, len(sessions))
	for _, sn := range sessions {
		out = append(out, sessionListItem{Session: sn, Current: sess != nil && sn.ID == sess.ID})
	}
	writeJSON(w, http.StatusOK, out)
}

// DELETE /api/v1/sessions/{id}
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	u, _ := auth.FromContext(r.Context())
	if err := s.Auth.DeleteSession(r.Context(), u.ID, id); err != nil {
		if errors.Is(err, auth.ErrNoSession) {
			writeError(w, http.StatusNotFound, "not_found", "session not found")
			return
		}
		s.Log.Error("delete session", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	// A revoked session must stop receiving events immediately, not at its
	// next request.
	s.closeSession(id)
	w.WriteHeader(http.StatusNoContent)
}
