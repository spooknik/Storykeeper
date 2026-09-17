// Package library walks library folders on disk and upserts books, files and
// chapters into the database.
//
// # Book folder_path convention
//
// books.folder_path is relative to the library root and unique per library.
// For a normal book (a directory containing audio files, or a parent
// directory spanning CD1/CD2/... subdirectories — see groupBooks) it is
// that directory's path. For an audio file sitting directly in the library
// root, there is no containing book directory, so the file is treated as
// its own single-file book: folder_path is set to the file's own name (its
// path relative to the root) and its single book_files.rel_path is ""
// (empty), meaning "the file lives directly at folder_path" rather than
// inside it. This keeps (library_id, folder_path) unique while still
// letting on-disk paths be reconstructed as
// filepath.Join(libraryRoot, folder_path, rel_path) uniformly.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spooknik/storykeeper/internal/db"
	"go.senan.xyz/taglib"
)

var ErrNotImplemented = errors.New("library: not implemented")

type Scanner struct {
	DB      *db.DB
	DataDir string // covers are written under DataDir/covers

	mu       sync.Mutex
	scanning map[int64]bool
	rescan   map[int64]bool // a scan was requested while one was running
}

func New(d *db.DB, dataDir string) *Scanner {
	return &Scanner{DB: d, DataDir: dataDir, scanning: make(map[int64]bool), rescan: make(map[int64]bool)}
}

type scanStatus int

const (
	statusSkipped scanStatus = iota
	statusAdded
	statusUpdated
)

// beginScan returns false if a scan of libraryID is already running. In that
// case it records that another pass is wanted, so the running scan repeats
// once it finishes and no filesystem change is lost.
func (s *Scanner) beginScan(libraryID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanning[libraryID] {
		s.rescan[libraryID] = true
		return false
	}
	s.scanning[libraryID] = true
	return true
}

// endScan releases the library and reports whether a rescan was requested
// while the scan ran.
func (s *Scanner) endScan(libraryID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.scanning, libraryID)
	again := s.rescan[libraryID]
	delete(s.rescan, libraryID)
	return again
}

// ScanLibrary walks one library root and reconciles the books table with disk.
// Concurrent calls for the same library coalesce: the second returns nil
// immediately and the first repeats its pass when done.
func (s *Scanner) ScanLibrary(ctx context.Context, libraryID int64) error {
	if !s.beginScan(libraryID) {
		slog.Debug("library scan already running, queued rescan", "library_id", libraryID)
		return nil
	}
	for {
		err := s.scanOnce(ctx, libraryID)
		again := s.endScan(libraryID)
		if err != nil || !again || ctx.Err() != nil {
			return err
		}
		if !s.beginScan(libraryID) {
			return nil
		}
	}
}

