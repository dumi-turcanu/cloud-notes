package auth

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/httpx"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

// Error codes the SPA's /login page understands (?error=...). They are fixed
// strings: provider error text never reaches a URL.
const (
	oidcErrCancelled     = "oidc_cancelled"
	oidcErrFailed        = "oidc_failed"
	oidcErrConflict      = "account_conflict"
	oidcErrEmailMissing  = "email_claim_missing"
	loginPath            = "/login"
	loginErrorQueryParam = "error"
)

// OIDCLogin handles GET /auth/oidc/login?from=<path>: it stores state, nonce
// and the PKCE verifier in a short-lived cookie and redirects to the provider.
func (h *Handlers) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	state, err1 := randomToken()
	nonce, err2 := randomToken()
	if err := errors.Join(err1, err2); err != nil {
		httpx.WriteInternal(w, r, "oidc login", err)
		return
	}
	st := oidcState{State: state, Nonce: nonce, Verifier: oauth2.GenerateVerifier(), From: SafeRedirect(r.URL.Query().Get("from"))}

	target, err := h.cfg.OIDC.AuthCodeURL(r.Context(), st.State, st.Nonce, st.Verifier)
	if err != nil {
		h.logOIDCFailure(r, "login_start", reasonDiscovery, discoveryAttrs(err)...)
		h.cfg.Recorder.LoginAttempt("oidc_failed")
		h.loginRedirect(w, r, oidcErrFailed)
		return
	}
	if err := setOIDCStateCookie(w, h.cfg.SecureCookie, st); err != nil {
		httpx.WriteInternal(w, r, "oidc login", err)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// OIDCCallback handles GET /auth/oidc/callback. Whatever happens, the browser
// ends up on a relative path: the requested page on success, /login?error=...
// otherwise. The state cookie is single use.
func (h *Handlers) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.rateLimited(w, r) {
		h.cfg.Recorder.LoginAttempt("rate_limited")
		return
	}
	st, haveState := oidcStateFromRequest(r, h.cfg.SecureCookie)
	clearOIDCStateCookie(w, h.cfg.SecureCookie)
	q := r.URL.Query()

	if q.Get("error") != "" { // the user denied access or the provider refused
		h.cfg.Recorder.LoginAttempt("oidc_cancelled")
		h.loginRedirect(w, r, oidcErrCancelled)
		return
	}
	if !haveState {
		h.callbackFailed(w, r, "state_cookie_missing")
		return
	}
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1 {
		h.callbackFailed(w, r, "state_mismatch")
		return
	}
	code := q.Get("code")
	if code == "" {
		h.callbackFailed(w, r, "code_missing")
		return
	}

	id, err := h.cfg.OIDC.Complete(r.Context(), code, st.Verifier, st.Nonce)
	if err != nil {
		reason := "unknown"
		var oerr *OIDCError
		if errors.As(err, &oerr) {
			reason = oerr.Reason
		}
		h.callbackFailed(w, r, reason)
		return
	}

	h.revokePresented(r) // best effort: a Strict cookie is not sent on cross-site returns
	issued, err := h.flow.LoginExternal(r.Context(), id, h.meta(r))
	switch {
	case errors.Is(err, ErrEmailClaimMissing):
		h.cfg.Recorder.LoginAttempt("oidc_email_missing")
		slog.WarnContext(r.Context(), "oidc login rejected", "reason", "email_claim_missing")
		h.loginRedirect(w, r, oidcErrEmailMissing)
		return
	case errors.Is(err, models.ErrAccountConflict):
		h.cfg.Recorder.LoginAttempt("oidc_conflict")
		slog.WarnContext(r.Context(), "oidc login rejected", "reason", "account_conflict")
		h.loginRedirect(w, r, oidcErrConflict)
		return
	case err != nil:
		// Not provider text: a store failure, safe to log in full.
		h.cfg.Recorder.LoginAttempt("oidc_failed")
		slog.ErrorContext(r.Context(), "oidc login", "error", err)
		h.loginRedirect(w, r, oidcErrFailed)
		return
	}

	h.cfg.Recorder.LoginAttempt("oidc_success")
	slog.InfoContext(r.Context(), "user logged in", "user_id", issued.User.ID, "method", "oidc")
	SetSessionCookie(w, h.cfg.SecureCookie, issued.Token, issued.ExpiresAt, h.cfg.Now())
	http.Redirect(w, r, SafeRedirect(st.From), http.StatusFound)
}

// callbackFailed records a failed callback and sends the browser to /login.
func (h *Handlers) callbackFailed(w http.ResponseWriter, r *http.Request, reason string) {
	h.cfg.Recorder.LoginAttempt("oidc_failed")
	h.logOIDCFailure(r, "callback", reason)
	h.loginRedirect(w, r, oidcErrFailed)
}

// logOIDCFailure logs only the stage and a fixed reason code. Authorization
// codes, tokens and provider error text never reach the log.
func (h *Handlers) logOIDCFailure(r *http.Request, stage, reason string, attrs ...any) {
	slog.WarnContext(r.Context(), "oidc login failed", append([]any{"stage", stage, "reason", reason}, attrs...)...)
}

// discoveryAttrs adds the one detail of a discovery failure that is safe and
// by far the most useful: when the provider's issuer differs from
// OIDC_ISSUER_URL, both are public configuration values, not secrets.
func discoveryAttrs(err error) []any {
	var mismatch *oidc.IssuerMismatchError
	if errors.As(err, &mismatch) {
		return []any{"configured_issuer", mismatch.Provided, "discovered_issuer", mismatch.Discovered}
	}
	return nil
}

// loginRedirect sends the browser to the SPA's login page with an error code.
func (h *Handlers) loginRedirect(w http.ResponseWriter, r *http.Request, code string) {
	q := url.Values{loginErrorQueryParam: {code}}
	http.Redirect(w, r, loginPath+"?"+q.Encode(), http.StatusFound)
}
