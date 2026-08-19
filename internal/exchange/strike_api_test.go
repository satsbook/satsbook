package exchange_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/exchange"
)

// fakeInvoice builds a fake Strike invoice API response item.
func fakeInvoice(id, state, created, btcAmount string, txns []map[string]interface{}) map[string]interface{} {
	item := map[string]interface{}{
		"invoiceId":   id,
		"state":       state,
		"created":     created,
		"description": "test invoice",
		"amount":      map[string]string{"amount": btcAmount, "currency": "BTC"},
	}
	if txns != nil {
		item["transactions"] = txns
	}
	return item
}

func pagedResponse(items []map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"items":          items,
		"count":          len(items),
		"isCountUnknown": false,
	}
}

func TestStrikeAPIClient_FetchRows_Basic(t *testing.T) {
	created := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Verify $filter and includeTransactions are set
		q := r.URL.Query()
		if q.Get("includeTransactions") != "true" {
			t.Errorf("expected includeTransactions=true, got %q", q.Get("includeTransactions"))
		}

		resp := pagedResponse([]map[string]interface{}{
			fakeInvoice("inv-001", "PAID", created.Format(time.RFC3339), "0.001", nil),
			// USD invoice — should be skipped
			{
				"invoiceId": "inv-usd",
				"state":     "PAID",
				"created":   created.Format(time.RFC3339),
				"amount":    map[string]string{"amount": "50.00", "currency": "USD"},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("test-key", exchange.WithStrikeAPIBaseURL(srv.URL))
	rows, err := client.FetchRows(context.Background())
	if err != nil {
		t.Fatalf("FetchRows() error = %v", err)
	}

	// USD invoice should be skipped; only BTC invoice remains.
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.TransactionID != "inv-001" {
		t.Errorf("TransactionID = %q, want inv-001", r.TransactionID)
	}
	if r.Type != "receive" {
		t.Errorf("Type = %q, want receive", r.Type)
	}
	if r.AmountSat != 100_000 {
		t.Errorf("AmountSat = %d, want 100000", r.AmountSat)
	}
	if r.AmountBTC != 0.001 {
		t.Errorf("AmountBTC = %v, want 0.001", r.AmountBTC)
	}
	if r.Status != "completed" {
		t.Errorf("Status = %q, want completed", r.Status)
	}
}

func TestStrikeAPIClient_FetchRows_WithTransactionSubObject(t *testing.T) {
	// When includeTransactions=true, the actual settled BTC amount from
	// the transaction sub-object should take precedence over the invoice amount.
	created := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
	completed := created.Add(10 * time.Minute)

	txns := []map[string]interface{}{
		{
			"transactionId": "tx-abc-123",
			"state":         "COMPLETED",
			"amountReceived": map[string]string{
				"amount":   "0.00099800", // actual settled (slightly less than invoice)
				"currency": "BTC",
			},
			"created":   created.Format(time.RFC3339),
			"completed": completed.Format(time.RFC3339),
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := pagedResponse([]map[string]interface{}{
			fakeInvoice("inv-002", "PAID", created.Format(time.RFC3339), "0.001", txns),
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("test-key", exchange.WithStrikeAPIBaseURL(srv.URL))
	rows, err := client.FetchRows(context.Background())
	if err != nil {
		t.Fatalf("FetchRows() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	// TransactionID should be the tx ID, not the invoice ID.
	if r.TransactionID != "tx-abc-123" {
		t.Errorf("TransactionID = %q, want tx-abc-123", r.TransactionID)
	}
	// Amount should come from amountReceived, not the invoice face amount.
	if r.AmountSat != 99_800 {
		t.Errorf("AmountSat = %d, want 99800", r.AmountSat)
	}
	// Date should be the completed timestamp.
	if !r.Date.Equal(completed) {
		t.Errorf("Date = %v, want %v", r.Date, completed)
	}
}

func TestStrikeAPIClient_FetchRows_Pagination(t *testing.T) {
	// Server returns exactly 100 items on page 1, then 2 on page 2.
	// FetchRows should make two requests and return 102 rows total.
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		created := "2024-01-01T00:00:00Z"
		var items []map[string]interface{}
		if r.URL.Query().Get("$skip") == "0" {
			for i := 0; i < 100; i++ {
				items = append(items, fakeInvoice(
					"inv-page1-"+string(rune('a'+i%26)), "PAID", created, "0.0001", nil,
				))
			}
		} else {
			items = []map[string]interface{}{
				fakeInvoice("inv-page2-1", "PAID", created, "0.0001", nil),
				fakeInvoice("inv-page2-2", "PAID", created, "0.0001", nil),
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pagedResponse(items))
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("key", exchange.WithStrikeAPIBaseURL(srv.URL))
	rows, err := client.FetchRows(context.Background())
	if err != nil {
		t.Fatalf("FetchRows() error = %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls (pagination), got %d", callCount)
	}
	if len(rows) != 102 {
		t.Errorf("got %d rows, want 102", len(rows))
	}
}

func TestStrikeAPIClient_FetchRows_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"data":{"status":401,"code":"UNAUTHORIZED","message":"Invalid or unspecified identity."}}`))
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("bad-key", exchange.WithStrikeAPIBaseURL(srv.URL))
	_, err := client.FetchRows(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

func TestStrikeAPIClient_FetchRows_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("key", exchange.WithStrikeAPIBaseURL(srv.URL))
	_, err := client.FetchRows(context.Background())
	if err == nil {
		t.Fatal("expected error for 403 (missing scope), got nil")
	}
}

func TestStrikeAPIClient_FetchBalance(t *testing.T) {
	// Actual Strike /v1/balances response: array of {currency, current, pending,
	// outgoing, reserved, available, total}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/balances" {
			http.NotFound(w, r)
			return
		}
		resp := []map[string]string{
			{
				"currency":  "USD",
				"current":   "150.00",
				"pending":   "0.00",
				"outgoing":  "0.00",
				"reserved":  "0.00",
				"available": "150.00",
				"total":     "150.00",
			},
			{
				"currency":  "BTC",
				"current":   "0.00500000",
				"pending":   "0.00050000",
				"outgoing":  "0.00010000",
				"reserved":  "0.00000000",
				"available": "0.00250000",
				"total":     "0.00260000",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("key", exchange.WithStrikeAPIBaseURL(srv.URL))
	sats, err := client.FetchBalance(context.Background())
	if err != nil {
		t.Fatalf("FetchBalance() error = %v", err)
	}

	// Should use 'available' field (250_000 sats), not 'total' (260_000).
	const want = 250_000
	if sats != want {
		t.Errorf("FetchBalance() = %d sats, want %d (available)", sats, want)
	}
}

func TestStrikeAPIClient_FetchBalance_NoBTC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := []map[string]string{
			{"currency": "USD", "current": "50.00", "available": "50.00", "total": "50.00"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("key", exchange.WithStrikeAPIBaseURL(srv.URL))
	sats, err := client.FetchBalance(context.Background())
	if err != nil {
		t.Fatalf("FetchBalance() error = %v", err)
	}
	if sats != 0 {
		t.Errorf("FetchBalance() = %d, want 0 when no BTC balance exists", sats)
	}
}
