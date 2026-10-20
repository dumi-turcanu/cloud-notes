package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const requestIDHeader = "X-Request-ID"

const maxRequestIDLen = 64

// requestInfo is the mutable per-request record shared between the outermost
// middleware and handlers deeper in the stack (which cannot otherwise
// communicate values "outwards" through the request context).
type requestInfo struct {
	id        string
	userID    string
	unmatched bool
}

type infoKey struct{}

func infoFrom(ctx context.Context) *requestInfo {
	ri, _ := ctx.Value(infoKey{}).(*requestInfo)
	return ri
}

// RequestID returns the request ID attached by the RequestID middleware.
func RequestID(ctx context.Context) string {
	if ri := infoFrom(ctx); ri != nil {
		return ri.id
	}
	return ""
}

// SetUserID records the authenticated user for the request log.
func SetUserID(ctx context.Context, id string) {
	if ri := infoFrom(ctx); ri != nil {
		ri.userID = id
	}
}

// MarkUnmatched flags the request as not matching any route, so metrics use
// route="unmatched". Call it from NotFound / MethodNotAllowed handlers.
func MarkUnmatched(ctx context.Context) {
	if ri := infoFrom(ctx); ri != nil {
		ri.unmatched = true
	}
}

// WithRequestID accepts a valid incoming X-Request-ID (at most 64 characters of
// [A-Za-z0-9._-]) or generates one, echoes it in the response and stores the
// per-request record in the context. It must be the outermost middleware.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), infoKey{}, &requestInfo{id: id})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validRequestID(s string) bool {
	if s == "" || len(s) > maxRequestIDLen {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error on supported platforms
	return hex.EncodeToString(b)
}
