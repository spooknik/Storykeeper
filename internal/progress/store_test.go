package progress

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/spooknik/storykeeper/internal/db"
)

// base is an arbitrary fixed server clock (ms) so every test is deterministic.
const (
	base   = int64(1_760_000_000_000)
	minute = int64(60_000)
	hour   = 60 * minute
	day    = 24 * hour
)

func newTestStore(t *testing.T) (*Store, int64, []int64) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	ctx := context.Background()
	var userID int64
	books := make([]int64, 0, 2)
	err = d.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO users (username, password_hash, role, created_at) VALUES ('alice','x','user',?)`, base)
		if err != nil {
			return err
		}
		if userID, err = res.LastInsertId(); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx,
			`INSERT INTO libraries (name, path, created_at) VALUES ('lib','/lib',?)`, base)
		if err != nil {
			return err
		}
		libID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for i, title := range []string{"Book One", "Book Two"} {
			res, err := tx.ExecContext(ctx, `INSERT INTO books
				(library_id, folder_path, title, duration_ms, added_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)`,
				libID, title, title, int64(20_000_000+i), base, base)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			books = append(books, id)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return New(d), userID, books
}

func boolPtr(b bool) *bool { return &b }

// step is one report in a scenario, with what the store is expected to answer.
type step struct {
	now              int64
	clientListenedAt int64
	clientNow        int64
	positionMs       int64
	fileIndex        int
	finished         *bool
	book             int // index into the seeded books
	wantErr          error
	wantAccepted     bool
	wantPosition     int64 // position on the returned record
	wantFinished     bool
}

func TestReport(t *testing.T) {
	// Mirrors the DESIGN.md worked case: device B listens offline Tue 1:00 up
	// to 3:00:00, device A listens online Tue 2:00 up to 3:30:00, B reconnects
	// on Wednesday.
	var (
		tue0100 = base
		tue0200 = base + hour
		wed1200 = base + day + 11*hour
		posB    = int64(3 * hour)           // 3:00:00
		posA    = int64(3*hour + 30*minute) // 3:30:00
	)

	cases := []struct {
		name  string
		steps []step
	}{
		{"first write accepted", []step{
			{now: base, clientListenedAt: 1_000, clientNow: 1_000, positionMs: 1_000,
				wantAccepted: true, wantPosition: 1_000},
		}},
		{"newer write accepted", []step{
			{now: base, clientListenedAt: 1_000, clientNow: 1_000, positionMs: 1_000,
				wantAccepted: true, wantPosition: 1_000},
			{now: base + minute, clientListenedAt: 1_000, clientNow: 1_000, positionMs: 5_000,
				wantAccepted: true, wantPosition: 5_000},
		}},
		{"older age-corrected write rejected and returns current", []step{
			{now: base, clientListenedAt: 1_000, clientNow: 1_000, positionMs: 5_000,
				wantAccepted: true, wantPosition: 5_000},
			// Arrives a minute later but describes a listen two minutes old.
			{now: base + minute, clientListenedAt: 0, clientNow: 2 * minute, positionMs: 1_000,
				wantAccepted: false, wantPosition: 5_000},
		}},
		{"tie within 2s prefers larger position", []step{
			{now: base, clientListenedAt: 0, clientNow: 0, positionMs: 5_000,
				wantAccepted: true, wantPosition: 5_000},
			// listened_at lands 1 s before the stored one: inside the window.
			{now: base + 1_000, clientListenedAt: 0, clientNow: 2_000, positionMs: 9_000,
				wantAccepted: true, wantPosition: 9_000},
		}},
		{"tie within 2s with smaller position rejected", []step{
			{now: base, clientListenedAt: 0, clientNow: 0, positionMs: 5_000,
				wantAccepted: true, wantPosition: 5_000},
			{now: base + 1_000, clientListenedAt: 0, clientNow: 2_000, positionMs: 100,
				wantAccepted: false, wantPosition: 5_000},
		}},
		{"report older than 30 days is stale", []step{
			{now: base, clientListenedAt: 0, clientNow: 31 * day, positionMs: 1_000,
				wantErr: ErrStale},
		}},
		{"stale report cannot clobber a stored position", []step{
			{now: base, clientListenedAt: 0, clientNow: 0, positionMs: 5_000,
				wantAccepted: true, wantPosition: 5_000},
			{now: base + minute, clientListenedAt: 0, clientNow: 31 * day, positionMs: 99_000,
				wantErr: ErrStale},
		}},
		{"negative age is clamped to zero", []step{
			// A client whose clock says the listen is in the future must not
			// gain a listened_at ahead of the server clock.
			{now: base, clientListenedAt: 10 * minute, clientNow: 0, positionMs: 5_000,
				wantAccepted: true, wantPosition: 5_000},
		}},
		{"finished flag set and preserved", []step{
			{now: base, clientListenedAt: 0, clientNow: 0, positionMs: 1_000,
				wantAccepted: true, wantPosition: 1_000, wantFinished: false},
			{now: base + minute, clientListenedAt: 0, clientNow: 0, positionMs: 2_000,
				finished: boolPtr(true), wantAccepted: true, wantPosition: 2_000, wantFinished: true},
			// A later report that says nothing about finished keeps it set.
			{now: base + 2*minute, clientListenedAt: 0, clientNow: 0, positionMs: 3_000,
				wantAccepted: true, wantPosition: 3_000, wantFinished: true},
			{now: base + 3*minute, clientListenedAt: 0, clientNow: 0, positionMs: 4_000,
				finished: boolPtr(false), wantAccepted: true, wantPosition: 4_000, wantFinished: false},
		}},
		{"design worked case: offline device B cannot clobber device A", []step{
			// B listens offline on Tuesday at 1:00 (nothing reaches the server).
			// A listens online on Tuesday at 2:00 and reports immediately.
			{now: tue0200, clientListenedAt: tue0200, clientNow: tue0200, positionMs: posA,
				wantAccepted: true, wantPosition: posA},
			// B reconnects on Wednesday and honestly reports its Tuesday listen.
			{now: wed1200, clientListenedAt: tue0100, clientNow: wed1200, positionMs: posB,
				wantAccepted: false, wantPosition: posA},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, userID, books := newTestStore(t)
			ctx := context.Background()
			for i, s := range tc.steps {
				rec, accepted, err := st.Report(ctx, ReportInput{
					UserID:           userID,
					BookID:           books[s.book],
					PositionMs:       s.positionMs,
					FileIndex:        s.fileIndex,
					ClientListenedAt: s.clientListenedAt,
					ClientNow:        s.clientNow,
					Finished:         s.finished,
					DeviceID:         "dev",
					DeviceName:       "Test Device",
					Now:              s.now,
				})
				if s.wantErr != nil {
					if !errors.Is(err, s.wantErr) {
						t.Fatalf("step %d: err = %v, want %v", i, err, s.wantErr)
					}
					continue
				}
				if err != nil {
					t.Fatalf("step %d: unexpected error: %v", i, err)
				}
				if accepted != s.wantAccepted {
					t.Fatalf("step %d: accepted = %v, want %v", i, accepted, s.wantAccepted)
				}
				if rec == nil {
					t.Fatalf("step %d: nil record", i)
				}
				if rec.PositionMs != s.wantPosition {
					t.Errorf("step %d: position = %d, want %d", i, rec.PositionMs, s.wantPosition)
				}
				if rec.Finished != s.wantFinished {
					t.Errorf("step %d: finished = %v, want %v", i, rec.Finished, s.wantFinished)
				}
				// The returned record must match what a fresh read sees.
				got, err := st.Get(ctx, userID, books[s.book])
				if err != nil {
					t.Fatalf("step %d: get: %v", i, err)
				}
				if got == nil || got.PositionMs != s.wantPosition || got.Finished != s.wantFinished {
					t.Errorf("step %d: stored record = %+v, want position %d finished %v",
						i, got, s.wantPosition, s.wantFinished)
				}
			}
		})
	}
}

func TestReportUnknownBook(t *testing.T) {
	st, userID, _ := newTestStore(t)
	_, _, err := st.Report(context.Background(), ReportInput{
		UserID: userID, BookID: 9999, PositionMs: 1, Now: base,
	})
	if !errors.Is(err, ErrNoBook) {
		t.Fatalf("err = %v, want ErrNoBook", err)
	}
}

func TestGetAbsent(t *testing.T) {
	st, userID, books := newTestStore(t)
	rec, err := st.Get(context.Background(), userID, books[0])
	if err != nil || rec != nil {
		t.Fatalf("Get = (%v, %v), want (nil, nil)", rec, err)
	}
}

func TestSeqIsMonotonicPerUserAcrossBooks(t *testing.T) {
	st, userID, books := newTestStore(t)
	ctx := context.Background()

	report := func(book int, pos, now int64) *Record {
		t.Helper()
		rec, accepted, err := st.Report(ctx, ReportInput{
			UserID: userID, BookID: books[book], PositionMs: pos,
			ClientListenedAt: 0, ClientNow: 0, DeviceID: "dev", Now: now,
		})
		if err != nil || !accepted {
			t.Fatalf("report(book %d): accepted=%v err=%v", book, accepted, err)
		}
		return rec
	}

	a := report(0, 1_000, base)
	b := report(1, 2_000, base+minute)
	c := report(0, 3_000, base+2*minute)
	if !(a.Seq < b.Seq && b.Seq < c.Seq) {
		t.Fatalf("seq not strictly increasing: %d, %d, %d", a.Seq, b.Seq, c.Seq)
	}

	all, err := st.ListSince(ctx, userID, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListSince(0) returned %d records, want 2", len(all))
	}
	if all[0].Seq > all[1].Seq {
		t.Errorf("ListSince is not ordered by seq: %d then %d", all[0].Seq, all[1].Seq)
	}

	// Only the book touched after b's seq comes back.
	since, err := st.ListSince(ctx, userID, b.Seq)
	if err != nil {
		t.Fatalf("list since: %v", err)
	}
	if len(since) != 1 || since[0].BookID != books[0] || since[0].Seq != c.Seq {
		t.Fatalf("ListSince(%d) = %+v, want only book %d at seq %d", b.Seq, since, books[0], c.Seq)
	}

	// A seq at or beyond the newest record returns an empty, non-nil slice.
	empty, err := st.ListSince(ctx, userID, c.Seq)
	if err != nil {
		t.Fatalf("list since newest: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListSince(newest) = %+v, want empty", empty)
	}
}

func TestReportWritesProgressLog(t *testing.T) {
	st, userID, books := newTestStore(t)
	ctx := context.Background()

	// accepted
	if _, _, err := st.Report(ctx, ReportInput{UserID: userID, BookID: books[0],
		PositionMs: 5_000, DeviceID: "dev", Now: base}); err != nil {
		t.Fatalf("accepted report: %v", err)
	}
	// rejected (older listen)
	if _, accepted, err := st.Report(ctx, ReportInput{UserID: userID, BookID: books[0],
		PositionMs: 1_000, ClientListenedAt: 0, ClientNow: 2 * minute,
		DeviceID: "dev", Now: base + minute}); err != nil || accepted {
		t.Fatalf("rejected report: accepted=%v err=%v", accepted, err)
	}
	// stale
	if _, _, err := st.Report(ctx, ReportInput{UserID: userID, BookID: books[0],
		PositionMs: 9_000, ClientListenedAt: 0, ClientNow: 31 * day,
		DeviceID: "dev", Now: base + 2*minute}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale report: err = %v, want ErrStale", err)
	}

	var ok, rejected int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM progress_log WHERE accepted = 1`).Scan(&ok); err != nil {
		t.Fatalf("count accepted: %v", err)
	}
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM progress_log WHERE accepted = 0`).Scan(&rejected); err != nil {
		t.Fatalf("count rejected: %v", err)
	}
	if ok != 1 || rejected != 2 {
		t.Fatalf("progress_log = %d accepted / %d rejected, want 1 / 2", ok, rejected)
	}
}

func TestPruneDropsOldLogRows(t *testing.T) {
	st, userID, books := newTestStore(t)
	ctx := context.Background()

	if err := st.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO progress_log
			(user_id, book_id, position_ms, device_id, listened_at, received_at, accepted)
			VALUES (?, ?, 1, 'old', ?, ?, 1)`,
			userID, books[0], base-40*day, base-40*day)
		return err
	}); err != nil {
		t.Fatalf("seed old log row: %v", err)
	}

	// Pruning state is package-level, so reset it: this test must not depend on
	// how many accepted writes other tests in this package already made.
	pruneMu.Lock()
	lastPrune, writeCount = 0, 0
	pruneMu.Unlock()

	// With no prune on record, the next accepted write prunes.
	if _, _, err := st.Report(ctx, ReportInput{UserID: userID, BookID: books[0],
		PositionMs: 5_000, DeviceID: "dev", Now: base}); err != nil {
		t.Fatalf("report: %v", err)
	}

	var n int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM progress_log WHERE device_id = 'old'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("old progress_log rows = %d, want 0", n)
	}
}
