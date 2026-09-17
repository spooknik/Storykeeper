package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/spooknik/storykeeper/internal/db"
)

// bookEditRequest is the admin metadata-edit body for PATCH /books/{id}.
// Every field is optional; only the fields present are changed.
type bookEditRequest struct {
	Title         *string   `json:"title,omitempty"`
	Subtitle      *string   `json:"subtitle,omitempty"`
	Authors       *[]string `json:"authors,omitempty"`
	Narrators     *[]string `json:"narrators,omitempty"`
	Series        *string   `json:"series,omitempty"`
	SeriesSeq     *string   `json:"series_seq,omitempty"`
	Description   *string   `json:"description,omitempty"`
	PublishedYear *int      `json:"published_year,omitempty"`
	Language      *string   `json:"language,omitempty"`
	ASIN          *string   `json:"asin,omitempty"`
	ISBN          *string   `json:"isbn,omitempty"`
}

func trimStringList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// PATCH /api/v1/books/{id} (admin). Marks the book's metadata as locked so
// the scanner's tag-derived rescans do not overwrite the edit.
func (s *Server) updateBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	var req bookEditRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	var exists bool
	if err := s.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM books WHERE id = ?)`, id).Scan(&exists); err != nil {
		s.Log.Error("check book", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found", "book not found")
		return
	}

	sets := []string{}
	args := []any{}

	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "title may not be empty")
			return
		}
		sets = append(sets, "title = ?")
		args = append(args, t)
	}
	if req.Subtitle != nil {
		sets = append(sets, "subtitle = ?")
		args = append(args, strings.TrimSpace(*req.Subtitle))
	}
	if req.Authors != nil {
		b, err := json.Marshal(trimStringList(*req.Authors))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "encode failed")
			return
		}
		sets = append(sets, "authors = ?")
		args = append(args, string(b))
	}
	if req.Narrators != nil {
		b, err := json.Marshal(trimStringList(*req.Narrators))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "encode failed")
			return
		}
		sets = append(sets, "narrators = ?")
		args = append(args, string(b))
	}
	if req.Series != nil {
		sets = append(sets, "series = ?")
		args = append(args, strings.TrimSpace(*req.Series))
	}
	if req.SeriesSeq != nil {
		sets = append(sets, "series_seq = ?")
		args = append(args, strings.TrimSpace(*req.SeriesSeq))
	}
	if req.Description != nil {
		sets = append(sets, "description = ?")
		args = append(args, strings.TrimSpace(*req.Description))
	}
	if req.PublishedYear != nil {
		sets = append(sets, "published_year = ?")
		args = append(args, *req.PublishedYear)
	}
	if req.Language != nil {
		sets = append(sets, "language = ?")
		args = append(args, strings.TrimSpace(*req.Language))
	}
	if req.ASIN != nil {
		sets = append(sets, "asin = ?")
		args = append(args, strings.TrimSpace(*req.ASIN))
	}
	if req.ISBN != nil {
		sets = append(sets, "isbn = ?")
		args = append(args, strings.TrimSpace(*req.ISBN))
	}

	sets = append(sets, "updated_at = ?", "metadata_locked = 1")
	args = append(args, db.Now(), id)

	err := s.DB.Write(r.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.Context(), `UPDATE books SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
		return err
	})
	if err != nil {
		s.Log.Error("update book", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}

	s.getBook(w, r)
}
