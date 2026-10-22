package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// UpsertExternal finds or creates the user behind an external identity, keyed
// on (issuer, subject). A known identity refreshes the email and display name
// from the claims; an unknown one creates a user without a password. It never
// links to an existing user by email: a taken email returns
// models.ErrAccountConflict.
func (r *UserRepository) UpsertExternal(ctx context.Context, id models.ExternalIdentity) (*models.User, error) {
	// Two first logins of the same person can race on the inserts; the loser
	// retries once and then finds the identity the winner created.
	u, err := r.upsertExternal(ctx, id)
	if errors.Is(err, errInsertRace) {
		u, err = r.upsertExternal(ctx, id)
		if errors.Is(err, errInsertRace) {
			return nil, models.ErrAccountConflict
		}
	}
	return u, err
}

var errInsertRace = errors.New("concurrent insert")

func (r *UserRepository) upsertExternal(ctx context.Context, id models.ExternalIdentity) (*models.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("upsert external identity: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var name *string
	if id.DisplayName != "" {
		name = &id.DisplayName
	}

	var userID string
	found := true
	err = tx.QueryRow(ctx,
		`UPDATE user_identities SET last_login_at = now() WHERE issuer = $1 AND subject = $2 RETURNING user_id::text`,
		id.Issuer, id.Subject).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		found = false
	} else if err != nil {
		return nil, fmt.Errorf("upsert external identity: look up: %w", err)
	}

	var u *models.User
	if found {
		u, err = refreshExternalUser(ctx, tx, userID, id.Email, name)
	} else {
		u, err = insertExternalUser(ctx, tx, id, name)
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("upsert external identity: commit: %w", err)
	}
	return u, nil
}

// refreshExternalUser updates the email (unless another account holds it) and
// the display name (unless the provider sent none) of an existing user.
func refreshExternalUser(ctx context.Context, tx pgx.Tx, userID, email string, name *string) (*models.User, error) {
	const withEmail = `UPDATE users SET email = $2, display_name = COALESCE($3, display_name),
		last_login_at = now(), updated_at = now() WHERE id = $1 RETURNING id, email, display_name, created_at`
	const keepEmail = `UPDATE users SET display_name = COALESCE($2, display_name),
		last_login_at = now(), updated_at = now() WHERE id = $1 RETURNING id, email, display_name, created_at`

	// The savepoint keeps a unique violation from aborting the whole transaction.
	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("upsert external identity: savepoint: %w", err)
	}
	u, err := scanPublicUser(sp.QueryRow(ctx, withEmail, userID, email, name))
	if err == nil {
		return u, sp.Commit(ctx)
	}
	_ = sp.Rollback(ctx)
	if !isUniqueViolation(err) {
		return nil, fmt.Errorf("upsert external identity: refresh user: %w", err)
	}
	slog.WarnContext(ctx, "external identity email already used by another account, keeping the old email", "user_id", userID)
	u, err = scanPublicUser(tx.QueryRow(ctx, keepEmail, userID, name))
	if err != nil {
		return nil, fmt.Errorf("upsert external identity: refresh user: %w", err)
	}
	return u, nil
}

func insertExternalUser(ctx context.Context, tx pgx.Tx, id models.ExternalIdentity, name *string) (*models.User, error) {
	u, err := scanPublicUser(tx.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, last_login_at) VALUES ($1, $2, NULL, now())
		 RETURNING id, email, display_name, created_at`, id.Email, name))
	if isUniqueViolation(err) {
		// Either another account owns this email (a real conflict) or a
		// concurrent first login of this person just created it; the retry
		// in UpsertExternal tells the two apart.
		return nil, errInsertRace
	}
	if err != nil {
		return nil, fmt.Errorf("upsert external identity: create user: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO user_identities (issuer, subject, user_id, last_login_at) VALUES ($1, $2, $3, now())`,
		id.Issuer, id.Subject, u.ID)
	if isUniqueViolation(err) {
		return nil, errInsertRace
	}
	if err != nil {
		return nil, fmt.Errorf("upsert external identity: create identity: %w", err)
	}
	return u, nil
}

func scanPublicUser(row pgx.Row) (*models.User, error) {
	u := &models.User{}
	if err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt); err != nil {
		return nil, err
	}
	return u, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
