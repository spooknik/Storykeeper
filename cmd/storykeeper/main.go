// Command storykeeper runs the Storykeeper server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spooknik/storykeeper/internal/api"
	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/db"
	"github.com/spooknik/storykeeper/internal/events"
	"github.com/spooknik/storykeeper/internal/library"
	"github.com/spooknik/storykeeper/web"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", envOr("SK_ADDR", ":8080"), "listen address (SK_ADDR)")
	dataDir := flag.String("data", envOr("SK_DATA_DIR", "./data"), "data directory for db, covers, uploads (SK_DATA_DIR)")
	libraryPath := flag.String("library", envOr("SK_LIBRARY", ""), "optional initial library folder (SK_LIBRARY)")
	secureCookie := flag.Bool("secure-cookie", strings.EqualFold(envOr("SK_SECURE_COOKIE", "false"), "true"),
		"force Secure on the session cookie (SK_SECURE_COOKIE)")
	trustProxy := flag.Bool("trust-proxy", strings.EqualFold(envOr("SK_TRUST_PROXY", "false"), "true"),
		"honour X-Forwarded-For from the reverse proxy for rate limiting (SK_TRUST_PROXY)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log, *addr, *dataDir, *libraryPath, *secureCookie, *trustProxy); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, addr, dataDir, libraryPath string, secureCookie, trustProxy bool) error {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	for _, sub := range []string{"", "covers", "uploads"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
			return err
		}
	}

	database, err := db.Open(filepath.Join(dataDir, "storykeeper.db"))
	if err != nil {
		return err
	}
	defer database.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	authSvc := auth.New(database, secureCookie)
	if err := bootstrapAdmin(ctx, log, authSvc); err != nil {
		return err
	}
	if err := bootstrapLibrary(ctx, log, database, libraryPath); err != nil {
		return err
	}

	scanner := library.New(database, dataDir)
	go func() {
		if err := scanner.ScanAll(ctx); err != nil && !errors.Is(err, library.ErrNotImplemented) {
			log.Error("initial scan", "err", err)
		}
		if err := scanner.Watch(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("watcher", "err", err)
		}
	}()

	webFS, err := fs.Sub(web.Build, "build")
	if err != nil {
		return err
	}
	srv := &api.Server{
		DB: database, Auth: authSvc, Scanner: scanner,
		Cfg: api.Config{DataDir: dataDir, TrustProxy: trustProxy}, Log: log, Web: webFS,
		Events: events.New(),
	}
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: SSE streams and multi-hour audio ranges are long-lived.
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr, "data", dataDir)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

func bootstrapAdmin(ctx context.Context, log *slog.Logger, a *auth.Service) error {
	n, err := a.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	user := envOr("SK_ADMIN_USER", "admin")
	pass := os.Getenv("SK_ADMIN_PASSWORD")
	generated := false
	if pass == "" {
		pass, err = auth.RandomToken(12)
		if err != nil {
			return err
		}
		generated = true
	}
	if _, err := a.CreateUser(ctx, user, pass, auth.RoleAdmin); err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	if generated {
		log.Warn("created initial admin with a generated password; change it after logging in",
			"username", user, "password", pass)
	} else {
		log.Info("created initial admin", "username", user)
	}
	return nil
}

func bootstrapLibrary(ctx context.Context, log *slog.Logger, d *db.DB, path string) error {
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		log.Warn("SK_LIBRARY is not a directory; skipping", "path", abs)
		return nil
	}
	var n int
	if err := d.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE path = ?`, abs).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	name := filepath.Base(abs)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "Library"
	}
	err = d.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO libraries (name, path, created_at) VALUES (?, ?, ?)`, name, abs, db.Now())
		return err
	})
	if err == nil {
		log.Info("created initial library", "name", name, "path", abs)
	}
	return err
}
