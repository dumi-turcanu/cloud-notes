package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth/authtest"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/httpx"
)

const password = "a-very-good-password"

type env struct {
	t     *testing.T
	h     http.Handler
	clock *authtest.Clock
	notes *fakeNotes
	cfg   config.Config
}

func newEnv(t *testing.T, mutate ...func(*config.Config)) *env {
	t.Helper()
	cfg := config.Config{
		APIBasePath: "/api", SignupMode: config.SignupOpen, SessionTTL: 12 * time.Hour,
		SessionIdleTimeout: time.Hour, SessionCookieSecure: false, MaxFailedLogins: 5,
		LockoutDuration: 15 * time.Minute, RateLimitPerMinute: 1000,
		PublicAppURL: "https://notes.example.com",
	}
	for _, m := range mutate {
		m(&cfg)
	}
	clock := authtest.NewClock()
	users := authtest.NewUsers(clock)
	svc, err := auth.NewService(users, authtest.NewSessions(users), auth.NewHasher(), auth.OptionsFromConfig(cfg), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	notes := newFakeNotes(clock.Now)
	h, err := NewRouter(Deps{Config: cfg, Version: "v-test", DB: fakePinger{}, Notes: notes, Auth: svc, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return &env{t, h, clock, notes, cfg}
}

type client struct {
	e      *env
	cookie *http.Cookie
}

// newClient registers a fresh user and returns a client carrying its session.
func (e *env) newClient(email string) *client {
	e.t.Helper()
	c := &client{e: e}
	rr := c.do("POST", "/api/auth/register", mustJSON(map[string]string{"email": email, "password": password}))
	if rr.Code != http.StatusCreated {
		e.t.Fatalf("register %s: %d %s", email, rr.Code, rr.Body)
	}
	c.cookie = rr.Result().Cookies()[0]
	return c
}

func (c *client) do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.1:1"
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rr := httptest.NewRecorder()
	c.e.h.ServeHTTP(rr, req)
	return rr
}

func decode[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rr.Body.String(), err)
	}
	return v
}

func wantError(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status %d, want %d (%s)", rr.Code, status, rr.Body)
	}
	if got := decode[httpx.ErrorBody](t, rr).Error; got != code {
		t.Fatalf("error code %q, want %q", got, code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type %q", ct)
	}
}

type noteJSON struct {
	ID        uuid.UUID       `json:"id"`
	UserID    uuid.UUID       `json:"user_id"`
	Title     string          `json:"title"`
	Content   json.RawMessage `json:"content"`
	IsPublic  bool            `json:"is_public"`
	ShareUUID uuid.UUID       `json:"share_uuid"`
	ShareURL  string          `json:"share_url"`
	DeletedAt *time.Time      `json:"deleted_at"`
}

func (c *client) createNote(title string) noteJSON {
	c.e.t.Helper()
	rr := c.do("POST", "/api/notes", mustJSON(map[string]any{"title": title, "content": map[string]string{"body": "text"}}))
	if rr.Code != http.StatusCreated {
		c.e.t.Fatalf("create: %d %s", rr.Code, rr.Body)
	}
	return decode[noteJSON](c.e.t, rr)
}

