package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makePNG writes a w x h gradient PNG at <dir>/orig.png.
func makePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x40, A: 0xff})
		}
	}
	p := filepath.Join(t.TempDir(), "orig.png")
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatalf("encode png: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func doCover(t *testing.T, path string, size int) *http.Response {
	t.Helper()
	req := httptest.NewRequest("GET", "/media/books/1/cover", nil)
	rec := httptest.NewRecorder()
	ServeCover(rec, req, path, size)
	return rec.Result()
}

func decodeJPEGBody(t *testing.T, res *http.Response) image.Image {
	t.Helper()
	if got, want := res.Header.Get("Content-Type"), "image/jpeg"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}
	b := body(t, res)
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("response is not decodable JPEG: %v", err)
	}
	return img
}

func assertSize(t *testing.T, img image.Image, w, h int) {
	t.Helper()
	gw, gh := img.Bounds().Dx(), img.Bounds().Dy()
	if abs(gw-w) > 1 || abs(gh-h) > 1 {
		t.Errorf("size = %dx%d, want %dx%d (+-1)", gw, gh, w, h)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestServeCoverResize200(t *testing.T) {
	orig := makePNG(t, 1200, 800)
	dir := filepath.Dir(orig)

	res := doCover(t, orig, 200)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got, want := res.Header.Get("Cache-Control"), "private, max-age=86400"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if res.Header.Get("ETag") == "" {
		t.Error("no ETag")
	}
	assertSize(t, decodeJPEGBody(t, res), 200, 133)

	cache := filepath.Join(dir, "200.jpg")
	fi, err := os.Stat(cache)
	if err != nil {
		t.Fatalf("cache file %s missing: %v", cache, err)
	}

	// Second request must be served from the existing cache file.
	res2 := doCover(t, orig, 200)
	assertSize(t, decodeJPEGBody(t, res2), 200, 133)
	fi2, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	if !fi2.ModTime().Equal(fi.ModTime()) {
		t.Errorf("cache regenerated: mtime %v -> %v", fi.ModTime(), fi2.ModTime())
	}
}

func TestServeCoverResize600(t *testing.T) {
	orig := makePNG(t, 1200, 800)
	res := doCover(t, orig, 600)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	assertSize(t, decodeJPEGBody(t, res), 600, 400)
	if _, err := os.Stat(filepath.Join(filepath.Dir(orig), "600.jpg")); err != nil {
		t.Errorf("600.jpg not written: %v", err)
	}
}

func TestServeCoverNoUpscale(t *testing.T) {
	orig := makePNG(t, 100, 100)
	res := doCover(t, orig, 600)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	assertSize(t, decodeJPEGBody(t, res), 100, 100)
}

func TestServeCoverOriginal(t *testing.T) {
	orig := makePNG(t, 1200, 800)
	want, err := os.ReadFile(orig)
	if err != nil {
		t.Fatal(err)
	}

	res := doCover(t, orig, 0)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got, w := res.Header.Get("Content-Type"), "image/png"; got != w {
		t.Errorf("Content-Type = %q, want %q", got, w)
	}
	if got := body(t, res); !bytes.Equal(got, want) {
		t.Errorf("body = %d bytes, want the %d original bytes", len(got), len(want))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(orig), "0.jpg")); err == nil {
		t.Error("size 0 must not generate a cache file")
	}
}

func TestServeCoverCorrupt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "orig.png")
	if err := os.WriteFile(p, []byte("this is definitely not a PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := doCover(t, p, 200)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
	body(t, res)
}

func TestServeCoverMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "orig.jpg")
	for _, size := range []int{0, 200, 600} {
		res := doCover(t, p, size)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("size %d: status = %d, want 404", size, res.StatusCode)
		}
		body(t, res)
	}
}

func TestServeCoverRegeneratesWhenStale(t *testing.T) {
	orig := makePNG(t, 1200, 800)
	cache := filepath.Join(filepath.Dir(orig), "200.jpg")

	body(t, doCover(t, orig, 200))
	fi, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	// Age the cache a minute behind the original.
	stale := fi.ModTime().Add(-time.Minute)
	if err := os.Chtimes(cache, stale, stale); err != nil {
		t.Fatal(err)
	}

	body(t, doCover(t, orig, 200))
	fi2, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	if !fi2.ModTime().After(stale) {
		t.Errorf("stale cache was not regenerated (mtime %v)", fi2.ModTime())
	}
}

func TestFitWithin(t *testing.T) {
	cases := []struct{ w, h, max, ww, wh int }{
		{1200, 800, 200, 200, 133},
		{1200, 800, 600, 600, 400},
		{800, 1200, 200, 133, 200},
		{100, 100, 600, 100, 100},
		{600, 600, 600, 600, 600},
		{4000, 10, 200, 200, 1},
	}
	for _, c := range cases {
		gw, gh := fitWithin(c.w, c.h, c.max)
		if gw != c.ww || gh != c.wh {
			t.Errorf("fitWithin(%d,%d,%d) = %d,%d want %d,%d", c.w, c.h, c.max, gw, gh, c.ww, c.wh)
		}
	}
}
