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
