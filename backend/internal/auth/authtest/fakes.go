// Package authtest provides in-memory fakes of the auth stores and a fake
// clock. It is only imported by tests.
package authtest

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// Clock is a manually advanced clock.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func NewClock() *Clock { return &Clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// Users is an in-memory auth.UserStore mirroring the SQL semantics.
type Users struct {
	mu            sync.Mutex
	Clock         *Clock
	byID          map[uuid.UUID]*models.UserAccount
	identities    map[[2]string]uuid.UUID // (issuer, subject) -> user
	HashUpdates   int
	FailNextWrite error
}

func NewUsers(c *Clock) *Users {
	return &Users{Clock: c, byID: map[uuid.UUID]*models.UserAccount{}, identities: map[[2]string]uuid.UUID{}}
}

func (u *Users) Create(_ context.Context, email string, name *string, hash string) (*models.UserAccount, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.byID {
		if x.Email == email {
			return nil, models.ErrEmailTaken
		}
	}
	acct := &models.UserAccount{
		User:         models.User{ID: uuid.New(), Email: email, DisplayName: name, CreatedAt: u.Clock.Now()},
		PasswordHash: hash,
	}
	u.byID[acct.ID] = acct
	cp := *acct
	return &cp, nil
}

func (u *Users) GetByEmail(_ context.Context, email string) (*models.UserAccount, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.byID {
		if x.Email == email {
			cp := *x
			return &cp, nil
		}
	}
	return nil, models.ErrNotFound
}

// Get returns a copy of the stored account (for assertions).
func (u *Users) Get(id uuid.UUID) *models.UserAccount {
	u.mu.Lock()
	defer u.mu.Unlock()
	if x, ok := u.byID[id]; ok {
		cp := *x
		return &cp
	}
	return nil
}

func (u *Users) RecordLoginFailure(_ context.Context, id uuid.UUID, maxFailures int, lockout time.Duration) (*time.Time, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.byID[id]
	if !ok {
		return nil, models.ErrNotFound
	}
	if x.FailedLoginCount+1 >= maxFailures {
		x.FailedLoginCount = 0
		t := u.Clock.Now().Add(lockout)
		x.LockedUntil = &t
	} else {
		x.FailedLoginCount++
	}
	return x.LockedUntil, nil
}

func (u *Users) RecordLoginSuccess(_ context.Context, id uuid.UUID) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if x, ok := u.byID[id]; ok {
		now := u.Clock.Now()
		x.FailedLoginCount, x.LockedUntil, x.LastLoginAt = 0, nil, &now
	}
	return nil
}

func (u *Users) UpdatePasswordHash(_ context.Context, id uuid.UUID, hash string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if x, ok := u.byID[id]; ok {
		x.PasswordHash = hash
		u.HashUpdates++
	}
	return nil
}

// UpsertExternal mirrors the SQL semantics: lookup by (issuer, subject), no
// linking by email, a taken email on refresh keeps the old one.
func (u *Users) UpsertExternal(_ context.Context, id models.ExternalIdentity) (*models.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	var name *string
	if id.DisplayName != "" {
		name = &id.DisplayName
	}
	now := u.Clock.Now()
	emailTaken := func(except uuid.UUID) bool {
		for _, x := range u.byID {
			if x.Email == id.Email && x.ID != except {
				return true
			}
		}
		return false
	}
	if uid, ok := u.identities[[2]string{id.Issuer, id.Subject}]; ok {
		x := u.byID[uid]
		if !emailTaken(uid) {
			x.Email = id.Email
		}
		if name != nil {
			x.DisplayName = name
		}
		x.LastLoginAt = &now
		pub := x.User
		return &pub, nil
	}
	if emailTaken(uuid.Nil) {
		return nil, models.ErrAccountConflict
	}
	acct := &models.UserAccount{
		User:        models.User{ID: uuid.New(), Email: id.Email, DisplayName: name, CreatedAt: now},
		LastLoginAt: &now,
	}
	u.byID[acct.ID] = acct
	u.identities[[2]string{id.Issuer, id.Subject}] = acct.ID
	pub := acct.User
	return &pub, nil
}

// SetHash overwrites a stored hash (to plant legacy-parameter hashes).
func (u *Users) SetHash(id uuid.UUID, hash string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.byID[id].PasswordHash = hash
}

// Sessions is an in-memory auth.SessionStore.
type Sessions struct {
	mu      sync.Mutex
	Users   *Users
	byHash  map[string]*models.Session
	Touches int
}

func NewSessions(users *Users) *Sessions {
	return &Sessions{Users: users, byHash: map[string]*models.Session{}}
}

func (s *Sessions) Create(_ context.Context, sess models.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byHash[string(sess.TokenHash)] = &sess
	return nil
}

func (s *Sessions) GetActive(_ context.Context, hash []byte, now time.Time, idle time.Duration) (*models.ActiveSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byHash[string(hash)]
	if !ok || !sess.ExpiresAt.After(now) || !sess.LastSeenAt.After(now.Add(-idle)) {
		return nil, models.ErrNotFound
	}
	acct := s.Users.Get(sess.UserID)
	if acct == nil {
		return nil, models.ErrNotFound
	}
	return &models.ActiveSession{Session: *sess, User: acct.User}, nil
}

func (s *Sessions) Touch(_ context.Context, hash []byte, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.byHash[string(hash)]; ok && sess.LastSeenAt.Before(now.Add(-time.Minute)) {
		sess.LastSeenAt = now
		s.Touches++
	}
	return nil
}

func (s *Sessions) Delete(_ context.Context, hash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byHash, string(hash))
	return nil
}

func (s *Sessions) DeleteExpired(_ context.Context, now time.Time, idle time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for k, sess := range s.byHash {
		if !sess.ExpiresAt.After(now) || !sess.LastSeenAt.After(now.Add(-idle)) {
			delete(s.byHash, k)
			n++
		}
	}
	return n, nil
}

// Count returns the number of stored sessions.
func (s *Sessions) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byHash)
}