func TestInfraEndpoints(t *testing.T) {
	e := newEnv(t)
	c := &client{e: e}
	for path, body := range map[string]string{"/healthz": `"ok"`, "/readyz": `"ready"`} {
		rr := c.do("GET", path, "")
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), body) {
			t.Errorf("%s: %d %s", path, rr.Code, rr.Body)
		}
	}
	h, _ := NewRouter(Deps{Config: e.cfg, DB: fakePinger{err: errBoom}, Notes: e.notes, Auth: nil2(t, e)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
	if rr.Code != 503 {
		t.Errorf("readyz with failing DB: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code != 200 {
		t.Errorf("healthz must not depend on DB: %d", rr.Code)
	}
}

func nil2(t *testing.T, e *env) auth.Flow {
	t.Helper()
	users := authtest.NewUsers(e.clock)
	svc, err := auth.NewService(users, authtest.NewSessions(users), auth.NewHasher(), auth.OptionsFromConfig(e.cfg), e.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestPublicConfig(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SignupMode = config.SignupInvite; c.PublicSharing = true })
	rr := (&client{e: e}).do("GET", "/api/config", "")
	got := decode[struct {
		SignupMode string `json:"signup_mode"`
		Features   struct {
			PublicSharing bool `json:"public_sharing"`
		} `json:"features"`
		Version string `json:"version"`
	}](t, rr)
	if rr.Code != 200 || got.SignupMode != "invite" || !got.Features.PublicSharing || got.Version != "v-test" {
		t.Errorf("%d %+v", rr.Code, got)
	}
}

func TestCustomAPIBasePath(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.APIBasePath = "/v1/notes-api" })
	c := &client{e: e}
	if rr := c.do("GET", "/v1/notes-api/config", ""); rr.Code != 200 {
		t.Errorf("config under custom base: %d", rr.Code)
	}
	if rr := c.do("GET", "/api/config", ""); rr.Code != 404 {
		t.Errorf("/api must not exist under a custom base: %d", rr.Code)
	}
}

func TestUnknownAPIRoutesReturnJSON(t *testing.T) {
	c := &client{e: newEnv(t)}
	wantError(t, c.do("GET", "/api/nope", ""), 404, "not_found")
	wantError(t, c.do("GET", "/api/auth/nope", ""), 404, "not_found")
	wantError(t, c.do("PATCH", "/api/config", ""), 405, "method_not_allowed")
	wantError(t, c.do("GET", "/api/auth/login", ""), 405, "method_not_allowed")
}

func TestNotesRequireSession(t *testing.T) {
	c := &client{e: newEnv(t)}
	for _, tc := range [][2]string{
		{"GET", "/api/notes"}, {"POST", "/api/notes"}, {"GET", "/api/notes/trash"},
		{"GET", "/api/notes/" + uuid.NewString()}, {"PUT", "/api/notes/" + uuid.NewString()},
		{"DELETE", "/api/notes/" + uuid.NewString()}, {"POST", "/api/notes/" + uuid.NewString() + "/restore"},
		{"GET", "/api/auth/me"},
	} {
		rr := c.do(tc[0], tc[1], `{"title":"x"}`)
		wantError(t, rr, 401, "unauthorized")
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s missing no-store", tc[0], tc[1])
		}
	}
}

func TestNoteLifecycle(t *testing.T) {
	e := newEnv(t)
	c := e.newClient("ann@example.com")

	n := c.createNote("First")
	if n.Title != "First" || n.IsPublic || n.UserID == uuid.Nil || string(n.Content) != `{"body":"text"}` {
		t.Errorf("created: %+v", n)
	}

	e.clock.Advance(time.Minute)
	n2 := c.createNote("Second")
	list := decode[[]noteJSON](t, c.do("GET", "/api/notes", ""))
	if len(list) != 2 || list[0].ID != n2.ID {
		t.Errorf("list must be newest first: %+v", list)
	}

	rr := c.do("PUT", "/api/notes/"+n.ID.String(), `{"title":"Renamed","content":{"body":"new"}}`)
	if rr.Code != 200 || decode[noteJSON](t, rr).Title != "Renamed" {
		t.Errorf("update: %d %s", rr.Code, rr.Body)
	}
	if rr := c.do("GET", "/api/notes/"+n.ID.String(), ""); rr.Code != 200 {
		t.Errorf("get: %d", rr.Code)
	}

	if rr := c.do("DELETE", "/api/notes/"+n.ID.String(), ""); rr.Code != 204 {
		t.Fatalf("delete: %d", rr.Code)
	}
	wantError(t, c.do("GET", "/api/notes/"+n.ID.String(), ""), 404, "not_found")
	wantError(t, c.do("DELETE", "/api/notes/"+n.ID.String(), ""), 404, "not_found")
	if l := decode[[]noteJSON](t, c.do("GET", "/api/notes", "")); len(l) != 1 {
		t.Errorf("active list after delete: %d", len(l))
	}
	trash := decode[[]noteJSON](t, c.do("GET", "/api/notes/trash", ""))
	if len(trash) != 1 || trash[0].ID != n.ID || trash[0].DeletedAt == nil {
		t.Errorf("trash: %+v", trash)
	}

	wantError(t, c.do("POST", "/api/notes/"+n2.ID.String()+"/restore", ""), 404, "not_found") // not in trash
	rr = c.do("POST", "/api/notes/"+n.ID.String()+"/restore", "")
	if rr.Code != 200 || decode[noteJSON](t, rr).DeletedAt != nil {
		t.Errorf("restore: %d %s", rr.Code, rr.Body)
	}
	if tr := decode[[]noteJSON](t, c.do("GET", "/api/notes/trash", "")); len(tr) != 0 {
		t.Errorf("trash must be empty (and a JSON array): %+v", tr)
	}
	if body := c.do("GET", "/api/notes/trash", "").Body.String(); strings.TrimSpace(body) != "[]" {
		t.Errorf("empty trash must serialise as []: %q", body)
	}
}

