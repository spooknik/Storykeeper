package library

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

var audioExts = map[string]bool{
	".m4b":  true,
	".m4a":  true,
	".mp3":  true,
	".ogg":  true,
	".opus": true,
	".flac": true,
	".wav":  true,
	".aac":  true,
}

func isAudioExt(name string) bool {
	return audioExts[strings.ToLower(filepath.Ext(name))]
}

// isHiddenOrIgnored reports whether a file/dir name should be skipped
// entirely: dotfiles/dotdirs and Synology's @eaDir thumbnail cache.
func isHiddenOrIgnored(name string) bool {
	return strings.HasPrefix(name, ".") || name == "@eaDir"
}

// walkAudioFiles walks root and returns every audio file found, as paths
// relative to root using forward slashes. Hidden files/dirs and @eaDir are
// skipped entirely.
//
// An unreadable directory below root (typically a permission problem) is
// logged and skipped rather than failing the whole library; partial is then
// true so the caller knows not to treat books under it as removed.
//
// A failure on root itself - including a root that does not exist, e.g. an
// unmounted share - is always returned as an error. Reporting it as an empty
// walk would let the caller conclude every book in the library is gone.
func walkAudioFiles(root string) (out []string, partial bool, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			if os.IsNotExist(err) {
				return nil
			}
			slog.Warn("library scan: cannot read, skipping", "path", p, "err", err)
			partial = true
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if p != root && isHiddenOrIgnored(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !isAudioExt(name) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, partial, nil
}
