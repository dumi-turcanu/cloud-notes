package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/httpx"
)

type recorder struct{ logins, regs []string }

func (r *recorder) LoginAttempt(res string) { r.logins = append(r.logins, res) }
func (r *recorder) Registration(res string) { r.regs = append(r.regs, res) }

type httpFixture struct {
	*fixture
	h      http.Handler
	rec    *recorder
	secure bool
}

func newHTTPFixture(t *testing.T, secure bool, rate int, mutate ...func(*Options)) *httpFixture {
	t.Helper()
	f := newFixture(t, mutate...)
	rec := &recorder{}
	hs := NewHandlers(f.svc, HandlerConfig{
		SecureCookie: secure, ProxyHops: 0, Recorder: rec, Now: f.clock.Now,
		Limiter: NewRateLimiter(rate, f.clock.Now),
	})
	r := chi.NewRouter()
	r.Post("/register", hs.Register)
	r.Post("/login", hs.Login)
	r.Post("/logout", hs.Logout)
	r.With(RequireSession(f.svc, secure)).Get("/me", hs.Me)
	return &httpFixture{f, r, rec, secure}
}

func (f *httpFixture) do(method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.1:5555"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	f.h.ServeHTTP(rr, req)
	return rr
}

func sessionCookie(t *testing.T, rr *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in response; headers: %v", name, rr.Header())
	return nil
}

func errCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var e httpx.ErrorBody
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("body is not an error envelope: %q", rr.Body.String())
	}
	return e.Error
}

const regBody = `{"email":"ann@example.com","password":"a-very-good-password","display_name":"Ann"}`

func TestRegisterSetsSecureCookie(t *testing.T) {
	f := newHTTPFixture(t, true, 100)
	rr := f.do("POST", "/register", regBody)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	c := sessionCookie(t, rr, "__Host-cn_session")
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("cookie attributes wrong: %+v", c)
	}
	if c.MaxAge != int((12 * time.Hour).Seconds()) {
		t.Errorf("Max-Age = %d, want remaining lifetime", c.MaxAge)
	}
	var u map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &u)
	if u["email"] != "ann@example.com" || u["display_name"] != "Ann" || u["id"] == nil {
		t.Errorf("user body: %v", u)
	}
	for _, leak := range []string{"password", "hash", "locked"} {
		if strings.Contains(strings.ToLower(rr.Body.String()), leak) {
			t.Errorf("response leaks %q: %s", leak, rr.Body)
		}
	}
	if len(f.rec.regs) != 1 || f.rec.regs[0] != "success" {
		t.Errorf("recorder: %v", f.rec.regs)
	}
}

func TestInsecureModeCookie(t *testing.T) {
	f := newHTTPFixture(t, false, 100)
	rr := f.do("POST", "/register", regBody)
	c := sessionCookie(t, rr, "cn_session")
	if c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("insecure-mode cookie wrong: %+v", c)
	}
	for _, c := range rr.Result().Cookies() {
		if strings.HasPrefix(c.Name, "__Host-") {
			t.Error("__Host- prefix requires Secure")
		}
	}
}

func TestRegisterErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Options)
		body   string
		status int
		code   string
	}{
		{"disabled", func(o *Options) { o.SignupMode = config.SignupDisabled }, regBody, 403, "signup_disabled"},
		{"invite", func(o *Options) { o.SignupMode = config.SignupInvite; o.InviteCode = strings.Repeat("c", 16) }, regBody, 403, "invalid_invite_code"},
		{"validation", nil, `{"email":"x","password":"short"}`, 400, "validation_failed"},
		{"unknown field", nil, `{"email":"a@b.co","password":"a-very-good-password","admin":true}`, 400, "invalid_request"},
		{"not json", nil, `nope`, 400, "invalid_request"},
		{"trailing data", nil, regBody + `{}`, 400, "invalid_request"},
		{"too large", nil, `{"email":"a@b.co","password":"` + strings.Repeat("a", 300<<10) + `"}`, 413, "payload_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m []func(*Options)
			if tc.mutate != nil {
				m = append(m, tc.mutate)
			}
			f := newHTTPFixture(t, true, 100, m...)
			rr := f.do("POST", "/register", tc.body)
			if rr.Code != tc.status || errCode(t, rr) != tc.code {
				t.Errorf("got %d %s, want %d %s", rr.Code, rr.Body, tc.status, tc.code)
			}
			if len(rr.Result().Cookies()) != 0 {
				t.Error("failed registration must not set a cookie")
			}
		})
	}
}

func TestRegisterValidationReportsFields(t *testing.T) {
	f := newHTTPFixture(t, true, 100)
	rr := f.do("POST", "/register", `{"email":"x","password":"short"}`)
	var e httpx.ErrorBody
	_ = json.Unmarshal(rr.Body.Bytes(), &e)
	if e.Fields["email"] == "" || e.Fields["password"] == "" {
		t.Errorf("fields: %v", e.Fields)
	}
}

func TestRegisterDuplicateIs409(t *testing.T) {
	f := newHTTPFixture(t, true, 100)
	f.do("POST", "/register", regBody)
	rr := f.do("POST", "/register", regBody)
	if rr.Code != 409 || errCode(t, rr) != "email_taken" {
		t.Errorf("got %d %s", rr.Code, rr.Body)
	}
}

