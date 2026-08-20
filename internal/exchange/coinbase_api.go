// Coinbase CDP API client.
//
// # Auth
//
// Uses Coinbase Developer Platform (CDP) API keys with Ed25519 JWT signing.
// Each request generates a short-lived JWT (120 s) signed with the private key.
// The user creates a key at cdp.coinbase.com with "Trade - View" permission.
// They supply:
//   - Key ID: UUID shown as "Key ID" on the CDP dashboard
//   - Private Key: the base64-encoded 64-byte Ed25519 private key (seed + public)
//
// # What is available
//
//   - GET /api/v3/brokerage/accounts  — all wallets; find BTC UUID and live balance
//   - GET /v2/accounts/{uuid}/transactions — full transaction history (paginated)
//
// Transaction types returned by the V2 API:
//   - "buy"    → USD-to-BTC purchase (maps to CoinbaseRow Type "buy")
//   - "sell"   → BTC-to-USD sale (maps to Type "sale")
//   - "trade"  → internal crypto-to-crypto conversion; positive BTC = acquiring (→ "buy"),
//                negative BTC = spending (→ "sale")
//   - "send"   → on-chain transfer; negative = outgoing (→ "send"), positive = incoming (→ "receive")
//
// # Deduplication
//
// API rows carry the Coinbase transaction UUID as TransactionID. ImportCoinbaseCSV uses
// this as the external_id when non-empty, enabling idempotent re-syncs. CSV-imported rows
// use the composite date|type|amount key and coexist without collision.
package exchange

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const coinbaseAPIBaseURL = "https://api.coinbase.com"
const coinbaseJWTTTL = 120 * time.Second

// CoinbaseAPIClient fetches BTC transaction history and live balance from the
// Coinbase CDP API using Ed25519 JWT authentication.
type CoinbaseAPIClient struct {
	keyID      string
	privateKey ed25519.PrivateKey
	client     *http.Client
	baseURL    string
	btcUUID    string // cached on first call
}

// CoinbaseAPIOption configures a CoinbaseAPIClient.
type CoinbaseAPIOption func(*CoinbaseAPIClient)

// WithCoinbaseBaseURL overrides the API base URL (used in tests).
func WithCoinbaseBaseURL(u string) CoinbaseAPIOption {
	return func(c *CoinbaseAPIClient) { c.baseURL = u }
}

