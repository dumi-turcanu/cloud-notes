package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/config"
)

const (
	// securetokenJWKSURL publishes the keys that sign Identity Platform /
	// Firebase ID tokens.
	securetokenJWKSURL = "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"
	securetokenIssuer  = "https://securetoken.google.com/"

	// maxAuthAge is how old a sign-in may be when its token is presented. The
	// SPA exchanges the token right after signing in, so this only blocks the
	// replay of tokens that were captured earlier.
	maxAuthAge = 5 * time.Minute
	// authTimeSkew tolerates a client/server clock difference in the other direction.
	authTimeSkew = time.Minute
)

// ErrInvalidToken means an Identity Platform ID token failed verification.
// The cause is deliberately not part of the error: it must not reach clients
// or logs.
var ErrInvalidToken = errors.New("invalid identity platform token")

// IdentityPlatformSettings configures the Identity Platform token verifier.
type IdentityPlatformSettings struct {
	ProjectID     string
	TenantID      string   // optional; must match firebase.tenant when set
	SignInMethods []string // subset of config.SignInPassword, config.SignInGoogle
	// KeySet verifies signatures; nil uses Google's published securetoken
	// keys. Tests inject a static one.
	KeySet oidc.KeySet
	Now    func() time.Time
}

// IdentityPlatformSettingsFromConfig maps the process configuration.
func IdentityPlatformSettingsFromConfig(c config.Config) IdentityPlatformSettings {
	return IdentityPlatformSettings{ProjectID: c.IDPProjectID, TenantID: c.IDPTenantID, SignInMethods: c.IDPSignInMethods}
}

// IdentityPlatformVerifier verifies the ID tokens the SPA obtains from the
// Firebase Auth SDK (GCP Identity Platform).
type IdentityPlatformVerifier struct {
	verifier  *oidc.IDTokenVerifier
	cfg       IdentityPlatformSettings
	providers []string // firebase.sign_in_provider values that are allowed
	now       func() time.Time
}

// NewIdentityPlatformVerifier builds the verifier. Nothing is fetched until
// the first token is verified.
func NewIdentityPlatformVerifier(s IdentityPlatformSettings) *IdentityPlatformVerifier {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	keys := s.KeySet
	if keys == nil {
		// The key set is long lived and caches keys, so it gets a background
		// context carrying an HTTP client with a timeout, not a request's.
		hc := &http.Client{Timeout: oidcHTTPTimeout}
		keys = oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), hc), securetokenJWKSURL)
	}
	v := &IdentityPlatformVerifier{
		verifier: oidc.NewVerifier(securetokenIssuer+s.ProjectID, keys, &oidc.Config{ClientID: s.ProjectID, Now: now}),
		cfg:      s,
		now:      now,
	}
	for _, m := range s.SignInMethods {
		switch m {
		case config.SignInPassword:
			v.providers = append(v.providers, "password")
		case config.SignInGoogle:
			v.providers = append(v.providers, "google.com")
		}
	}
	return v
}

// Verify checks signature, issuer (securetoken.google.com/<project>), audience
// (<project>) and expiry, then the Identity Platform specifics: a fresh
// sign-in (auth_time), the tenant, and an allowed sign-in method. It returns
// ErrInvalidToken for any failure.
func (v *IdentityPlatformVerifier) Verify(ctx context.Context, rawToken string) (ExternalIdentity, error) {
	tok, err := v.verifier.Verify(ctx, rawToken)
	if err != nil || tok.Subject == "" {
		return ExternalIdentity{}, ErrInvalidToken
	}
	var claims struct {
		AuthTime int64  `json:"auth_time"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Firebase struct {
			Tenant         string `json:"tenant"`
			SignInProvider string `json:"sign_in_provider"`
		} `json:"firebase"`
	}
	if tok.Claims(&claims) != nil || claims.AuthTime <= 0 {
		return ExternalIdentity{}, ErrInvalidToken
	}
	authTime := time.Unix(claims.AuthTime, 0)
	if now := v.now(); now.Sub(authTime) > maxAuthAge || authTime.Sub(now) > authTimeSkew {
		return ExternalIdentity{}, ErrInvalidToken
	}
	if v.cfg.TenantID != "" && claims.Firebase.Tenant != v.cfg.TenantID {
		return ExternalIdentity{}, ErrInvalidToken
	}
	if !slices.Contains(v.providers, claims.Firebase.SignInProvider) {
		return ExternalIdentity{}, ErrInvalidToken
	}
	return ExternalIdentity{
		Issuer: tok.Issuer, Subject: tok.Subject,
		Email: strings.TrimSpace(claims.Email), DisplayName: claims.Name,
	}, nil
}
