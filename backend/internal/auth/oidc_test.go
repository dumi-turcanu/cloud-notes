package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth/oidctest"
)

const (
	appURL      = "http://app.test"
	callbackURL = appURL + "/api/auth/oidc/callback"
)

type oidcFixture struct {
	*fixture
	idp    *oidctest.Server
	client *OIDCClient
	h      http.Handler
	rec    *recorder
	secure bool
}

type oidcOption func(*oidcFixture, *OIDCSettings)

func newOIDCFixture(t *testing.T, secure bool, rate int, opts ...oidcOption) *oidcFixture {
	t.Helper()
	f := newFixture(t)
	idp := oidctest.New(t)
	o := &oidcFixture{fixture: f, idp: idp, rec: &recorder{}, secure: secure}
	settings := OIDCSettings{
		IssuerURL: idp.URL, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret,
		RedirectURL: callbackURL, Scopes: []string{"openid", "email", "profile"},
		PostLogoutRedirectURL: appURL + "/login",
	}
	for _, opt := range opts {
		opt(o, &settings)
	}
	o.client = NewOIDCClient(settings)
	hs := NewHandlers(f.svc, HandlerConfig{
		SecureCookie: secure, Recorder: o.rec, Now: f.clock.Now,
		Limiter: NewRateLimiter(rate, f.clock.Now), OIDC: o.client,
	})
	r := chi.NewRouter()
	r.Get("/oidc/login", hs.OIDCLogin)
	r.Get("/oidc/callback", hs.OIDCCallback)
	r.Post("/logout", hs.Logout)
	r.With(RequireSession(f.svc, secure)).Get("/me", hs.Me)
	o.h = r
	return o
}

func (o *oidcFixture) do(method, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "192.0.2.1:5555"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	o.h.ServeHTTP(rr, req)
	return rr
}

// startLogin calls /oidc/login and returns the provider URL and the state cookie.
func (o *oidcFixture) startLogin(t *testing.T, from string) (authURL string, state *http.Cookie) {
	t.Helper()
	target := "/oidc/login"
	if from != "" {
		target += "?from=" + url.QueryEscape(from)
	}
	rr := o.do("GET", target)
	if rr.Code != http.StatusFound {
		t.Fatalf("login: status %d: %s", rr.Code, rr.Body)
	}
	return rr.Header().Get("Location"), sessionCookie(t, rr, OIDCStateCookieName(o.secure))
}

// authorize plays the browser at the provider: it follows /authorize once and
// returns the callback path and query the provider redirected to.
func (o *oidcFixture) authorize(t *testing.T, authURL string) string {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize: status %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Scheme+"://"+loc.Host != appURL {
		t.Fatalf("provider redirected to %q", resp.Header.Get("Location"))
	}
	return strings.TrimPrefix(loc.RequestURI(), "/api/auth")
}

// login runs the whole flow and returns the callback response.
func (o *oidcFixture) login(t *testing.T, from string) *httptest.ResponseRecorder {
	t.Helper()
	authURL, state := o.startLogin(t, from)
	return o.do("GET", o.authorize(t, authURL), state)
}

func wantRedirect(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != want {
		t.Errorf("got %d Location=%q, want 302 %q", rr.Code, rr.Header().Get("Location"), want)
	}
}

func noSession(t *testing.T, rr *httptest.ResponseRecorder, o *oidcFixture) {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if c.Name == CookieName(o.secure) && c.MaxAge >= 0 {
			t.Error("a failed login must not set a session cookie")
		}
	}
	if o.sessions.Count() != 0 {
		t.Errorf("sessions = %d, a failed login must not create one", o.sessions.Count())
	}
}

