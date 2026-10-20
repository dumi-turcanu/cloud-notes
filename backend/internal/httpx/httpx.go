// Package httpx holds the JSON request/response helpers shared by the auth and
// handlers packages: the error envelope, error codes and body decoding.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

// MaxBodyBytes is the request body limit (256 KiB).
const MaxBodyBytes = 256 << 10

// Machine-readable error codes (contract §3.2).
const (
	CodeInvalidRequest     = "invalid_request"
	CodeValidationFailed   = "validation_failed"
	CodeUnauthorized       = "unauthorized"
	CodeForbidden          = "forbidden"
	CodeSignupDisabled     = "signup_disabled"
	CodeInvalidInviteCode  = "invalid_invite_code"
	CodeInvalidCredentials = "invalid_credentials"
	CodeEmailTaken         = "email_taken"
	CodeNotFound           = "not_found"
	CodeMethodNotAllowed   = "method_not_allowed"
	CodeTooManyRequests    = "too_many_requests"
	CodeTooManyAttempts    = "too_many_attempts"
	CodePayloadTooLarge    = "payload_too_large"
	CodeInternalError      = "internal_error"
)

// ErrorBody is the JSON envelope of every non-2xx API response.
type ErrorBody struct {
	Error   string            `json:"error"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// WriteJSON writes v with the given status as application/json.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		slog.Error("encode response", "error", err)
		status = http.StatusInternalServerError
		body = []byte(`{"error":"internal_error","message":"internal server error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

// WriteError writes the standard error envelope.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{Error: code, Message: message})
}

// WriteValidation writes a 400 validation_failed response with per-field messages.
func WriteValidation(w http.ResponseWriter, fields map[string]string) {
	WriteJSON(w, http.StatusBadRequest, ErrorBody{
		Error: CodeValidationFailed, Message: "request validation failed", Fields: fields,
	})
}

// WriteInternal logs err and writes a generic 500 (never leaks details).
func WriteInternal(w http.ResponseWriter, r *http.Request, msg string, err error) {
	slog.ErrorContext(r.Context(), msg, "error", err)
	WriteError(w, http.StatusInternalServerError, CodeInternalError, "internal server error")
}

// DecodeJSON reads a single JSON value from the body into dst, enforcing the
// size limit and rejecting unknown fields and trailing data. On failure it has
// already written the error response and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		// A second value (or garbage) after the first is a malformed request.
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("unexpected data after JSON body")
		}
	}
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		WriteError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large")
		return false
	}
	WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "request body must be a single valid JSON object with known fields")
	return false
}

// NoStore marks the response as uncacheable.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
