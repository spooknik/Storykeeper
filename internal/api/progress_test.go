package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/db"
)

// fakeProgressPublisher records everything the handlers fan out, including
// the stream terminations they ask for.
type fakeProgressPublisher struct {
	mu     sync.Mutex
	events []struct {
		userID  int64
		name    string
		payload any
	}
	closedSessions []int64
	closedUsers    []int64
}

func (f *fakeProgressPublisher) Publish(userID int64, name string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, struct {
		userID  int64
		name    string
		payload any
	}{userID, name, payload})
}

func (f *fakeProgressPublisher) Broadcast(string, any) {}

func (f *fakeProgressPublisher) CloseSession(sessionID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedSessions = append(f.closedSessions, sessionID)
}

func (f *fakeProgressPublisher) CloseUser(userID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closedUsers = append(f.closedUsers, userID)
}

func (f *fakeProgressPublisher) named(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.events {
		if e.name == name {
			n++
		}
	}
	return n
}

const progressTestNow = int64(1_760_000_000_000)

func newProgressTestServer(t *testing.T) (*Server, *auth.User, *auth.Session, []int64) {
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
			`INSERT INTO users (username, password_hash, role, created_at) VALUES ('alice','x','user',?)`, progressTestNow)
		if err != nil {
			return err
		}
		if userID, err = res.LastInsertId(); err != nil {
			return err
		}
		res, err = tx.ExecContext(ctx,
			`INSERT INTO libraries (name, path, created_at) VALUES ('lib','/lib',?)`, progressTestNow)
		if err != nil {
			return err
		}
		libID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, title := range []string{"Book One", "Book Two"} {
			res, err := tx.ExecContext(ctx, `INSERT INTO books
				(library_id, folder_path, title, duration_ms, added_at, updated_at)
				VALUES (?, ?, ?, 20000000, ?, ?)`, libID, title, title, progressTestNow, progressTestNow)
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

	s := &Server{DB: d, Auth: auth.New(d, false), Log: slog.New(slog.DiscardHandler)}
	u := &auth.User{ID: userID, Username: "alice", Role: auth.RoleUser, CreatedAt: progressTestNow}
	sess := &auth.Session{ID: 1, UserID: userID, CSRFToken: "csrf-token-abc",
		DeviceID: "device-a", DeviceName: "iPhone", CreatedAt: progressTestNow}
	return s, u, sess, books
}

