package license

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockStore implements LicenseStore for testing.
type mockStore struct {
	cached *CachedLicense
	err    error
}

func (m *mockStore) GetCachedLicense(_ context.Context) (*CachedLicense, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.cached == nil {
		return &CachedLicense{Tier: string(TierFree)}, nil
	}
	return m.cached, nil
}

func (m *mockStore) UpdateCachedLicense(_ context.Context, cl *CachedLicense) error {
	if m.err != nil {
		return m.err
	}
	m.cached = cl
	return nil
}

// testKeys generates a keypair and returns common checker options for testing.
func testKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

// signedValidationServer creates a test HTTP server that returns signed tokens.
func signedValidationServer(t *testing.T, priv ed25519.PrivateKey, tier string, valid bool, tokenExpiry time.Time) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)

		resp := map[string]interface{}{"valid": valid, "tier": tier}

		if valid {
			token, err := SignToken(TokenPayload{
				Key:       req["key"],
				Tier:      tier,
				ExpiresAt: tokenExpiry.Unix(),
			}, priv)
			if err != nil {
				t.Fatalf("SignToken in test server: %v", err)
			}
			resp["token"] = token
		}

		json.NewEncoder(w).Encode(resp)
	}))
}

func TestNewChecker_NoKey_FreeImmediately(t *testing.T) {
	store := &mockStore{}
	checker := NewChecker(store, "")

	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}

	if got := checker.CurrentTier(); got != TierFree {
		t.Errorf("CurrentTier() = %q, want %q", got, TierFree)
	}
	if store.cached == nil || store.cached.Tier != string(TierFree) {
		t.Error("expected cache to be updated to free")
	}
}

func TestVerify_ValidKey_ServerReturnsPro(t *testing.T) {
	pub, priv := testKeys(t)
	srv := signedValidationServer(t, priv, "pro", true, time.Now().Add(24*time.Hour))
	defer srv.Close()

	store := &mockStore{}
	checker := NewChecker(store, "sk_test_123",
		WithValidationURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithPublicKey(pub),
	)

	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}

	if got := checker.CurrentTier(); got != TierPro {
		t.Errorf("CurrentTier() = %q, want %q", got, TierPro)
	}
	if store.cached.Tier != string(TierPro) {
		t.Errorf("cached tier = %q, want %q", store.cached.Tier, string(TierPro))
	}
	if store.cached.SignedToken == "" {
		t.Error("cached signed token should not be empty")
	}
}

func TestVerify_ValidKey_ServerReturnsPower(t *testing.T) {
	pub, priv := testKeys(t)
	srv := signedValidationServer(t, priv, "power", true, time.Now().Add(24*time.Hour))
	defer srv.Close()

	store := &mockStore{}
	checker := NewChecker(store, "sk_power_key",
		WithValidationURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithPublicKey(pub),
	)

	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}

	if got := checker.CurrentTier(); got != TierPower {
		t.Errorf("CurrentTier() = %q, want %q", got, TierPower)
	}
}

func TestVerify_ServerReturnsInvalid(t *testing.T) {
	pub, priv := testKeys(t)
	srv := signedValidationServer(t, priv, "", false, time.Time{})
	defer srv.Close()

	store := &mockStore{}
	checker := NewChecker(store, "sk_bad_key",
		WithValidationURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithPublicKey(pub),
	)

	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}

	if got := checker.CurrentTier(); got != TierFree {
		t.Errorf("CurrentTier() = %q, want %q", got, TierFree)
	}
}

func TestVerify_FakeServer_WrongSigningKey(t *testing.T) {
	realPub, _ := testKeys(t)
	_, fakePriv := testKeys(t) // different keypair

	// Fake server signs with the wrong key
	srv := signedValidationServer(t, fakePriv, "power", true, time.Now().Add(24*time.Hour))
	defer srv.Close()

	store := &mockStore{}
	checker := NewChecker(store, "sk_test",
		WithValidationURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithPublicKey(realPub), // app has the real public key
	)

	// Should fail verification and fall back to cache (which is empty = free)
	_ = checker.Verify(context.Background())

	if got := checker.CurrentTier(); got != TierFree {
		t.Errorf("CurrentTier() = %q, want %q (fake server should be rejected)", got, TierFree)
	}
}

func TestVerify_ServerReturns401_FallsBackToSignedCache(t *testing.T) {
	pub, priv := testKeys(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	now := time.Now()
	// Create a valid signed token for the cache
	token, _ := SignToken(TokenPayload{
		Key:       "sk_test_123",
		Tier:      "pro",
		ExpiresAt: now.Add(30 * 24 * time.Hour).Unix(), // token valid for 30 days
	}, priv)

	store := &mockStore{
		cached: &CachedLicense{
			LicenseKey:   "sk_test_123",
			Tier:         string(TierPro),
			SignedToken:  token,
			LastVerified: now.Add(-24 * time.Hour),
			ExpiresAt:    now.Add(6 * 24 * time.Hour),
		},
	}

	checker := NewChecker(store, "sk_test_123",
		WithValidationURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithPublicKey(pub),
		withNowFunc(func() time.Time { return now }),
	)

	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}

	if got := checker.CurrentTier(); got != TierPro {
		t.Errorf("CurrentTier() = %q, want %q (signed cache should be trusted)", got, TierPro)
	}
}