func TestListSince(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.SessionIdleTimeout = 24 * time.Hour })
	c := e.newClient("ann@example.com")
	c.createNote("old")
	e.clock.Advance(time.Hour)
	cut := e.clock.Now()
	e.clock.Advance(time.Hour)
	c.createNote("new")

	q := "/api/notes?since=" + strings.ReplaceAll(cut.Format(time.RFC3339), "+", "%2B")
	list := decode[[]noteJSON](t, c.do("GET", q, ""))
	if len(list) != 1 || list[0].Title != "new" {
		t.Errorf("since filter: %+v", list)
	}
	wantError(t, c.do("GET", "/api/notes?since=yesterday", ""), 400, "invalid_request")
}

func TestNoteIsolationBetweenUsers(t *testing.T) {
	e := newEnv(t)
	ann, bob := e.newClient("ann@example.com"), e.newClient("bob@example.com")
	n := ann.createNote("secret")
	id := n.ID.String()

	wantError(t, bob.do("GET", "/api/notes/"+id, ""), 404, "not_found")
	wantError(t, bob.do("PUT", "/api/notes/"+id, `{"title":"hacked"}`), 404, "not_found")
	wantError(t, bob.do("DELETE", "/api/notes/"+id, ""), 404, "not_found")
	if l := decode[[]noteJSON](t, bob.do("GET", "/api/notes", "")); len(l) != 0 {
		t.Errorf("bob sees ann's notes: %+v", l)
	}
	ann.do("DELETE", "/api/notes/"+id, "")
	wantError(t, bob.do("POST", "/api/notes/"+id+"/restore", ""), 404, "not_found")
	if l := decode[[]noteJSON](t, bob.do("GET", "/api/notes/trash", "")); len(l) != 0 {
		t.Errorf("bob sees ann's trash: %+v", l)
	}
}

func TestNoteValidation(t *testing.T) {
	e := newEnv(t)
	c := e.newClient("ann@example.com")
	long := strings.Repeat("é", 256)
	okLong := strings.Repeat("é", 255) // 255 runes, 510 bytes: must pass

	tests := []struct {
		name, body string
		status     int
		code       string
	}{
		{"missing title", `{"content":{}}`, 400, "validation_failed"},
		{"empty title", `{"title":""}`, 400, "validation_failed"},
		{"blank title", `{"title":"   "}`, 400, "validation_failed"},
		{"title 256 runes", `{"title":"` + long + `"}`, 400, "validation_failed"},
		{"title 255 runes", `{"title":"` + okLong + `"}`, 201, ""},
		{"content array", `{"title":"t","content":[1]}`, 400, "validation_failed"},
		{"content string", `{"title":"t","content":"x"}`, 400, "validation_failed"},
		{"content number", `{"title":"t","content":5}`, 400, "validation_failed"},
		{"content null defaults", `{"title":"t","content":null}`, 201, ""},
		{"content missing defaults", `{"title":"t"}`, 201, ""},
		{"unknown field", `{"title":"t","is_public":true}`, 400, "invalid_request"},
		{"malformed", `{"title":`, 400, "invalid_request"},
		{"empty body", ``, 400, "invalid_request"},
		{"too large", `{"title":"t","content":{"body":"` + strings.Repeat("a", 300<<10) + `"}}`, 413, "payload_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := c.do("POST", "/api/notes", tc.body)
			if tc.code == "" {
				if rr.Code != tc.status {
					t.Fatalf("status %d: %s", rr.Code, rr.Body)
				}
				if got := decode[noteJSON](t, rr); len(got.Content) == 0 || got.Content[0] != '{' {
					t.Errorf("content must be an object, got %s", got.Content)
				}
				return
			}
			wantError(t, rr, tc.status, tc.code)
		})
	}

	n := c.createNote("x")
	wantError(t, c.do("PUT", "/api/notes/"+n.ID.String(), `{"title":"","content":{}}`), 400, "validation_failed")
	wantError(t, c.do("GET", "/api/notes/not-a-uuid", ""), 400, "invalid_request")
}

