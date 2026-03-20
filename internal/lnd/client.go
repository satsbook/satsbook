package lnd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Client handles communication with the LND node.
type Client struct {
	conn   *grpc.ClientConn
	client lnrpc.LightningClient
}

// NewClient creates a new LND client with macaroon and TLS authentication.
func NewClient(host string, port int, macaroonPath, tlsCertPath string) (*Client, error) {
	// Load TLS certificate
	tlsConfig, err := loadTLSCredentials(tlsCertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS credentials: %w", err)
	}

	// Load macaroon
	macaroonCreds, err := loadMacaroon(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load macaroon: %w", err)
	}

	// Combine credentials
	creds := credentials.NewTLS(tlsConfig)

	// Create connection
	address := fmt.Sprintf("%s:%d", host, port)
	conn, err := grpc.Dial(
		address,
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(macaroonCreds),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to LND at %s: %w", address, err)
	}

	return &Client{
		conn:   conn,
		client: lnrpc.NewLightningClient(conn),
	}, nil
}

// Close closes the connection to the LND node.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// loadTLSCredentials loads the TLS certificate from the given path.
func loadTLSCredentials(certPath string) (*tls.Config, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read TLS cert file: %w", err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(certPEM) {
		return nil, fmt.Errorf("failed to parse TLS certificate")
	}

	return &tls.Config{
		RootCAs: certPool,
	}, nil
}

// loadMacaroon loads the macaroon from the given path.
func loadMacaroon(macaroonPath string) (*macaroonCredential, error) {
	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read macaroon file: %w", err)
	}

	return &macaroonCredential{
		macaroon: fmt.Sprintf("%x", macaroonBytes),
	}, nil
}

// macaroonCredential implements grpc.PerRPCCredentials for macaroon authentication.
type macaroonCredential struct {
	macaroon string
}

func (m *macaroonCredential) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"macaroon": m.macaroon,
	}, nil
}

func (m *macaroonCredential) RequireTransportSecurity() bool {
	return true
}

// NodeInfo represents basic information about the LND node.
type NodeInfo struct {
	Alias          string
	PubKey         string
	Synced         bool
	NumChannels    uint32
	NumActiveChannels uint32
	NumPeers       uint32
	BlockHeight    uint32
	Version        string
}

// GetInfo returns basic information about the LND node.
func (c *Client) GetInfo(ctx context.Context) (*NodeInfo, error) {
	resp, err := c.client.GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node info: %w", err)
	}

	return &NodeInfo{
		Alias:             resp.Alias,
		PubKey:            resp.IdentityPubkey,
		Synced:            resp.SyncedToChain,
		NumChannels:       resp.NumActiveChannels + resp.NumInactiveChannels + resp.NumPendingChannels,
		NumActiveChannels: resp.NumActiveChannels,
		NumPeers:          resp.NumPeers,
		BlockHeight:       resp.BlockHeight,
		Version:           resp.Version,
	}, nil
}

// Channel represents a Lightning Network channel.
type Channel struct {
	ChannelID     uint64
	ChannelPoint  string
	RemotePubKey  string
	Capacity      int64
	LocalBalance  int64
	RemoteBalance int64
	Active        bool
	Private       bool
	Initiator     bool
}

// ListChannels returns a list of all channels.
func (c *Client) ListChannels(ctx context.Context) ([]Channel, error) {
	resp, err := c.client.ListChannels(ctx, &lnrpc.ListChannelsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list channels: %w", err)
	}

	channels := make([]Channel, len(resp.Channels))
	for i, ch := range resp.Channels {
		channels[i] = Channel{
			ChannelID:     ch.ChanId,
			ChannelPoint:  ch.ChannelPoint,
			RemotePubKey:  ch.RemotePubkey,
			Capacity:      ch.Capacity,
			LocalBalance:  ch.LocalBalance,
			RemoteBalance: ch.RemoteBalance,
			Active:        ch.Active,
			Private:       ch.Private,
			Initiator:     ch.Initiator,
		}
	}

	return channels, nil
}

// ForwardingEvent represents a routing event with fee information.
type ForwardingEvent struct {
	Timestamp      time.Time
	ChanIDIn       uint64
	ChanIDOut      uint64
	AmountIn       uint64 // msat
	AmountOut      uint64 // msat
	Fee            uint64 // msat
	FeeMsat        uint64 // deprecated, same as Fee
}

