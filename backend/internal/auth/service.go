package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/database"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// Validation limits (contract §4).
const (
	maxEmailLen       = 254
	maxDisplayNameLen = 100
	minPasswordLen    = 12
	maxPasswordLen    = 128
)

// Domain errors mapped to HTTP codes by the handlers.
var (
	ErrSignupDisabled     = errors.New("sign-up is disabled")
	ErrInvalidInviteCode  = errors.New("invalid invite code")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("not authenticated")
	// ErrEmailClaimMissing means the identity provider sent no usable email.
	ErrEmailClaimMissing = errors.New("identity has no valid email")
)

// LockedError is returned while an account is locked after too many failures.
type LockedError struct{ RetryAfter time.Duration }

func (e *LockedError) Error() string { return "account temporarily locked" }

// ValidationError carries per-field validation messages.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

// UserStore is the user persistence the service needs.
type UserStore interface {
	Create(ctx context.Context, email string, displayName *string, passwordHash string) (*models.UserAccount, error)
	GetByEmail(ctx context.Context, email string) (*models.UserAccount, error)
	RecordLoginFailure(ctx context.Context, id uuid.UUID, maxFailures int, lockout time.Duration) (*time.Time, error)
	RecordLoginSuccess(ctx context.Context, id uuid.UUID) error
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash string) error
	// UpsertExternal finds or creates the user for an external identity,
	// keyed on (issuer, subject). It returns models.ErrAccountConflict when
	// the email belongs to another account.
	UpsertExternal(ctx context.Context, id models.ExternalIdentity) (*models.User, error)
}

// SessionStore is the session persistence the service needs.
type SessionStore interface {
	Create(ctx context.Context, s models.Session) error
	GetActive(ctx context.Context, tokenHash []byte, now time.Time, idle time.Duration) (*models.ActiveSession, error)
	Touch(ctx context.Context, tokenHash []byte, now time.Time) error
	Delete(ctx context.Context, tokenHash []byte) error
	DeleteExpired(ctx context.Context, now time.Time, idle time.Duration) (int64, error)
}

// Options are the service settings derived from the configuration.
type Options struct {
	SignupMode      string
	InviteCode      string
	SessionTTL      time.Duration
	IdleTimeout     time.Duration
	MaxFailedLogins int
	LockoutDuration time.Duration
}

// OptionsFromConfig maps the process configuration to service options.
func OptionsFromConfig(c config.Config) Options {
	return Options{
		SignupMode:      c.SignupMode,
		InviteCode:      c.InviteCode,
		SessionTTL:      c.SessionTTL,
		IdleTimeout:     c.SessionIdleTimeout,
		MaxFailedLogins: c.MaxFailedLogins,
		LockoutDuration: c.LockoutDuration,
	}
}

// ExternalIdentity is a verified identity from an OIDC or Identity Platform token.
type ExternalIdentity = models.ExternalIdentity

// ClientMeta is request metadata recorded with a session.
type ClientMeta struct{ UserAgent, IP string }

// Issued is a freshly created session. Token is the only copy of the secret.
type Issued struct {
	User      models.User
	Token     string
	ExpiresAt time.Time
}

// Service implements registration, login, logout and session authentication.
type Service struct {
	users     UserStore
	sessions  SessionStore
	hasher    *Hasher
	opts      Options
	now       func() time.Time
	dummyHash string
}

// NewService builds a Service. now may be nil (defaults to time.Now); it is
// injected so tests can control expiry and lockout. The dummy hash used to
// equalise login timing for unknown users is computed here, at startup.
func NewService(users UserStore, sessions SessionStore, hasher *Hasher, opts Options, now func() time.Time) (*Service, error) {
	if now == nil {
		now = time.Now
	}
	dummy, err := hasher.Hash(context.Background(), "dummy-password-for-timing-equalisation")
	if err != nil {
		return nil, fmt.Errorf("compute dummy hash: %w", err)
	}
	return &Service{users: users, sessions: sessions, hasher: hasher, opts: opts, now: now, dummyHash: dummy}, nil
}

