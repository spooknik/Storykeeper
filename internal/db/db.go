// Package db opens the SQLite database, applies embedded migrations, and
// serialises writers so SQLite's single-writer model never surfaces as SQLITE_BUSY.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sync"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB wraps *sql.DB. Reads may use DB directly; every write must go through Write.
type DB struct {
	*sql.DB
	writeMu sync.Mutex
}

// Open opens (creating if needed) the SQLite file at path and runs migrations.
func Open(path string) (*DB, error) {
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqldb.SetMaxOpenConns(8)
	sqldb.SetConnMaxLifetime(0)
	if err := sqldb.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	goose.SetBaseFS(migrations)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, err
	}
	if err := goose.Up(sqldb, "migrations"); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{DB: sqldb}, nil
}

// Write runs fn inside a serialised BEGIN IMMEDIATE transaction.
func (d *DB) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Now returns the server clock in Unix milliseconds. All timestamps in the DB use this.
func Now() int64 { return time.Now().UnixMilli() }