func TestVerify_NetworkError_GracePeriodActive_SignedToken(t *testing.T) {
	pub, priv := testKeys(t)

	now := time.Now()
	token, _ := SignToken(TokenPayload{
		Key:       "sk_test_123",
		Tier:      "power",
		ExpiresAt: now.Add(30 * 24 * time.Hour).Unix(),
	}, priv)

	store := &mockStore{
		cached: &CachedLicense{
			LicenseKey:   "sk_test_123",
			Tier:         string(TierPower),
			SignedToken:  token,
			LastVerified: now.Add(-48 * time.Hour),
			ExpiresAt:    now.Add(5 * 24 * time.Hour),
		},
	}

	checker := NewChecker(store, "sk_test_123",
		WithValidationURL("http://localhost:1"), // unreachable
		WithPublicKey(pub),
		withNowFunc(func() time.Time { return now }),
	)

	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}

	if got := checker.CurrentTier(); got != TierPower {
		t.Errorf("CurrentTier() = %q, want %q (grace period active, signed)", got, TierPower)
	}
}

func TestVerify_NetworkError_GracePeriodExpired(t *testing.T) {
	pub, priv := testKeys(t)

	now := time.Now()
	token, _ := SignToken(TokenPayload{
		Key:       "sk_test_123",
		Tier:      "power",
		ExpiresAt: now.Add(30 * 24 * time.Hour).Unix(),
	}, priv)

	store := &mockStore{
		cached: &CachedLicense{
			LicenseKey:   "sk_test_123",
			Tier:         string(TierPower),
			SignedToken:  token,
			LastVerified: now.Add(-10 * 24 * time.Hour),
			ExpiresAt:    now.Add(-3 * 24 * time.Hour), // expired 3 days ago
		},
	}

	checker := NewChecker(store, "sk_test_123",
		WithValidationURL("http://localhost:1"),
		WithPublicKey(pub),
		withNowFunc(func() time.Time { return now }),
	)

	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}

	if got := checker.CurrentTier(); got != TierFree {
		t.Errorf("CurrentTier() = %q, want %q (grace period expired)", got, TierFree)
	}
}

func TestVerify_KeyChanged_CacheMismatch(t *testing.T) {
	pub, priv := testKeys(t)

	now := time.Now()
	token, _ := SignToken(TokenPayload{
		Key:       "old_key",
		Tier:      "power",
		ExpiresAt: now.Add(30 * 24 * time.Hour).Unix(),
	}, priv)

	store := &mockStore{
		cached: &CachedLicense{
			LicenseKey:   "old_key",
			Tier:         string(TierPower),
			SignedToken:  token,
			LastVerified: now.Add(-24 * time.Hour),
			ExpiresAt:    now.Add(6 * 24 * time.Hour),
		},
	}

	checker := NewChecker(store, "new_key",
		WithValidationURL("http://localhost:1"),
		WithPublicKey(pub),
		withNowFunc(func() time.Time { return now }),
	)

	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error: %v", err)
	}

	if got := checker.CurrentTier(); got != TierFree {
		t.Errorf("CurrentTier() = %q, want %q (key mismatch)", got, TierFree)
	}
}

func TestVerify_CachedToken_TamperedInDB(t *testing.T) {
	pub, _ := testKeys(t)

	now := time.Now()
	store := &mockStore{
		cached: &CachedLicense{
			LicenseKey:   "sk_test",
			Tier:         string(TierPower),
			SignedToken:  "tampered.garbage", // not a valid signature
			LastVerified: now.Add(-24 * time.Hour),
			ExpiresAt:    now.Add(6 * 24 * time.Hour),
		},
	}

	checker := NewChecker(store, "sk_test",
		WithValidationURL("http://localhost:1"),
		WithPublicKey(pub),
		withNowFunc(func() time.Time { return now }),
	)

	_ = checker.Verify(context.Background())

	if got := checker.CurrentTier(); got != TierFree {
		t.Errorf("CurrentTier() = %q, want %q (tampered token should be rejected)", got, TierFree)
	}
}

func TestVerify_CachedToken_NoSignedToken(t *testing.T) {
	pub, _ := testKeys(t)

	now := time.Now()
	store := &mockStore{
		cached: &CachedLicense{
			LicenseKey:   "sk_test",
			Tier:         string(TierPower),
			SignedToken:  "", // no token
			LastVerified: now.Add(-24 * time.Hour),
			ExpiresAt:    now.Add(6 * 24 * time.Hour),
		},
	}

	checker := NewChecker(store, "sk_test",
		WithValidationURL("http://localhost:1"),
		WithPublicKey(pub),
		withNowFunc(func() time.Time { return now }),
	)

	_ = checker.Verify(context.Background())

	if got := checker.CurrentTier(); got != TierFree {
		t.Errorf("CurrentTier() = %q, want %q (no signed token = free)", got, TierFree)
	}
}

func TestFreeChecker(t *testing.T) {
	var c FreeChecker
	if got := c.CurrentTier(); got != TierFree {
		t.Errorf("FreeChecker.CurrentTier() = %q, want %q", got, TierFree)
	}
	if err := c.Verify(context.Background()); err != nil {
		t.Errorf("FreeChecker.Verify() error: %v", err)
	}
}

func TestCurrentTier_DefaultIsFree(t *testing.T) {
	store := &mockStore{}
	checker := NewChecker(store, "some-key")
	if got := checker.CurrentTier(); got != TierFree {
		t.Errorf("CurrentTier() before Verify = %q, want %q", got, TierFree)
	}
}
