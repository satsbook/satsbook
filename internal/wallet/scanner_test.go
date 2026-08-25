package wallet

import (
	"context"
	"fmt"
	"testing"
)

// mockBalanceProvider returns predefined balances for specific script hashes.
type mockBalanceProvider struct {
	// balances maps script hash → balance in sats
	balances map[string]int64
	// calls tracks how many GetBalance calls were made
	calls int
}

func (m *mockBalanceProvider) GetBalance(_ context.Context, scriptHash string) (int64, error) {
	m.calls++
	return m.balances[scriptHash], nil
}

func TestScanXpub_EmptyWallet(t *testing.T) {
	mock := &mockBalanceProvider{balances: map[string]int64{}}
	scanner := NewScanner(mock, WithGapLimit(5))

	result, err := scanner.ScanXpub(context.Background(), testXpub, DeriveBIP44)
	if err != nil {
		t.Fatalf("ScanXpub: %v", err)
	}

	if result.BalanceSats != 0 {
		t.Errorf("balance = %d, want 0", result.BalanceSats)
	}
	if result.AddressesUsed != 0 {
		t.Errorf("addresses used = %d, want 0", result.AddressesUsed)
	}
	// Should scan gap_limit addresses on each branch (external + change)
	// = 5 * 2 = 10 minimum calls
	if mock.calls < 10 {
		t.Errorf("expected at least 10 balance lookups, got %d", mock.calls)
	}
}

func TestScanXpub_WithBalance(t *testing.T) {
	// Pre-compute the script hash for the first external address
	addrs, err := DeriveAddresses(testXpub, 0, 0, 1, DeriveBIP44)
	if err != nil {
		t.Fatalf("DeriveAddresses: %v", err)
	}
	sh, err := AddressToScriptHash(addrs[0])
	if err != nil {
		t.Fatalf("AddressToScriptHash: %v", err)
	}

	mock := &mockBalanceProvider{
		balances: map[string]int64{
			sh: 100000, // 0.001 BTC on first address
		},
	}
	scanner := NewScanner(mock, WithGapLimit(5))

	result, err := scanner.ScanXpub(context.Background(), testXpub, DeriveBIP44)
	if err != nil {
		t.Fatalf("ScanXpub: %v", err)
	}

	if result.BalanceSats != 100000 {
		t.Errorf("balance = %d, want 100000", result.BalanceSats)
	}
	if result.AddressesUsed != 1 {
		t.Errorf("addresses used = %d, want 1", result.AddressesUsed)
	}
}

func TestScanXpub_SparseWallet(t *testing.T) {
	// Balances on addresses at index 0, 3, and 7 (gaps in between)
	indices := []int{0, 3, 7}
	balances := map[string]int64{}

	for _, idx := range indices {
		addrs, err := DeriveAddresses(testXpub, 0, idx, 1, DeriveBIP44)
		if err != nil {
			t.Fatalf("DeriveAddresses: %v", err)
		}
		sh, err := AddressToScriptHash(addrs[0])
		if err != nil {
			t.Fatalf("AddressToScriptHash: %v", err)
		}
		balances[sh] = 50000
	}

	mock := &mockBalanceProvider{balances: balances}
	scanner := NewScanner(mock, WithGapLimit(10))

	result, err := scanner.ScanXpub(context.Background(), testXpub, DeriveBIP44)
	if err != nil {
		t.Fatalf("ScanXpub: %v", err)
	}

	if result.BalanceSats != 150000 {
		t.Errorf("balance = %d, want 150000", result.BalanceSats)
	}
	if result.AddressesUsed != 3 {
		t.Errorf("addresses used = %d, want 3", result.AddressesUsed)
	}
}

func TestScanXpub_ChangeAddresses(t *testing.T) {
	// Balance on both external and change branch
	ext, _ := DeriveAddresses(testXpub, 0, 0, 1, DeriveBIP44)
	chg, _ := DeriveAddresses(testXpub, 1, 0, 1, DeriveBIP44)

	extSH, _ := AddressToScriptHash(ext[0])
	chgSH, _ := AddressToScriptHash(chg[0])

	mock := &mockBalanceProvider{
		balances: map[string]int64{
			extSH: 30000,
			chgSH: 20000,
		},
	}
	scanner := NewScanner(mock, WithGapLimit(5))

	result, err := scanner.ScanXpub(context.Background(), testXpub, DeriveBIP44)
	if err != nil {
		t.Fatalf("ScanXpub: %v", err)
	}

	if result.BalanceSats != 50000 {
		t.Errorf("balance = %d, want 50000", result.BalanceSats)
	}
}

func TestScanAddress(t *testing.T) {
	addr := "bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4"
	sh, _ := AddressToScriptHash(addr)

	mock := &mockBalanceProvider{
		balances: map[string]int64{sh: 42000},
	}
	scanner := NewScanner(mock)

	balance, err := scanner.ScanAddress(context.Background(), addr)
	if err != nil {
		t.Fatalf("ScanAddress: %v", err)
	}

	if balance != 42000 {
		t.Errorf("balance = %d, want 42000", balance)
	}
}

func TestScanAddress_Invalid(t *testing.T) {
	mock := &mockBalanceProvider{balances: map[string]int64{}}
	scanner := NewScanner(mock)

	_, err := scanner.ScanAddress(context.Background(), "notanaddress")
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestScanXpub_CancelledContext(t *testing.T) {
	mock := &mockBalanceProvider{balances: map[string]int64{}}
	scanner := NewScanner(mock, WithGapLimit(100))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := scanner.ScanXpub(ctx, testXpub, DeriveBIP44)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// errorBalanceProvider always returns an error.
type errorBalanceProvider struct{}

func (e *errorBalanceProvider) GetBalance(_ context.Context, _ string) (int64, error) {
	return 0, fmt.Errorf("connection refused")
}

func TestScanXpub_ElectrumError(t *testing.T) {
	scanner := NewScanner(&errorBalanceProvider{}, WithGapLimit(5))

	_, err := scanner.ScanXpub(context.Background(), testXpub, DeriveBIP44)
	if err == nil {
		t.Fatal("expected error when balance provider fails")
	}
}

func TestNewScanner_WithLogger(t *testing.T) {
	// WithLogger should be accepted without error; passing nil is valid
	// (scanner.go falls back to log.Default() for nil in the option).
	mock := &mockBalanceProvider{balances: map[string]int64{}}
	scanner := NewScanner(mock, WithGapLimit(3), WithLogger(nil))
	if scanner == nil {
		t.Fatal("expected non-nil scanner")
	}
	if scanner.gapLimit != 3 {
		t.Errorf("gapLimit = %d, want 3", scanner.gapLimit)
	}
}
