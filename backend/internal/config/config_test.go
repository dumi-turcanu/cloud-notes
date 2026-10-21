package config

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func noFiles(string) ([]byte, error) { return nil, os.ErrNotExist }

func baseEnv() map[string]string {
	return map[string]string{"DB_HOST": "db", "DB_NAME": "n", "DB_USER": "u", "DB_PASSWORD": "p"}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(baseEnv()), noFiles)
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.APIBasePath != "/api" || c.MetricsAddr != ":9090" || c.LogLevel != "info" {
		t.Errorf("listener defaults wrong: %+v", c)
	}
	if c.ShutdownTimeout != 15*time.Second || c.ShutdownDelay != 0 || c.TrustedProxyHops != 0 {
		t.Errorf("shutdown defaults wrong: %+v", c)
	}
	if c.SignupMode != SignupDisabled || c.PublicSharing || !c.SessionCookieSecure || !c.DBMigrateOnStart {
		t.Errorf("secure defaults wrong: %+v", c)
	}
	if c.SessionTTL != 12*time.Hour || c.SessionIdleTimeout != time.Hour || c.MaxFailedLogins != 5 ||
		c.LockoutDuration != 15*time.Minute || c.RateLimitPerMinute != 10 || c.DBMaxConns != 10 {
		t.Errorf("auth defaults wrong: %+v", c)
	}
	u, _ := url.Parse(c.DatabaseURL)
	if u.Host != "db:5432" || u.Query().Get("sslmode") != "require" {
		t.Errorf("dsn defaults wrong: %s", c.DatabaseURL)
	}
}

func TestPortDerivesHTTPAddr(t *testing.T) {
	e := baseEnv()
	e["PORT"] = "9999"
	c, err := Load(env(e), noFiles)
	if err != nil || c.HTTPAddr != ":9999" {
		t.Fatalf("got %q, %v", c.HTTPAddr, err)
	}
	e["HTTP_ADDR"] = "127.0.0.1:1"
	c, _ = Load(env(e), noFiles)
	if c.HTTPAddr != "127.0.0.1:1" {
		t.Errorf("HTTP_ADDR must win over PORT, got %q", c.HTTPAddr)
	}
}

func TestSecretFilePrecedenceAndTrim(t *testing.T) {
	e := baseEnv()
	e["DB_PASSWORD"] = "from-env"
	e["DB_PASSWORD_FILE"] = "/secrets/pw"
	read := func(p string) ([]byte, error) {
		if p != "/secrets/pw" {
			return nil, os.ErrNotExist
		}
		return []byte("from-file\n"), nil
	}
	c, err := Load(env(e), read)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(c.DatabaseURL)
	if pw, _ := u.User.Password(); pw != "from-file" {
		t.Errorf("password = %q, want from-file", pw)
	}
}

func TestSecretFileUnreadableIsError(t *testing.T) {
	e := baseEnv()
	e["DB_PASSWORD_FILE"] = "/missing"
	if _, err := Load(env(e), noFiles); err == nil || !strings.Contains(err.Error(), "DB_PASSWORD_FILE") {
		t.Fatalf("want DB_PASSWORD_FILE error, got %v", err)
	}
}

func TestDSNEscapesSpecialCharacters(t *testing.T) {
	e := baseEnv()
	e["DB_PASSWORD"] = "p@ss:w/ord?#%&=+ x"
	e["DB_USER"] = "us er@x"
	e["DB_PORT"] = "6543"
	e["DB_SSLMODE"] = "verify-full"
	e["DB_SSLROOTCERT"] = "/etc/ca bundle.pem"
	c, err := Load(env(e), noFiles)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if pw != e["DB_PASSWORD"] || u.User.Username() != e["DB_USER"] {
		t.Errorf("credentials not round-tripped: %q %q", u.User.Username(), pw)
	}
	if u.Host != "db:6543" || u.Path != "/n" {
		t.Errorf("host/path wrong: %s %s", u.Host, u.Path)
	}
	if u.Query().Get("sslmode") != "verify-full" || u.Query().Get("sslrootcert") != "/etc/ca bundle.pem" {
		t.Errorf("query wrong: %s", u.RawQuery)
	}
}

func TestDatabaseURLOverridesParts(t *testing.T) {
	c, err := Load(env(map[string]string{"DATABASE_URL": "postgres://a:b@h:1/d?sslmode=disable"}), noFiles)
	if err != nil || c.DatabaseURL != "postgres://a:b@h:1/d?sslmode=disable" {
		t.Fatalf("got %q, %v", c.DatabaseURL, err)
	}
}

