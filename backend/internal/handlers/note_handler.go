package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/httpx"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

const maxTitleRunes = 255

// NoteRepository is defined here (on the consumer side) per Go interface idiom.
// The real implementation lives in internal/database; tests use a fake.
type NoteRepository interface {
	Create(ctx context.Context, note *models.Note) error
	GetAll(ctx context.Context, userID uuid.UUID) ([]*models.Note, error)
	GetSince(ctx context.Context, userID uuid.UUID, since time.Time) ([]*models.Note, error)
	// GetByID returns nil, nil when no active note matches id + userID (not found or wrong owner).
	GetByID(ctx context.Context, id, userID uuid.UUID) (*models.Note, error)
	// Update returns nil, nil when no active note matches id + userID.
	Update(ctx context.Context, note *models.Note) (*models.Note, error)
	// SoftDelete returns false when no active note matches id + userID.
	SoftDelete(ctx context.Context, id, userID uuid.UUID) (bool, error)
	GetTrashed(ctx context.Context, userID uuid.UUID) ([]*models.Note, error)
	// Restore returns nil, nil when not found, wrong owner, or not in trash.
	Restore(ctx context.Context, id, userID uuid.UUID) (*models.Note, error)
	// Publish sets is_public = true. Returns nil, nil when not found or wrong owner.
	Publish(ctx context.Context, id, userID uuid.UUID) (*models.Note, error)
	// Unpublish sets is_public = false. Returns false when not found or wrong owner.
	Unpublish(ctx context.Context, id, userID uuid.UUID) (bool, error)
	// GetPublic returns the public projection of a published, non-deleted note, or nil, nil.
	GetPublic(ctx context.Context, shareUUID uuid.UUID) (*models.PublicNote, error)
}

// publishResponse wraps a note with its computed share URL.
type publishResponse struct {
	*models.Note
	ShareURL string `json:"share_url"`
}

// NoteHandler serves the notes endpoints. Every handler runs behind
// auth.RequireSession except GetPublicNote.
type NoteHandler struct {
	repo         NoteRepository
	publicAppURL string
}

func NewNoteHandler(repo NoteRepository, publicAppURL string) *NoteHandler {
	return &NoteHandler{repo: repo, publicAppURL: strings.TrimRight(publicAppURL, "/")}
}

// currentUser returns the authenticated user's id or writes a 401.
func currentUser(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
		return uuid.Nil, false
	}
	return u.ID, true
}

func noteID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidRequest, "invalid note id")
		return uuid.Nil, false
	}
	return id, true
}

func notFound(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "note not found")
}

// decodeNote parses and validates a create/update body.
func decodeNote(w http.ResponseWriter, r *http.Request) (title string, content json.RawMessage, ok bool) {
	var req models.NoteRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return "", nil, false
	}
	fields := map[string]string{}
	title = strings.TrimSpace(req.Title)
	if n := utf8.RuneCountInString(title); n < 1 || n > maxTitleRunes {
		fields["title"] = "is required and must be 1-255 characters"
	}
	content = bytes.TrimSpace(req.Content)
	switch {
	case len(content) == 0 || bytes.Equal(content, []byte("null")):
		content = json.RawMessage(`{}`)
	case content[0] != '{':
		fields["content"] = "must be a JSON object"
	}
	if len(fields) > 0 {
		httpx.WriteValidation(w, fields)
		return "", nil, false
	}
	return title, content, true
}

func (h *NoteHandler) CreateNote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	title, content, ok := decodeNote(w, r)
	if !ok {
		return
	}
	note := &models.Note{UserID: userID, Title: title, Content: content}
	if err := h.repo.Create(r.Context(), note); err != nil {
		httpx.WriteInternal(w, r, "create note", err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, note)
}

func (h *NoteHandler) GetNotes(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	var (
		notes []*models.Note
		err   error
	)
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		since, perr := time.Parse(time.RFC3339, sinceStr)
		if perr != nil {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidRequest, "invalid 'since' parameter: expected RFC 3339")
			return
		}
		notes, err = h.repo.GetSince(r.Context(), userID, since)
	} else {
		notes, err = h.repo.GetAll(r.Context(), userID)
	}
	if err != nil {
		httpx.WriteInternal(w, r, "list notes", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, nonNil(notes))
}

func (h *NoteHandler) GetNoteByID(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	note, err := h.repo.GetByID(r.Context(), id, userID)
	if err != nil {
		httpx.WriteInternal(w, r, "get note", err)
		return
	}
	if note == nil {
		notFound(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, note)
}

func (h *NoteHandler) UpdateNote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	title, content, ok := decodeNote(w, r)
	if !ok {
		return
	}
	updated, err := h.repo.Update(r.Context(), &models.Note{ID: id, UserID: userID, Title: title, Content: content})
	if err != nil {
		httpx.WriteInternal(w, r, "update note", err)
		return
	}
	if updated == nil {
		notFound(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, updated)
}

func (h *NoteHandler) GetTrash(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	notes, err := h.repo.GetTrashed(r.Context(), userID)
	if err != nil {
		httpx.WriteInternal(w, r, "list trash", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, nonNil(notes))
}

func (h *NoteHandler) RestoreNote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	note, err := h.repo.Restore(r.Context(), id, userID)
	if err != nil {
		httpx.WriteInternal(w, r, "restore note", err)
		return
	}
	if note == nil {
		notFound(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, note)
}

func (h *NoteHandler) DeleteNote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	found, err := h.repo.SoftDelete(r.Context(), id, userID)
	if err != nil {
		httpx.WriteInternal(w, r, "delete note", err)
		return
	}
	if !found {
		notFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PublishNote is only registered when FEATURE_PUBLIC_SHARING=true.
func (h *NoteHandler) PublishNote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	note, err := h.repo.Publish(r.Context(), id, userID)
	if err != nil {
		httpx.WriteInternal(w, r, "publish note", err)
		return
	}
	if note == nil {
		notFound(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, publishResponse{
		Note:     note,
		ShareURL: h.publicAppURL + "/shared/" + note.ShareUUID.String(),
	})
}

// UnpublishNote is only registered when FEATURE_PUBLIC_SHARING=true.
func (h *NoteHandler) UnpublishNote(w http.ResponseWriter, r *http.Request) {
	userID, ok := currentUser(w, r)
	if !ok {
		return
	}
	id, ok := noteID(w, r)
	if !ok {
		return
	}
	found, err := h.repo.Unpublish(r.Context(), id, userID)
	if err != nil {
		httpx.WriteInternal(w, r, "unpublish note", err)
		return
	}
	if !found {
		notFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetPublicNote is unauthenticated and only registered when sharing is enabled.
func (h *NoteHandler) GetPublicNote(w http.ResponseWriter, r *http.Request) {
	shareUUID, err := uuid.Parse(chi.URLParam(r, "share_uuid"))
	if err != nil {
		notFound(w)
		return
	}
	note, err := h.repo.GetPublic(r.Context(), shareUUID)
	if err != nil {
		httpx.WriteInternal(w, r, "get public note", err)
		return
	}
	if note == nil {
		notFound(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, note)
}

func nonNil(n []*models.Note) []*models.Note {
	if n == nil {
		return []*models.Note{}
	}
	return n
}
