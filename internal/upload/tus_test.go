package upload

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spooknik/storykeeper/internal/db"
)

const basePath = "/api/v1/upload/"

// --- helpers ---------------------------------------------------------------

func metadataHeader(kv map[string]string) string {
	parts := make([]string, 0, len(kv))
	for k, v := range kv {
		parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(v)))
	}
	return strings.Join(parts, ",")
}

// testServer wires a Service into an httptest server mounted at basePath,
// backed by a temp DataDir and a temp SQLite DB with one library row
// pointing at a temp folder. It returns the server, the library ID, the
// library's on-disk root, and a channel that receives (libraryID, folder)
// each time onComplete fires.
func testServer(t *testing.T) (*httptest.Server, int64, string, chan completion) {
	t.Helper()

	dataDir := t.TempDir()
	libDir := t.TempDir()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqldb, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = sqldb.Close() })

	res, err := sqldb.Exec(`INSERT INTO libraries (name, path, created_at) VALUES (?, ?, ?)`,
		"Test Library", libDir, db.Now())
	if err != nil {
		t.Fatalf("insert library: %v", err)
	}
	libID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}

	doneCh := make(chan completion, 8)
	svc, err := New(Config{DataDir: dataDir, BasePath: basePath}, sqldb,
		func(ctx context.Context, libraryID int64, folder string) {
			doneCh <- completion{libraryID: libraryID, folder: folder}
		})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle(basePath, svc.Handler())
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return ts, libID, libDir, doneCh
}

type completion struct {
	libraryID int64
	folder    string
}

func createUpload(t *testing.T, ts *httptest.Server, size int, meta map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+basePath, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", strconv.Itoa(size))
	req.Header.Set("Upload-Metadata", metadataHeader(meta))
	req.Header.Set("X-Storykeeper", "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	return resp
}

func patchChunk(t *testing.T, loc string, offset int, chunk []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, loc, bytes.NewReader(chunk))
	if err != nil {
		t.Fatalf("new patch request: %v", err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Offset", strconv.Itoa(offset))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("X-Storykeeper", "1")
	req.ContentLength = int64(len(chunk))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch chunk: %v", err)
	}
	return resp
}

func headOffset(t *testing.T, loc string) (int, *http.Response) {
	t.Helper()
	req, err := http.NewRequest(http.MethodHead, loc, nil)
	if err != nil {
		t.Fatalf("new head request: %v", err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	off, err := strconv.Atoi(resp.Header.Get("Upload-Offset"))
	if err != nil {
		t.Fatalf("parse Upload-Offset %q: %v", resp.Header.Get("Upload-Offset"), err)
	}
	return off, resp
}

func waitComplete(t *testing.T, ch chan completion) completion {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for upload completion")
		return completion{}
	}
}

// --- tests -------------------------------------------------------------

func TestUploadLifecycle_TwoChunks(t *testing.T) {
	ts, libID, _, doneCh := testServer(t)

	content := bytes.Repeat([]byte("storykeeper-audio-bytes-"), 1000) // deterministic content
	chunk1, chunk2 := content[:len(content)/2], content[len(content)/2:]

	createResp := createUpload(t, ts, len(content), map[string]string{
		"filename":   "chapter one.m4b",
		"library_id": strconv.FormatInt(libID, 10),
		"author":     "Jane Doe",
		"title":      "My Great Book",
	})
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create: got status %d, body %s", createResp.StatusCode, body)
	}
	loc := createResp.Header.Get("Location")
	if loc == "" {
		t.Fatal("create response missing Location header")
	}

	p1 := patchChunk(t, loc, 0, chunk1)
	defer p1.Body.Close()
	if p1.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(p1.Body)
		t.Fatalf("patch 1: got status %d, body %s", p1.StatusCode, body)
	}
	if got := p1.Header.Get("Upload-Offset"); got != strconv.Itoa(len(chunk1)) {
		t.Fatalf("patch 1 Upload-Offset = %q, want %d", got, len(chunk1))
	}

	off, headResp := headOffset(t, loc)
	headResp.Body.Close()
	if off != len(chunk1) {
		t.Fatalf("HEAD offset = %d, want %d", off, len(chunk1))
	}

	p2 := patchChunk(t, loc, len(chunk1), chunk2)
	defer p2.Body.Close()
	if p2.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(p2.Body)
		t.Fatalf("patch 2: got status %d, body %s", p2.StatusCode, body)
	}
	if got := p2.Header.Get("Upload-Offset"); got != strconv.Itoa(len(content)) {
		t.Fatalf("patch 2 Upload-Offset = %q, want %d", got, len(content))
	}

	done := waitComplete(t, doneCh)
	if done.libraryID != libID {
		t.Fatalf("onComplete libraryID = %d, want %d", done.libraryID, libID)
	}
	wantDir := filepath.Base(done.folder)
	if wantDir != "Jane Doe - My Great Book" {
		t.Fatalf("dest folder = %q, want %q", wantDir, "Jane Doe - My Great Book")
	}

	destPath := filepath.Join(done.folder, "chapter one.m4b")
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest file %s: %v", destPath, err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("dest file bytes mismatch: got %d bytes, want %d bytes", len(got), len(content))
	}
}

