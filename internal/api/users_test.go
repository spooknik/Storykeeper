package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/db"
)

// newTestServer opens a fresh temp SQLite DB with migrations applied and
// returns a minimal *Server wired to it. Shared by every _test.go file in
// this package.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return &Server{
		DB:   d,
		Auth: auth.New(d, false),
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// mustCreateUser creates a user via the auth service, failing the test on error.
func mustCreateUser(t *testing.T, s *Server, username, password, role string) *auth.User {
	t.Helper()
	u, err := s.Auth.CreateUser(context.Background(), username, password, role)
	if err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return u
}

// reqAs builds a request with the given principal attached and, for JSON
// bodies, a Content-Type header. body may be nil.
func reqAs(method, target string, body []byte, u *auth.User, sess *auth.Session) *http.Request {
	var br io.Reader
	if body != nil {
		br = bytes.NewReader(body)
	}
	r := httptest.NewRequest(method, target, br)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	return r.WithContext(auth.WithPrincipal(r.Context(), u, sess))
}

func setID(r *http.Request, id int64) *http.Request {
	r.SetPathValue("id", strconv.FormatInt(id, 10))
	return r
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode response body %q: %v", w.Body.String(), err)
	}
}

func TestDeleteUser_LastAdminProtected(t *testing.T) {
	s := newTestServer(t)
	admin := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)
	// Caller is a distinct principal (not persisted) so the self-delete guard
	// doesn't short-circuit before the last-admin check runs.
	caller := &auth.User{ID: admin.ID + 999, Role: auth.RoleAdmin}

	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodDelete, "/api/v1/users/"+strconv.FormatInt(admin.ID, 10), nil, caller, nil), admin.ID)
	s.deleteUser(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	var errResp ErrorResponse
	decodeBody(t, w, &errResp)
	if errResp.Error.Code != "last_admin" {
		t.Fatalf("error code = %q, want last_admin", errResp.Error.Code)
	}
}

func TestUpdateUser_LastAdminDemoteProtected(t *testing.T) {
	s := newTestServer(t)
	admin := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)
	caller := &auth.User{ID: admin.ID + 999, Role: auth.RoleAdmin}

	role := auth.RoleUser
	body, _ := json.Marshal(updateUserRequest{Role: &role})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPatch, "/api/v1/users/"+strconv.FormatInt(admin.ID, 10), body, caller, nil), admin.ID)
	s.updateUser(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", w.Code, w.Body.String())
	}
	var errResp ErrorResponse
	decodeBody(t, w, &errResp)
	if errResp.Error.Code != "last_admin" {
		t.Fatalf("error code = %q, want last_admin", errResp.Error.Code)
	}
}

func TestDeleteUser_RefusesSelfDelete(t *testing.T) {
	s := newTestServer(t)
	admin1 := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)
	mustCreateUser(t, s, "admin2", "password123", auth.RoleAdmin) // so it's not the last admin

	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodDelete, "/api/v1/users/"+strconv.FormatInt(admin1.ID, 10), nil, admin1, nil), admin1.ID)
	s.deleteUser(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestSetUserLibraries_ReplacesRows(t *testing.T) {
	s := newTestServer(t)
	admin := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)
	target := mustCreateUser(t, s, "bob", "password123", auth.RoleUser)

	ctx := context.Background()
	var lib1, lib2, lib3 int64
	if err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		for _, p := range []struct {
			name string
			id   *int64
		}{{"lib1", &lib1}, {"lib2", &lib2}, {"lib3", &lib3}} {
			res, err := tx.ExecContext(ctx, `INSERT INTO libraries (name, path, created_at) VALUES (?, ?, ?)`,
				p.name, "/"+p.name, db.Now())
			if err != nil {
				return err
			}
			*p.id, err = res.LastInsertId()
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed libraries: %v", err)
	}

	body, _ := json.Marshal(setLibrariesRequest{LibraryIDs: []int64{lib1, lib2}})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPut, "/api/v1/users/"+strconv.FormatInt(target.ID, 10)+"/libraries", body, admin, nil), target.ID)
	s.setUserLibraries(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	ids, err := s.libraryIDsForUser(ctx, target.ID)
	if err != nil {
		t.Fatalf("libraryIDsForUser: %v", err)
	}
	if len(ids) != 2 || ids[0] != lib1 || ids[1] != lib2 {
		t.Fatalf("library ids = %v, want [%d %d]", ids, lib1, lib2)
	}

	// Replace with a different set.
	body, _ = json.Marshal(setLibrariesRequest{LibraryIDs: []int64{lib3}})
	w = httptest.NewRecorder()
	r = setID(reqAs(http.MethodPut, "/api/v1/users/"+strconv.FormatInt(target.ID, 10)+"/libraries", body, admin, nil), target.ID)
	s.setUserLibraries(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	ids, err = s.libraryIDsForUser(ctx, target.ID)
	if err != nil {
		t.Fatalf("libraryIDsForUser: %v", err)
	}
	if len(ids) != 1 || ids[0] != lib3 {
		t.Fatalf("library ids = %v, want [%d]", ids, lib3)
	}
}

func TestUpdateUser_PasswordChangeRevokesSessions(t *testing.T) {
	s := newTestServer(t)
	admin := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)
	mustCreateUser(t, s, "bob", "password123", auth.RoleUser)

	_, sess, _, err := s.Auth.Login(context.Background(), "bob", "password123", "device1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	newPass := "newpassword456"
	body, _ := json.Marshal(updateUserRequest{Password: &newPass})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPatch, "/api/v1/users/"+strconv.FormatInt(sess.UserID, 10), body, admin, nil), sess.UserID)
	s.updateUser(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var n int
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, sess.UserID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 0 {
		t.Fatalf("sessions remaining = %d, want 0", n)
	}
}
