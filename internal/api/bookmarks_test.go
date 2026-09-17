package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/db"
)

// mustCreateLibrary inserts a library, optionally restricted to the given user IDs.
func mustCreateLibrary(t *testing.T, s *Server, name string, restrictedTo ...int64) int64 {
	t.Helper()
	ctx := context.Background()
	var libID int64
	if err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO libraries (name, path, created_at) VALUES (?, ?, ?)`,
			name, "/"+name, db.Now())
		if err != nil {
			return err
		}
		libID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		for _, uid := range restrictedTo {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO library_access (library_id, user_id) VALUES (?, ?)`, libID, uid); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("create library: %v", err)
	}
	return libID
}

// mustCreateBook inserts a minimal book row into libID.
func mustCreateBook(t *testing.T, s *Server, libID int64, title string, durationMs int64) int64 {
	t.Helper()
	ctx := context.Background()
	var bookID int64
	if err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO books
			(library_id, folder_path, title, authors, narrators, duration_ms, added_at, updated_at)
			VALUES (?, ?, ?, '[]', '[]', ?, ?, ?)`,
			libID, title, title, durationMs, db.Now(), db.Now())
		if err != nil {
			return err
		}
		bookID, err = res.LastInsertId()
		return err
	}); err != nil {
		t.Fatalf("create book: %v", err)
	}
	return bookID
}

func TestListBookmarks_InvisibleBookNotFound(t *testing.T) {
	s := newTestServer(t)
	owner := mustCreateUser(t, s, "owner", "password123", auth.RoleUser)
	outsider := mustCreateUser(t, s, "outsider", "password123", auth.RoleUser)

	libID := mustCreateLibrary(t, s, "restricted", owner.ID)
	bookID := mustCreateBook(t, s, libID, "Secret Book", 100_000)

	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodGet, "/api/v1/books/"+strconv.FormatInt(bookID, 10)+"/bookmarks", nil, outsider, nil), bookID)
	s.listBookmarks(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestCreateBookmark_InvisibleBookNotFound(t *testing.T) {
	s := newTestServer(t)
	owner := mustCreateUser(t, s, "owner", "password123", auth.RoleUser)
	outsider := mustCreateUser(t, s, "outsider", "password123", auth.RoleUser)

	libID := mustCreateLibrary(t, s, "restricted", owner.ID)
	bookID := mustCreateBook(t, s, libID, "Secret Book", 100_000)

	body, _ := json.Marshal(bookmarkRequest{PositionMs: 1000, Note: "hi"})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPost, "/api/v1/books/"+strconv.FormatInt(bookID, 10)+"/bookmarks", body, outsider, nil), bookID)
	s.createBookmark(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestBookmarks_CreateListDelete(t *testing.T) {
	s := newTestServer(t)
	owner := mustCreateUser(t, s, "owner", "password123", auth.RoleUser)
	other := mustCreateUser(t, s, "other", "password123", auth.RoleUser)
	libID := mustCreateLibrary(t, s, "open")
	bookID := mustCreateBook(t, s, libID, "Open Book", 100_000)

	body, _ := json.Marshal(bookmarkRequest{PositionMs: 5000, Note: "chapter break"})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPost, "/api/v1/books/"+strconv.FormatInt(bookID, 10)+"/bookmarks", body, owner, nil), bookID)
	s.createBookmark(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var bm Bookmark
	decodeBody(t, w, &bm)
	if bm.PositionMs != 5000 || bm.Note != "chapter break" {
		t.Fatalf("bookmark = %+v", bm)
	}

	// Out-of-range position is rejected.
	badBody, _ := json.Marshal(bookmarkRequest{PositionMs: 999_999, Note: ""})
	w = httptest.NewRecorder()
	r = setID(reqAs(http.MethodPost, "/api/v1/books/"+strconv.FormatInt(bookID, 10)+"/bookmarks", badBody, owner, nil), bookID)
	s.createBookmark(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range status = %d, want 400; body=%s", w.Code, w.Body.String())
	}

	// Another user can't delete owner's bookmark.
	w = httptest.NewRecorder()
	r = setID(reqAs(http.MethodDelete, "/api/v1/bookmarks/"+strconv.FormatInt(bm.ID, 10), nil, other, nil), bm.ID)
	s.deleteBookmark(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete-by-other status = %d, want 404; body=%s", w.Code, w.Body.String())
	}

	// Owner deletes their own bookmark.
	w = httptest.NewRecorder()
	r = setID(reqAs(http.MethodDelete, "/api/v1/bookmarks/"+strconv.FormatInt(bm.ID, 10), nil, owner, nil), bm.ID)
	s.deleteBookmark(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete-by-owner status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}
