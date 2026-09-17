package library

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	watchDebounce   = 5 * time.Second
	fullScanPeriod  = 12 * time.Hour
	reconcilePeriod = 5 * time.Minute
)

type libraryRow struct {
	ID   int64
	Path string
}

func (s *Scanner) libraries(ctx context.Context) ([]libraryRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, path FROM libraries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []libraryRow
	for rows.Next() {
		var l libraryRow
		if err := rows.Scan(&l.ID, &l.Path); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// findOwningLibrary returns the library whose root is the longest matching
// prefix of p (p itself or a descendant of the root).
func findOwningLibrary(libs []libraryRow, p string) (int64, bool) {
	cp := filepath.Clean(p)
	bestLen := -1
	var bestID int64
	found := false
	for _, l := range libs {
		lp := filepath.Clean(l.Path)
		if cp != lp && !strings.HasPrefix(cp, lp+string(filepath.Separator)) {
			continue
		}
		if len(lp) > bestLen {
			bestLen = len(lp)
			bestID = l.ID
			found = true
		}
	}
	return bestID, found
}

// Watch blocks, watching all library roots with fsnotify (debounced) and
// running a full ScanAll every 12h, until ctx is cancelled. It never lets a
// scan error stop the loop; errors are logged and watching continues.
func (s *Scanner) Watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	watched := map[string]bool{}
	var libs []libraryRow

	watchDirIfNew := func(p string) {
		if watched[p] {
			return
		}
		if err := watcher.Add(p); err != nil {
			slog.Warn("library watch: add failed", "path", p, "err", err)
			return
		}
		watched[p] = true
	}

	// addDirRecursive watches a newly-created directory (and its existing
	// children) immediately, ahead of the next reconcile.
	addDirRecursive := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if p != root && isHiddenOrIgnored(d.Name()) {
				return filepath.SkipDir
			}
			watchDirIfNew(p)
			return nil
		})
	}

	// reconcile re-reads the libraries table and updates the watched-dir
	// set to match: newly added libraries (or newly created subfolders)
	// get watched, removed libraries/folders get unwatched.
	reconcile := func() {
		newLibs, err := s.libraries(ctx)
		if err != nil {
			slog.Error("library watch: load libraries failed", "err", err)
			return
		}
		libs = newLibs

		seen := map[string]bool{}
		for _, l := range libs {
			_ = filepath.WalkDir(l.Path, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() {
					return nil
				}
				if p != l.Path && isHiddenOrIgnored(d.Name()) {
					return filepath.SkipDir
				}
				seen[p] = true
				watchDirIfNew(p)
				return nil
			})
		}
		for p := range watched {
			if !seen[p] {
				_ = watcher.Remove(p)
				delete(watched, p)
			}
		}
	}

	reconcile()

	ticker := time.NewTicker(fullScanPeriod)
	defer ticker.Stop()
	// Libraries created through the API get watched at the next reconcile.
	reconcileTicker := time.NewTicker(reconcilePeriod)
	defer reconcileTicker.Stop()

	// All state above (watched, libs) is owned by this goroutine. Debounce
	// timers only hand the library id back to the loop; scans run in their
	// own goroutines so the event loop never blocks (fsnotify drops events on
	// Windows when its buffer overflows).
	scanReq := make(chan int64, 64)
	var debMu sync.Mutex
	timers := map[int64]*time.Timer{}
	trigger := func(libID int64) {
		debMu.Lock()
		defer debMu.Unlock()
		if t, ok := timers[libID]; ok {
			t.Stop()
		}
		timers[libID] = time.AfterFunc(watchDebounce, func() {
			select {
			case scanReq <- libID:
			default: // a request is already queued; ScanLibrary coalesces
			}
		})
	}
	runScan := func(libID int64) {
		go func() {
			if err := s.ScanLibrary(ctx, libID); err != nil && ctx.Err() == nil {
				slog.Error("library watch: scan failed", "library_id", libID, "err", err)
			}
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case libID := <-scanReq:
			reconcile()
			runScan(libID)

		case <-reconcileTicker.C:
			reconcile()

		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() && !isHiddenOrIgnored(filepath.Base(ev.Name)) {
					addDirRecursive(ev.Name)
				}
			}
			if id, ok := findOwningLibrary(libs, ev.Name); ok {
				trigger(id)
			}

		case werr, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Error("library watch: fsnotify error", "err", werr)

		case <-ticker.C:
			reconcile()
			go func() {
				if err := s.ScanAll(ctx); err != nil && ctx.Err() == nil {
					slog.Error("library watch: scheduled scan failed", "err", err)
				}
			}()
		}
	}
}