// call runs one handler with the principal attached and bookId set.
func (f *progressFixture) call(t *testing.T, h http.HandlerFunc, method, target string, bookID int64, body any) *http.Response {
	t.Helper()
	var r *http.Request
	switch b := body.(type) {
	case nil:
		r = httptest.NewRequest(method, target, nil)
	case string:
		r = httptest.NewRequest(method, target, strings.NewReader(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = httptest.NewRequest(method, target, bytes.NewReader(raw))
	}
	r = r.WithContext(auth.WithPrincipal(r.Context(), f.user, f.sess))
	if bookID > 0 {
		r.SetPathValue("bookId", strconv.FormatInt(bookID, 10))
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec.Result()
}

// fixture is one server with a seeded user, two books and a fake publisher.
type progressFixture struct {
	srv   *Server
	user  *auth.User
	sess  *auth.Session
	books []int64
	pub   *fakeProgressPublisher
}

func decodeProgress(t *testing.T, res *http.Response) Progress {
	t.Helper()
	var p Progress
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode progress from %q: %v", raw, err)
	}
	return p
}

func decodeProgressErr(t *testing.T, res *http.Response) ErrorBody {
	t.Helper()
	var e ErrorResponse
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("decode error from %q: %v", raw, err)
	}
	return e.Error
}

func newProgressFixture(t *testing.T) *progressFixture {
	t.Helper()
	s, u, sess, books := newProgressTestServer(t)
	pub := &fakeProgressPublisher{}
	s.Events = pub
	return &progressFixture{srv: s, user: u, sess: sess, books: books, pub: pub}
}

func TestPutProgressAccepted(t *testing.T) {
	f := newProgressFixture(t)

	res := f.call(t, f.srv.putProgress, http.MethodPut, "/api/v1/progress/1", f.books[0],
		ProgressReport{PositionMs: 5_000, FileIndex: 0, ClientListenedAt: 100, ClientNow: 100})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	got := decodeProgress(t, res)
	if got.PositionMs != 5_000 || got.BookID != f.books[0] {
		t.Fatalf("body = %+v, want position 5000 for book %d", got, f.books[0])
	}
	if got.Seq == 0 {
		t.Errorf("seq = 0, want an assigned seq")
	}
	if got.DurationMs != 20_000_000 {
		t.Errorf("duration_ms = %d, want the book duration 20000000", got.DurationMs)
	}
	if got.DeviceID != "device-a" || got.DeviceName != "iPhone" {
		t.Errorf("device = %q/%q, want the session device", got.DeviceID, got.DeviceName)
	}
	if n := f.pub.named("progress"); n != 1 {
		t.Errorf("published %d progress events, want 1", n)
	}

	// The record is then readable.
	res = f.call(t, f.srv.getProgress, http.MethodGet, "/api/v1/progress/1", f.books[0], nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", res.StatusCode)
	}
	if p := decodeProgress(t, res); p.PositionMs != 5_000 {
		t.Errorf("get position = %d, want 5000", p.PositionMs)
	}
}

func TestPutProgressConflictReturnsCurrent(t *testing.T) {
	f := newProgressFixture(t)

	res := f.call(t, f.srv.putProgress, http.MethodPut, "/api/v1/progress/1", f.books[0],
		ProgressReport{PositionMs: 500_000, ClientListenedAt: 100, ClientNow: 100})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("first put status = %d, want 200", res.StatusCode)
	}
	current := decodeProgress(t, res)

	// A report describing a listen an hour old loses to the stored one.
	res = f.call(t, f.srv.putProgress, http.MethodPut, "/api/v1/progress/1", f.books[0],
		ProgressReport{PositionMs: 10_000, ClientListenedAt: 0, ClientNow: 3_600_000})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("second put status = %d, want 409", res.StatusCode)
	}
	got := decodeProgress(t, res)
	if got.PositionMs != current.PositionMs || got.Seq != current.Seq {
		t.Fatalf("409 body = %+v, want the current record %+v", got, current)
	}
	if n := f.pub.named("progress"); n != 1 {
		t.Errorf("published %d progress events, want 1 (rejected writes publish nothing)", n)
	}
}

func TestPutProgressErrors(t *testing.T) {
	f := newProgressFixture(t)

	cases := []struct {
		name     string
		bookID   int64
		body     any
		wantCode int
		wantErr  string
	}{
		{"invalid json", f.books[0], "{not json", http.StatusBadRequest, "bad_request"},
		{"negative position", f.books[0],
			ProgressReport{PositionMs: -1, ClientListenedAt: 100, ClientNow: 100},
			http.StatusBadRequest, "bad_request"},
		{"stale report", f.books[0],
			ProgressReport{PositionMs: 1_000, ClientListenedAt: 0, ClientNow: 31 * 24 * 60 * 60 * 1000},
			http.StatusBadRequest, "stale_report"},
		{"unknown book", 987654,
			ProgressReport{PositionMs: 1_000, ClientListenedAt: 100, ClientNow: 100},
			http.StatusNotFound, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := f.call(t, f.srv.putProgress, http.MethodPut, "/api/v1/progress/1", tc.bookID, tc.body)
			if res.StatusCode != tc.wantCode {
				t.Fatalf("status = %d, want %d", res.StatusCode, tc.wantCode)
			}
			if code := decodeProgressErr(t, res).Code; code != tc.wantErr {
				t.Fatalf("error code = %q, want %q", code, tc.wantErr)
			}
		})
	}
}

