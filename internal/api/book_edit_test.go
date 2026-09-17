package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/spooknik/storykeeper/internal/auth"
)

func TestUpdateBook_TrimsAndLocksMetadata(t *testing.T) {
	s := newTestServer(t)
	admin := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)
	libID := mustCreateLibrary(t, s, "lib")
	bookID := mustCreateBook(t, s, libID, "Old Title", 100_000)

	ctx := context.Background()
	var before int64
	if err := s.DB.QueryRowContext(ctx, `SELECT updated_at FROM books WHERE id = ?`, bookID).Scan(&before); err != nil {
		t.Fatalf("read updated_at: %v", err)
	}

	title := "  New Title  "
	authors := []string{" Jane Doe ", "", "  John Smith"}
	req := bookEditRequest{
		Title:   &title,
		Authors: &authors,
	}
	body, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPatch, "/api/v1/books/"+strconv.FormatInt(bookID, 10), body, admin, nil), bookID)
	s.updateBook(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var detail BookDetail
	decodeBody(t, w, &detail)
	if detail.Title != "New Title" {
		t.Fatalf("title = %q, want %q", detail.Title, "New Title")
	}
	if len(detail.Authors) != 2 || detail.Authors[0] != "Jane Doe" || detail.Authors[1] != "John Smith" {
		t.Fatalf("authors = %v", detail.Authors)
	}

	var authorsJSON string
	var locked int
	var updatedAt int64
	if err := s.DB.QueryRowContext(ctx, `SELECT authors, metadata_locked, updated_at FROM books WHERE id = ?`, bookID).
		Scan(&authorsJSON, &locked, &updatedAt); err != nil {
		t.Fatalf("read book row: %v", err)
	}
	if authorsJSON != `["Jane Doe","John Smith"]` {
		t.Fatalf("authors JSON = %q", authorsJSON)
	}
	if locked != 1 {
		t.Fatalf("metadata_locked = %d, want 1", locked)
	}
	if updatedAt < before {
		t.Fatalf("updated_at not bumped: before=%d after=%d", before, updatedAt)
	}
}

func TestUpdateBook_EmptyTitleRejected(t *testing.T) {
	s := newTestServer(t)
	admin := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)
	libID := mustCreateLibrary(t, s, "lib")
	bookID := mustCreateBook(t, s, libID, "Keep Me", 100_000)

	empty := "   "
	body, _ := json.Marshal(bookEditRequest{Title: &empty})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPatch, "/api/v1/books/"+strconv.FormatInt(bookID, 10), body, admin, nil), bookID)
	s.updateBook(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateBook_NotFound(t *testing.T) {
	s := newTestServer(t)
	admin := mustCreateUser(t, s, "admin1", "password123", auth.RoleAdmin)

	title := "Whatever"
	body, _ := json.Marshal(bookEditRequest{Title: &title})
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodPatch, "/api/v1/books/999", body, admin, nil), 999)
	s.updateBook(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}
