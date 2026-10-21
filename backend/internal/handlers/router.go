package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/httpx"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/observability"
)

const readyTimeout = 2 * time.Second

// Pinger checks database connectivity.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the collaborators the router wires together.
type Deps struct {
	Config  config.Config
	Version string
	DB      Pinger
	Notes   NoteRepository
	Auth    auth.Flow
	// OIDC is optional; when nil and AUTH_PROVIDER=oidc it is built from the
	// configuration. Tests inject one pointed at a fake provider.
	OIDC *auth.OIDCClient
	// Recorder is optional; it receives auth outcomes for metrics.
	Recorder auth.Recorder
	// Metrics is optional; when set, requests are counted per route pattern.
	Metrics *observability.Metrics
	// Ready is optional; /readyz answers 503 while it returns false (shutdown).
	Ready func() bool
	// Logger is optional (defaults to slog.Default()).
	Logger *slog.Logger
	// Now is optional (defaults to time.Now); injected for tests.
	Now func() time.Time
}

// NewRouter builds the full HTTP handler: infra endpoints at the root and
// the application under Config.APIBasePath.
func NewRouter(d Deps) (http.Handler, error) {
	api, err := newAPIHandler(d)
	if err != nil {
		return nil, err
	}

	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	hops := d.Config.TrustedProxyHops

	r := chi.NewRouter()
	r.Use(observability.WithRequestID)
	if d.Metrics != nil {
		r.Use(d.Metrics.Middleware)
	}
	r.Use(observability.RequestLogger(logger, func(req *http.Request) string { return auth.ClientIP(req, hops) }))
	r.Use(observability.Recoverer(logger))
	r.NotFound(routeNotFound)
	r.MethodNotAllowed(routeMethodNotAllowed)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if d.Ready != nil && !d.Ready() {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "shutting_down"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), readyTimeout)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	r.Mount(d.Config.APIBasePath, api)
	return r, nil
}

// newAPIHandler builds the /api subtree wrapped in CSRF protection.
func newAPIHandler(d Deps) (http.Handler, error) {
	cfg := d.Config

	cop := http.NewCrossOriginProtection()
	for _, o := range cfg.CSRFTrustedOrigins {
		if err := cop.AddTrustedOrigin(o); err != nil {
			return nil, fmt.Errorf("CSRF_TRUSTED_ORIGINS %q: %w", o, err)
		}
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "cross-origin request rejected")
	}))

	now := d.Now
	if now == nil {
		now = time.Now
	}
	limiter := auth.NewRateLimiter(cfg.RateLimitPerMinute, now)
	oidcClient := d.OIDC
	if oidcClient == nil && cfg.AuthProvider == config.AuthOIDC {
		oidcClient = auth.NewOIDCClient(auth.OIDCSettingsFromConfig(cfg))
	}
	authH := auth.NewHandlers(d.Auth, auth.HandlerConfig{
		SecureCookie: cfg.SessionCookieSecure,
		ProxyHops:    cfg.TrustedProxyHops,
		Limiter:      limiter,
		Recorder:     d.Recorder,
		Now:          now,
		OIDC:         oidcClient,
	})
	require := auth.RequireSession(d.Auth, cfg.SessionCookieSecure)
	notes := NewNoteHandler(d.Notes, cfg.PublicAppURL)

	api := chi.NewRouter()
	api.NotFound(routeNotFound)
	api.MethodNotAllowed(routeMethodNotAllowed)

	api.Get("/config", publicConfig(cfg, d.Version))

	api.Route("/auth", func(r chi.Router) {
		r.Use(httpx.NoStore)
		// With an external provider the identity service owns credentials, so
		// the password endpoints are not mounted and fall through to 404.
		if !externalAuth(cfg) {
			r.Post("/register", authH.Register)
			r.Post("/login", authH.Login)
		}
		if oidcClient != nil {
			r.Get("/oidc/login", authH.OIDCLogin)
			r.Get("/oidc/callback", authH.OIDCCallback)
		}
		r.Post("/logout", authH.Logout)
		r.With(require).Get("/me", authH.Me)
	})

	api.Route("/notes", func(r chi.Router) {
		r.Use(require)
		r.Post("/", notes.CreateNote)
		r.Get("/", notes.GetNotes)
		r.Get("/trash", notes.GetTrash)
		r.Get("/{id}", notes.GetNoteByID)
		r.Put("/{id}", notes.UpdateNote)
		r.Delete("/{id}", notes.DeleteNote)
		r.Post("/{id}/restore", notes.RestoreNote)
		if cfg.PublicSharing {
			r.Post("/{id}/publish", notes.PublishNote)
			r.Delete("/{id}/publish", notes.UnpublishNote)
		}
	})

	if cfg.PublicSharing {
		api.Get("/public/notes/{share_uuid}", notes.GetPublicNote)
	}

	return cop.Handler(api), nil
}

func routeNotFound(w http.ResponseWriter, r *http.Request) {
	observability.MarkUnmatched(r.Context())
	httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "resource not found")
}

func routeMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	observability.MarkUnmatched(r.Context())
	httpx.WriteError(w, http.StatusMethodNotAllowed, httpx.CodeMethodNotAllowed, "method not allowed")
}

// externalAuth reports whether credentials are handled by an external identity
// provider. An unset provider (tests building a Config by hand) means local.
func externalAuth(cfg config.Config) bool {
	return cfg.AuthProvider == config.AuthOIDC || cfg.AuthProvider == config.AuthIdentityPlatform
}

func publicConfig(cfg config.Config, version string) http.HandlerFunc {
	type features struct {
		PublicSharing bool `json:"public_sharing"`
	}
	// identityPlatform is the Firebase web configuration; the API key is
	// public by design.
	type identityPlatform struct {
		APIKey        string   `json:"api_key"`
		AuthDomain    string   `json:"auth_domain"`
		ProjectID     string   `json:"project_id"`
		TenantID      string   `json:"tenant_id,omitempty"`
		SignInMethods []string `json:"sign_in_methods"`
	}
	type authInfo struct {
		Provider         string            `json:"provider"`
		DisplayName      string            `json:"display_name,omitempty"`
		IdentityPlatform *identityPlatform `json:"identity_platform,omitempty"`
	}
	type body struct {
		SignupMode string   `json:"signup_mode"`
		Features   features `json:"features"`
		Auth       authInfo `json:"auth"`
		Version    string   `json:"version"`
	}
	resp := body{
		SignupMode: cfg.SignupMode,
		Features:   features{PublicSharing: cfg.PublicSharing},
		Auth:       authInfo{Provider: config.AuthLocal},
		Version:    version,
	}
	switch cfg.AuthProvider {
	case config.AuthOIDC:
		resp.Auth = authInfo{Provider: config.AuthOIDC, DisplayName: cfg.OIDCProviderName}
	case config.AuthIdentityPlatform:
		resp.Auth = authInfo{Provider: config.AuthIdentityPlatform, IdentityPlatform: &identityPlatform{
			APIKey: cfg.IDPAPIKey, AuthDomain: cfg.IDPAuthDomain, ProjectID: cfg.IDPProjectID,
			TenantID: cfg.IDPTenantID, SignInMethods: cfg.IDPSignInMethods,
		}}
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, resp)
	}
}
