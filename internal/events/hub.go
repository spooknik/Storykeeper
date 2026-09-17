// Package events implements the Server-Sent Events hub: one stream per session,
// a per-user replay buffer keyed by event id, and fan-out to every other device
// a user has connected.
//
// Event ids are one global monotonically increasing counter. Clients only use
// Last-Event-ID for ordering ("give me everything after this"), so a single
// sequence across all users is enough and keeps replay a simple comparison.
package events

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxEvents = 512              // per user, newest kept
	defaultMaxAge    = 5 * time.Minute  // per user, older records pruned
	defaultPing      = 20 * time.Second // `: ping` comment interval
	subscriberBuffer = 64               // frames queued before a client is dropped
)

var (
	retryFrame  = []byte("retry: 3000\n\n")
	pingFrame   = []byte(": ping\n\n")
	resyncFrame = []byte("event: resync\ndata: {}\n\n")
)

// record is one buffered event, already rendered as its wire frame.
type record struct {
	id    uint64
	at    time.Time
	frame []byte
}

type subscriber struct {
	ch chan []byte
}

type userState struct {
	buf  []record
	subs map[*subscriber]struct{}
}

// Hub fans events out to connected clients and buffers them for replay.
// The zero value is not usable; call New.
type Hub struct {
	// PingInterval is how often a `: ping` comment is written to each live
	// stream. Set it before serving; Serve samples it once per connection.
	PingInterval time.Duration

	mu        sync.Mutex
	lastID    uint64
	maxEvents int
	maxAge    time.Duration
	users     map[int64]*userState
}

// New returns a ready Hub.
func New() *Hub {
	return &Hub{
		PingInterval: defaultPing,
		maxEvents:    defaultMaxEvents,
		maxAge:       defaultMaxAge,
		users:        make(map[int64]*userState),
	}
}

// Publish sends a named event to every session of one user and buffers it for
// replay. It never blocks: a subscriber whose queue is full is dropped instead.
func (h *Hub) Publish(userID int64, name string, payload any) {
	data := encode(payload)

	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastID++
	id := h.lastID
	f := frame(id, name, data)
	us := h.stateLocked(userID)
	h.appendLocked(us, id, f)
	fanout(us, f)
}

// Broadcast sends a named event to every connected session and into the buffer
// of every currently known user (one with a buffer or a live subscriber).
func (h *Hub) Broadcast(name string, payload any) {
	data := encode(payload)

	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastID++
	id := h.lastID
	f := frame(id, name, data)
	for _, us := range h.users {
		h.appendLocked(us, id, f)
		fanout(us, f)
	}
}

// Subscribers reports how many live streams a user currently has.
func (h *Hub) Subscribers(userID int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if us, ok := h.users[userID]; ok {
		return len(us.subs)
	}
	return 0
}

// Serve streams events for userID until the request context is done.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, userID int64) {
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream; charset=utf-8")
	hdr.Set("Cache-Control", "no-cache, no-transform")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// The logging middleware implements Unwrap, so the controller finds Flush.
	rc := http.NewResponseController(w)
	write := func(b []byte) bool {
		if _, err := w.Write(b); err != nil {
			return false
		}
		// A missing Flush is not fatal; the write itself is what matters.
		_ = rc.Flush()
		return true
	}

	if !write(retryFrame) {
		return
	}

	lastID, hasLast := lastEventID(r)
	sub, replay, resync := h.subscribe(userID, lastID, hasLast)
	defer h.unsubscribe(userID, sub)

	if resync {
		// The client is further behind than the buffer reaches: tell it to
		// refetch state rather than handing it a gap.
		if !write(resyncFrame) {
			return
		}
	}
	for _, f := range replay {
		if !write(f) {
			return
		}
	}

	interval := h.PingInterval
	if interval <= 0 {
		interval = defaultPing
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-sub.ch:
			if !ok {
				return // dropped for being too slow; the client will reconnect
			}
			if !write(f) {
				return
			}
		case <-ticker.C:
			if !write(pingFrame) {
				return
			}
		}
	}
}

// --- internals ---

func encode(payload any) []byte {
	data, err := json.Marshal(payload)
	if err != nil {
		return []byte("null")
	}
	return data
}

// frame renders one event. JSON never contains a raw newline, so the payload is
// always a single `data:` line.
func frame(id uint64, name string, data []byte) []byte {
	b := make([]byte, 0, len(name)+len(data)+32)
	b = append(b, "event: "...)
	b = append(b, name...)
	b = append(b, "\nid: "...)
	b = strconv.AppendUint(b, id, 10)
	b = append(b, "\ndata: "...)
	b = append(b, data...)
	b = append(b, "\n\n"...)
	return b
}

func (h *Hub) stateLocked(userID int64) *userState {
	us, ok := h.users[userID]
	if !ok {
		us = &userState{subs: make(map[*subscriber]struct{})}
		h.users[userID] = us
	}
	return us
}

func (h *Hub) appendLocked(us *userState, id uint64, f []byte) {
	us.buf = append(us.buf, record{id: id, at: time.Now(), frame: f})
	h.pruneLocked(us)
}

// pruneLocked drops records older than maxAge and anything beyond maxEvents.
func (h *Hub) pruneLocked(us *userState) {
	cutoff := time.Now().Add(-h.maxAge)
	drop := 0
	for drop < len(us.buf) && us.buf[drop].at.Before(cutoff) {
		drop++
	}
	if n := len(us.buf) - h.maxEvents; n > drop {
		drop = n
	}
	if drop <= 0 {
		return
	}
	kept := copy(us.buf, us.buf[drop:])
	clear(us.buf[kept:])
	us.buf = us.buf[:kept]
}

// fanout delivers to every live subscriber, dropping any whose queue is full
// rather than blocking the publisher.
func fanout(us *userState, f []byte) {
	for s := range us.subs {
		select {
		case s.ch <- f:
		default:
			delete(us.subs, s)
			close(s.ch)
		}
	}
}

// subscribe registers a stream and decides its replay in one critical section,
// so no event can slip between the replay snapshot and the live feed.
func (h *Hub) subscribe(userID int64, lastID uint64, hasLast bool) (*subscriber, [][]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	us := h.stateLocked(userID)
	h.pruneLocked(us)

	var replay [][]byte
	resync := false
	if hasLast {
		switch {
		case len(us.buf) == 0:
			resync = lastID > 0
		case lastID+1 < us.buf[0].id:
			resync = true // the events the client missed are already gone
		default:
			for _, rec := range us.buf {
				if rec.id > lastID {
					replay = append(replay, rec.frame)
				}
			}
		}
	}

	s := &subscriber{ch: make(chan []byte, subscriberBuffer)}
	us.subs[s] = struct{}{}
	return s, replay, resync
}

func (h *Hub) unsubscribe(userID int64, s *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	us, ok := h.users[userID]
	if !ok {
		return
	}
	if _, ok := us.subs[s]; ok {
		delete(us.subs, s)
		close(s.ch)
	}
}

// lastEventID reads the resume point from the EventSource header, falling back
// to a query parameter (EventSource cannot set headers; tests and manual
// clients can use ?lastEventId=).
func lastEventID(r *http.Request) (uint64, bool) {
	v := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if v == "" {
		v = strings.TrimSpace(r.URL.Query().Get("lastEventId"))
	}
	if v == "" {
		return 0, false
	}
	id, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
