package web

import (
	"bytes"
	"context"
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

type mockWalletStore struct {
	wallets []db.WatchedWallet
	wallet  *db.WatchedWallet
}

func (m *mockWalletStore) AddWallet(_ context.Context, label, wType, value, derivationType string) (int64, error) {
	return 1, nil
}
func (m *mockWalletStore) RemoveWallet(_ context.Context, id int64) error { return nil }
func (m *mockWalletStore) ListWallets(_ context.Context) ([]db.WatchedWallet, error) {
	return m.wallets, nil
}
func (m *mockWalletStore) GetWallet(_ context.Context, id int64) (*db.WatchedWallet, error) {
	if m.wallet != nil {
		return m.wallet, nil
	}
	return nil, errors.New("not found")
}
func (m *mockWalletStore) UpdateWalletBalance(_ context.Context, id int64, sats int64) error {
	return nil
}
func (m *mockWalletStore) TotalWatchedBalance(_ context.Context) (int64, error) { return 0, nil }

func fullMockStore() *mockStore {
	now := time.Now().UTC()
	return &mockStore{
		feeSummaryFn: func(_ context.Context, since time.Time) (int64, int64, error) {
			if since.IsZero() {
				return 6000, 3, nil
			}
			return 3000, 2, nil
		},
		activeChannelFn: func(_ context.Context) (int, error) { return 5, nil },
		latestWalletFn: func(_ context.Context) (*db.WalletBalanceSnapshot, error) {
			return &db.WalletBalanceSnapshot{TotalSat: 100000}, nil
		},
		channelStatsFn: func(_ context.Context) ([]db.ChannelStat, error) {
			return []db.ChannelStat{
				{ChanID: 100, RemotePubKey: "abc123", LocalBalance: 50000, RemoteBalance: 50000, Active: true, FeesEarnedAllTimeMsat: 5000},
			}, nil
		},
		dailyFeesFn: func(_ context.Context, _ time.Time) ([]db.DailyFeeStat, error) {
			return []db.DailyFeeStat{
				{Day: now.AddDate(0, 0, -1).Format("2006-01-02"), TotalFeeMsat: 3000, Count: 2},
				{Day: now.Format("2006-01-02"), TotalFeeMsat: 2000, Count: 1},
			}, nil
		},
		lastSyncedAtFn: func(_ context.Context) (time.Time, error) {
			return now.Add(-5 * time.Minute), nil
		},
		forwardingEventsFn: func(_ context.Context, _, _ time.Time, _, _ int) (*db.ForwardingPage, error) {
			return &db.ForwardingPage{
				Events: []db.ForwardingEvent{
					{Timestamp: now, ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
				},
				Total: 1,
			}, nil
		},
		portfolioPositionFn: func(_ context.Context, since time.Time) (*db.PortfolioPositionResult, error) {
			if since.IsZero() {
				return &db.PortfolioPositionResult{
					BySource: map[string]db.SourceBalance{
						"strike": {Source: "strike", NetSats: 100000, PurchasedSats: 100000},
					},
					ExchangeNetSats: 100000,
					PurchasedSats:   100000,
					RoutingFeesSats: 6, // 6000 msat
					RoutedCount:     3,
				}, nil
			}
			return &db.PortfolioPositionResult{
				BySource:        map[string]db.SourceBalance{},
				ExchangeNetSats: 50000,
				PurchasedSats:   50000,
				RoutingFeesSats: 3,
				RoutedCount:     2,
			}, nil
		},
	}
}

func TestHandleDashboard_OnboardingFlags(t *testing.T) {
	tests := []struct {
		name                string
		portfolio           *db.PortfolioPositionResult
		nodeErr             error
		wantOnboarding      bool
		wantImportBanner    bool
		wantLNDBanner       bool
	}{
		{
			name: "empty state shows onboarding",
			portfolio: &db.PortfolioPositionResult{
				BySource: map[string]db.SourceBalance{},
			},
			wantOnboarding: true,
		},
		{
			name: "fees only shows import banner",
			portfolio: &db.PortfolioPositionResult{
				BySource:        map[string]db.SourceBalance{},
				RoutingFeesSats: 5000,
			},
			wantImportBanner: true,
		},
		{
			name: "exchange only without LND shows LND banner",
			portfolio: &db.PortfolioPositionResult{
				BySource:        map[string]db.SourceBalance{"strike": {NetSats: 100000}},
				ExchangeNetSats: 100000,
			},
			nodeErr:       errTest,
			wantLNDBanner: true,
		},
		{
			name: "everything connected — no banners",
			portfolio: &db.PortfolioPositionResult{
				BySource:        map[string]db.SourceBalance{"strike": {NetSats: 100000}},
				ExchangeNetSats: 100000,
				RoutingFeesSats: 5000,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &mockStore{
				feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
				activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
				latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
				channelStatsFn:  func(_ context.Context) ([]db.ChannelStat, error) { return nil, nil },
				forwardingEventsFn: func(_ context.Context, _, _ time.Time, _, _ int) (*db.ForwardingPage, error) {
					return &db.ForwardingPage{}, nil
				},
				portfolioPositionFn: func(_ context.Context, _ time.Time) (*db.PortfolioPositionResult, error) {
					return tc.portfolio, nil
				},
			}
			node := &mockNodeInfo{info: &lnd.NodeInfo{Alias: "n"}, err: tc.nodeErr}
			price := &mockPrice{price: 67000}
			h := newTestHandler(store, node, price)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			w := httptest.NewRecorder()
			h.HandleDashboard(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			body := w.Body.String()

			hasOnboarding := strings.Contains(body, "Welcome to Satsbook")
			hasImportBanner := strings.Contains(body, "Track your full position")
			hasLNDBanner := strings.Contains(body, "LND not connected:")

			if hasOnboarding != tc.wantOnboarding {
				t.Errorf("onboarding: got %v, want %v", hasOnboarding, tc.wantOnboarding)
			}
			if hasImportBanner != tc.wantImportBanner {
				t.Errorf("import banner: got %v, want %v", hasImportBanner, tc.wantImportBanner)
			}
			if hasLNDBanner != tc.wantLNDBanner {
				t.Errorf("LND banner: got %v, want %v", hasLNDBanner, tc.wantLNDBanner)
			}
		})
	}
}

func TestHandleDashboard_Success(t *testing.T) {
	store := fullMockStore()
	node := &mockNodeInfo{info: &lnd.NodeInfo{
		Alias: "test-node", PubKey: "02abc", Synced: true,
		NumActiveChannels: 5, BlockHeight: 800000, Version: "0.17.0",
	}}
	price := &mockPrice{price: 67000.0, fetchedAt: time.Now()}
	h := newTestHandler(store, node, price)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.HandleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html content type, got %q", ct)
	}

	body := w.Body.String()

	// Check key elements are present (lightning-specific content moved to /lightning)
	checks := []string{
		"Satsbook",
		"test-node",
		"Total BTC Position",
		"Portfolio Value",
		"synced",
		"$67,000.00",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("expected body to contain %q", check)
		}
	}
}

func TestHandleDashboard_NotFoundForOtherPaths(t *testing.T) {
	h := newTestHandler(fullMockStore(), &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	h.HandleDashboard(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for /nonexistent, got %d", w.Code)
	}
}

func TestHandleLightningPage_Success(t *testing.T) {
	store := fullMockStore()
	node := &mockNodeInfo{info: &lnd.NodeInfo{
		Alias: "test-node", PubKey: "02abc", Synced: true,
		NumActiveChannels: 5, BlockHeight: 800000, Version: "0.17.0",
	}}
	price := &mockPrice{price: 67000.0, fetchedAt: time.Now()}
	h := newTestHandler(store, node, price)

	req := httptest.NewRequest(http.MethodGet, "/lightning", nil)
	w := httptest.NewRecorder()
	h.HandleLightningPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	checks := []string{
		"Lightning Node",
		"Fee Income",
		"Payments Routed",
		"Active Channels",
		"Wallet Balance",
		"Daily Fee Income",
		"Channels",
		"Forwarding Events",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("expected body to contain %q", check)
		}
	}
}

func TestHandleDashboard_NodeDown(t *testing.T) {
	store := fullMockStore()
	node := &mockNodeInfo{err: errTest}
	price := &mockPrice{err: errTest}
	h := newTestHandler(store, node, price)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.HandleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even with node down, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "node unreachable") {
		t.Error("expected 'node unreachable' banner when node is down")
	}
	if !strings.Contains(body, "price unavailable") {
		t.Error("expected 'price unavailable' when price fails")
	}
}

