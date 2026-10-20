package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Note represents the exact shape of the notes table.
type Note struct {
	ID        uuid.UUID       `json:"id"`
	UserID    uuid.UUID       `json:"user_id"`
	Title     string          `json:"title"`
	Content   json.RawMessage `json:"content"`
	IsPublic  bool            `json:"is_public"`
	ShareUUID uuid.UUID       `json:"share_uuid"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	DeletedAt *time.Time      `json:"deleted_at"`
}

// PublicNote is the only shape exposed by the unauthenticated share endpoint:
// no ids and no user data.
type PublicNote struct {
	Title     string          `json:"title"`
	Content   json.RawMessage `json:"content"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// NoteRequest is the body of POST /notes and PUT /notes/{id}.
// is_public and user_id are intentionally excluded — they are set server-side.
type NoteRequest struct {
	Title   string          `json:"title"`
	Content json.RawMessage `json:"content"`
}
