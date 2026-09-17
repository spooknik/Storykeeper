package auth

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	ok, retry := l.Allow("alice")
	if ok || retry <= 0 || retry > time.Minute {
		t.Fatalf("4th attempt should be refused with a retry-after within the window, got ok=%v retry=%v", ok, retry)
	}
	if ok, _ := l.Allow("bob"); !ok {
		t.Fatal("other keys are independent")
	}

	now = now.Add(time.Minute)
	if ok, _ := l.Allow("alice"); !ok {
		t.Fatal("window expired: alice should be allowed again")
	}

	l.Reset("alice")
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("alice"); !ok {
			t.Fatalf("after reset attempt %d should be allowed", i+1)
		}
	}

	now = now.Add(2 * time.Minute)
	l.Allow("carol") // triggers gc
	l.mu.Lock()
	_, aliceKept := l.buckets["alice"]
	l.mu.Unlock()
	if aliceKept {
		t.Fatal("expired bucket should have been garbage collected")
	}
}
