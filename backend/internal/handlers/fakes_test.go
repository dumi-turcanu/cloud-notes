package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// fakeNotes is an in-memory NoteRepository with the same ownership and
// soft-delete semantics as the SQL implementation.
type fakeNotes struct {
	mu    sync.Mutex
	now   func() time.Time
	notes map[uuid.UUID]*models.Note
	err   error // returned by every call when set
}

func newFakeNotes(now func() time.Time) *fakeNotes {
	return &fakeNotes{now: now, notes: map[uuid.UUID]*models.Note{}}
}

func (f *fakeNotes) cp(n *models.Note) *models.Note { c := *n; return &c }

func (f *fakeNotes) Create(_ context.Context, n *models.Note) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	n.ID, n.ShareUUID, n.IsPublic = uuid.New(), uuid.New(), false
	n.CreatedAt, n.UpdatedAt = f.now(), f.now()
	f.notes[n.ID] = f.cp(n)
	return nil
}

func (f *fakeNotes) list(userID uuid.UUID, keep func(*models.Note) bool, less func(a, b *models.Note) bool) []*models.Note {
	var out []*models.Note
	for _, n := range f.notes {
		if n.UserID == userID && keep(n) {
			out = append(out, f.cp(n))
		}
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func (f *fakeNotes) GetAll(_ context.Context, u uuid.UUID) ([]*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.list(u, func(n *models.Note) bool { return n.DeletedAt == nil },
		func(a, b *models.Note) bool { return a.CreatedAt.After(b.CreatedAt) }), nil
}

func (f *fakeNotes) GetSince(_ context.Context, u uuid.UUID, since time.Time) ([]*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list(u, func(n *models.Note) bool { return n.DeletedAt == nil && n.UpdatedAt.After(since) },
		func(a, b *models.Note) bool { return a.UpdatedAt.After(b.UpdatedAt) }), nil
}

func (f *fakeNotes) active(id, u uuid.UUID) *models.Note {
	if n, ok := f.notes[id]; ok && n.UserID == u && n.DeletedAt == nil {
		return n
	}
	return nil
}

func (f *fakeNotes) GetByID(_ context.Context, id, u uuid.UUID) (*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n := f.active(id, u); n != nil {
		return f.cp(n), nil
	}
	return nil, nil
}

func (f *fakeNotes) Update(_ context.Context, in *models.Note) (*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.active(in.ID, in.UserID)
	if n == nil {
		return nil, nil
	}
	n.Title, n.Content, n.UpdatedAt = in.Title, in.Content, f.now()
	return f.cp(n), nil
}

func (f *fakeNotes) SoftDelete(_ context.Context, id, u uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.active(id, u)
	if n == nil {
		return false, nil
	}
	t := f.now()
	n.DeletedAt, n.UpdatedAt = &t, t
	return true, nil
}

func (f *fakeNotes) GetTrashed(_ context.Context, u uuid.UUID) ([]*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list(u, func(n *models.Note) bool { return n.DeletedAt != nil },
		func(a, b *models.Note) bool { return a.DeletedAt.After(*b.DeletedAt) }), nil
}

func (f *fakeNotes) Restore(_ context.Context, id, u uuid.UUID) (*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.notes[id]
	if !ok || n.UserID != u || n.DeletedAt == nil {
		return nil, nil
	}
	n.DeletedAt, n.UpdatedAt = nil, f.now()
	return f.cp(n), nil
}

func (f *fakeNotes) Publish(_ context.Context, id, u uuid.UUID) (*models.Note, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.active(id, u)
	if n == nil {
		return nil, nil
	}
	n.IsPublic, n.UpdatedAt = true, f.now()
	return f.cp(n), nil
}

func (f *fakeNotes) Unpublish(_ context.Context, id, u uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.active(id, u)
	if n == nil {
		return false, nil
	}
	n.IsPublic, n.UpdatedAt = false, f.now()
	return true, nil
}

func (f *fakeNotes) GetPublic(_ context.Context, share uuid.UUID) (*models.PublicNote, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, n := range f.notes {
		if n.ShareUUID == share && n.IsPublic && n.DeletedAt == nil {
			return &models.PublicNote{Title: n.Title, Content: n.Content, UpdatedAt: n.UpdatedAt}, nil
		}
	}
	return nil, nil
}

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

var errBoom = errors.New("boom")

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
