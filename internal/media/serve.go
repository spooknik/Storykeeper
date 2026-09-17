// Package media serves audio files with correct HTTP range semantics and
// serves/produces cover images.
package media

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ContentType returns the MIME type Safari expects for an audio file extension.
func ContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m4b", ".m4a", ".mp4":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".wav":
		return "audio/wav"
	case ".aac":
		return "audio/aac"
	}
	return "application/octet-stream"
}

// SafeJoin joins parts under root and guarantees the result stays inside root.
// Returns "" if the path escapes.
func SafeJoin(root string, parts ...string) string {
	p := filepath.Join(append([]string{root}, parts...)...)
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	pAbs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(rootAbs, pAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return pAbs
}

// notFound writes a plain-text 404. Media routes must never redirect: Safari's
// range probe treats a 3xx on an <audio> source as a failure.
func notFound(w http.ResponseWriter) {
	http.Error(w, "not found", http.StatusNotFound)
}

// strongETag builds a quoted, strong validator from size and mtime. Strong (not
// W/) because a weak validator disables If-Range, and Safari relies on If-Range
// to resume a partial download without re-fetching from zero.
func strongETag(fi os.FileInfo) string {
	return fmt.Sprintf("%q", fmt.Sprintf("%x-%x", fi.Size(), fi.ModTime().UnixNano()))
}

// ServeAudio streams absPath honouring Range requests (206, Content-Range, Accept-Ranges).
//
// All range handling is delegated to http.ServeContent, which implements single
// and multipart ranges, 416 with "Content-Range: bytes */size", If-Range and
// conditional GET, plus HEAD, correctly. Hand-rolling any of that is how the
// iOS Safari probe (Range: bytes=0-1) ends up with a 200 and silent playback
// failure.
func ServeAudio(w http.ResponseWriter, r *http.Request, absPath string) {
	f, err := os.Open(absPath)
	if err != nil {
		notFound(w)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil || fi.IsDir() || !fi.Mode().IsRegular() {
		notFound(w)
		return
	}

	h := w.Header()
	// Preset so ServeContent does not sniff (sniffing an m4b yields video/mp4).
	h.Set("Content-Type", ContentType(absPath))
	// ServeContent sets this too; set it explicitly so it is present even on
	// the conditional-request paths.
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "private, max-age=0, must-revalidate")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("ETag", strongETag(fi))

	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
