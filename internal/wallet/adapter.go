package wallet

import (
	"context"
	"fmt"
)

// ScannerAdapter wraps a Scanner to implement the web.WalletScanner interface,
// handling key normalization before scanning.
type ScannerAdapter struct {
	scanner *Scanner
}

// NewScannerAdapter creates a new adapter around a Scanner.
func NewScannerAdapter(scanner *Scanner) *ScannerAdapter {
	return &ScannerAdapter{scanner: scanner}
}

// ScanAddress scans a single Bitcoin address and returns its balance in sats.
func (a *ScannerAdapter) ScanAddress(ctx context.Context, address string) (int64, error) {
	return a.scanner.ScanAddress(ctx, address)
}

// ScanXpub normalizes the key (xpub/ypub/zpub), determines derivation type,
// and scans both external and change branches.
func (a *ScannerAdapter) ScanXpub(ctx context.Context, key string, derivationTypeHint string) (int64, error) {
	xpub, dt, err := NormalizeKey(key)
	if err != nil {
		return 0, fmt.Errorf("normalize key: %w", err)
	}

	// Override with stored derivation type if the key was an xpub
	// (where auto-detection defaults to BIP44)
	if dt == DeriveBIP44 && derivationTypeHint != "" {
		switch derivationTypeHint {
		case "bip84":
			dt = DeriveBIP84
		case "bip49":
			dt = DeriveBIP49
		case "bip44":
			dt = DeriveBIP44
		}
	}

	result, err := a.scanner.ScanXpub(ctx, xpub, dt)
	if err != nil {
		return 0, err
	}

	return result.BalanceSats, nil
}

// ScanDescriptor is not supported via Electrum — descriptors require Bitcoin Core RPC.
func (a *ScannerAdapter) ScanDescriptor(ctx context.Context, descriptor string) (int64, error) {
	return 0, fmt.Errorf("descriptor scanning requires Bitcoin Core RPC (not available via Electrum)")
}
