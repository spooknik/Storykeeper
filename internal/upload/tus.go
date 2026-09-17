// Package upload implements resumable audiobook uploads using the tus
// resumable-upload protocol via the embedded tusd v2 handler. Files are
// staged under <DataDir>/uploads and, once complete, moved into the target
// library folder.
package upload

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tus/tusd/v2/pkg/filelocker"
	"github.com/tus/tusd/v2/pkg/filestore"
	"github.com/tus/tusd/v2/pkg/handler"

	"github.com/spooknik/storykeeper/internal/db"
)

// allowedExt is the set of audiobook file extensions accepted for upload.
var allowedExt = map[string]bool{
	".m4b":  true,
	".m4a":  true,
	".mp3":  true,
	".ogg":  true,
	".opus": true,
	".flac": true,
	".aac":  true,
	".wav":  true,
}

const (
	defaultMaxSize = 8 << 30 // 8 GiB
	defaultMaxAge  = 7 * 24 * time.Hour
	expirySweep    = time.Hour
)

// Config configures the upload Service.
type Config struct {
	// DataDir is the base data directory; uploads are staged under
	// <DataDir>/uploads until they are moved into the target library.
	DataDir string
	// BasePath is the URL path the tus handler is mounted at, e.g.
	// "/api/v1/upload/".
	BasePath string
	// MaxSize is the largest upload accepted, in bytes. Defaults to 8 GiB.
	MaxSize int64
	// MaxAge is how long an incomplete upload is kept before the expiry
	// sweep removes it. Defaults to 7 days.
	MaxAge time.Duration
}

func (c *Config) setDefaults() {
	if c.MaxSize <= 0 {
		c.MaxSize = defaultMaxSize
	}
	if c.MaxAge <= 0 {
		c.MaxAge = defaultMaxAge
	}
	if c.BasePath == "" {
		c.BasePath = "/api/v1/upload/"
	}
	if !strings.HasSuffix(c.BasePath, "/") {
		c.BasePath += "/"
	}
}

// Service wires a tusd handler backed by the local filesystem to
// storykeeper's upload validation and completion behaviour.
type Service struct {
	cfg        Config
	uploadsDir string
	db         *db.DB
	onComplete func(ctx context.Context, libraryID int64, finalPath string)
	handler    *handler.Handler
	log        *slog.Logger
}

// New creates the uploads staging directory, wires up the tusd handler and
// starts the goroutine that drains completed uploads. Callers must also call
// Run to start the expiry sweep for abandoned uploads.
func New(cfg Config, d *db.DB, onComplete func(ctx context.Context, libraryID int64, finalPath string)) (*Service, error) {
	cfg.setDefaults()

	uploadsDir := filepath.Join(cfg.DataDir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		return nil, fmt.Errorf("upload: create uploads dir: %w", err)
	}

	s := &Service{
		cfg:        cfg,
		uploadsDir: uploadsDir,
		db:         d,
		onComplete: onComplete,
		log:        slog.Default().With("component", "upload"),
	}

	store := filestore.New(uploadsDir)
	locker := filelocker.New(uploadsDir)
	composer := handler.NewStoreComposer()
	store.UseIn(composer)
	locker.UseIn(composer)

	h, err := handler.NewHandler(handler.Config{
		StoreComposer:           composer,
		BasePath:                cfg.BasePath,
		MaxSize:                 cfg.MaxSize,
		RespectForwardedHeaders: true,
		NotifyCompleteUploads:   true,
		DisableDownload:         true,
		DisableTermination:      false,
		PreUploadCreateCallback: s.preCreate,
	})
	if err != nil {
		return nil, fmt.Errorf("upload: create tus handler: %w", err)
	}
	s.handler = h

	// The CompleteUploads channel is unbuffered; it must be drained for the
	// life of the handler or completing PATCH requests will block forever.
	go s.completionLoop()

	s.log.Info("upload service ready", "uploads_dir", uploadsDir, "base_path", cfg.BasePath,
		"max_size", cfg.MaxSize, "max_age", cfg.MaxAge)

	return s, nil
}

// Handler returns an http.Handler serving the tus protocol (POST, HEAD,
// PATCH, DELETE, OPTIONS). It must be mounted so requests still carry
// cfg.BasePath, e.g. mux.Handle("/api/v1/upload/", svc.Handler()); the
// prefix is stripped internally before being handed to tusd's router.
func (s *Service) Handler() http.Handler {
	return http.StripPrefix(s.cfg.BasePath, s.handler)
}

// Run starts the background sweep that deletes incomplete uploads older
// than cfg.MaxAge. It blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	s.retryUnmoved(ctx)
	ticker := time.NewTicker(expirySweep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.retryUnmoved(ctx)
			s.sweepExpired()
		}
	}
}

