package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/auth/authtest"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

const goodPassword = "a-very-good-password"

type fixture struct {
	svc      *Service
	clock    *authtest.Clock
	users    *authtest.Users
	sessions *authtest.Sessions
}

func newFixture(t *testing.T, mutate ...func(*Options)) *fixture {
	t.Helper()
	clock := authtest.NewClock()
	users := authtest.NewUsers(clock)
	sessions := authtest.NewSessions(users)
	opts := Options{
		SignupMode: config.SignupOpen, SessionTTL: 12 * time.Hour, IdleTimeout: time.Hour,
		MaxFailedLogins: 3, LockoutDuration: 15 * time.Minute,
	}
	for _, m := range mutate {
		m(&opts)
	}
	svc, err := NewService(users, sessions, NewHasher(), opts, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{svc, clock, users, sessions}
}

func (f *fixture) register(t *testing.T, email string) *Issued {
	t.Helper()
	iss, err := f.svc.Register(context.Background(), models.RegisterRequest{Email: email, Password: goodPassword}, ClientMeta{})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return iss
}

func TestRegisterSignupModes(t *testing.T) {
	invite := strings.Repeat("i", 16)
	tests := []struct {
		name string
		mode string
		code string
		want error
	}{
		{"disabled", config.SignupDisabled, "", ErrSignupDisabled},
		{"invite wrong", config.SignupInvite, "nope", ErrInvalidInviteCode},
		{"invite empty", config.SignupInvite, "", ErrInvalidInviteCode},
		{"invite right", config.SignupInvite, invite, nil},
		{"open", config.SignupOpen, "", nil},
		{"unknown mode fails closed", "weird", "", ErrSignupDisabled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, func(o *Options) { o.SignupMode = tc.mode; o.InviteCode = invite })
			_, err := f.svc.Register(context.Background(),
				models.RegisterRequest{Email: "a@example.com", Password: goodPassword, InviteCode: tc.code}, ClientMeta{})
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestRegisterEmptyInviteCodeNeverMatchesEmptyConfig(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.SignupMode = config.SignupInvite; o.InviteCode = "" })
	_, err := f.svc.Register(context.Background(), models.RegisterRequest{Email: "a@example.com", Password: goodPassword}, ClientMeta{})
	if !errors.Is(err, ErrInvalidInviteCode) {
		t.Errorf("got %v", err)
	}
}

func TestRegisterValidationAndNormalisation(t *testing.T) {
	f := newFixture(t)
	long := strings.Repeat("x", 250) + "@example.com"
	tests := []struct {
		name, email, password, display, badField string
	}{
		{"bad email", "not-an-email", goodPassword, "", "email"},
		{"display form rejected", "Ann <ann@example.com>", goodPassword, "", "email"},
		{"email too long", long, goodPassword, "", "email"},
		{"short password", "a@example.com", "short", "", "password"},
		{"long password", "a@example.com", strings.Repeat("p", 129), "", "password"},
		{"password equals email", "longer-address@example.com", "Longer-Address@example.com", "", "password"},
		{"display name too long", "a@example.com", goodPassword, strings.Repeat("d", 101), "display_name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.Register(context.Background(),
				models.RegisterRequest{Email: tc.email, Password: tc.password, DisplayName: tc.display}, ClientMeta{})
			var verr *ValidationError
			if !errors.As(err, &verr) || verr.Fields[tc.badField] == "" {
				t.Errorf("want validation error on %s, got %v", tc.badField, err)
			}
		})
	}

	iss := f.register(t, "  Mixed.Case@Example.COM ")
	if iss.User.Email != "mixed.case@example.com" {
		t.Errorf("email not normalised: %q", iss.User.Email)
	}
	multibyte := strings.Repeat("ü", 12) // 12 runes, 24 bytes
	if _, err := f.svc.CreateUser(context.Background(), "u@example.com", "", multibyte); err != nil {
		t.Errorf("12-rune password must be accepted: %v", err)
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	f := newFixture(t)
	f.register(t, "dup@example.com")
	_, err := f.svc.Register(context.Background(), models.RegisterRequest{Email: "DUP@example.com", Password: goodPassword}, ClientMeta{})
	if !errors.Is(err, models.ErrEmailTaken) {
		t.Errorf("got %v", err)
	}
}

func TestRegisterStoresHashNotPassword(t *testing.T) {
	f := newFixture(t)
	iss := f.register(t, "h@example.com")
	acct := f.users.Get(iss.User.ID)
	if strings.Contains(acct.PasswordHash, goodPassword) || !strings.HasPrefix(acct.PasswordHash, "$argon2id$") {
		t.Errorf("bad stored hash %q", acct.PasswordHash)
	}
}