func TestHandleForwardingPartial_Success(t *testing.T) {
	now := time.Now().UTC()
	store := &mockStore{
		forwardingEventsFn: func(_ context.Context, _, _ time.Time, limit, offset int) (*db.ForwardingPage, error) {
			return &db.ForwardingPage{
				Events: []db.ForwardingEvent{
					{Timestamp: now, ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
				},
				Total: 1,
			}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/partials/forwarding", nil)
	w := httptest.NewRecorder()
	h.HandleForwardingPartial(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Should be a partial — no <html> wrapper
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("partial should not contain full HTML document")
	}
	// Should contain table
	if !strings.Contains(body, "<table>") {
		t.Error("expected table in partial")
	}
	if !strings.Contains(body, "Showing 1-1 of 1") {
		t.Errorf("expected pagination info, got: %s", body)
	}
}

func TestHandleForwardingPartial_Empty(t *testing.T) {
	store := &mockStore{
		forwardingEventsFn: func(_ context.Context, _, _ time.Time, _, _ int) (*db.ForwardingPage, error) {
			return &db.ForwardingPage{Events: nil, Total: 0}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/partials/forwarding", nil)
	w := httptest.NewRecorder()
	h.HandleForwardingPartial(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No forwarding events") {
		t.Error("expected empty state message")
	}
}

func TestHandleForwardingPartial_Pagination(t *testing.T) {
	store := &mockStore{
		forwardingEventsFn: func(_ context.Context, _, _ time.Time, limit, offset int) (*db.ForwardingPage, error) {
			if limit != 10 || offset != 10 {
				t.Errorf("expected limit=10, offset=10, got limit=%d, offset=%d", limit, offset)
			}
			return &db.ForwardingPage{
				Events: []db.ForwardingEvent{
					{Timestamp: time.Now(), ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
				},
				Total: 25,
			}, nil
		},
	}
	h := newTestHandler(store, nil, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/partials/forwarding?page=2&limit=10", nil)
	w := httptest.NewRecorder()
	h.HandleForwardingPartial(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Previous") {
		t.Error("expected Previous link on page 2")
	}
	if !strings.Contains(body, "Next") {
		t.Error("expected Next link when more pages exist")
	}
}

var errTest = errors.New("test error")

// --- HandlePLPage tests ---

func TestHandlePLPage_Success(t *testing.T) {
	store := fullMockStore()
	store.exchangeSummaryFn = func(_ context.Context, _ string, _ time.Time) (*db.ExchangeSummaryResult, error) {
		return &db.ExchangeSummaryResult{
			PurchasedSats:        500000,
			ReceivedSats:         100000,
			SoldSats:             50000,
			SentSats:             25000,
			TotalCostBasisUSD:    335.00,
			TotalSaleProceedsUSD: 40.00,
			FeesPaidUSD:          2.50,
		}, nil
	}
	node := &mockNodeInfo{info: &lnd.NodeInfo{
		Alias: "test-node", PubKey: "02abc", Synced: true, BlockHeight: 800000, Version: "0.17.0",
	}}
	price := &mockPrice{price: 67000.0, fetchedAt: time.Now()}
	h := newTestHandler(store, node, price)

	req := httptest.NewRequest(http.MethodGet, "/pl", nil)
	w := httptest.NewRecorder()
	h.HandlePLPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	checks := []string{
		"informational purposes only",
		"Routing Fee Income",
		"BTC Purchased",
		"BTC Sold",
		"BTC Received",
		"BTC Sent",
		"Net BTC This Period",
		"Net USD Spent",
		"30 days",
		"90 days",
		"YTD",
		"All time",
		"test-node",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Errorf("expected body to contain %q", check)
		}
	}
}

func TestHandlePLPage_DefaultPeriod(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/pl", nil)
	w := httptest.NewRecorder()
	h.HandlePLPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// 30d button should be active (has accent background)
	body := w.Body.String()
	if !strings.Contains(body, `period=30d`) {
		t.Error("expected 30d period link")
	}
}

func TestHandlePLPage_AllPeriods(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	for _, period := range []string{"30d", "90d", "ytd", "all"} {
		req := httptest.NewRequest(http.MethodGet, "/pl?period="+period, nil)
		w := httptest.NewRecorder()
		h.HandlePLPage(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("period=%s: expected 200, got %d", period, w.Code)
		}
	}
}

func TestHandlePLPage_InvalidPeriodFallsBack(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/pl?period=invalid", nil)
	w := httptest.NewRecorder()
	h.HandlePLPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandlePLPage_NodeDown(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{err: errTest}, &mockPrice{err: errTest})

	req := httptest.NewRequest(http.MethodGet, "/pl", nil)
	w := httptest.NewRecorder()
	h.HandlePLPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even with node down, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "node unreachable") {
		t.Error("expected 'node unreachable' when node is down")
	}
	if !strings.Contains(body, "price unavailable") {
		t.Error("expected 'price unavailable' when price fails")
	}
}

// --- mockWalletScanner ---

type mockWalletScanner struct {
	balance int64
}

func (m *mockWalletScanner) ScanAddress(_ context.Context, _ string) (int64, error) {
	return m.balance, nil
}
func (m *mockWalletScanner) ScanXpub(_ context.Context, _ string, _ string) (int64, error) {
	return m.balance, nil
}
func (m *mockWalletScanner) ScanDescriptor(_ context.Context, _ string) (int64, error) {
	return m.balance, nil
}

// --- HandleImportPage tests ---

func TestHandleImportPage(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/import", nil)
	w := httptest.NewRecorder()
	h.HandleImportPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Import Transactions") {
		t.Error("expected import page content")
	}
}

// --- HandleWalletsPage tests ---

func TestHandleWalletsPage(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{price: 67000.0})
	h.SetWalletStore(&mockWalletStore{
		wallets: []db.WatchedWallet{
			{ID: 1, Label: "TestWallet", Type: "address", Value: "bc1qtest", BalanceSats: 50000},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/wallets", nil)
	w := httptest.NewRecorder()
	h.HandleWalletsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "TestWallet") {
		t.Error("expected wallet label in page")
	}
}

func TestHandleWalletsPage_NoWalletStore(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/wallets", nil)
	w := httptest.NewRecorder()
	h.HandleWalletsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- HandleExchangeDetail tests ---

func TestHandleExchangeDetail(t *testing.T) {
	store := fullMockStore()
	store.listExchangeTransactionsFn = func(_ context.Context, source string, limit, offset int) (*db.ExchangeTransactionPage, error) {
		return &db.ExchangeTransactionPage{
			Transactions: []db.ExchangeTransaction{
				{ID: 1, Source: source, TxType: "purchase", Date: time.Now(), AmountBTC: 0.001, AmountSats: 100000, AmountUSD: 67.00, CostBasisUSD: 67.00},
			},
			Total: 1,
			Page:  1,
			Limit: 50,
		}, nil
	}
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{price: 67000.0})

	req := httptest.NewRequest(http.MethodGet, "/exchange/strike", nil)
	w := httptest.NewRecorder()
	h.HandleExchangeDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Strike Transactions") {
		t.Error("expected 'Strike Transactions' heading")
	}
	if !strings.Contains(body, "purchase") {
		t.Error("expected transaction type in table")
	}
}

func TestHandleExchangeDetail_InvalidSource(t *testing.T) {
	h := newTestHandler(fullMockStore(), &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/exchange/kraken", nil)
	w := httptest.NewRecorder()
	h.HandleExchangeDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleExchangeDetail_EmptySource(t *testing.T) {
	h := newTestHandler(fullMockStore(), &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/exchange/", nil)
	w := httptest.NewRecorder()
	h.HandleExchangeDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleExchangeDetail_Pagination(t *testing.T) {
	store := fullMockStore()
	store.listExchangeTransactionsFn = func(_ context.Context, source string, limit, offset int) (*db.ExchangeTransactionPage, error) {
		// Return a non-empty list so the table (and pagination) renders
		txns := make([]db.ExchangeTransaction, 1)
		txns[0] = db.ExchangeTransaction{ID: 1, Source: source, TxType: "purchase", AmountSats: 100}
		return &db.ExchangeTransactionPage{
			Transactions: txns,
			Total:        100,
			Page:         2,
			Limit:        50,
		}, nil
	}
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/exchange/strike?page=2", nil)
	w := httptest.NewRecorder()
	h.HandleExchangeDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Page 2 of 2") {
		t.Error("expected pagination info")
	}
}

func TestHandleExchangeDetail_AllSources(t *testing.T) {
	for _, source := range []string{"strike", "river", "coinbase", "swan"} {
		store := fullMockStore()
		h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

		req := httptest.NewRequest(http.MethodGet, "/exchange/"+source, nil)
		w := httptest.NewRecorder()
		h.HandleExchangeDetail(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("source=%s: expected 200, got %d", source, w.Code)
		}
	}
}

// --- HandleWalletDetail tests ---

func TestHandleWalletDetail(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{price: 67000.0})
	h.SetWalletStore(&mockWalletStore{
		wallet: &db.WatchedWallet{
			ID: 1, Label: "Coldcard", Type: "address", Value: "bc1qtest123", BalanceSats: 500000,
			CreatedAt: time.Now(),
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	w := httptest.NewRecorder()
	h.HandleWalletDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Coldcard") {
		t.Error("expected wallet label")
	}
	if !strings.Contains(body, "bc1qtest123") {
		t.Error("expected wallet value")
	}
}

func TestHandleWalletDetail_NotFound(t *testing.T) {
	store := fullMockStore()
	h := newTestHandler(store, &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})
	h.SetWalletStore(&mockWalletStore{})

	req := httptest.NewRequest(http.MethodGet, "/wallets/999", nil)
	w := httptest.NewRecorder()
	h.HandleWalletDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleWalletDetail_InvalidID(t *testing.T) {
	h := newTestHandler(fullMockStore(), &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})
	h.SetWalletStore(&mockWalletStore{})

	req := httptest.NewRequest(http.MethodGet, "/wallets/abc", nil)
	w := httptest.NewRecorder()
	h.HandleWalletDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleWalletDetail_NoWalletStore(t *testing.T) {
	h := newTestHandler(fullMockStore(), &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})

	req := httptest.NewRequest(http.MethodGet, "/wallets/1", nil)
	w := httptest.NewRecorder()
	h.HandleWalletDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without wallet store, got %d", w.Code)
	}
}

// --- HandleAddWallet tests ---

func TestHandleAddWallet_Success(t *testing.T) {
	h := newTestHandler(fullMockStore(), &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})
	ws := &mockWalletStore{}
	h.SetWalletStore(ws)
	h.SetWalletScanner(&mockWalletScanner{balance: 100000})

	body := strings.NewReader("label=TestWallet&value=bc1qtest123")
	req := httptest.NewRequest(http.MethodPost, "/api/wallets", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleAddWallet(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
}

func TestHandleAddWallet_MissingFields(t *testing.T) {
	h := newTestHandler(fullMockStore(), &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})
	h.SetWalletStore(&mockWalletStore{})

	body := strings.NewReader("label=&value=")
	req := httptest.NewRequest(http.MethodPost, "/api/wallets", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleAddWallet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- HandleRemoveWallet tests ---

func TestHandleRemoveWallet(t *testing.T) {
	h := newTestHandler(fullMockStore(), &mockNodeInfo{info: &lnd.NodeInfo{}}, &mockPrice{})
	h.SetWalletStore(&mockWalletStore{})

	body := strings.NewReader("id=1")
	req := httptest.NewRequest(http.MethodPost, "/api/wallets/delete", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleRemoveWallet(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
}

// --- isDescriptor tests ---

func TestIsDescriptor(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"wsh(sortedmulti(2,xpub...))", true},
		{"wpkh(xpub...)", true},
		{"sh(wpkh(xpub...))", true},
		{"tr(xpub...)", true},
		{"bc1qtest", false},
		{"zpub6rFR7y4Q2AijBEqTUqmBQ5kWnViJaSBhg", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isDescriptor(tt.input)
		if got != tt.want {
			t.Errorf("isDescriptor(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- River/Coinbase import handler tests ---

func TestHandleRiverImport_Success(t *testing.T) {
	importStore := &mockImportStore{
		importRiverFn: func(_ context.Context, rows []exchange.RiverRow) (*db.ImportSummary, error) {
			return &db.ImportSummary{Total: len(rows), NewPurchases: 1}, nil
		},
	}
	h := NewHandler(fullMockStore(), nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	csv := "Date,Sent Amount,Sent Currency,Received Amount,Received Currency,Fee Amount,Fee Currency,Tag\n2024-09-26 19:59:41,3.96,USD,0.00006093,BTC,0.04,USD,Buy\n"
	req, w := createMultipartCSV(t, csv)
	req.URL.Path = "/api/import/river"
	h.HandleRiverImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRiverImport_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/import/river", nil)
	w := httptest.NewRecorder()
	h.HandleRiverImport(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleCoinbaseImport_Success(t *testing.T) {
	importStore := &mockImportStore{
		importCoinbaseFn: func(_ context.Context, rows []exchange.CoinbaseRow) (*db.ImportSummary, error) {
			return &db.ImportSummary{Total: len(rows), NewPurchases: 1}, nil
		},
	}
	h := NewHandler(fullMockStore(), nil, &mockPrice{}, importStore, log.New(os.Stderr, "[test] ", 0))

	csv := "\nTransactions\nUser,Test User,test-uuid\nID,Timestamp,Transaction Type,Asset,Quantity Transacted,Price Currency,Price at Transaction,Subtotal,Total (inclusive of fees and/or spread),Fees and/or Spread,Notes\nabc123,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought 0.001 BTC\n"
	req, w := createMultipartCSV(t, csv)
	req.URL.Path = "/api/import/coinbase"
	h.HandleCoinbaseImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCoinbaseImport_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/import/coinbase", nil)
	w := httptest.NewRecorder()
	h.HandleCoinbaseImport(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- HandleSwanImport tests ---

func TestHandleSwanImport_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/import/swan", nil)
	w := httptest.NewRecorder()
	h.HandleSwanImport(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleSwanImport_NoFiles(t *testing.T) {
	h := NewHandler(fullMockStore(), nil, &mockPrice{}, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))

	// Empty multipart form with no files
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/import/swan", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleSwanImport(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleSwanImport_WithTradesFile(t *testing.T) {
	h := NewHandler(fullMockStore(), nil, &mockPrice{}, &mockImportStore{}, log.New(os.Stderr, "[test] ", 0))

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("trades", "trades.csv")
	part.Write([]byte("Date,Received Quantity,Received Currency,Sent Quantity,Sent Currency,Fee Amount,Fee Currency,Tag\n11/16/2024 17:36:30,0.00010975,BTC,10.00,USD,0.00,USD,\"\"\n"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/import/swan", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	h.HandleSwanImport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// --- HandleRefreshWallet tests ---

func TestHandleRefreshWallet_Success(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	h.SetWalletStore(&mockWalletStore{
		wallet: &db.WatchedWallet{ID: 1, Label: "Test", Type: "address", Value: "bc1q123"},
	})
	h.SetWalletScanner(&mockWalletScanner{balance: 50000})

	body := strings.NewReader("id=1")
	req := httptest.NewRequest(http.MethodPost, "/api/wallets/refresh", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleRefreshWallet(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
}

func TestHandleRefreshWallet_NoScanner(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	h.SetWalletStore(&mockWalletStore{})

	body := strings.NewReader("id=1")
	req := httptest.NewRequest(http.MethodPost, "/api/wallets/refresh", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleRefreshWallet(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleRefreshWallet_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/wallets/refresh", nil)
	w := httptest.NewRecorder()
	h.HandleRefreshWallet(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- HandleRefreshAll tests ---

func TestHandleRefreshAll_Success(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	h.SetWalletStore(&mockWalletStore{
		wallets: []db.WatchedWallet{{ID: 1, Label: "Test", Type: "address", Value: "bc1q123"}},
	})
	h.SetWalletScanner(&mockWalletScanner{balance: 50000})

	req := httptest.NewRequest(http.MethodPost, "/api/wallets/refresh-all", nil)
	w := httptest.NewRecorder()
	h.HandleRefreshAll(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", w.Code)
	}
}

func TestHandleRefreshAll_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/wallets/refresh-all", nil)
	w := httptest.NewRecorder()
	h.HandleRefreshAll(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

// --- HandleAddWallet edge cases ---

func TestHandleAddWallet_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/wallets", nil)
	w := httptest.NewRecorder()
	h.HandleAddWallet(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleAddWallet_NoWalletStore(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})

	body := strings.NewReader("label=Test&value=bc1q123")
	req := httptest.NewRequest(http.MethodPost, "/api/wallets", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleAddWallet(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- HandleRemoveWallet edge cases ---

func TestHandleRemoveWallet_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/api/wallets/delete", nil)
	w := httptest.NewRecorder()
	h.HandleRemoveWallet(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestHandleRemoveWallet_InvalidID(t *testing.T) {
	h := newTestHandler(fullMockStore(), nil, &mockPrice{})
	h.SetWalletStore(&mockWalletStore{})

	body := strings.NewReader("id=abc")
	req := httptest.NewRequest(http.MethodPost, "/api/wallets/delete", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleRemoveWallet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
