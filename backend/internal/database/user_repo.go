package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

const pgUniqueViolation = "23505"

// UserRepository persists users and their lockout state.
type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository { return &UserRepository{pool: pool} }

const userColumns = `id, email, display_name, password_hash, failed_login_count, locked_until, last_login_at, created_at`

func scanUser(row pgx.Row) (*models.UserAccount, error) {
	u := &models.UserAccount{}
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &u.FailedLoginCount,
		&u.LockedUntil, &u.LastLoginAt, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, models.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Create inserts a user. Returns models.ErrEmailTaken on a duplicate email.
func (r *UserRepository) Create(ctx context.Context, email string, displayName *string, passwordHash string) (*models.UserAccount, error) {
	u, err := scanUser(r.pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash) VALUES ($1, $2, $3) RETURNING `+userColumns,
		email, displayName, passwordHash))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return nil, models.ErrEmailTaken
	}
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// GetByEmail returns models.ErrNotFound when no user has that (lower-case) email.
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*models.UserAccount, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email))
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return u, err
}

// GetByID returns models.ErrNotFound when the user does not exist.
func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.UserAccount, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, err
}

// RecordLoginFailure atomically increments the failure counter. When it
// reaches maxFailures the account is locked for lockout and the counter is
// reset to 0. It returns the resulting lock expiry (nil when not locked).
func (r *UserRepository) RecordLoginFailure(ctx context.Context, id uuid.UUID, maxFailures int, lockout time.Duration) (*time.Time, error) {
	const q = `
		UPDATE users SET
		  failed_login_count = CASE WHEN failed_login_count + 1 >= $2 THEN 0 ELSE failed_login_count + 1 END,
		  locked_until = CASE WHEN failed_login_count + 1 >= $2 THEN now() + make_interval(secs => $3) ELSE locked_until END,
		  updated_at = now()
		WHERE id = $1
		RETURNING locked_until`
	var lockedUntil *time.Time
	err := r.pool.QueryRow(ctx, q, id, maxFailures, lockout.Seconds()).Scan(&lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, models.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("record login failure: %w", err)
	}
	return lockedUntil, nil
}

// RecordLoginSuccess resets the failure counter, clears any lock and stamps last_login_at.
func (r *UserRepository) RecordLoginSuccess(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users SET failed_login_count = 0, locked_until = NULL, last_login_at = now(), updated_at = now()
		WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("record login success: %w", err)
	}
	return nil
}

// UpdatePasswordHash replaces the stored hash (used for transparent re-hashing).
func (r *UserRepository) UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	if err != nil {
		return fmt.Errorf("update password hash: %w", err)
	}
	return nil
}
