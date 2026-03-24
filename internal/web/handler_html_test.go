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
