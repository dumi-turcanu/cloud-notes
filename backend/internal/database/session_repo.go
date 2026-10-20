package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// TouchInterval is the minimum gap between last_seen_at updates of a session.
const TouchInterval = time.Minute

// SessionRepository persists login sessions. Only SHA-256 token hashes are stored.
// Time is passed in by the caller so expiry logic uses a single injectable clock.
type SessionRepository struct {
	pool *pgxpool.Pool
}

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create stores a new session.
func (r *SessionRepository) Create(ctx context.Context, s models.Session) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, created_at, last_seen_at, expires_at, user_agent, client_ip)
		VALUES ($1, $2, $3, $3, $4, $5, $6)`,
		s.TokenHash, s.UserID, s.CreatedAt, s.ExpiresAt, truncate(s.UserAgent, 512), truncate(s.ClientIP, 64))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// GetActive returns the session and its user if it exists, has not passed its
// absolute expiry and was seen within the idle timeout. Otherwise models.ErrNotFound.
func (r *SessionRepository) GetActive(ctx context.Context, tokenHash []byte, now time.Time, idle time.Duration) (*models.ActiveSession, error) {
	const q = `
		SELECT s.token_hash, s.user_id, s.created_at, s.last_seen_at, s.expires_at,
		       u.id, u.email, u.display_name, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2 AND s.last_seen_at > $3`
	a := &models.ActiveSession{}
	err := r.pool.QueryRow(ctx, q, tokenHash, now, now.Add(-idle)).Scan(
		&a.TokenHash, &a.UserID, &a.CreatedAt, &a.LastSeenAt, &a.ExpiresAt,
		&a.User.ID, &a.User.Email, &a.User.DisplayName, &a.User.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, models.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get active session: %w", err)
	}
	return a, nil
}

// Touch bumps last_seen_at, but only if it is older than TouchInterval.
func (r *SessionRepository) Touch(ctx context.Context, tokenHash []byte, now time.Time) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = $2 WHERE token_hash = $1 AND last_seen_at < $3`,
		tokenHash, now, now.Add(-TouchInterval))
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// Delete removes a session (idempotent).
func (r *SessionRepository) Delete(ctx context.Context, tokenHash []byte) error {
	if _, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteExpired removes sessions past their absolute expiry or idle timeout.
// Idempotent and safe to run concurrently from many replicas.
func (r *SessionRepository) DeleteExpired(ctx context.Context, now time.Time, idle time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM sessions WHERE expires_at <= $1 OR last_seen_at <= $2`, now, now.Add(-idle))
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
