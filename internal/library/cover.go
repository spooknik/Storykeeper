package library

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"go.senan.xyz/taglib"
)

var coverImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// extractCover finds cover art for a book: first any embedded image on the
// files (in play order), then (for folder books only) cover.jpg/png/webp,
// folder.jpg, or a single lone image file in the book folder. It returns
// the raw bytes, a sniffed extension ("jpg"/"png"/"webp"), and - only when
// the cover came from a folder image file rather than embedded art - that
// file's own name (used to fold it into the scan hash). Bytes are nil if
// no cover was found.
func extractCover(bookDirAbs string, files []*fileMeta, isSingleFile bool) (data []byte, ext string, coverFileName string) {
	for _, f := range files {
		img, err := taglib.ReadImage(f.absPath)
		if err == nil && len(img) > 0 {
			return img, sniffImageExt(img), ""
		}
	}

	if isSingleFile {
		// A single-file book has no folder of its own to search (its
		// "folder" is the shared library root/subdir it lives in, which
		// may be shared with unrelated books), so we only trust embedded
		// art for these.
		return nil, "", ""
	}

	pick := candidateCoverFolderFile(bookDirAbs, isSingleFile)
	if pick == "" {
		return nil, "", ""
	}

	fileData, err := os.ReadFile(filepath.Join(bookDirAbs, pick))
	if err != nil {
		return nil, "", ""
	}
	return fileData, sniffImageExt(fileData), pick
}

// candidateCoverFolderFile returns the name of the on-disk cover image
// file that extractCover would fall back to for a folder book if no audio
// file supplies embedded art: cover.jpg/png/webp, folder.jpg, or a single
// lone image file. It does not read the file, so it's cheap enough to call
// at scan-hash time (see gatherHashExtras): a changed cover.jpg then
// invalidates the hash without needing a (comparatively expensive) tag
// read first. An embedded-art change is already covered by its audio
// file's own size/mtime, which is always part of the hash.
func candidateCoverFolderFile(bookDirAbs string, isSingleFile bool) string {
	if isSingleFile {
		return ""
	}

	entries, err := os.ReadDir(bookDirAbs)
	if err != nil {
		return ""
	}

	var named string
	var loneImages []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		ext := filepath.Ext(lower)
		base := strings.TrimSuffix(lower, ext)
		if coverImageExts[ext] && (base == "cover" || base == "folder") {
			named = e.Name()
		}
		if coverImageExts[ext] {
			loneImages = append(loneImages, e.Name())
		}
	}

	if named != "" {
		return named
	}
	if len(loneImages) == 1 {
		return loneImages[0]
	}
	return ""
}

func sniffImageExt(data []byte) string {
	ct := http.DetectContentType(data)
	switch {
	case strings.Contains(ct, "png"):
		return "png"
	case strings.Contains(ct, "webp"):
		return "webp"
	default:
		return "jpg"
	}
}

// writeCoverFile writes cover bytes to <dataDir>/covers/<bookID>/orig.<ext>,
// removing any stale orig.* file left from a previous scan (e.g. if the
// cover's format changed). It returns the cover_path value to store,
// relative to dataDir with forward slashes.
func writeCoverFile(dataDir string, bookID int64, data []byte, ext string) (string, error) {
	dir := filepath.Join(dataDir, "covers", strconv.FormatInt(bookID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "orig.") {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	fname := "orig." + ext
	if err := os.WriteFile(filepath.Join(dir, fname), data, 0o644); err != nil {
		return "", err
	}
	return path.Join("covers", strconv.FormatInt(bookID, 10), fname), nil
}

// removeCoverDir removes any cover files stored for a book, e.g. when the
// book is deleted from the library.
func removeCoverDir(dataDir string, bookID int64) {
	_ = os.RemoveAll(filepath.Join(dataDir, "covers", strconv.FormatInt(bookID, 10)))
}