// NormalizeEmail trims, lower-cases and validates an email address.
func NormalizeEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > maxEmailLen {
		return "", false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", false
	}
	return email, true
}

// Register creates an account (subject to the signup mode) and logs it in.
func (s *Service) Register(ctx context.Context, req models.RegisterRequest, meta ClientMeta) (*Issued, error) {
	switch s.opts.SignupMode {
	case config.SignupOpen:
	case config.SignupInvite:
		if !constantTimeEqual(req.InviteCode, s.opts.InviteCode) || s.opts.InviteCode == "" {
			return nil, ErrInvalidInviteCode
		}
	default:
		return nil, ErrSignupDisabled
	}
	user, err := s.CreateUser(ctx, req.Email, req.DisplayName, req.Password)
	if err != nil {
		return nil, err
	}
	return s.issue(ctx, user.ID, user.User, meta)
}

// CreateUser validates and stores a new account regardless of the signup mode
// (used by registration and the create-user command).
func (s *Service) CreateUser(ctx context.Context, rawEmail, displayName, password string) (*models.UserAccount, error) {
	fields := map[string]string{}
	email, ok := NormalizeEmail(rawEmail)
	if !ok {
		fields["email"] = "must be a valid email address of at most 254 characters"
	}
	if n := utf8.RuneCountInString(password); n < minPasswordLen || n > maxPasswordLen {
		fields["password"] = fmt.Sprintf("must be %d-%d characters", minPasswordLen, maxPasswordLen)
	} else if ok && strings.EqualFold(strings.TrimSpace(password), email) {
		fields["password"] = "must not be the same as the email"
	}
	var name *string
	if dn := strings.TrimSpace(displayName); dn != "" {
		if utf8.RuneCountInString(dn) > maxDisplayNameLen {
			fields["display_name"] = fmt.Sprintf("must be at most %d characters", maxDisplayNameLen)
		}
		name = &dn
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}

	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.users.Create(ctx, email, name, hash)
	if err != nil {
		return nil, err // models.ErrEmailTaken passes through
	}
	return user, nil
}

// Login verifies credentials and issues a new session. Unknown users and wrong
// passwords are indistinguishable; locked accounts are rejected before the
// password is checked.
func (s *Service) Login(ctx context.Context, rawEmail, password string, meta ClientMeta) (*Issued, error) {
	email, valid := NormalizeEmail(rawEmail)
	var acct *models.UserAccount
	if valid {
		var err error
		acct, err = s.users.GetByEmail(ctx, email)
		if err != nil && !errors.Is(err, models.ErrNotFound) {
			return nil, fmt.Errorf("load user: %w", err)
		}
	}
	// Users that sign in through an external provider have no password hash:
	// they get the same answer and the same work as unknown users.
	if acct == nil || acct.PasswordHash == "" {
		_, _ = s.hasher.Verify(ctx, password, s.dummyHash) // equalise timing
		return nil, ErrInvalidCredentials
	}

	now := s.now()
	if acct.LockedUntil != nil && acct.LockedUntil.After(now) {
		return nil, &LockedError{RetryAfter: acct.LockedUntil.Sub(now)}
	}

	ok, err := s.hasher.Verify(ctx, password, acct.PasswordHash)
	if err != nil && !errors.Is(err, errInvalidHash) {
		return nil, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		lockedUntil, ferr := s.users.RecordLoginFailure(ctx, acct.ID, s.opts.MaxFailedLogins, s.opts.LockoutDuration)
		if ferr != nil {
			return nil, fmt.Errorf("record login failure: %w", ferr)
		}
		if lockedUntil != nil && lockedUntil.After(now) {
			slog.WarnContext(ctx, "account locked after repeated failed logins", "user_id", acct.ID)
		}
		return nil, ErrInvalidCredentials
	}

	if err := s.users.RecordLoginSuccess(ctx, acct.ID); err != nil {
		return nil, fmt.Errorf("record login success: %w", err)
	}
	if NeedsRehash(acct.PasswordHash) {
		s.rehash(ctx, acct.ID, password)
	}
	return s.issue(ctx, acct.ID, acct.User, meta)
}

