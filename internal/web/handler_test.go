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
	exchangeSummaryFn          func(ctx context.Context, source string, since time.Time) (*db.ExchangeSummaryResult, error)
	listExchangeTransactionsFn func(ctx context.Context, source string, limit, offset int) (*db.ExchangeTransactionPage, error)
	portfolioPositionFn        func(ctx context.Context, since time.Time) (*db.PortfolioPositionResult, error)
	portfolioSnapshotsFn       func(ctx context.Context, days int) ([]db.PortfolioSnapshot, error)
	portfolioBreakdownFn       func(ctx context.Context) (*db.PortfolioBreakdown, error)
	netFlowSummaryFn           func(ctx context.Context, since time.Time, excludeTransfers bool) (*db.NetFlowResult, error)
	netFlowSummaryBySourceFn   func(ctx context.Context, since time.Time, sources []string, excludeTransfers bool) (*db.NetFlowResult, error)
	setTransferFlagFn          func(ctx context.Context, sourceID string, isTransfer bool) error
	getTransferFlagFn          func(ctx context.Context, sourceID string) (bool, error)
	listTransferCandidatesFn   func(ctx context.Context, sourceID string, amountSat int64, ts time.Time) ([]db.TransferCandidate, error)
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
func (m *mockStore) ListExchangeTransactions(ctx context.Context, source string, limit, offset int) (*db.ExchangeTransactionPage, error) {
	if m.listExchangeTransactionsFn != nil {
		return m.listExchangeTransactionsFn(ctx, source, limit, offset)
	}
	return &db.ExchangeTransactionPage{}, nil
}
func (m *mockStore) PortfolioPosition(ctx context.Context, since time.Time) (*db.PortfolioPositionResult, error) {
	if m.portfolioPositionFn != nil {
		return m.portfolioPositionFn(ctx, since)
	}
	return &db.PortfolioPositionResult{BySource: map[string]db.SourceBalance{}}, nil
}
func (m *mockStore) PortfolioSnapshots(ctx context.Context, days int) ([]db.PortfolioSnapshot, error) {
	if m.portfolioSnapshotsFn != nil {
		return m.portfolioSnapshotsFn(ctx, days)
	}
	return nil, nil
}
func (m *mockStore) BackfillPortfolioSnapshots(ctx context.Context) (int, error) {
	return 0, nil
}
func (m *mockStore) PortfolioBreakdownQuery(ctx context.Context) (*db.PortfolioBreakdown, error) {
	if m.portfolioBreakdownFn != nil {
		return m.portfolioBreakdownFn(ctx)
	}
	return &db.PortfolioBreakdown{ExchangeSats: map[string]int64{}}, nil
}
func (m *mockStore) NetFlowSummary(ctx context.Context, since time.Time, excludeTransfers bool) (*db.NetFlowResult, error) {
	if m.netFlowSummaryFn != nil {
		return m.netFlowSummaryFn(ctx, since, excludeTransfers)
	}
	return &db.NetFlowResult{}, nil
}
func (m *mockStore) NetFlowSummaryBySource(ctx context.Context, since time.Time, sources []string, excludeTransfers bool) (*db.NetFlowResult, error) {
	if m.netFlowSummaryBySourceFn != nil {
		return m.netFlowSummaryBySourceFn(ctx, since, sources, excludeTransfers)
	}
	return &db.NetFlowResult{}, nil
}
func (m *mockStore) SetTransferFlag(ctx context.Context, sourceID string, isTransfer bool) error {
	if m.setTransferFlagFn != nil {
		return m.setTransferFlagFn(ctx, sourceID, isTransfer)
	}
	return nil
}
func (m *mockStore) GetTransferFlag(ctx context.Context, sourceID string) (bool, error) {
	if m.getTransferFlagFn != nil {
		return m.getTransferFlagFn(ctx, sourceID)
	}
	return false, nil
}
func (m *mockStore) GetTransactionNote(ctx context.Context, sourceID string) (string, error) {
	return "", nil
}
func (m *mockStore) ListTransferCandidates(ctx context.Context, sourceID string, amountSat int64, ts time.Time) ([]db.TransferCandidate, error) {
	if m.listTransferCandidatesFn != nil {
		return m.listTransferCandidatesFn(ctx, sourceID, amountSat, ts)
	}
	return nil, nil
}
func (m *mockStore) StrikeCollateralSats(ctx context.Context) (int64, error) { return 0, nil }
func (m *mockStore) AnnualReport(ctx context.Context, year int) (*db.AnnualReportData, error) {
	return &db.AnnualReportData{Year: year}, nil
}
func (m *mockStore) AvailableReportYears(ctx context.Context) ([]int, error) {
	return []int{2026, 2025}, nil
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

func (m *mockImportStore) ImportSwanCSV(ctx context.Context, rows []exchange.SwanRow) (*db.ImportSummary, error) {
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

	// Phase 2 (confirm=yes): post a pre-classified row and expect import result.
	rawLine := "tx-001,Jan 15 2024 10:30:00,Purchase,67.00,0.50,0.00100000,0.00000500,94000.00,67.00,,Buy,,"
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("confirm", "yes")
	writer.WriteField("row", rawLine)
	writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/import/strike", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
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

func TestHandleStrikeImport_HTMXPreviewPhase(t *testing.T) {
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))

	csv := strikeHeader + "tx-001,Jan 15 2024 10:30:00,Purchase,67.00,0.50,0.001,,94000.00,67.00,,,,\n"
	req, w := createMultipartCSV(t, csv)
	req.Header.Set("HX-Request", "true")
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("expected text/html for HTMX preview, got %q", w.Header().Get("Content-Type"))
	}
	// Phase 1 HTMX returns the preview partial
	body := w.Body.String()
	if !strings.Contains(body, "Preview Import") {
		t.Errorf("expected preview HTML, got: %s", body)
	}
}

