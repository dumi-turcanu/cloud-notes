// Package oidctest provides an in-process OpenID Connect provider for tests:
// discovery, JWKS, an /authorize endpoint that redirects straight back with a
// code, and a /token endpoint that checks PKCE and returns a signed ID token.
// It is only imported by tests.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const keyID = "test-key"

// Signer signs JWTs with a throw-away RSA key.
type Signer struct {
	key    *rsa.PrivateKey
	signer jose.Signer
}

// NewSigner generates a key. 2048 bits keeps go-jose happy and is still fast.
func NewSigner(t testing.TB) *Signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID))
	if err != nil {
		t.Fatal(err)
	}
	return &Signer{key: key, signer: signer}
}

// Public is the verification key (for oidc.StaticKeySet).
func (s *Signer) Public() crypto.PublicKey { return &s.key.PublicKey }

// Sign returns a compact JWT with exactly the given claims. It panics on
// failure, which can only come from a broken test setup.
func (s *Signer) Sign(claims map[string]any) string {
	payload, err := json.Marshal(claims)
	if err != nil {
		panic(err)
	}
	obj, err := s.signer.Sign(payload)
	if err != nil {
		panic(err)
	}
	tok, err := obj.CompactSerialize()
	if err != nil {
		panic(err)
	}
	return tok
}

// JWKS is the key set document served at /jwks.
func (s *Signer) JWKS() []byte {
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &s.key.PublicKey, KeyID: keyID, Algorithm: string(jose.RS256), Use: "sig"}}}
	b, _ := json.Marshal(set)
	return b
}

// Server is a fake OIDC provider. Configure the exported fields before the
// first request; they are read without locking.
type Server struct {
	*httptest.Server
	// Signer's public key is published at /jwks.
	Signer *Signer
	// TokenSigner signs the ID tokens; it defaults to Signer. A different one
	// produces tokens that do not verify against the published keys.
	TokenSigner *Signer

	ClientID     string
	ClientSecret string
	// Issuer is what discovery and the tokens claim; it defaults to the server URL.
	Issuer string
	// EndSession adds end_session_endpoint to the discovery document.
	EndSession bool
	// Deny makes /authorize answer error=access_denied instead of a code.
	Deny bool
	// Claims returns the ID token claims for a login; nil uses DefaultClaims.
	// The nonce is the one sent to /authorize.
	Claims func(nonce string) map[string]any

	failDiscovery atomic.Int32
	discoveryHits atomic.Int32

	mu    sync.Mutex
	codes map[string]issuedCode
	// TokenRequests records the form of every /token call (for assertions).
	TokenRequests []url.Values
}

type issuedCode struct {
	challenge, nonce, redirectURI string
}

// New starts a provider that registers a confidential client.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		Signer: NewSigner(t), ClientID: "cloud-notes", ClientSecret: "test-client-secret",
		codes: map[string]issuedCode{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(s.Signer.JWKS())
	})
	mux.HandleFunc("GET /authorize", s.authorize)
	mux.HandleFunc("POST /token", s.token)
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Server.Close)
	s.Issuer = s.URL
	return s
}

// FailDiscovery makes the next n discovery requests answer 503.
func (s *Server) FailDiscovery(n int) { s.failDiscovery.Store(int32(n)) }

// DiscoveryRequests is the number of discovery requests received so far.
func (s *Server) DiscoveryRequests() int { return int(s.discoveryHits.Load()) }

// DefaultClaims is a valid ID token for ann@example.com.
func (s *Server) DefaultClaims(nonce string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": s.Issuer, "sub": "user-1", "aud": s.ClientID,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": nonce,
		"email": "ann@example.com", "email_verified": true, "name": "Ann Example",
	}
}

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	s.discoveryHits.Add(1)
	if s.failDiscovery.Load() > 0 {
		s.failDiscovery.Add(-1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	doc := map[string]any{
		"issuer":                                s.Issuer,
		"authorization_endpoint":                s.URL + "/authorize",
		"token_endpoint":                        s.URL + "/token",
		"jwks_uri":                              s.URL + "/jwks",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}
	if s.EndSession {
		doc["end_session_endpoint"] = s.URL + "/logout"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect := q.Get("redirect_uri")
	if q.Get("client_id") != s.ClientID || q.Get("response_type") != "code" || redirect == "" ||
		q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	back, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	v := back.Query()
	v.Set("state", q.Get("state"))
	if s.Deny {
		v.Set("error", "access_denied")
	} else {
		code := randomString()
		s.mu.Lock()
		s.codes[code] = issuedCode{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirectURI: redirect}
		s.mu.Unlock()
		v.Set("code", code)
	}
	back.RawQuery = v.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.TokenRequests = append(s.TokenRequests, r.PostForm)
	s.mu.Unlock()

	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != s.ClientID || subtle.ConstantTimeCompare([]byte(secret), []byte(s.ClientSecret)) != 1 {
		tokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	s.mu.Lock()
	ic, ok := s.codes[r.PostForm.Get("code")]
	delete(s.codes, r.PostForm.Get("code")) // codes are single use
	s.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if r.PostForm.Get("grant_type") != "authorization_code" || !ok ||
		r.PostForm.Get("redirect_uri") != ic.redirectURI ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != ic.challenge {
		tokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	claims := s.DefaultClaims
	if s.Claims != nil {
		claims = s.Claims
	}
	signer := s.Signer
	if s.TokenSigner != nil {
		signer = s.TokenSigner
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "access-" + randomString(), "token_type": "Bearer", "expires_in": 3600,
		"id_token": signer.Sign(claims(ic.nonce)),
	})
}

func tokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func randomString() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
