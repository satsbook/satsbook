package licensedb_test

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/license"
	"github.com/satsbook/satsbook/internal/licensedb"
)

// TestValidateFlow tests the full flow: create key → validate → verify token.
func TestValidateFlow(t *testing.T) {
	store, err := licensedb.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// Create a license
	lic, err := store.Create("user@test.com", "pro", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Set up a validate handler (simulates the server)
	pub, priv, _ := ed25519.GenerateKey(nil)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key string `json:"key"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		found, _ := store.Lookup(req.Key)
		if !found.IsValid() {
			json.NewEncoder(w).Encode(map[string]any{"valid": false})
			return
		}

		token, _ := license.SignToken(license.TokenPayload{
			Key:       req.Key,
			Tier:      found.Tier,
			ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix(),
		}, priv)

		json.NewEncoder(w).Encode(map[string]any{
			"valid": true,
			"tier":  found.Tier,
			"token": token,
		})
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Hit the validate endpoint with a valid key
	body := `{"key":"` + lic.LicenseKey + `"}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var result struct {
		Valid bool   `json:"valid"`
		Tier  string `json:"tier"`
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if !result.Valid {
		t.Fatal("expected valid=true")
	}
	if result.Tier != "pro" {
		t.Errorf("expected pro, got %s", result.Tier)
	}

	// Verify the token signature
	payload, err := license.VerifyToken(result.Token, pub)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if payload.Tier != "pro" {
		t.Errorf("token tier: expected pro, got %s", payload.Tier)
	}

	// Now revoke and try again
	store.Revoke(lic.LicenseKey)
	body = `{"key":"` + lic.LicenseKey + `"}`
	resp2, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	var result2 struct {
		Valid bool `json:"valid"`
	}
	json.NewDecoder(resp2.Body).Decode(&result2)
	if result2.Valid {
		t.Error("revoked license should return valid=false")
	}
}
