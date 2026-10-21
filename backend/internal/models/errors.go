package models

import "errors"

// Sentinel errors returned by repositories and the auth service.
var (
	ErrNotFound   = errors.New("not found")
	ErrEmailTaken = errors.New("email already registered")
	// ErrAccountConflict means an external identity's email already belongs to
	// a different account. Accounts are never linked by email.
	ErrAccountConflict = errors.New("email belongs to another account")
)
