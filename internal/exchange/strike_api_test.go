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

func TestStrikeAPIClient_FetchRows(t *testing.T) {
	created := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/invoices" {
			http.NotFound(w, r)
			return
		}
		resp := map[string]interface{}{
			"items": []map[string]interface{}{
				{
					"invoiceId":   "inv-001",
					"state":       "PAID",
					"created":     created.Format(time.RFC3339),
					"description": "Test payment",
					"amount":      map[string]string{"amount": "0.001", "currency": "BTC"},
				},
				{
					"invoiceId":   "inv-002",
					"state":       "PAID",
					"created":     created.Format(time.RFC3339),
					"description": "USD invoice",
					"amount":      map[string]string{"amount": "50.00", "currency": "USD"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("test-key", exchange.WithStrikeAPIBaseURL(srv.URL))
	rows, err := client.FetchRows(context.Background())
	if err != nil {
		t.Fatalf("FetchRows() error = %v", err)
	}

	// Only the BTC invoice should be included; USD invoice should be skipped.
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
	if r.Description != "Test payment" {
		t.Errorf("Description = %q, want 'Test payment'", r.Description)
	}
	if r.Status != "completed" {
		t.Errorf("Status = %q, want completed", r.Status)
	}
}

func TestStrikeAPIClient_FetchRows_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("bad-key", exchange.WithStrikeAPIBaseURL(srv.URL))
	_, err := client.FetchRows(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

func TestStrikeAPIClient_FetchRows_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := exchange.NewStrikeAPIClient("key", exchange.WithStrikeAPIBaseURL(srv.URL))
	_, err := client.FetchRows(context.Background())
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
}

func TestStrikeAPIClient_FetchBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/balances" {
			http.NotFound(w, r)
			return
		}
		resp := []map[string]string{
			{"currency": "USD", "total": "150.00", "available": "150.00"},
			{"currency": "BTC", "total": "0.00250000", "available": "0.00250000"},
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

	const want = 250_000
	if sats != want {
		t.Errorf("FetchBalance() = %d sats, want %d", sats, want)
	}
}

func TestStrikeAPIClient_FetchBalance_NoBTC(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := []map[string]string{
			{"currency": "USD", "total": "50.00", "available": "50.00"},
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
		t.Errorf("FetchBalance() = %d, want 0 when no BTC balance", sats)
	}
}
