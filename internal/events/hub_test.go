package events

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- helpers -------------------------------------------------------------

type progress struct {
	BookID     int64 `json:"book_id"`
	PositionMS int64 `json:"position_ms"`
}

type sseFrame struct {
	comment string
	event   string
	id      string
	data    string
	retry   string
}

// serveHub exposes the hub over a real HTTP server so the flushing and
// streaming path is the one under test. The user is taken from ?uid=.
func serveHub(t *testing.T, h *Hub) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, _ := strconv.ParseInt(r.URL.Query().Get("uid"), 10, 64)
		h.Serve(w, r, uid)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type stream struct {
	t      *testing.T
	resp   *http.Response
	frames chan sseFrame
	done   chan struct{}
	cancel context.CancelFunc
	closed bool
}

// connect opens a stream for uid. header and query set Last-Event-ID via the
// request header and the ?lastEventId= fallback respectively; "" omits them.
func connect(t *testing.T, srv *httptest.Server, uid int64, header, query string) *stream {
	t.Helper()
	q := url.Values{"uid": {strconv.FormatInt(uid, 10)}}
	if query != "" {
		q.Set("lastEventId", query)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/?"+q.Encode(), nil)
	if err != nil {
		cancel()
		t.Fatalf("new request: %v", err)
	}
	if header != "" {
		req.Header.Set("Last-Event-ID", header)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if ab := resp.Header.Get("X-Accel-Buffering"); ab != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", ab)
	}

	s := &stream{t: t, resp: resp, frames: make(chan sseFrame, 64),
		done: make(chan struct{}), cancel: cancel}
	br := bufio.NewReader(resp.Body)
	go func() {
		for {
			f, err := readFrame(br)
			if err != nil {
				return
			}
			select {
			case s.frames <- f:
			case <-s.done:
				return
			}
		}
	}()
	t.Cleanup(s.close)

	// Every stream opens with the reconnect hint.
	if got := s.next(2 * time.Second); got.retry != "3000" {
		t.Fatalf("first frame = %+v, want retry: 3000", got)
	}
	return s
}

func (s *stream) close() {
	if s.closed {
		return
	}
	s.closed = true
	close(s.done)
	s.cancel()
	_ = s.resp.Body.Close()
}

func (s *stream) next(d time.Duration) sseFrame {
	s.t.Helper()
	select {
	case f, ok := <-s.frames:
		if !ok {
			s.t.Fatal("stream closed while waiting for a frame")
		}
		return f
	case <-time.After(d):
		s.t.Fatalf("timed out after %s waiting for a frame", d)
	}
	return sseFrame{}
}

func (s *stream) expectNone(d time.Duration) {
	s.t.Helper()
	select {
	case f := <-s.frames:
		s.t.Fatalf("unexpected extra frame: %+v", f)
	case <-time.After(d):
	}
}

func readFrame(br *bufio.Reader) (sseFrame, error) {
	var f sseFrame
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return f, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return f, nil
		}
		if strings.HasPrefix(line, ":") {
			f.comment = strings.TrimSpace(line[1:])
			continue
		}
		name, val, _ := strings.Cut(line, ":")
		val = strings.TrimPrefix(val, " ")
		switch name {
		case "event":
			f.event = val
		case "id":
			f.id = val
		case "data":
			f.data = val
		case "retry":
			f.retry = val
		}
	}
}

func waitSubs(t *testing.T, h *Hub, uid int64, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.Subscribers(uid) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("user %d: Subscribers = %d, want %d", uid, h.Subscribers(uid), want)
}

// --- tests ---------------------------------------------------------------

func TestPublishReachesSubscriber(t *testing.T) {
	h := New()
	srv := serveHub(t, h)
	s := connect(t, srv, 7, "", "")
	waitSubs(t, h, 7, 1)

	want := progress{BookID: 42, PositionMS: 1234}
	h.Publish(7, "progress", want)

	f := s.next(2 * time.Second)
	if f.event != "progress" {
		t.Errorf("event = %q, want progress", f.event)
	}
	if id, err := strconv.ParseUint(f.id, 10, 64); err != nil || id == 0 {
		t.Errorf("id = %q, want a positive integer", f.id)
	}
	var got progress
	if err := json.Unmarshal([]byte(f.data), &got); err != nil {
		t.Fatalf("data %q: %v", f.data, err)
	}
	if got != want {
		t.Errorf("data = %+v, want %+v", got, want)
	}
}

