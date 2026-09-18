// Package prefs stores per-user playback preferences: default playback rate,
// skip amounts, auto-rewind and default sleep timer length.
//
// A user who has never saved preferences has no row in user_prefs; Get
// answers with the documented defaults in that case rather than an error, so
// callers never have to special-case "never configured" themselves.
package prefs

import (
	"context"
	"database/sql"
	"errors"

	"github.com/spooknik/storykeeper/internal/db"
)

// Defaults, mirrored by the user_prefs column defaults in the migration.
const (
	DefaultPlaybackRate        = 1.0
	DefaultSkipBackSec         = 30
	DefaultSkipForwardSec      = 30
	DefaultAutoRewind          = true
	DefaultDefaultSleepMinutes = 0
)

// Record is the stored preference set for one user.
type Record struct {
	UserID              int64   `json:"user_id"`
	PlaybackRate        float64 `json:"playback_rate"`
	SkipBackSec         int     `json:"skip_back_sec"`
	SkipForwardSec      int     `json:"skip_forward_sec"`
	AutoRewind          bool    `json:"auto_rewind"`
	DefaultSleepMinutes int     `json:"default_sleep_minutes"`
	UpdatedAt           int64   `json:"updated_at"`
}

func defaults(userID int64) Record {
	return Record{
		UserID:              userID,
		PlaybackRate:        DefaultPlaybackRate,
		SkipBackSec:         DefaultSkipBackSec,
		SkipForwardSec:      DefaultSkipForwardSec,
		AutoRewind:          DefaultAutoRewind,
		DefaultSleepMinutes: DefaultDefaultSleepMinutes,
	}
}

// Patch is a partial update to a user's preferences; nil fields are left
// unchanged.
type Patch struct {
	PlaybackRate        *float64
	SkipBackSec         *int
	SkipForwardSec      *int
	AutoRewind          *bool
	DefaultSleepMinutes *int
}

// Store reads and writes user_prefs rows.
type Store struct {
	DB *db.DB
}

func New(d *db.DB) *Store { return &Store{DB: d} }

const recordSelect = `SELECT user_id, playback_rate, skip_back_sec, skip_forward_sec,
	auto_rewind, default_sleep_minutes, updated_at FROM user_prefs`

type scanner interface{ Scan(...any) error }

func scanRecord(sc scanner) (Record, error) {
	var r Record
	var autoRewind int
	err := sc.Scan(&r.UserID, &r.PlaybackRate, &r.SkipBackSec, &r.SkipForwardSec,
		&autoRewind, &r.DefaultSleepMinutes, &r.UpdatedAt)
	if err != nil {
		return Record{}, err
	}
	r.AutoRewind = autoRewind != 0
	return r, nil
}

// Get returns the stored preferences for userID, or the documented defaults
// when the user has no row yet.
func (s *Store) Get(ctx context.Context, userID int64) (Record, error) {
	row := s.DB.QueryRowContext(ctx, recordSelect+` WHERE user_id = ?`, userID)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return defaults(userID), nil
	}
	if err != nil {
		return Record{}, err
	}
	return rec, nil
}

// Update applies patch on top of the user's current preferences (or the
// defaults, if they have none yet) and upserts the result.
func (s *Store) Update(ctx context.Context, userID int64, patch Patch) (Record, error) {
	var rec Record
	err := s.DB.Write(ctx, func(tx *sql.Tx) error {
		cur, err := currentRecord(ctx, tx, userID)
		if err != nil {
			return err
		}
		if cur == nil {
			d := defaults(userID)
			cur = &d
		}
		next := *cur
		if patch.PlaybackRate != nil {
			next.PlaybackRate = *patch.PlaybackRate
		}
		if patch.SkipBackSec != nil {
			next.SkipBackSec = *patch.SkipBackSec
		}
		if patch.SkipForwardSec != nil {
			next.SkipForwardSec = *patch.SkipForwardSec
		}
		if patch.AutoRewind != nil {
			next.AutoRewind = *patch.AutoRewind
		}
		if patch.DefaultSleepMinutes != nil {
			next.DefaultSleepMinutes = *patch.DefaultSleepMinutes
		}
		next.UserID = userID
		next.UpdatedAt = db.Now()

		if _, err := tx.ExecContext(ctx, `INSERT INTO user_prefs
			(user_id, playback_rate, skip_back_sec, skip_forward_sec, auto_rewind,
			 default_sleep_minutes, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET
				playback_rate         = excluded.playback_rate,
				skip_back_sec         = excluded.skip_back_sec,
				skip_forward_sec      = excluded.skip_forward_sec,
				auto_rewind           = excluded.auto_rewind,
				default_sleep_minutes = excluded.default_sleep_minutes,
				updated_at            = excluded.updated_at`,
			next.UserID, next.PlaybackRate, next.SkipBackSec, next.SkipForwardSec,
			boolInt(next.AutoRewind), next.DefaultSleepMinutes, next.UpdatedAt); err != nil {
			return err
		}
		rec = next
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return rec, nil
}

func currentRecord(ctx context.Context, tx *sql.Tx, userID int64) (*Record, error) {
	row := tx.QueryRowContext(ctx, recordSelect+` WHERE user_id = ?`, userID)
	rec, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
