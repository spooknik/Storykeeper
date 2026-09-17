package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/spooknik/storykeeper/internal/db"
)

// openTestDB opens a fresh sqlite database under a temp dir, skipping the
// test if ffmpeg isn't available (every test in this file needs ffmpeg to
// generate audio fixtures via genAudio).
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH, skipping integration test")
	}
	dbPath := filepath.Join(t.TempDir(), "t.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// TestScanPathAddsOnlyTargetedBook proves ScanPath (a) discovers a brand new
// book folder without a full library walk and (b) never removes a book
// whose folder was deleted beforehand - that's removeStale's job, which
// ScanPath must not run.
func TestScanPathAddsOnlyTargetedBook(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	genAudio(t, filepath.Join(root, "Existing Author - Existing Book", "book.mp3"), map[string]string{
		"album": "Existing Book", "artist": "Existing Author",
	})
	genAudio(t, filepath.Join(root, "Doomed Author - Doomed Book", "book.mp3"), map[string]string{
		"album": "Doomed Book", "artist": "Doomed Author",
	})

	database := openTestDB(t)
	libID := insertLibrary(t, database, "test", root)

	s := New(database, dataDir)
	ctx := context.Background()

	if err := s.ScanLibrary(ctx, libID); err != nil {
		t.Fatalf("initial ScanLibrary: %v", err)
	}
	books := fetchBooks(t, database, libID)
	if len(books) != 2 {
		t.Fatalf("expected 2 books after initial scan, got %d: %+v", len(books), books)
	}

	// Delete one book's folder on disk without rescanning, then add a brand
	// new book and run ScanPath on just the new folder.
	if err := os.RemoveAll(filepath.Join(root, "Doomed Author - Doomed Book")); err != nil {
		t.Fatal(err)
	}

	newBookDir := filepath.Join(root, "New Author - New Book")
	genAudio(t, filepath.Join(newBookDir, "book.mp3"), map[string]string{
		"album": "New Book", "artist": "New Author",
	})

	if err := s.ScanPath(ctx, libID, newBookDir); err != nil {
		t.Fatalf("ScanPath: %v", err)
	}

	after := fetchBooks(t, database, libID)
	if len(after) != 3 {
		t.Fatalf("expected 3 books after ScanPath (2 existing including the deleted-on-disk one, plus 1 new), got %d: %+v", len(after), after)
	}

	byTitle := map[string]bool{}
	for _, b := range after {
		byTitle[b.title] = true
	}
	if !byTitle["New Book"] {
		t.Errorf("ScanPath did not add the new book, got titles: %+v", byTitle)
	}
	if !byTitle["Existing Book"] {
		t.Errorf("existing book missing after ScanPath, got titles: %+v", byTitle)
	}
	if !byTitle["Doomed Book"] {
		t.Errorf("ScanPath removed a book whose folder vanished on disk (removeStale must not run), got titles: %+v", byTitle)
	}
}

// TestScanPathOutsideLibraryErrors proves a path outside the library root is
// rejected rather than silently scanning (or worse, walking) foreign
// directories.
func TestScanPathOutsideLibraryErrors(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	dataDir := t.TempDir()

	database := openTestDB(t)
	libID := insertLibrary(t, database, "test", root)

	s := New(database, dataDir)
	ctx := context.Background()

	if err := s.ScanPath(ctx, libID, filepath.Join(outside, "Some Book")); err == nil {
		t.Fatal("ScanPath on a path outside the library root should return an error, got nil")
	}
}

// TestScanPathCDSubfolderScansParent proves that uploading a "CD2" folder
// next to an existing "CD1" folder causes ScanPath to widen its scan to the
// shared parent, producing one book with both discs' files rather than a
// second, wrong book.
func TestScanPathCDSubfolderScansParent(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	bookDir := filepath.Join(root, "Multi Disc Author - Multi Disc Book")
	genAudio(t, filepath.Join(bookDir, "CD1", "01.mp3"), map[string]string{
		"album": "Multi Disc Book", "artist": "Multi Disc Author",
	})

	database := openTestDB(t)
	libID := insertLibrary(t, database, "test", root)

	s := New(database, dataDir)
	ctx := context.Background()

	if err := s.ScanLibrary(ctx, libID); err != nil {
		t.Fatalf("initial ScanLibrary: %v", err)
	}

	folderPath := "Multi Disc Author - Multi Disc Book"
	book, ok := fetchBookByFolder(t, database, libID, folderPath)
	if !ok {
		t.Fatalf("missing initial book at folder_path %q", folderPath)
	}
	if files := fetchFiles(t, database, book.id); len(files) != 1 {
		t.Fatalf("expected 1 file before CD2 upload, got %d: %+v", len(files), files)
	}

	cd2Dir := filepath.Join(bookDir, "CD2")
	genAudio(t, filepath.Join(cd2Dir, "01.mp3"), map[string]string{
		"album": "Multi Disc Book", "artist": "Multi Disc Author",
	})

	if err := s.ScanPath(ctx, libID, cd2Dir); err != nil {
		t.Fatalf("ScanPath on CD2 subfolder: %v", err)
	}

	books := fetchBooks(t, database, libID)
	if len(books) != 1 {
		t.Fatalf("expected exactly 1 book after CD2 upload (no second book created), got %d: %+v", len(books), books)
	}

	updated, ok := fetchBookByFolder(t, database, libID, folderPath)
	if !ok {
		t.Fatalf("book at folder_path %q missing after ScanPath", folderPath)
	}
	files := fetchFiles(t, database, updated.id)
	if len(files) != 2 {
		t.Fatalf("expected 2 files (CD1 + CD2) in the merged book, got %d: %+v", len(files), files)
	}
}
