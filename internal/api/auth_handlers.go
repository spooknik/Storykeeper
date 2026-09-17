package api

import (
	"errors"
	"net/http"

	"github.com/spooknik/storykeeper/internal/auth"
)

type loginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
}

type sessionResponse struct {
	User    *auth.User    `json:"user"`
	Session *auth.Session `json:"session"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(req.DeviceName) > 80 {
		req.DeviceName = req.DeviceName[:80]
	}
	u, sess, raw, err := s.Auth.Login(r.Context(), req.Username, req.Password, req.DeviceName)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
		return
	}
	if err != nil {
		s.Log.Error("login", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "login failed")
		return
	}
	s.Auth.SetCookie(w, r, raw)
	writeJSON(w, http.StatusOK, sessionResponse{User: u, Session: sess})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_, sess := auth.FromContext(r.Context())
	if sess != nil {
		_ = s.Auth.Logout(r.Context(), sess.ID)
	}
	s.Auth.ClearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, sess := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, sessionResponse{User: u, Session: sess})
}