// NewCoinbaseAPIClient creates a client from the CDP Key ID and base64-encoded Ed25519 private key.
// Returns an error if the private key cannot be decoded.
func NewCoinbaseAPIClient(keyID, privateKeyB64 string, opts ...CoinbaseAPIOption) (*CoinbaseAPIClient, error) {
	privBytes, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		return nil, fmt.Errorf("coinbase api: decode private key: %w", err)
	}
	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("coinbase api: private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(privBytes))
	}
	c := &CoinbaseAPIClient{
		keyID:      keyID,
		privateKey: ed25519.PrivateKey(privBytes),
		client:     &http.Client{Timeout: 30 * time.Second},
		baseURL:    coinbaseAPIBaseURL,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// FetchBalance returns the current BTC balance in satoshis from the live Coinbase account.
func (c *CoinbaseAPIClient) FetchBalance(ctx context.Context) (int64, error) {
	uuid, btcStr, err := c.findBTCAccount(ctx)
	if err != nil {
		return 0, err
	}
	c.btcUUID = uuid

	btc, err := strconv.ParseFloat(btcStr, 64)
	if err != nil {
		return 0, fmt.Errorf("coinbase api: parse BTC balance %q: %w", btcStr, err)
	}
	return int64(math.Round(btc * 1e8)), nil
}

// FetchRows returns all BTC transactions from Coinbase as CoinbaseRow values,
// paginating through the full history. Rows are idempotent via TransactionID.
func (c *CoinbaseAPIClient) FetchRows(ctx context.Context) ([]CoinbaseRow, error) {
	uuid := c.btcUUID
	if uuid == "" {
		var err error
		uuid, _, err = c.findBTCAccount(ctx)
		if err != nil {
			return nil, err
		}
		c.btcUUID = uuid
	}

	var rows []CoinbaseRow
	path := "/v2/accounts/" + uuid + "/transactions?limit=100"

	for path != "" {
		var page cbV2TxPage
		if err := c.get(ctx, path, &page); err != nil {
			return nil, err
		}
		for _, tx := range page.Data {
			row, ok := cbV2TxToRow(tx)
			if !ok {
				continue
			}
			rows = append(rows, row)
		}
		path = page.Pagination.NextURI
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	return rows, nil
}

// --- internal response types ---

type cbV2TxPage struct {
	Data []cbV2Transaction `json:"data"`
	Pagination struct {
		NextURI string `json:"next_uri"`
	} `json:"pagination"`
}

type cbV2Transaction struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Amount struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"amount"`
	NativeAmount struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"native_amount"`
	CreatedAt string `json:"created_at"`
}

type cbV3AccountsPage struct {
	Accounts []struct {
		UUID     string `json:"uuid"`
		Currency string `json:"currency"`
		AvailableBalance struct {
			Value    string `json:"value"`
			Currency string `json:"currency"`
		} `json:"available_balance"`
	} `json:"accounts"`
}

// cbV2TxToRow converts a V2 API transaction to a CoinbaseRow.
// Returns (row, false) for non-BTC, pending, or unrecognised transaction types.
func cbV2TxToRow(tx cbV2Transaction) (CoinbaseRow, bool) {
	if tx.Status != "completed" {
		return CoinbaseRow{}, false
	}
	if tx.Amount.Currency != "BTC" {
		return CoinbaseRow{}, false
	}

	btc, err := strconv.ParseFloat(tx.Amount.Amount, 64)
	if err != nil {
		return CoinbaseRow{}, false
	}
	usd, _ := strconv.ParseFloat(tx.NativeAmount.Amount, 64)

	t, err := time.Parse(time.RFC3339, tx.CreatedAt)
	if err != nil {
		t, _ = time.Parse("2006-01-02T15:04:05Z", tx.CreatedAt)
	}

	// Map API type + amount sign to CoinbaseRow.Type (always positive AmountBTC).
	var rowType string
	switch tx.Type {
	case "buy":
		rowType = "buy"
	case "sell":
		rowType = "sale"
	case "trade":
		if btc >= 0 {
			rowType = "buy" // acquiring BTC via internal crypto conversion
		} else {
			rowType = "sale" // spending BTC via internal crypto conversion
		}
	case "send":
		if btc < 0 {
			rowType = "send"
		} else {
			rowType = "receive"
		}
	default:
		return CoinbaseRow{}, false
	}

	absBTC := math.Abs(btc)
	absUSD := math.Abs(usd)
	amtSat := int64(math.Round(absBTC * 1e8))

	row := CoinbaseRow{
		TransactionID: tx.ID,
		Date:          t,
		Type:          rowType,
		AmountBTC:     absBTC,
		AmountSat:     amtSat,
		AmountUSD:     absUSD,
	}
	if rowType == "buy" {
		row.CostBasisUSD = absUSD
	}
	return row, true
}

// findBTCAccount fetches all brokerage accounts and returns the BTC wallet UUID
// and available balance string.
func (c *CoinbaseAPIClient) findBTCAccount(ctx context.Context) (uuid, balanceStr string, err error) {
	var page cbV3AccountsPage
	if err := c.get(ctx, "/api/v3/brokerage/accounts?limit=250", &page); err != nil {
		return "", "", err
	}
	for _, acc := range page.Accounts {
		if acc.Currency == "BTC" {
			return acc.UUID, acc.AvailableBalance.Value, nil
		}
	}
	return "", "", fmt.Errorf("coinbase api: no BTC account found")
}

// get performs an authenticated GET and JSON-decodes the response into dst.
func (c *CoinbaseAPIClient) get(ctx context.Context, path string, dst interface{}) error {
	jwtPath := path
	if i := strings.Index(path, "?"); i >= 0 {
		jwtPath = path[:i]
	}

	jwt, err := c.makeJWT("GET", jwtPath)
	if err != nil {
		return fmt.Errorf("coinbase api: build jwt: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("coinbase api: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("coinbase api: request %s: %w", path, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// ok
	case http.StatusUnauthorized:
		return fmt.Errorf("coinbase api: authentication failed (401) — check Key ID and private key")
	case http.StatusForbidden:
		return fmt.Errorf("coinbase api: forbidden (403) — API key is missing Trade - View permission")
	default:
		return fmt.Errorf("coinbase api: unexpected status %d for %s", resp.StatusCode, path)
	}

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("coinbase api: decode response from %s: %w", path, err)
	}
	return nil
}

// makeJWT builds and signs a short-lived EdDSA JWT for the CDP API.
func (c *CoinbaseAPIClient) makeJWT(method, path string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	now := time.Now().Unix()
	header := map[string]interface{}{
		"alg":   "EdDSA",
		"kid":   c.keyID,
		"nonce": fmt.Sprintf("%x", nonce),
		"typ":   "JWT",
	}
	payload := map[string]interface{}{
		"iss": "cdp",
		"nbf": now,
		"exp": now + int64(coinbaseJWTTTL.Seconds()),
		"sub": c.keyID,
		"uri": method + " api.coinbase.com" + path,
	}

	b64url := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

	hJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	msg := b64url(hJSON) + "." + b64url(pJSON)
	sig := ed25519.Sign(c.privateKey, []byte(msg))
	return msg + "." + b64url(sig), nil
}
