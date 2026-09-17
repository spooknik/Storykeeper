package api

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/spooknik/storykeeper/internal/auth"
)

// clientIP returns the caller's address. X-Forwarded-For is only honoured
// when the operator says a trusted proxy sits in front (SK_TRUST_PROXY),
// otherwise anyone could spoof their way past the per-IP limit.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, ok := strings.Cut(xff, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(xff)
		}
		if rip := r.Header.Get("X-Real-IP"); rip != "" {
			return strings.TrimSpace(rip)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

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
	// Brute-force protection: a few tries per username and a larger budget
	// per client address. Refused attempts do not touch the password hash.
	userKey := "u:" + strings.ToLower(strings.TrimSpace(req.Username))
	ipKey := "ip:" + clientIP(r, s.Cfg.TrustProxy)
	for _, k := range []struct {
		lim *auth.Limiter
		key string
	}{{s.loginByUser, userKey}, {s.loginByIP, ipKey}} {
		if ok, retry := k.lim.Allow(k.key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts, try again later")
			return
		}
	}
	u, sess, raw, err := s.Auth.Login(r.Context(), req.Username, req.Password, req.DeviceName)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", err.Error())
		return
	}
	s.loginByUser.Reset(userKey)
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
		// The session is gone; its event stream must go with it.
		s.closeSession(sess.ID)
	}
	s.Auth.ClearCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, sess := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, sessionResponse{User: u, Session: sess})
}
