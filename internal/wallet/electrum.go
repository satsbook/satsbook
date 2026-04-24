package wallet

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ElectrumClient connects to an Electrum server (electrs/fulcrum) via TCP
// and queries UTXO balances using the Electrum protocol.
type ElectrumClient struct {
	addr    string
	conn    net.Conn
	reader  *bufio.Reader
	mu      sync.Mutex
	nextID  atomic.Int64
	timeout time.Duration
}

// ElectrumOption configures an ElectrumClient.
type ElectrumOption func(*ElectrumClient)

// WithTimeout sets the read/write timeout for Electrum requests.
func WithTimeout(d time.Duration) ElectrumOption {
	return func(c *ElectrumClient) {
		c.timeout = d
	}
}

// NewElectrumClient creates a new client connected to the given host:port.
func NewElectrumClient(ctx context.Context, host string, port int, opts ...ElectrumOption) (*ElectrumClient, error) {
	addr := fmt.Sprintf("%s:%d", host, port)

	c := &ElectrumClient{
		addr:    addr,
		timeout: 10 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}

	dialer := net.Dialer{Timeout: c.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("connect to electrum at %s: %w", addr, err)
	}

	c.conn = conn
	c.reader = bufio.NewReader(conn)

	// Negotiate version
	if err := c.negotiate(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("electrum version negotiation: %w", err)
	}

	return c, nil
}

// Close closes the connection.
func (c *ElectrumClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// UTXO represents an unspent transaction output returned by Electrum.
type UTXO struct {
	TxHash string `json:"tx_hash"`
	TxPos  int    `json:"tx_pos"`
	Height int    `json:"height"`
	Value  int64  `json:"value"`
}

// GetBalance returns the total balance in satoshis for a given script hash.
func (c *ElectrumClient) GetBalance(ctx context.Context, scriptHash string) (int64, error) {
	utxos, err := c.ListUnspent(ctx, scriptHash)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, u := range utxos {
		total += u.Value
	}
	return total, nil
}

// ListUnspent returns the list of UTXOs for a given script hash.
func (c *ElectrumClient) ListUnspent(ctx context.Context, scriptHash string) ([]UTXO, error) {
	result, err := c.call(ctx, "blockchain.scripthash.listunspent", []interface{}{scriptHash})
	if err != nil {
		return nil, fmt.Errorf("listunspent: %w", err)
	}

	var utxos []UTXO
	if err := json.Unmarshal(result, &utxos); err != nil {
		return nil, fmt.Errorf("unmarshal utxos: %w", err)
	}

	return utxos, nil
}

// electrumRequest is the JSON-RPC request format.
type electrumRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int64         `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

// electrumResponse is the JSON-RPC response format.
type electrumResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *electrumError  `json:"error,omitempty"`
}

type electrumError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// negotiate performs the initial server.version handshake.
func (c *ElectrumClient) negotiate(ctx context.Context) error {
	_, err := c.call(ctx, "server.version", []interface{}{"satsbook", "1.4"})
	return err
}

// call sends a JSON-RPC request and reads the response.
// It is not safe for concurrent use — callers must hold c.mu.
func (c *ElectrumClient) call(ctx context.Context, method string, params []interface{}) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.nextID.Add(1)

	req := electrumRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.timeout)
	}
	c.conn.SetDeadline(deadline)

	if _, err := c.conn.Write(data); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp electrumResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("electrum error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}
