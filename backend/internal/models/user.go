package models

import (
	"time"

	"github.com/google/uuid"
)

// User is the public representation of an account. It never carries the
// password hash or lockout state.
type User struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName *string   `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// RegisterRequest is the body of POST /auth/register.
type RegisterRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	InviteCode  string `json:"invite_code"`
}

// LoginRequest is the body of POST /auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// UserAccount is the persistence view of a user: public fields plus the
// credentials and lockout state. It must never be serialised to clients.
type UserAccount struct {
	User
	PasswordHash     string // empty for users that sign in through an external provider
	FailedLoginCount int
	LockedUntil      *time.Time
	LastLoginAt      *time.Time
}

// ExternalIdentity is a verified identity from an external provider: the
// issuer and subject identify the person, the rest comes from the claims.
type ExternalIdentity struct {
	Issuer, Subject, Email, DisplayName string
}

// Session is a server-side login session. TokenHash is SHA-256 of the cookie
// token; the token itself is never stored.
type Session struct {
	TokenHash  []byte
	UserID     uuid.UUID
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	UserAgent  string
	ClientIP   string
}

// ActiveSession is a live session joined with its owner.
type ActiveSession struct {
	Session
	User User
}
