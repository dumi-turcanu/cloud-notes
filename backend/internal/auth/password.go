// Package auth implements built-in email + password authentication with
// server-side sessions (contract §4).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Current Argon2id parameters (OWASP minimum profile: 19 MiB, t=2, p=1).
const (
	argonMemoryKiB = 19456
	argonTime      = 2
	argonThreads   = 1
	argonSaltLen   = 16
	argonKeyLen    = 32

	// maxConcurrentHashes bounds peak memory (4 × 19 MiB).
	maxConcurrentHashes = 4

	// Upper bounds applied when parsing stored hashes, as a sanity guard.
	maxParseMemoryKiB = 1 << 21
	maxParseTime      = 16
	maxParseThreads   = 32
)

var errInvalidHash = errors.New("invalid argon2id hash")

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
}

var currentParams = argonParams{memory: argonMemoryKiB, time: argonTime, threads: argonThreads, keyLen: argonKeyLen}

// Hasher hashes and verifies passwords, limiting concurrent Argon2 operations.
type Hasher struct {
	sem chan struct{}
}

// NewHasher returns a Hasher with the standard concurrency limit.
func NewHasher() *Hasher { return &Hasher{sem: make(chan struct{}, maxConcurrentHashes)} }

func (h *Hasher) acquire(ctx context.Context) (release func(), err error) {
	select {
	case h.sem <- struct{}{}:
		return func() { <-h.sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Hash returns a PHC-encoded Argon2id hash of password using current parameters.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	return hashWith(password, currentParams)
}

func hashWith(password string, p argonParams) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify checks password against an encoded hash in constant time. A malformed
// hash returns an error (never a match).
func (h *Hasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	p, salt, want, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether encoded was made with parameters other than the
// current defaults (or cannot be parsed).
func NeedsRehash(encoded string) bool {
	p, _, _, err := parseHash(encoded)
	return err != nil || p != currentParams
}

func parseHash(encoded string) (p argonParams, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	// "", "argon2id", "v=19", "m=..,t=..,p=..", salt, key
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, errInvalidHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return p, nil, nil, errInvalidHash
	}
	var m, t, th uint64
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return p, nil, nil, errInvalidHash
	}
	for i, spec := range []struct {
		prefix string
		dst    *uint64
		max    uint64
	}{{"m=", &m, maxParseMemoryKiB}, {"t=", &t, maxParseTime}, {"p=", &th, maxParseThreads}} {
		if !strings.HasPrefix(params[i], spec.prefix) {
			return p, nil, nil, errInvalidHash
		}
		v, perr := strconv.ParseUint(strings.TrimPrefix(params[i], spec.prefix), 10, 32)
		if perr != nil || v == 0 || v > spec.max {
			return p, nil, nil, errInvalidHash
		}
		*spec.dst = v
	}
	salt, err = base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return p, nil, nil, errInvalidHash
	}
	key, err = base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < 16 {
		return p, nil, nil, errInvalidHash
	}
	return argonParams{memory: uint32(m), time: uint32(t), threads: uint8(th), keyLen: uint32(len(key))}, salt, key, nil
}
