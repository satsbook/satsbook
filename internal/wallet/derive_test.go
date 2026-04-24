package wallet

import (
	"testing"
)

// Test vectors generated from seed 000102030405060708090a0b0c0d0e0f
// using standard BIP44/49/84 derivation paths.
const (
	testXpub = "xpub6CDEarkRoiwWPj3n3gYygGwgoGchxYg3g6Zs5L2nB4B6wdojzcWCKKHMu9XuY1GyYygRfrVembjAko1T5xTsxj7ecKXxEPzDxx7nCK8Dxtx"
	testYpub = "ypub6X72NFZXyacDVkCZu4gNxmiPFJkqaKe5etAB1DDo6mtoEm9FugxbgpGAPFxNLvCmuNs7YpnA69YVo7iEGPnX1HSL6y5gtVFcxZFHRqGZsPs"
	testZpub = "zpub6qfp6hKyMTw1jdnUQGr4xihYxp7rQmAPp67pk4YYAcZBdRisqyaqh1Z2N1RVCNtEVW6c4eLuPZctjUx3QVBQEQPFNaR5uvumrzUbGRQ8voQ"
)

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantType   DerivationType
		wantErr    bool
		wantPrefix string
	}{
		{
			name:       "xpub passthrough",
			key:        testXpub,
			wantType:   DeriveBIP44,
			wantPrefix: "xpub",
		},
		{
			name:       "zpub to xpub",
			key:        testZpub,
			wantType:   DeriveBIP84,
			wantPrefix: "xpub",
		},
		{
			name:       "ypub to xpub",
			key:        testYpub,
			wantType:   DeriveBIP49,
			wantPrefix: "xpub",
		},
		{
			name:    "too short",
			key:     "abc",
			wantErr: true,
		},
		{
			name:    "empty string",
			key:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xpub, dt, err := NormalizeKey(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dt != tt.wantType {
				t.Errorf("derivation type = %s, want %s", dt, tt.wantType)
			}
			if tt.wantPrefix != "" && len(xpub) >= 4 && xpub[:4] != tt.wantPrefix {
				t.Errorf("xpub prefix = %s, want %s", xpub[:4], tt.wantPrefix)
			}
		})
	}
}

func TestNormalizeKey_RoundTrip(t *testing.T) {
	// Normalizing an xpub should return the same key
	xpub, _, err := NormalizeKey(testXpub)
	if err != nil {
		t.Fatalf("NormalizeKey: %v", err)
	}
	if xpub != testXpub {
		t.Errorf("xpub round-trip failed: got %s, want %s", xpub, testXpub)
	}
}

func TestDeriveAddresses_BIP84(t *testing.T) {
	xpub, dt, err := NormalizeKey(testZpub)
	if err != nil {
		t.Fatalf("NormalizeKey: %v", err)
	}
	if dt != DeriveBIP84 {
		t.Fatalf("expected BIP84, got %s", dt)
	}

	addrs, err := DeriveAddresses(xpub, 0, 0, 3, DeriveBIP84)
	if err != nil {
		t.Fatalf("DeriveAddresses: %v", err)
	}

	if len(addrs) != 3 {
		t.Fatalf("expected 3 addresses, got %d", len(addrs))
	}

	for i, addr := range addrs {
		if len(addr) < 4 || addr[:4] != "bc1q" {
			t.Errorf("address[%d] = %s, expected bc1q... prefix", i, addr)
		}
	}

	// Addresses should be unique
	assertUniqueAddresses(t, addrs)
}

func TestDeriveAddresses_BIP49(t *testing.T) {
	xpub, dt, err := NormalizeKey(testYpub)
	if err != nil {
		t.Fatalf("NormalizeKey: %v", err)
	}
	if dt != DeriveBIP49 {
		t.Fatalf("expected BIP49, got %s", dt)
	}

	addrs, err := DeriveAddresses(xpub, 0, 0, 3, DeriveBIP49)
	if err != nil {
		t.Fatalf("DeriveAddresses: %v", err)
	}

	for i, addr := range addrs {
		if addr[0] != '3' {
			t.Errorf("address[%d] = %s, expected 3... prefix", i, addr)
		}
	}

	assertUniqueAddresses(t, addrs)
}

func TestDeriveAddresses_BIP44(t *testing.T) {
	addrs, err := DeriveAddresses(testXpub, 0, 0, 3, DeriveBIP44)
	if err != nil {
		t.Fatalf("DeriveAddresses: %v", err)
	}

	for i, addr := range addrs {
		if addr[0] != '1' {
			t.Errorf("address[%d] = %s, expected 1... prefix", i, addr)
		}
	}

	assertUniqueAddresses(t, addrs)
}

func TestDeriveAddresses_KnownFirstAddress(t *testing.T) {
	// Known first external address for testXpub (verified with gen_vectors)
	addrs, err := DeriveAddresses(testXpub, 0, 0, 1, DeriveBIP44)
	if err != nil {
		t.Fatalf("DeriveAddresses: %v", err)
	}

	if addrs[0] != "1NQpH6Nf8QtR2HphLRcvuVqfhXBXsiWn8r" {
		t.Errorf("first address = %s, want 1NQpH6Nf8QtR2HphLRcvuVqfhXBXsiWn8r", addrs[0])
	}
}

func TestDeriveAddresses_ChangeBranch(t *testing.T) {
	external, err := DeriveAddresses(testXpub, 0, 0, 1, DeriveBIP44)
	if err != nil {
		t.Fatalf("DeriveAddresses external: %v", err)
	}

	change, err := DeriveAddresses(testXpub, 1, 0, 1, DeriveBIP44)
	if err != nil {
		t.Fatalf("DeriveAddresses change: %v", err)
	}

	if external[0] == change[0] {
		t.Errorf("external and change addresses should differ")
	}
}

func TestDeriveAddresses_ZeroCount(t *testing.T) {
	addrs, err := DeriveAddresses(testXpub, 0, 0, 0, DeriveBIP44)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addrs) != 0 {
		t.Errorf("expected 0 addresses, got %d", len(addrs))
	}
}

func TestAddressToScriptHash(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{
			name:    "P2PKH",
			address: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		},
		{
			name:    "P2WPKH",
			address: "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4",
		},
		{
			name:    "P2SH",
			address: "3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
		},
		{
			name:    "invalid",
			address: "notanaddress",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sh, err := AddressToScriptHash(tt.address)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(sh) != 64 {
				t.Errorf("script hash length = %d, want 64", len(sh))
			}
		})
	}
}

func TestAddressToScriptHash_Deterministic(t *testing.T) {
	sh1, _ := AddressToScriptHash("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4")
	sh2, _ := AddressToScriptHash("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4")
	if sh1 != sh2 {
		t.Errorf("script hash not deterministic: %s != %s", sh1, sh2)
	}
}

func assertUniqueAddresses(t *testing.T, addrs []string) {
	t.Helper()
	seen := make(map[string]bool)
	for _, addr := range addrs {
		if seen[addr] {
			t.Errorf("duplicate address: %s", addr)
		}
		seen[addr] = true
	}
}