func TestOIDCLoginRedirectsWithPKCEAndStateCookie(t *testing.T) {
	for _, secure := range []bool{true, false} {
		o := newOIDCFixture(t, secure, 100)
		authURL, state := o.startLogin(t, "/notes?x=1")

		u, err := url.Parse(authURL)
		if err != nil || u.Scheme+"://"+u.Host != o.idp.URL || u.Path != "/authorize" {
			t.Fatalf("redirect target %q", authURL)
		}
		q := u.Query()
		if q.Get("response_type") != "code" || q.Get("client_id") != o.idp.ClientID || q.Get("redirect_uri") != callbackURL ||
			q.Get("scope") != "openid email profile" || q.Get("code_challenge_method") != "S256" ||
			q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" {
			t.Errorf("authorization request incomplete: %v", q)
		}
		if q.Get("client_secret") != "" || strings.Contains(authURL, "verifier") {
			t.Error("secret or verifier leaked into the authorization URL")
		}

		wantName := map[bool]string{true: "__Host-cn_oidc", false: "cn_oidc"}[secure]
		if state.Name != wantName || !state.HttpOnly || state.Secure != secure || state.SameSite != http.SameSiteLaxMode ||
			state.Path != "/" || state.Domain != "" || state.MaxAge != 600 {
			t.Errorf("state cookie wrong: %+v", state)
		}
		st, ok := oidcStateFromRequest(func() *http.Request {
			r := httptest.NewRequest("GET", "/", nil)
			r.AddCookie(state)
			return r
		}(), secure)
		if !ok || st.State != q.Get("state") || st.Nonce != q.Get("nonce") || st.From != "/notes?x=1" || st.Verifier == "" {
			t.Errorf("cookie content does not match the request: %+v", st)
		}
	}
}