func TestGetProgressNotFound(t *testing.T) {
	f := newProgressFixture(t)
	res := f.call(t, f.srv.getProgress, http.MethodGet, "/api/v1/progress/1", f.books[0], nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	if code := decodeProgressErr(t, res).Code; code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

func TestBeaconProgress(t *testing.T) {
	f := newProgressFixture(t)

	// Wrong token: 403, nothing stored.
	res := f.call(t, f.srv.beaconProgress, http.MethodPost, "/api/v1/progress/1/beacon", f.books[0],
		ProgressReport{PositionMs: 4_000, ClientListenedAt: 100, ClientNow: 100, CSRFToken: "wrong"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("bad csrf status = %d, want 403", res.StatusCode)
	}
	if code := decodeProgressErr(t, res).Code; code != "csrf" {
		t.Fatalf("error code = %q, want csrf", code)
	}
	if rec, err := f.srv.progressStore().Get(context.Background(), 1, f.books[0]); err != nil || rec != nil {
		t.Fatalf("a rejected beacon stored %+v (err %v), want nothing", rec, err)
	}

	// Missing token is equally refused.
	res = f.call(t, f.srv.beaconProgress, http.MethodPost, "/api/v1/progress/1/beacon", f.books[0],
		ProgressReport{PositionMs: 4_000, ClientListenedAt: 100, ClientNow: 100})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d, want 403", res.StatusCode)
	}
	res.Body.Close()

	// Correct token: 204 and the write lands.
	res = f.call(t, f.srv.beaconProgress, http.MethodPost, "/api/v1/progress/1/beacon", f.books[0],
		ProgressReport{PositionMs: 4_000, ClientListenedAt: 100, ClientNow: 100, CSRFToken: f.sess.CSRFToken})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("good csrf status = %d, want 204", res.StatusCode)
	}
	res.Body.Close()
	rec, err := f.srv.progressStore().Get(context.Background(), f.sess.UserID, f.books[0])
	if err != nil || rec == nil || rec.PositionMs != 4_000 {
		t.Fatalf("stored record = %+v (err %v), want position 4000", rec, err)
	}
	if n := f.pub.named("progress"); n != 1 {
		t.Errorf("published %d progress events, want 1", n)
	}

	// A losing beacon still answers 204 and publishes nothing extra.
	res = f.call(t, f.srv.beaconProgress, http.MethodPost, "/api/v1/progress/1/beacon", f.books[0],
		ProgressReport{PositionMs: 1, ClientListenedAt: 0, ClientNow: 3_600_000, CSRFToken: f.sess.CSRFToken})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("rejected beacon status = %d, want 204", res.StatusCode)
	}
	res.Body.Close()
	if n := f.pub.named("progress"); n != 1 {
		t.Errorf("published %d progress events, want 1", n)
	}
	rec, err = f.srv.progressStore().Get(context.Background(), f.sess.UserID, f.books[0])
	if err != nil || rec == nil || rec.PositionMs != 4_000 {
		t.Fatalf("stored record = %+v (err %v), want the position to be unchanged", rec, err)
	}
}

func TestListProgressSince(t *testing.T) {
	f := newProgressFixture(t)

	// Nothing stored yet: an empty array, never null.
	res := f.call(t, f.srv.listProgress, http.MethodGet, "/api/v1/progress", 0, nil)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if got := strings.TrimSpace(string(raw)); got != "[]" {
		t.Fatalf("empty list body = %q, want []", got)
	}

	put := func(bookID, pos int64) Progress {
		t.Helper()
		res := f.call(t, f.srv.putProgress, http.MethodPut, "/api/v1/progress/1", bookID,
			ProgressReport{PositionMs: pos, ClientListenedAt: 100, ClientNow: 100})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("put status = %d, want 200", res.StatusCode)
		}
		return decodeProgress(t, res)
	}
	first := put(f.books[0], 1_000)
	second := put(f.books[1], 2_000)
	if second.Seq <= first.Seq {
		t.Fatalf("seq did not increase across books: %d then %d", first.Seq, second.Seq)
	}

	list := func(q string) []Progress {
		t.Helper()
		res := f.call(t, f.srv.listProgress, http.MethodGet, "/api/v1/progress"+q, 0, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("list status = %d, want 200", res.StatusCode)
		}
		var out []Progress
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode list %q: %v", raw, err)
		}
		return out
	}

	if all := list(""); len(all) != 2 {
		t.Fatalf("list all returned %d records, want 2", len(all))
	}
	since := list("?since=" + strconv.FormatInt(first.Seq, 10))
	if len(since) != 1 || since[0].BookID != f.books[1] {
		t.Fatalf("?since=%d returned %+v, want only book %d", first.Seq, since, f.books[1])
	}
	if newest := list("?since=" + strconv.FormatInt(second.Seq, 10)); len(newest) != 0 {
		t.Fatalf("?since=newest returned %+v, want nothing", newest)
	}
	if n := f.pub.named("progress"); n != 2 {
		t.Errorf("published %d progress events, want 2", n)
	}
}

