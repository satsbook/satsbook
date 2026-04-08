package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/exchange"
	"github.com/satsbook/satsbook/internal/lnd"
)

// --- Mock implementations ---

type mockStore struct {
	feeSummaryFn        func(ctx context.Context, since time.Time) (int64, int64, error)
	activeChannelFn     func(ctx context.Context) (int, error)
	latestWalletFn      func(ctx context.Context) (*db.WalletBalanceSnapshot, error)
	channelStatsFn      func(ctx context.Context) ([]db.ChannelStat, error)
	forwardingEventsFn  func(ctx context.Context, from, to time.Time, limit, offset int) (*db.ForwardingPage, error)
	dailyFeesFn         func(ctx context.Context, since time.Time) ([]db.DailyFeeStat, error)
	lastSyncedAtFn      func(ctx context.Context) (time.Time, error)
	exchangeBalanceFn   func(ctx context.Context, source string) (int64, error)
	exchangeSummaryFn   func(ctx context.Context, source string, since time.Time) (*db.ExchangeSummaryResult, error)
	portfolioPositionFn func(ctx context.Context, since time.Time) (*db.PortfolioPositionResult, error)
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
func (m *mockStore) DailyFees(ctx context.Context, since time.Time) ([]db.DailyFeeStat, error) {
	if m.dailyFeesFn != nil {
		return m.dailyFeesFn(ctx, since)
	}
	return nil, nil
}
func (m *mockStore) LastSyncedAt(ctx context.Context) (time.Time, error) {
	if m.lastSyncedAtFn != nil {
		return m.lastSyncedAtFn(ctx)
	}
	return time.Time{}, nil
}
func (m *mockStore) ExchangeBalance(ctx context.Context, source string) (int64, error) {
	if m.exchangeBalanceFn != nil {
		return m.exchangeBalanceFn(ctx, source)
	}
	return 0, nil
}
func (m *mockStore) ExchangeSummary(ctx context.Context, source string, since time.Time) (*db.ExchangeSummaryResult, error) {
	if m.exchangeSummaryFn != nil {
		return m.exchangeSummaryFn(ctx, source, since)
	}
	return &db.ExchangeSummaryResult{}, nil
}
func (m *mockStore) PortfolioPosition(ctx context.Context, since time.Time) (*db.PortfolioPositionResult, error) {
	if m.portfolioPositionFn != nil {
		return m.portfolioPositionFn(ctx, since)
	}
	return &db.PortfolioPositionResult{BySource: map[string]db.SourceBalance{}}, nil
}

type mockNodeInfo struct {
	info *lnd.NodeInfo
	err  error
}

func (m *mockNodeInfo) GetInfo(ctx context.Context) (*lnd.NodeInfo, error) {
	return m.info, m.err
}

type mockPrice struct {
	price     float64
	err       error
	fetchedAt time.Time
}

func (m *mockPrice) GetBTCPrice(ctx context.Context) (float64, error) {
	return m.price, m.err
}
func (m *mockPrice) FetchedAt() time.Time {
	return m.fetchedAt
}

type mockImportStore struct {
	importStrikeFn   func(ctx context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error)
	importRiverFn    func(ctx context.Context, rows []exchange.RiverRow) (*db.ImportSummary, error)
	importCoinbaseFn func(ctx context.Context, rows []exchange.CoinbaseRow) (*db.ImportSummary, error)
	clearFn          func(ctx context.Context, source string) (*db.ClearExchangeResult, error)
}

func (m *mockImportStore) ClearExchangeSource(ctx context.Context, source string) (*db.ClearExchangeResult, error) {
	if m.clearFn != nil {
		return m.clearFn(ctx, source)
	}
	return &db.ClearExchangeResult{Source: source}, nil
}

func (m *mockImportStore) ImportStrikeCSV(ctx context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error) {
	if m.importStrikeFn != nil {
		return m.importStrikeFn(ctx, rows)
	}
	return &db.ImportSummary{}, nil
}

func (m *mockImportStore) ImportRiverCSV(ctx context.Context, rows []exchange.RiverRow) (*db.ImportSummary, error) {
	if m.importRiverFn != nil {
		return m.importRiverFn(ctx, rows)
	}
	return &db.ImportSummary{}, nil
}

func (m *mockImportStore) ImportCoinbaseCSV(ctx context.Context, rows []exchange.CoinbaseRow) (*db.ImportSummary, error) {
	if m.importCoinbaseFn != nil {
		return m.importCoinbaseFn(ctx, rows)
	}
	return &db.ImportSummary{}, nil
}

func newTestHandler(store DashboardStore, node NodeInfoProvider, price PriceProvider) *Handler {
	return NewHandler(store, node, price, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))
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

// --- HandleStrikeImport tests ---

func createMultipartCSV(t *testing.T, csvContent string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "transactions.csv")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(csvContent))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/import/strike", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, httptest.NewRecorder()
}

