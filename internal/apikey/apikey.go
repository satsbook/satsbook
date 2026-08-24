// Package apikey handles generation, hashing, and rate limiting for Satsbook API keys.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

const (
	// Prefix for all Satsbook API keys.
	Prefix = "sbk_"
	// RateLimit is the maximum number of requests per minute per key.
	RateLimit = 100
)

// Generate creates a new random API key and returns the raw key, its SHA-256 hash,
// and a display prefix. The raw key is shown once and never stored.
func Generate() (raw, hash, prefix string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", fmt.Errorf("generate api key: %w", err)
	}
	raw = Prefix + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(h[:])
	prefix = raw[:len(Prefix)+8] // "sbk_" + first 8 hex chars
	return raw, hash, prefix, nil
}

// Hash returns the SHA-256 hex digest of a raw key string.
func Hash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// RateLimiter tracks request counts per API key using a sliding window.
type RateLimiter struct {
	mu      sync.Mutex
	windows map[int64][]time.Time
}

// NewRateLimiter creates a new per-key rate limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{windows: make(map[int64][]time.Time)}
}

// Allow returns true if the key with the given ID is within the rate limit (100 req/min).
func (r *RateLimiter) Allow(keyID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	ts := r.windows[keyID]
	j := 0
	for _, t := range ts {
		if t.After(cutoff) {
			ts[j] = t
			j++
		}
	}
	ts = ts[:j]

	if len(ts) >= RateLimit {
		r.windows[keyID] = ts
		return false
	}
	r.windows[keyID] = append(ts, now)
	return true
}
