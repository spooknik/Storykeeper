package api

import (
	"net/http"

	"github.com/spooknik/storykeeper/internal/auth"
)

// events streams Server-Sent Events for the authenticated user.
//
// The route is wired to a publisher through the EventPublisher interface, which
// deliberately says nothing about HTTP; the concrete hub also knows how to serve
// a stream, so this handler asks for that capability at run time and degrades to
// a JSON 503 when no hub is configured.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	u, sess := auth.FromContext(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "login required")
		return
	}
	streamer, ok := s.Events.(interface {
		Serve(http.ResponseWriter, *http.Request, int64, int64)
	})
	if s.Events == nil || !ok {
		writeError(w, http.StatusServiceUnavailable, "events_unavailable", "event stream is not available")
		return
	}
	// The session id lets the hub drop this stream the moment the session is
	// revoked, instead of leaving it live until the client reconnects.
	var sessionID int64
	if sess != nil {
		sessionID = sess.ID
	}
	streamer.Serve(w, r, u.ID, sessionID)
}