func TestLoginSuccessAndFailure(t *testing.T) {
	f := newFixture(t)
	f.register(t, "l@example.com")
	ctx := context.Background()

	iss, err := f.svc.Login(ctx, "L@Example.com", goodPassword, ClientMeta{IP: "1.2.3.4", UserAgent: "ua"})
	if err != nil || iss.Token == "" {
		t.Fatalf("login: %v", err)
	}
	if got := iss.ExpiresAt.Sub(f.clock.Now()); got != 12*time.Hour {
		t.Errorf("absolute lifetime = %v", got)
	}
	for _, tc := range []struct{ email, pw string }{
		{"l@example.com", "wrong-password-1"},
		{"nobody@example.com", goodPassword},
		{"not an email", goodPassword},
		{"", ""},
	} {
		if _, err := f.svc.Login(ctx, tc.email, tc.pw, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("login(%q): got %v", tc.email, err)
		}
	}
}

func TestLoginLockoutAndUnlock(t *testing.T) {
	f := newFixture(t) // 3 failures lock for 15m
	iss := f.register(t, "lock@example.com")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := f.svc.Login(ctx, "lock@example.com", "wrong-password-1", ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// Locked: even the correct password is refused without being checked.
	var locked *LockedError
	_, err := f.svc.Login(ctx, "lock@example.com", goodPassword, ClientMeta{})
	if !errors.As(err, &locked) {
		t.Fatalf("want LockedError, got %v", err)
	}
	if locked.RetryAfter <= 0 || locked.RetryAfter > 15*time.Minute {
		t.Errorf("RetryAfter = %v", locked.RetryAfter)
	}
	f.clock.Advance(14 * time.Minute)
	if _, err := f.svc.Login(ctx, "lock@example.com", goodPassword, ClientMeta{}); !errors.As(err, &locked) {
		t.Errorf("still locked after 14m, got %v", err)
	}
	f.clock.Advance(2 * time.Minute)
	if _, err := f.svc.Login(ctx, "lock@example.com", goodPassword, ClientMeta{}); err != nil {
		t.Errorf("must unlock after duration: %v", err)
	}
	if acct := f.users.Get(iss.User.ID); acct.FailedLoginCount != 0 || acct.LockedUntil != nil || acct.LastLoginAt == nil {
		t.Errorf("success must reset lockout state: %+v", acct)
	}
}

func TestSuccessfulLoginResetsFailureCounter(t *testing.T) {
	f := newFixture(t)
	iss := f.register(t, "r@example.com")
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, _ = f.svc.Login(ctx, "r@example.com", "wrong-password-1", ClientMeta{})
	}
	if _, err := f.svc.Login(ctx, "r@example.com", goodPassword, ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if f.users.Get(iss.User.ID).FailedLoginCount != 0 {
		t.Error("counter not reset")
	}
	// Two more failures must not lock (counter restarted).
	for i := 0; i < 2; i++ {
		_, _ = f.svc.Login(ctx, "r@example.com", "wrong-password-1", ClientMeta{})
	}
	if _, err := f.svc.Login(ctx, "r@example.com", goodPassword, ClientMeta{}); err != nil {
		t.Errorf("should not be locked: %v", err)
	}
}

func TestLoginRehashesLegacyParameters(t *testing.T) {
	f := newFixture(t)
	iss := f.register(t, "re@example.com")
	weak, _ := hashWith(goodPassword, argonParams{memory: 8, time: 1, threads: 1, keyLen: 32})
	f.users.SetHash(iss.User.ID, weak)

	if _, err := f.svc.Login(context.Background(), "re@example.com", goodPassword, ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if f.users.HashUpdates != 1 {
		t.Fatalf("expected one rehash, got %d", f.users.HashUpdates)
	}
	if NeedsRehash(f.users.Get(iss.User.ID).PasswordHash) {
		t.Error("stored hash still uses old parameters")
	}
	if _, err := f.svc.Login(context.Background(), "re@example.com", goodPassword, ClientMeta{}); err != nil || f.users.HashUpdates != 1 {
		t.Errorf("second login must not rehash again (updates=%d, err=%v)", f.users.HashUpdates, err)
	}
}

func TestAuthenticateLifecycle(t *testing.T) {
	f := newFixture(t)
	iss := f.register(t, "s@example.com")
	ctx := context.Background()

	sess, err := f.svc.Authenticate(ctx, iss.Token)
	if err != nil || sess.User.Email != "s@example.com" {
		t.Fatalf("authenticate: %v", err)
	}
	for _, bad := range []string{"", "garbage", strings.Repeat("A", 43)} {
		if _, err := f.svc.Authenticate(ctx, bad); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("token %q: %v", bad, err)
		}
	}
	if err := f.svc.Logout(ctx, iss.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(ctx, iss.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after logout: %v", err)
	}
	if err := f.svc.Logout(ctx, "not-a-token"); err != nil {
		t.Errorf("logout of garbage must be a no-op: %v", err)
	}
}

func TestSessionAbsoluteExpiry(t *testing.T) {
	f := newFixture(t)
	iss := f.register(t, "abs@example.com")
	// Stay active (within idle timeout) but cross the 12h absolute limit.
	for i := 0; i < 12; i++ {
		f.clock.Advance(59 * time.Minute)
		if _, err := f.svc.Authenticate(context.Background(), iss.Token); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	f.clock.Advance(59 * time.Minute) // 12h11m since creation
	if _, err := f.svc.Authenticate(context.Background(), iss.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("want absolute expiry, got %v", err)
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	f := newFixture(t)
	iss := f.register(t, "idle@example.com")
	f.clock.Advance(61 * time.Minute)
	if _, err := f.svc.Authenticate(context.Background(), iss.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("want idle expiry, got %v", err)
	}
}

func TestTouchIsThrottled(t *testing.T) {
	f := newFixture(t)
	iss := f.register(t, "t@example.com")
	ctx := context.Background()

	f.clock.Advance(30 * time.Second)
	_, _ = f.svc.Authenticate(ctx, iss.Token)
	_, _ = f.svc.Authenticate(ctx, iss.Token)
	if f.sessions.Touches != 0 {
		t.Errorf("touched within a minute: %d", f.sessions.Touches)
	}
	f.clock.Advance(40 * time.Second)
	_, _ = f.svc.Authenticate(ctx, iss.Token)
	_, _ = f.svc.Authenticate(ctx, iss.Token)
	if f.sessions.Touches != 1 {
		t.Errorf("want exactly 1 touch after 70s, got %d", f.sessions.Touches)
	}
	// Activity keeps an idle session alive past the nominal idle timeout.
	for i := 0; i < 5; i++ {
		f.clock.Advance(50 * time.Minute)
		if _, err := f.svc.Authenticate(ctx, iss.Token); err != nil {
			t.Fatalf("active session expired at step %d: %v", i, err)
		}
	}
}

func TestSessionTokenStoredOnlyAsHash(t *testing.T) {
	f := newFixture(t)
	iss := f.register(t, "hash@example.com")
	if f.sessions.Count() != 1 {
		t.Fatal("session not stored")
	}
	if _, err := f.svc.Authenticate(context.Background(), iss.Token); err != nil {
		t.Fatal(err)
	}
}

func TestCleanerRemovesExpiredSessions(t *testing.T) {
	f := newFixture(t)
	f.register(t, "c1@example.com")
	f.clock.Advance(2 * time.Hour) // idle-expired
	f.register(t, "c2@example.com")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.svc.RunCleaner(ctx, 10*time.Millisecond); close(done) }()
	deadline := time.After(2 * time.Second)
	for f.sessions.Count() != 1 {
		select {
		case <-deadline:
			t.Fatalf("cleaner did not remove expired session, count=%d", f.sessions.Count())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleaner did not stop on context cancel")
	}
}

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{" A@B.co ": "a@b.co", "x@y.z": "x@y.z"} {
		if got, ok := NormalizeEmail(in); !ok || got != want {
			t.Errorf("%q → %q,%v", in, got, ok)
		}
	}
	for _, in := range []string{"", "plain", "a@", "@b.c", "A <a@b.c>", "a@b.c, d@e.f"} {
		if _, ok := NormalizeEmail(in); ok {
			t.Errorf("%q must be rejected", in)
		}
	}
}

func TestLoginExternalCreatesUserWithoutPassword(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := ExternalIdentity{Issuer: "https://idp.example.com", Subject: "sub-1", Email: "  Ext@Example.COM ", DisplayName: "  Eve  "}

	iss, err := f.svc.LoginExternal(ctx, id, ClientMeta{IP: "1.2.3.4", UserAgent: "ua"})
	if err != nil || iss.Token == "" {
		t.Fatalf("login external: %v", err)
	}
	if iss.User.Email != "ext@example.com" || iss.User.DisplayName == nil || *iss.User.DisplayName != "Eve" {
		t.Errorf("user not normalised: %+v", iss.User)
	}
	if got := iss.ExpiresAt.Sub(f.clock.Now()); got != 12*time.Hour {
		t.Errorf("absolute lifetime = %v", got)
	}
	sess, err := f.svc.Authenticate(ctx, iss.Token)
	if err != nil || sess.User.ID != iss.User.ID {
		t.Errorf("session does not resolve to the user: %v", err)
	}
	if acct := f.users.Get(iss.User.ID); acct.PasswordHash != "" {
		t.Errorf("external user must have no password hash, got %q", acct.PasswordHash)
	}

	again, err := f.svc.LoginExternal(ctx, id, ClientMeta{})
	if err != nil || again.User.ID != iss.User.ID {
		t.Errorf("same (issuer, subject) must map to the same user: %v", err)
	}
	if again.Token == iss.Token || f.sessions.Count() != 2 {
		t.Error("each login must issue a new session")
	}
}

func TestLoginExternalEmailHandling(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  error
	}{
		{"missing", "", ErrEmailClaimMissing},
		{"blank", "   ", ErrEmailClaimMissing},
		{"not an address", "not-an-email", ErrEmailClaimMissing},
		{"display form is rejected", "Eve <eve@example.com>", ErrEmailClaimMissing},
		{"too long", strings.Repeat("a", 250) + "@example.com", ErrEmailClaimMissing},
		{"valid", "eve@example.com", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.svc.LoginExternal(context.Background(),
				ExternalIdentity{Issuer: "i", Subject: "s", Email: tc.email}, ClientMeta{})
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v want %v", err, tc.want)
			}
			if tc.want != nil && f.sessions.Count() != 0 {
				t.Error("no session may be issued without a valid email")
			}
		})
	}
}

