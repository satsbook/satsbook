package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBtcToSats(t *testing.T) {
	tests := []struct {
		btc  float64
		want int64
	}{
		{0.0, 0},
		{0.00000001, 1},
		{0.01234567, 1234567},
		{1.0, 100000000},
		{1.99999999, 199999999},
		{21000000.0, 2100000000000000},
	}
	for _, tt := range tests {
		got := btcToSats(tt.btc)
		if got != tt.want {
			t.Errorf("btcToSats(%v) = %d, want %d", tt.btc, got, tt.want)
		}
	}
}

func TestXpubDescriptor(t *testing.T) {
	tests := []struct {
		dt     DerivationType
		branch int
		want   string
	}{
		{DeriveBIP84, 0, "wpkh(xpub123/0/*)"},
		{DeriveBIP84, 1, "wpkh(xpub123/1/*)"},
		{DeriveBIP49, 0, "sh(wpkh(xpub123/0/*))"},
		{DeriveBIP49, 1, "sh(wpkh(xpub123/1/*))"},
		{DeriveBIP44, 0, "combo(xpub123/0/*)"},
		{DeriveBIP44, 1, "combo(xpub123/1/*)"},
	}
	for _, tt := range tests {
		got := xpubDescriptor("xpub123", tt.branch, tt.dt)
		if got != tt.want {
			t.Errorf("xpubDescriptor(xpub123, %d, %v) = %q, want %q", tt.branch, tt.dt, got, tt.want)
		}
	}
}

func TestBitcoinRPCScanAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcRequest
		json.Unmarshal(body, &req)

		if req.Method != "scantxoutset" {
			t.Errorf("expected method scantxoutset, got %s", req.Method)
		}

		w.Write([]byte(`{"result":{"success":true,"total_amount":0.01234567},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	got, err := scanner.ScanAddress(context.Background(), "bc1qtest")
	if err != nil {
		t.Fatalf("ScanAddress: %v", err)
	}
	if got != 1234567 {
		t.Errorf("ScanAddress = %d, want 1234567", got)
	}
}

func TestBitcoinRPCScanAddressEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"success":true,"total_amount":0.00000000},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	got, err := scanner.ScanAddress(context.Background(), "bc1qempty")
	if err != nil {
		t.Fatalf("ScanAddress: %v", err)
	}
	if got != 0 {
		t.Errorf("ScanAddress = %d, want 0", got)
	}
}

func TestBitcoinRPCScanXpubDescriptors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcRequest
		json.Unmarshal(body, &req)

		// Verify the descriptors param is an array with 2 entries
		params, ok := req.Params[1].([]interface{})
		if !ok {
			t.Fatal("expected params[1] to be an array")
		}
		if len(params) != 2 {
			t.Fatalf("expected 2 descriptors, got %d", len(params))
		}

		// Check descriptor format for BIP84
		d0 := params[0].(map[string]interface{})
		d1 := params[1].(map[string]interface{})
		if !strings.HasPrefix(d0["desc"].(string), "wpkh(") {
			t.Errorf("expected wpkh descriptor, got %s", d0["desc"])
		}
		if !strings.Contains(d0["desc"].(string), "/0/*") {
			t.Errorf("expected /0/* in external descriptor, got %s", d0["desc"])
		}
		if !strings.Contains(d1["desc"].(string), "/1/*") {
			t.Errorf("expected /1/* in change descriptor, got %s", d1["desc"])
		}

		w.Write([]byte(`{"result":{"success":true,"total_amount":0.05000000},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	// Use a real-format zpub that NormalizeKey can handle — but since we can't
	// easily construct one in tests, we test the descriptor builder directly
	// in TestXpubDescriptor above. Here we just verify the RPC flow.
	// ScanXpub will fail at NormalizeKey with a test key, so we test the
	// scan method directly.
	got, err := scanner.scan(context.Background(), []interface{}{
		map[string]interface{}{"desc": "wpkh(xpub.../0/*)", "range": 1000},
		map[string]interface{}{"desc": "wpkh(xpub.../1/*)", "range": 1000},
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got != 5000000 {
		t.Errorf("scan = %d, want 5000000", got)
	}
}

func TestBitcoinRPCCookieAuth(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, ".cookie")
	os.WriteFile(cookiePath, []byte("__cookie__:abcdef123456"), 0600)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"result":{"success":true,"total_amount":0.0},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithCookieAuth(cookiePath))
	scanner.ScanAddress(context.Background(), "bc1qtest")

	if gotAuth == "" {
		t.Fatal("expected Authorization header to be set")
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("expected Basic auth, got %s", gotAuth)
	}
}

func TestBitcoinRPCCookieReread(t *testing.T) {
	dir := t.TempDir()
	cookiePath := filepath.Join(dir, ".cookie")
	os.WriteFile(cookiePath, []byte("user1:pass1"), 0600)

	var authHeaders []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		w.Write([]byte(`{"result":{"success":true,"total_amount":0.0},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithCookieAuth(cookiePath))
	scanner.ScanAddress(context.Background(), "bc1qtest")

	// Rewrite cookie with different credentials
	os.WriteFile(cookiePath, []byte("user2:pass2"), 0600)
	scanner.ScanAddress(context.Background(), "bc1qtest")

	if len(authHeaders) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(authHeaders))
	}
	if authHeaders[0] == authHeaders[1] {
		t.Error("expected different auth headers after cookie rewrite")
	}
}

func TestBitcoinRPCErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":null,"error":{"code":-8,"message":"Invalid descriptor"}}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	_, err := scanner.ScanAddress(context.Background(), "bc1qtest")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Invalid descriptor") {
		t.Errorf("expected 'Invalid descriptor' in error, got: %v", err)
	}
}

func TestBitcoinRPCMethodNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":null,"error":{"code":-32601,"message":"Method not found"}}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	_, err := scanner.ScanAddress(context.Background(), "bc1qtest")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "v0.17+") {
		t.Errorf("expected version hint in error, got: %v", err)
	}
}

func TestBitcoinRPCScanDescriptor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req rpcRequest
		json.Unmarshal(body, &req)

		if req.Method != "scantxoutset" {
			t.Errorf("expected method scantxoutset, got %s", req.Method)
		}

		// Verify descriptor is passed through
		params := req.Params[1].([]interface{})
		d := params[0].(map[string]interface{})
		desc := d["desc"].(string)
		if !strings.Contains(desc, "sortedmulti") {
			t.Errorf("expected sortedmulti in descriptor, got %s", desc)
		}

		w.Write([]byte(`{"result":{"success":true,"total_amount":1.50000000},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	got, err := scanner.ScanDescriptor(context.Background(), "wsh(sortedmulti(2,xpub1.../0/*,xpub2.../0/*,xpub3.../0/*))")
	if err != nil {
		t.Fatalf("ScanDescriptor: %v", err)
	}
	if got != 150000000 {
		t.Errorf("ScanDescriptor = %d, want 150000000", got)
	}
}

func TestBitcoinRPCContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response — should be cancelled
		select {}
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := scanner.ScanAddress(ctx, "bc1qtest")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestWithRPCLogger(t *testing.T) {
	scanner := NewBitcoinRPCScanner("localhost", 8332, WithRPCLogger(nil))
	if scanner == nil {
		t.Fatal("expected non-nil scanner with nil logger")
	}
}

func TestWithRPCTimeout(t *testing.T) {
	scanner := NewBitcoinRPCScanner("localhost", 8332, WithRPCTimeout(5*time.Second))
	if scanner == nil {
		t.Fatal("expected non-nil scanner")
	}
	if scanner.client.Timeout != 5*time.Second {
		t.Errorf("client.Timeout = %v, want 5s", scanner.client.Timeout)
	}
}

func TestBitcoinRPCScanXpub_BIP84(t *testing.T) {
	// ScanXpub normalizes the key and calls scan with 2 descriptors (external + change).
	// We use a real zpub and verify BIP84 (wpkh) descriptors are used.
	callCount := 0
	var gotDescriptors []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		body, _ := io.ReadAll(r.Body)
		var req rpcRequest
		json.Unmarshal(body, &req)

		if req.Method == "scantxoutset" {
			params, ok := req.Params[1].([]interface{})
			if ok {
				for _, p := range params {
					d, ok := p.(map[string]interface{})
					if ok {
						gotDescriptors = append(gotDescriptors, d["desc"].(string))
					}
				}
			}
		}
		w.Write([]byte(`{"result":{"success":true,"total_amount":0.00200000},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))

	// testZpub is defined in derive_test.go, which is also in package wallet
	got, err := scanner.ScanXpub(context.Background(), testZpub, "bip84")
	if err != nil {
		t.Fatalf("ScanXpub: %v", err)
	}
	if got != 200000 {
		t.Errorf("ScanXpub = %d, want 200000", got)
	}
	// Verify BIP84 (wpkh) descriptors were used
	if len(gotDescriptors) != 2 {
		t.Fatalf("expected 2 descriptors, got %d: %v", len(gotDescriptors), gotDescriptors)
	}
	for _, desc := range gotDescriptors {
		if !strings.HasPrefix(desc, "wpkh(") {
			t.Errorf("expected wpkh descriptor, got %q", desc)
		}
	}
}

func TestBitcoinRPCScanXpub_InvalidKey(t *testing.T) {
	scanner := NewBitcoinRPCScanner("localhost", 8332, WithUserPassAuth("u", "p"))
	_, err := scanner.ScanXpub(context.Background(), "invalid-key", "bip84")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestBitcoinRPCScan_AlreadyInProgress(t *testing.T) {
	// When the first scantxoutset returns "Scan already in progress",
	// the scanner should abort and retry.
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		body, _ := io.ReadAll(r.Body)
		var req rpcRequest
		json.Unmarshal(body, &req)

		if req.Method == "scantxoutset" {
			// First call: return "scan in progress"
			// Subsequent: return success
			if callCount == 1 {
				w.Write([]byte(`{"result":null,"error":{"code":-1,"message":"Scan already in progress"}}`))
				return
			}
			// abort call or retry start
			if req.Params[0] == "abort" {
				w.Write([]byte(`{"result":true,"error":null}`))
				return
			}
		}
		w.Write([]byte(`{"result":{"success":true,"total_amount":0.001},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	got, err := scanner.ScanAddress(context.Background(), "bc1qtest")
	if err != nil {
		t.Fatalf("ScanAddress: %v", err)
	}
	if got != 100000 {
		t.Errorf("ScanAddress = %d, want 100000", got)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 RPC calls (abort + retry), got %d", callCount)
	}
}

func TestBitcoinRPCScan_SuccessFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"success":false,"total_amount":0.0},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("user", "pass"))
	_, err := scanner.ScanAddress(context.Background(), "bc1qtest")
	if err == nil {
		t.Fatal("expected error for success=false")
	}
	if !strings.Contains(err.Error(), "success=false") {
		t.Errorf("expected 'success=false' in error, got: %v", err)
	}
}

func TestBitcoinRPCUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithUserPassAuth("bad", "creds"))
	_, err := scanner.ScanAddress(context.Background(), "bc1qtest")
	if err == nil {
		t.Fatal("expected error for 401 Unauthorized")
	}
}

func TestBitcoinRPCCookieAuth_MissingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":{"success":true,"total_amount":0},"error":null}`))
	}))
	defer srv.Close()

	parts := strings.SplitN(strings.TrimPrefix(srv.URL, "http://"), ":", 2)
	host := parts[0]
	var port int
	fmt.Sscanf(parts[1], "%d", &port)

	scanner := NewBitcoinRPCScanner(host, port, WithCookieAuth("/nonexistent/.cookie"))
	_, err := scanner.ScanAddress(context.Background(), "bc1qtest")
	if err == nil {
		t.Fatal("expected error for missing cookie file")
	}
}