func TestPutProgressRate(t *testing.T) {
	f := newProgressFixture(t)

	res := f.call(t, f.srv.putProgressRate, http.MethodPut, "/api/v1/progress/1/rate", f.books[0],
		RateUpdate{PlaybackRate: float64Ptr(1.5)})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	got := decodeProgress(t, res)
	if got.PlaybackRate == nil || *got.PlaybackRate != 1.5 {
		t.Fatalf("playback_rate = %v, want 1.5", got.PlaybackRate)
	}
	if got.PositionMs != 0 || got.Finished {
		t.Fatalf("fresh row = %+v, want position 0, not finished", got)
	}

	// A subsequent GET reflects the same rate.
	res = f.call(t, f.srv.getProgress, http.MethodGet, "/api/v1/progress/1", f.books[0], nil)
	if got := decodeProgress(t, res); got.PlaybackRate == nil || *got.PlaybackRate != 1.5 {
		t.Fatalf("get playback_rate = %v, want 1.5", got.PlaybackRate)
	}

	// A real listen report must not reset the rate.
	res = f.call(t, f.srv.putProgress, http.MethodPut, "/api/v1/progress/1", f.books[0],
		ProgressReport{PositionMs: 9_000, ClientListenedAt: 100, ClientNow: 100})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("report status = %d, want 200", res.StatusCode)
	}
	if got := decodeProgress(t, res); got.PlaybackRate == nil || *got.PlaybackRate != 1.5 {
		t.Fatalf("playback_rate after report = %v, want preserved 1.5", got.PlaybackRate)
	}

	// A null body clears it, leaving the position untouched.
	res = f.call(t, f.srv.putProgressRate, http.MethodPut, "/api/v1/progress/1/rate", f.books[0],
		RateUpdate{PlaybackRate: nil})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("clear status = %d, want 200", res.StatusCode)
	}
	got = decodeProgress(t, res)
	if got.PlaybackRate != nil {
		t.Fatalf("playback_rate after clear = %v, want nil", got.PlaybackRate)
	}
	if got.PositionMs != 9_000 {
		t.Fatalf("position after clear = %d, want 9000 (untouched)", got.PositionMs)
	}
}

func TestPutProgressRateErrors(t *testing.T) {
	f := newProgressFixture(t)

	res := f.call(t, f.srv.putProgressRate, http.MethodPut, "/api/v1/progress/1/rate", f.books[0],
		RateUpdate{PlaybackRate: float64Ptr(0.4)})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("low rate status = %d, want 400", res.StatusCode)
	}
	if code := decodeProgressErr(t, res).Code; code != "bad_request" {
		t.Fatalf("low rate error code = %q, want bad_request", code)
	}

	res = f.call(t, f.srv.putProgressRate, http.MethodPut, "/api/v1/progress/1/rate", f.books[0],
		RateUpdate{PlaybackRate: float64Ptr(3.1)})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("high rate status = %d, want 400", res.StatusCode)
	}

	res = f.call(t, f.srv.putProgressRate, http.MethodPut, "/api/v1/progress/1/rate", 987654,
		RateUpdate{PlaybackRate: float64Ptr(1.0)})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown book status = %d, want 404", res.StatusCode)
	}
	if code := decodeProgressErr(t, res).Code; code != "not_found" {
		t.Fatalf("unknown book error code = %q, want not_found", code)
	}
}
