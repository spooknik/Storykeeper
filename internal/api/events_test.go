package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spooknik/storykeeper/internal/auth"
	"github.com/spooknik/storykeeper/internal/events"
)

// eventStream opens GET /events against a real HTTP server and reports when
// the server closes the stream. session picks which principal the test server
// attaches ("" = the first one).
type eventStream struct {
	ended chan struct{}
}

func openEventStream(t *testing.T, srv *httptest.Server, session string) *eventStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if session != "" {
		req.Header.Set("X-Session", session)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	es := &eventStream{ended: make(chan struct{})}
	go func() {
		defer close(es.ended)
		br := bufio.NewReader(resp.Body)
		for {
			if _, err := br.ReadString('\n'); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = resp.Body.Close() })
	return es
}

func (e *eventStream) waitEnd(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-e.ended:
	case <-time.After(d):
		t.Fatalf("event stream still open %s after the session was revoked", d)
	}
}

func (e *eventStream) expectAlive(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case <-e.ended:
		t.Fatal("event stream ended, want it still open")
	case <-time.After(d):
	}
}

// TestEvents_RevokedSessionStreamEnds: deleting a session must terminate that
// session's live SSE stream promptly, leaving the user's other devices alone.
func TestEvents_RevokedSessionStreamEnds(t *testing.T) {
	s := newTestServer(t)
	hub := events.New()
	hub.PingInterval = 50 * time.Millisecond
	s.Events = hub

	mustCreateUser(t, s, "alice", "password123", auth.RoleUser)
	ctx := context.Background()
	u, sessA, _, err := s.Auth.Login(ctx, "alice", "password123", "device-a")
	if err != nil {
		t.Fatalf("login a: %v", err)
	}
	_, sessB, _, err := s.Auth.Login(ctx, "alice", "password123", "device-b")
	if err != nil {
		t.Fatalf("login b: %v", err)
	}

	// The test server attaches whichever principal the request asks for: the
	// stream handler needs a user and a session in its context.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := sessA
		if r.Header.Get("X-Session") == "b" {
			sess = sessB
		}
		s.events(w, r.WithContext(auth.WithPrincipal(r.Context(), u, sess)))
	}))
	t.Cleanup(srv.Close)

	streamA := openEventStream(t, srv, "")
	streamB := openEventStream(t, srv, "b")

	// Both streams are live.
	deadline := time.Now().Add(3 * time.Second)
	for hub.Subscribers(u.ID) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := hub.Subscribers(u.ID); n != 2 {
		t.Fatalf("subscribers = %d, want 2", n)
	}

	// Revoke session A through the API.
	w := httptest.NewRecorder()
	r := setID(reqAs(http.MethodDelete, "/api/v1/sessions/x", nil, u, sessB), sessA.ID)
	s.deleteSession(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deleteSession status = %d, want 204; body=%s", w.Code, w.Body.String())
	}

	streamA.waitEnd(t, time.Second)
	streamB.expectAlive(t, 200*time.Millisecond)
}

// TestEvents_PasswordChangeEndsEveryStream: updateUser revokes all sessions of
// the user, so all of their streams must end too.
func TestEvents_PasswordChangeEndsEveryStream(t *testing.T) {
	s := newTestServer(t)
	hub := events.New()
	hub.PingInterval = 50 * time.Millisecond
	s.Events = hub

	mustCreateUser(t, s, "alice", "password123", auth.RoleUser)
	admin := &auth.User{ID: -1, Role: auth.RoleAdmin}
	u, sess, _, err := s.Auth.Login(context.Background(), "alice", "password123", "device-a")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.events(w, r.WithContext(auth.WithPrincipal(r.Context(), u, sess)))
	}))
	t.Cleanup(srv.Close)
	stream := openEventStream(t, srv, "")

	deadline := time.Now().Add(3 * time.Second)
	for hub.Subscribers(u.ID) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	pw := "a-brand-new-password"
	body := mustJSON(t, updateUserRequest{Password: &pw})
	w := httptest.NewRecorder()
	s.updateUser(w, setID(reqAs(http.MethodPatch, "/api/v1/users/x", body, admin, nil), u.ID))
	if w.Code != http.StatusOK {
		t.Fatalf("updateUser status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	stream.waitEnd(t, time.Second)
}
