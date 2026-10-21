package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
)

// oidcHTTPTimeout bounds every call to the identity provider (discovery,
// JWKS, token endpoint), so a slow provider cannot pin request goroutines.
const oidcHTTPTimeout = 10 * time.Second

// maxRedirectLen is the longest post-login path we are willing to round-trip.
const maxRedirectLen = 2048

// OIDCSettings configures the OIDC client (the BFF side of the code flow).
type OIDCSettings struct {
	IssuerURL, DiscoveryURL string
	ClientID, ClientSecret  string
	RedirectURL             string
	Scopes                  []string
	LogoutURL               string // template: {client_id}, {post_logout_redirect_uri}
	PostLogoutRedirectURL   string
	HTTPClient              *http.Client // optional
	Now                     func() time.Time
}

// OIDCSettingsFromConfig maps the process configuration to OIDC settings.
func OIDCSettingsFromConfig(c config.Config) OIDCSettings {
	return OIDCSettings{
		IssuerURL: c.OIDCIssuerURL, DiscoveryURL: c.OIDCDiscoveryURL,
		ClientID: c.OIDCClientID, ClientSecret: c.OIDCClientSecret,
		RedirectURL: c.OIDCRedirectURL, Scopes: c.OIDCScopes,
		LogoutURL: c.OIDCLogoutURL, PostLogoutRedirectURL: c.OIDCPostLogoutRedirectURL,
	}
}

// OIDCClient is a confidential OpenID Connect client using the authorization
// code flow with PKCE. Discovery happens lazily on first use and is cached
// only on success, so an identity provider outage blocks new logins but
// neither existing sessions nor /readyz.
type OIDCClient struct {
	cfg  OIDCSettings
	http *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

// NewOIDCClient builds the client without contacting the provider.
func NewOIDCClient(s OIDCSettings) *OIDCClient {
	hc := s.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: oidcHTTPTimeout}
	}
	return &OIDCClient{cfg: s, http: hc}
}

// discover returns the cached provider or fetches the discovery document. The
// mutex is held across the fetch so concurrent first logins share one request.
func (c *OIDCClient) discover(ctx context.Context) (*oidc.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c.provider, nil
	}
	ctx = oidc.ClientContext(ctx, c.http)
	issuer := c.cfg.IssuerURL
	if c.cfg.DiscoveryURL != "" {
		// Dev only: fetch discovery from an internal host name but keep
		// checking the token's iss against the public issuer.
		ctx = oidc.InsecureIssuerURLContext(ctx, c.cfg.IssuerURL)
		issuer = c.cfg.DiscoveryURL
	}
	p, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	c.provider = p
	return p, nil
}

func (c *OIDCClient) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID: c.cfg.ClientID, ClientSecret: c.cfg.ClientSecret,
		Endpoint: p.Endpoint(), RedirectURL: c.cfg.RedirectURL, Scopes: c.cfg.Scopes,
	}
}

// AuthCodeURL returns the provider URL that starts a login.
func (c *OIDCClient) AuthCodeURL(ctx context.Context, state, nonce, verifier string) (string, error) {
	p, err := c.discover(ctx)
	if err != nil {
		return "", err
	}
	return c.oauth(p).AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Failure reasons of Complete. They are the only description of a failed
// login that is logged: provider error text can carry token material.
const (
	reasonDiscovery   = "discovery_failed"
	reasonExchange    = "code_exchange_failed"
	reasonNoIDToken   = "id_token_missing"
	reasonTokenExpire = "id_token_expired"
	reasonTokenBad    = "id_token_invalid"
	reasonNonce       = "nonce_mismatch"
	reasonClaims      = "claims_invalid"
)

// OIDCError is a failed code exchange or token verification. Reason is one
// of a fixed set of codes; Err is kept for errors.Is but must not be logged.
type OIDCError struct {
	Reason string
	Err    error
}

func (e *OIDCError) Error() string { return "oidc: " + e.Reason }
func (e *OIDCError) Unwrap() error { return e.Err }

// Complete redeems the authorization code, verifies the ID token (signature,
// issuer, audience, expiry and nonce) and returns the identity it asserts. The
// email may be empty when the provider sent no usable, verified address; the
// caller (LoginExternal) rejects that.
func (c *OIDCClient) Complete(ctx context.Context, code, verifier, nonce string) (ExternalIdentity, error) {
	p, err := c.discover(ctx)
	if err != nil {
		return ExternalIdentity{}, &OIDCError{Reason: reasonDiscovery, Err: err}
	}
	tok, err := c.oauth(p).Exchange(context.WithValue(ctx, oauth2.HTTPClient, c.http), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return ExternalIdentity{}, &OIDCError{Reason: reasonExchange, Err: err}
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return ExternalIdentity{}, &OIDCError{Reason: reasonNoIDToken}
	}
	idToken, err := p.Verifier(&oidc.Config{ClientID: c.cfg.ClientID, Now: c.cfg.Now}).Verify(oidc.ClientContext(ctx, c.http), raw)
	if err != nil {
		var expired *oidc.TokenExpiredError
		if errors.As(err, &expired) {
			return ExternalIdentity{}, &OIDCError{Reason: reasonTokenExpire, Err: err}
		}
		return ExternalIdentity{}, &OIDCError{Reason: reasonTokenBad, Err: err}
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonce)) != 1 || nonce == "" {
		return ExternalIdentity{}, &OIDCError{Reason: reasonNonce}
	}
	if idToken.Subject == "" {
		return ExternalIdentity{}, &OIDCError{Reason: reasonClaims}
	}

	var claims struct {
		Email             string `json:"email"`
		EmailVerified     any    `json:"email_verified"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		GivenName         string `json:"given_name"`
		FamilyName        string `json:"family_name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return ExternalIdentity{}, &OIDCError{Reason: reasonClaims, Err: err}
	}
	id := ExternalIdentity{Issuer: idToken.Issuer, Subject: idToken.Subject, DisplayName: claims.Name}
	if id.DisplayName == "" {
		id.DisplayName = strings.TrimSpace(claims.GivenName + " " + claims.FamilyName)
	}
	switch {
	case claims.Email != "":
		// An address the provider says it has not verified would let a
		// stranger squat on someone else's email, so it is dropped.
		if !explicitlyFalse(claims.EmailVerified) {
			id.Email = claims.Email
		}
	default:
		// Entra External ID may omit email; the user name is then often the address.
		if _, ok := NormalizeEmail(claims.PreferredUsername); ok {
			id.Email = claims.PreferredUsername
		}
	}
	return id, nil
}

