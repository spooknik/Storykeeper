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
//
// Visibility is driven by libraries.restricted, never by the mere presence of
// library_access rows: an unrestricted library is visible to everyone even if
// stray grants exist, and a restricted one stays private when its last grant
// is revoked.
func visibleClause(u *auth.User, col string) (string, []any) {
	if u.IsAdmin() {
		return "1=1", nil
	}
	return `(EXISTS (SELECT 1 FROM libraries lv WHERE lv.id = ` + col + ` AND lv.restricted = 0)
	         OR EXISTS (SELECT 1 FROM library_access la WHERE la.library_id = ` + col + ` AND la.user_id = ?))`,
		[]any{u.ID}
}

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.FromContext(r.Context())
	where, args := visibleClause(u, "l.id")
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT l.id, l.name, l.path, l.created_at,
		       (SELECT COUNT(*) FROM books b WHERE b.library_id = l.id),
		       l.restricted
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

type libraryPatchRequest struct {
	Name       *string `json:"name,omitempty"`
	Restricted *bool   `json:"restricted,omitempty"`
}

// PATCH /api/v1/libraries/{id} (admin): rename a library and/or flip its
// visibility. restricted=true limits it to admins and users holding a
// library_access grant; false opens it to everyone, whatever grants exist.
func (s *Server) updateLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	var req libraryPatchRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "name cannot be empty")
			return
		}
		req.Name = &name
	}

	var exists bool
	if err := s.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM libraries WHERE id = ?)`, id).Scan(&exists); err != nil {
		s.Log.Error("check library", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found", "library not found")
		return
	}

	err := s.DB.Write(r.Context(), func(tx *sql.Tx) error {
		if req.Name != nil {
			if _, err := tx.ExecContext(r.Context(), `UPDATE libraries SET name = ? WHERE id = ?`, *req.Name, id); err != nil {
				return err
			}
		}
		if req.Restricted != nil {
			if _, err := tx.ExecContext(r.Context(), `UPDATE libraries SET restricted = ? WHERE id = ?`, *req.Restricted, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.Log.Error("update library", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}

	lib, err := s.fetchLibrary(r.Context(), id)
	if err != nil {
		s.Log.Error("fetch library", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	writeJSON(w, http.StatusOK, lib)
}

// fetchLibrary loads one library row as the wire type, path included (only
// admin handlers use it).
func (s *Server) fetchLibrary(ctx context.Context, id int64) (*Library, error) {
	var l Library
	err := s.DB.QueryRowContext(ctx, `
		SELECT l.id, l.name, l.path, l.created_at,
		       (SELECT COUNT(*) FROM books b WHERE b.library_id = l.id),
		       l.restricted
		FROM libraries l WHERE l.id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Path, &l.CreatedAt, &l.BookCount, &l.Restricted)
	if err != nil {
		return nil, err
	}
	return &l, nil
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