func TestHandleStrikeImport_Success(t *testing.T) {
	importStore := &mockImportStore{
		importStrikeFn: func(_ context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error) {
			return &db.ImportSummary{Total: len(rows), NewPurchases: 1, Duplicates: 0}, nil
		},
	}
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	csv := "Reference,Date & Time (UTC),Transaction Type,Amount USD,Fee USD,Amount BTC,Fee BTC,BTC Price,Cost Basis (USD),Destination,Description,Transaction Hash,Note\ntx-001,Jan 15 2024 10:30:00,Purchase,67.00,0.50,0.001,,94000.00,67.00,,,,\n"
	req, w := createMultipartCSV(t, csv)
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp importResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("expected total 1, got %d", resp.Total)
	}
	if resp.NewPurchases != 1 {
		t.Errorf("expected 1 new purchase, got %d", resp.NewPurchases)
	}
}

func TestHandleStrikeImport_WrongMethod(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/import/strike", nil)
	w := httptest.NewRecorder()
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleStrikeImport_InvalidCSV(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	csv := "Bad,Header,Format\ndata,here,now\n"
	req, w := createMultipartCSV(t, csv)
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleStrikeImport_EmptyCSV(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	csv := "Reference,Date & Time (UTC),Transaction Type,Amount USD,Fee USD,Amount BTC,Fee BTC,BTC Price,Cost Basis (USD),Destination,Description,Transaction Hash,Note\n"
	req, w := createMultipartCSV(t, csv)
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty data, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleStrikeImport_MissingFile(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodPost, "/api/import/strike", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=abc")
	w := httptest.NewRecorder()
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleStrikeImport_HTMXResponse(t *testing.T) {
	importStore := &mockImportStore{
		importStrikeFn: func(_ context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error) {
			return &db.ImportSummary{Total: 2, NewPurchases: 1, Duplicates: 1}, nil
		},
	}
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	csv := "Reference,Date & Time (UTC),Transaction Type,Amount USD,Fee USD,Amount BTC,Fee BTC,BTC Price,Cost Basis (USD),Destination,Description,Transaction Hash,Note\ntx-001,Jan 15 2024 10:30:00,Purchase,67.00,0.50,0.001,,94000.00,67.00,,,,\n"
	req, w := createMultipartCSV(t, csv)
	req.Header.Set("HX-Request", "true")
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html for HTMX, got %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "new purchase") {
		t.Errorf("expected summary in HTML, got: %s", body)
	}
}

// --- HandleClearImport tests ---

func TestHandleClearImport_Success(t *testing.T) {
	importStore := &mockImportStore{
		clearFn: func(ctx context.Context, source string) (*db.ClearExchangeResult, error) {
			return &db.ClearExchangeResult{Source: source, ImportsRemoved: 5, LotsRemoved: 2}, nil
		},
	}
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	req := httptest.NewRequest(http.MethodPost, "/api/import/strike/clear", strings.NewReader("confirm=yes"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleClearImport("strike")(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("HX-Trigger") != "satsbook:data-cleared" {
		t.Errorf("missing HX-Trigger header")
	}
	if !strings.Contains(w.Body.String(), "strike") {
		t.Errorf("expected body to mention source, got: %s", w.Body.String())
	}
}

func TestHandleClearImport_MissingConfirm(t *testing.T) {
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))
	req := httptest.NewRequest(http.MethodPost, "/api/import/strike/clear", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleClearImport("strike")(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleClearImport_NonHTMXRejected(t *testing.T) {
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))
	req := httptest.NewRequest(http.MethodPost, "/api/import/strike/clear", strings.NewReader("confirm=yes"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleClearImport("strike")(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-HTMX, got %d", w.Code)
	}
}

func TestHandleClearImport_InvalidSource(t *testing.T) {
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))
	req := httptest.NewRequest(http.MethodPost, "/api/import/kraken/clear", strings.NewReader("confirm=yes"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleClearImport("kraken")(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid source, got %d", w.Code)
	}
}

func TestHandleClearImport_GETRejected(t *testing.T) {
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))
	req := httptest.NewRequest(http.MethodGet, "/api/import/strike/clear", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleClearImport("strike")(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
