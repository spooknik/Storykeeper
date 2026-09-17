package api

import (
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spooknik/storykeeper/internal/auth"
)

// CSRFHeader must be present on every mutating /api request, except the
// sendBeacon progress alias which carries the token in its body.
const CSRFHeader = "X-Storykeeper"

type middleware func(http.Handler) http.Handler

func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush for SSE.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: 200}
		start := time.Now()
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/_app/") {
			return
		}
		s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic", "err", rec, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withSession attaches the principal when a valid cookie is present. It never rejects.
func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(auth.CookieName); err == nil {
			if u, sess, err := s.Auth.Lookup(r.Context(), c.Value); err == nil {
				r = r.WithContext(auth.WithPrincipal(r.Context(), u, sess))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// csrf enforces the custom header on mutating /api requests.
func csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasSuffix(r.URL.Path, "/beacon") {
			if r.Header.Get(CSRFHeader) == "" {
				writeError(w, http.StatusForbidden, "csrf", "missing "+CSRFHeader+" header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireUser rejects unauthenticated API requests with JSON 401.
func requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, _ := auth.FromContext(r.Context()); u == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "login required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.FromContext(r.Context())
		if u == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "login required")
			return
		}
		if !u.IsAdmin() {
			writeError(w, http.StatusForbidden, "forbidden", "admin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireMediaUser is for /media/*: a bare 401 with no body and never a
// redirect, because Safari's range probe must see a plain status.
func requireMediaUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, _ := auth.FromContext(r.Context()); u == nil {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func user(h http.HandlerFunc) http.Handler       { return requireUser(h) }
func admin(h http.HandlerFunc) http.Handler      { return requireAdmin(h) }
func mediaRoute(h http.HandlerFunc) http.Handler { return requireMediaUser(h) }
