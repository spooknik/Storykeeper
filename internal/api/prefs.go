package api

import (
	"net/http"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/prefs"
)

// prefsStore builds the store on demand; it is stateless and only holds *db.DB.
func (s *Server) prefsStore() *prefs.Store { return prefs.New(s.DB) }

func toPrefs(r prefs.Record) Prefs {
	return Prefs{
		PlaybackRate:        r.PlaybackRate,
		SkipBackSeconds:     r.SkipBackSec,
		SkipForwardSeconds:  r.SkipForwardSec,
		AutoRewind:          r.AutoRewind,
		DefaultSleepMinutes: r.DefaultSleepMinutes,
	}
}

// getPrefs serves GET /api/v1/me/prefs.
func (s *Server) getPrefs(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	rec, err := s.prefsStore().Get(r.Context(), u.ID)
	if err != nil {
		s.Log.Error("get prefs", "err", err, "user", u.ID)
		writeError(w, http.StatusInternalServerError, "internal", "could not read preferences")
		return
	}
	writeJSON(w, http.StatusOK, toPrefs(rec))
}

// putPrefs serves PUT /api/v1/me/prefs. Every field is optional; out-of-range
// values are rejected with 400 rather than clamped.
func (s *Server) putPrefs(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	var body PrefsPatch
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid body: "+err.Error())
		return
	}
	if body.PlaybackRate != nil && (*body.PlaybackRate < 0.5 || *body.PlaybackRate > 3.0) {
		writeError(w, http.StatusBadRequest, "bad_request", "playback_rate must be between 0.5 and 3.0")
		return
	}
	if body.SkipBackSeconds != nil && (*body.SkipBackSeconds < 5 || *body.SkipBackSeconds > 120) {
		writeError(w, http.StatusBadRequest, "bad_request", "skip_back_seconds must be between 5 and 120")
		return
	}
	if body.SkipForwardSeconds != nil && (*body.SkipForwardSeconds < 5 || *body.SkipForwardSeconds > 120) {
		writeError(w, http.StatusBadRequest, "bad_request", "skip_forward_seconds must be between 5 and 120")
		return
	}
	if body.DefaultSleepMinutes != nil && (*body.DefaultSleepMinutes < 0 || *body.DefaultSleepMinutes > 180) {
		writeError(w, http.StatusBadRequest, "bad_request", "default_sleep_minutes must be between 0 and 180")
		return
	}
	rec, err := s.prefsStore().Update(r.Context(), u.ID, prefs.Patch{
		PlaybackRate:        body.PlaybackRate,
		SkipBackSec:         body.SkipBackSeconds,
		SkipForwardSec:      body.SkipForwardSeconds,
		AutoRewind:          body.AutoRewind,
		DefaultSleepMinutes: body.DefaultSleepMinutes,
	})
	if err != nil {
		s.Log.Error("put prefs", "err", err, "user", u.ID)
		writeError(w, http.StatusInternalServerError, "internal", "could not save preferences")
		return
	}
	writeJSON(w, http.StatusOK, toPrefs(rec))
}