func TestValidationAggregatesAllErrors(t *testing.T) {
	_, err := Load(env(map[string]string{
		"API_BASE_PATH":          "api/",
		"LOG_LEVEL":              "loud",
		"SHUTDOWN_TIMEOUT":       "soon",
		"TRUSTED_PROXY_HOPS":     "-1",
		"FEATURE_PUBLIC_SHARING": "true",
		"DB_SSLMODE":             "sometimes",
		"AUTH_SIGNUP_MODE":       "invite",
		"AUTH_INVITE_CODE":       "short",
		"SESSION_COOKIE_SECURE":  "maybe",
		"DB_MAX_CONNS":           "0",
	}), noFiles)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"API_BASE_PATH", "LOG_LEVEL", "SHUTDOWN_TIMEOUT", "TRUSTED_PROXY_HOPS", "PUBLIC_APP_URL",
		"DB_SSLMODE", "AUTH_INVITE_CODE", "SESSION_COOKIE_SECURE", "DB_MAX_CONNS",
		"DB_HOST", "DB_NAME", "DB_USER", "DB_PASSWORD",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error list missing %s:\n%v", want, err)
		}
	}
	var j interface{ Unwrap() []error }
	if !errors.As(err, &j) || len(j.Unwrap()) < 10 {
		t.Errorf("errors not aggregated")
	}
}

func TestSharingRequiresValidPublicAppURL(t *testing.T) {
	e := baseEnv()
	e["FEATURE_PUBLIC_SHARING"] = "true"
	e["PUBLIC_APP_URL"] = "https://notes.example.com/"
	c, err := Load(env(e), noFiles)
	if err != nil || c.PublicAppURL != "https://notes.example.com" {
		t.Fatalf("got %q, %v", c.PublicAppURL, err)
	}
	e["PUBLIC_APP_URL"] = "notes.example.com"
	if _, err := Load(env(e), noFiles); err == nil {
		t.Error("relative PUBLIC_APP_URL must be rejected")
	}
}

func TestVerifyModesRequireRootCert(t *testing.T) {
	e := baseEnv()
	e["DB_SSLMODE"] = "verify-ca"
	if _, err := Load(env(e), noFiles); err == nil || !strings.Contains(err.Error(), "DB_SSLROOTCERT") {
		t.Errorf("want DB_SSLROOTCERT error, got %v", err)
	}
}

func TestCSRFTrustedOriginsParsed(t *testing.T) {
	e := baseEnv()
	e["CSRF_TRUSTED_ORIGINS"] = " https://a.example , ,https://b.example"
	c, _ := Load(env(e), noFiles)
	if len(c.CSRFTrustedOrigins) != 2 || c.CSRFTrustedOrigins[1] != "https://b.example" {
		t.Errorf("got %v", c.CSRFTrustedOrigins)
	}
}

func TestNewUserPassword(t *testing.T) {
	pw, err := NewUserPassword(env(map[string]string{"NEW_USER_PASSWORD": " abc "}), noFiles)
	if err != nil || pw != "abc" {
		t.Errorf("got %q %v", pw, err)
	}
}

func oidcEnv() map[string]string {
	e := baseEnv()
	e["AUTH_PROVIDER"] = "oidc"
	e["PUBLIC_APP_URL"] = "https://notes.example.com/"
	e["OIDC_ISSUER_URL"] = "https://idp.example.com/realm"
	e["OIDC_CLIENT_ID"] = "cloud-notes"
	e["OIDC_CLIENT_SECRET"] = "s3cret"
	return e
}

func idpEnv() map[string]string {
	e := baseEnv()
	e["AUTH_PROVIDER"] = "identity-platform"
	e["IDP_PROJECT_ID"] = "my-project"
	e["IDP_API_KEY"] = "public-key"
	e["IDP_AUTH_DOMAIN"] = "my-project.firebaseapp.com"
	return e
}

func TestAuthProviderDefaultsToLocalAndIgnoresExternalSettings(t *testing.T) {
	e := baseEnv()
	e["OIDC_ISSUER_URL"] = "not a url"
	e["IDP_AUTH_DOMAIN"] = "https://bad/"
	c, err := Load(env(e), noFiles)
	if err != nil || c.AuthProvider != AuthLocal {
		t.Fatalf("got %q, %v", c.AuthProvider, err)
	}
	e["AUTH_PROVIDER"] = "ldap"
	if _, err := Load(env(e), noFiles); err == nil || !strings.Contains(err.Error(), "AUTH_PROVIDER") {
		t.Errorf("want AUTH_PROVIDER error, got %v", err)
	}
}

