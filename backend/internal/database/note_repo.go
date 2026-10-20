package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// NoteRepository handles all direct database interactions for notes.
// Every query is scoped by user_id in SQL so ownership cannot be bypassed.
type NoteRepository struct {
	pool *pgxpool.Pool
}

// NewNoteRepository creates a new repository instance.
func NewNoteRepository(pool *pgxpool.Pool) *NoteRepository {
	return &NoteRepository{pool: pool}
}

const noteColumns = `id, user_id, title, content, is_public, share_uuid, created_at, updated_at, deleted_at`

func scanNote(row pgx.Row) (*models.Note, error) {
	n := &models.Note{}
	err := row.Scan(&n.ID, &n.UserID, &n.Title, &n.Content, &n.IsPublic, &n.ShareUUID,
		&n.CreatedAt, &n.UpdatedAt, &n.DeletedAt)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// queryOne runs a single-row query and maps "no rows" to nil, nil.
func (r *NoteRepository) queryOne(ctx context.Context, op, query string, args ...any) (*models.Note, error) {
	n, err := scanNote(r.pool.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return n, nil
}

// Create inserts a new private note and returns it with DB-generated fields.
func (r *NoteRepository) Create(ctx context.Context, note *models.Note) error {
	created, err := r.queryOne(ctx, "create note", `
		INSERT INTO notes (user_id, title, content) VALUES ($1, $2, $3)
		RETURNING `+noteColumns, note.UserID, note.Title, note.Content)
	if err != nil {
		return err
	}
	*note = *created
	return nil
}

// GetAll returns active (non-deleted) notes owned by userID, newest first.
func (r *NoteRepository) GetAll(ctx context.Context, userID uuid.UUID) ([]*models.Note, error) {
	return r.scanNotes(ctx, `SELECT `+noteColumns+` FROM notes
		WHERE user_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC`, userID)
}

// GetSince returns active notes owned by userID updated after since, newest first.
func (r *NoteRepository) GetSince(ctx context.Context, userID uuid.UUID, since time.Time) ([]*models.Note, error) {
	return r.scanNotes(ctx, `SELECT `+noteColumns+` FROM notes
		WHERE user_id = $1 AND deleted_at IS NULL AND updated_at > $2 ORDER BY updated_at DESC`, userID, since)
}

// GetByID returns the active note matching id and userID, or nil, nil
// (not found or belongs to someone else — callers map both to 404).
func (r *NoteRepository) GetByID(ctx context.Context, id, userID uuid.UUID) (*models.Note, error) {
	return r.queryOne(ctx, "get note by id", `SELECT `+noteColumns+` FROM notes
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, id, userID)
}

// Update replaces title and content on an active note owned by the caller.
// Returns nil, nil when not found or wrong owner.
func (r *NoteRepository) Update(ctx context.Context, note *models.Note) (*models.Note, error) {
	return r.queryOne(ctx, "update note", `
		UPDATE notes SET title = $1, content = $2, updated_at = now()
		WHERE id = $3 AND user_id = $4 AND deleted_at IS NULL
		RETURNING `+noteColumns, note.Title, note.Content, note.ID, note.UserID)
}

// SoftDelete sets deleted_at on an active note owned by the caller.
func (r *NoteRepository) SoftDelete(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE notes SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return false, fmt.Errorf("soft delete note: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GetTrashed returns soft-deleted notes owned by userID, most recently deleted first.
func (r *NoteRepository) GetTrashed(ctx context.Context, userID uuid.UUID) ([]*models.Note, error) {
	return r.scanNotes(ctx, `SELECT `+noteColumns+` FROM notes
		WHERE user_id = $1 AND deleted_at IS NOT NULL ORDER BY deleted_at DESC`, userID)
}

// Restore clears deleted_at on a trashed note owned by the caller.
// Returns nil, nil when not found, wrong owner, or the note is not in trash.
func (r *NoteRepository) Restore(ctx context.Context, id, userID uuid.UUID) (*models.Note, error) {
	return r.queryOne(ctx, "restore note", `
		UPDATE notes SET deleted_at = NULL, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NOT NULL
		RETURNING `+noteColumns, id, userID)
}

// Publish sets is_public = true on an active note owned by the caller.
func (r *NoteRepository) Publish(ctx context.Context, id, userID uuid.UUID) (*models.Note, error) {
	return r.queryOne(ctx, "publish note", `
		UPDATE notes SET is_public = true, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		RETURNING `+noteColumns, id, userID)
}

// Unpublish sets is_public = false on an active note owned by the caller.
func (r *NoteRepository) Unpublish(ctx context.Context, id, userID uuid.UUID) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE notes SET is_public = false, updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return false, fmt.Errorf("unpublish note: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// GetPublic returns the public projection of a published, non-deleted note by
// share_uuid, or nil, nil.
func (r *NoteRepository) GetPublic(ctx context.Context, shareUUID uuid.UUID) (*models.PublicNote, error) {
	p := &models.PublicNote{}
	err := r.pool.QueryRow(ctx, `SELECT title, content, updated_at FROM notes
		WHERE share_uuid = $1 AND is_public = true AND deleted_at IS NULL`, shareUUID).
		Scan(&p.Title, &p.Content, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get public note: %w", err)
	}
	return p, nil
}

func (r *NoteRepository) scanNotes(ctx context.Context, query string, args ...any) ([]*models.Note, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("notes query: %w", err)
	}
	defer rows.Close()

	notes := []*models.Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, fmt.Errorf("scan note row: %w", err)
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate note rows: %w", err)
	}
	return notes, nil
}
