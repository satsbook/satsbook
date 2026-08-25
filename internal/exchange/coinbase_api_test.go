package exchange

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- helpers ---

// newTestKey generates a random Ed25519 key pair and returns (keyID, base64-encoded private key).
func newTestKey(t *testing.T) (string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	_ = pub
	return "test-key-id", base64.StdEncoding.EncodeToString([]byte(priv))
}

// cbAccountsResponse builds a JSON /api/v3/brokerage/accounts response.
func cbAccountsResponse(uuid, btcBalance string) map[string]interface{} {
	return map[string]interface{}{
		"accounts": []interface{}{
			map[string]interface{}{
				"uuid":     "usd-acc-uuid",
				"currency": "USD",
				"available_balance": map[string]interface{}{
					"value":    "100.00",
					"currency": "USD",
				},
			},
			map[string]interface{}{
				"uuid":     uuid,
				"currency": "BTC",
				"available_balance": map[string]interface{}{
					"value":    btcBalance,
					"currency": "BTC",
				},
			},
		},
	}
}

// cbTxPageResponse builds a JSON /v2/accounts/{uuid}/transactions response.
func cbTxPageResponse(txns []map[string]interface{}, nextURI string) map[string]interface{} {
	return map[string]interface{}{
		"data": txns,
		"pagination": map[string]interface{}{
			"next_uri": nextURI,
		},
	}
}

// cbTx builds a fake V2 API transaction.
func cbTx(id, txType, status, btcAmount, usdAmount, createdAt string) map[string]interface{} {
	return map[string]interface{}{
		"id":     id,
		"type":   txType,
		"status": status,
		"amount": map[string]string{
			"amount":   btcAmount,
			"currency": "BTC",
		},
		"native_amount": map[string]string{
			"amount":   usdAmount,
			"currency": "USD",
		},
		"created_at": createdAt,
	}
}

// --- NewCoinbaseAPIClient tests (Issue #53: invalid key detection) ---

func TestNewCoinbaseAPIClient_ValidKey(t *testing.T) {
	keyID, privB64 := newTestKey(t)

	client, err := NewCoinbaseAPIClient(keyID, privB64)
	if err != nil {
		t.Fatalf("unexpected error for valid key: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewCoinbaseAPIClient_InvalidBase64(t *testing.T) {
	_, err := NewCoinbaseAPIClient("kid", "not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
}

func TestNewCoinbaseAPIClient_WrongKeyLength(t *testing.T) {
	// Only 16 bytes — should fail length check (need 64 for ed25519)
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := NewCoinbaseAPIClient("kid", shortKey)
	if err == nil {
		t.Fatal("expected error for wrong key length, got nil")
	}
}

func TestWithCoinbaseBaseURL(t *testing.T) {
	keyID, privB64 := newTestKey(t)
	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL("https://test.example.com"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.baseURL != "https://test.example.com" {
		t.Errorf("baseURL = %q, want https://test.example.com", client.baseURL)
	}
}

// --- cbV2TxToRow tests (Issue #53: type mapping and amount correctness) ---

func TestCbV2TxToRow_Buy(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-buy-1",
		Type:   "buy",
		Status: "completed",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "0.001", Currency: "BTC"},
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "30.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	row, ok := cbV2TxToRow(tx)
	if !ok {
		t.Fatal("expected ok=true for buy")
	}
	if row.Type != "buy" {
		t.Errorf("Type = %q, want buy", row.Type)
	}
	if row.TransactionID != "tx-buy-1" {
		t.Errorf("TransactionID = %q, want tx-buy-1", row.TransactionID)
	}
	if row.AmountSat != 100000 {
		t.Errorf("AmountSat = %d, want 100000", row.AmountSat)
	}
	if math.Abs(row.AmountBTC-0.001) > 1e-9 {
		t.Errorf("AmountBTC = %f, want 0.001", row.AmountBTC)
	}
	// Cost basis should be set for buys
	if math.Abs(row.CostBasisUSD-30.00) > 0.001 {
		t.Errorf("CostBasisUSD = %f, want 30.00", row.CostBasisUSD)
	}
}

func TestCbV2TxToRow_Sell(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-sell-1",
		Type:   "sell",
		Status: "completed",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "-0.001", Currency: "BTC"},
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "-25.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	row, ok := cbV2TxToRow(tx)
	if !ok {
		t.Fatal("expected ok=true for sell")
	}
	if row.Type != "sale" {
		t.Errorf("Type = %q, want sale", row.Type)
	}
	// Amount should be absolute value (positive)
	if row.AmountSat != 100000 {
		t.Errorf("AmountSat = %d, want 100000 (absolute)", row.AmountSat)
	}
	// Cost basis not set for sells
	if row.CostBasisUSD != 0 {
		t.Errorf("CostBasisUSD = %f, want 0 for sell", row.CostBasisUSD)
	}
}

func TestCbV2TxToRow_Trade_AcquiringBTC(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-trade-buy",
		Type:   "trade",
		Status: "completed",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "0.005", Currency: "BTC"}, // positive = acquiring BTC
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "150.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	row, ok := cbV2TxToRow(tx)
	if !ok {
		t.Fatal("expected ok=true for trade acquiring BTC")
	}
	if row.Type != "buy" {
		t.Errorf("Type = %q, want buy (positive trade = acquiring BTC)", row.Type)
	}
}

func TestCbV2TxToRow_Trade_SpendingBTC(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-trade-sell",
		Type:   "trade",
		Status: "completed",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "-0.003", Currency: "BTC"}, // negative = spending BTC
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "-90.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	row, ok := cbV2TxToRow(tx)
	if !ok {
		t.Fatal("expected ok=true for trade spending BTC")
	}
	if row.Type != "sale" {
		t.Errorf("Type = %q, want sale (negative trade = spending BTC)", row.Type)
	}
}