func TestLoginFlowAndMe(t *testing.T) {
	f := newHTTPFixture(t, true, 100)
	f.do("POST", "/register", regBody)

	rr := f.do("POST", "/login", `{"email":"ANN@example.com","password":"a-very-good-password"}`)
	if rr.Code != 200 {
		t.Fatalf("login: %d %s", rr.Code, rr.Body)
	}
	c := sessionCookie(t, rr, "__Host-cn_session")

	me := f.do("GET", "/me", "", c)
	if me.Code != 200 || me.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("me: %d cache=%q", me.Code, me.Header().Get("Cache-Control"))
	}

	bad := f.do("POST", "/login", `{"email":"ann@example.com","password":"wrong-password-x"}`)
	if bad.Code != 401 || errCode(t, bad) != "invalid_credentials" {
		t.Errorf("bad login: %d %s", bad.Code, bad.Body)
	}
	if got := f.rec.logins; len(got) != 2 || got[0] != "success" || got[1] != "invalid_credentials" {
		t.Errorf("recorder: %v", got)
	}
}

func TestLoginRotatesPresentedSession(t *testing.T) {
	f := newHTTPFixture(t, true, 100)
	old := sessionCookie(t, f.do("POST", "/register", regBody), "__Host-cn_session")
	rr := f.do("POST", "/login", `{"email":"ann@example.com","password":"a-very-good-password"}`, old)
	fresh := sessionCookie(t, rr, "__Host-cn_session")
	if fresh.Value == old.Value {
		t.Fatal("session token must change on login")
	}
	if f.do("GET", "/me", "", old).Code != 401 {
		t.Error("old session must be revoked")
	}
	if f.do("GET", "/me", "", fresh).Code != 200 {
		t.Error("new session must work")
	}
}

func TestLoginLockoutReturns429WithRetryAfter(t *testing.T) {
	f := newHTTPFixture(t, true, 1000)
	f.do("POST", "/register", regBody)
	for i := 0; i < 3; i++ {
		f.do("POST", "/login", `{"email":"ann@example.com","password":"wrong-password-x"}`)
	}
	rr := f.do("POST", "/login", `{"email":"ann@example.com","password":"a-very-good-password"}`)
	if rr.Code != 429 || errCode(t, rr) != "too_many_attempts" {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if ra := rr.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q", ra)
	}
	if f.rec.logins[len(f.rec.logins)-1] != "locked" {
		t.Errorf("recorder: %v", f.rec.logins)
	}
}

func TestPerIPRateLimit(t *testing.T) {
	f := newHTTPFixture(t, true, 2)
	body := `{"email":"nobody@example.com","password":"wrong-password-x"}`
	f.do("POST", "/login", body)
	f.do("POST", "/login", body)
	rr := f.do("POST", "/login", body)
	if rr.Code != 429 || errCode(t, rr) != "too_many_requests" || rr.Header().Get("Retry-After") != "60" {
		t.Errorf("login: %d %s %v", rr.Code, rr.Body, rr.Header())
	}
	// The bucket is shared between login and register for one client IP.
	if rr := f.do("POST", "/register", regBody); rr.Code != 429 {
		t.Errorf("register must be limited too: %d", rr.Code)
	}
	f.clock.Advance(time.Minute)
	if rr := f.do("POST", "/login", body); rr.Code != 401 {
		t.Errorf("must recover after refill: %d", rr.Code)
	}
}

func TestLogoutAlways204AndRevokes(t *testing.T) {
	f := newHTTPFixture(t, true, 100)
	c := sessionCookie(t, f.do("POST", "/register", regBody), "__Host-cn_session")

	rr := f.do("POST", "/logout", "", c)
	if rr.Code != 204 {
		t.Fatalf("logout: %d", rr.Code)
	}
	cleared := sessionCookie(t, rr, "__Host-cn_session")
	if cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("cookie not cleared: %+v", cleared)
	}
	if f.do("GET", "/me", "", c).Code != 401 {
		t.Error("session must be revoked server-side")
	}
	if f.do("POST", "/logout", "").Code != 204 {
		t.Error("logout without cookie must still be 204")
	}
}

func TestRequireSession(t *testing.T) {
	f := newHTTPFixture(t, true, 100)

	rr := f.do("GET", "/me", "")
	if rr.Code != 401 || errCode(t, rr) != "unauthorized" || rr.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("no cookie: %d %s", rr.Code, rr.Body)
	}

	rr = f.do("GET", "/me", "", &http.Cookie{Name: "__Host-cn_session", Value: "bogus"})
	if rr.Code != 401 {
		t.Fatalf("invalid cookie: %d", rr.Code)
	}
	if c := sessionCookie(t, rr, "__Host-cn_session"); c.MaxAge >= 0 {
		t.Error("invalid session cookie must be cleared")
	}

	// The insecure cookie name is not honoured in secure mode.
	c := sessionCookie(t, f.do("POST", "/register", regBody), "__Host-cn_session")
	if f.do("GET", "/me", "", &http.Cookie{Name: "cn_session", Value: c.Value}).Code != 401 {
		t.Error("wrong cookie name must not authenticate")
	}
	if f.do("GET", "/me", "", c).Code != 200 {
		t.Error("valid session must pass")
	}

	f.clock.Advance(2 * time.Hour)
	if f.do("GET", "/me", "", c).Code != 401 {
		t.Error("idle-expired session must be rejected")
	}
}
