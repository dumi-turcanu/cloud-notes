// Package config loads and validates the process configuration from the
// environment. Secrets additionally accept a <NAME>_FILE variant.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Signup modes.
const (
	SignupDisabled = "disabled"
	SignupInvite   = "invite"
	SignupOpen     = "open"
)

// Authentication providers (AUTH_PROVIDER).
const (
	AuthLocal            = "local"
	AuthOIDC             = "oidc"
	AuthIdentityPlatform = "identity-platform"
)

// Identity Platform sign-in methods (IDP_SIGN_IN_METHODS).
const (
	SignInPassword = "password"
	SignInGoogle   = "google"
)

// Config is the complete, validated runtime configuration.
type Config struct {
	HTTPAddr           string
	APIBasePath        string
	MetricsAddr        string
	LogLevel           string
	ShutdownDelay      time.Duration
	ShutdownTimeout    time.Duration
	TrustedProxyHops   int
	CSRFTrustedOrigins []string
	PublicSharing      bool
	PublicAppURL       string

	DatabaseURL      string // fully assembled DSN (postgres://...)
	DBMaxConns       int
	DBMigrateOnStart bool

	SignupMode          string
	InviteCode          string
	SessionTTL          time.Duration
	SessionIdleTimeout  time.Duration
	SessionCookieSecure bool
	MaxFailedLogins     int
	LockoutDuration     time.Duration
	RateLimitPerMinute  int

	// AuthProvider selects the identity backend: local, oidc or identity-platform.
	AuthProvider string

	// AUTH_PROVIDER=oidc: the API is a confidential OIDC client (BFF).
	OIDCIssuerURL             string
	OIDCClientID              string
	OIDCClientSecret          string
	OIDCRedirectURL           string
	OIDCScopes                []string
	OIDCProviderName          string
	OIDCLogoutURL             string // template with {client_id} and {post_logout_redirect_uri}
	OIDCPostLogoutRedirectURL string
	OIDCDiscoveryURL          string // dev only: where to fetch discovery from

	// AUTH_PROVIDER=identity-platform (GCP). The API key is public by design:
	// it is sent to the browser.
	IDPProjectID     string
	IDPAPIKey        string
	IDPAuthDomain    string
	IDPTenantID      string
	IDPSignInMethods []string
}

