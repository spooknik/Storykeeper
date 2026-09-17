// Package auth implements users, password hashing and cookie sessions.
package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/spooknik/storykeeper/internal/db"
)

const (
	CookieName = "sk_session"
	RoleAdmin  = "admin"
	RoleUser   = "user"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrNoSession          = errors.New("no session")
)

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

type Session struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"-"`
	CSRFToken  string `json:"csrf_token"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	ExpiresAt  int64  `json:"expires_at"`
}

type Service struct {
	DB           *db.DB
	SessionTTL   time.Duration // sliding
	SecureCookie bool          // force Secure; otherwise inferred from the request
}

func New(d *db.DB, secureCookie bool) *Service {
	return &Service{DB: d, SessionTTL: 30 * 24 * time.Hour, SecureCookie: secureCookie}
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Service) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Service) CreateUser(ctx context.Context, username, password, role string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 8 {
		return nil, errors.New("username required and password must be at least 8 characters")
	}
	if role != RoleAdmin && role != RoleUser {
		return nil, errors.New("invalid role")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &User{Username: username, Role: role, CreatedAt: db.Now()}
	err = s.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO users (username, password_hash, role, created_at) VALUES (?, ?, ?, ?)`,
			u.Username, hash, u.Role, u.CreatedAt)
		if err != nil {
			return err
		}
		u.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Login verifies credentials and creates a session. It returns the raw token to put in the cookie.
func (s *Service) Login(ctx context.Context, username, password, deviceName string) (*User, *Session, string, error) {
	var u User
	var hash string
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, username, role, created_at, password_hash FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		VerifyPassword(dummyHash, password)
		return nil, nil, "", ErrInvalidCredentials
	}
	if err != nil {
		return nil, nil, "", err
	}
	if !VerifyPassword(hash, password) {
		return nil, nil, "", ErrInvalidCredentials
	}
	raw, err := RandomToken(32)
	if err != nil {
		return nil, nil, "", err
	}
	csrf, err := RandomToken(24)
	if err != nil {
		return nil, nil, "", err
	}
	deviceID, err := RandomToken(12)
	if err != nil {
		return nil, nil, "", err
	}
	now := db.Now()
	sess := &Session{UserID: u.ID, CSRFToken: csrf, DeviceID: deviceID, DeviceName: deviceName,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now + s.SessionTTL.Milliseconds()}
	err = s.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO sessions
			(user_id, token_hash, csrf_token, device_id, device_name, created_at, last_seen_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			sess.UserID, hashToken(raw), sess.CSRFToken, sess.DeviceID, sess.DeviceName,
			sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt)
		if err != nil {
			return err
		}
		sess.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, nil, "", err
	}
	return &u, sess, raw, nil
}

// Lookup resolves a raw cookie token to its user and session, sliding the expiry.
func (s *Service) Lookup(ctx context.Context, raw string) (*User, *Session, error) {
	if raw == "" {
		return nil, nil, ErrNoSession
	}
	var u User
	var sess Session
	err := s.DB.QueryRowContext(ctx, `SELECT s.id, s.user_id, s.csrf_token, s.device_id, s.device_name,
			s.created_at, s.last_seen_at, s.expires_at, u.id, u.username, u.role, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, hashToken(raw)).
		Scan(&sess.ID, &sess.UserID, &sess.CSRFToken, &sess.DeviceID, &sess.DeviceName,
			&sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &u.ID, &u.Username, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNoSession
	}
	if err != nil {
		return nil, nil, err
	}
	now := db.Now()
	if sess.ExpiresAt < now {
		_ = s.Logout(ctx, sess.ID)
		return nil, nil, ErrNoSession
	}
	// Slide expiry at most once every 10 minutes to keep writes rare.
	if now-sess.LastSeenAt > 10*time.Minute.Milliseconds() {
		sess.LastSeenAt = now
		sess.ExpiresAt = now + s.SessionTTL.Milliseconds()
		_ = s.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
				sess.LastSeenAt, sess.ExpiresAt, sess.ID)
			return err
		})
	}
	return &u, &sess, nil
}

func (s *Service) Logout(ctx context.Context, sessionID int64) error {
	return s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
		return err
	})
}

func (s *Service) secure(r *http.Request) bool {
	return s.SecureCookie || r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Service) SetCookie(w http.ResponseWriter, r *http.Request, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: raw, Path: "/", HttpOnly: true,
		Secure: s.secure(r), SameSite: http.SameSiteLaxMode,
		MaxAge: int(s.SessionTTL.Seconds()),
	})
}

func (s *Service) ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: s.secure(r), SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// --- request context ---

type ctxKey struct{}

type principal struct {
	user *User
	sess *Session
}

func WithPrincipal(ctx context.Context, u *User, sess *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, principal{u, sess})
}

// FromContext returns the authenticated user and session, or nils.
func FromContext(ctx context.Context) (*User, *Session) {
	p, _ := ctx.Value(ctxKey{}).(principal)
	return p.user, p.sess
}
