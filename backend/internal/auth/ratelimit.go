package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	limiterIdleTTL       = 10 * time.Minute
	limiterSweepInterval = time.Minute
)

// RateLimiter is a per-key (client IP) token bucket. State is in memory and
// therefore per replica (best effort); idle entries are evicted.
type RateLimiter struct {
	mu        sync.Mutex
	limit     rate.Limit
	burst     int
	now       func() time.Time
	entries   map[string]*limiterEntry
	lastSweep time.Time
}

type limiterEntry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter allows perMinute requests per minute per key with burst perMinute.
func NewRateLimiter(perMinute int, now func() time.Time) *RateLimiter {
	return &RateLimiter{
		limit:     rate.Limit(float64(perMinute) / 60),
		burst:     perMinute,
		now:       now,
		entries:   make(map[string]*limiterEntry),
		lastSweep: now(),
	}
}

// Allow consumes one token for key and reports whether the request may proceed.
func (l *RateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweepLocked(now)
	e, ok := l.entries[key]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(l.limit, l.burst)}
		l.entries[key] = e
	}
	e.lastSeen = now
	return e.lim.AllowN(now, 1)
}

func (l *RateLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < limiterSweepInterval {
		return
	}
	l.lastSweep = now
	for k, e := range l.entries {
		if now.Sub(e.lastSeen) > limiterIdleTTL {
			delete(l.entries, k)
		}
	}
}

// Len returns the number of tracked keys (for tests).
func (l *RateLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
