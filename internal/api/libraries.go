package api

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/db"
)

// visibleClause returns SQL restricting library_id (column expr) to libraries
// the user may see, plus the args it needs.
func visibleClause(u *auth.User, col string) (string, []any) {
	if u.IsAdmin() {
		return "1=1", nil
	}
	return `(NOT EXISTS (SELECT 1 FROM library_access la WHERE la.library_id = ` + col + `)
	         OR EXISTS (SELECT 1 FROM library_access la WHERE la.library_id = ` + col + ` AND la.user_id = ?))`,
		[]any{u.ID}
}

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	where, args := visibleClause(u, "l.id")
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT l.id, l.name, l.path, l.created_at,
		       (SELECT COUNT(*) FROM books b WHERE b.library_id = l.id),
		       EXISTS (SELECT 1 FROM library_access la WHERE la.library_id = l.id)
		FROM libraries l WHERE `+where+` ORDER BY l.name COLLATE NOCASE`, args...)
	if err != nil {
		s.Log.Error("list libraries", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.Path, &l.CreatedAt, &l.BookCount, &l.Restricted); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "scan failed")
			return
		}
		if !u.IsAdmin() {
			l.Path = ""
		}
		out = append(out, l)
	}
	writeJSON(w, http.StatusOK, out)
}

type libraryRequest struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var req libraryRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	abs, err := filepath.Abs(strings.TrimSpace(req.Path))
	if req.Name == "" || err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "name and path are required")
		return
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		writeError(w, http.StatusBadRequest, "bad_path", "path is not a readable directory on the server")
		return
	}
	lib, err := s.insertLibrary(r.Context(), req.Name, abs)
	if err != nil {
		writeError(w, http.StatusConflict, "conflict", "a library with that path already exists")
		return
	}
	go s.runScan(lib.ID)
	writeJSON(w, http.StatusCreated, lib)
}

func (s *Server) insertLibrary(ctx context.Context, name, abs string) (*Library, error) {
	lib := &Library{Name: name, Path: abs, CreatedAt: db.Now()}
	err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO libraries (name, path, created_at) VALUES (?, ?, ?)`,
			lib.Name, lib.Path, lib.CreatedAt)
		if err != nil {
			return err
		}
		lib.ID, err = res.LastInsertId()
		return err
	})
	return lib, err
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	err := s.DB.Write(r.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.Context(), `DELETE FROM libraries WHERE id = ?`, id)
		return err
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) scanLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	go s.runScan(id)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) runScan(libraryID int64) {
	if s.Scanner == nil {
		return
	}
	if err := s.Scanner.ScanLibrary(context.Background(), libraryID); err != nil {
		s.Log.Error("scan", "library", libraryID, "err", err)
	}
}