func TestValidationErrorListsFields(t *testing.T) {
	c := newEnv(t).newClient("ann@example.com")
	rr := c.do("POST", "/api/notes", `{"title":"","content":[]}`)
	e := decode[httpx.ErrorBody](t, rr)
	if e.Fields["title"] == "" || e.Fields["content"] == "" {
		t.Errorf("fields: %+v", e.Fields)
	}
}

func TestServerErrorsDoNotLeakDetails(t *testing.T) {
	e := newEnv(t)
	c := e.newClient("ann@example.com")
	e.notes.err = errBoom
	rr := c.do("GET", "/api/notes", "")
	wantError(t, rr, 500, "internal_error")
	if strings.Contains(rr.Body.String(), "boom") {
		t.Error("internal error text leaked to client")
	}
}

func TestCSRFRejectsCrossSiteMutations(t *testing.T) {
	e := newEnv(t)
	c := e.newClient("ann@example.com")

	rr := c.do("POST", "/api/notes", `{"title":"x"}`, "Sec-Fetch-Site", "cross-site")
	wantError(t, rr, 403, "forbidden")
	rr = c.do("POST", "/api/auth/login", `{"email":"a@b.co","password":"x"}`, "Sec-Fetch-Site", "cross-site")
	wantError(t, rr, 403, "forbidden")
	wantError(t, c.do("POST", "/api/notes", `{"title":"x"}`, "Origin", "https://evil.example"), 403, "forbidden")

	if rr := c.do("POST", "/api/notes", `{"title":"x"}`, "Sec-Fetch-Site", "same-origin"); rr.Code != 201 {
		t.Errorf("same-origin: %d", rr.Code)
	}
	if rr := c.do("GET", "/api/notes", "", "Sec-Fetch-Site", "cross-site"); rr.Code != 200 {
		t.Errorf("safe methods are allowed: %d", rr.Code)
	}
}

func TestCSRFTrustedOrigins(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.CSRFTrustedOrigins = []string{"https://notes.example.com"} })
	c := e.newClient("ann@example.com")
	if rr := c.do("POST", "/api/notes", `{"title":"x"}`, "Origin", "https://notes.example.com"); rr.Code != 201 {
		t.Errorf("trusted origin rejected: %d %s", rr.Code, rr.Body)
	}
	if _, err := NewRouter(Deps{Config: config.Config{APIBasePath: "/api", CSRFTrustedOrigins: []string{"not-an-origin"}, RateLimitPerMinute: 1}, Auth: nil2(t, e)}); err == nil {
		t.Error("invalid trusted origin must fail router construction")
	}
}

func TestSharingDisabledByDefault(t *testing.T) {
	e := newEnv(t)
	c := e.newClient("ann@example.com")
	n := c.createNote("x")
	wantError(t, c.do("POST", "/api/notes/"+n.ID.String()+"/publish", ""), 404, "not_found")
	wantError(t, c.do("DELETE", "/api/notes/"+n.ID.String()+"/publish", ""), 404, "not_found")
	wantError(t, (&client{e: e}).do("GET", "/api/public/notes/"+uuid.NewString(), ""), 404, "not_found")
}