func TestCbV2TxToRow_Send_Outgoing(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-send-out",
		Type:   "send",
		Status: "completed",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "-0.002", Currency: "BTC"}, // negative = outgoing
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "-60.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	row, ok := cbV2TxToRow(tx)
	if !ok {
		t.Fatal("expected ok=true for outgoing send")
	}
	if row.Type != "send" {
		t.Errorf("Type = %q, want send (negative btc = outgoing)", row.Type)
	}
}

func TestCbV2TxToRow_Send_Incoming(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-send-in",
		Type:   "send",
		Status: "completed",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "0.0005", Currency: "BTC"}, // positive = incoming
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "15.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	row, ok := cbV2TxToRow(tx)
	if !ok {
		t.Fatal("expected ok=true for incoming send")
	}
	if row.Type != "receive" {
		t.Errorf("Type = %q, want receive (positive btc send = incoming)", row.Type)
	}
}

func TestCbV2TxToRow_SkipsPending(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-pending",
		Type:   "buy",
		Status: "pending",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "0.001", Currency: "BTC"},
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "30.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	_, ok := cbV2TxToRow(tx)
	if ok {
		t.Fatal("expected ok=false for pending transaction")
	}
}

func TestCbV2TxToRow_SkipsNonBTC(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-eth",
		Type:   "buy",
		Status: "completed",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "1.0", Currency: "ETH"}, // not BTC
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "2000.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	_, ok := cbV2TxToRow(tx)
	if ok {
		t.Fatal("expected ok=false for non-BTC transaction")
	}
}

func TestCbV2TxToRow_SkipsUnknownType(t *testing.T) {
	tx := cbV2Transaction{
		ID:     "tx-reward",
		Type:   "staking_reward",
		Status: "completed",
		Amount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "0.0001", Currency: "BTC"},
		NativeAmount: struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		}{Amount: "3.00", Currency: "USD"},
		CreatedAt: "2024-01-15T10:00:00Z",
	}

	_, ok := cbV2TxToRow(tx)
	if ok {
		t.Fatal("expected ok=false for unknown transaction type")
	}
}

// --- FetchBalance HTTP mock tests (Issue #53: live balance from API) ---

func TestCoinbaseAPIClient_FetchBalance(t *testing.T) {
	keyID, privB64 := newTestKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/api/v3/brokerage/accounts" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cbAccountsResponse("btc-uuid-1", "0.00500000"))
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	sats, err := client.FetchBalance(context.Background())
	if err != nil {
		t.Fatalf("FetchBalance() error = %v", err)
	}

	// 0.00500000 BTC = 500000 sats
	if sats != 500000 {
		t.Errorf("FetchBalance() = %d sats, want 500000", sats)
	}
}

func TestCoinbaseAPIClient_FetchBalance_NoBTCAccount(t *testing.T) {
	keyID, privB64 := newTestKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return accounts with no BTC account
		json.NewEncoder(w).Encode(map[string]interface{}{
			"accounts": []interface{}{
				map[string]interface{}{
					"uuid":     "usd-uuid",
					"currency": "USD",
					"available_balance": map[string]interface{}{"value": "100.00", "currency": "USD"},
				},
			},
		})
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	_, err = client.FetchBalance(context.Background())
	if err == nil {
		t.Fatal("expected error when no BTC account found")
	}
}

func TestCoinbaseAPIClient_FetchBalance_Unauthorized(t *testing.T) {
	keyID, privB64 := newTestKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	_, err = client.FetchBalance(context.Background())
	if err == nil {
		t.Fatal("expected error for 401 Unauthorized")
	}
}

func TestCoinbaseAPIClient_FetchBalance_Forbidden(t *testing.T) {
	keyID, privB64 := newTestKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	_, err = client.FetchBalance(context.Background())
	if err == nil {
		t.Fatal("expected error for 403 Forbidden — missing Trade-View permission")
	}
}

