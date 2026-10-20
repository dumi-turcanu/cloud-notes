package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	h := NewHasher()
	enc, err := h.Hash(context.Background(), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("unexpected PHC prefix: %s", enc)
	}
	if NeedsRehash(enc) {
		t.Error("fresh hash must not need rehash")
	}
	ok, err := h.Verify(context.Background(), "correct horse battery", enc)
	if err != nil || !ok {
		t.Errorf("verify correct: %v %v", ok, err)
	}
	ok, err = h.Verify(context.Background(), "wrong password!!", enc)
	if err != nil || ok {
		t.Errorf("verify wrong: %v %v", ok, err)
	}
	enc2, _ := h.Hash(context.Background(), "correct horse battery")
	if enc == enc2 {
		t.Error("salts must differ between hashes")
	}
}

func TestVerifyMalformedHashes(t *testing.T) {
	h := NewHasher()
	valid, _ := h.Hash(context.Background(), "whatever-password")
	parts := strings.Split(valid, "$")
	bad := []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=19456,t=2,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=18$m=19456,t=2,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=19456,t=2$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=0,t=2,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=999999999,t=2,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=19456,t=x,p=1$" + parts[4] + "$" + parts[5],
		"$argon2id$v=19$m=19456,t=2,p=1$!!!$" + parts[5],
		"$argon2id$v=19$m=19456,t=2,p=1$" + parts[4] + "$",
		valid + "$extra",
	}
	for _, enc := range bad {
		ok, err := h.Verify(context.Background(), "whatever-password", enc)
		if ok || err == nil {
			t.Errorf("malformed hash %q accepted (ok=%v err=%v)", enc, ok, err)
		}
		if !NeedsRehash(enc) {
			t.Errorf("malformed hash %q should report NeedsRehash", enc)
		}
	}
}

func TestNeedsRehashOnDifferentParams(t *testing.T) {
	weak, err := hashWith("pw-pw-pw-pw-pw", argonParams{memory: 8, time: 1, threads: 1, keyLen: 32})
	if err != nil {
		t.Fatal(err)
	}
	if !NeedsRehash(weak) {
		t.Error("weaker params must need rehash")
	}
	ok, err := NewHasher().Verify(context.Background(), "pw-pw-pw-pw-pw", weak)
	if err != nil || !ok {
		t.Errorf("legacy-parameter hash must still verify: %v %v", ok, err)
	}
}

func TestHasherHonoursContextWhenSaturated(t *testing.T) {
	h := NewHasher()
	for i := 0; i < maxConcurrentHashes; i++ {
		h.sem <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "x"); err == nil {
		t.Error("expected context error when semaphore is full")
	}
}

func TestHasherConcurrencyLimit(t *testing.T) {
	h := NewHasher()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.Hash(context.Background(), "concurrent-password"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(h.sem) != 0 {
		t.Errorf("semaphore leaked %d slots", len(h.sem))
	}
}

func TestNewToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, hash, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != 43 || strings.ContainsAny(tok, "+/=") {
			t.Fatalf("token must be 43 chars base64url without padding, got %q", tok)
		}
		if len(hash) != 32 || string(hash) != string(HashToken(tok)) {
			t.Fatal("hash must be SHA-256 of token")
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
		if !validTokenFormat(tok) {
			t.Fatal("generated token must pass format check")
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 44), strings.Repeat("!", 43)} {
		if validTokenFormat(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name string
		hops int
		addr string
		xff  []string
		want string
	}{
		{"0 hops ignores XFF", 0, "10.0.0.1:1234", []string{"1.2.3.4"}, "10.0.0.1"},
		{"no port in RemoteAddr", 0, "10.0.0.1", nil, "10.0.0.1"},
		{"1 hop takes last entry", 1, "10.0.0.1:1", []string{"6.6.6.6, 203.0.113.9"}, "203.0.113.9"},
		{"1 hop ignores spoofed prefix", 1, "10.0.0.1:1", []string{"9.9.9.9", "6.6.6.6, 203.0.113.9"}, "203.0.113.9"},
		{"2 hops takes second to last", 2, "10.0.0.1:1", []string{"6.6.6.6, 203.0.113.9, 10.1.1.1"}, "203.0.113.9"},
		{"fewer entries than hops falls back", 2, "10.0.0.1:1", []string{"203.0.113.9"}, "10.0.0.1"},
		{"missing header falls back", 1, "10.0.0.1:1", nil, "10.0.0.1"},
		{"garbage entry falls back", 1, "10.0.0.1:1", []string{"not-an-ip"}, "10.0.0.1"},
		{"ipv6", 1, "[::1]:80", []string{"2001:db8::1"}, "2001:db8::1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &http.Request{RemoteAddr: tc.addr, Header: http.Header{}}
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tc.hops); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(3, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if !l.Allow("a") {
			t.Fatalf("request %d within burst denied", i)
		}
	}
	if l.Allow("a") {
		t.Error("4th request must be denied")
	}
	if !l.Allow("b") {
		t.Error("other key must have its own bucket")
	}
	now = now.Add(20 * time.Second) // 3/min => 1 token per 20s
	if !l.Allow("a") {
		t.Error("token should have refilled")
	}
	if l.Allow("a") {
		t.Error("only one token should have refilled")
	}
}

func TestRateLimiterEvictsIdleEntries(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(5, func() time.Time { return now })
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	if l.Len() != 3 {
		t.Fatalf("len=%d", l.Len())
	}
	now = now.Add(limiterIdleTTL + 2*limiterSweepInterval)
	l.Allow("d")
	if l.Len() != 1 {
		t.Errorf("idle entries not evicted, len=%d", l.Len())
	}
}