func TestSharingFlow(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.PublicSharing = true })
	ann := e.newClient("ann@example.com")
	anon := &client{e: e}
	n := ann.createNote("Shared title")

	rr := ann.do("POST", "/api/notes/"+n.ID.String()+"/publish", "")
	pub := decode[noteJSON](t, rr)
	if rr.Code != 200 || !pub.IsPublic || pub.ShareURL != "https://notes.example.com/shared/"+n.ShareUUID.String() {
		t.Fatalf("publish: %d %s", rr.Code, rr.Body)
	}

	rr = anon.do("GET", "/api/public/notes/"+n.ShareUUID.String(), "")
	if rr.Code != 200 {
		t.Fatalf("public get: %d %s", rr.Code, rr.Body)
	}
	raw := decode[map[string]any](t, rr)
	if len(raw) != 3 || raw["title"] != "Shared title" || raw["content"] == nil || raw["updated_at"] == nil {
		t.Errorf("public shape: %v", raw)
	}
	for _, leak := range []string{n.ID.String(), n.UserID.String(), "user_id", "share_uuid", "is_public"} {
		if strings.Contains(rr.Body.String(), leak) {
			t.Errorf("public response leaks %q: %s", leak, rr.Body)
		}
	}
	wantError(t, anon.do("GET", "/api/public/notes/not-a-uuid", ""), 404, "not_found")

	bob := e.newClient("bob@example.com")
	wantError(t, bob.do("POST", "/api/notes/"+n.ID.String()+"/publish", ""), 404, "not_found")
	wantError(t, bob.do("DELETE", "/api/notes/"+n.ID.String()+"/publish", ""), 404, "not_found")

	if rr := ann.do("DELETE", "/api/notes/"+n.ID.String()+"/publish", ""); rr.Code != 204 {
		t.Fatalf("unpublish: %d", rr.Code)
	}
	wantError(t, anon.do("GET", "/api/public/notes/"+n.ShareUUID.String(), ""), 404, "not_found")

	ann.do("POST", "/api/notes/"+n.ID.String()+"/publish", "")
	ann.do("DELETE", "/api/notes/"+n.ID.String(), "")
	wantError(t, anon.do("GET", "/api/public/notes/"+n.ShareUUID.String(), ""), 404, "not_found") // trashed
}

func TestAuthResponsesAreNoStore(t *testing.T) {
	e := newEnv(t)
	c := &client{e: e}
	for _, p := range []string{"/api/auth/register", "/api/auth/login", "/api/auth/logout"} {
		rr := c.do("POST", p, `{}`)
		if rr.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control = %q", p, rr.Header().Get("Cache-Control"))
		}
	}
}

func TestLoginRateLimitedPerClientIP(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.RateLimitPerMinute = 2; c.TrustedProxyHops = 1 })
	c := &client{e: e}
	body := `{"email":"x@example.com","password":"wrong-password-x"}`
	for i := 0; i < 2; i++ {
		c.do("POST", "/api/auth/login", body, "X-Forwarded-For", "198.51.100.7")
	}
	wantError(t, c.do("POST", "/api/auth/login", body, "X-Forwarded-For", "198.51.100.7"), 429, "too_many_requests")
	// A different client behind the same proxy has its own bucket; a spoofed
	// left-most entry must not help the first client either.
	wantError(t, c.do("POST", "/api/auth/login", body, "X-Forwarded-For", "1.1.1.1, 198.51.100.7"), 429, "too_many_requests")
	if rr := c.do("POST", "/api/auth/login", body, "X-Forwarded-For", "198.51.100.8"); rr.Code != 401 {
		t.Errorf("other IP must not be limited: %d", rr.Code)
	}
}

