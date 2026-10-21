package auth

import (
	"bytes"
	"crypto"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth/oidctest"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
)

const (
	idpProject = "test-project"
	idpIssuer  = "https://securetoken.google.com/" + idpProject
)

type idpFixture struct {
	*httpFixture
	signer *oidctest.Signer
}

func newIDPFixture(t *testing.T, rate int, mutate func(*IdentityPlatformSettings)) *idpFixture {
	t.Helper()
	signer := oidctest.NewSigner(t)
	f := newFixture(t)
	settings := IdentityPlatformSettings{
		ProjectID: idpProject, SignInMethods: []string{config.SignInPassword},
		KeySet: &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{signer.Public()}}, Now: f.clock.Now,
	}
	if mutate != nil {
		mutate(&settings)
	}
	rec := &recorder{}
	hs := NewHandlers(f.svc, HandlerConfig{
		SecureCookie: false, Recorder: rec, Now: f.clock.Now, Limiter: NewRateLimiter(rate, f.clock.Now),
		IdentityPlatform: NewIdentityPlatformVerifier(settings),
	})
	r := chi.NewRouter()
	r.Post("/session", hs.CreateSession)
	r.With(RequireSession(f.svc, false)).Get("/me", hs.Me)
	return &idpFixture{&httpFixture{f, r, rec, false}, signer}
}

// token returns a valid password sign-in token for ann@example.com; mutate
// adjusts the claims before signing.
func (f *idpFixture) token(mutate ...func(c map[string]any)) string {
	now := f.clock.Now()
	c := map[string]any{
		"iss": idpIssuer, "aud": idpProject, "sub": "firebase-uid-1", "user_id": "firebase-uid-1",
		"auth_time": now.Add(-10 * time.Second).Unix(), "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"email": "ann@example.com", "email_verified": false,
		"firebase": map[string]any{"sign_in_provider": "password", "identities": map[string]any{"email": []string{"ann@example.com"}}},
	}
	for _, m := range mutate {
		m(c)
	}
	return f.signer.Sign(c)
}

func (f *idpFixture) post(tok string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"id_token": tok})
	return f.do("POST", "/session", string(body))
}

func firebase(c map[string]any, k string, v any) { c["firebase"].(map[string]any)[k] = v }

func TestIdentityPlatformSessionHappyPath(t *testing.T) {
	f := newIDPFixture(t, 100, nil)
	rr := f.post(f.token(func(c map[string]any) { c["name"] = "Ann Example" }))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	c := sessionCookie(t, rr, "cn_session")
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Errorf("session cookie wrong: %+v", c)
	}
	var u map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &u)
	if u["email"] != "ann@example.com" || u["display_name"] != "Ann Example" || u["id"] == nil {
		t.Errorf("user body: %v", u)
	}
	if me := f.do("GET", "/me", "", c); me.Code != 200 || !strings.Contains(me.Body.String(), "ann@example.com") {
		t.Errorf("/me: %d %s", me.Code, me.Body)
	}
	if got := strings.Join(f.rec.logins, ","); got != "idp_success" {
		t.Errorf("recorder: %s", got)
	}

	// The same Firebase user maps to the same account, keyed on the token's iss.
	again := f.post(f.token())
	if again.Code != 200 || sessionCookie(t, again, "cn_session").Value == c.Value {
		t.Errorf("second login: %d", again.Code)
	}
	var u2 map[string]any
	_ = json.Unmarshal(again.Body.Bytes(), &u2)
	if u2["id"] != u["id"] {
		t.Error("same (issuer, subject) must be the same user")
	}
}

func TestIdentityPlatformSessionRotatesPresentedSession(t *testing.T) {
	f := newIDPFixture(t, 100, nil)
	first := sessionCookie(t, f.post(f.token()), "cn_session")
	body, _ := json.Marshal(map[string]string{"id_token": f.token()})
	rr := f.do("POST", "/session", string(body), first)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	if me := f.do("GET", "/me", "", first); me.Code != http.StatusUnauthorized {
		t.Errorf("the previous session must be revoked, /me = %d", me.Code)
	}
}

