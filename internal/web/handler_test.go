package web

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
)

// --- Mock implementations ---

type mockStore struct {
	feeSummaryFn        func(ctx context.Context, since time.Time) (int64, int64, error)
	activeChannelFn     func(ctx context.Context) (int, error)
	latestWalletFn      func(ctx context.Context) (*db.WalletBalanceSnapshot, error)
	channelStatsFn      func(ctx context.Context) ([]db.ChannelStat, error)
	forwardingEventsFn  func(ctx context.Context, from, to time.Time, limit, offset int) (*db.ForwardingPage, error)
}

func (m *mockStore) FeeSummary(ctx context.Context, since time.Time) (int64, int64, error) {
	return m.feeSummaryFn(ctx, since)
}
func (m *mockStore) ActiveChannelCount(ctx context.Context) (int, error) {
	return m.activeChannelFn(ctx)
}
func (m *mockStore) LatestWalletBalance(ctx context.Context) (*db.WalletBalanceSnapshot, error) {
	return m.latestWalletFn(ctx)
}
func (m *mockStore) ChannelStats(ctx context.Context) ([]db.ChannelStat, error) {
	return m.channelStatsFn(ctx)
}
func (m *mockStore) ForwardingEvents(ctx context.Context, from, to time.Time, limit, offset int) (*db.ForwardingPage, error) {
	return m.forwardingEventsFn(ctx, from, to, limit, offset)
}

type mockNodeInfo struct {
	info *lnd.NodeInfo
	err  error
}

func (m *mockNodeInfo) GetInfo(ctx context.Context) (*lnd.NodeInfo, error) {
	return m.info, m.err
}

type mockPrice struct {
	price float64
	err   error
}

func (m *mockPrice) GetBTCPrice(ctx context.Context) (float64, error) {
	return m.price, m.err
}

func newTestHandler(store DashboardStore, node NodeInfoProvider, price PriceProvider) *Handler {
	return NewHandler(store, node, price, log.New(os.Stderr, "[test] ", 0))
}

// --- HandleSummary tests ---

