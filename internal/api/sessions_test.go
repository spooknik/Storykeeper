package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/spooknik/storykeeper/internal/auth"
)

func TestListSessions_MarksCurrent(t *testing.T) {
	s := newTestServer(t)
	mustCreateUser(t, s, "alice", "password123", auth.RoleUser)

	ctx := context.Background()
	u1, sessA, _, err := s.Auth.Login(ctx, "alice", "password123", "device-a")
	if err != nil {
		t.Fatalf("login a: %v", err)
	}
	_, sessB, _, err := s.Auth.Login(ctx, "alice", "password123", "device-b")
	if err != nil {
		t.Fatalf("login b: %v", err)
	}

	w := httptest.NewRecorder()
	r := reqAs(http.MethodGet, "/api/v1/sessions", nil, u1, sessB)
	s.listSessions(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var out []sessionListItem
	decodeBody(t, w, &out)
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	var gotCurrent int64 = -1
	for _, item := range out {
		if item.Current {
			gotCurrent = item.ID
		}
	}
	if gotCurrent != sessB.ID {
		t.Fatalf("current session id = %d, want %d", gotCurrent, sessB.ID)
	}
	_ = sessA
}

func TestDeleteSession_OtherUsersSessionNotFound(t *testing.T) {
	s := newTestServer(t)
	mustCreateUser(t, s, "alice", "password123", auth.RoleUser)
	mustCreateUser(t, s, "bob", "password123", auth.RoleUser)

	ctx := context.Background()
	u1, _, _, err := s.Auth.Login(ctx, "alice", "password123", "device-a")
	if err != nil {
		t.Fatalf("login alice: %v", err)
	}
	_, sessBob, _, err := s.Auth.Login(ctx, "bob", "password123", "device-b")
	if err != nil {
		t.Fatalf("login bob: %v", err)
	}

	w := httptest.NewRecorder()
	target := "/api/v1/sessions/" + strconv.FormatInt(sessBob.ID, 10)
	r := reqAs(http.MethodDelete, target, nil, u1, nil)
	r.SetPathValue("id", strconv.FormatInt(sessBob.ID, 10))
	s.deleteSession(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}

	// Bob's session must still exist.
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ?`, sessBob.ID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 1 {
		t.Fatalf("bob's session count = %d, want 1", n)
	}
}
