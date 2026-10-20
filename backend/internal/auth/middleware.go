package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/httpx"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/observability"
)

type ctxKey struct{}

// UserFromContext returns the authenticated user set by RequireSession.
func UserFromContext(ctx context.Context) (models.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(models.User)
	return u, ok
}

// ContextWithUser is exported for tests of downstream handlers.
func ContextWithUser(ctx context.Context, u models.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// Authenticator resolves a session token.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*models.ActiveSession, error)
}

// RequireSession rejects requests without a live session (401) and otherwise
// stores the user in the request context. Responses are never cacheable.
func RequireSession(a Authenticator, secureCookie bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			token := TokenFromRequest(r, secureCookie)
			if token == "" {
				httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
				return
			}
			sess, err := a.Authenticate(r.Context(), token)
			if errors.Is(err, ErrUnauthenticated) {
				ClearSessionCookie(w, secureCookie)
				httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
				return
			}
			if err != nil {
				slog.ErrorContext(r.Context(), "authenticate session", "error", err)
				httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternalError, "internal server error")
				return
			}
			observability.SetUserID(r.Context(), sess.User.ID.String())
			next.ServeHTTP(w, r.WithContext(ContextWithUser(r.Context(), sess.User)))
		})
	}
}
