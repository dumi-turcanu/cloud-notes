package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

func envFn(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func noFile(string) ([]byte, error) { return nil, errors.New("no files in this test") }

// --- healthcheck ---------------------------------------------------------

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer ok.Close()
	unready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer unready.Close()
	hang := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-hang }))
	defer slow.Close()
	defer close(hang)

	cases := []struct {
		name string
		args []string
		want int
	}{
		{"200 is healthy", []string{"--url", ok.URL}, exitOK},
		{"503 is unhealthy", []string{"--url", unready.URL}, exitError},
		{"unreachable", []string{"--url", "http://127.0.0.1:1/readyz"}, exitError},
		{"bad flag", []string{"--nope"}, exitUsage},
		{"stray arg", []string{"extra"}, exitUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := healthcheck(context.Background(), tc.args, envFn(nil), &stderr); got != tc.want {
				t.Errorf("exit = %d, want %d (stderr: %s)", got, tc.want, stderr.String())
			}
		})
	}

	t.Run("timeout", func(t *testing.T) {
		start := time.Now()
		var stderr bytes.Buffer
		if got := healthcheck(context.Background(), []string{"--url", slow.URL}, envFn(nil), &stderr); got != exitError {
			t.Errorf("exit = %d, want %d", got, exitError)
		}
		if d := time.Since(start); d > healthcheckTimeout+2*time.Second {
			t.Errorf("took %s, timeout not enforced", d)
		}
	})
}

func TestDefaultHealthURL(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{nil, "http://127.0.0.1:8080/readyz"},
		{map[string]string{"PORT": "9000"}, "http://127.0.0.1:9000/readyz"},
		{map[string]string{"HTTP_ADDR": "0.0.0.0:7000", "PORT": "9000"}, "http://127.0.0.1:7000/readyz"},
		{map[string]string{"HTTP_ADDR": ":7001"}, "http://127.0.0.1:7001/readyz"},
		{map[string]string{"HTTP_ADDR": "garbage"}, "http://127.0.0.1:8080/readyz"},
	}
	for _, tc := range cases {
		if got := defaultHealthURL(envFn(tc.env)); got != tc.want {
			t.Errorf("env %v: got %s, want %s", tc.env, got, tc.want)
		}
	}
}

// --- create-user ---------------------------------------------------------

type fakeCreator struct {
	existing map[string]bool
	calls    []string
	gotPass  string
	err      error
}

func (f *fakeCreator) CreateUser(_ context.Context, email, name, password string) (*models.UserAccount, error) {
	f.calls = append(f.calls, email+"|"+name)
	f.gotPass = password
	if f.err != nil {
		return nil, f.err
	}
	if f.existing[email] {
		return nil, models.ErrEmailTaken
	}
	return &models.UserAccount{User: models.User{ID: uuid.New(), Email: email}}, nil
}

func runCreate(t *testing.T, args []string, env map[string]string, stdin string, f *fakeCreator) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	opened := false
	code := createUser(context.Background(), args, envFn(env), noFile, strings.NewReader(stdin), &out, &errb,
		func(context.Context) (userCreator, func(), error) { opened = true; return f, func() {}, nil })
	if code == exitUsage && opened {
		t.Error("database opened although usage was wrong")
	}
	return code, out.String(), errb.String()
}

func TestCreateUserPasswordFromEnv(t *testing.T) {
	f := &fakeCreator{}
	code, out, errs := runCreate(t, []string{"--email", "ann@example.com", "--display-name", "Ann"},
		map[string]string{"NEW_USER_PASSWORD": "correct horse battery"}, "", f)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if f.gotPass != "correct horse battery" || f.calls[0] != "ann@example.com|Ann" {
		t.Errorf("service called with %v / %q", f.calls, f.gotPass)
	}
	if strings.Contains(out+errs, "correct horse battery") {
		t.Error("password printed")
	}
}

func TestCreateUserPasswordFromStdin(t *testing.T) {
	f := &fakeCreator{}
	code, _, errs := runCreate(t, []string{"--email", "a@example.com", "--password-stdin"},
		map[string]string{"NEW_USER_PASSWORD": "ignored-env-password"}, "from-stdin-password\r\nsecond line\n", f)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if f.gotPass != "from-stdin-password" {
		t.Errorf("password = %q", f.gotPass)
	}
}