func TestPublicConfigAuthSection(t *testing.T) {
	type authBody struct {
		Auth struct {
			Provider         string `json:"provider"`
			DisplayName      string `json:"display_name"`
			IdentityPlatform *struct {
				APIKey        string   `json:"api_key"`
				AuthDomain    string   `json:"auth_domain"`
				ProjectID     string   `json:"project_id"`
				TenantID      string   `json:"tenant_id"`
				SignInMethods []string `json:"sign_in_methods"`
			} `json:"identity_platform"`
		} `json:"auth"`
		SignupMode string `json:"signup_mode"`
	}
	tests := []struct {
		name   string
		mutate func(c *config.Config)
		check  func(t *testing.T, b authBody)
	}{
		{"unset provider means local", func(c *config.Config) {}, func(t *testing.T, b authBody) {
			if b.Auth.Provider != "local" || b.Auth.DisplayName != "" || b.Auth.IdentityPlatform != nil {
				t.Errorf("%+v", b.Auth)
			}
		}},
		{"local", func(c *config.Config) { c.AuthProvider = config.AuthLocal }, func(t *testing.T, b authBody) {
			if b.Auth.Provider != "local" || b.SignupMode != "open" {
				t.Errorf("%+v", b)
			}
		}},
		{"oidc", func(c *config.Config) {
			c.AuthProvider, c.OIDCProviderName = config.AuthOIDC, "Acme SSO"
			c.OIDCClientSecret = "must-not-leak"
		}, func(t *testing.T, b authBody) {
			if b.Auth.Provider != "oidc" || b.Auth.DisplayName != "Acme SSO" || b.Auth.IdentityPlatform != nil {
				t.Errorf("%+v", b.Auth)
			}
		}},
		{"identity platform", func(c *config.Config) {
			c.AuthProvider = config.AuthIdentityPlatform
			c.IDPAPIKey, c.IDPAuthDomain, c.IDPProjectID = "key", "p.firebaseapp.com", "p"
			c.IDPTenantID, c.IDPSignInMethods = "t-1", []string{"password", "google"}
		}, func(t *testing.T, b authBody) {
			ip := b.Auth.IdentityPlatform
			if b.Auth.Provider != "identity-platform" || ip == nil || ip.APIKey != "key" || ip.AuthDomain != "p.firebaseapp.com" ||
				ip.ProjectID != "p" || ip.TenantID != "t-1" || len(ip.SignInMethods) != 2 {
				t.Errorf("%+v %+v", b.Auth, ip)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, tc.mutate)
			rr := (&client{e: e}).do("GET", "/api/config", "")
			if rr.Code != 200 {
				t.Fatalf("status %d", rr.Code)
			}
			if strings.Contains(rr.Body.String(), "must-not-leak") {
				t.Error("config leaks the client secret")
			}
			tc.check(t, decode[authBody](t, rr))
		})
	}
}

func TestPasswordEndpointsAreNotMountedWithExternalProvider(t *testing.T) {
	for _, provider := range []string{config.AuthOIDC, config.AuthIdentityPlatform} {
		t.Run(provider, func(t *testing.T) {
			e := newEnv(t, func(c *config.Config) { c.AuthProvider = provider })
			c := &client{e: e}
			for _, p := range []string{"/api/auth/register", "/api/auth/login"} {
				wantError(t, c.do("POST", p, `{"email":"a@example.com","password":"a-very-good-password"}`), 404, "not_found")
			}
			// Session endpoints stay mounted.
			if rr := c.do("POST", "/api/auth/logout", ""); rr.Code != 204 {
				t.Errorf("logout: %d", rr.Code)
			}
			wantError(t, c.do("GET", "/api/auth/me", ""), 401, "unauthorized")
		})
	}
}

func TestOIDCEndpointsAreMountedOnlyInOIDCMode(t *testing.T) {
	for _, provider := range []string{"", config.AuthLocal, config.AuthIdentityPlatform} {
		e := newEnv(t, func(c *config.Config) { c.AuthProvider = provider })
		for _, p := range []string{"/api/auth/oidc/login", "/api/auth/oidc/callback"} {
			wantError(t, (&client{e: e}).do("GET", p, ""), 404, "not_found")
		}
	}

	// The provider is unreachable (no issuer configured), so a login attempt
	// ends on the SPA's login page; the point is that the route exists.
	e := newEnv(t, func(c *config.Config) { c.AuthProvider = config.AuthOIDC })
	c := &client{e: e}
	rr := c.do("GET", "/api/auth/oidc/login?from=/trash", "")
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/login?error=oidc_failed" {
		t.Errorf("login: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	rr = c.do("GET", "/api/auth/oidc/callback?error=access_denied", "")
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/login?error=oidc_cancelled" {
		t.Errorf("callback: %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Error("oidc redirects must not be cached")
	}
}