func TestHandleStrikeImport_HTMXConfirmPhase(t *testing.T) {
	importStore := &mockImportStore{
		importStrikeFn: func(_ context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error) {
			return &db.ImportSummary{Total: 1, NewPurchases: 1}, nil
		},
	}
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	rawLine := "tx-001,Jan 15 2024 10:30:00,Purchase,67.00,0.50,0.00100000,0.00000500,94000.00,67.00,,Buy,,"
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("confirm", "yes")
	writer.WriteField("row", rawLine)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/import/strike", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("expected text/html, got %q", w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "new purchase") {
		t.Errorf("expected import result HTML with 'new purchase', got: %s", w.Body.String())
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

// --- Portfolio page tests ---

func TestHandlePortfolioPage_Success(t *testing.T) {
	store := &mockStore{
		portfolioBreakdownFn: func(_ context.Context) (*db.PortfolioBreakdown, error) {
			return &db.PortfolioBreakdown{
				TotalSats:       1_000_000,
				OnChainSats:     500_000,
				ChannelSats:     300_000,
				ColdStorageSats: 200_000,
				ExchangeSats:    map[string]int64{},
			}, nil
		},
		netFlowSummaryFn: func(_ context.Context, _ time.Time, _ bool) (*db.NetFlowResult, error) {
			return &db.NetFlowResult{InflowSats: 100_000, InflowCount: 5, OutflowSats: 50_000, OutflowCount: 3}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{price: 60000})
	req := httptest.NewRequest(http.MethodGet, "/portfolio?period=30d", nil)
	w := httptest.NewRecorder()
	h.HandlePortfolioPage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "1,000,000") {
		t.Error("expected total sats in response")
	}
	if !strings.Contains(body, "donut-svg") {
		t.Error("expected donut chart SVG in response")
	}
	if !strings.Contains(body, "Net Flows") {
		t.Error("expected net flows section")
	}
}

func TestHandlePortfolioPage_AllPeriods(t *testing.T) {
	store := &mockStore{}
	h := newTestHandler(store, nil, &mockPrice{})
	for _, period := range []string{"30d", "90d", "ytd", "all"} {
		req := httptest.NewRequest(http.MethodGet, "/portfolio?period="+period, nil)
		w := httptest.NewRecorder()
		h.HandlePortfolioPage(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("period %s: expected 200, got %d", period, w.Code)
		}
	}
}

func TestHandlePortfolioPage_WithCostBasis(t *testing.T) {
	store := &mockStore{
		portfolioBreakdownFn: func(_ context.Context) (*db.PortfolioBreakdown, error) {
			return &db.PortfolioBreakdown{TotalSats: 500_000, ExchangeSats: map[string]int64{}}, nil
		},
		portfolioPositionFn: func(_ context.Context, _ time.Time) (*db.PortfolioPositionResult, error) {
			return &db.PortfolioPositionResult{
				TotalCostBasisUSD: 200.00,
				PurchasedSats:     500_000,
				BySource:          map[string]db.SourceBalance{},
			}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{price: 60000})
	req := httptest.NewRequest(http.MethodGet, "/portfolio", nil)
	w := httptest.NewRecorder()
	h.HandlePortfolioPage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Cost Basis") {
		t.Error("expected cost basis section")
	}
	if !strings.Contains(body, "$200.00") {
		t.Error("expected total cost basis USD")
	}
}

// --- Portfolio source flows tests ---

func TestHandlePortfolioSourceFlows_Success(t *testing.T) {
	store := &mockStore{
		netFlowSummaryBySourceFn: func(_ context.Context, _ time.Time, sources []string, _ bool) (*db.NetFlowResult, error) {
			if len(sources) != 1 || sources[0] != "strike" {
				t.Errorf("expected sources [strike], got %v", sources)
			}
			return &db.NetFlowResult{InflowSats: 50_000, InflowCount: 2, OutflowSats: 10_000, OutflowCount: 1}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/portfolio/source-flows?source=strike&period=30d", nil)
	w := httptest.NewRecorder()
	h.HandlePortfolioSourceFlows(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Strike") {
		t.Error("expected source label in response")
	}
	if !strings.Contains(body, "50,000") {
		t.Error("expected inflow sats in response")
	}
}

func TestHandlePortfolioSourceFlows_InvalidSource(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/portfolio/source-flows?source=unknown&period=30d", nil)
	w := httptest.NewRecorder()
	h.HandlePortfolioSourceFlows(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty), got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Error("expected empty body for unknown source")
	}
}

func TestHandlePortfolioSourceFlows_AllPeriods(t *testing.T) {
	store := &mockStore{
		netFlowSummaryBySourceFn: func(_ context.Context, _ time.Time, _ []string, _ bool) (*db.NetFlowResult, error) {
			return &db.NetFlowResult{}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	for _, period := range []string{"30d", "90d", "ytd", "all"} {
		req := httptest.NewRequest(http.MethodGet, "/portfolio/source-flows?source=onchain&period="+period, nil)
		w := httptest.NewRecorder()
		h.HandlePortfolioSourceFlows(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("period %s: expected 200, got %d", period, w.Code)
		}
	}
}

// --- Transfer toggle tests ---

func TestHandleTransferToggle_Success(t *testing.T) {
	flagState := false
	store := &mockStore{
		getTransferFlagFn: func(_ context.Context, _ string) (bool, error) {
			return flagState, nil
		},
		setTransferFlagFn: func(_ context.Context, sourceID string, isTransfer bool) error {
			if sourceID != "strike:abc:Buy" {
				t.Errorf("unexpected sourceID: %s", sourceID)
			}
			flagState = isTransfer
			return nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/transfer", strings.NewReader("source_id=strike:abc:Buy"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransferToggle(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !flagState {
		t.Error("expected flag to be toggled to true")
	}
}

func TestHandleTransferToggle_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/transfer", nil)
	w := httptest.NewRecorder()
	h.HandleTransferToggle(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleTransferToggle_MissingSourceID(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/transfer", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransferToggle(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleTransferToggle_StoreError(t *testing.T) {
	store := &mockStore{
		setTransferFlagFn: func(_ context.Context, _ string, _ bool) error {
			return errors.New("db error")
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/transfer", strings.NewReader("source_id=test:1:Buy"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransferToggle(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- Transfer bulk tests ---

func TestHandleTransferBulk_Success(t *testing.T) {
	var flaggedIDs []string
	store := &mockStore{
		setTransferFlagFn: func(_ context.Context, sourceID string, _ bool) error {
			flaggedIDs = append(flaggedIDs, sourceID)
			return nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/transfer/bulk",
		strings.NewReader("source_id=strike:abc:Buy&candidate_id=lnd_onchain:def:Send"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransferBulk(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(flaggedIDs) != 2 {
		t.Fatalf("expected 2 flags set, got %d", len(flaggedIDs))
	}
	if flaggedIDs[0] != "strike:abc:Buy" || flaggedIDs[1] != "lnd_onchain:def:Send" {
		t.Errorf("unexpected flagged IDs: %v", flaggedIDs)
	}
}

func TestHandleTransferBulk_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/transfer/bulk", nil)
	w := httptest.NewRecorder()
	h.HandleTransferBulk(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleTransferBulk_MissingSourceID(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/transfer/bulk", strings.NewReader("candidate_id=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransferBulk(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleTransferBulk_NoCandidateID(t *testing.T) {
	var flaggedIDs []string
	store := &mockStore{
		setTransferFlagFn: func(_ context.Context, sourceID string, _ bool) error {
			flaggedIDs = append(flaggedIDs, sourceID)
			return nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/transfer/bulk",
		strings.NewReader("source_id=strike:abc:Buy"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransferBulk(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(flaggedIDs) != 1 {
		t.Errorf("expected 1 flag set, got %d", len(flaggedIDs))
	}
}

// --- Transfer candidates tests ---

func TestHandleTransferCandidates_Success(t *testing.T) {
	ts := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	store := &mockStore{
		listTransferCandidatesFn: func(_ context.Context, sourceID string, amountSat int64, at time.Time) ([]db.TransferCandidate, error) {
			return []db.TransferCandidate{
				{SourceID: "lnd_onchain:abc:Send", Source: "lnd_onchain", AmountSat: 50000, Time: ts},
			}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/transfer/candidates?source_id=strike:xyz:Buy&amount_sat=50000&ts="+ts.Format(time.RFC3339), nil)
	w := httptest.NewRecorder()
	h.HandleTransferCandidates(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "lnd_onchain:abc:Send") {
		t.Error("expected candidate source_id in response")
	}
}

func TestHandleTransferCandidates_MissingParams(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/transfer/candidates?source_id=x", nil)
	w := httptest.NewRecorder()
	h.HandleTransferCandidates(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (renders empty popover), got %d", w.Code)
	}
}

func TestHandleTransferCandidates_NoMatches(t *testing.T) {
	ts := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	store := &mockStore{
		listTransferCandidatesFn: func(_ context.Context, _ string, _ int64, _ time.Time) ([]db.TransferCandidate, error) {
			return nil, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/transfer/candidates?source_id=strike:xyz:Buy&amount_sat=50000&ts="+ts.Format(time.RFC3339), nil)
	w := httptest.NewRecorder()
	h.HandleTransferCandidates(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No matches") {
		t.Error("expected no-match message in response")
	}
}

// --- donutChart template function tests ---

func TestDonutChart_Empty(t *testing.T) {
	result := donutChart(nil, "30d")
	if !strings.Contains(string(result), "chart-empty") {
		t.Error("expected chart-empty for nil items")
	}
}

func TestDonutChart_SingleItem(t *testing.T) {
	items := []BreakdownItem{
		{Label: "On-chain", SourceKey: "onchain", Clickable: true, Sats: 100_000, USD: 60.0, Pct: 100.0},
	}
	result := string(donutChart(items, "30d"))
	if !strings.Contains(result, "donut-svg") {
		t.Error("expected SVG with id donut-svg")
	}
	if !strings.Contains(result, `data-source="onchain"`) {
		t.Error("expected data-source attribute")
	}
	if !strings.Contains(result, `data-clickable="true"`) {
		t.Error("expected data-clickable attribute for clickable item")
	}
	if !strings.Contains(result, `data-period="30d"`) {
		t.Error("expected data-period attribute")
	}
}

func TestDonutChart_MultipleItems(t *testing.T) {
	items := []BreakdownItem{
		{Label: "On-chain", SourceKey: "onchain", Clickable: true, Sats: 60_000, USD: 36.0, Pct: 60.0},
		{Label: "Channels", SourceKey: "channels", Clickable: false, Sats: 40_000, USD: 24.0, Pct: 40.0},
	}
	result := string(donutChart(items, "90d"))
	if !strings.Contains(result, `data-source="onchain"`) {
		t.Error("expected onchain segment")
	}
	if !strings.Contains(result, `data-source="channels"`) {
		t.Error("expected channels segment")
	}
	// Channels should NOT have data-clickable
	if strings.Contains(result, `data-source="channels"`) {
		// Find the channels segment and verify no clickable
		idx := strings.Index(result, `data-source="channels"`)
		// Look backwards to find this circle's start
		segStart := strings.LastIndex(result[:idx], "<circle")
		segEnd := strings.Index(result[idx:], "</circle>") + idx
		seg := result[segStart:segEnd]
		if strings.Contains(seg, `data-clickable`) {
			t.Error("channels segment should not be clickable")
		}
	}
}

func TestDonutChart_ZeroPctSkipped(t *testing.T) {
	items := []BreakdownItem{
		{Label: "On-chain", SourceKey: "onchain", Sats: 100_000, Pct: 100.0},
		{Label: "Empty", SourceKey: "empty", Sats: 0, Pct: 0.0},
	}
	result := string(donutChart(items, "30d"))
	if strings.Contains(result, `data-source="empty"`) {
		t.Error("zero-pct item should be skipped")
	}
}

// --- HandleHealth tests ---

func TestHandleHealth_DBOK(t *testing.T) {
	store := &mockStore{
		lastSyncedAtFn: func(_ context.Context) (time.Time, error) {
			return time.Now(), nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	h.SetVersion("1.2.3")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.HandleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("expected status ok, got %s", body)
	}
	if !strings.Contains(body, `"version":"1.2.3"`) {
		t.Errorf("expected version 1.2.3, got %s", body)
	}
	if !strings.Contains(body, `"db_ok":true`) {
		t.Errorf("expected db_ok true, got %s", body)
	}
	if !strings.Contains(body, `"lnd_configured":false`) {
		t.Errorf("expected lnd_configured false, got %s", body)
	}
}

func TestHandleHealth_DBError(t *testing.T) {
	store := &mockStore{
		lastSyncedAtFn: func(_ context.Context) (time.Time, error) {
			return time.Time{}, errors.New("db unavailable")
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.HandleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even on DB error (degraded), got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"db_ok":false`) {
		t.Errorf("expected db_ok false, got %s", w.Body.String())
	}
}

func TestHandleHealth_DefaultVersion(t *testing.T) {
	store := &mockStore{
		lastSyncedAtFn: func(_ context.Context) (time.Time, error) {
			return time.Now(), nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})
	// version not set — should default to "dev"

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.HandleHealth(w, req)

	if !strings.Contains(w.Body.String(), `"version":"dev"`) {
		t.Errorf("expected version dev, got %s", w.Body.String())
	}
}

func TestHandleHealth_LNDConfigured(t *testing.T) {
	store := &mockStore{
		lastSyncedAtFn: func(_ context.Context) (time.Time, error) {
			return time.Now(), nil
		},
	}
	node := &mockNodeInfo{info: &lnd.NodeInfo{Alias: "test-node"}}
	h := newTestHandler(store, node, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	h.HandleHealth(w, req)

	if !strings.Contains(w.Body.String(), `"lnd_configured":true`) {
		t.Errorf("expected lnd_configured true, got %s", w.Body.String())
	}
}

// --- mockTransactionStore for export tests ---

type mockTransactionStore struct {
	result *db.UnifiedTransactionPage
	err    error
}

func (m *mockTransactionStore) ListUnifiedTransactions(_ context.Context, _ db.TransactionFilter) (*db.UnifiedTransactionPage, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return &db.UnifiedTransactionPage{}, nil
}

func (m *mockTransactionStore) SetTransactionNote(_ context.Context, _, _ string) error { return nil }
func (m *mockTransactionStore) DistinctTransactionValues(_ context.Context) ([]string, []string, error) {
	return nil, nil, nil
}

func TestHandleExportTransactions_CSV(t *testing.T) {
	ts := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	store := &mockTransactionStore{
		result: &db.UnifiedTransactionPage{
			Transactions: []db.UnifiedTransaction{
				{Source: "strike", SourceID: "tx-1", Time: ts, TxType: "buy", AmountSat: 500000, AmountUSD: 300.00, FeeSat: 100, FeeUSD: 0.06, Memo: "DCA purchase"},
				{Source: "lnd_forward", SourceID: "fwd-1", Time: ts, TxType: "fee_income", AmountSat: 250, Memo: ""},
			},
			Total: 2,
		},
	}

	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	h.SetTransactionStore(store)

	req := httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil)
	w := httptest.NewRecorder()
	h.HandleExportTransactions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("expected text/csv, got %s", ct)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Error("expected attachment content-disposition")
	}

	body := w.Body.String()
	if !strings.HasPrefix(body, "date,type,source,amount_sats,amount_usd,fee_sats,fee_usd,txid,memo") {
		t.Errorf("unexpected CSV header: %q", strings.SplitN(body, "\n", 2)[0])
	}
	if !strings.Contains(body, "strike") {
		t.Error("expected strike row in CSV")
	}
	if !strings.Contains(body, "DCA purchase") {
		t.Error("expected memo in CSV")
	}
	if !strings.Contains(body, "500000") {
		t.Error("expected amount_sats in CSV")
	}
}

func TestHandleExportTransactions_NoStore(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	// txStore intentionally not set

	req := httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil)
	w := httptest.NewRecorder()
	h.HandleExportTransactions(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleExportTransactions_StoreError(t *testing.T) {
	store := &mockTransactionStore{err: errors.New("db error")}

	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	h.SetTransactionStore(store)

	req := httptest.NewRequest(http.MethodGet, "/api/export/transactions", nil)
	w := httptest.NewRecorder()
	h.HandleExportTransactions(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// --- HandlePortfolioBackfill tests (Issue #66: portfolio snapshot history) ---

func TestHandlePortfolioBackfill_Success(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodPost, "/api/portfolio/backfill", nil)
	w := httptest.NewRecorder()
	h.HandlePortfolioBackfill(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePortfolioBackfill_WrongMethod(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/portfolio/backfill", nil)
	w := httptest.NewRecorder()
	h.HandlePortfolioBackfill(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandlePortfolioBackfill_HTMXResponse(t *testing.T) {
	h := newTestHandler(&mockStore{}, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodPost, "/api/portfolio/backfill", nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandlePortfolioBackfill(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected HTML response for HTMX request, got %q", ct)
	}
}

// --- Import preview/confirm tests (#147) ---

const strikeHeader = "Reference,Date & Time (UTC),Transaction Type,Amount USD,Fee USD,Amount BTC,Fee BTC,BTC Price,Cost Basis (USD),Destination,Description,Transaction Hash,Note\n"

// TestHandleStrikeImport_PreviewPhase verifies that posting without confirm=yes
// returns a preview and does NOT write to the DB.
func TestHandleStrikeImport_PreviewPhase(t *testing.T) {
	imported := 0
	importStore := &mockImportStore{
		importStrikeFn: func(_ context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error) {
			imported += len(rows)
			return &db.ImportSummary{Total: len(rows), NewPurchases: len(rows)}, nil
		},
	}
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	csv := strikeHeader +
		"tx-001,Jan 15 2025 10:00:00,Purchase,500.00,2.50,0.005,,100000.00,500.00,,Buy BTC,,\n" +
		"tx-002,Jan 16 2025 11:00:00,Withdraw,,,0.001,,,,,wallet,txhash,\n"

	req, w := createMultipartCSV(t, csv)
	h.HandleStrikeImport(w, req)

	// No confirm — should return JSON preview, not write to DB
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if imported != 0 {
		t.Errorf("expected 0 rows imported in preview phase, got %d", imported)
	}
	var preview map[string]int
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode preview: %v", err)
	}
	if preview["acquisitions"] != 1 {
		t.Errorf("expected 1 acquisition, got %d", preview["acquisitions"])
	}
	if preview["ignored"] != 1 {
		t.Errorf("expected 1 ignored row (Withdraw), got %d", preview["ignored"])
	}
}

// TestHandleStrikeImport_UnrecognizedTypeAppearsIgnored verifies that rows with
// unknown transaction types are classified as ignored and do NOT get imported.
func TestHandleStrikeImport_UnrecognizedTypeAppearsIgnored(t *testing.T) {
	imported := 0
	importStore := &mockImportStore{
		importStrikeFn: func(_ context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error) {
			imported += len(rows)
			return &db.ImportSummary{Total: len(rows)}, nil
		},
	}
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	// CSV with only unrecognized types: Send, Transfer, Withdraw
	csv := strikeHeader +
		"tx-001,Jan 15 2025 10:00:00,Send,,,0.001,,,,,payment,txhash,\n" +
		"tx-002,Jan 16 2025 11:00:00,Transfer,,,0.002,,,,,transfer,txhash2,\n" +
		"tx-003,Jan 17 2025 12:00:00,Withdraw,,,0.003,,,,,withdraw,txhash3,\n"

	req, w := createMultipartCSV(t, csv)
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var preview map[string]int
	if err := json.NewDecoder(w.Body).Decode(&preview); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if preview["ignored"] != 3 {
		t.Errorf("expected 3 ignored rows, got %d", preview["ignored"])
	}
	if preview["acquisitions"] != 0 {
		t.Errorf("expected 0 acquisitions, got %d", preview["acquisitions"])
	}
	if imported != 0 {
		t.Errorf("expected 0 rows written to DB before confirm, got %d", imported)
	}
}

// TestHandleStrikeImport_ConfirmWritesToDB verifies that confirm=yes posts
// the pre-classified rows and writes them to the DB.
func TestHandleStrikeImport_ConfirmWritesToDB(t *testing.T) {
	var importedRows []exchange.StrikeRow
	importStore := &mockImportStore{
		importStrikeFn: func(_ context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error) {
			importedRows = rows
			return &db.ImportSummary{Total: len(rows), NewPurchases: len(rows)}, nil
		},
	}
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	// Send confirm=yes with pre-classified row data (as the confirm form would)
	rawLine := "tx-001,Jan 15 2025 10:00:00,Purchase,500.00,2.50,0.00500000,0.00002500,100000.00,500.00,,Buy BTC,,"
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("confirm", "yes")
	writer.WriteField("row", rawLine)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/import/strike", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(importedRows) != 1 {
		t.Fatalf("expected 1 row imported, got %d", len(importedRows))
	}
	if importedRows[0].Type != "Purchase" {
		t.Errorf("expected imported row type Purchase, got %q", importedRows[0].Type)
	}
}

// TestHandleStrikeImport_IgnoredRowsNotImportedOnConfirm verifies that even if
// ignored rows somehow appear in the confirm POST, they are not written to the DB.
func TestHandleStrikeImport_IgnoredRowsNotImportedOnConfirm(t *testing.T) {
	var importedRows []exchange.StrikeRow
	importStore := &mockImportStore{
		importStrikeFn: func(_ context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error) {
			importedRows = rows
			return &db.ImportSummary{Total: len(rows)}, nil
		},
	}
	h := NewHandler(&mockStore{}, nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	withdrawLine := "tx-bad,Jan 15 2025 10:00:00,Withdraw,,,0.001,,,,,wallet,txhash,"
	purchaseLine := "tx-ok,Jan 15 2025 10:00:00,Purchase,500.00,2.50,0.00500000,0.00002500,100000.00,500.00,,Buy,,"

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("confirm", "yes")
	writer.WriteField("row", withdrawLine)
	writer.WriteField("row", purchaseLine)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/import/strike", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleStrikeImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// Only the Purchase row should be imported — Withdraw is ignored
	for _, r := range importedRows {
		if r.IsIgnored() {
			t.Errorf("ignored row type %q was written to DB", r.Type)
		}
	}
	if len(importedRows) != 1 {
		t.Errorf("expected 1 row imported (Purchase only), got %d", len(importedRows))
	}
}