func TestOIDCLoginHappyPath(t *testing.T) {
	o := newOIDCFixture(t, true, 100)
	rr := o.login(t, "/trash")
	wantRedirect(t, rr, "/trash")

	c := sessionCookie(t, rr, "__Host-cn_session")
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Errorf("session cookie wrong: %+v", c)
	}
	if st := sessionCookie(t, rr, "__Host-cn_oidc"); st.MaxAge >= 0 {
		t.Errorf("state cookie must be cleared, got %+v", st)
	}
	me := o.do("GET", "/me", c)
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"email":"ann@example.com"`) || !strings.Contains(me.Body.String(), `"display_name":"Ann Example"`) {
		t.Errorf("/me: %d %s", me.Code, me.Body)
	}
	if got := strings.Join(o.rec.logins, ","); got != "oidc_success" {
		t.Errorf("recorder: %s", got)
	}

	// The token request used the PKCE verifier and the confidential client's secret.
	if len(o.idp.TokenRequests) != 1 || o.idp.TokenRequests[0].Get("code_verifier") == "" {
		t.Errorf("token request: %v", o.idp.TokenRequests)
	}

	// A second login of the same person reuses the account.
	second := o.login(t, "")
	wantRedirect(t, second, "/")
	again := o.do("GET", "/me", sessionCookie(t, second, "__Host-cn_session"))
	if again.Body.String() != me.Body.String() {
		t.Errorf("same identity must map to the same user:\n%s\n%s", me.Body, again.Body)
	}
}

func TestOIDCFromIsSanitised(t *testing.T) {
	tests := []struct{ name, from, want string }{
		{"path", "/notes", "/notes"},
		{"path with query", "/notes?tag=a%20b", "/notes?tag=a%20b"},
		{"empty", "", "/"},
		{"absolute url", "https://evil.example/", "/"},
		{"scheme relative", "//evil.example/", "/"},
		{"backslash", `/\evil.example`, "/"},
		{"no leading slash", "notes", "/"},
		{"newline", "/a\nSet-Cookie: x=1", "/"},
		{"tab", "/a\tb", "/"},
		{"nul", "/a\x00b", "/"},
		{"c1 control", "/a\u0085b", "/"},
		{"too long", "/" + strings.Repeat("a", 2048), "/"},
		{"longest allowed", "/" + strings.Repeat("a", 2047), "/" + strings.Repeat("a", 2047)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeRedirect(tc.from); got != tc.want {
				t.Errorf("SafeRedirect(%q) = %q, want %q", tc.from, got, tc.want)
			}
		})
	}

	// End to end: an attacker-controlled from never becomes the redirect target.
	o := newOIDCFixture(t, false, 100)
	wantRedirect(t, o.login(t, "//evil.example/phish"), "/")
}

func TestOIDCCallbackRejections(t *testing.T) {
	failed := "/login?error=oidc_failed"
	tests := []struct {
		name   string
		setup  func(o *oidcFixture)
		mutate func(callback string, state *http.Cookie) (string, *http.Cookie)
		want   string
	}{
		{"state mismatch", nil, func(cb string, st *http.Cookie) (string, *http.Cookie) {
			u, _ := url.Parse(cb)
			q := u.Query()
			q.Set("state", "attacker-state")
			return u.Path + "?" + q.Encode(), st
		}, failed},
		{"state parameter missing", nil, func(cb string, st *http.Cookie) (string, *http.Cookie) {
			u, _ := url.Parse(cb)
			q := u.Query()
			q.Del("state")
			return u.Path + "?" + q.Encode(), st
		}, failed},
		{"state cookie missing", nil, func(cb string, _ *http.Cookie) (string, *http.Cookie) { return cb, nil }, failed},
		{"state cookie garbage", nil, func(cb string, st *http.Cookie) (string, *http.Cookie) {
			bad := *st
			bad.Value = "%%%not-base64%%%"
			return cb, &bad
		}, failed},
		{"state cookie not json", nil, func(cb string, st *http.Cookie) (string, *http.Cookie) {
			bad := *st
			bad.Value = "bm90IGpzb24"
			return cb, &bad
		}, failed},
		{"code missing", nil, func(cb string, st *http.Cookie) (string, *http.Cookie) {
			u, _ := url.Parse(cb)
			q := u.Query()
			q.Del("code")
			return u.Path + "?" + q.Encode(), st
		}, failed},
		{"code not issued", nil, func(cb string, st *http.Cookie) (string, *http.Cookie) {
			u, _ := url.Parse(cb)
			q := u.Query()
			q.Set("code", "forged")
			return u.Path + "?" + q.Encode(), st
		}, failed},
		{"nonce mismatch", func(o *oidcFixture) {
			o.idp.Claims = func(string) map[string]any {
				c := o.idp.DefaultClaims("another-nonce")
				return c
			}
		}, nil, failed},
		{"nonce missing", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any { c := o.idp.DefaultClaims(n); delete(c, "nonce"); return c }
		}, nil, failed},
		{"wrong audience", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any { c := o.idp.DefaultClaims(n); c["aud"] = "someone-else"; return c }
		}, nil, failed},
		{"expired", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any {
				c := o.idp.DefaultClaims(n)
				c["exp"] = time.Now().Add(-time.Minute).Unix()
				return c
			}
		}, nil, failed},
		{"wrong issuer", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any {
				c := o.idp.DefaultClaims(n)
				c["iss"] = "https://evil.example"
				return c
			}
		}, nil, failed},
		{"bad signature", func(o *oidcFixture) { o.idp.TokenSigner = oidctest.NewSigner(t) }, nil, failed},
		{"subject missing", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any { c := o.idp.DefaultClaims(n); delete(c, "sub"); return c }
		}, nil, failed},
		{"user denied at the provider", func(o *oidcFixture) { o.idp.Deny = true }, nil, "/login?error=oidc_cancelled"},
		{"email missing", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any { c := o.idp.DefaultClaims(n); delete(c, "email"); return c }
		}, nil, "/login?error=email_claim_missing"},
		{"email not an address", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any { c := o.idp.DefaultClaims(n); c["email"] = "nope"; return c }
		}, nil, "/login?error=email_claim_missing"},
		{"email unverified", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any { c := o.idp.DefaultClaims(n); c["email_verified"] = false; return c }
		}, nil, "/login?error=email_claim_missing"},
		{"email unverified as string", func(o *oidcFixture) {
			o.idp.Claims = func(n string) map[string]any { c := o.idp.DefaultClaims(n); c["email_verified"] = "false"; return c }
		}, nil, "/login?error=email_claim_missing"},
		{"account conflict", func(o *oidcFixture) { o.register(t, "ann@example.com") }, nil, "/login?error=account_conflict"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := newOIDCFixture(t, false, 100)
			if tc.setup != nil {
				tc.setup(o)
			}
			before := o.sessions.Count()
			authURL, state := o.startLogin(t, "/secret")
			callback := o.authorize(t, authURL)
			if tc.mutate != nil {
				callback, state = tc.mutate(callback, state)
			}
			var cookies []*http.Cookie
			if state != nil {
				cookies = append(cookies, state)
			}
			rr := o.do("GET", callback, cookies...)
			wantRedirect(t, rr, tc.want)
			if o.sessions.Count() != before {
				t.Error("a failed login must not create a session")
			}
			for _, c := range rr.Result().Cookies() {
				if c.Name == "cn_session" && c.MaxAge >= 0 {
					t.Error("a failed login must not set a session cookie")
				}
			}
			if st := sessionCookie(t, rr, "cn_oidc"); st.MaxAge >= 0 {
				t.Error("the state cookie must be cleared on every outcome")
			}
		})
	}
}

func TestOIDCEmailFallsBackToPreferredUsername(t *testing.T) {
	tests := []struct {
		name   string
		claims func(c map[string]any)
		want   string // email of the created user; "" means rejected
	}{
		{"user name is an address", func(c map[string]any) { delete(c, "email"); c["preferred_username"] = "Upn@Example.com" }, "upn@example.com"},
		{"user name is not an address", func(c map[string]any) { delete(c, "email"); c["preferred_username"] = "ann" }, ""},
		{"email wins over user name", func(c map[string]any) { c["preferred_username"] = "other@example.com" }, "ann@example.com"},
		{"unverified email does not fall back", func(c map[string]any) {
			c["email_verified"] = false
			c["preferred_username"] = "other@example.com"
		}, ""},
		{"given and family name", func(c map[string]any) {
			delete(c, "name")
			c["given_name"], c["family_name"] = "Ann", "Example"
		}, "ann@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := newOIDCFixture(t, false, 100)
			o.idp.Claims = func(n string) map[string]any { c := o.idp.DefaultClaims(n); tc.claims(c); return c }
			rr := o.login(t, "")
			if tc.want == "" {
				wantRedirect(t, rr, "/login?error=email_claim_missing")
				return
			}
			me := o.do("GET", "/me", sessionCookie(t, rr, "cn_session"))
			if !strings.Contains(me.Body.String(), `"email":"`+tc.want+`"`) {
				t.Errorf("/me: %s", me.Body)
			}
		})
	}
}

func TestOIDCCallbackReplayIsRejected(t *testing.T) {
	o := newOIDCFixture(t, false, 100)
	authURL, state := o.startLogin(t, "")
	callback := o.authorize(t, authURL)
	wantRedirect(t, o.do("GET", callback, state), "/")
	// The same code again: the provider has burned it.
	wantRedirect(t, o.do("GET", callback, state), "/login?error=oidc_failed")
	if o.sessions.Count() != 1 {
		t.Errorf("sessions = %d, want 1", o.sessions.Count())
	}
}

func TestOIDCCallbackIsRateLimited(t *testing.T) {
	o := newOIDCFixture(t, false, 2)
	for range 2 {
		o.do("GET", "/oidc/callback?error=access_denied")
	}
	rr := o.do("GET", "/oidc/callback?error=access_denied")
	if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" {
		t.Errorf("got %d, want 429 with Retry-After", rr.Code)
	}
	if o.rec.logins[len(o.rec.logins)-1] != "rate_limited" {
		t.Errorf("recorder: %v", o.rec.logins)
	}
}

func TestOIDCDiscoveryIsLazyCachedAndRetried(t *testing.T) {
	o := newOIDCFixture(t, false, 100)
	if o.idp.DiscoveryRequests() != 0 {
		t.Fatal("constructing the client must not contact the provider")
	}
	o.idp.FailDiscovery(1)

	rr := o.do("GET", "/oidc/login")
	wantRedirect(t, rr, "/login?error=oidc_failed")
	for _, c := range rr.Result().Cookies() {
		if c.Name == "cn_oidc" && c.MaxAge > 0 {
			t.Error("no state cookie may be set when the provider is unreachable")
		}
	}

	// The failure was not cached: the next attempt discovers and succeeds.
	if rr := o.do("GET", "/oidc/login"); rr.Code != http.StatusFound || !strings.HasPrefix(rr.Header().Get("Location"), o.idp.URL) {
		t.Fatalf("retry: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if got := o.idp.DiscoveryRequests(); got != 2 {
		t.Errorf("discovery requests = %d, want 2", got)
	}
	// Success is cached, and a provider outage no longer matters for sessions.
	o.idp.FailDiscovery(100)
	o.login(t, "")
	if got := o.idp.DiscoveryRequests(); got != 2 {
		t.Errorf("discovery requests = %d after caching, want 2", got)
	}
	if got := strings.Join(o.rec.logins, ","); got != "oidc_failed,oidc_success" {
		t.Errorf("recorder: %s", got)
	}
}

func TestOIDCDiscoveryURLKeepsCheckingThePublicIssuer(t *testing.T) {
	const public = "https://login.example.com/realms/notes"
	o := newOIDCFixture(t, false, 100, func(o *oidcFixture, s *OIDCSettings) {
		o.idp.Issuer = public // what the provider says about itself
		s.IssuerURL, s.DiscoveryURL = public, o.idp.URL
	})
	wantRedirect(t, o.login(t, "/x"), "/x")

	o2 := newOIDCFixture(t, false, 100, func(o *oidcFixture, s *OIDCSettings) {
		o.idp.Issuer = public
		s.IssuerURL, s.DiscoveryURL = public, o.idp.URL
	})
	o2.idp.Claims = func(n string) map[string]any {
		c := o2.idp.DefaultClaims(n)
		c["iss"] = "https://evil.example"
		return c
	}
	wantRedirect(t, o2.login(t, ""), "/login?error=oidc_failed")
}

func TestOIDCIssuerMismatchInDiscoveryFails(t *testing.T) {
	o := newOIDCFixture(t, false, 100, func(o *oidcFixture, s *OIDCSettings) { o.idp.Issuer = "https://other.example" })
	wantRedirect(t, o.do("GET", "/oidc/login"), "/login?error=oidc_failed")
}

func TestOIDCLogoutURL(t *testing.T) {
	const template = "https://auth.example.com/logout?client_id={client_id}&logout_uri={post_logout_redirect_uri}"
	tests := []struct {
		name  string
		opt   oidcOption
		check func(t *testing.T, rr *httptest.ResponseRecorder)
	}{
		{"template", func(o *oidcFixture, s *OIDCSettings) { s.LogoutURL = template }, func(t *testing.T, rr *httptest.ResponseRecorder) {
			want := "https://auth.example.com/logout?client_id=cloud-notes&logout_uri=http%3A%2F%2Fapp.test%2Flogin"
			if got := logoutURL(t, rr); rr.Code != 200 || got != want {
				t.Errorf("got %d %q, want %q", rr.Code, got, want)
			}
		}},
		{"template needs no discovery", func(o *oidcFixture, s *OIDCSettings) { s.LogoutURL = template; o.idp.FailDiscovery(100) }, func(t *testing.T, rr *httptest.ResponseRecorder) {
			if rr.Code != 200 {
				t.Errorf("got %d %s", rr.Code, rr.Body)
			}
		}},
		{"placeholder values are URL-encoded", func(o *oidcFixture, s *OIDCSettings) {
			s.LogoutURL = template
			s.ClientID = "a b&c=d"
			s.PostLogoutRedirectURL = "https://app.example/login?x=1&y=2"
		}, func(t *testing.T, rr *httptest.ResponseRecorder) {
			want := "https://auth.example.com/logout?client_id=a+b%26c%3Dd&logout_uri=https%3A%2F%2Fapp.example%2Flogin%3Fx%3D1%26y%3D2"
			if got := logoutURL(t, rr); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		}},
		{"discovered end_session_endpoint", func(o *oidcFixture, s *OIDCSettings) { o.idp.EndSession = true }, func(t *testing.T, rr *httptest.ResponseRecorder) {
			got := logoutURL(t, rr)
			u, err := url.Parse(got)
			if err != nil || u.Path != "/logout" || u.Query().Get("client_id") != "cloud-notes" ||
				u.Query().Get("post_logout_redirect_uri") != appURL+"/login" {
				t.Errorf("logout url %q (%v)", got, err)
			}
		}},
		{"no logout URL at all", nil, func(t *testing.T, rr *httptest.ResponseRecorder) {
			if rr.Code != http.StatusNoContent || rr.Body.Len() != 0 {
				t.Errorf("got %d %q, want a plain 204", rr.Code, rr.Body)
			}
		}},
		{"discovery down and no template", func(o *oidcFixture, s *OIDCSettings) { o.idp.EndSession = true; o.idp.FailDiscovery(100) }, func(t *testing.T, rr *httptest.ResponseRecorder) {
			if rr.Code != http.StatusNoContent {
				t.Errorf("got %d, want 204", rr.Code)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var opts []oidcOption
			if tc.opt != nil {
				opts = append(opts, tc.opt)
			}
			o := newOIDCFixture(t, false, 100, opts...)
			session := o.register(t, "bob@example.com")
			rr := o.do("POST", "/logout", &http.Cookie{Name: "cn_session", Value: session.Token})

			tc.check(t, rr)
			if o.sessions.Count() != 0 {
				t.Error("the local session must be revoked")
			}
			if c := sessionCookie(t, rr, "cn_session"); c.MaxAge >= 0 {
				t.Error("the session cookie must be cleared")
			}
		})
	}
}

func TestOIDCFailuresNeverLogOrExposeSecrets(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	o := newOIDCFixture(t, false, 100)
	var idToken string
	o.idp.Claims = func(n string) map[string]any {
		c := o.idp.DefaultClaims(n)
		c["aud"] = "someone-else" // verification fails after the exchange succeeded
		idToken = o.idp.Signer.Sign(c)
		return c
	}
	authURL, state := o.startLogin(t, "")
	callback := o.authorize(t, authURL)
	code := mustQuery(t, callback, "code")
	rr := o.do("GET", callback, state)

	wantRedirect(t, rr, "/login?error=oidc_failed")
	out := logs.String() + rr.Header().Get("Location") + rr.Body.String()
	for name, secret := range map[string]string{
		"authorization code": code, "client secret": o.idp.ClientSecret, "id token": idToken,
		"state": mustQuery(t, authURL, "state"), "nonce": mustQuery(t, authURL, "nonce"),
	} {
		if secret == "" || strings.Contains(out, secret) {
			t.Errorf("%s leaked (or was empty) in log/response: %q", name, out)
		}
	}
	if !strings.Contains(logs.String(), "id_token_invalid") {
		t.Errorf("the failure reason should be logged, got %q", logs.String())
	}
}

func TestOIDCCompleteReportsReasons(t *testing.T) {
	o := newOIDCFixture(t, false, 100)
	o.idp.Claims = func(n string) map[string]any {
		c := o.idp.DefaultClaims(n)
		c["exp"] = time.Now().Add(-time.Hour).Unix()
		return c
	}
	authURL, _ := o.startLogin(t, "")
	callback := o.authorize(t, authURL)
	_, err := o.client.Complete(context.Background(), mustQuery(t, callback, "code"), "wrong-verifier", mustQuery(t, authURL, "nonce"))
	if oerr, ok := err.(*OIDCError); !ok || oerr.Reason != "code_exchange_failed" {
		t.Errorf("wrong verifier: got %v", err)
	}
}

func logoutURL(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		LogoutURL string `json:"logout_url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rr.Body, err)
	}
	return body.LogoutURL
}

func mustQuery(t *testing.T, rawURL, key string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}