func TestHandleSummary_Success(t *testing.T) {
	store := &mockStore{
		feeSummaryFn: func(_ context.Context, since time.Time) (int64, int64, error) {
			if since.IsZero() {
				return 6000, 3, nil // all-time
			}
			return 3000, 2, nil // 30d or 7d
		},
		activeChannelFn: func(_ context.Context) (int, error) { return 5, nil },
		latestWalletFn: func(_ context.Context) (*db.WalletBalanceSnapshot, error) {
			return &db.WalletBalanceSnapshot{TotalSat: 100000}, nil
		},
	}
	price := &mockPrice{price: 67000.0}
	h := newTestHandler(store, nil, price)

	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	w := httptest.NewRecorder()
	h.HandleSummary(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp summaryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if resp.Fees.AllTime.Sats != 6 {
		t.Errorf("expected all-time fees 6 sats (6000 msat / 1000), got %d", resp.Fees.AllTime.Sats)
	}
	if resp.ActiveChannels != 5 {
		t.Errorf("expected 5 active channels, got %d", resp.ActiveChannels)
	}
	if resp.WalletBalanceSats != 100000 {
		t.Errorf("expected balance 100000, got %d", resp.WalletBalanceSats)
	}
	if resp.BTCPriceUSD == nil || *resp.BTCPriceUSD != 67000.0 {
		t.Errorf("expected BTC price 67000, got %v", resp.BTCPriceUSD)
	}
	if resp.Fees.AllTime.USD == nil {
		t.Error("expected USD fee to be set")
	}
}

func TestHandleSummary_NilBalance(t *testing.T) {
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	price := &mockPrice{err: errors.New("no price")}
	h := newTestHandler(store, nil, price)

	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	w := httptest.NewRecorder()
	h.HandleSummary(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp summaryResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.WalletBalanceSats != 0 {
		t.Errorf("expected 0 balance when nil, got %d", resp.WalletBalanceSats)
	}
	if resp.BTCPriceUSD != nil {
		t.Errorf("expected nil BTC price on error, got %v", resp.BTCPriceUSD)
	}
}

func TestHandleSummary_StoreError(t *testing.T) {
	store := &mockStore{
		feeSummaryFn: func(_ context.Context, _ time.Time) (int64, int64, error) {
			return 0, 0, errors.New("db error")
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	w := httptest.NewRecorder()
	h.HandleSummary(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- HandleChannels tests ---

func TestHandleChannels_Success(t *testing.T) {
	store := &mockStore{
		channelStatsFn: func(_ context.Context) ([]db.ChannelStat, error) {
			return []db.ChannelStat{
				{ChanID: 100, RemotePubKey: "abc", LocalBalance: 50000, RemoteBalance: 50000, Active: true, FeesEarnedAllTimeMsat: 5000, FeesEarned30dMsat: 2000},
				{ChanID: 200, RemotePubKey: "def", LocalBalance: 30000, RemoteBalance: 70000, Active: false, FeesEarnedAllTimeMsat: 1000, FeesEarned30dMsat: 500},
			}, nil
		},
	}
	price := &mockPrice{price: 67000.0}
	h := newTestHandler(store, nil, price)

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	h.HandleChannels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var channels []channelResponse
	if err := json.NewDecoder(w.Body).Decode(&channels); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(channels))
	}
	if channels[0].FeesEarnedAllTimeSats != 5 {
		t.Errorf("expected 5 sats, got %d", channels[0].FeesEarnedAllTimeSats)
	}
	if channels[0].FeesEarnedAllTimeUSD == nil {
		t.Error("expected USD fee to be set")
	}
}

func TestHandleChannels_NoPriceAvailable(t *testing.T) {
	store := &mockStore{
		channelStatsFn: func(_ context.Context) ([]db.ChannelStat, error) {
			return []db.ChannelStat{
				{ChanID: 100, RemotePubKey: "abc", Active: true, FeesEarnedAllTimeMsat: 5000},
			}, nil
		},
	}
	price := &mockPrice{err: errors.New("no price")}
	h := newTestHandler(store, nil, price)

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	h.HandleChannels(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var channels []channelResponse
	if err := json.NewDecoder(w.Body).Decode(&channels); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if channels[0].FeesEarnedAllTimeUSD != nil {
		t.Errorf("expected nil USD when price unavailable, got %v", channels[0].FeesEarnedAllTimeUSD)
	}
}

func TestHandleChannels_StoreError(t *testing.T) {
	store := &mockStore{
		channelStatsFn: func(_ context.Context) ([]db.ChannelStat, error) {
			return nil, errors.New("db error")
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	h.HandleChannels(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- HandleForwarding tests ---

func TestHandleForwarding_Success(t *testing.T) {
	now := time.Now().UTC()
	store := &mockStore{
		forwardingEventsFn: func(_ context.Context, _, _ time.Time, limit, offset int) (*db.ForwardingPage, error) {
			if limit != 50 || offset != 0 {
				t.Errorf("expected default limit=50, offset=0, got limit=%d, offset=%d", limit, offset)
			}
			return &db.ForwardingPage{
				Events: []db.ForwardingEvent{
					{Timestamp: now, ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
				},
				Total: 1,
			}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/forwarding", nil)
	w := httptest.NewRecorder()
	h.HandleForwarding(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp forwardingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Pagination.Total != 1 {
		t.Errorf("expected total 1, got %d", resp.Pagination.Total)
	}
	if resp.Pagination.Page != 1 {
		t.Errorf("expected page 1, got %d", resp.Pagination.Page)
	}
	if len(resp.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(resp.Events))
	}
}

func TestHandleForwarding_Pagination(t *testing.T) {
	store := &mockStore{
		forwardingEventsFn: func(_ context.Context, _, _ time.Time, limit, offset int) (*db.ForwardingPage, error) {
			if limit != 10 || offset != 20 {
				t.Errorf("expected limit=10, offset=20 (page 3), got limit=%d, offset=%d", limit, offset)
			}
			return &db.ForwardingPage{Events: nil, Total: 50}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/forwarding?page=3&limit=10", nil)
	w := httptest.NewRecorder()
	h.HandleForwarding(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleForwarding_LimitCapped(t *testing.T) {
	store := &mockStore{
		forwardingEventsFn: func(_ context.Context, _, _ time.Time, limit, _ int) (*db.ForwardingPage, error) {
			if limit != 100 {
				t.Errorf("expected limit capped to 100, got %d", limit)
			}
			return &db.ForwardingPage{Events: nil, Total: 0}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/forwarding?limit=500", nil)
	w := httptest.NewRecorder()
	h.HandleForwarding(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleForwarding_InvalidFrom(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/forwarding?from=bad", nil)
	w := httptest.NewRecorder()
	h.HandleForwarding(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleForwarding_StoreError(t *testing.T) {
	store := &mockStore{
		forwardingEventsFn: func(_ context.Context, _, _ time.Time, _, _ int) (*db.ForwardingPage, error) {
			return nil, errors.New("db error")
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/forwarding", nil)
	w := httptest.NewRecorder()
	h.HandleForwarding(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- HandleNode tests ---

func TestHandleNode_Success(t *testing.T) {
	node := &mockNodeInfo{
		info: &lnd.NodeInfo{
			Alias:             "my-node",
			PubKey:            "02abc",
			Synced:            true,
			NumActiveChannels: 3,
			NumPeers:          5,
			BlockHeight:       800000,
			Version:           "0.17.0",
		},
	}
	h := newTestHandler(&mockStore{}, node, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/node", nil)
	w := httptest.NewRecorder()
	h.HandleNode(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp nodeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Alias != "my-node" {
		t.Errorf("expected alias 'my-node', got %q", resp.Alias)
	}
	if resp.PubKey != "02abc" {
		t.Errorf("expected pubkey '02abc', got %q", resp.PubKey)
	}
	if !resp.Synced {
		t.Error("expected synced to be true")
	}
	if resp.BlockHeight != 800000 {
		t.Errorf("expected block height 800000, got %d", resp.BlockHeight)
	}
}

func TestHandleNode_Error(t *testing.T) {
	node := &mockNodeInfo{err: errors.New("lnd unavailable")}
	h := newTestHandler(&mockStore{}, node, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/api/node", nil)
	w := httptest.NewRecorder()
	h.HandleNode(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- Helper tests ---

func TestMsatToSat(t *testing.T) {
	tests := []struct {
		msat     int64
		expected int64
	}{
		{0, 0},
		{1000, 1},
		{999, 0},
		{5500, 5},
		{100_000_000_000, 100_000_000},
	}
	for _, tt := range tests {
		got := msatToSat(tt.msat)
		if got != tt.expected {
			t.Errorf("msatToSat(%d) = %d, want %d", tt.msat, got, tt.expected)
		}
	}
}

func TestMsatToUSD(t *testing.T) {
	got := msatToUSD(100_000_000_000, 67000.0)
	if got != 67000.0 {
		t.Errorf("msatToUSD(1 BTC, $67k) = %f, want 67000", got)
	}
}

func TestParseIntParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?a=5&b=bad", nil)
	if got := parseIntParam(req, "a", 1); got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
	if got := parseIntParam(req, "b", 1); got != 1 {
		t.Errorf("expected default 1 for bad param, got %d", got)
	}
	if got := parseIntParam(req, "missing", 42); got != 42 {
		t.Errorf("expected default 42, got %d", got)
	}
}

func TestParseTimeParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test?t=2024-01-15T10:00:00Z", nil)
	defaultTime := time.Now()

	got, err := parseTimeParam(req, "t", defaultTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	if !got.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, got)
	}

	// Missing param returns default
	got, err = parseTimeParam(req, "missing", defaultTime)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(defaultTime) {
		t.Errorf("expected default time, got %v", got)
	}

	// Invalid format returns error
	req = httptest.NewRequest(http.MethodGet, "/test?t=invalid", nil)
	_, err = parseTimeParam(req, "t", defaultTime)
	if err == nil {
		t.Error("expected error for invalid time format")
	}
}