// Load builds a Config. getenv and readFile are injected for testability.
// All validation problems are collected and returned together.
func Load(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	l := &loader{getenv: getenv, readFile: readFile}
	var c Config

	port := l.str("PORT", "")
	defAddr := ":8080"
	if port != "" {
		defAddr = ":" + port
	}
	c.HTTPAddr = l.str("HTTP_ADDR", defAddr)
	c.APIBasePath = l.str("API_BASE_PATH", "/api")
	if !strings.HasPrefix(c.APIBasePath, "/") || strings.HasSuffix(c.APIBasePath, "/") {
		l.errf("API_BASE_PATH must start with '/' and have no trailing slash, got %q", c.APIBasePath)
	}
	c.MetricsAddr = l.str("METRICS_ADDR", ":9090")
	c.LogLevel = strings.ToLower(l.str("LOG_LEVEL", "info"))
	if !oneOf(c.LogLevel, "debug", "info", "warn", "error") {
		l.errf("LOG_LEVEL must be one of debug|info|warn|error, got %q", c.LogLevel)
	}
	c.ShutdownDelay = l.duration("SHUTDOWN_DELAY", 0, false)
	c.ShutdownTimeout = l.duration("SHUTDOWN_TIMEOUT", 15*time.Second, true)
	c.TrustedProxyHops = l.integer("TRUSTED_PROXY_HOPS", 0, 0)
	for _, o := range strings.Split(l.str("CSRF_TRUSTED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.CSRFTrustedOrigins = append(c.CSRFTrustedOrigins, o)
		}
	}
	c.PublicSharing = l.boolean("FEATURE_PUBLIC_SHARING", false)
	c.PublicAppURL = strings.TrimRight(l.str("PUBLIC_APP_URL", ""), "/")
	c.AuthProvider = strings.ToLower(l.str("AUTH_PROVIDER", AuthLocal))
	if !oneOf(c.AuthProvider, AuthLocal, AuthOIDC, AuthIdentityPlatform) {
		l.errf("AUTH_PROVIDER must be one of local|oidc|identity-platform, got %q", c.AuthProvider)
	}
	if c.PublicSharing || c.AuthProvider == AuthOIDC {
		if c.PublicAppURL == "" {
			if c.PublicSharing {
				l.errf("PUBLIC_APP_URL is required when FEATURE_PUBLIC_SHARING=true")
			}
			if c.AuthProvider == AuthOIDC {
				l.errf("PUBLIC_APP_URL is required when AUTH_PROVIDER=oidc")
			}
		} else if u, err := url.Parse(c.PublicAppURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			l.errf("PUBLIC_APP_URL must be an absolute http(s) URL, got %q", c.PublicAppURL)
		}
	}

	c.DBMaxConns = l.integer("DB_MAX_CONNS", 10, 1)
	c.DBMigrateOnStart = l.boolean("DB_MIGRATE_ON_START", true)
	c.DatabaseURL = l.database()

	c.SignupMode = strings.ToLower(l.str("AUTH_SIGNUP_MODE", SignupDisabled))
	if !oneOf(c.SignupMode, SignupDisabled, SignupInvite, SignupOpen) {
		l.errf("AUTH_SIGNUP_MODE must be one of disabled|invite|open, got %q", c.SignupMode)
	}
	c.InviteCode = l.secret("AUTH_INVITE_CODE")
	if c.SignupMode == SignupInvite && len(c.InviteCode) < 16 {
		l.errf("AUTH_INVITE_CODE must be at least 16 characters when AUTH_SIGNUP_MODE=invite")
	}
	c.SessionTTL = l.duration("SESSION_TTL", 12*time.Hour, true)
	c.SessionIdleTimeout = l.duration("SESSION_IDLE_TIMEOUT", time.Hour, true)
	c.SessionCookieSecure = l.boolean("SESSION_COOKIE_SECURE", true)
	c.MaxFailedLogins = l.integer("AUTH_MAX_FAILED_LOGINS", 5, 1)
	c.LockoutDuration = l.duration("AUTH_LOCKOUT_DURATION", 15*time.Minute, true)
	c.RateLimitPerMinute = l.integer("AUTH_RATE_LIMIT_PER_MINUTE", 10, 1)

	switch c.AuthProvider {
	case AuthOIDC:
		l.oidc(&c)
	case AuthIdentityPlatform:
		l.identityPlatform(&c)
	}

	if len(l.errs) > 0 {
		return Config{}, errors.Join(l.errs...)
	}
	return c, nil
}

// NewUserPassword reads the one-off password used by `create-user`.
func NewUserPassword(getenv func(string) string, readFile func(string) ([]byte, error)) (string, error) {
	l := &loader{getenv: getenv, readFile: readFile}
	v := l.secret("NEW_USER_PASSWORD")
	return v, errors.Join(l.errs...)
}

type loader struct {
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	errs     []error
}

func (l *loader) errf(format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf(format, args...))
}

func (l *loader) str(name, def string) string {
	if v := strings.TrimSpace(l.getenv(name)); v != "" {
		return v
	}
	return def
}