func (s *Scanner) scanOnce(ctx context.Context, libraryID int64) error {
	start := time.Now()

	var libPath string
	err := s.DB.QueryRowContext(ctx, `SELECT path FROM libraries WHERE id = ?`, libraryID).Scan(&libPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load library %d: %w", libraryID, err)
	}

	relPaths, err := walkAudioFiles(libPath)
	if err != nil {
		return fmt.Errorf("walk library %d root %q: %w", libraryID, libPath, err)
	}

	groups := groupBooks(relPaths)
	haveFFprobe := ffprobeAvailable()

	var added, updated, removed, skipped int
	valid := make(map[string]bool, len(groups))

	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		valid[g.FolderPath] = true
		status, err := s.processBookGroup(ctx, libraryID, libPath, g, haveFFprobe)
		if err != nil {
			slog.Error("library scan: book failed", "library_id", libraryID, "folder", g.FolderPath, "err", err)
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

	n, err := s.removeStale(ctx, libraryID, valid)
	if err != nil {
		slog.Error("library scan: remove stale books failed", "library_id", libraryID, "err", err)
	} else {
		removed = n
	}

	slog.Info("library scan complete",
		"library_id", libraryID,
		"added", added,
		"updated", updated,
		"removed", removed,
		"skipped", skipped,
		"duration", time.Since(start),
	)
	return nil
}

// ScanAll scans every library.
func (s *Scanner) ScanAll(ctx context.Context) error {
	ids, err := s.libraryIDs(ctx)
	if err != nil {
		return fmt.Errorf("list libraries: %w", err)
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.ScanLibrary(ctx, id); err != nil {
			slog.Error("library scan failed", "library_id", id, "err", err)
		}
	}
	return nil
}

func (s *Scanner) libraryIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM libraries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type existingBook struct {
	id          int64
	addedAt     int64
	asin        string
	isbn        string
	description string
	scanHash    string
}

// processBookGroup reconciles one book (as discovered by groupBooks) with
// the database, reading tags/properties/chapters and writing a cover only
// when the folder's scan_hash has actually changed.
func (s *Scanner) processBookGroup(ctx context.Context, libID int64, libPath string, g bookGroup, haveFFprobe bool) (scanStatus, error) {
	isSingleFile := len(g.Files) == 1 && g.Files[0] == ""

	stats := make([]*fileMeta, 0, len(g.Files))
	for _, f := range g.Files {
		var abs string
		if f == "" {
			abs = filepath.Join(libPath, filepath.FromSlash(g.FolderPath))
		} else {
			abs = filepath.Join(libPath, filepath.FromSlash(g.FolderPath), filepath.FromSlash(f))
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return statusSkipped, fmt.Errorf("stat %s: %w", abs, err)
		}
		stats = append(stats, &fileMeta{
			relPath: f,
			absPath: abs,
			size:    fi.Size(),
			mtimeMs: fi.ModTime().UnixMilli(),
		})
	}
	if len(stats) == 0 {
		return statusSkipped, nil
	}

	hash := computeScanHash(stats)

	var existing *existingBook
	{
		var eb existingBook
		row := s.DB.QueryRowContext(ctx,
			`SELECT id, added_at, asin, isbn, description, scan_hash FROM books WHERE library_id = ? AND folder_path = ?`,
			libID, g.FolderPath)
		switch err := row.Scan(&eb.id, &eb.addedAt, &eb.asin, &eb.isbn, &eb.description, &eb.scanHash); {
		case err == nil:
			existing = &eb
		case errors.Is(err, sql.ErrNoRows):
			existing = nil
		default:
			return statusSkipped, fmt.Errorf("load existing book: %w", err)
		}
	}

	if existing != nil && existing.scanHash == hash {
		return statusSkipped, nil
	}

	// Hash changed (or book is new): do the full tag/property read now.
	for _, f := range stats {
		tags, err := taglib.ReadTags(f.absPath)
		if err != nil {
			slog.Warn("library scan: read tags failed", "path", f.absPath, "err", err)
			tags = map[string][]string{}
		}
		props, err := taglib.ReadProperties(f.absPath)
		if err != nil {
			slog.Warn("library scan: read properties failed", "path", f.absPath, "err", err)
		}
		f.tags = tags
		f.durMs = props.Length.Milliseconds()
		f.codec = codecFor(f.absPath)
		f.bitrate = int(props.BitRate)
		disc, _ := parseLeadingInt(firstTag(tags, taglib.DiscNumber))
		track, trackOK := parseLeadingInt(firstTag(tags, taglib.TrackNumber))
		f.disc = disc
		f.track = track
		f.hasTrack = trackOK
	}

	orderFiles(stats)

	folderName := singleFileTitle(g.FolderPath)
	if !isSingleFile {
		folderName = filepath.Base(filepath.FromSlash(g.FolderPath))
	}
	meta := buildBookMeta(stats[0], folderName)

	var durSum int64
	for _, f := range stats {
		durSum += f.durMs
	}
	meta.durationMs = durSum

	bookDirAbs := filepath.Join(libPath, filepath.FromSlash(g.FolderPath))
	coverData, coverExt := extractCover(bookDirAbs, stats, isSingleFile)
	meta.coverData = coverData
	meta.coverExt = coverExt

	chapters := buildChapters(ctx, stats, haveFFprobe)

	if _, err := s.upsertBookAndChildren(ctx, libID, g.FolderPath, meta, stats, chapters, existing, hash); err != nil {
		return statusSkipped, fmt.Errorf("upsert book: %w", err)
	}

	if existing != nil {
		return statusUpdated, nil
	}
	return statusAdded, nil
}

// upsertBookAndChildren inserts or updates the book row and, in the same
// transaction, replaces its book_files and chapters rows. added_at is
// preserved across updates; a non-empty asin/isbn is never overwritten by a
// (possibly empty) tag value, and a non-empty DB description is kept when
// the tag comment is empty (admin edits win over empty tags).
func (s *Scanner) upsertBookAndChildren(ctx context.Context, libID int64, folderPath string, meta bookMeta, files []*fileMeta, chapters []chapterRow, existing *existingBook, scanHash string) (int64, error) {
	var bookID int64
	err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		addedAt := now
		asin := meta.asin
		isbn := meta.isbn
		description := meta.description

		if existing != nil {
			bookID = existing.id
			addedAt = existing.addedAt
			if existing.asin != "" {
				asin = existing.asin
			}
			if existing.isbn != "" {
				isbn = existing.isbn
			}
			if description == "" && existing.description != "" {
				description = existing.description
			}
		} else {
			res, err := tx.ExecContext(ctx,
				`INSERT INTO books (library_id, folder_path, title, added_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
				libID, folderPath, meta.title, now, now)
			if err != nil {
				return fmt.Errorf("insert book: %w", err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			bookID = id
		}

		coverPath := ""
		if len(meta.coverData) > 0 {
			cp, err := writeCoverFile(s.DataDir, bookID, meta.coverData, meta.coverExt)
			if err != nil {
				slog.Warn("library scan: write cover failed", "book_id", bookID, "err", err)
			} else {
				coverPath = cp
			}
		}

		_, err := tx.ExecContext(ctx, `
			UPDATE books SET
				title = ?, subtitle = ?, authors = ?, narrators = ?,
				series = ?, series_seq = ?, description = ?, published_year = ?,
				language = ?, duration_ms = ?, cover_path = ?, asin = ?, isbn = ?,
				added_at = ?, updated_at = ?, scan_hash = ?
			WHERE id = ?`,
			meta.title, meta.subtitle, meta.authorsJSON, meta.narratorsJSON,
			meta.series, meta.seriesSeq, description, meta.publishedYear,
			meta.language, meta.durationMs, coverPath, asin, isbn,
			addedAt, now, scanHash, bookID,
		)
		if err != nil {
			return fmt.Errorf("update book: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM book_files WHERE book_id = ?`, bookID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE book_id = ?`, bookID); err != nil {
			return err
		}

		for i, f := range files {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO book_files (book_id, idx, rel_path, size, duration_ms, codec, bitrate, mtime) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				bookID, i, f.relPath, f.size, f.durMs, f.codec, f.bitrate, f.mtimeMs,
			); err != nil {
				return fmt.Errorf("insert book_file: %w", err)
			}
		}
		for i, c := range chapters {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO chapters (book_id, idx, title, start_ms, end_ms) VALUES (?, ?, ?, ?, ?)`,
				bookID, i, c.Title, c.StartMs, c.EndMs,
			); err != nil {
				return fmt.Errorf("insert chapter: %w", err)
			}
		}
		return nil
	})
	return bookID, err
}

// removeStale deletes books in libraryID whose folder_path is no longer
// present on disk (books_files/chapters/progress/bookmarks cascade via FK).
func (s *Scanner) removeStale(ctx context.Context, libraryID int64, valid map[string]bool) (int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, folder_path FROM books WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, err
	}
	var toDelete []int64
	for rows.Next() {
		var id int64
		var fp string
		if err := rows.Scan(&id, &fp); err != nil {
			rows.Close()
			return 0, err
		}
		if !valid[fp] {
			toDelete = append(toDelete, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()

	if len(toDelete) == 0 {
		return 0, nil
	}

	err = s.DB.Write(ctx, func(tx *sql.Tx) error {
		for _, id := range toDelete {
			if _, err := tx.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, id := range toDelete {
		removeCoverDir(s.DataDir, id)
	}
	return len(toDelete), nil
}
