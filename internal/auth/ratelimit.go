package auth

import (
	"sync"
	"time"
)

// Limiter is a fixed-window counter keyed by an arbitrary string (a username,
// a client IP). It is intentionally simple: the goal is to turn an online
// password-guessing attack from "unlimited" into "a handful of tries per
// window", not to shape traffic precisely.
type Limiter struct {
	Limit  int
	Window time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
	now     func() time.Time
}

type bucket struct {
	count int
	start time.Time
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{Limit: limit, Window: window, buckets: map[string]*bucket{}, now: time.Now}
}

// Allow records an attempt for key and reports whether it is within the
// limit. The retry-after duration is non-zero only when refused.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	b := l.buckets[key]
	if b == nil || now.Sub(b.start) >= l.Window {
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	if b.count >= l.Limit {
		return false, l.Window - now.Sub(b.start)
	}
	b.count++
	return true, 0
}

// Reset forgets key, e.g. after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// gc drops expired buckets at most once per window so the map stays bounded
// by the number of distinct keys seen in the last window.
func (l *Limiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < l.Window {
		return
	}
	l.lastGC = now
	for k, b := range l.buckets {
		if now.Sub(b.start) >= l.Window {
			delete(l.buckets, k)
		}
	}
}
