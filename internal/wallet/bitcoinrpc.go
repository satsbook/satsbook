package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// BitcoinRPCScanner implements web.WalletScanner using Bitcoin Core's
// scantxoutset RPC. Works on pruned nodes — no electrs required.
type BitcoinRPCScanner struct {
	url    string
	user   string
	pass   string
	cookie string // path to .cookie file, re-read each call
	client *http.Client
	logger *log.Logger
}

// BitcoinRPCOption configures a BitcoinRPCScanner.
type BitcoinRPCOption func(*BitcoinRPCScanner)

// WithCookieAuth sets cookie-file authentication.
func WithCookieAuth(path string) BitcoinRPCOption {
	return func(s *BitcoinRPCScanner) {
		s.cookie = path
	}
}

// WithUserPassAuth sets username/password authentication.
func WithUserPassAuth(user, pass string) BitcoinRPCOption {
	return func(s *BitcoinRPCScanner) {
		s.user = user
		s.pass = pass
	}
}

// WithRPCLogger sets the logger.
func WithRPCLogger(l *log.Logger) BitcoinRPCOption {
	return func(s *BitcoinRPCScanner) {
		s.logger = l
	}
}

// WithRPCTimeout sets the HTTP client timeout.
func WithRPCTimeout(d time.Duration) BitcoinRPCOption {
	return func(s *BitcoinRPCScanner) {
		s.client.Timeout = d
	}
}

// NewBitcoinRPCScanner creates a scanner that queries balances via Bitcoin Core RPC.
func NewBitcoinRPCScanner(host string, port int, opts ...BitcoinRPCOption) *BitcoinRPCScanner {
	s := &BitcoinRPCScanner{
		url:    fmt.Sprintf("http://%s:%d", host, port),
		client: &http.Client{Timeout: 90 * time.Second},
		logger: log.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ScanAddress returns the balance in sats for a single Bitcoin address.
func (s *BitcoinRPCScanner) ScanAddress(ctx context.Context, address string) (int64, error) {
	desc := fmt.Sprintf("addr(%s)", address)
	descriptors := []interface{}{
		map[string]interface{}{"desc": desc, "range": 0},
	}
	return s.scan(ctx, descriptors)
}

// ScanXpub returns the total balance in sats for an extended public key,
// scanning both external and change branches.
func (s *BitcoinRPCScanner) ScanXpub(ctx context.Context, key string, derivationTypeHint string) (int64, error) {
	xpub, dt, err := NormalizeKey(key)
	if err != nil {
		return 0, fmt.Errorf("normalize key: %w", err)
	}

	// Override with stored derivation type if the key was an xpub
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

	// Build descriptors for both external (0/*) and change (1/*) branches
	var descriptors []interface{}
	for _, branch := range []int{0, 1} {
		desc := xpubDescriptor(xpub, branch, dt)
		descriptors = append(descriptors, map[string]interface{}{
			"desc":  desc,
			"range": 1000,
		})
	}

	return s.scan(ctx, descriptors)
}

// ScanDescriptor returns the total balance in sats for a raw output descriptor.
// The descriptor is passed directly to scantxoutset with a range of 1000.
func (s *BitcoinRPCScanner) ScanDescriptor(ctx context.Context, descriptor string) (int64, error) {
	descriptors := []interface{}{
		map[string]interface{}{"desc": descriptor, "range": 1000},
	}
	return s.scan(ctx, descriptors)
}

// xpubDescriptor builds the output descriptor for scantxoutset.
func xpubDescriptor(xpub string, branch int, dt DerivationType) string {
	path := fmt.Sprintf("%s/%d/*", xpub, branch)
	switch dt {
	case DeriveBIP84:
		return fmt.Sprintf("wpkh(%s)", path)
	case DeriveBIP49:
		return fmt.Sprintf("sh(wpkh(%s))", path)
	default: // BIP44
		return fmt.Sprintf("combo(%s)", path)
	}
}

// scan calls scantxoutset and returns the total balance in sats.
func (s *BitcoinRPCScanner) scan(ctx context.Context, descriptors []interface{}) (int64, error) {
	result, err := s.callRPC(ctx, "scantxoutset", []interface{}{"start", descriptors})
	if err != nil {
		return 0, err
	}

	var scanResult struct {
		Success     bool    `json:"success"`
		TotalAmount float64 `json:"total_amount"`
	}
	if err := json.Unmarshal(result, &scanResult); err != nil {
		return 0, fmt.Errorf("unmarshal scantxoutset result: %w", err)
	}

	if !scanResult.Success {
		return 0, fmt.Errorf("scantxoutset returned success=false")
	}

	return btcToSats(scanResult.TotalAmount), nil
}

// btcToSats converts a BTC float to satoshis.
func btcToSats(btc float64) int64 {
	return int64(math.Round(btc * 1e8))
}

// rpcRequest is the JSON-RPC 1.0 request format for Bitcoin Core.
type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      string        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

// rpcResponse is the JSON-RPC response format.
type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// callRPC sends a JSON-RPC request to Bitcoin Core.
func (s *BitcoinRPCScanner) callRPC(ctx context.Context, method string, params []interface{}) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "1.0",
		ID:      "satsbook",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	auth, err := s.authHeader()
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bitcoin rpc request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("bitcoin rpc auth failed (401): check rpc credentials or cookie path")
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if rpcResp.Error != nil {
		if rpcResp.Error.Code == -32601 {
			return nil, fmt.Errorf("scantxoutset requires Bitcoin Core v0.17+")
		}
		return nil, fmt.Errorf("bitcoin rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

// authHeader returns the Basic auth header value.
func (s *BitcoinRPCScanner) authHeader() (string, error) {
	var user, pass string

	if s.cookie != "" {
		data, err := os.ReadFile(s.cookie)
		if err != nil {
			return "", fmt.Errorf("read cookie file: %w (ensure bitcoind is running)", err)
		}
		parts := strings.SplitN(strings.TrimSpace(string(data)), ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid cookie file format")
		}
		user, pass = parts[0], parts[1]
	} else {
		user, pass = s.user, s.pass
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	return "Basic " + encoded, nil
}