func TestOIDCDefaults(t *testing.T) {
	c, err := Load(env(oidcEnv()), noFiles)
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthProvider != AuthOIDC || c.OIDCIssuerURL != "https://idp.example.com/realm" ||
		c.OIDCClientID != "cloud-notes" || c.OIDCClientSecret != "s3cret" {
		t.Errorf("settings wrong: %+v", c)
	}
	if c.OIDCRedirectURL != "https://notes.example.com/api/auth/oidc/callback" ||
		c.OIDCPostLogoutRedirectURL != "https://notes.example.com/login" {
		t.Errorf("derived URLs wrong: %q %q", c.OIDCRedirectURL, c.OIDCPostLogoutRedirectURL)
	}
	if strings.Join(c.OIDCScopes, " ") != "openid email profile" || c.OIDCProviderName != "Single sign-on" {
		t.Errorf("defaults wrong: %v %q", c.OIDCScopes, c.OIDCProviderName)
	}
}

func TestOIDCRedirectFollowsAPIBasePath(t *testing.T) {
	e := oidcEnv()
	e["API_BASE_PATH"] = "/v1/api"
	c, err := Load(env(e), noFiles)
	if err != nil || c.OIDCRedirectURL != "https://notes.example.com/v1/api/auth/oidc/callback" {
		t.Fatalf("got %q, %v", c.OIDCRedirectURL, err)
	}
}

func TestOIDCClientSecretFile(t *testing.T) {
	e := oidcEnv()
	delete(e, "OIDC_CLIENT_SECRET")
	e["OIDC_CLIENT_SECRET_FILE"] = "/secrets/oidc"
	read := func(string) ([]byte, error) { return []byte("from-file\n"), nil }
	c, err := Load(env(e), read)
	if err != nil || c.OIDCClientSecret != "from-file" {
		t.Fatalf("got %q, %v", c.OIDCClientSecret, err)
	}
}

func TestOIDCValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(e map[string]string)
		wantErr string // substring; "" means the config must load
	}{
		{"valid with all options", func(e map[string]string) {
			e["OIDC_SCOPES"] = "openid  email"
			e["OIDC_LOGOUT_URL"] = "https://d.example.com/logout?client_id={client_id}&logout_uri={post_logout_redirect_uri}"
			e["OIDC_DISCOVERY_URL"] = "https://internal.example.com/realm"
		}, ""},
		{"issuer missing", func(e map[string]string) { delete(e, "OIDC_ISSUER_URL") }, "OIDC_ISSUER_URL"},
		{"issuer http", func(e map[string]string) { e["OIDC_ISSUER_URL"] = "http://idp.example.com" }, "OIDC_ISSUER_URL must use https"},
		{"issuer relative", func(e map[string]string) { e["OIDC_ISSUER_URL"] = "idp.example.com" }, "OIDC_ISSUER_URL"},
		{"issuer with fragment", func(e map[string]string) { e["OIDC_ISSUER_URL"] = "https://idp.example.com/#x" }, "OIDC_ISSUER_URL"},
		{"client id missing", func(e map[string]string) { delete(e, "OIDC_CLIENT_ID") }, "OIDC_CLIENT_ID"},
		{"client secret missing", func(e map[string]string) { delete(e, "OIDC_CLIENT_SECRET") }, "OIDC_CLIENT_SECRET"},
		{"public app url missing", func(e map[string]string) { delete(e, "PUBLIC_APP_URL") }, "PUBLIC_APP_URL is required when AUTH_PROVIDER=oidc"},
		{"public app url http", func(e map[string]string) { e["PUBLIC_APP_URL"] = "http://notes.example.com" }, "OIDC_REDIRECT_URL must use https"},
		{"redirect http", func(e map[string]string) { e["OIDC_REDIRECT_URL"] = "http://notes.example.com/cb" }, "OIDC_REDIRECT_URL must use https"},
		{"logout http", func(e map[string]string) { e["OIDC_LOGOUT_URL"] = "http://d.example.com/logout?c={client_id}" }, "OIDC_LOGOUT_URL must use https"},
		{"post logout http", func(e map[string]string) { e["OIDC_POST_LOGOUT_REDIRECT_URL"] = "http://notes.example.com/login" }, "OIDC_POST_LOGOUT_REDIRECT_URL must use https"},
		{"discovery http", func(e map[string]string) { e["OIDC_DISCOVERY_URL"] = "http://keycloak:8080/realm" }, "OIDC_DISCOVERY_URL must use https"},
		{"scopes without openid", func(e map[string]string) { e["OIDC_SCOPES"] = "email profile" }, "OIDC_SCOPES must contain openid"},
		{"http allowed for local development", func(e map[string]string) {
			e["SESSION_COOKIE_SECURE"] = "false"
			e["PUBLIC_APP_URL"] = "http://localhost:3000"
			e["OIDC_ISSUER_URL"] = "http://localhost:8081/realms/cloud-notes"
			e["OIDC_DISCOVERY_URL"] = "http://keycloak:8080/realms/cloud-notes"
		}, ""},
		{"non-http scheme is never allowed", func(e map[string]string) {
			e["SESSION_COOKIE_SECURE"] = "false"
			e["OIDC_ISSUER_URL"] = "ftp://idp.example.com"
		}, "OIDC_ISSUER_URL must use https"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := oidcEnv()
			tc.mutate(e)
			_, err := Load(env(e), noFiles)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestOIDCErrorsAreAggregated(t *testing.T) {
	_, err := Load(env(map[string]string{
		"AUTH_PROVIDER": "oidc", "DB_HOST": "db", "DB_NAME": "n", "DB_USER": "u", "DB_PASSWORD": "p",
	}), noFiles)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"PUBLIC_APP_URL", "OIDC_ISSUER_URL", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error list missing %s:\n%v", want, err)
		}
	}
}

