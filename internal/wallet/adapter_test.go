package wallet

import (
	"context"
	"testing"
)

func TestScannerAdapter_ScanAddress(t *testing.T) {
	addr := "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
	sh, _ := AddressToScriptHash(addr)

	mock := &mockBalanceProvider{
		balances: map[string]int64{sh: 99000},
	}
	scanner := NewScanner(mock)
	adapter := NewScannerAdapter(scanner)

	balance, err := adapter.ScanAddress(context.Background(), addr)
	if err != nil {
		t.Fatalf("ScanAddress: %v", err)
	}
	if balance != 99000 {
		t.Errorf("balance = %d, want 99000", balance)
	}
}

func TestScannerAdapter_ScanXpub_Zpub(t *testing.T) {
	// Get first address script hash for our test zpub
	xpub, _, _ := NormalizeKey(testZpub)
	addrs, _ := DeriveAddresses(xpub, 0, 0, 1, DeriveBIP84)
	sh, _ := AddressToScriptHash(addrs[0])

	mock := &mockBalanceProvider{
		balances: map[string]int64{sh: 50000},
	}
	scanner := NewScanner(mock, WithGapLimit(5))
	adapter := NewScannerAdapter(scanner)

	balance, err := adapter.ScanXpub(context.Background(), testZpub, "bip84")
	if err != nil {
		t.Fatalf("ScanXpub: %v", err)
	}
	if balance != 50000 {
		t.Errorf("balance = %d, want 50000", balance)
	}
}

func TestScannerAdapter_ScanXpub_WithDerivationHint(t *testing.T) {
	// When given an xpub (BIP44 by default) with a bip84 hint,
	// it should use BIP84 derivation
	mock := &mockBalanceProvider{balances: map[string]int64{}}
	scanner := NewScanner(mock, WithGapLimit(3))
	adapter := NewScannerAdapter(scanner)

	// Should not error even with empty wallet
	balance, err := adapter.ScanXpub(context.Background(), testXpub, "bip84")
	if err != nil {
		t.Fatalf("ScanXpub with hint: %v", err)
	}
	if balance != 0 {
		t.Errorf("balance = %d, want 0", balance)
	}
}

func TestScannerAdapter_ScanXpub_InvalidKey(t *testing.T) {
	mock := &mockBalanceProvider{balances: map[string]int64{}}
	scanner := NewScanner(mock)
	adapter := NewScannerAdapter(scanner)

	_, err := adapter.ScanXpub(context.Background(), "invalid_key", "bip84")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}
