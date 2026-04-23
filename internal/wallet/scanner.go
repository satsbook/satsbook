package wallet

import (
	"context"
	"fmt"
	"log"
)

const (
	// DefaultGapLimit is the BIP44 standard gap limit — stop scanning
	// after this many consecutive addresses with zero balance.
	DefaultGapLimit = 20

	// DefaultBatchSize is the number of addresses to query in parallel.
	DefaultBatchSize = 10

	// MaxAddressesPerPath caps the total addresses derived per branch
	// to prevent runaway scanning.
	MaxAddressesPerPath = 500
)

// BalanceProvider queries the balance for a given Electrum script hash.
type BalanceProvider interface {
	GetBalance(ctx context.Context, scriptHash string) (int64, error)
}

// Scanner scans extended public keys for balances using Electrum.
type Scanner struct {
	balance  BalanceProvider
	gapLimit int
	logger   *log.Logger
}

// ScannerOption configures a Scanner.
type ScannerOption func(*Scanner)

// WithGapLimit sets the gap limit for address scanning.
func WithGapLimit(n int) ScannerOption {
	return func(s *Scanner) {
		s.gapLimit = n
	}
}

// WithLogger sets the logger for the scanner.
func WithLogger(l *log.Logger) ScannerOption {
	return func(s *Scanner) {
		s.logger = l
	}
}

// NewScanner creates a Scanner that queries balances via the given provider.
func NewScanner(balance BalanceProvider, opts ...ScannerOption) *Scanner {
	s := &Scanner{
		balance:  balance,
		gapLimit: DefaultGapLimit,
		logger:   log.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ScanResult holds the result of scanning an extended public key.
type ScanResult struct {
	BalanceSats    int64
	AddressesUsed int
}

// ScanXpub scans an extended public key (already normalized to xpub) on both
// external (m/0/i) and change (m/1/i) branches and returns the total balance.
func (s *Scanner) ScanXpub(ctx context.Context, xpub string, derivationType DerivationType) (*ScanResult, error) {
	result := &ScanResult{}

	for _, branch := range []uint32{0, 1} {
		branchResult, err := s.scanBranch(ctx, xpub, branch, derivationType)
		if err != nil {
			return nil, fmt.Errorf("scan branch %d: %w", branch, err)
		}
		result.BalanceSats += branchResult.BalanceSats
		result.AddressesUsed += branchResult.AddressesUsed
	}

	return result, nil
}

// ScanAddress scans a single Bitcoin address and returns its balance.
func (s *Scanner) ScanAddress(ctx context.Context, address string) (int64, error) {
	scriptHash, err := AddressToScriptHash(address)
	if err != nil {
		return 0, fmt.Errorf("address to script hash: %w", err)
	}

	balance, err := s.balance.GetBalance(ctx, scriptHash)
	if err != nil {
		return 0, fmt.Errorf("get balance for %s: %w", address, err)
	}

	return balance, nil
}

// scanBranch scans a single branch (external or change) with the gap limit.
func (s *Scanner) scanBranch(ctx context.Context, xpub string, branch uint32, dt DerivationType) (*ScanResult, error) {
	result := &ScanResult{}
	gap := 0
	index := 0

	for gap < s.gapLimit && index < MaxAddressesPerPath {
		// Derive a batch of addresses
		batchSize := s.gapLimit - gap
		if batchSize > DefaultBatchSize {
			batchSize = DefaultBatchSize
		}
		remaining := MaxAddressesPerPath - index
		if batchSize > remaining {
			batchSize = remaining
		}

		addrs, err := DeriveAddresses(xpub, branch, index, batchSize, dt)
		if err != nil {
			return nil, fmt.Errorf("derive addresses at index %d: %w", index, err)
		}

		// Query each address balance
		for _, addr := range addrs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			scriptHash, err := AddressToScriptHash(addr)
			if err != nil {
				return nil, fmt.Errorf("address to script hash at index %d: %w", index, err)
			}

			balance, err := s.balance.GetBalance(ctx, scriptHash)
			if err != nil {
				return nil, fmt.Errorf("get balance at index %d: %w", index, err)
			}

			if balance > 0 {
				result.BalanceSats += balance
				result.AddressesUsed++
				gap = 0
			} else {
				gap++
			}

			index++

			if gap >= s.gapLimit {
				break
			}
		}
	}

	return result, nil
}
