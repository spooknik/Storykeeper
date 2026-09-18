package prefs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/spooknik/storykeeper/internal/db"
)

const base = int64(1_760_000_000_000)

func newTestStore(t *testing.T) (*Store, int64) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	ctx := context.Background()
	var userID int64
	err = d.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO users (username, password_hash, role, created_at) VALUES ('alice','x','user',?)`, base)
		if err != nil {
			return err
		}
		userID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return New(d), userID
}

func float64Ptr(f float64) *float64 { return &f }
func intPtr(n int) *int             { return &n }
func boolPtr(b bool) *bool          { return &b }

func TestGetDefaultsWhenNoRow(t *testing.T) {
	st, userID := newTestStore(t)
	rec, err := st.Get(context.Background(), userID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	want := Record{
		UserID:              userID,
		PlaybackRate:        DefaultPlaybackRate,
		SkipBackSec:         DefaultSkipBackSec,
		SkipForwardSec:      DefaultSkipForwardSec,
		AutoRewind:          DefaultAutoRewind,
		DefaultSleepMinutes: DefaultDefaultSleepMinutes,
	}
	if rec != want {
		t.Fatalf("Get() = %+v, want %+v", rec, want)
	}
}

func TestUpdatePartial(t *testing.T) {
	st, userID := newTestStore(t)
	ctx := context.Background()

	rec, err := st.Update(ctx, userID, Patch{PlaybackRate: float64Ptr(1.5)})
	if err != nil {
		t.Fatalf("update 1: %v", err)
	}
	if rec.PlaybackRate != 1.5 {
		t.Fatalf("playback_rate = %v, want 1.5", rec.PlaybackRate)
	}
	if rec.SkipBackSec != DefaultSkipBackSec {
		t.Fatalf("skip_back_sec = %v, want default %v", rec.SkipBackSec, DefaultSkipBackSec)
	}
	if rec.UpdatedAt == 0 {
		t.Fatalf("updated_at = 0, want set")
	}

	// A second, unrelated patch must not clobber the first field.
	rec2, err := st.Update(ctx, userID, Patch{SkipForwardSec: intPtr(15), AutoRewind: boolPtr(false)})
	if err != nil {
		t.Fatalf("update 2: %v", err)
	}
	if rec2.PlaybackRate != 1.5 {
		t.Fatalf("playback_rate after second patch = %v, want preserved 1.5", rec2.PlaybackRate)
	}
	if rec2.SkipForwardSec != 15 {
		t.Fatalf("skip_forward_sec = %v, want 15", rec2.SkipForwardSec)
	}
	if rec2.AutoRewind {
		t.Fatalf("auto_rewind = true, want false")
	}

	got, err := st.Get(ctx, userID)
	if err != nil {
		t.Fatalf("get after updates: %v", err)
	}
	if got != rec2 {
		t.Fatalf("stored record = %+v, want %+v", got, rec2)
	}
}

func TestUpdateAllFields(t *testing.T) {
	st, userID := newTestStore(t)
	ctx := context.Background()

	rec, err := st.Update(ctx, userID, Patch{
		PlaybackRate:        float64Ptr(2.0),
		SkipBackSec:         intPtr(10),
		SkipForwardSec:      intPtr(20),
		AutoRewind:          boolPtr(false),
		DefaultSleepMinutes: intPtr(45),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	want := Record{
		UserID:              userID,
		PlaybackRate:        2.0,
		SkipBackSec:         10,
		SkipForwardSec:      20,
		AutoRewind:          false,
		DefaultSleepMinutes: 45,
		UpdatedAt:           rec.UpdatedAt,
	}
	if rec != want {
		t.Fatalf("Update() = %+v, want %+v", rec, want)
	}
}

func TestUpdateEmptyPatchKeepsCurrentValues(t *testing.T) {
	st, userID := newTestStore(t)
	ctx := context.Background()

	first, err := st.Update(ctx, userID, Patch{PlaybackRate: float64Ptr(1.75)})
	if err != nil {
		t.Fatalf("update 1: %v", err)
	}
	second, err := st.Update(ctx, userID, Patch{})
	if err != nil {
		t.Fatalf("update 2 (empty patch): %v", err)
	}
	if second.PlaybackRate != first.PlaybackRate {
		t.Fatalf("playback_rate after empty patch = %v, want preserved %v", second.PlaybackRate, first.PlaybackRate)
	}
}
