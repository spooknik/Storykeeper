// Package api wires HTTP routes to the service packages.
package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/db"
	"github.com/spooknik/storykeeper/internal/library"
)

type Config struct {
	DataDir string
	// TrustProxy enables X-Forwarded-For for the client address (rate limits).
	// Set it only when a reverse proxy you control is the only way in.
	TrustProxy bool
}

type Server struct {
	DB      *db.DB
	Auth    *auth.Service
	Scanner *library.Scanner
	Cfg     Config
	Log     *slog.Logger
	Web     fs.FS          // built SvelteKit app; nil disables static serving
	Events  EventPublisher // may be nil

	loginByUser *auth.Limiter
	loginByIP   *auth.Limiter
}

// Handler returns the full route table.
//
// Route ownership (see the plan's execution strategy):
//   - auth, libraries, books, static: Fable (this package)
//   - /media/*: internal/media (agent:media) via serveBookFile/serveBookCover
//   - progress, events, upload, users, sessions, bookmarks: Wave A agent packages
func (s *Server) Handler() http.Handler {
	if s.loginByUser == nil {
		s.loginByUser = auth.NewLimiter(10, 15*time.Minute)
	}
	if s.loginByIP == nil {
		s.loginByIP = auth.NewLimiter(60, 15*time.Minute)
	}
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)

	// Authenticated
	mux.Handle("POST /api/v1/auth/logout", user(s.logout))
	mux.Handle("GET /api/v1/auth/me", user(s.me))

	mux.Handle("GET /api/v1/libraries", user(s.listLibraries))
	mux.Handle("POST /api/v1/libraries", admin(s.createLibrary))
	mux.Handle("DELETE /api/v1/libraries/{id}", admin(s.deleteLibrary))
	mux.Handle("POST /api/v1/libraries/{id}/scan", admin(s.scanLibrary))

	mux.Handle("GET /api/v1/books", user(s.listBooks))
	mux.Handle("GET /api/v1/books/{id}", user(s.getBook))
	mux.Handle("PATCH /api/v1/books/{id}", admin(s.updateBook))
	mux.Handle("GET /api/v1/authors", user(s.listAuthors))
	mux.Handle("GET /api/v1/series", user(s.listSeries))
	mux.Handle("GET /api/v1/narrators", user(s.listNarrators))

	mux.Handle("GET /media/books/{id}/files/{idx}", mediaRoute(s.serveBookFile))
	mux.Handle("GET /media/books/{id}/cover", mediaRoute(s.serveBookCover))

	// Progress, events, upload, users, sessions, bookmarks
	mux.Handle("GET /api/v1/progress", user(s.listProgress))
	mux.Handle("GET /api/v1/progress/{bookId}", user(s.getProgress))
	mux.Handle("PUT /api/v1/progress/{bookId}", user(s.putProgress))
	mux.Handle("POST /api/v1/progress/{bookId}/beacon", user(s.beaconProgress))
	mux.Handle("GET /api/v1/events", user(s.events))
	mux.Handle("/api/v1/upload/", requireUser(s.uploadHandler()))
	mux.Handle("GET /api/v1/users", admin(s.listUsers))
	mux.Handle("POST /api/v1/users", admin(s.createUser))
	mux.Handle("PATCH /api/v1/users/{id}", admin(s.updateUser))
	mux.Handle("DELETE /api/v1/users/{id}", admin(s.deleteUser))
	mux.Handle("PUT /api/v1/users/{id}/libraries", admin(s.setUserLibraries))
	mux.Handle("GET /api/v1/sessions", user(s.listSessions))
	mux.Handle("DELETE /api/v1/sessions/{id}", user(s.deleteSession))
	mux.Handle("GET /api/v1/books/{id}/bookmarks", user(s.listBookmarks))
	mux.Handle("POST /api/v1/books/{id}/bookmarks", user(s.createBookmark))
	mux.Handle("DELETE /api/v1/bookmarks/{id}", user(s.deleteBookmark))

	// Anything else under /api is a 404 in JSON, not the SPA shell.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such route")
	})

	if s.Web != nil {
		mux.Handle("/", spaHandler(s.Web))
	}

	return chain(mux, s.recoverer, s.logger, securityHeaders, s.withSession, csrf)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) notImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "this endpoint is not implemented yet")
}

// spaHandler serves the embedded build with an index.html fallback for client routes.
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FS(fsys)
	fileServer := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(p, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if f, err := files.Open(p); err == nil {
			st, err := f.Stat()
			_ = f.Close()
			if err == nil && !st.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		idx, err := fsys.Open("index.html")
		if err != nil {
			http.Error(w, "web app not built: run `npm run build` in ./web", http.StatusServiceUnavailable)
			return
		}
		defer idx.Close()
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeFileFS(w, r, fsys, "index.html")
	})
}