// ForwardingHistory returns routing events between the given times, with automatic pagination.
// It fetches all events in the time range by paginating through the results.
func (c *Client) ForwardingHistory(ctx context.Context, startTime, endTime time.Time) ([]ForwardingEvent, error) {
	const pageSize uint32 = 1000
	var allEvents []ForwardingEvent
	var indexOffset uint32 = 0

	for {
		req := &lnrpc.ForwardingHistoryRequest{
			StartTime:      uint64(startTime.Unix()),
			EndTime:        uint64(endTime.Unix()),
			IndexOffset:    indexOffset,
			NumMaxEvents:   pageSize,
		}

		resp, err := c.client.ForwardingHistory(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to get forwarding history: %w", err)
		}

		for _, ev := range resp.ForwardingEvents {
			allEvents = append(allEvents, ForwardingEvent{
				Timestamp:      time.Unix(int64(ev.Timestamp), 0),
				ChanIDIn:       ev.ChanIdIn,
				ChanIDOut:      ev.ChanIdOut,
				AmountIn:       ev.AmtInMsat,
				AmountOut:      ev.AmtOutMsat,
				Fee:            ev.FeeMsat,
				FeeMsat:        ev.FeeMsat,
			})
		}

		// Stop if we got fewer events than the page size
		if len(resp.ForwardingEvents) < int(pageSize) {
			break
		}

		indexOffset = resp.LastOffsetIndex
	}

	return allEvents, nil
}

// Invoice represents a received payment.
type Invoice struct {
	PaymentRequest string
	RHash          string
	Value          int64
	ValueMsat      int64
	Settled        bool
	SettleDate     time.Time
	CreationDate   time.Time
	Memo           string
}

// ListInvoices returns invoices starting from the given offset and the next offset to use.
func (c *Client) ListInvoices(ctx context.Context, offset uint64) ([]Invoice, uint64, error) {
	req := &lnrpc.ListInvoiceRequest{
		IndexOffset:    offset,
		NumMaxInvoices: 1000,
		Reversed:       false,
	}

	resp, err := c.client.ListInvoices(ctx, req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list invoices: %w", err)
	}

	invoices := make([]Invoice, len(resp.Invoices))
	for i, inv := range resp.Invoices {
		var settleDate time.Time
		if inv.SettleDate > 0 {
			settleDate = time.Unix(inv.SettleDate, 0)
		}

		invoices[i] = Invoice{
			PaymentRequest: inv.PaymentRequest,
			RHash:          fmt.Sprintf("%x", inv.RHash),
			Value:          inv.Value,
			ValueMsat:      inv.ValueMsat,
			Settled:        inv.Settled,
			SettleDate:     settleDate,
			CreationDate:   time.Unix(inv.CreationDate, 0),
			Memo:           inv.Memo,
		}
	}

	return invoices, resp.LastIndexOffset, nil
}

// Payment represents a sent payment.
type Payment struct {
	PaymentHash    string
	Value          int64
	ValueMsat      int64
	Fee            int64
	FeeMsat        int64
	CreationDate   time.Time
	Status         string
	PaymentRequest string
}

// ListPayments returns payments starting from the given offset and the next offset to use.
func (c *Client) ListPayments(ctx context.Context, offset uint64) ([]Payment, uint64, error) {
	req := &lnrpc.ListPaymentsRequest{
		IndexOffset: offset,
		MaxPayments: 1000,
	}

	resp, err := c.client.ListPayments(ctx, req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list payments: %w", err)
	}

	payments := make([]Payment, len(resp.Payments))
	for i, pmt := range resp.Payments {
		payments[i] = Payment{
			PaymentHash:    pmt.PaymentHash,
			Value:          pmt.Value,
			ValueMsat:      pmt.ValueMsat,
			Fee:            pmt.Fee,
			FeeMsat:        pmt.FeeMsat,
			CreationDate:   time.Unix(pmt.CreationDate, 0),
			Status:         pmt.Status.String(),
			PaymentRequest: pmt.PaymentRequest,
		}
	}

	return payments, resp.LastIndexOffset, nil
}

// WalletBalance represents the on-chain wallet balance.
type WalletBalance struct {
	TotalBalance       int64
	ConfirmedBalance   int64
	UnconfirmedBalance int64
}

// WalletBalance returns the on-chain wallet balance.
func (c *Client) WalletBalance(ctx context.Context) (*WalletBalance, error) {
	resp, err := c.client.WalletBalance(ctx, &lnrpc.WalletBalanceRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet balance: %w", err)
	}

	return &WalletBalance{
		TotalBalance:       resp.TotalBalance,
		ConfirmedBalance:   resp.ConfirmedBalance,
		UnconfirmedBalance: resp.UnconfirmedBalance,
	}, nil
}