// secret returns NAME_FILE's trimmed content if set, else NAME. The _FILE
// variant wins.
func (l *loader) secret(name string) string {
	if path := strings.TrimSpace(l.getenv(name + "_FILE")); path != "" {
		b, err := l.readFile(path)
		if err != nil {
			l.errf("%s_FILE: cannot read %q: %v", name, path, err)
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	return strings.TrimSpace(l.getenv(name))
}

func (l *loader) duration(name string, def time.Duration, positive bool) time.Duration {
	raw := l.str(name, "")
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		l.errf("%s: invalid duration %q", name, raw)
		return def
	}
	if d < 0 || (positive && d == 0) {
		l.errf("%s: must be %s, got %q", name, map[bool]string{true: "positive", false: "non-negative"}[positive], raw)
	}
	return d
}

func (l *loader) integer(name string, def, min int) int {
	raw := l.str(name, "")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		l.errf("%s: invalid integer %q", name, raw)
		return def
	}
	if n < min {
		l.errf("%s: must be >= %d, got %d", name, min, n)
	}
	return n
}

func (l *loader) boolean(name string, def bool) bool {
	raw := l.str(name, "")
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		l.errf("%s: invalid boolean %q", name, raw)
		return def
	}
	return b
}

// oidc reads and validates the OIDC_* settings. It runs after the session and
// public URL settings because the redirect defaults and the http exception
// depend on them.
func (l *loader) oidc(c *Config) {
	c.OIDCIssuerURL = l.str("OIDC_ISSUER_URL", "")
	l.requireURL("OIDC_ISSUER_URL", c.OIDCIssuerURL, c.SessionCookieSecure, true)
	c.OIDCClientID = l.str("OIDC_CLIENT_ID", "")
	if c.OIDCClientID == "" {
		l.errf("OIDC_CLIENT_ID is required when AUTH_PROVIDER=oidc")
	}
	c.OIDCClientSecret = l.secret("OIDC_CLIENT_SECRET")
	if c.OIDCClientSecret == "" {
		l.errf("OIDC_CLIENT_SECRET (or OIDC_CLIENT_SECRET_FILE) is required when AUTH_PROVIDER=oidc")
	}

	c.OIDCRedirectURL = l.str("OIDC_REDIRECT_URL", "")
	if c.OIDCRedirectURL == "" && c.PublicAppURL != "" {
		c.OIDCRedirectURL = c.PublicAppURL + c.APIBasePath + "/auth/oidc/callback"
	}
	l.requireURL("OIDC_REDIRECT_URL", c.OIDCRedirectURL, c.SessionCookieSecure, false)

	c.OIDCScopes = strings.Fields(l.str("OIDC_SCOPES", "openid email profile"))
	if !slices.Contains(c.OIDCScopes, "openid") {
		l.errf("OIDC_SCOPES must contain openid, got %q", strings.Join(c.OIDCScopes, " "))
	}
	c.OIDCProviderName = l.str("OIDC_PROVIDER_NAME", "Single sign-on")

	c.OIDCLogoutURL = l.str("OIDC_LOGOUT_URL", "")
	if c.OIDCLogoutURL != "" {
		// Placeholders are substituted before the shape check.
		probe := strings.NewReplacer("{client_id}", "x", "{post_logout_redirect_uri}", "x").Replace(c.OIDCLogoutURL)
		l.requireURL("OIDC_LOGOUT_URL", probe, c.SessionCookieSecure, false)
	}
	c.OIDCPostLogoutRedirectURL = l.str("OIDC_POST_LOGOUT_REDIRECT_URL", "")
	if c.OIDCPostLogoutRedirectURL == "" && c.PublicAppURL != "" {
		c.OIDCPostLogoutRedirectURL = c.PublicAppURL + "/login"
	}
	l.requireURL("OIDC_POST_LOGOUT_REDIRECT_URL", c.OIDCPostLogoutRedirectURL, c.SessionCookieSecure, false)

	c.OIDCDiscoveryURL = l.str("OIDC_DISCOVERY_URL", "")
	if c.OIDCDiscoveryURL != "" {
		l.requireURL("OIDC_DISCOVERY_URL", c.OIDCDiscoveryURL, c.SessionCookieSecure, false)
	}
}

