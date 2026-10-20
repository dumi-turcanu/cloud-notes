package models

import "errors"

// Sentinel errors returned by repositories and the auth service.
var (
	ErrNotFound   = errors.New("not found")
	ErrEmailTaken = errors.New("email already registered")
)
