// Package progress implements cross-device listening position storage with the
// "most recent listen wins" rule.
//
// Every report carries the client clock twice: when the listening happened
// (client_listened_at) and what the client thinks "now" is (client_now). The
// server only ever uses the difference between the two - the age of the report
// - and re-bases it on its own clock:
//
//	listened_at = server_now - (client_now - client_listened_at)
//
// That makes device clock skew irrelevant while still letting a device that was
// offline for a day honestly report an old listen, so it cannot clobber a newer
// position written from another device in the meantime.
package progress

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/spooknik/storykeeper/internal/db"
)

const (
	// maxReportAge rejects reports whose listen is older than 30 days.
	maxReportAge = 30 * 24 * 60 * 60 * 1000
	// tieWindow is the window inside which two listens count as simultaneous;
	// the larger position wins a tie.
	tieWindow = 2000
	// logRetention is how long progress_log rows are kept.
	logRetention = 30 * 24 * 60 * 60 * 1000
	// pruneInterval and pruneEveryN bound how often pruning runs.
	pruneInterval = 60 * 60 * 1000
	pruneEveryN   = 1000
)

var (
	// ErrStale is returned when a report describes a listen older than 30 days.
	ErrStale = errors.New("progress: report is older than 30 days")
	// ErrNoBook is returned when the reported book does not exist.
	ErrNoBook = errors.New("progress: no such book")
)