// LoginExternal issues a session for an identity already verified by the
// provider adapter (OIDC callback or Identity Platform token). The user is
// found by (issuer, subject) and created on first login; there is no linking
// by email.
func (s *Service) LoginExternal(ctx context.Context, id ExternalIdentity, meta ClientMeta) (*Issued, error) {
	email, ok := NormalizeEmail(id.Email)
	if !ok {
		return nil, ErrEmailClaimMissing
	}
	id.Email = email
	id.DisplayName = strings.TrimSpace(id.DisplayName)
	if utf8.RuneCountInString(id.DisplayName) > maxDisplayNameLen {
		id.DisplayName = string([]rune(id.DisplayName)[:maxDisplayNameLen])
	}
	user, err := s.users.UpsertExternal(ctx, id)
	if err != nil {
		return nil, err // models.ErrAccountConflict passes through
	}
	return s.issue(ctx, user.ID, *user, meta)
}

// rehash upgrades the stored hash to current parameters; failures are logged only.
func (s *Service) rehash(ctx context.Context, id uuid.UUID, password string) {
	hash, err := s.hasher.Hash(ctx, password)
	if err == nil {
		err = s.users.UpdatePasswordHash(ctx, id, hash)
	}
	if err != nil {
		slog.WarnContext(ctx, "password rehash failed", "user_id", id, "error", err)
	}
}

func (s *Service) issue(ctx context.Context, userID uuid.UUID, user models.User, meta ClientMeta) (*Issued, error) {
	token, hash, err := NewToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	expires := now.Add(s.opts.SessionTTL)
	err = s.sessions.Create(ctx, models.Session{
		TokenHash: hash, UserID: userID, CreatedAt: now, LastSeenAt: now,
		ExpiresAt: expires, UserAgent: meta.UserAgent, ClientIP: meta.IP,
	})
	if err != nil {
		return nil, err
	}
	return &Issued{User: user, Token: token, ExpiresAt: expires}, nil
}

// Logout revokes the session behind token. Unknown tokens are ignored.
func (s *Service) Logout(ctx context.Context, token string) error {
	if !validTokenFormat(token) {
		return nil
	}
	return s.sessions.Delete(ctx, HashToken(token))
}

// Authenticate resolves a cookie token to its live session. Absolute and idle
// expiry are enforced; last_seen_at is refreshed at most once per minute.
func (s *Service) Authenticate(ctx context.Context, token string) (*models.ActiveSession, error) {
	if !validTokenFormat(token) {
		return nil, ErrUnauthenticated
	}
	hash := HashToken(token)
	now := s.now()
	sess, err := s.sessions.GetActive(ctx, hash, now, s.opts.IdleTimeout)
	if errors.Is(err, models.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if now.Sub(sess.LastSeenAt) >= database.TouchInterval {
		if err := s.sessions.Touch(ctx, hash, now); err != nil {
			slog.WarnContext(ctx, "session touch failed", "user_id", sess.UserID, "error", err)
		}
	}
	return sess, nil
}

// cleanerJitter is the +/- fraction applied to the cleanup interval so that
// replicas started together do not all hit the database at the same moment.
const cleanerJitter = 0.2

// RunCleaner deletes expired sessions about every interval (+/-20% jitter)
// until ctx is cancelled. It is idempotent, so running it on every replica is
// safe.
func (s *Service) RunCleaner(ctx context.Context, interval time.Duration) {
	for {
		t := time.NewTimer(jittered(interval))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			n, err := s.sessions.DeleteExpired(ctx, s.now(), s.opts.IdleTimeout)
			if err != nil && ctx.Err() == nil {
				slog.Warn("session cleanup failed", "error", err)
			} else if n > 0 {
				slog.Info("expired sessions removed", "count", n)
			}
		}
	}
}

// jittered returns d scaled by a random factor in [1-cleanerJitter, 1+cleanerJitter].
func jittered(d time.Duration) time.Duration {
	f := 1 + cleanerJitter*(2*rand.Float64()-1)
	return time.Duration(float64(d) * f)
}

// constantTimeEqual compares two secrets without leaking their length.
func constantTimeEqual(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
