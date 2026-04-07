package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
)

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

	// Check key elements are present
	checks := []string{
		"Satsbook",
		"test-node",
		"Fee Income",
		"Payments Routed",
		"Active Channels",
		"Wallet Balance",
		"Daily Fee Income",
		"Channels",
		"Forwarding Events",
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