func TestSharingStillRequiresPublicAppURLWithOIDC(t *testing.T) {
	e := oidcEnv()
	delete(e, "PUBLIC_APP_URL")
	e["FEATURE_PUBLIC_SHARING"] = "true"
	_, err := Load(env(e), noFiles)
	if err == nil || !strings.Contains(err.Error(), "FEATURE_PUBLIC_SHARING") || !strings.Contains(err.Error(), "AUTH_PROVIDER=oidc") {
		t.Errorf("want both PUBLIC_APP_URL reasons, got %v", err)
	}
}

func TestIdentityPlatformDefaults(t *testing.T) {
	c, err := Load(env(idpEnv()), noFiles)
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthProvider != AuthIdentityPlatform || c.IDPProjectID != "my-project" || c.IDPAPIKey != "public-key" ||
		c.IDPAuthDomain != "my-project.firebaseapp.com" || c.IDPTenantID != "" {
		t.Errorf("settings wrong: %+v", c)
	}
	if len(c.IDPSignInMethods) != 1 || c.IDPSignInMethods[0] != SignInPassword {
		t.Errorf("sign-in methods = %v, want [password]", c.IDPSignInMethods)
	}
}

func TestIdentityPlatformValidation(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(e map[string]string)
		wantErr     string
		wantMethods string
	}{
		{"project missing", func(e map[string]string) { delete(e, "IDP_PROJECT_ID") }, "IDP_PROJECT_ID", ""},
		{"api key missing", func(e map[string]string) { delete(e, "IDP_API_KEY") }, "IDP_API_KEY", ""},
		{"auth domain missing", func(e map[string]string) { delete(e, "IDP_AUTH_DOMAIN") }, "IDP_AUTH_DOMAIN", ""},
		{"auth domain with scheme", func(e map[string]string) { e["IDP_AUTH_DOMAIN"] = "https://a.firebaseapp.com" }, "IDP_AUTH_DOMAIN must be a bare host name", ""},
		{"auth domain with path", func(e map[string]string) { e["IDP_AUTH_DOMAIN"] = "a.firebaseapp.com/x" }, "IDP_AUTH_DOMAIN must be a bare host name", ""},
		{"auth domain with port", func(e map[string]string) { e["IDP_AUTH_DOMAIN"] = "a.firebaseapp.com:443" }, "IDP_AUTH_DOMAIN must be a bare host name", ""},
		{"tenant is optional", func(e map[string]string) { e["IDP_TENANT_ID"] = "tenant-abc" }, "", "password"},
		{"password and google", func(e map[string]string) { e["IDP_SIGN_IN_METHODS"] = "password, Google" }, "", "password,google"},
		{"duplicates collapse", func(e map[string]string) { e["IDP_SIGN_IN_METHODS"] = "google,google" }, "", "google"},
		{"unknown method", func(e map[string]string) { e["IDP_SIGN_IN_METHODS"] = "password,facebook" }, "IDP_SIGN_IN_METHODS", ""},
		{"only separators", func(e map[string]string) { e["IDP_SIGN_IN_METHODS"] = " , " }, "IDP_SIGN_IN_METHODS must list at least one", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := idpEnv()
			tc.mutate(e)
			c, err := Load(env(e), noFiles)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := strings.Join(c.IDPSignInMethods, ","); got != tc.wantMethods {
				t.Errorf("sign-in methods = %q, want %q", got, tc.wantMethods)
			}
		})
	}
}
