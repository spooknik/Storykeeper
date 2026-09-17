package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ScanPath scans a single newly-created subtree of libraryID (e.g. the
// destination folder of a just-completed upload) rather than the whole
// library root, so a new book appears without waiting for the next full
// ScanLibrary pass. Unlike ScanLibrary it never removes books: it only
// upserts the small number of book groups found under absPath, so a book
// elsewhere in the library that happens to be temporarily unreadable (or
// simply outside absPath) is never touched.
//
// absPath must resolve inside the library's root; otherwise an error is
// returned.
//
// Concurrency mirrors ScanLibrary's per-library coalescing: if a scan of the
// same library (targeted or full) is already running, ScanPath queues a
// rescan and returns nil immediately rather than walking concurrently. When
// the running scan is a full ScanLibrary, its repeat pass picks up the new
// file; when it's another targeted scan, the queued rescan is picked up the
// next time any scan of that library runs.
func (s *Scanner) ScanPath(ctx context.Context, libraryID int64, absPath string) error {
	var libPath string
	err := s.DB.QueryRowContext(ctx, `SELECT path FROM libraries WHERE id = ?`, libraryID).Scan(&libPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load library %d: %w", libraryID, err)
	}

	libRoot := filepath.Clean(libPath)
	target := filepath.Clean(absPath)
	if target != libRoot && !strings.HasPrefix(target, libRoot+string(filepath.Separator)) {
		return fmt.Errorf("scan path %q is not inside library %d root %q", absPath, libraryID, libPath)
	}

	// The uploaded folder may itself be a CD/Disc-style subfolder of an
	// existing book (e.g. a new "CD2" dropped alongside an existing "CD1").
	// If its parent (still inside the root) is itself a book folder by
	// group.go's rules, scan the parent subtree instead so the two combine
	// into one book rather than the upload creating a second, wrong book.
	if parent := filepath.Dir(target); parent != target && parent != libRoot && strings.HasPrefix(parent, libRoot+string(filepath.Separator)) {
		if parentIsBookDir(parent) {
			target = parent
		}
	}

	if !s.beginScan(libraryID) {
		slog.Debug("targeted library scan: a scan is already running, queued rescan", "library_id", libraryID, "path", target)
		return nil
	}
	defer s.endScan(libraryID)

	start := time.Now()

	subPaths, _, err := walkAudioFiles(target)
	if err != nil {
		return fmt.Errorf("walk %q: %w", target, err)
	}

	relPrefix := ""
	if rel, err := filepath.Rel(libRoot, target); err == nil && rel != "." {
		relPrefix = filepath.ToSlash(rel)
	}
	relPaths := make([]string, len(subPaths))
	for i, p := range subPaths {
		if relPrefix == "" {
			relPaths[i] = p
		} else {
			relPaths[i] = relPrefix + "/" + p
		}
	}

	groups := groupBooks(relPaths)
	haveFFprobe := ffprobeAvailable()

	var added, updated, skipped int
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, err := s.processBookGroup(ctx, libraryID, libPath, g, haveFFprobe)
		if err != nil {
			slog.Error("targeted library scan: book failed", "library_id", libraryID, "folder", g.FolderPath, "err", err)
			continue
		}
		switch status {
		case statusAdded:
			added++
		case statusUpdated:
			updated++
		case statusSkipped:
			skipped++
		}
	}

	slog.Info("library scan complete",
		"library_id", libraryID,
		"added", added,
		"updated", updated,
		"removed", 0,
		"skipped", skipped,
		"duration", time.Since(start),
		"targeted", true,
	)
	return nil
}

// parentIsBookDir reports whether dir would be treated as a book folder by
// groupBooks: it either directly contains an audio file, or has a
// CD/Disc/Part-style subdirectory (see cdPattern) that would roll up into
// it. It's a cheap, approximate pre-check used only to decide whether
// ScanPath should walk dir's parent instead of dir itself; the actual
// grouping of whatever gets walked is always done by the real groupBooks.
func parentIsBookDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	hasCDChild := false
	for _, e := range entries {
		name := e.Name()
		if isHiddenOrIgnored(name) {
			continue
		}
		if e.IsDir() {
			if cdPattern.MatchString(name) {
				hasCDChild = true
			}
			continue
		}
		if isAudioExt(name) {
			return true
		}
	}
	return hasCDChild
}