func TestIdentityPlatformTokenRejections(t *testing.T) {
	tests := []struct {
		name     string
		settings func(*IdentityPlatformSettings)
		token    func(f *idpFixture) string
	}{
		{"expired", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["exp"] = f.clock.Now().Add(-time.Minute).Unix() })
		}},
		{"wrong audience", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["aud"] = "other-project" })
		}},
		{"wrong issuer", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["iss"] = "https://securetoken.google.com/other-project" })
		}},
		{"accounts.google.com issuer", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["iss"] = "https://accounts.google.com" })
		}},
		{"signed by another key", nil, func(f *idpFixture) string {
			f.signer = oidctest.NewSigner(t)
			return f.token()
		}},
		{"auth_time too old", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["auth_time"] = f.clock.Now().Add(-6 * time.Minute).Unix() })
		}},
		{"auth_time in the future", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["auth_time"] = f.clock.Now().Add(10 * time.Minute).Unix() })
		}},
		{"auth_time missing", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { delete(c, "auth_time") })
		}},
		{"subject missing", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { delete(c, "sub") })
		}},
		{"subject empty", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["sub"] = "" })
		}},
		{"email missing", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { delete(c, "email") })
		}},
		{"email not an address", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["email"] = "nope" })
		}},
		{"anonymous sign-in", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { firebase(c, "sign_in_provider", "anonymous") })
		}},
		{"custom token sign-in", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { firebase(c, "sign_in_provider", "custom") })
		}},
		{"google when only password is enabled", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { firebase(c, "sign_in_provider", "google.com") })
		}},
		{"password when only google is enabled", func(s *IdentityPlatformSettings) { s.SignInMethods = []string{config.SignInGoogle} },
			func(f *idpFixture) string { return f.token() }},
		{"provider missing", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { delete(c, "firebase") })
		}},
		{"tenant required but absent", func(s *IdentityPlatformSettings) { s.TenantID = "tenant-1" },
			func(f *idpFixture) string { return f.token() }},
		{"tenant mismatch", func(s *IdentityPlatformSettings) { s.TenantID = "tenant-1" },
			func(f *idpFixture) string {
				return f.token(func(c map[string]any) { firebase(c, "tenant", "tenant-2") })
			}},
		{"unsigned token", nil, func(f *idpFixture) string {
			return "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJ4In0."
		}},
		{"garbage", nil, func(f *idpFixture) string { return "not-a-jwt" }},
		{"empty", nil, func(f *idpFixture) string { return "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newIDPFixture(t, 100, tc.settings)
			rr := f.post(tc.token(f))
			if rr.Code != http.StatusUnauthorized || errCode(t, rr) != "invalid_token" {
				t.Errorf("got %d %s, want 401 invalid_token", rr.Code, rr.Body)
			}
			for _, c := range rr.Result().Cookies() {
				if c.Name == "cn_session" {
					t.Error("a rejected token must not set a session cookie")
				}
			}
			if f.sessions.Count() != 0 {
				t.Error("a rejected token must not create a session")
			}
			if strings.Contains(rr.Body.String(), "expired") || strings.Contains(rr.Body.String(), "audience") {
				t.Errorf("the response must not say why: %s", rr.Body)
			}
		})
	}
}

func TestIdentityPlatformAcceptedVariants(t *testing.T) {
	tests := []struct {
		name     string
		settings func(*IdentityPlatformSettings)
		token    func(f *idpFixture) string
	}{
		{"google sign-in", func(s *IdentityPlatformSettings) {
			s.SignInMethods = []string{config.SignInPassword, config.SignInGoogle}
		},
			func(f *idpFixture) string {
				return f.token(func(c map[string]any) { firebase(c, "sign_in_provider", "google.com"); c["email_verified"] = true })
			}},
		{"tenant matches", func(s *IdentityPlatformSettings) { s.TenantID = "tenant-1" },
			func(f *idpFixture) string {
				return f.token(func(c map[string]any) { firebase(c, "tenant", "tenant-1") })
			}},
		{"auth_time just inside the window", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["auth_time"] = f.clock.Now().Add(-4*time.Minute - 59*time.Second).Unix() })
		}},
		{"small clock skew into the future", nil, func(f *idpFixture) string {
			return f.token(func(c map[string]any) { c["auth_time"] = f.clock.Now().Add(30 * time.Second).Unix() })
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newIDPFixture(t, 100, tc.settings)
			if rr := f.post(tc.token(f)); rr.Code != 200 {
				t.Errorf("got %d %s", rr.Code, rr.Body)
			}
		})
	}
}

func TestIdentityPlatformAccountConflict(t *testing.T) {
	f := newIDPFixture(t, 100, nil)
	f.register(t, "ann@example.com") // a local account owns the email

	rr := f.post(f.token())
	if rr.Code != http.StatusConflict || errCode(t, rr) != "account_conflict" {
		t.Errorf("got %d %s, want 409 account_conflict", rr.Code, rr.Body)
	}
	if f.sessions.Count() != 1 {
		t.Error("a conflicting identity must not get a session")
	}
	if got := f.rec.logins; len(got) != 1 || got[0] != "idp_conflict" {
		t.Errorf("recorder: %v", got)
	}
}

func TestIdentityPlatformRequestValidation(t *testing.T) {
	f := newIDPFixture(t, 100, nil)
	tests := []struct {
		name, body string
		status     int
		code       string
	}{
		{"not json", `nope`, 400, "invalid_request"},
		{"unknown field", `{"id_token":"x","admin":true}`, 400, "invalid_request"},
		{"missing token", `{}`, 401, "invalid_token"},
		{"wrong type", `{"id_token":42}`, 400, "invalid_request"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.do("POST", "/session", tc.body)
			if rr.Code != tc.status || errCode(t, rr) != tc.code {
				t.Errorf("got %d %s", rr.Code, rr.Body)
			}
		})
	}
}

func TestIdentityPlatformSessionIsRateLimited(t *testing.T) {
	f := newIDPFixture(t, 2, nil)
	for range 2 {
		f.post("garbage")
	}
	rr := f.post(f.token())
	if rr.Code != http.StatusTooManyRequests || errCode(t, rr) != "too_many_requests" {
		t.Errorf("got %d %s, want 429", rr.Code, rr.Body)
	}
	if f.sessions.Count() != 0 {
		t.Error("a rate-limited request must not log in")
	}
}

func TestIdentityPlatformNeverLogsTheToken(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	f := newIDPFixture(t, 100, nil)
	bad := f.token(func(c map[string]any) { c["aud"] = "other-project" })
	good := f.token()
	rr1, rr2 := f.post(bad), f.post(good)
	for name, secret := range map[string]string{"rejected token": bad, "accepted token": good, "session cookie": sessionCookie(t, rr2, "cn_session").Value} {
		if strings.Contains(logs.String()+rr1.Body.String(), secret) {
			t.Errorf("%s leaked into the log or a response", name)
		}
	}
}
