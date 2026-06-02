package licensedb

import (
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndLookup(t *testing.T) {
	s := testStore(t)

	lic, err := s.Create("test@example.com", "pro", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if lic.LicenseKey[:3] != "sk_" {
		t.Errorf("key should start with sk_, got %s", lic.LicenseKey)
	}
	if len(lic.LicenseKey) != 27 { // "sk_" + 24 hex chars
		t.Errorf("key length should be 27, got %d", len(lic.LicenseKey))
	}
	if lic.Tier != "pro" {
		t.Errorf("tier should be pro, got %s", lic.Tier)
	}
	if lic.Status != "active" {
		t.Errorf("status should be active, got %s", lic.Status)
	}

	found, err := s.Lookup(lic.LicenseKey)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if found == nil {
		t.Fatal("expected license, got nil")
	}
	if found.Email != "test@example.com" {
		t.Errorf("email mismatch: %s", found.Email)
	}
	if found.Tier != "pro" {
		t.Errorf("tier mismatch: %s", found.Tier)
	}
}

func TestCreateWithStripeParams(t *testing.T) {
	s := testStore(t)

	lic, err := s.Create("stripe@test.com", "pro", &CreateParams{
		StripeCustomerID:     "cus_test123",
		StripeSubscriptionID: "sub_test456",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := s.Lookup(lic.LicenseKey)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if found.StripeCustomerID != "cus_test123" {
		t.Errorf("stripe customer id: got %q", found.StripeCustomerID)
	}
	if found.StripeSubscriptionID != "sub_test456" {
		t.Errorf("stripe subscription id: got %q", found.StripeSubscriptionID)
	}
}

func TestLookupByStripeSubscription(t *testing.T) {
	s := testStore(t)

	lic, _ := s.Create("sub@test.com", "power", &CreateParams{
		StripeSubscriptionID: "sub_lookup789",
	})

	found, err := s.LookupByStripeSubscription("sub_lookup789")
	if err != nil {
		t.Fatalf("lookup by sub: %v", err)
	}
	if found == nil {
		t.Fatal("expected license, got nil")
	}
	if found.LicenseKey != lic.LicenseKey {
		t.Errorf("key mismatch: %s vs %s", found.LicenseKey, lic.LicenseKey)
	}

	// Not found case
	notFound, err := s.LookupByStripeSubscription("sub_nonexistent")
	if err != nil {
		t.Fatalf("lookup by sub: %v", err)
	}
	if notFound != nil {
		t.Errorf("expected nil, got %+v", notFound)
	}
}

func TestLookupNotFound(t *testing.T) {
	s := testStore(t)

	found, err := s.Lookup("sk_nonexistent")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if found != nil {
		t.Errorf("expected nil, got %+v", found)
	}
}

func TestIsValid_Active(t *testing.T) {
	s := testStore(t)
	lic, _ := s.Create("a@b.com", "pro", nil)

	found, _ := s.Lookup(lic.LicenseKey)
	if !found.IsValid() {
		t.Error("active license should be valid")
	}
}

func TestIsValid_Cancelled(t *testing.T) {
	s := testStore(t)
	lic, _ := s.Create("a@b.com", "pro", nil)
	s.Revoke(lic.LicenseKey)

	found, _ := s.Lookup(lic.LicenseKey)
	if found.IsValid() {
		t.Error("cancelled license should not be valid")
	}
}

func TestIsValid_Expired(t *testing.T) {
	s := testStore(t)
	past := time.Now().Add(-24 * time.Hour)
	lic, _ := s.Create("a@b.com", "pro", &CreateParams{ExpiresAt: &past})

	found, _ := s.Lookup(lic.LicenseKey)
	if found.IsValid() {
		t.Error("expired license should not be valid")
	}
}

func TestIsValid_FutureExpiry(t *testing.T) {
	s := testStore(t)
	future := time.Now().Add(30 * 24 * time.Hour)
	lic, _ := s.Create("a@b.com", "pro", &CreateParams{ExpiresAt: &future})

	found, _ := s.Lookup(lic.LicenseKey)
	if !found.IsValid() {
		t.Error("license with future expiry should be valid")
	}
}

func TestIsValid_Nil(t *testing.T) {
	var l *License
	if l.IsValid() {
		t.Error("nil license should not be valid")
	}
}

func TestRevoke(t *testing.T) {
	s := testStore(t)
	lic, _ := s.Create("a@b.com", "pro", nil)

	if err := s.Revoke(lic.LicenseKey); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	found, _ := s.Lookup(lic.LicenseKey)
	if found.Status != "cancelled" {
		t.Errorf("expected cancelled, got %s", found.Status)
	}
}

func TestRevokeNotFound(t *testing.T) {
	s := testStore(t)
	if err := s.Revoke("sk_nope"); err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestUpgrade(t *testing.T) {
	s := testStore(t)
	lic, _ := s.Create("a@b.com", "pro", nil)

	if err := s.Upgrade(lic.LicenseKey, "power"); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	found, _ := s.Lookup(lic.LicenseKey)
	if found.Tier != "power" {
		t.Errorf("expected power, got %s", found.Tier)
	}
}

func TestUpgradeNotFound(t *testing.T) {
	s := testStore(t)
	if err := s.Upgrade("sk_nope", "power"); err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestList(t *testing.T) {
	s := testStore(t)
	s.Create("a@b.com", "pro", nil)
	s.Create("c@d.com", "power", nil)

	list, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 licenses, got %d", len(list))
	}
}
