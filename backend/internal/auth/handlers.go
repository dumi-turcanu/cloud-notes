package auth

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/httpx"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// Flow is the part of Service used by the HTTP handlers.
type Flow interface {
	Authenticator
	Register(ctx context.Context, req models.RegisterRequest, meta ClientMeta) (*Issued, error)
	Login(ctx context.Context, email, password string, meta ClientMeta) (*Issued, error)
	LoginExternal(ctx context.Context, id ExternalIdentity, meta ClientMeta) (*Issued, error)
	Logout(ctx context.Context, token string) error
}

// Recorder receives auth outcomes (the observability package backs it with Prometheus counters).
type Recorder interface {
	LoginAttempt(result string)
	Registration(result string)
}

type noopRecorder struct{}

func (noopRecorder) LoginAttempt(string) {}
func (noopRecorder) Registration(string) {}

// HandlerConfig configures the auth HTTP handlers.
type HandlerConfig struct {
	SecureCookie bool
	ProxyHops    int
	Limiter      *RateLimiter // optional
	Recorder     Recorder     // optional
	Now          func() time.Time
	// OIDC is required for the OIDC handlers (AUTH_PROVIDER=oidc); when set,
	// logout also returns the provider's logout URL.
	OIDC *OIDCClient
}

// Handlers serves /auth/register, /auth/login, /auth/logout and /auth/me, and
// in oidc mode /auth/oidc/login and /auth/oidc/callback.
type Handlers struct {
	flow Flow
	cfg  HandlerConfig
}

// NewHandlers builds the auth handlers.
func NewHandlers(flow Flow, cfg HandlerConfig) *Handlers {
	if cfg.Recorder == nil {
		cfg.Recorder = noopRecorder{}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Handlers{flow: flow, cfg: cfg}
}

func (h *Handlers) meta(r *http.Request) ClientMeta {
	return ClientMeta{UserAgent: r.UserAgent(), IP: ClientIP(r, h.cfg.ProxyHops)}
}

// rateLimited applies the per-IP limit; it writes the 429 and reports true when exceeded.
func (h *Handlers) rateLimited(w http.ResponseWriter, r *http.Request) bool {
	if h.cfg.Limiter == nil || h.cfg.Limiter.Allow(ClientIP(r, h.cfg.ProxyHops)) {
		return false
	}
	w.Header().Set("Retry-After", "60")
	httpx.WriteError(w, http.StatusTooManyRequests, httpx.CodeTooManyRequests, "too many requests, retry later")
	return true
}

// Register handles POST /auth/register.
func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	if h.rateLimited(w, r) {
		h.cfg.Recorder.Registration("rate_limited")
		return
	}
	var req models.RegisterRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	h.revokePresented(r)
	issued, err := h.flow.Register(r.Context(), req, h.meta(r))
	if err != nil {
		var verr *ValidationError
		switch {
		case errors.Is(err, ErrSignupDisabled):
			h.cfg.Recorder.Registration("signup_disabled")
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeSignupDisabled, "sign-up is disabled")
		case errors.Is(err, ErrInvalidInviteCode):
			h.cfg.Recorder.Registration("invalid_invite_code")
			httpx.WriteError(w, http.StatusForbidden, httpx.CodeInvalidInviteCode, "invalid invite code")
		case errors.As(err, &verr):
			h.cfg.Recorder.Registration("validation_failed")
			httpx.WriteValidation(w, verr.Fields)
		case errors.Is(err, models.ErrEmailTaken):
			h.cfg.Recorder.Registration("email_taken")
			httpx.WriteError(w, http.StatusConflict, httpx.CodeEmailTaken, "email already registered")
		default:
			h.cfg.Recorder.Registration("error")
			httpx.WriteInternal(w, r, "register", err)
		}
		return
	}
	h.cfg.Recorder.Registration("success")
	slog.InfoContext(r.Context(), "user registered", "user_id", issued.User.ID)
	SetSessionCookie(w, h.cfg.SecureCookie, issued.Token, issued.ExpiresAt, h.cfg.Now())
	httpx.WriteJSON(w, http.StatusCreated, issued.User)
}

// Login handles POST /auth/login. A session presented on the request is
// revoked first, so a successful login always rotates the session.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if h.rateLimited(w, r) {
		h.cfg.Recorder.LoginAttempt("rate_limited")
		return
	}
	var req models.LoginRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	h.revokePresented(r)
	issued, err := h.flow.Login(r.Context(), req.Email, req.Password, h.meta(r))
	if err != nil {
		var locked *LockedError
		switch {
		case errors.As(err, &locked):
			h.cfg.Recorder.LoginAttempt("locked")
			secs := int(math.Ceil(locked.RetryAfter.Seconds()))
			w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
			httpx.WriteError(w, http.StatusTooManyRequests, httpx.CodeTooManyAttempts, "too many failed attempts, try again later")
		case errors.Is(err, ErrInvalidCredentials):
			h.cfg.Recorder.LoginAttempt("invalid_credentials")
			httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeInvalidCredentials, "invalid email or password")
		default:
			httpx.WriteInternal(w, r, "login", err)
		}
		return
	}
	h.cfg.Recorder.LoginAttempt("success")
	slog.InfoContext(r.Context(), "user logged in", "user_id", issued.User.ID)
	SetSessionCookie(w, h.cfg.SecureCookie, issued.Token, issued.ExpiresAt, h.cfg.Now())
	httpx.WriteJSON(w, http.StatusOK, issued.User)
}

// Logout handles POST /auth/logout. The local session is always revoked. It
// answers 204, except in oidc mode when the provider has a logout URL: then
// 200 {"logout_url": ...} so the SPA can end the provider session too.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if token := TokenFromRequest(r, h.cfg.SecureCookie); token != "" {
		if err := h.flow.Logout(r.Context(), token); err != nil {
			slog.ErrorContext(r.Context(), "logout", "error", err)
		}
	}
	ClearSessionCookie(w, h.cfg.SecureCookie)
	if h.cfg.OIDC != nil {
		if u, ok := h.cfg.OIDC.LogoutURL(r.Context()); ok {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"logout_url": u})
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /auth/me (behind RequireSession).
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, u)
}

// revokePresented deletes the session cookie's session, if any (rotation).
func (h *Handlers) revokePresented(r *http.Request) {
	if token := TokenFromRequest(r, h.cfg.SecureCookie); token != "" {
		if err := h.flow.Logout(r.Context(), token); err != nil {
			slog.WarnContext(r.Context(), "revoke presented session", "error", err)
		}
	}
}