func TestReplayFromLastEventID(t *testing.T) {
	const uid = 42
	h := New()
	srv := serveHub(t, h)

	h.Publish(uid, "progress", progress{BookID: 1})
	h.Publish(uid, "progress", progress{BookID: 2})
	h.Publish(uid, "progress", progress{BookID: 3})

	// Discover the ids by replaying everything from 0.
	probe := connect(t, srv, uid, "", "0")
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		ids = append(ids, probe.next(2*time.Second).id)
	}
	probe.close()

	// Resuming at the first id must yield exactly the last two.
	s := connect(t, srv, uid, ids[0], "")
	for _, want := range ids[1:] {
		if got := s.next(2 * time.Second); got.id != want {
			t.Fatalf("replayed id = %q, want %q", got.id, want)
		}
	}
	s.expectNone(300 * time.Millisecond)
}

func TestResyncWhenBehindBuffer(t *testing.T) {
	const uid = 5
	h := New()
	h.maxEvents = 11 // keep only ids 10..20 below
	srv := serveHub(t, h)

	for i := 0; i < 20; i++ {
		h.Publish(uid, "progress", progress{BookID: int64(i)})
	}

	s := connect(t, srv, uid, "3", "")
	f := s.next(2 * time.Second)
	if f.event != "resync" {
		t.Fatalf("event = %q, want resync (frame %+v)", f.event, f)
	}
	if f.data != "{}" {
		t.Errorf("data = %q, want {}", f.data)
	}
	s.expectNone(300 * time.Millisecond)
}

func TestBroadcastReachesEveryUser(t *testing.T) {
	h := New()
	srv := serveHub(t, h)
	a := connect(t, srv, 1, "", "")
	b := connect(t, srv, 2, "", "")
	waitSubs(t, h, 1, 1)
	waitSubs(t, h, 2, 1)

	h.Broadcast("library", map[string]any{"library_id": 3, "action": "scanned"})

	for name, s := range map[string]*stream{"user 1": a, "user 2": b} {
		f := s.next(2 * time.Second)
		if f.event != "library" {
			t.Errorf("%s: event = %q, want library", name, f.event)
		}
		if !strings.Contains(f.data, `"library_id":3`) {
			t.Errorf("%s: data = %q", name, f.data)
		}
	}
}

func TestPingComment(t *testing.T) {
	h := New()
	h.PingInterval = 50 * time.Millisecond
	srv := serveHub(t, h)
	s := connect(t, srv, 9, "", "")

	if f := s.next(2 * time.Second); f.comment != "ping" {
		t.Fatalf("frame = %+v, want a ping comment", f)
	}
}

func TestDisconnectRemovesSubscriber(t *testing.T) {
	const uid = 11
	h := New()
	srv := serveHub(t, h)
	s := connect(t, srv, uid, "", "")
	waitSubs(t, h, uid, 1)

	s.close()
	waitSubs(t, h, uid, 0)
}

func TestPublishWithoutSubscribersDoesNotBlock(t *testing.T) {
	h := New()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			h.Publish(77, "progress", progress{BookID: int64(i)})
		}
		h.Broadcast("library", map[string]any{"action": "scanned"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked with no subscribers")
	}
	if n := h.Subscribers(77); n != 0 {
		t.Errorf("Subscribers = %d, want 0", n)
	}
}

// TestConcurrentPublishAndServe gives `go test -race` something to chew on:
// publishers, broadcasters and connecting/disconnecting streams at once.
func TestConcurrentPublishAndServe(t *testing.T) {
	h := New()
	h.PingInterval = 10 * time.Millisecond
	srv := serveHub(t, h)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(uid int64) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				h.Publish(uid, "progress", progress{BookID: uid})
				h.Broadcast("library", map[string]any{"library_id": uid})
			}
		}(int64(w%2) + 1)
	}
	for i := 0; i < 6; i++ {
		s := connect(t, srv, int64(i%2)+1, "", "0")
		s.next(2 * time.Second)
		s.close()
	}
	close(stop)
	wg.Wait()
}