// requireURL checks that raw is an absolute https URL (http only when the
// deployment runs without Secure cookies, i.e. local development). An empty
// value is an error only when the setting is required or was derived; callers
// that default from PUBLIC_APP_URL have already reported that one missing.
func (l *loader) requireURL(name, raw string, secure, required bool) {
	if raw == "" {
		if required {
			l.errf("%s is required when AUTH_PROVIDER=oidc", name)
		}
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" {
		l.errf("%s must be an absolute URL without a fragment, got %q", name, raw)
		return
	}
	if u.Scheme != "https" && (u.Scheme != "http" || secure) {
		l.errf("%s must use https (http is allowed only with SESSION_COOKIE_SECURE=false), got %q", name, raw)
	}
}

// identityPlatform reads and validates the IDP_* settings.
func (l *loader) identityPlatform(c *Config) {
	for _, f := range []struct {
		name string
		dst  *string
	}{
		{"IDP_PROJECT_ID", &c.IDPProjectID},
		{"IDP_API_KEY", &c.IDPAPIKey},
		{"IDP_AUTH_DOMAIN", &c.IDPAuthDomain},
	} {
		if *f.dst = l.str(f.name, ""); *f.dst == "" {
			l.errf("%s is required when AUTH_PROVIDER=identity-platform", f.name)
		}
	}
	if d := c.IDPAuthDomain; d != "" {
		if u, err := url.Parse("https://" + d); err != nil || u.Host != d || u.Hostname() == "" || strings.Contains(d, ":") {
			l.errf("IDP_AUTH_DOMAIN must be a bare host name such as <project>.firebaseapp.com, got %q", d)
		}
	}
	c.IDPTenantID = l.str("IDP_TENANT_ID", "")
	for _, m := range strings.Split(l.str("IDP_SIGN_IN_METHODS", SignInPassword), ",") {
		m = strings.ToLower(strings.TrimSpace(m))
		switch {
		case m == "":
		case !oneOf(m, SignInPassword, SignInGoogle):
			l.errf("IDP_SIGN_IN_METHODS entries must be password or google, got %q", m)
		case !slices.Contains(c.IDPSignInMethods, m):
			c.IDPSignInMethods = append(c.IDPSignInMethods, m)
		}
	}
	if len(c.IDPSignInMethods) == 0 {
		l.errf("IDP_SIGN_IN_METHODS must list at least one of password,google")
	}
}

// database resolves DATABASE_URL or assembles a DSN from DB_* variables.
func (l *loader) database() string {
	sslmode := strings.ToLower(l.str("DB_SSLMODE", "require"))
	if !oneOf(sslmode, "disable", "require", "verify-ca", "verify-full") {
		l.errf("DB_SSLMODE must be one of disable|require|verify-ca|verify-full, got %q", sslmode)
	}
	rootCert := l.str("DB_SSLROOTCERT", "")
	if (sslmode == "verify-ca" || sslmode == "verify-full") && rootCert == "" {
		l.errf("DB_SSLROOTCERT is required when DB_SSLMODE=%s", sslmode)
	}

	if dsn := l.secret("DATABASE_URL"); dsn != "" {
		u, err := url.Parse(dsn)
		if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
			l.errf("DATABASE_URL must be a postgres:// URL")
		}
		return dsn
	}

	host := l.str("DB_HOST", "")
	name := l.str("DB_NAME", "")
	user := l.str("DB_USER", "")
	pass := l.secret("DB_PASSWORD")
	port := l.integer("DB_PORT", 5432, 1)
	for k, v := range map[string]string{"DB_HOST": host, "DB_NAME": name, "DB_USER": user, "DB_PASSWORD": pass} {
		if v == "" {
			l.errf("%s is required unless DATABASE_URL is set", k)
		}
	}
	q := url.Values{}
	q.Set("sslmode", sslmode)
	if rootCert != "" {
		q.Set("sslrootcert", rootCert)
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(user, pass),
		Host:     host + ":" + strconv.Itoa(port),
		Path:     "/" + name,
		RawQuery: q.Encode(),
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") { // bare IPv6
		u.Host = "[" + host + "]:" + strconv.Itoa(port)
	}
	return u.String()
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
