package db

import (
	"context"
	"testing"
	"time"
)

func TestGetCachedLicense_Default(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	cl, err := d.GetCachedLicense(context.Background())
	if err != nil {
		t.Fatalf("GetCachedLicense() error: %v", err)
	}
	if cl.Tier != "free" {
		t.Errorf("Tier = %q, want %q", cl.Tier, "free")
	}
	if cl.LicenseKey != "" {
		t.Errorf("LicenseKey = %q, want empty", cl.LicenseKey)
	}
	if !cl.LastVerified.IsZero() {
		t.Errorf("LastVerified should be zero, got %v", cl.LastVerified)
	}
	if !cl.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt should be zero, got %v", cl.ExpiresAt)
	}
}

func TestUpdateAndGetCachedLicense(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(7 * 24 * time.Hour)

	err := d.UpdateCachedLicense(ctx, &CachedLicense{
		LicenseKey:   "sk_test_abc123",
		Tier:         "power",
		LastVerified: now,
		ExpiresAt:    expires,
	})
	if err != nil {
		t.Fatalf("UpdateCachedLicense() error: %v", err)
	}

	cl, err := d.GetCachedLicense(ctx)
	if err != nil {
		t.Fatalf("GetCachedLicense() error: %v", err)
	}

	if cl.LicenseKey != "sk_test_abc123" {
		t.Errorf("LicenseKey = %q, want %q", cl.LicenseKey, "sk_test_abc123")
	}
	if cl.Tier != "power" {
		t.Errorf("Tier = %q, want %q", cl.Tier, "power")
	}
	if !cl.LastVerified.Equal(now) {
		t.Errorf("LastVerified = %v, want %v", cl.LastVerified, now)
	}
	if !cl.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", cl.ExpiresAt, expires)
	}
}

func TestUpdateCachedLicense_Overwrite(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)

	// First write: power tier
	err := d.UpdateCachedLicense(ctx, &CachedLicense{
		LicenseKey:   "key1",
		Tier:         "power",
		LastVerified: now,
		ExpiresAt:    now.Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("first UpdateCachedLicense() error: %v", err)
	}

	// Overwrite: downgrade to free
	err = d.UpdateCachedLicense(ctx, &CachedLicense{
		LicenseKey:   "",
		Tier:         "free",
		LastVerified: time.Time{},
		ExpiresAt:    time.Time{},
	})
	if err != nil {
		t.Fatalf("second UpdateCachedLicense() error: %v", err)
	}

	cl, err := d.GetCachedLicense(ctx)
	if err != nil {
		t.Fatalf("GetCachedLicense() error: %v", err)
	}
	if cl.Tier != "free" {
		t.Errorf("Tier = %q, want %q after downgrade", cl.Tier, "free")
	}
	if cl.LicenseKey != "" {
		t.Errorf("LicenseKey = %q, want empty after downgrade", cl.LicenseKey)
	}
}
