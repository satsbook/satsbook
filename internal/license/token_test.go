package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
	"time"
)

func generateTestKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func TestSignAndVerifyToken(t *testing.T) {
	pub, priv := generateTestKeys(t)

	payload := TokenPayload{
		Key:       "sk_test_123",
		Tier:      "power",
		ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
	}

	token, err := SignToken(payload, priv)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	got, err := VerifyToken(token, pub)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}

	if got.Key != payload.Key {
		t.Errorf("Key = %q, want %q", got.Key, payload.Key)
	}
	if got.Tier != payload.Tier {
		t.Errorf("Tier = %q, want %q", got.Tier, payload.Tier)
	}
	if got.ExpiresAt != payload.ExpiresAt {
		t.Errorf("ExpiresAt = %d, want %d", got.ExpiresAt, payload.ExpiresAt)
	}
}

func TestVerifyToken_WrongKey(t *testing.T) {
	_, priv := generateTestKeys(t)
	otherPub, _ := generateTestKeys(t)

	token, err := SignToken(TokenPayload{
		Key:       "sk_test",
		Tier:      "pro",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}, priv)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	_, err = VerifyToken(token, otherPub)
	if err == nil {
		t.Fatal("expected error verifying with wrong public key")
	}
	if err.Error() != "invalid signature" {
		t.Errorf("error = %q, want %q", err.Error(), "invalid signature")
	}
}

func TestVerifyToken_Expired(t *testing.T) {
	pub, priv := generateTestKeys(t)

	token, err := SignToken(TokenPayload{
		Key:       "sk_test",
		Tier:      "pro",
		ExpiresAt: time.Now().Add(-time.Hour).Unix(), // expired 1 hour ago
	}, priv)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	_, err = VerifyToken(token, pub)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if err.Error() != "token expired" {
		t.Errorf("error = %q, want %q", err.Error(), "token expired")
	}
}

func TestVerifyToken_NoExpiry(t *testing.T) {
	pub, priv := generateTestKeys(t)

	token, err := SignToken(TokenPayload{
		Key:       "sk_test",
		Tier:      "power",
		ExpiresAt: 0, // no expiry
	}, priv)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	got, err := VerifyToken(token, pub)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if got.Tier != "power" {
		t.Errorf("Tier = %q, want %q", got.Tier, "power")
	}
}

func TestVerifyToken_TamperedPayload(t *testing.T) {
	pub, priv := generateTestKeys(t)

	token, err := SignToken(TokenPayload{
		Key:  "sk_test",
		Tier: "free",
	}, priv)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	// Tamper: replace payload with one that says "power"
	tampered := base64.RawURLEncoding.EncodeToString([]byte(`{"key":"sk_test","tier":"power","exp":0}`))
	origParts := splitToken(token)
	tamperedToken := tampered + "." + origParts[1]

	_, err = VerifyToken(tamperedToken, pub)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
	if err.Error() != "invalid signature" {
		t.Errorf("error = %q, want %q", err.Error(), "invalid signature")
	}
}

func splitToken(token string) [2]string {
	for i, c := range token {
		if c == '.' {
			return [2]string{token[:i], token[i+1:]}
		}
	}
	return [2]string{token, ""}
}

func TestVerifyToken_InvalidFormat(t *testing.T) {
	pub, _ := generateTestKeys(t)

	tests := []struct {
		name  string
		token string
	}{
		{"no dot", "nodothere"},
		{"empty", ""},
		{"bad base64 payload", "!!!.YWJj"},
		{"bad base64 sig", "YWJj.!!!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := VerifyToken(tt.token, pub)
			if err == nil {
				t.Error("expected error for invalid format")
			}
		})
	}
}

func TestVerifyTokenAt_CustomTime(t *testing.T) {
	pub, priv := generateTestKeys(t)

	expiry := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	token, err := SignToken(TokenPayload{
		Key:       "sk_test",
		Tier:      "pro",
		ExpiresAt: expiry.Unix(),
	}, priv)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	// Before expiry — should succeed
	before := expiry.Add(-24 * time.Hour)
	_, err = VerifyTokenAt(token, pub, before)
	if err != nil {
		t.Errorf("VerifyTokenAt before expiry should succeed: %v", err)
	}

	// After expiry — should fail
	after := expiry.Add(24 * time.Hour)
	_, err = VerifyTokenAt(token, pub, after)
	if err == nil {
		t.Error("VerifyTokenAt after expiry should fail")
	}
}
