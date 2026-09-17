package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/spooknik/storykeeper/internal/auth"
)

// userResponse adds the restricted-library set a user has explicit access to.
type userResponse struct {
	auth.User
	LibraryIDs []int64 `json:"library_ids"`
}

func (s *Server) libraryIDsForUser(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT library_id FROM library_access WHERE user_id = ? ORDER BY library_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Server) fetchUser(ctx context.Context, id int64) (*auth.User, error) {
	var u auth.User
	err := s.DB.QueryRowContext(ctx, `SELECT id, username, role, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GET /api/v1/users
func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Auth.ListUsers(r.Context())
	if err != nil {
		s.Log.Error("list users", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	out := make([]userResponse, 0, len(users))
	for _, u := range users {
		ids, err := s.libraryIDsForUser(r.Context(), u.ID)
		if err != nil {
			s.Log.Error("list user libraries", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "query failed")
			return
		}
		out = append(out, userResponse{User: u, LibraryIDs: ids})
	}
	writeJSON(w, http.StatusOK, out)
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// POST /api/v1/users
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), req.Username, req.Password, req.Role)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			writeError(w, http.StatusConflict, "conflict", "username already exists")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

type updateUserRequest struct {
	Password *string `json:"password,omitempty"`
	Role     *string `json:"role,omitempty"`
}

// PATCH /api/v1/users/{id}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	var req updateUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	err := s.Auth.UpdateUser(r.Context(), id, req.Password, req.Role)
	switch {
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "cannot demote the last admin")
		return
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.Password != nil {
		// Changing the password revoked every session of that user; drop their
		// live event streams too.
		s.closeUser(id)
	}
	u, err := s.fetchUser(r.Context(), id)
	if err != nil {
		s.Log.Error("fetch user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// DELETE /api/v1/users/{id}
func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	u, _ := auth.FromContext(r.Context())
	if u.ID == id {
		writeError(w, http.StatusBadRequest, "bad_request", "cannot delete your own account")
		return
	}
	err := s.Auth.DeleteUser(r.Context(), id)
	switch {
	case errors.Is(err, auth.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "cannot delete the last admin")
		return
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	case err != nil:
		s.Log.Error("delete user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	s.closeUser(id)
	w.WriteHeader(http.StatusNoContent)
}

type setLibrariesRequest struct {
	LibraryIDs []int64 `json:"library_ids"`
}

// PUT /api/v1/users/{id}/libraries
func (s *Server) setUserLibraries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", "bad id")
		return
	}
	var req setLibrariesRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var exists bool
	if err := s.DB.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, id).Scan(&exists); err != nil {
		s.Log.Error("check user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	err := s.DB.Write(r.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM library_access WHERE user_id = ?`, id); err != nil {
			return err
		}
		for _, lid := range req.LibraryIDs {
			if _, err := tx.ExecContext(r.Context(),
				`INSERT INTO library_access (library_id, user_id) VALUES (?, ?)`, lid, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		s.Log.Error("set user libraries", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