// preCreate validates upload metadata before tusd creates the upload
// resource: filename with an allowed extension, an existing library_id, and
// a size within cfg.MaxSize. author and title are optional.
func (s *Service) preCreate(hook handler.HookEvent) (handler.HTTPResponse, handler.FileInfoChanges, error) {
	info := hook.Upload

	if s.cfg.MaxSize > 0 && info.Size > s.cfg.MaxSize {
		return handler.HTTPResponse{}, handler.FileInfoChanges{},
			handler.NewError("ERR_SK_MAX_SIZE", "upload exceeds the maximum allowed size", http.StatusRequestEntityTooLarge)
	}

	filename := strings.TrimSpace(info.MetaData["filename"])
	if filename == "" {
		return handler.HTTPResponse{}, handler.FileInfoChanges{},
			handler.NewError("ERR_SK_FILENAME", "filename metadata is required", http.StatusBadRequest)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !allowedExt[ext] {
		return handler.HTTPResponse{}, handler.FileInfoChanges{},
			handler.NewError("ERR_SK_EXTENSION", "unsupported file extension: "+ext, http.StatusBadRequest)
	}

	libIDStr := strings.TrimSpace(info.MetaData["library_id"])
	if libIDStr == "" {
		return handler.HTTPResponse{}, handler.FileInfoChanges{},
			handler.NewError("ERR_SK_LIBRARY", "library_id metadata is required", http.StatusBadRequest)
	}
	libID, err := strconv.ParseInt(libIDStr, 10, 64)
	if err != nil {
		return handler.HTTPResponse{}, handler.FileInfoChanges{},
			handler.NewError("ERR_SK_LIBRARY", "library_id must be a number", http.StatusBadRequest)
	}

	var libPath string
	err = s.db.QueryRowContext(hook.Context, `SELECT path FROM libraries WHERE id = ?`, libID).Scan(&libPath)
	if errors.Is(err, sql.ErrNoRows) {
		return handler.HTTPResponse{}, handler.FileInfoChanges{},
			handler.NewError("ERR_SK_LIBRARY", "library not found", http.StatusBadRequest)
	}
	if err != nil {
		s.log.Error("upload: library lookup failed", "library_id", libID, "err", err)
		return handler.HTTPResponse{}, handler.FileInfoChanges{},
			handler.NewError("ERR_SK_INTERNAL", "failed to validate library", http.StatusInternalServerError)
	}

	// Refuse before a single byte is transferred if the finished file could
	// not be moved into the library anyway (read-only mount, permissions).
	if err := probeWritable(libPath); err != nil {
		s.log.Warn("upload: library not writable", "library_id", libID, "path", libPath, "err", err)
		return handler.HTTPResponse{}, handler.FileInfoChanges{},
			handler.NewError("ERR_SK_READONLY",
				"the library folder is not writable by the server: check the volume mount (remove :ro) and folder permissions",
				http.StatusBadRequest)
	}

	return handler.HTTPResponse{}, handler.FileInfoChanges{}, nil
}

// probeWritable creates and removes a temporary file in dir.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".storykeeper-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// retryUnmoved finds uploads that finished but could not be moved into their
// library (typically a permissions problem that has since been fixed) and
// runs the completion step for them again.
func (s *Service) retryUnmoved(ctx context.Context) {
	entries, err := os.ReadDir(s.uploadsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".info") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.uploadsDir, name))
		if err != nil {
			continue
		}
		var info handler.FileInfo
		if err := json.Unmarshal(raw, &info); err != nil || info.Size <= 0 || info.Offset < info.Size {
			continue // corrupt or still in progress
		}
		if info.Storage == nil {
			info.Storage = map[string]string{}
		}
		if info.Storage[filestore.StorageKeyPath] == "" {
			info.Storage[filestore.StorageKeyPath] = filepath.Join(s.uploadsDir, strings.TrimSuffix(name, ".info"))
		}
		if info.Storage[filestore.StorageKeyInfoPath] == "" {
			info.Storage[filestore.StorageKeyInfoPath] = filepath.Join(s.uploadsDir, name)
		}
		if _, err := os.Stat(info.Storage[filestore.StorageKeyPath]); err != nil {
			continue
		}
		s.log.Info("upload: retrying move of a finished upload", "id", info.ID)
		s.handleComplete(handler.HookEvent{Context: ctx, Upload: info})
	}
}

// completionLoop drains handler.CompleteUploads for the life of the
// Service, moving each finished upload into its library.
func (s *Service) completionLoop() {
	for event := range s.handler.CompleteUploads {
		s.handleComplete(event)
	}
}

