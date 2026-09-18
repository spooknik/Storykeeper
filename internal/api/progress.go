package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/progress"
)

// progressStore builds the store on demand; it is stateless and only holds *db.DB.
func (s *Server) progressStore() *progress.Store { return progress.New(s.DB) }

// toProgress converts the storage record to the wire type. The two have the
// same JSON shape; they are separate so internal/progress never imports api.
func toProgress(r *progress.Record) Progress {
	return Progress{
		BookID:       r.BookID,
		PositionMs:   r.PositionMs,
		DurationMs:   r.DurationMs,
		FileIndex:    r.FileIndex,
		Seq:          r.Seq,
		ListenedAt:   r.ListenedAt,
		DeviceID:     r.DeviceID,
		DeviceName:   r.DeviceName,
		Finished:     r.Finished,
		PlaybackRate: r.PlaybackRate,
	}
}

// listProgress serves GET /api/v1/progress?since=N.
func (s *Server) listProgress(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	var since int64
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "since must be a non-negative integer")
			return
		}
		since = n
	}
	recs, err := s.progressStore().ListSince(r.Context(), u.ID, since)
	if err != nil {
		s.Log.Error("list progress", "err", err, "user", u.ID)
		writeError(w, http.StatusInternalServerError, "internal", "could not read progress")
		return
	}
	out := make([]Progress, 0, len(recs))
	for i := range recs {
		out = append(out, toProgress(&recs[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// getProgress serves GET /api/v1/progress/{bookId}.
func (s *Server) getProgress(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	bookID, ok := pathInt(r, "bookId")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid book id")
		return
	}
	if !s.bookVisible(w, r, bookID) {
		return
	}
	rec, err := s.progressStore().Get(r.Context(), u.ID, bookID)
	if err != nil {
		s.Log.Error("get progress", "err", err, "user", u.ID, "book", bookID)
		writeError(w, http.StatusInternalServerError, "internal", "could not read progress")
		return
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, "not_found", "no progress for this book")
		return
	}
	writeJSON(w, http.StatusOK, toProgress(rec))
}

// putProgress serves PUT /api/v1/progress/{bookId}: 200 with the stored record
// when the report wins, 409 with the CURRENT record when it loses.
func (s *Server) putProgress(w http.ResponseWriter, r *http.Request) {
	bookID, in, ok := s.decodeReport(w, r, false)
	if !ok || !s.bookVisible(w, r, bookID) {
		return
	}
	u, _ := auth.FromContext(r.Context())
	rec, accepted, err := s.progressStore().Report(r.Context(), in)
	switch {
	case errors.Is(err, progress.ErrStale):
		writeError(w, http.StatusBadRequest, "stale_report", "listen is older than 30 days")
		return
	case errors.Is(err, progress.ErrNoBook):
		writeError(w, http.StatusNotFound, "not_found", "no such book")
		return
	case err != nil:
		s.Log.Error("put progress", "err", err, "user", u.ID, "book", bookID)
		writeError(w, http.StatusInternalServerError, "internal", "could not save progress")
		return
	}
	body := toProgress(rec)
	if !accepted {
		writeJSON(w, http.StatusConflict, body)
		return
	}
	s.publish(u.ID, "progress", body)
	writeJSON(w, http.StatusOK, body)
}

// beaconProgress serves POST /api/v1/progress/{bookId}/beacon, the
// navigator.sendBeacon alias. The CSRF middleware skips this path, so the
// session CSRF token must be in the body instead. sendBeacon cannot read a
// response, so every outcome past that check answers 204.
func (s *Server) beaconProgress(w http.ResponseWriter, r *http.Request) {
	bookID, in, ok := s.decodeReport(w, r, true)
	if !ok {
		return
	}
	u, _ := auth.FromContext(r.Context())
	if b, err := s.loadBook(r, bookID); err != nil || b == nil {
		// Beacons never get a readable response; just refuse silently.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	rec, accepted, err := s.progressStore().Report(r.Context(), in)
	if err != nil {
		if !errors.Is(err, progress.ErrStale) && !errors.Is(err, progress.ErrNoBook) {
			s.Log.Error("beacon progress", "err", err, "user", u.ID, "book", bookID)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if accepted {
		s.publish(u.ID, "progress", toProgress(rec))
	}
	w.WriteHeader(http.StatusNoContent)
}

// putProgressRate serves PUT /api/v1/progress/{bookId}/rate: sets or clears
// (with a null body value) the per-book playback rate override.
func (s *Server) putProgressRate(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	bookID, ok := pathInt(r, "bookId")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid book id")
		return
	}
	if !s.bookVisible(w, r, bookID) {
		return
	}
	var req RateUpdate
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid body: "+err.Error())
		return
	}
	if req.PlaybackRate != nil && (*req.PlaybackRate < 0.5 || *req.PlaybackRate > 3.0) {
		writeError(w, http.StatusBadRequest, "bad_request", "playback_rate must be between 0.5 and 3.0")
		return
	}
	rec, err := s.progressStore().SetPlaybackRate(r.Context(), u.ID, bookID, req.PlaybackRate)
	if err != nil {
		if errors.Is(err, progress.ErrNoBook) {
			writeError(w, http.StatusNotFound, "not_found", "no such book")
			return
		}
		s.Log.Error("set playback rate", "err", err, "user", u.ID, "book", bookID)
		writeError(w, http.StatusInternalServerError, "internal", "could not save playback rate")
		return
	}
	body := toProgress(&rec)
	s.publish(u.ID, "progress", body)
	writeJSON(w, http.StatusOK, body)
}

// bookVisible enforces library access for progress routes. Books in a
// restricted library the user is not granted are reported as 404, exactly as
// GET /books/{id} does, so their existence is not revealed either.
func (s *Server) bookVisible(w http.ResponseWriter, r *http.Request, bookID int64) bool {
	b, err := s.loadBook(r, bookID)
	if err != nil {
		s.Log.Error("load book", "err", err, "book", bookID)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return false
	}
	if b == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such book")
		return false
	}
	return true
}

// decodeReport parses and validates the shared body of both write endpoints.
// It writes the error response itself and reports whether to continue. When
// bodyCSRF is set (the beacon path, which the CSRF middleware skips) the body
// must carry the session CSRF token.
func (s *Server) decodeReport(w http.ResponseWriter, r *http.Request, bodyCSRF bool) (int64, progress.ReportInput, bool) {
	var zero progress.ReportInput
	u, sess := auth.FromContext(r.Context())
	bookID, ok := pathInt(r, "bookId")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid book id")
		return 0, zero, false
	}
	var rep ProgressReport
	if err := decodeJSON(w, r, &rep); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid body: "+err.Error())
		return 0, zero, false
	}
	if bodyCSRF {
		if sess == nil || subtle.ConstantTimeCompare([]byte(rep.CSRFToken), []byte(sess.CSRFToken)) != 1 {
			writeError(w, http.StatusForbidden, "csrf", "invalid csrf_token")
			return 0, zero, false
		}
	}
	if rep.PositionMs < 0 || rep.FileIndex < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "position_ms and file_index must not be negative")
		return 0, zero, false
	}
	in := progress.ReportInput{
		UserID:           u.ID,
		BookID:           bookID,
		PositionMs:       rep.PositionMs,
		FileIndex:        rep.FileIndex,
		ClientListenedAt: rep.ClientListenedAt,
		ClientNow:        rep.ClientNow,
		BaseSeq:          rep.BaseSeq,
		Finished:         rep.Finished,
	}
	if sess != nil {
		in.DeviceID = sess.DeviceID
		in.DeviceName = sess.DeviceName
	}
	return bookID, in, true
}
