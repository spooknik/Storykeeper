package media

import (
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/draw"

	// Decoder registrations for image.Decode / image.DecodeConfig
	// (image/jpeg is imported above for encoding and registers itself too).
	_ "image/png"

	_ "golang.org/x/image/webp"
)

const (
	// maxCoverBytes and maxCoverDim bound what we are willing to decode.
	// DecodeConfig is cheap and reads only the header, so a decompression bomb
	// is rejected before any pixels are allocated.
	maxCoverBytes = 40 << 20
	maxCoverDim   = 12000

	jpegQuality = 85
)

var errBadImage = errors.New("media: undecodable or oversized image")

// coverLocks serialises generation per cache path so two concurrent requests
// for the same size do not both decode and both rename.
var coverLocks sync.Map // cache path -> *sync.Mutex

func lockFor(path string) *sync.Mutex {
	v, _ := coverLocks.LoadOrStore(path, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// imageContentType maps a cover extension to its MIME type.
func imageContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return "application/octet-stream"
}

// ServeCover serves the cover at absPath. size is 0 (original), 600 or 200.
func ServeCover(w http.ResponseWriter, r *http.Request, absPath string, size int) {
	if size <= 0 {
		serveImage(w, r, absPath, imageContentType(absPath))
		return
	}
	cache := filepath.Join(filepath.Dir(absPath), strconv.Itoa(size)+".jpg")
	if err := ensureResized(absPath, cache, size); err != nil {
		// A bad or hostile cover file is not a server fault; 404 and stay quiet.
		notFound(w)
		return
	}
	serveImage(w, r, cache, "image/jpeg")
}

// serveImage sends path with cover cache headers, again via ServeContent so
// ranges and conditional requests behave.
func serveImage(w http.ResponseWriter, r *http.Request, path, ctype string) {
	f, err := os.Open(path)
	if err != nil {
		notFound(w)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		notFound(w)
		return
	}

	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("ETag", strongETag(fi))

	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// ensureResized makes sure cache holds a JPEG of orig scaled to size, and
// regenerates it when it is missing or older than the original.
func ensureResized(orig, cache string, size int) error {
	oi, err := os.Stat(orig)
	if err != nil || !oi.Mode().IsRegular() {
		return errBadImage
	}
	if ci, err := os.Stat(cache); err == nil && ci.Mode().IsRegular() && !ci.ModTime().Before(oi.ModTime()) {
		return nil
	}

	mu := lockFor(cache)
	mu.Lock()
	defer mu.Unlock()

	// Re-check: another goroutine may have generated it while we waited.
	if ci, err := os.Stat(cache); err == nil && ci.Mode().IsRegular() && !ci.ModTime().Before(oi.ModTime()) {
		return nil
	}
	return generateResized(orig, cache, size, oi.Size())
}

func generateResized(orig, cache string, size int, origBytes int64) error {
	if origBytes <= 0 || origBytes >= maxCoverBytes {
		return errBadImage
	}
	f, err := os.Open(orig)
	if err != nil {
		return errBadImage
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return errBadImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxCoverDim || cfg.Height > maxCoverDim {
		return errBadImage
	}
	if _, err := f.Seek(0, 0); err != nil {
		return errBadImage
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return errBadImage
	}

	dstW, dstH := fitWithin(src.Bounds().Dx(), src.Bounds().Dy(), size)
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	// JPEG has no alpha: matte onto white so transparent PNG covers do not
	// come out with black edges.
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	return writeJPEGAtomic(cache, dst)
}

// writeJPEGAtomic encodes to a temp file in the destination directory and
// renames it into place, so a reader never sees a half-written cache file.
func writeJPEGAtomic(cache string, img image.Image) error {
	dir := filepath.Dir(cache)
	tmp, err := os.CreateTemp(dir, ".cover-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, cache)
}

// fitWithin scales w x h so the longest side is max, never upscaling.
func fitWithin(w, h, max int) (int, int) {
	if w <= 0 || h <= 0 {
		return 1, 1
	}
	if w <= max && h <= max {
		return w, h
	}
	if w >= h {
		nh := int(math.Round(float64(h) * float64(max) / float64(w)))
		return max, clampMin1(nh)
	}
	nw := int(math.Round(float64(w) * float64(max) / float64(h)))
	return clampMin1(nw), max
}

func clampMin1(v int) int {
	if v < 1 {
		return 1
	}
	return v
}