func TestUploadLifecycle_NoAuthorTitle_UsesUploadsFolder(t *testing.T) {
	ts, libID, libDir, doneCh := testServer(t)

	content := []byte("small audio file contents")
	createResp := createUpload(t, ts, len(content), map[string]string{
		"filename":   "solo book.mp3",
		"library_id": strconv.FormatInt(libID, 10),
	})
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got status %d", createResp.StatusCode)
	}
	loc := createResp.Header.Get("Location")

	p := patchChunk(t, loc, 0, content)
	p.Body.Close()
	if p.StatusCode != http.StatusNoContent {
		t.Fatalf("patch: got status %d", p.StatusCode)
	}

	done := waitComplete(t, doneCh)
	wantDir := filepath.Join(libDir, "Uploads", "solo book")
	if done.folder != wantDir {
		t.Fatalf("dest folder = %q, want %q", done.folder, wantDir)
	}
	if _, err := os.Stat(filepath.Join(done.folder, "solo book.mp3")); err != nil {
		t.Fatalf("dest file missing: %v", err)
	}
}

func TestCreate_RejectsDisallowedExtension(t *testing.T) {
	ts, libID, _, _ := testServer(t)

	resp := createUpload(t, ts, 10, map[string]string{
		"filename":   "malware.exe",
		"library_id": strconv.FormatInt(libID, 10),
	})
	defer resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("got status %d, want 4xx", resp.StatusCode)
	}
}

func TestCreate_RejectsMissingLibraryID(t *testing.T) {
	ts, _, _, _ := testServer(t)

	resp := createUpload(t, ts, 10, map[string]string{
		"filename": "book.m4b",
	})
	defer resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("got status %d, want 4xx", resp.StatusCode)
	}
}

func TestCreate_RejectsUnknownLibraryID(t *testing.T) {
	ts, libID, _, _ := testServer(t)

	resp := createUpload(t, ts, 10, map[string]string{
		"filename":   "book.m4b",
		"library_id": strconv.FormatInt(libID+999, 10),
	})
	defer resp.Body.Close()
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Fatalf("got status %d, want 4xx", resp.StatusCode)
	}
}

func TestUploadLifecycle_DuplicateFolder_GetsSuffix(t *testing.T) {
	ts, libID, libDir, doneCh := testServer(t)

	upload := func(filename string, content []byte) completion {
		createResp := createUpload(t, ts, len(content), map[string]string{
			"filename":   filename,
			"library_id": strconv.FormatInt(libID, 10),
			"author":     "Same Author",
			"title":      "Same Title",
		})
		defer createResp.Body.Close()
		if createResp.StatusCode != http.StatusCreated {
			t.Fatalf("create: got status %d", createResp.StatusCode)
		}
		loc := createResp.Header.Get("Location")

		p := patchChunk(t, loc, 0, content)
		p.Body.Close()
		if p.StatusCode != http.StatusNoContent {
			t.Fatalf("patch: got status %d", p.StatusCode)
		}
		return waitComplete(t, doneCh)
	}

	// Files sharing author and title are parts of one book: same folder.
	first := upload("part1.m4b", []byte("first upload content"))
	second := upload("part2.m4b", []byte("second upload content, different"))
	// A colliding file name gets a " (2)" suffix instead of a new folder.
	third := upload("part1.m4b", []byte("re-upload of part1"))

	want := filepath.Join(libDir, "Same Author - Same Title")
	for i, got := range []string{first.folder, second.folder, third.folder} {
		if got != want {
			t.Fatalf("upload %d folder = %q, want %q", i+1, got, want)
		}
	}
	for _, name := range []string{"part1.m4b", "part2.m4b", "part1 (2).m4b"} {
		if _, err := os.Stat(filepath.Join(want, name)); err != nil {
			t.Fatalf("file %s missing: %v", name, err)
		}
	}
}

// --- sanitizer unit test --------------------------------------------------

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Normal Name", "Normal Name"},
		{"  trims spaces  ", "trims spaces"},
		{"path/traversal\\attempt", "pathtraversalattempt"},
		{"../../etc/passwd", "etcpasswd"},
		{"trailing dots...", "trailing dots"},
		{"trailing space ", "trailing space"},
		{"cont\x00rol\x1fchars", "controlchars"},
		{strings.Repeat("a", 250), strings.Repeat("a", 200)},
		{"", ""},
	}
	for _, tc := range cases {
		got := sanitizeName(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
