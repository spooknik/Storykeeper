package api

import (
	"context"
	"net/http"
	"sync"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/upload"
)

// Package-level rather than a Server field: server.go (which defines Server)
// is off-limits here, and the lead wires this handler into the route table
// separately. One process serves one upload Service for its lifetime.
var (
	uploadOnce sync.Once
	uploadSvc  *upload.Service
	uploadErr  error
)

// uploadHandler lazily constructs the tus upload Service on first use and
// returns an http.Handler that only admins may call. The route wrapper the
// lead mounts this behind also enforces login; this adds the admin check
// tus uploads additionally require.
func (s *Server) uploadHandler() http.Handler {
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

		svc, err := s.getUploadService()
		if err != nil {
			s.Log.Error("upload service unavailable", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "upload service unavailable")
			return
		}
		svc.Handler().ServeHTTP(w, r)
	})
}

// getUploadService builds the shared upload.Service exactly once and starts
// its expiry-sweep loop for the lifetime of the process.
func (s *Server) getUploadService() (*upload.Service, error) {
	uploadOnce.Do(func() {
		uploadSvc, uploadErr = upload.New(upload.Config{
			DataDir:  s.Cfg.DataDir,
			BasePath: "/api/v1/upload/",
		}, s.DB, func(ctx context.Context, libraryID int64, folder string) {
			if s.Scanner != nil {
				if err := s.Scanner.ScanLibrary(ctx, libraryID); err != nil {
					s.Log.Error("upload: post-upload scan failed", "library_id", libraryID, "folder", folder, "err", err)
				}
			}
			if s.Events != nil {
				s.Events.Broadcast("library", map[string]any{"library_id": libraryID, "action": "upload_complete"})
			}
		})
		if uploadErr == nil {
			go uploadSvc.Run(context.Background())
		}
	})
	return uploadSvc, uploadErr
}
