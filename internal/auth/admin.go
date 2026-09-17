package auth

import (
	"context"
	"database/sql"
	"errors"
)

// ErrLastAdmin is returned when an operation would leave the system with no
// remaining admin user.
var ErrLastAdmin = errors.New("cannot remove the last admin")

// ListUsers returns every user, ordered by username.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, username, role, created_at FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// adminCount returns the number of admins other than excludeID.
func (s *Service) adminCount(ctx context.Context, excludeID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = ? AND id != ?`, RoleAdmin, excludeID).Scan(&n)
	return n, err
}

// UpdateUser applies the given optional changes to a user. Updating the
// password revokes all of that user's existing sessions. Demoting the last
// remaining admin returns ErrLastAdmin. A missing user returns sql.ErrNoRows.
func (s *Service) UpdateUser(ctx context.Context, id int64, password, role *string) error {
	if role != nil && *role != RoleAdmin && *role != RoleUser {
		return errors.New("invalid role")
	}
	if password != nil && len(*password) < 8 {
		return errors.New("password must be at least 8 characters")
	}

	var currentRole string
	if err := s.DB.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, id).Scan(&currentRole); err != nil {
		return err
	}

	if role != nil && *role != RoleAdmin && currentRole == RoleAdmin {
		n, err := s.adminCount(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastAdmin
		}
	}

	var hash string
	if password != nil {
		var err error
		hash, err = HashPassword(*password)
		if err != nil {
			return err
		}
	}

	return s.DB.Write(ctx, func(tx *sql.Tx) error {
		if role != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, *role, id); err != nil {
				return err
			}
		}
		if password != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteUser removes a user. Deleting the last remaining admin returns
// ErrLastAdmin. A missing user returns sql.ErrNoRows.
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	var role string
	if err := s.DB.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
		return err
	}
	if role == RoleAdmin {
		n, err := s.adminCount(ctx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastAdmin
		}
	}
	return s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
		return err
	})
}

// ListSessions returns every session belonging to userID, most recently seen first.
func (s *Service) ListSessions(ctx context.Context, userID int64) ([]Session, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, csrf_token, device_id, device_name,
			created_at, last_seen_at, expires_at
		FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.CSRFToken, &sess.DeviceID, &sess.DeviceName,
			&sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// DeleteSession revokes sessionID, but only if it belongs to userID.
// Any other case (missing, or owned by someone else) returns ErrNoSession.
func (s *Service) DeleteSession(ctx context.Context, userID, sessionID int64) error {
	var owner int64
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE id = ?`, sessionID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSession
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrNoSession
	}
	return s.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
		return err
	})
}
