package price

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestServer(body string, statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write([]byte(body))
	}))
}

func TestGetBTCPrice_FreshFetch(t *testing.T) {
	srv := newTestServer(`{"USD": 67000.50}`, http.StatusOK)
	defer srv.Close()

	c := NewCache(WithAPIURL(srv.URL), WithTTL(5*time.Minute))
	price, err := c.GetBTCPrice(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if price != 67000.50 {
		t.Errorf("expected 67000.50, got %f", price)
	}
}

func TestGetBTCPrice_CachedReturn(t *testing.T) {
	var callCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.Write([]byte(`{"USD": 67000}`))
	}))
	defer srv.Close()

	c := NewCache(WithAPIURL(srv.URL), WithTTL(5*time.Minute))

	// First call fetches
	_, err := c.GetBTCPrice(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call should use cache
	_, err = c.GetBTCPrice(context.Background())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if atomic.LoadInt64(&callCount) != 1 {
		t.Errorf("expected 1 HTTP call, got %d", callCount)
	}
}

func TestGetBTCPrice_CacheExpiry(t *testing.T) {
	var callCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.Write([]byte(`{"USD": 67000}`))
	}))
	defer srv.Close()

	c := NewCache(WithAPIURL(srv.URL), WithTTL(1*time.Millisecond))

	_, _ = c.GetBTCPrice(context.Background())
	time.Sleep(5 * time.Millisecond)
	_, _ = c.GetBTCPrice(context.Background())

	if atomic.LoadInt64(&callCount) != 2 {
		t.Errorf("expected 2 HTTP calls after expiry, got %d", callCount)
	}
}

func TestGetBTCPrice_FallbackOnError(t *testing.T) {
	callNum := int64(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&callNum, 1)
		if n == 1 {
			w.Write([]byte(`{"USD": 67000}`))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := NewCache(WithAPIURL(srv.URL), WithTTL(1*time.Millisecond))

	// First call succeeds
	price, err := c.GetBTCPrice(context.Background())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if price != 67000 {
		t.Errorf("expected 67000, got %f", price)
	}

	// Expire cache
	time.Sleep(5 * time.Millisecond)

	// Second call fails but returns stale price
	price, err = c.GetBTCPrice(context.Background())
	if err != nil {
		t.Fatalf("expected stale fallback, got error: %v", err)
	}
	if price != 67000 {
		t.Errorf("expected stale price 67000, got %f", price)
	}
}

func TestGetBTCPrice_NoCache_Error(t *testing.T) {
	srv := newTestServer("", http.StatusInternalServerError)
	defer srv.Close()

	c := NewCache(WithAPIURL(srv.URL))
	_, err := c.GetBTCPrice(context.Background())
	if err == nil {
		t.Fatal("expected error with no cache and failed fetch")
	}
}

func TestGetBTCPrice_InvalidJSON(t *testing.T) {
	srv := newTestServer("not json", http.StatusOK)
	defer srv.Close()

	c := NewCache(WithAPIURL(srv.URL))
	_, err := c.GetBTCPrice(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestGetBTCPrice_ZeroPrice(t *testing.T) {
	srv := newTestServer(`{"USD": 0}`, http.StatusOK)
	defer srv.Close()

	c := NewCache(WithAPIURL(srv.URL))
	_, err := c.GetBTCPrice(context.Background())
	if err == nil {
		t.Fatal("expected error for zero price")
	}
}

func TestMsatToUSD(t *testing.T) {
	tests := []struct {
		msat     int64
		btcPrice float64
		expected float64
	}{
		{100_000_000_000, 67000.0, 67000.0},  // 1 BTC
		{1_000_000, 67000.0, 0.67},            // 1000 sats
		{0, 67000.0, 0.0},
		{1_000_000_000, 100000.0, 1000.0},     // 0.01 BTC at $100k
	}

	for _, tt := range tests {
		got := MsatToUSD(tt.msat, tt.btcPrice)
		if math.Abs(got-tt.expected) > 0.01 {
			t.Errorf("MsatToUSD(%d, %f) = %f, want %f", tt.msat, tt.btcPrice, got, tt.expected)
		}
	}
}

func TestNewCache_Defaults(t *testing.T) {
	c := NewCache()
	if c.ttl != 5*time.Minute {
		t.Errorf("expected default TTL 5m, got %v", c.ttl)
	}
	if c.apiURL != "https://mempool.space/api/v1/prices" {
		t.Errorf("unexpected default API URL: %s", c.apiURL)
	}
}
