// Package auth implements magic-link and session authentication. Raw bearer
// tokens never cross the Store boundary; SQLite only receives SHA-256 hashes.
package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrRateLimited is deliberately handled like a successful request by the
	// HTTP layer so the response cannot be used to enumerate registered emails.
	ErrRateLimited = errors.New("magic-link request rate limited")
	ErrInvalidLink = errors.New("magic link is invalid, expired, or already used")
)

const (
	rateWindow     = 15 * time.Minute
	emailRateLimit = 3
	ipRateLimit    = 10
)

// User is the signed-in Nightloch identity.
type User struct {
	ID    int64
	Email string
}

// Store persists authentication state.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateMagicLink atomically consumes both rate-limit quotas and stores the
// already-hashed magic-link token.
func (s *Store) CreateMagicLink(ctx context.Context, email, ip string, tokenHash [32]byte, now, expires time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin magic-link request: %w", err)
	}
	defer tx.Rollback()

	allowed, err := consumeRateLimits(ctx, tx, email, ip, now)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRateLimited
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO magic_link_tokens(token_hash, email, request_ip, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`, tokenHash[:], email, ip, now.Unix(), expires.Unix()); err != nil {
		return fmt.Errorf("store magic-link token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit magic-link request: %w", err)
	}
	return nil
}

func consumeRateLimits(ctx context.Context, tx *sql.Tx, email, ip string, now time.Time) (bool, error) {
	type quota struct {
		kind  string
		key   string
		limit int
		hash  [32]byte
		start int64
		count int
	}
	quotas := []quota{
		{kind: "email", key: email, limit: emailRateLimit},
		{kind: "ip", key: ip, limit: ipRateLimit},
	}
	for index := range quotas {
		quota := &quotas[index]
		quota.hash = sha256.Sum256([]byte(quota.kind + "\x00" + quota.key))
		err := tx.QueryRowContext(ctx, `
			SELECT window_started_at, request_count
			FROM auth_rate_limits WHERE kind = ? AND key_hash = ?`, quota.kind, quota.hash[:]).Scan(&quota.start, &quota.count)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("read %s rate limit: %w", quota.kind, err)
		}
		if errors.Is(err, sql.ErrNoRows) || now.Unix()-quota.start >= int64(rateWindow/time.Second) {
			quota.start = now.Unix()
			quota.count = 0
		}
		if quota.count >= quota.limit {
			return false, nil
		}
	}

	for _, quota := range quotas {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO auth_rate_limits(kind, key_hash, window_started_at, request_count)
			VALUES (?, ?, ?, 1)
			ON CONFLICT(kind, key_hash) DO UPDATE SET
				window_started_at = excluded.window_started_at,
				request_count = CASE
					WHEN auth_rate_limits.window_started_at = excluded.window_started_at
					THEN auth_rate_limits.request_count + 1
					ELSE 1
				END`, quota.kind, quota.hash[:], quota.start); err != nil {
			return false, fmt.Errorf("update %s rate limit: %w", quota.kind, err)
		}
	}
	return true, nil
}

// InvalidateMagicLink makes an undelivered link unusable.
func (s *Store) InvalidateMagicLink(ctx context.Context, tokenHash [32]byte, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE magic_link_tokens SET used_at = ?
		WHERE token_hash = ? AND used_at IS NULL`, now.Unix(), tokenHash[:])
	if err != nil {
		return fmt.Errorf("invalidate magic link: %w", err)
	}
	return nil
}

// ConsumeMagicLink marks a link used, creates the User if this is their first
// successful sign-in, and stores a hashed session token in one transaction.
func (s *Store) ConsumeMagicLink(ctx context.Context, linkHash, sessionHash [32]byte, now, sessionExpires time.Time) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin magic-link consumption: %w", err)
	}
	defer tx.Rollback()

	var email string
	var expiresAt int64
	var usedAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT email, expires_at, used_at FROM magic_link_tokens
		WHERE token_hash = ?`, linkHash[:]).Scan(&email, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidLink
	}
	if err != nil {
		return User{}, fmt.Errorf("read magic link: %w", err)
	}
	if usedAt.Valid || now.Unix() >= expiresAt {
		return User{}, ErrInvalidLink
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE magic_link_tokens SET used_at = ?
		WHERE token_hash = ? AND used_at IS NULL`, now.Unix(), linkHash[:])
	if err != nil {
		return User{}, fmt.Errorf("consume magic link: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return User{}, ErrInvalidLink
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users(email, created_at) VALUES (?, ?)
		ON CONFLICT(email) DO NOTHING`, email, now.Unix()); err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	var user User
	if err := tx.QueryRowContext(ctx, "SELECT id, email FROM users WHERE email = ?", email).Scan(&user.ID, &user.Email); err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions(user_id, token_hash, created_at, expires_at)
		VALUES (?, ?, ?, ?)`, user.ID, sessionHash[:], now.Unix(), sessionExpires.Unix()); err != nil {
		return User{}, fmt.Errorf("create session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit magic-link consumption: %w", err)
	}
	return user, nil
}

// Authenticate returns the User for a live session hash.
func (s *Store) Authenticate(ctx context.Context, sessionHash [32]byte, now time.Time) (User, error) {
	var user User
	err := s.db.QueryRowContext(ctx, `
		SELECT users.id, users.email
		FROM sessions JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = ?
		  AND sessions.revoked_at IS NULL
		  AND sessions.expires_at > ?
		  -- A deleted User has no session. Their row survives so the Activity
		  -- and receipts that reference it do, not so it can still sign in.
		  AND users.deleted_at IS NULL`, sessionHash[:], now.Unix()).Scan(&user.ID, &user.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrInvalidLink
	}
	if err != nil {
		return User{}, fmt.Errorf("authenticate session: %w", err)
	}
	return user, nil
}

// RevokeSession immediately invalidates the matching session, if present.
func (s *Store) RevokeSession(ctx context.Context, sessionHash [32]byte, now time.Time) error {
	if _, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ?
		WHERE token_hash = ? AND revoked_at IS NULL`, now.Unix(), sessionHash[:]); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}
