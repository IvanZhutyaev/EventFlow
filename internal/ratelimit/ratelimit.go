// Package ratelimit implements an in-memory token-bucket rate limiter keyed by
// project (spec §16). MVP runs a single API instance, so in-memory state is
// sufficient; Redis-backed shared limiting arrives in v2.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a per-key token bucket.
type Limiter struct {
	mu      sync.Mutex
	rps     float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter builds a limiter allowing rps requests per second per key.
func NewLimiter(rps float64) *Limiter {
	return &Limiter{
		rps:     rps,
		buckets: make(map[string]*bucket),
	}
}

// Allow reports whether a request for the key may proceed.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.rps - 1, last: now}
		return true
	}

	// Refill tokens elapsed since the last call (capped at rps).
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.rps
	if b.tokens > l.rps {
		b.tokens = l.rps
	}
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