func (s *Service) handleComplete(event handler.HookEvent) {
	info := event.Upload
	ctx := event.Context

	dataPath, ok := info.Storage[filestore.StorageKeyPath]
	if !ok || dataPath == "" {
		s.log.Error("upload: completed upload missing storage path", "id", info.ID)
		return
	}
	infoPath := info.Storage[filestore.StorageKeyInfoPath]

	libIDStr := info.MetaData["library_id"]
	libID, err := strconv.ParseInt(libIDStr, 10, 64)
	if err != nil {
		s.log.Error("upload: completed upload has an invalid library_id", "id", info.ID, "library_id", libIDStr)
		return
	}

	var libPath string
	if err := s.db.QueryRowContext(ctx, `SELECT path FROM libraries WHERE id = ?`, libID).Scan(&libPath); err != nil {
		s.log.Error("upload: completed upload's library no longer exists", "id", info.ID, "library_id", libID, "err", err)
		return
	}

	filename := destFilename(info)
	author := sanitizeName(info.MetaData["author"])
	title := sanitizeName(info.MetaData["title"])

	var folderName string
	if author != "" && title != "" {
		folderName = author + " - " + title
	} else {
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		folderName = filepath.Join("Uploads", base)
	}

	// Files that share author and title belong to one book, so the folder is
	// reused; only a colliding file name gets a " (2)" suffix.
	destDir := filepath.Join(libPath, folderName)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		s.log.Error("upload: failed to create destination directory", "dir", destDir, "err", err)
		return
	}
	destPath := uniqueFile(filepath.Join(destDir, filename))

	if err := moveFile(dataPath, destPath); err != nil {
		s.log.Error("upload: failed to move file into library", "src", dataPath, "dst", destPath, "err", err)
		return
	}
	if infoPath != "" {
		if err := os.Remove(infoPath); err != nil && !os.IsNotExist(err) {
			s.log.Warn("upload: failed to remove .info file", "path", infoPath, "err", err)
		}
	}

	s.log.Info("upload complete", "id", info.ID, "library_id", libID, "dest", destPath)

	if s.onComplete != nil {
		s.onComplete(ctx, libID, destDir)
	}
}

// destFilename returns the sanitized filename to use on disk, preferring
// the sanitized upload filename metadata but falling back to the upload ID
// (with the original extension) if sanitizing strips it entirely.
func destFilename(info handler.FileInfo) string {
	raw := info.MetaData["filename"]
	ext := strings.ToLower(filepath.Ext(raw))
	name := sanitizeName(raw)
	if name == "" || !strings.HasSuffix(strings.ToLower(name), ext) {
		name = info.ID + ext
	}
	return name
}

// uniqueFile returns path, or if that file already exists, path with
// " (2)", " (3)", ... inserted before the extension until a free name is
// found.
func uniqueFile(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// moveFile renames src to dst, falling back to copy+fsync+remove when the
// rename fails (e.g. src and dst are on different mounts, such as /data and
// /library in the Docker image).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open destination: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("fsync: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("close destination: %w", err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source: %w", err)
	}
	return nil
}

var controlCharRE = regexp.MustCompile(`[\x00-\x1f\x7f]`)

// sanitizeName makes s safe to use as a single path component: strips
// path separators, "..", control characters and trailing dots/spaces, and
// caps the result at 200 characters (on a rune boundary).
func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, "\\", "")
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", "")
	}
	s = controlCharRE.ReplaceAllString(s, "")
	s = strings.TrimRight(s, " .")
	s = strings.TrimSpace(s)

	if r := []rune(s); len(r) > 200 {
		s = strings.TrimRight(string(r[:200]), " .")
	}
	return s
}

// sweepExpired deletes incomplete uploads (data file plus its .info
// sidecar) whose data file is older than cfg.MaxAge.
func (s *Service) sweepExpired() {
	entries, err := os.ReadDir(s.uploadsDir)
	if err != nil {
		s.log.Error("upload: expiry sweep failed to read uploads dir", "err", err)
		return
	}

	cutoff := time.Now().Add(-s.cfg.MaxAge)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".info") || strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".stop") {
			continue
		}
		fi, err := e.Info()
		if err != nil || fi.ModTime().After(cutoff) {
			continue
		}

		dataPath := filepath.Join(s.uploadsDir, name)
		infoPath := dataPath + ".info"
		if err := os.Remove(dataPath); err != nil && !os.IsNotExist(err) {
			s.log.Error("upload: expiry sweep failed to remove data file", "path", dataPath, "err", err)
			continue
		}
		if err := os.Remove(infoPath); err != nil && !os.IsNotExist(err) {
			s.log.Error("upload: expiry sweep failed to remove info file", "path", infoPath, "err", err)
		}
		s.log.Info("upload: expired incomplete upload removed", "id", name)
	}
}