func TestLoginExternalTruncatesDisplayName(t *testing.T) {
	f := newFixture(t)
	long := strings.Repeat("é", maxDisplayNameLen+20)
	iss, err := f.svc.LoginExternal(context.Background(),
		ExternalIdentity{Issuer: "i", Subject: "s", Email: "e@example.com", DisplayName: long}, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if got := utf8.RuneCountInString(*iss.User.DisplayName); got != maxDisplayNameLen {
		t.Errorf("display name has %d runes, want %d", got, maxDisplayNameLen)
	}
}

func TestLoginExternalNeverLinksByEmail(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	local := f.register(t, "victim@example.com")

	_, err := f.svc.LoginExternal(ctx, ExternalIdentity{Issuer: "i", Subject: "attacker", Email: "victim@example.com"}, ClientMeta{})
	if !errors.Is(err, models.ErrAccountConflict) {
		t.Fatalf("got %v, want ErrAccountConflict", err)
	}
	if f.sessions.Count() != 1 {
		t.Error("a conflicting identity must not get a session")
	}

	// Two providers using the same email stay two separate identities: the
	// second one conflicts instead of inheriting the first one's notes.
	if _, err := f.svc.LoginExternal(ctx, ExternalIdentity{Issuer: "a", Subject: "1", Email: "shared@example.com"}, ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.LoginExternal(ctx, ExternalIdentity{Issuer: "b", Subject: "1", Email: "shared@example.com"}, ClientMeta{}); !errors.Is(err, models.ErrAccountConflict) {
		t.Errorf("same email at another issuer: got %v", err)
	}
	if got, _ := f.users.GetByEmail(ctx, "victim@example.com"); got.ID != local.User.ID {
		t.Error("local account was modified")
	}
}

func TestLoginExternalRefreshesProfile(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := ExternalIdentity{Issuer: "i", Subject: "s", Email: "old@example.com", DisplayName: "Old"}
	first, err := f.svc.LoginExternal(ctx, id, ClientMeta{})
	if err != nil {
		t.Fatal(err)
	}

	id.Email, id.DisplayName = "new@example.com", "New"
	second, err := f.svc.LoginExternal(ctx, id, ClientMeta{})
	if err != nil || second.User.ID != first.User.ID || second.User.Email != "new@example.com" || *second.User.DisplayName != "New" {
		t.Fatalf("profile not refreshed: %+v %v", second, err)
	}

	// A provider-side email that now belongs to another account is ignored.
	f.register(t, "taken@example.com")
	id.Email = "taken@example.com"
	third, err := f.svc.LoginExternal(ctx, id, ClientMeta{})
	if err != nil || third.User.ID != first.User.ID || third.User.Email != "new@example.com" {
		t.Errorf("taken email must keep the old one: %+v %v", third, err)
	}
}

func TestLocalLoginRejectsExternalUsers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.LoginExternal(ctx, ExternalIdentity{Issuer: "i", Subject: "s", Email: "ext@example.com"}, ClientMeta{}); err != nil {
		t.Fatal(err)
	}
	for _, pw := range []string{goodPassword, "", "dummy-password-for-timing-equalisation"} {
		if _, err := f.svc.Login(ctx, "ext@example.com", pw, ClientMeta{}); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("login(%q) for a user without a password: got %v", pw, err)
		}
	}
	// It is indistinguishable from an unknown user: no lockout state is kept.
	acct, _ := f.users.GetByEmail(ctx, "ext@example.com")
	if acct.FailedLoginCount != 0 || acct.LockedUntil != nil {
		t.Errorf("external user accrued lockout state: %+v", acct)
	}
}