// Record is the stored progress for one (user, book). Its JSON shape is
// identical to api.Progress; the api package converts between the two so that
// this package never imports it.
type Record struct {
	BookID     int64  `json:"book_id"`
	PositionMs int64  `json:"position_ms"`
	DurationMs int64  `json:"duration_ms"`
	FileIndex  int    `json:"file_index"`
	Seq        int64  `json:"seq"`
	ListenedAt int64  `json:"listened_at"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Finished   bool   `json:"finished"`
}

// ReportInput is one client progress report.
type ReportInput struct {
	UserID           int64
	BookID           int64
	PositionMs       int64
	FileIndex        int
	ClientListenedAt int64
	ClientNow        int64
	BaseSeq          int64
	Finished         *bool
	DeviceID         string
	DeviceName       string
	// Now is the server clock in Unix ms. Zero means db.Now(); tests inject it.
	Now int64
}

// Store reads and writes progress rows.
type Store struct {
	DB *db.DB
}

func New(d *db.DB) *Store { return &Store{DB: d} }

const recordSelect = `SELECT book_id, position_ms, duration_ms, file_index, seq,
	listened_at, device_id, device_name, finished FROM progress`

type scanner interface{ Scan(...any) error }

func scanRecord(sc scanner) (*Record, error) {
	var r Record
	err := sc.Scan(&r.BookID, &r.PositionMs, &r.DurationMs, &r.FileIndex, &r.Seq,
		&r.ListenedAt, &r.DeviceID, &r.DeviceName, &r.Finished)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Get returns the stored progress for one book, or (nil, nil) when there is none.
func (s *Store) Get(ctx context.Context, userID, bookID int64) (*Record, error) {
	row := s.DB.QueryRowContext(ctx, recordSelect+` WHERE user_id = ? AND book_id = ?`, userID, bookID)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// ListSince returns every record for the user with seq greater than sinceSeq,
// ordered by seq. Pass 0 for everything.
func (s *Store) ListSince(ctx context.Context, userID, sinceSeq int64) ([]Record, error) {
	rows, err := s.DB.QueryContext(ctx,
		recordSelect+` WHERE user_id = ? AND seq > ? ORDER BY seq`, userID, sinceSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Record, 0, 16)
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rec)
	}
	return out, rows.Err()
}

// Report applies the most-recent-listen-wins rule.
//
// It returns the stored record and whether the report was accepted. On a
// rejection the returned record is the CURRENT one, which the caller answers
// with 409 so the client can adopt it. ErrStale and ErrNoBook are the two
// caller-visible failures.
func (s *Store) Report(ctx context.Context, in ReportInput) (*Record, bool, error) {
	now := in.Now
	if now == 0 {
		now = db.Now()
	}

	age := in.ClientNow - in.ClientListenedAt
	if age < 0 {
		age = 0
	}
	listenedAt := now - age

	var (
		rec      *Record
		accepted bool
		stale    bool
	)
	err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		if age > maxReportAge {
			// Commit the rejection to progress_log, then report ErrStale to
			// the caller once the transaction is through.
			stale = true
			return appendLog(ctx, tx, in, listenedAt, now, false)
		}

		var durationMs int64
		err := tx.QueryRowContext(ctx, `SELECT duration_ms FROM books WHERE id = ?`, in.BookID).Scan(&durationMs)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoBook
		}
		if err != nil {
			return err
		}

		cur, err := currentRecord(ctx, tx, in.UserID, in.BookID)
		if err != nil {
			return err
		}

		if !shouldAccept(cur, listenedAt, in.PositionMs) {
			if err := appendLog(ctx, tx, in, listenedAt, now, false); err != nil {
				return err
			}
			rec, accepted = cur, false
			return nil
		}

		seq, err := nextSeq(ctx, tx)
		if err != nil {
			return err
		}

		finished := false
		if cur != nil {
			finished = cur.Finished
		}
		if in.Finished != nil {
			finished = *in.Finished
		}

		next := &Record{
			BookID:     in.BookID,
			PositionMs: in.PositionMs,
			DurationMs: durationMs,
			FileIndex:  in.FileIndex,
			Seq:        seq,
			ListenedAt: listenedAt,
			DeviceID:   in.DeviceID,
			DeviceName: in.DeviceName,
			Finished:   finished,
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO progress
			(user_id, book_id, position_ms, duration_ms, file_index, seq, listened_at,
			 received_at, device_id, device_name, finished)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id, book_id) DO UPDATE SET
				position_ms = excluded.position_ms,
				duration_ms = excluded.duration_ms,
				file_index  = excluded.file_index,
				seq         = excluded.seq,
				listened_at = excluded.listened_at,
				received_at = excluded.received_at,
				device_id   = excluded.device_id,
				device_name = excluded.device_name,
				finished    = excluded.finished`,
			in.UserID, next.BookID, next.PositionMs, next.DurationMs, next.FileIndex, next.Seq,
			next.ListenedAt, now, next.DeviceID, next.DeviceName, boolInt(next.Finished)); err != nil {
			return err
		}
		if err := appendLog(ctx, tx, in, listenedAt, now, true); err != nil {
			return err
		}
		if shouldPrune(now) {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM progress_log WHERE received_at < ?`, now-logRetention); err != nil {
				return err
			}
		}
		rec, accepted = next, true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if stale {
		return nil, false, ErrStale
	}
	return rec, accepted, nil
}

// shouldAccept implements the write rule: a newer listen always wins, and
// inside the 2 s tie window the larger position wins.
func shouldAccept(cur *Record, listenedAt, positionMs int64) bool {
	if cur == nil {
		return true
	}
	if listenedAt > cur.ListenedAt {
		return true
	}
	return abs(listenedAt-cur.ListenedAt) <= tieWindow && positionMs >= cur.PositionMs
}

func currentRecord(ctx context.Context, tx *sql.Tx, userID, bookID int64) (*Record, error) {
	row := tx.QueryRowContext(ctx, recordSelect+` WHERE user_id = ? AND book_id = ?`, userID, bookID)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// nextSeq bumps the global progress counter. One counter for the whole server
// keeps seq monotonic per user across every book and keeps issuing fresh values
// after progress rows are deleted.
func nextSeq(ctx context.Context, tx *sql.Tx) (int64, error) {
	if _, err := tx.ExecContext(ctx,
		`UPDATE counters SET value = value + 1 WHERE name = 'progress_seq'`); err != nil {
		return 0, err
	}
	var seq int64
	if err := tx.QueryRowContext(ctx,
		`SELECT value FROM counters WHERE name = 'progress_seq'`).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}

func appendLog(ctx context.Context, tx *sql.Tx, in ReportInput, listenedAt, now int64, accepted bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO progress_log
		(user_id, book_id, position_ms, device_id, listened_at, received_at, accepted)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.UserID, in.BookID, in.PositionMs, in.DeviceID, listenedAt, now, boolInt(accepted))
	return err
}

// Pruning of progress_log is opportunistic: at most once an hour, or every
// pruneEveryN accepted writes, whichever comes first.
var (
	pruneMu    sync.Mutex
	lastPrune  int64
	writeCount int64
)

func shouldPrune(now int64) bool {
	pruneMu.Lock()
	defer pruneMu.Unlock()
	writeCount++
	if writeCount%pruneEveryN == 0 || now-lastPrune > pruneInterval {
		lastPrune = now
		return true
	}
	return false
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