func TestCreateUserPasswordFromFile(t *testing.T) {
	var out, errb bytes.Buffer
	f := &fakeCreator{}
	readFile := func(p string) ([]byte, error) {
		if p != "/run/secrets/pw" {
			return nil, errors.New("unexpected path")
		}
		return []byte("file-password-123\n"), nil
	}
	code := createUser(context.Background(), []string{"--email", "a@example.com"},
		envFn(map[string]string{"NEW_USER_PASSWORD_FILE": "/run/secrets/pw"}), readFile, strings.NewReader(""), &out, &errb,
		func(context.Context) (userCreator, func(), error) { return f, func() {}, nil })
	if code != exitOK || f.gotPass != "file-password-123" {
		t.Errorf("exit %d pass %q: %s", code, f.gotPass, errb.String())
	}
}

func TestCreateUserIfNotExists(t *testing.T) {
	f := &fakeCreator{existing: map[string]bool{"ann@example.com": true}}
	env := map[string]string{"NEW_USER_PASSWORD": "correct horse battery"}

	if code, _, _ := runCreate(t, []string{"--email", "ann@example.com", "--if-not-exists"}, env, "", f); code != exitOK {
		t.Errorf("with --if-not-exists: exit %d, want 0", code)
	}
	if code, _, errs := runCreate(t, []string{"--email", "ann@example.com"}, env, "", f); code != exitError || !strings.Contains(errs, "already exists") {
		t.Errorf("without flag: exit %d (%s), want 1", code, errs)
	}
}

func TestCreateUserValidationErrors(t *testing.T) {
	f := &fakeCreator{err: &auth.ValidationError{Fields: map[string]string{"password": "must be 12-128 characters"}}}
	code, _, errs := runCreate(t, []string{"--email", "a@example.com"}, map[string]string{"NEW_USER_PASSWORD": "short"}, "", f)
	if code != exitError || !strings.Contains(errs, "password") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}

func TestCreateUserUsageErrors(t *testing.T) {
	env := map[string]string{"NEW_USER_PASSWORD": "correct horse battery"}
	cases := []struct {
		name  string
		args  []string
		env   map[string]string
		stdin string
	}{
		{"missing email", nil, env, ""},
		{"unknown flag", []string{"--email", "a@b.co", "--password", "x"}, env, ""}, // no --password flag exists
		{"no password anywhere", []string{"--email", "a@b.co"}, nil, ""},
		{"empty stdin", []string{"--email", "a@b.co", "--password-stdin"}, env, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _, _ := runCreate(t, tc.args, tc.env, tc.stdin, &fakeCreator{}); code != exitUsage {
				t.Errorf("exit %d, want %d", code, exitUsage)
			}
		})
	}
}

func TestCreateUserBackendFailure(t *testing.T) {
	f := &fakeCreator{err: errors.New("db down")}
	code, _, errs := runCreate(t, []string{"--email", "a@example.com"}, map[string]string{"NEW_USER_PASSWORD": "correct horse battery"}, "", f)
	if code != exitError || !strings.Contains(errs, "db down") {
		t.Errorf("exit %d: %s", code, errs)
	}
}

func TestDispatch(t *testing.T) {
	var out, errb bytes.Buffer
	if code := dispatch(context.Background(), []string{"version"}, strings.NewReader(""), &out, &errb); code != exitOK ||
		!strings.Contains(out.String(), "cloud-notes-api dev (commit unknown") {
		t.Errorf("version: %d %q", code, out.String())
	}
	if code := dispatch(context.Background(), []string{"bogus"}, strings.NewReader(""), &out, &errb); code != exitUsage {
		t.Errorf("unknown command exit %d", code)
	}
	if code := dispatch(context.Background(), []string{"serve", "--extra"}, strings.NewReader(""), &out, &errb); code != exitUsage {
		t.Errorf("serve with args exit %d", code)
	}
}
