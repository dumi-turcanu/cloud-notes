package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth/authtest"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/observability"
)

// opsEnv builds a router wired like production: metrics, logging, readiness.
func opsEnv(t *testing.T, ready func() bool) (http.Handler, *observability.Metrics, *bytes.Buffer, *env) {
	t.Helper()
	e := newEnv(t)
	clock := authtest.NewClock()
	users := authtest.NewUsers(clock)
	svc, err := auth.NewService(users, authtest.NewSessions(users), auth.NewHasher(), auth.OptionsFromConfig(e.cfg), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	m := observability.NewMetrics("v-test", "abc")
	var logs bytes.Buffer
	h, err := NewRouter(Deps{Config: e.cfg, Version: "v-test", DB: fakePinger{}, Notes: e.notes, Auth: svc,
		Recorder: m, Metrics: m, Ready: ready, Logger: observability.NewLogger(&logs, "info"), Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return h, m, &logs, e
}

func serve(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestReadyzFlipsWhenShuttingDown(t *testing.T) {
	ready := true
	h, _, _, _ := opsEnv(t, func() bool { return ready })
	if rr := serve(h, "GET", "/readyz", "", nil); rr.Code != 200 {
		t.Fatalf("readyz = %d", rr.Code)
	}
	ready = false
	if rr := serve(h, "GET", "/readyz", "", nil); rr.Code != 503 {
		t.Errorf("readyz during shutdown = %d, want 503", rr.Code)
	}
	if rr := serve(h, "GET", "/healthz", "", nil); rr.Code != 200 {
		t.Errorf("healthz must stay 200 during shutdown, got %d", rr.Code)
	}
}

func TestMetricsAndLogsThroughRealRouter(t *testing.T) {
	h, m, logs, _ := opsEnv(t, nil)

	body := `{"email":"ops@example.com","password":"a-long-enough-password"}`
	if rr := serve(h, "POST", "/api/auth/register", body, nil); rr.Code != 201 {
		t.Fatalf("register = %d %s", rr.Code, rr.Body)
	}
	serve(h, "POST", "/api/auth/login", `{"email":"ops@example.com","password":"wrong-password-here"}`, nil)
	rr := serve(h, "POST", "/api/auth/login", body, nil)
	cookie := rr.Result().Cookies()[0]
	req := httptest.NewRequest("GET", "/api/notes", nil)
	req.AddCookie(cookie)
	nrr := httptest.NewRecorder()
	h.ServeHTTP(nrr, req)
	if nrr.Code != 200 {
		t.Fatalf("notes = %d", nrr.Code)
	}
	if rr := serve(h, "GET", "/api/does-not-exist/abc", "", nil); rr.Code != 404 ||
		!strings.Contains(rr.Body.String(), `"error":"not_found"`) {
		t.Errorf("unknown api route = %d %s", rr.Code, rr.Body)
	}
	if rr := serve(h, "GET", "/nope", "", nil); rr.Code != 404 {
		t.Errorf("unknown root route = %d", rr.Code)
	}

	mrr := serve(m.Handler(), "GET", "/metrics", "", nil)
	out := mrr.Body.String()
	for _, want := range []string{
		`http_requests_total{code="201",method="POST",route="/api/auth/register"} 1`,
		`http_requests_total{code="200",method="GET",route="/api/notes"} 1`,
		`http_requests_total{code="404",method="GET",route="unmatched"} 2`,
		`auth_login_attempts_total{result="success"} 1`,
		`auth_login_attempts_total{result="invalid_credentials"} 1`,
		`auth_registrations_total{result="success"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics lack %s", want)
		}
	}

	var sawUser bool
	for _, l := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(l), &entry); err != nil {
			t.Fatalf("non-JSON log line %q", l)
		}
		if entry["route"] == "/api/notes" && entry["user_id"] != nil && entry["user_id"] != "" {
			sawUser = true
		}
	}
	if !sawUser {
		t.Errorf("authenticated request log lacks user_id:\n%s", logs.String())
	}
	for _, secret := range []string{"a-long-enough-password", "wrong-password-here", cookie.Value} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("logs leak %q", secret)
		}
	}
}
