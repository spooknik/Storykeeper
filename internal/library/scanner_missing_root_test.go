package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spooknik/storykeeper/internal/db"
)

// openPlainTestDB opens a fresh sqlite database under a temp dir. Unlike
// openTestDB it does not need ffmpeg: these tests never generate audio.
func openPlainTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// TestScanLibraryMissingRootKeepsBooks proves that a library root which has
// gone away (unmounted NAS, renamed folder) never makes the scanner treat the
// library as empty: the scan must fail loudly and leave books, progress and
// bookmarks untouched.
func TestScanLibraryMissingRootKeepsBooks(t *testing.T) {
	database := openPlainTestDB(t)
	ctx := context.Background()

	// A root that existed once and is now gone.
	root := filepath.Join(t.TempDir(), "library-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	libID := insertLibrary(t, database, "Gone", root)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	res, err := database.Exec(
		`INSERT INTO books (library_id, folder_path, title, added_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		libID, "Some Author - Some Book", "Some Book", db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	bookID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	res, err = database.Exec(
		`INSERT INTO users (username, password_hash, role, created_at) VALUES (?, ?, ?, ?)`,
		"alice", "x", "user", db.Now())
	if err != nil {
		t.Fatal(err)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO progress
		(user_id, book_id, position_ms, duration_ms, file_index, seq, listened_at, received_at, device_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, bookID, 123456, 600000, 0, 1, db.Now(), db.Now(), "device-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO bookmarks (user_id, book_id, position_ms, note, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, bookID, 1000, "here", db.Now()); err != nil {
		t.Fatal(err)
	}

	s := New(database, t.TempDir())
	if err := s.ScanLibrary(ctx, libID); err == nil {
		t.Fatal("ScanLibrary returned nil for a missing library root, want an error")
	}

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := database.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM books WHERE library_id = ?`, libID); n != 1 {
		t.Errorf("books = %d, want 1 (the scan must not remove books)", n)
	}
	if n := count(`SELECT COUNT(*) FROM progress WHERE book_id = ?`, bookID); n != 1 {
		t.Errorf("progress rows = %d, want 1", n)
	}
	if n := count(`SELECT COUNT(*) FROM bookmarks WHERE book_id = ?`, bookID); n != 1 {
		t.Errorf("bookmark rows = %d, want 1", n)
	}
}

// TestScanLibraryRootIsAFileFails covers the other unavailable-root shape: a
// path that exists but is not a directory.
func TestScanLibraryRootIsAFileFails(t *testing.T) {
	database := openPlainTestDB(t)

	notADir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	libID := insertLibrary(t, database, "File", notADir)

	s := New(database, t.TempDir())
	if err := s.ScanLibrary(context.Background(), libID); err == nil {
		t.Fatal("ScanLibrary returned nil for a non-directory library root, want an error")
	}
}

// TestScanPathMissingRootFails: the targeted scan must refuse just as loudly.
func TestScanPathMissingRootFails(t *testing.T) {
	database := openPlainTestDB(t)

	root := filepath.Join(t.TempDir(), "library-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	libID := insertLibrary(t, database, "Gone", root)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	s := New(database, t.TempDir())
	if err := s.ScanPath(context.Background(), libID, filepath.Join(root, "New Book")); err == nil {
		t.Fatal("ScanPath returned nil for a missing library root, want an error")
	}
}

// TestWalkAudioFilesMissingRootErrors pins the low-level behaviour: a missing
// root is an error, not an empty walk.
func TestWalkAudioFilesMissingRootErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if _, _, err := walkAudioFiles(missing); err == nil {
		t.Fatal("walkAudioFiles returned nil error for a missing root")
	}
}
