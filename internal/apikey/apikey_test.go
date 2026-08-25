package apikey

import (
	"strings"
	"testing"
)

func TestGenerate_Format(t *testing.T) {
	raw, hash, prefix, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(raw, Prefix) {
		t.Errorf("raw key should start with %q, got %q", Prefix, raw)
	}
	// sbk_ (4) + 64 hex chars = 68
	if len(raw) != 68 {
		t.Errorf("expected raw key length 68, got %d", len(raw))
	}
	if len(hash) != 64 {
		t.Errorf("expected hash length 64, got %d", len(hash))
	}
	if !strings.HasPrefix(prefix, Prefix) {
		t.Errorf("prefix should start with %q, got %q", Prefix, prefix)
	}
}

func TestGenerate_Unique(t *testing.T) {
	raw1, _, _, _ := Generate()
	raw2, _, _, _ := Generate()
	if raw1 == raw2 {
		t.Error("two generated keys should not be equal")
	}
}

func TestHash_Deterministic(t *testing.T) {
	raw := "sbk_deadbeef"
	h1 := Hash(raw)
	h2 := Hash(raw)
	if h1 != h2 {
		t.Error("Hash should be deterministic")
	}
}

func TestHash_MatchesGenerate(t *testing.T) {
	raw, hash, _, _ := Generate()
	if Hash(raw) != hash {
		t.Error("Hash(raw) should match the hash returned by Generate")
	}
}

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < RateLimit; i++ {
		if !rl.Allow(1) {
			t.Fatalf("expected allow on request %d", i+1)
		}
	}
}

func TestRateLimiter_BlocksAtLimit(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < RateLimit; i++ {
		rl.Allow(1)
	}
	if rl.Allow(1) {
		t.Error("expected block after hitting rate limit")
	}
}

func TestRateLimiter_IndependentKeys(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < RateLimit; i++ {
		rl.Allow(1)
	}
	// Key 2 should still be allowed
	if !rl.Allow(2) {
		t.Error("key 2 should not be affected by key 1 being rate limited")
	}
}