// --- FetchRows HTTP mock tests (Issue #53: transaction history sync) ---

func TestCoinbaseAPIClient_FetchRows_Basic(t *testing.T) {
	keyID, privB64 := newTestKey(t)
	created := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/api/v3/brokerage/accounts":
			json.NewEncoder(w).Encode(cbAccountsResponse("btc-uuid-1", "0.001"))
		case "/v2/accounts/btc-uuid-1/transactions":
			txns := []map[string]interface{}{
				cbTx("tx-buy-1", "buy", "completed", "0.001", "30.00", created.Format(time.RFC3339)),
				// Non-BTC should be skipped
				{
					"id": "tx-eth-1", "type": "buy", "status": "completed",
					"amount":        map[string]string{"amount": "1.0", "currency": "ETH"},
					"native_amount": map[string]string{"amount": "2000.00", "currency": "USD"},
					"created_at":    created.Format(time.RFC3339),
				},
				// Pending should be skipped
				cbTx("tx-pending", "buy", "pending", "0.0005", "15.00", created.Format(time.RFC3339)),
			}
			json.NewEncoder(w).Encode(cbTxPageResponse(txns, ""))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	rows, err := client.FetchRows(context.Background())
	if err != nil {
		t.Fatalf("FetchRows() error = %v", err)
	}

	// Only the completed BTC buy should be returned
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].TransactionID != "tx-buy-1" {
		t.Errorf("TransactionID = %q, want tx-buy-1", rows[0].TransactionID)
	}
	if rows[0].Type != "buy" {
		t.Errorf("Type = %q, want buy", rows[0].Type)
	}
}

func TestCoinbaseAPIClient_FetchRows_Pagination(t *testing.T) {
	keyID, privB64 := newTestKey(t)
	created := "2024-01-01T00:00:00Z"
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v3/brokerage/accounts" {
			json.NewEncoder(w).Encode(cbAccountsResponse("btc-uuid-2", "0.005"))
			return
		}
		// Transaction pages
		callCount++
		if callCount == 1 {
			// First page with next_uri
			txns := []map[string]interface{}{
				cbTx("tx-1", "buy", "completed", "0.001", "30.00", created),
				cbTx("tx-2", "sell", "completed", "-0.0005", "-15.00", created),
			}
			json.NewEncoder(w).Encode(cbTxPageResponse(txns, "/v2/accounts/btc-uuid-2/transactions?starting_after=tx-2"))
		} else {
			// Second page (no next_uri)
			txns := []map[string]interface{}{
				cbTx("tx-3", "buy", "completed", "0.002", "60.00", created),
			}
			json.NewEncoder(w).Encode(cbTxPageResponse(txns, ""))
		}
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	rows, err := client.FetchRows(context.Background())
	if err != nil {
		t.Fatalf("FetchRows() error = %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 pagination requests, got %d", callCount)
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows, want 3 (across 2 pages)", len(rows))
	}
}

func TestCoinbaseAPIClient_FetchRows_UsesCachedUUID(t *testing.T) {
	keyID, privB64 := newTestKey(t)
	created := "2024-01-01T00:00:00Z"
	accountsCallCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v3/brokerage/accounts" {
			accountsCallCount++
			json.NewEncoder(w).Encode(cbAccountsResponse("btc-uuid-3", "0.001"))
			return
		}
		txns := []map[string]interface{}{cbTx("tx-1", "buy", "completed", "0.001", "30.00", created)}
		json.NewEncoder(w).Encode(cbTxPageResponse(txns, ""))
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	// FetchBalance first to cache the UUID
	if _, err := client.FetchBalance(context.Background()); err != nil {
		t.Fatalf("FetchBalance: %v", err)
	}

	// FetchRows should NOT call /accounts again
	if _, err := client.FetchRows(context.Background()); err != nil {
		t.Fatalf("FetchRows: %v", err)
	}

	// The /accounts endpoint should have been called exactly once (by FetchBalance)
	if accountsCallCount != 1 {
		t.Errorf("accounts endpoint called %d times, want 1 (UUID should be cached)", accountsCallCount)
	}
}

func TestCoinbaseAPIClient_FetchRows_Unauthorized(t *testing.T) {
	keyID, privB64 := newTestKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	_, err = client.FetchRows(context.Background())
	if err == nil {
		t.Fatal("expected error for 401 Unauthorized")
	}
}

func TestCoinbaseAPIClient_FetchRows_ServerError(t *testing.T) {
	keyID, privB64 := newTestKey(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/brokerage/accounts" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cbAccountsResponse("btc-uuid", "0.001"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, err := NewCoinbaseAPIClient(keyID, privB64, WithCoinbaseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("NewCoinbaseAPIClient: %v", err)
	}

	_, err = client.FetchRows(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 server error")
	}
}
