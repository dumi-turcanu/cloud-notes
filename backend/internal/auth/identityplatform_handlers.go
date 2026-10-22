package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/httpx"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// sessionRequest is the body of POST /auth/session.
type sessionRequest struct {
	IDToken string `json:"id_token"`
}

// CreateSession handles POST /auth/session (AUTH_PROVIDER=identity-platform):
// it exchanges a fresh Identity Platform ID token for the session cookie. Like
// every API mutation it is behind CrossOriginProtection, and it is rate
// limited per client IP.
func (h *Handlers) CreateSession(w http.ResponseWriter, r *http.Request) {
	if h.rateLimited(w, r) {
		h.cfg.Recorder.LoginAttempt("rate_limited")
		return
	}
	var req sessionRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	id, err := h.cfg.IdentityPlatform.Verify(r.Context(), req.IDToken)
	if err != nil {
		// No cause is logged or returned: it can quote the token.
		h.cfg.Recorder.LoginAttempt("idp_invalid_token")
		slog.WarnContext(r.Context(), "identity platform login rejected", "reason", "invalid_token")
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeInvalidToken, "invalid token")
		return
	}

	h.revokePresented(r) // same-origin request: the old session cookie is present, rotate it
	issued, err := h.flow.LoginExternal(r.Context(), id, h.meta(r))
	switch {
	case errors.Is(err, models.ErrAccountConflict):
		h.cfg.Recorder.LoginAttempt("idp_conflict")
		slog.WarnContext(r.Context(), "identity platform login rejected", "reason", "account_conflict")
		httpx.WriteError(w, http.StatusConflict, httpx.CodeAccountConflict, "this email address belongs to another account")
		return
	case errors.Is(err, ErrEmailClaimMissing):
		h.cfg.Recorder.LoginAttempt("idp_invalid_token")
		slog.WarnContext(r.Context(), "identity platform login rejected", "reason", "email_claim_missing")
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeInvalidToken, "invalid token")
		return
	case err != nil:
		h.cfg.Recorder.LoginAttempt("idp_failed")
		httpx.WriteInternal(w, r, "identity platform login", err)
		return
	}

	h.cfg.Recorder.LoginAttempt("idp_success")
	slog.InfoContext(r.Context(), "user logged in", "user_id", issued.User.ID, "method", "identity-platform")
	SetSessionCookie(w, h.cfg.SecureCookie, issued.Token, issued.ExpiresAt, h.cfg.Now())
	httpx.WriteJSON(w, http.StatusOK, issued.User)
}
