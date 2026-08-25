package wallet

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

// mockElectrumServer runs a simple TCP server that speaks the Electrum JSON-RPC
// protocol. This allows testing the ElectrumClient without a real Electrum node.
// Per issue #52, the Electrum client is the primary balance source for watched wallets
// (electrs/fulcrum on Umbrel).
type mockElectrumServer struct {
	ln       net.Listener
	handlers map[string]func(params []interface{}) interface{}
}

func newMockElectrumServer(t *testing.T) *mockElectrumServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &mockElectrumServer{
		ln:       ln,
		handlers: make(map[string]func(params []interface{}) interface{}),
	}
	// Default handler for server.version (version negotiation)
	s.handlers["server.version"] = func(params []interface{}) interface{} {
		return []string{"ElectrumX 1.16.0", "1.4"}
	}
	go s.serve(t)
	return s
}

func (s *mockElectrumServer) addr() string {
	return s.ln.Addr().String()
}

func (s *mockElectrumServer) close() {
	s.ln.Close()
}

func (s *mockElectrumServer) serve(t *testing.T) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // server closed
		}
		go s.handleConn(t, conn)
	}
}

func (s *mockElectrumServer) handleConn(t *testing.T, conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}

		var req struct {
			ID     int64         `json:"id"`
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}

		handler, ok := s.handlers[req.Method]
		var result interface{}
		if ok {
			result = handler(req.Params)
		}

		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		}
		data, _ := json.Marshal(resp)
		data = append(data, '\n')
		conn.Write(data)
	}
}

// hostPort splits "host:port" for NewElectrumClient.
func hostPort(addr string) (string, int) {
	h, p, _ := net.SplitHostPort(addr)
	var port int
	fmt.Sscanf(p, "%d", &port)
	return h, port
}

// --- ElectrumClient tests (Issue #52: UTXO balance queries for watched wallets) ---

func TestNewElectrumClient_ConnectAndNegotiate(t *testing.T) {
	srv := newMockElectrumServer(t)
	defer srv.close()

	host, port := hostPort(srv.addr())
	client, err := NewElectrumClient(context.Background(), host, port)
	if err != nil {
		t.Fatalf("NewElectrumClient: %v", err)
	}
	defer client.Close()
}

func TestWithTimeout(t *testing.T) {
	srv := newMockElectrumServer(t)
	defer srv.close()

	host, port := hostPort(srv.addr())
	client, err := NewElectrumClient(context.Background(), host, port, WithTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("NewElectrumClient with timeout: %v", err)
	}
	defer client.Close()

	if client.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", client.timeout)
	}
}

func TestElectrumClient_GetBalance_WithFunds(t *testing.T) {
	srv := newMockElectrumServer(t)
	defer srv.close()

	// Respond to listunspent with UTXOs totalling 150000 sats
	srv.handlers["blockchain.scripthash.listunspent"] = func(params []interface{}) interface{} {
		return []UTXO{
			{TxHash: "abc123", TxPos: 0, Height: 800000, Value: 100000},
			{TxHash: "def456", TxPos: 1, Height: 800001, Value: 50000},
		}
	}

	host, port := hostPort(srv.addr())
	client, err := NewElectrumClient(context.Background(), host, port)
	if err != nil {
		t.Fatalf("NewElectrumClient: %v", err)
	}
	defer client.Close()

	balance, err := client.GetBalance(context.Background(), "abcdef1234567890")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance != 150000 {
		t.Errorf("GetBalance = %d, want 150000", balance)
	}
}

func TestElectrumClient_GetBalance_Empty(t *testing.T) {
	srv := newMockElectrumServer(t)
	defer srv.close()

	// No UTXOs for this address — common case for unused derived addresses
	srv.handlers["blockchain.scripthash.listunspent"] = func(params []interface{}) interface{} {
		return []UTXO{}
	}

	host, port := hostPort(srv.addr())
	client, err := NewElectrumClient(context.Background(), host, port)
	if err != nil {
		t.Fatalf("NewElectrumClient: %v", err)
	}
	defer client.Close()

	balance, err := client.GetBalance(context.Background(), "deadbeef")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance != 0 {
		t.Errorf("GetBalance = %d, want 0 for unused address", balance)
	}
}

func TestElectrumClient_ListUnspent(t *testing.T) {
	srv := newMockElectrumServer(t)
	defer srv.close()

	srv.handlers["blockchain.scripthash.listunspent"] = func(params []interface{}) interface{} {
		return []UTXO{
			{TxHash: "tx1", TxPos: 0, Height: 800000, Value: 75000},
		}
	}

	host, port := hostPort(srv.addr())
	client, err := NewElectrumClient(context.Background(), host, port)
	if err != nil {
		t.Fatalf("NewElectrumClient: %v", err)
	}
	defer client.Close()

	utxos, err := client.ListUnspent(context.Background(), "scripthash1")
	if err != nil {
		t.Fatalf("ListUnspent: %v", err)
	}
	if len(utxos) != 1 {
		t.Fatalf("got %d UTXOs, want 1", len(utxos))
	}
	if utxos[0].Value != 75000 {
		t.Errorf("UTXO value = %d, want 75000", utxos[0].Value)
	}
	if utxos[0].TxHash != "tx1" {
		t.Errorf("UTXO TxHash = %q, want tx1", utxos[0].TxHash)
	}
}

func TestElectrumClient_Close(t *testing.T) {
	srv := newMockElectrumServer(t)
	defer srv.close()

	host, port := hostPort(srv.addr())
	client, err := NewElectrumClient(context.Background(), host, port)
	if err != nil {
		t.Fatalf("NewElectrumClient: %v", err)
	}

	// Close should not error
	if err := client.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestNewElectrumClient_ConnectFails(t *testing.T) {
	// Port 1 is reserved and should refuse connections
	_, err := NewElectrumClient(context.Background(), "127.0.0.1", 1)
	if err == nil {
		t.Fatal("expected connection error to unreachable address")
	}
}

func TestElectrumClient_GetBalance_UsedAsBalanceProvider(t *testing.T) {
	// Verify ElectrumClient can be used as a BalanceProvider for the Scanner
	// (the integration the Scanner is designed for, per issue #52)
	srv := newMockElectrumServer(t)
	defer srv.close()

	var queriedScriptHash string
	srv.handlers["blockchain.scripthash.listunspent"] = func(params []interface{}) interface{} {
		if len(params) > 0 {
			queriedScriptHash, _ = params[0].(string)
		}
		return []UTXO{{TxHash: "abc", TxPos: 0, Height: 800000, Value: 200000}}
	}

	host, port := hostPort(srv.addr())
	client, err := NewElectrumClient(context.Background(), host, port)
	if err != nil {
		t.Fatalf("NewElectrumClient: %v", err)
	}
	defer client.Close()

	// Use the client directly as a BalanceProvider
	balance, err := client.GetBalance(context.Background(), "testscripthash123")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance != 200000 {
		t.Errorf("balance = %d, want 200000", balance)
	}
	if queriedScriptHash != "testscripthash123" {
		t.Errorf("script hash queried = %q, want testscripthash123", queriedScriptHash)
	}
}
