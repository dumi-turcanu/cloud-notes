package observability

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newRouter mimics the production wiring: middleware on the root router, an
// /api sub-router mounted below it, probes at the root.
func newRouter(logger *slog.Logger, m *Metrics) http.Handler {
	r := chi.NewRouter()
	r.Use(WithRequestID)
	if m != nil {
		r.Use(m.Middleware)
	}
	r.Use(RequestLogger(logger, func(*http.Request) string { return "203.0.113.9" }))
	r.Use(Recoverer(logger))
	unmatched := func(w http.ResponseWriter, r *http.Request) {
		MarkUnmatched(r.Context())
		http.NotFound(w, r)
	}
	r.NotFound(unmatched)

	api := chi.NewRouter()
	api.NotFound(unmatched)
	api.Get("/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		SetUserID(r.Context(), "user-1")
		_, _ = io.WriteString(w, "hello")
	})
	api.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("kaboom\nsecret-ish") })
	r.Mount("/api", api)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	return r
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func TestRequestLogLine(t *testing.T) {
	var buf bytes.Buffer
	h := newRouter(NewLogger(&buf, "info"), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/notes/42", nil)
	req.Header.Set("Cookie", "__Host-cn_session=super-secret-token")
	req.Header.Set("Authorization", "Bearer also-secret")
	req.Header.Set("X-Request-ID", "req-abc.123")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	lines := logLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want 1: %s", len(lines), buf.String())
	}
	l := lines[0]
	want := map[string]any{
		"method": "GET", "route": "/api/notes/{id}", "status": float64(200), "bytes": float64(5),
		"request_id": "req-abc.123", "client_ip": "203.0.113.9", "user_id": "user-1",
	}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("log[%q] = %v, want %v", k, l[k], v)
		}
	}
	if _, ok := l["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms missing: %v", l)
	}
	if strings.Contains(buf.String(), "super-secret-token") || strings.Contains(buf.String(), "also-secret") {
		t.Errorf("log leaks credentials: %s", buf.String())
	}
	if got := rec.Header().Get("X-Request-ID"); got != "req-abc.123" {
		t.Errorf("X-Request-ID echoed as %q", got)
	}
}

func TestRequestIDValidation(t *testing.T) {
	h := newRouter(NewLogger(io.Discard, "info"), nil)
	for _, bad := range []string{"has space", "semi;colon", strings.Repeat("a", 65), "new\nline"} {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header["X-Request-Id"] = []string{bad}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		got := rec.Header().Get("X-Request-ID")
		if got == bad || !validRequestID(got) {
			t.Errorf("incoming %q: response id %q should be freshly generated and valid", bad, got)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if len(rec.Header().Get("X-Request-ID")) != 32 {
		t.Errorf("generated id = %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestProbesLoggedAtDebugOnly(t *testing.T) {
	var buf bytes.Buffer
	h := newRouter(NewLogger(&buf, "info"), nil)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if buf.Len() != 0 {
		t.Errorf("/healthz logged at info: %s", buf.String())
	}
	buf.Reset()
	h = newRouter(NewLogger(&buf, "debug"), nil)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if lines := logLines(t, &buf); len(lines) != 1 || lines[0]["level"] != "DEBUG" {
		t.Errorf("expected one DEBUG line, got %s", buf.String())
	}
}

func TestRecovererReturnsJSON500AndLogsStack(t *testing.T) {
	var buf bytes.Buffer
	m := NewMetrics("v", "c")
	h := newRouter(NewLogger(&buf, "info"), m)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/boom", nil))

	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"error":"internal_error"`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	var sawPanic, sawRequest bool
	for _, l := range logLines(t, &buf) {
		switch l["msg"] {
		case "panic recovered":
			sawPanic = strings.Contains(l["stack"].(string), "goroutine")
		case "request":
			sawRequest = l["status"] == float64(500)
		}
	}
	if !sawPanic || !sawRequest {
		t.Errorf("panic logged with stack=%v, request line with 500=%v\n%s", sawPanic, sawRequest, buf.String())
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "/api/boom", "500")); got != 1 {
		t.Errorf("500 not counted: %v", got)
	}
}

func TestMetricsUseRoutePatterns(t *testing.T) {
	m := NewMetrics("1.2.3", "abc")
	h := newRouter(NewLogger(io.Discard, "info"), m)
	for _, p := range []string{"/api/notes/1", "/api/notes/2", "/api/nope/123", "/totally/unknown/path"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "/api/notes/{id}", "200")); got != 2 {
		t.Errorf("route pattern count = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "unmatched", "404")); got != 2 {
		t.Errorf("unmatched count = %v, want 2", got)
	}

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`http_requests_total{code="200",method="GET",route="/api/notes/{id}"} 2`,
		`http_request_duration_seconds_bucket{method="GET",route="/api/notes/{id}",le=`,
		`http_requests_in_flight 0`,
		`cloud_notes_build_info{commit="abc",go_version="go`,
		`auth_login_attempts_total{result="locked"} 0`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
	if strings.Contains(body, "/api/notes/1") {
		t.Error("raw paths leaked into labels (cardinality)")
	}
}

func TestAuthRecorderCounters(t *testing.T) {
	m := NewMetrics("v", "c")
	m.LoginAttempt("success")
	m.LoginAttempt("success")
	m.LoginAttempt("locked")
	m.Registration("email_taken")
	if got := testutil.ToFloat64(m.logins.WithLabelValues("success")); got != 2 {
		t.Errorf("success = %v", got)
	}
	if got := testutil.ToFloat64(m.logins.WithLabelValues("locked")); got != 1 {
		t.Errorf("locked = %v", got)
	}
	if got := testutil.ToFloat64(m.signups.WithLabelValues("email_taken")); got != 1 {
		t.Errorf("registrations = %v", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	var buf bytes.Buffer
	l := NewLogger(&buf, "warn")
	l.Info("hidden")
	l.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Errorf("level filtering wrong: %s", buf.String())
	}
}
