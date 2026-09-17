package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/db"
)

func mustInsertLibrary(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	res, err := s.DB.Exec(`INSERT INTO libraries (name, path, created_at) VALUES (?, ?, ?)`,
		name, t.TempDir(), db.Now())
	if err != nil {
		t.Fatalf("insert library %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func grant(t *testing.T, s *Server, libID, userID int64) {
	t.Helper()
	if _, err := s.DB.Exec(`INSERT INTO library_access (library_id, user_id) VALUES (?, ?)`, libID, userID); err != nil {
		t.Fatalf("grant: %v", err)
	}
}

func revoke(t *testing.T, s *Server, libID, userID int64) {
	t.Helper()
	if _, err := s.DB.Exec(`DELETE FROM library_access WHERE library_id = ? AND user_id = ?`, libID, userID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
}

// visibleLibraries returns the library ids GET /libraries shows to u.
func visibleLibraries(t *testing.T, s *Server, u *auth.User) map[int64]Library {
	t.Helper()
	w := httptest.NewRecorder()
	s.listLibraries(w, reqAs(http.MethodGet, "/api/v1/libraries", nil, u, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("listLibraries status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var out []Library
	decodeBody(t, w, &out)
	got := make(map[int64]Library, len(out))
	for _, l := range out {
		got[l.ID] = l
	}
	return got
}

// TestVisibility_RemovingLastGrantKeepsLibraryRestricted is the core of the
// defect: a restricted library whose last grant is revoked must NOT fall back
// to being visible to everyone.
func TestVisibility_RemovingLastGrantKeepsLibraryRestricted(t *testing.T) {
	s := newTestServer(t)
	alice := mustCreateUser(t, s, "alice", "password123", auth.RoleUser)
	bob := mustCreateUser(t, s, "bob", "password123", auth.RoleUser)

	libID := mustInsertLibrary(t, s, "Private")
	setRestricted(t, s, libID, true)
	grant(t, s, libID, alice.ID)

	if _, ok := visibleLibraries(t, s, alice)[libID]; !ok {
		t.Fatal("alice should see the library she is granted")
	}
	if _, ok := visibleLibraries(t, s, bob)[libID]; ok {
		t.Fatal("bob should not see a restricted library he has no grant for")
	}

	revoke(t, s, libID, alice.ID)

	if _, ok := visibleLibraries(t, s, alice)[libID]; ok {
		t.Error("alice still sees the library after her grant was revoked")
	}
	if _, ok := visibleLibraries(t, s, bob)[libID]; ok {
		t.Error("removing the last grant made the restricted library public to bob")
	}
}

// TestVisibility_RevokeOneOfTwoGrants: the contract scenario from the review.
func TestVisibility_RevokeOneOfTwoGrants(t *testing.T) {
	s := newTestServer(t)
	alice := mustCreateUser(t, s, "alice", "password123", auth.RoleUser)
	bob := mustCreateUser(t, s, "bob", "password123", auth.RoleUser)
	carol := mustCreateUser(t, s, "carol", "password123", auth.RoleUser)
	admin := mustCreateUser(t, s, "root", "password123", auth.RoleAdmin)

	libID := mustInsertLibrary(t, s, "Private")
	setRestricted(t, s, libID, true)
	grant(t, s, libID, alice.ID)
	grant(t, s, libID, bob.ID)

	// Revoke bob's grant the way the API does it: PUT /users/{id}/libraries {}.
	setLibraries(t, s, admin, bob.ID, nil)

	if _, ok := visibleLibraries(t, s, alice)[libID]; !ok {
		t.Error("alice lost access when bob's grant was revoked")
	}
	if _, ok := visibleLibraries(t, s, bob)[libID]; ok {
		t.Error("bob still sees the library after his grant was revoked")
	}
	if _, ok := visibleLibraries(t, s, carol)[libID]; ok {
		t.Error("carol sees a restricted library she was never granted")
	}
	if _, ok := visibleLibraries(t, s, admin)[libID]; !ok {
		t.Error("admins must see every library")
	}
}

// TestVisibility_UnrestrictedLibraryWithStrayGrant: grants alone never
// restrict anything; only the flag does.
func TestVisibility_UnrestrictedLibraryWithStrayGrant(t *testing.T) {
	s := newTestServer(t)
	alice := mustCreateUser(t, s, "alice", "password123", auth.RoleUser)
	bob := mustCreateUser(t, s, "bob", "password123", auth.RoleUser)

	libID := mustInsertLibrary(t, s, "Open")
	grant(t, s, libID, alice.ID) // stray leftover grant, flag not set

	for name, u := range map[string]*auth.User{"alice": alice, "bob": bob} {
		lib, ok := visibleLibraries(t, s, u)[libID]
		if !ok {
			t.Errorf("%s cannot see an unrestricted library", name)
			continue
		}
		if lib.Restricted {
			t.Errorf("%s: restricted = true, want false", name)
		}
	}
}

// setLibraries calls PUT /users/{id}/libraries as admin.
func setLibraries(t *testing.T, s *Server, admin *auth.User, userID int64, libIDs []int64) {
	t.Helper()
	body := mustJSON(t, setLibrariesRequest{LibraryIDs: libIDs})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPut, "/api/v1/users/x/libraries", body, admin, nil), userID)
	s.setUserLibraries(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("setUserLibraries status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// setRestricted flips a library's visibility through PATCH /libraries/{id},
// which is the contract admins use.
func setRestricted(t *testing.T, s *Server, libID int64, restricted bool) {
	t.Helper()
	admin := &auth.User{ID: -1, Role: auth.RoleAdmin}
	body := mustJSON(t, libraryPatchRequest{Restricted: &restricted})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPatch, "/api/v1/libraries/x", body, admin, nil), libID)
	s.updateLibrary(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("updateLibrary status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got Library
	decodeBody(t, w, &got)
	if got.Restricted != restricted {
		t.Fatalf("PATCH returned restricted = %v, want %v", got.Restricted, restricted)
	}
}

func TestUpdateLibrary_PatchNameAndFlag(t *testing.T) {
	s := newTestServer(t)
	admin := &auth.User{ID: -1, Role: auth.RoleAdmin}
	libID := mustInsertLibrary(t, s, "Old Name")

	name := "New Name"
	restricted := true
	body := mustJSON(t, libraryPatchRequest{Name: &name, Restricted: &restricted})
	w := httptest.NewRecorder()
	s.updateLibrary(w, setID(reqAs(http.MethodPatch, "/api/v1/libraries/x", body, admin, nil), libID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got Library
	decodeBody(t, w, &got)
	if got.ID != libID || got.Name != "New Name" || !got.Restricted {
		t.Fatalf("library = %+v, want id %d, name New Name, restricted true", got, libID)
	}

	// Name-only patch leaves the flag alone.
	other := "Newer"
	body = mustJSON(t, libraryPatchRequest{Name: &other})
	w = httptest.NewRecorder()
	s.updateLibrary(w, setID(reqAs(http.MethodPatch, "/api/v1/libraries/x", body, admin, nil), libID))
	decodeBody(t, w, &got)
	if got.Name != "Newer" || !got.Restricted {
		t.Fatalf("library = %+v, want name Newer and restricted still true", got)
	}

	// Unknown library is a 404.
	w = httptest.NewRecorder()
	s.updateLibrary(w, setID(reqAs(http.MethodPatch, "/api/v1/libraries/x", mustJSON(t, libraryPatchRequest{Name: &other}), admin, nil), libID+9999))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// TestMigration0004MarksGrantedLibrariesRestricted runs the migrations from
// disk, stopping just before 00004, seeds the pre-migration state (one library
// with grants, one without) and checks that the upgrade preserves the old
// meaning of "has grants" == restricted.
func TestMigration0004MarksGrantedLibrariesRestricted(t *testing.T) {
	migDir := filepath.Join("..", "db")
	if _, err := os.Stat(filepath.Join(migDir, "migrations", "00004_library_restricted.sql")); err != nil {
		t.Skipf("migrations not readable from here: %v", err)
	}

	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "m.db")) +
		"?_pragma=foreign_keys(ON)&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer sqldb.Close()

	goose.SetBaseFS(os.DirFS(migDir))
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(sqldb, "migrations", 3); err != nil {
		t.Fatalf("migrate to 00003: %v", err)
	}

	mustExec := func(q string, args ...any) sql.Result {
		t.Helper()
		res, err := sqldb.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return res
	}
	granted := mustExec(`INSERT INTO libraries (name, path, created_at) VALUES ('Granted', '/a', 1)`)
	open := mustExec(`INSERT INTO libraries (name, path, created_at) VALUES ('Open', '/b', 1)`)
	grantedID, _ := granted.LastInsertId()
	openID, _ := open.LastInsertId()
	mustExec(`INSERT INTO users (username, password_hash, role, created_at) VALUES ('alice', 'x', 'user', 1)`)
	mustExec(`INSERT INTO library_access (library_id, user_id) VALUES (?, 1)`, grantedID)

	if err := goose.Up(sqldb, "migrations"); err != nil {
		t.Fatalf("migrate to head: %v", err)
	}

	flag := func(id int64) int {
		t.Helper()
		var n int
		if err := sqldb.QueryRow(`SELECT restricted FROM libraries WHERE id = ?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := flag(grantedID); got != 1 {
		t.Errorf("library with a pre-existing grant: restricted = %d, want 1", got)
	}
	if got := flag(openID); got != 0 {
		t.Errorf("library without grants: restricted = %d, want 0", got)
	}
}
