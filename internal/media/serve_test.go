package media

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const audioLen = 10000

// writeAudio creates a temp file of audioLen known bytes and returns its path
// and contents.
func writeAudio(t *testing.T, name string) (string, []byte) {
	t.Helper()
	data := make([]byte, audioLen)
	for i := range data {
		data[i] = byte(i % 251)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	return p, data
}

func doAudio(t *testing.T, method, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, "/media/books/1/files/0", nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	ServeAudio(rec, req, path)
	return rec.Result()
}

func body(t *testing.T, res *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	res.Body.Close()
	return b
}

// TestServeAudioProbeRange is the exact request iOS Safari makes before it will
// play anything.
func TestServeAudioProbeRange(t *testing.T) {
	p, data := writeAudio(t, "book.m4b")
	res := doAudio(t, "GET", p, map[string]string{"Range": "bytes=0-1"})

	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", res.StatusCode)
	}
	if got, want := res.Header.Get("Content-Range"), "bytes 0-1/10000"; got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
	if got, want := res.Header.Get("Content-Length"), "2"; got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got, want := res.Header.Get("Accept-Ranges"), "bytes"; got != want {
		t.Errorf("Accept-Ranges = %q, want %q", got, want)
	}
	if got, want := res.Header.Get("Content-Type"), "audio/mp4"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got := body(t, res); !bytes.Equal(got, data[:2]) {
		t.Errorf("body = %v, want %v", got, data[:2])
	}
}

func TestServeAudioContentTypeMP3(t *testing.T) {
	p, _ := writeAudio(t, "book.mp3")
	res := doAudio(t, "GET", p, map[string]string{"Range": "bytes=0-1"})
	if got, want := res.Header.Get("Content-Type"), "audio/mpeg"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if res.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want 206", res.StatusCode)
	}
	body(t, res)
}

func TestServeAudioOpenEndedRange(t *testing.T) {
	p, data := writeAudio(t, "book.m4b")
	res := doAudio(t, "GET", p, map[string]string{"Range": "bytes=9990-"})

	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", res.StatusCode)
	}
	if got, want := res.Header.Get("Content-Range"), "bytes 9990-9999/10000"; got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
	if got, want := res.Header.Get("Content-Length"), "10"; got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got := body(t, res); !bytes.Equal(got, data[9990:]) {
		t.Errorf("body = %v, want %v", got, data[9990:])
	}
}

func TestServeAudioUnsatisfiableRange(t *testing.T) {
	p, _ := writeAudio(t, "book.m4b")
	res := doAudio(t, "GET", p, map[string]string{"Range": "bytes=20000-30000"})

	if res.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", res.StatusCode)
	}
	if got, want := res.Header.Get("Content-Range"), "bytes */10000"; got != want {
		t.Errorf("Content-Range = %q, want %q", got, want)
	}
	body(t, res)
}

func TestServeAudioNoRange(t *testing.T) {
	p, data := writeAudio(t, "book.m4b")
	res := doAudio(t, "GET", p, nil)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got, want := res.Header.Get("Content-Length"), "10000"; got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got, want := res.Header.Get("Accept-Ranges"), "bytes"; got != want {
		t.Errorf("Accept-Ranges = %q, want %q", got, want)
	}
	if got, want := res.Header.Get("Cache-Control"), "private, max-age=0, must-revalidate"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if got, want := res.Header.Get("X-Content-Type-Options"), "nosniff"; got != want {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, want)
	}
	if res.Header.Get("Content-Disposition") != "" {
		t.Errorf("Content-Disposition should not be set, got %q", res.Header.Get("Content-Disposition"))
	}
	if got := body(t, res); !bytes.Equal(got, data) {
		t.Errorf("body length = %d, want %d", len(got), len(data))
	}
}

func TestServeAudioHEAD(t *testing.T) {
	p, _ := writeAudio(t, "book.m4b")
	res := doAudio(t, "HEAD", p, nil)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got, want := res.Header.Get("Content-Length"), "10000"; got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got := body(t, res); len(got) != 0 {
		t.Errorf("HEAD body = %d bytes, want 0", len(got))
	}
}

func TestServeAudioETagAndIfNoneMatch(t *testing.T) {
	p, _ := writeAudio(t, "book.m4b")

	res := doAudio(t, "GET", p, nil)
	etag := res.Header.Get("ETag")
	body(t, res)
	if etag == "" {
		t.Fatal("no ETag")
	}
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Errorf("ETag %q is not a quoted strong validator", etag)
	}

	res2 := doAudio(t, "GET", p, map[string]string{"If-None-Match": etag})
	if res2.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", res2.StatusCode)
	}
	if got := body(t, res2); len(got) != 0 {
		t.Errorf("304 body = %d bytes, want 0", len(got))
	}
}

func TestServeAudioMissingAndDirectory(t *testing.T) {
	dir := t.TempDir()

	res := doAudio(t, "GET", filepath.Join(dir, "nope.m4b"), nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("missing file status = %d, want 404", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("404 Content-Type = %q, want text/plain", ct)
	}
	if res.Header.Get("Location") != "" {
		t.Error("404 must not redirect")
	}
	body(t, res)

	res2 := doAudio(t, "GET", dir, nil)
	if res2.StatusCode != http.StatusNotFound {
		t.Errorf("directory status = %d, want 404", res2.StatusCode)
	}
	body(t, res2)
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)

	// Normal nested join.
	got := SafeJoin(root, "Author", "Book", "file.m4b")
	want := filepath.Join(rootAbs, "Author", "Book", "file.m4b")
	if got != want {
		t.Errorf("SafeJoin nested = %q, want %q", got, want)
	}

	// Traversal escapes must be rejected outright.
	escapes := [][]string{
		{".."},
		{"..", "evil.txt"},
		{"a", "..", "..", "evil.txt"},
		{filepath.Join("x", "..", "..", "etc", "passwd")},
		{"a", filepath.Join("..", "..") + sep + "evil.txt"},
	}
	for _, parts := range escapes {
		if got := SafeJoin(root, parts...); got != "" {
			t.Errorf("SafeJoin(root, %q) = %q, want \"\" (escape)", parts, got)
		}
	}

	// Absolute-path injection must never resolve outside root. filepath.Join
	// re-roots it, so the contract is containment rather than a specific string.
	for _, abs := range []string{
		sep + filepath.Join("etc", "passwd"),
		sep + filepath.Join("Windows", "win.ini"),
	} {
		got := SafeJoin(root, abs)
		if got == "" {
			continue // rejected outright is also fine
		}
		if !strings.HasPrefix(got, rootAbs+sep) {
			t.Errorf("SafeJoin(root, %q) = %q, escaped root %q", abs, got, rootAbs)
		}
	}
}