// explicitlyFalse reports whether an email_verified claim says false. Some
// providers send it as the string "false".
func explicitlyFalse(v any) bool {
	switch x := v.(type) {
	case bool:
		return !x
	case string:
		return strings.EqualFold(x, "false")
	}
	return false
}

// LogoutURL returns where the browser should go to end the provider session.
// It reports false when neither OIDC_LOGOUT_URL nor the discovery document
// defines one (or discovery is currently failing), in which case only the
// local session ends.
func (c *OIDCClient) LogoutURL(ctx context.Context) (string, bool) {
	if c.cfg.LogoutURL != "" {
		return strings.NewReplacer(
			"{client_id}", url.QueryEscape(c.cfg.ClientID),
			"{post_logout_redirect_uri}", url.QueryEscape(c.cfg.PostLogoutRedirectURL),
		).Replace(c.cfg.LogoutURL), true
	}
	p, err := c.discover(ctx)
	if err != nil {
		return "", false
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	if p.Claims(&meta) != nil || meta.EndSession == "" {
		return "", false
	}
	u, err := url.Parse(meta.EndSession)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", false
	}
	q := u.Query()
	q.Set("client_id", c.cfg.ClientID)
	q.Set("post_logout_redirect_uri", c.cfg.PostLogoutRedirectURL)
	u.RawQuery = q.Encode()
	return u.String(), true
}

// SafeRedirect returns target when it is a same-site absolute path and "/"
// otherwise. It mirrors safeRedirect in the frontend (lib/errors.ts), with
// the extra limits that matter for a value that travels through a cookie.
func SafeRedirect(target string) string {
	if target == "" || len(target) > maxRedirectLen || !utf8.ValidString(target) ||
		!strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.HasPrefix(target, `/\`) {
		return "/"
	}
	for _, r := range target {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return "/"
		}
	}
	return target
}

// oidcState is what the callback must find again: it ties the browser that
// started the login to the one that finishes it.
type oidcState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	From     string `json:"f"`
}

const oidcStateMaxAge = 10 * time.Minute

// OIDCStateCookieName returns the login-state cookie name (the __Host- prefix
// needs Secure, like the session cookie).
func OIDCStateCookieName(secure bool) string {
	if secure {
		return "__Host-cn_oidc"
	}
	return "cn_oidc"
}

// setOIDCStateCookie stores the login state for the callback. SameSite=Lax
// (not Strict) because the provider's redirect back is a cross-site
// navigation, which must carry the cookie.
func setOIDCStateCookie(w http.ResponseWriter, secure bool, st oidcState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     OIDCStateCookieName(secure),
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/",
		MaxAge:   int(oidcStateMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// clearOIDCStateCookie drops the login state; it is single use.
func clearOIDCStateCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     OIDCStateCookieName(secure),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// oidcStateFromRequest decodes the login-state cookie; ok is false when it is
// missing or malformed.
func oidcStateFromRequest(r *http.Request, secure bool) (oidcState, bool) {
	c, err := r.Cookie(OIDCStateCookieName(secure))
	if err != nil {
		return oidcState{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return oidcState{}, false
	}
	var st oidcState
	if json.Unmarshal(raw, &st) != nil || st.State == "" || st.Nonce == "" || st.Verifier == "" {
		return oidcState{}, false
	}
	return st, true
}

// randomToken returns 32 random bytes, base64url-encoded.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
