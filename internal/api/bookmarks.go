package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/db"
)

// GET /api/v1/books/{id}/bookmarks
func (s *Server) listBookmarks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	b, err := s.loadBook(r, id)
	if err != nil {
		s.Log.Error("load book", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	if b == nil {
		writeError(w, http.StatusNotFound, "not_found", "book not found")
		return
	}
	u, _ := auth.FromContext(r.Context())
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, book_id, position_ms, note, created_at FROM bookmarks WHERE user_id = ? AND book_id = ? ORDER BY position_ms`,
		u.ID, id)
	if err != nil {
		s.Log.Error("list bookmarks", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := []Bookmark{}
	for rows.Next() {
		var bm Bookmark
		if err := rows.Scan(&bm.ID, &bm.BookID, &bm.PositionMs, &bm.Note, &bm.CreatedAt); err != nil {
			s.Log.Error("scan bookmark", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "scan failed")
			return
		}
		out = append(out, bm)
	}
	writeJSON(w, http.StatusOK, out)
}

type bookmarkRequest struct {
	PositionMs int64  `json:"position_ms"`
	Note       string `json:"note"`
}

// POST /api/v1/books/{id}/bookmarks
func (s *Server) createBookmark(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	b, err := s.loadBook(r, id)
	if err != nil {
		s.Log.Error("load book", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	if b == nil {
		writeError(w, http.StatusNotFound, "not_found", "book not found")
		return
	}
	var req bookmarkRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if len(req.Note) > 500 {
		writeError(w, http.StatusBadRequest, "bad_request", "note must be at most 500 characters")
		return
	}
	if req.PositionMs < 0 || req.PositionMs > b.DurationMs {
		writeError(w, http.StatusBadRequest, "bad_request", "position_ms out of range")
		return
	}
	u, _ := auth.FromContext(r.Context())
	bm := Bookmark{BookID: id, PositionMs: req.PositionMs, Note: req.Note, CreatedAt: db.Now()}
	err = s.DB.Write(r.Context(), func(tx *sql.Tx) error {
		res, err := tx.ExecContext(r.Context(),
			`INSERT INTO bookmarks (user_id, book_id, position_ms, note, created_at) VALUES (?, ?, ?, ?, ?)`,
			u.ID, bm.BookID, bm.PositionMs, bm.Note, bm.CreatedAt)
		if err != nil {
			return err
		}
		bm.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		s.Log.Error("create bookmark", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "insert failed")
		return
	}
	writeJSON(w, http.StatusCreated, bm)
}

// DELETE /api/v1/bookmarks/{id}
func (s *Server) deleteBookmark(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	u, _ := auth.FromContext(r.Context())
	err := s.DB.Write(r.Context(), func(tx *sql.Tx) error {
		res, err := tx.ExecContext(r.Context(), `DELETE FROM bookmarks WHERE id = ? AND user_id = ?`, id, u.ID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return sql.ErrNoRows
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "bookmark not found")
		return
	}
	if err != nil {
		s.Log.Error("delete bookmark", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
