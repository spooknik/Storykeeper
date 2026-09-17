package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/spooknik/storykeeper/internal/db"
)

// genAudio generates a short sine-wave audio file with the given tags,
// using ffmpeg. It skips the calling test if ffmpeg is not on PATH.
func genAudio(t *testing.T, path string, tags map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH, skipping integration test")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-y", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=2"}
	for k, v := range tags {
		args = append(args, "-metadata", k+"="+v)
	}
	args = append(args, path)
	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate %s failed: %v\n%s", path, err, out)
	}
}

func insertLibrary(t *testing.T, database *db.DB, name, path string) int64 {
	t.Helper()
	res, err := database.Exec(`INSERT INTO libraries (name, path, created_at) VALUES (?, ?, ?)`, name, path, db.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type bookRow struct {
	id         int64
	folderPath string
	title      string
	authors    string
	narrators  string
	series     string
	seriesSeq  string
	durationMs int64
	updatedAt  int64
	scanHash   string
}

func fetchBooks(t *testing.T, database *db.DB, libID int64) []bookRow {
	t.Helper()
	rows, err := database.Query(`SELECT id, folder_path, title, authors, narrators, series, series_seq, duration_ms, updated_at, scan_hash FROM books WHERE library_id = ? ORDER BY title`, libID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []bookRow
	for rows.Next() {
		var b bookRow
		if err := rows.Scan(&b.id, &b.folderPath, &b.title, &b.authors, &b.narrators, &b.series, &b.seriesSeq, &b.durationMs, &b.updatedAt, &b.scanHash); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func fetchBookByFolder(t *testing.T, database *db.DB, libID int64, folderPath string) (bookRow, bool) {
	t.Helper()
	for _, b := range fetchBooks(t, database, libID) {
		if b.folderPath == folderPath {
			return b, true
		}
	}
	return bookRow{}, false
}

type chapterRowDB struct {
	title          string
	startMs, endMs int64
}

func fetchChapters(t *testing.T, database *db.DB, bookID int64) []chapterRowDB {
	t.Helper()
	rows, err := database.Query(`SELECT title, start_ms, end_ms FROM chapters WHERE book_id = ? ORDER BY idx`, bookID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []chapterRowDB
	for rows.Next() {
		var c chapterRowDB
		if err := rows.Scan(&c.title, &c.startMs, &c.endMs); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func fetchFiles(t *testing.T, database *db.DB, bookID int64) []struct {
	relPath    string
	durationMs int64
} {
	t.Helper()
	rows, err := database.Query(`SELECT rel_path, duration_ms FROM book_files WHERE book_id = ? ORDER BY idx`, bookID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []struct {
		relPath    string
		durationMs int64
	}
	for rows.Next() {
		var f struct {
			relPath    string
			durationMs int64
		}
		if err := rows.Scan(&f.relPath, &f.durationMs); err != nil {
			t.Fatal(err)
		}
		out = append(out, f)
	}
	return out
}

func TestScanLibraryIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH, skipping integration test")
	}

	root := t.TempDir()
	dataDir := t.TempDir()

	// Book 1: a single-file book inside its own folder.
	genAudio(t, filepath.Join(root, "BookOne", "BookOne.m4b"), map[string]string{
		"title": "Solo Chapter", "album": "Book One", "artist": "Author One",
	})

	// Book 2: two mp3 files whose filenames sort the OPPOSITE of their
	// track-number tags, to prove tag-based ordering (not just natural
	// filename sort) is used.
	genAudio(t, filepath.Join(root, "BookTwo", "second.mp3"), map[string]string{
		"title": "Track A", "album": "Book Two", "artist": "Author Two", "track": "1",
	})
	genAudio(t, filepath.Join(root, "BookTwo", "first.mp3"), map[string]string{
		"title": "Track B", "album": "Book Two", "artist": "Author Two", "track": "2",
	})

	// Book 3: a root-level single file.
	genAudio(t, filepath.Join(root, "RootBook.m4b"), map[string]string{
		"title": "Root Solo", "album": "Root Book", "artist": "Root Author",
	})

	// Book 4: an ABS-managed book with a metadata.json sidecar. The folder
	// name carries a leading sequence number ("01 - Title") that should be
	// stripped from the title (series is known, from the sidecar), with
	// narrators and chapters coming entirely from the sidecar.
	sidecarBookDir := filepath.Join(root, "SidecarAuthor", "Sidecar Series", "01 - Title")
	genAudio(t, filepath.Join(sidecarBookDir, "track.mp3"), map[string]string{})
	sidecarJSON := `{
		"title": null,
		"narrators": ["Jane Narrator"],
		"series": ["Sidecar Series #1"],
		"chapters": [
			{"id": 0, "start": 0, "end": 1.0, "title": "Chapter 1"},
			{"id": 1, "start": 1.0, "end": 2.0, "title": "Chapter 2"}
		]
	}`
	if err := os.WriteFile(filepath.Join(sidecarBookDir, "metadata.json"), []byte(sidecarJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Book 5: a book folder with direct audio plus a nested "Bonus"
	// subfolder that isn't itself a book - both must land in one book
	// under the shallowest-directory grouping rule.
	genAudio(t, filepath.Join(root, "MergeAuthor", "Other", "main.mp3"), map[string]string{
		"album": "Other Book", "artist": "Merge Author",
	})
	genAudio(t, filepath.Join(root, "MergeAuthor", "Other", "Bonus", "extra.mp3"), map[string]string{})

	dbPath := filepath.Join(t.TempDir(), "t.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	libID := insertLibrary(t, database, "test", root)

	s := New(database, dataDir)
	ctx := context.Background()

	if err := s.ScanLibrary(ctx, libID); err != nil {
		t.Fatalf("ScanLibrary: %v", err)
	}

	books := fetchBooks(t, database, libID)
	if len(books) != 5 {
		t.Fatalf("expected 5 books, got %d: %+v", len(books), books)
	}

	byTitle := map[string]bookRow{}
	for _, b := range books {
		byTitle[b.title] = b
	}

	bookOne, ok := byTitle["Book One"]
	if !ok {
		t.Fatalf("missing 'Book One', got titles: %+v", byTitle)
	}
	if bookOne.folderPath != "BookOne" {
		t.Errorf("Book One folder_path = %q, want %q", bookOne.folderPath, "BookOne")
	}
	if bookOne.durationMs <= 0 {
		t.Errorf("Book One duration_ms = %d, want > 0", bookOne.durationMs)
	}

	bookTwo, ok := byTitle["Book Two"]
	if !ok {
		t.Fatalf("missing 'Book Two', got titles: %+v", byTitle)
	}
	if bookTwo.durationMs <= 0 {
		t.Errorf("Book Two duration_ms = %d, want > 0", bookTwo.durationMs)
	}
	files := fetchFiles(t, database, bookTwo.id)
	if len(files) != 2 {
		t.Fatalf("Book Two: expected 2 files, got %d", len(files))
	}
	if files[0].relPath != "second.mp3" || files[1].relPath != "first.mp3" {
		t.Errorf("Book Two file order = [%s, %s], want [second.mp3, first.mp3] (tag track order)", files[0].relPath, files[1].relPath)
	}
	for _, f := range files {
		if f.durationMs <= 0 {
			t.Errorf("Book Two file %s duration_ms = %d, want > 0", f.relPath, f.durationMs)
		}
	}

	rootBook, ok := byTitle["Root Book"]
	if !ok {
		t.Fatalf("missing 'Root Book', got titles: %+v", byTitle)
	}
	if rootBook.folderPath != "RootBook.m4b" {
		t.Errorf("Root Book folder_path = %q, want %q", rootBook.folderPath, "RootBook.m4b")
	}
	rootFiles := fetchFiles(t, database, rootBook.id)
	if len(rootFiles) != 1 || rootFiles[0].relPath != "" {
		t.Fatalf("Root Book files = %+v, want one entry with rel_path \"\"", rootFiles)
	}

	// Book 4: title/series/series_seq/narrators/chapters all resolved from
	// folder-name convention + the metadata.json sidecar.
	sidecarFolderPath := "SidecarAuthor/Sidecar Series/01 - Title"
	sidecarBook, ok := fetchBookByFolder(t, database, libID, sidecarFolderPath)
	if !ok {
		t.Fatalf("missing sidecar book at folder_path %q, got: %+v", sidecarFolderPath, books)
	}
	if sidecarBook.title != "Title" {
		t.Errorf("sidecar book title = %q, want %q", sidecarBook.title, "Title")
	}
	if sidecarBook.series != "Sidecar Series" {
		t.Errorf("sidecar book series = %q, want %q", sidecarBook.series, "Sidecar Series")
	}
	if sidecarBook.seriesSeq != "1" {
		t.Errorf("sidecar book series_seq = %q, want %q", sidecarBook.seriesSeq, "1")
	}
	if sidecarBook.narrators != `["Jane Narrator"]` {
		t.Errorf("sidecar book narrators = %q, want %q", sidecarBook.narrators, `["Jane Narrator"]`)
	}
	sidecarChapters := fetchChapters(t, database, sidecarBook.id)
	wantChapters := []chapterRowDB{
		{title: "Chapter 1", startMs: 0, endMs: 1000},
		{title: "Chapter 2", startMs: 1000, endMs: 2000},
	}
	if len(sidecarChapters) != len(wantChapters) {
		t.Fatalf("sidecar book chapters = %+v, want %+v", sidecarChapters, wantChapters)
	}
	for i, c := range sidecarChapters {
		if c != wantChapters[i] {
			t.Errorf("sidecar book chapter %d = %+v, want %+v", i, c, wantChapters[i])
		}
	}

	// Book 5: main.mp3 plus a nested Bonus/extra.mp3 must be one book.
	mergeFolderPath := "MergeAuthor/Other"
	mergeBook, ok := fetchBookByFolder(t, database, libID, mergeFolderPath)
	if !ok {
		t.Fatalf("missing merged book at folder_path %q, got: %+v", mergeFolderPath, books)
	}
	mergeFiles := fetchFiles(t, database, mergeBook.id)
	if len(mergeFiles) != 2 {
		t.Fatalf("merged book: expected 2 files, got %d: %+v", len(mergeFiles), mergeFiles)
	}

	// Rescan: nothing changed on disk, so scan_hash should cause every book
	// to be skipped (no updated_at change).
	before := map[int64]int64{}
	for _, b := range books {
		before[b.id] = b.updatedAt
	}
	if err := s.ScanLibrary(ctx, libID); err != nil {
		t.Fatalf("ScanLibrary (rescan): %v", err)
	}
	after := fetchBooks(t, database, libID)
	if len(after) != 5 {
		t.Fatalf("expected 5 books after rescan, got %d", len(after))
	}
	for _, b := range after {
		if b.updatedAt != before[b.id] {
			t.Errorf("book %d (%s) updated_at changed on no-op rescan: %d -> %d", b.id, b.title, before[b.id], b.updatedAt)
		}
	}

	// Delete Book Two's folder, rescan, and confirm it's removed.
	if err := os.RemoveAll(filepath.Join(root, "BookTwo")); err != nil {
		t.Fatal(err)
	}
	if err := s.ScanLibrary(ctx, libID); err != nil {
		t.Fatalf("ScanLibrary (after delete): %v", err)
	}
	final := fetchBooks(t, database, libID)
	if len(final) != 4 {
		t.Fatalf("expected 4 books after removing BookTwo, got %d: %+v", len(final), final)
	}
	for _, b := range final {
		if b.title == "Book Two" {
			t.Errorf("Book Two still present after its folder was removed")
		}
	}

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM books WHERE id = ?`, bookTwo.id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("Book Two row (id=%d) still exists after removal", bookTwo.id)
	}
}
