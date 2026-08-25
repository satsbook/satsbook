package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/tax"
)

// --- mockTaxStore for tax handler tests (#19/#124) ---

type mockTaxStore struct {
	lots      []db.BTCLot
	disposals []db.DisposalRow
	lotsErr   error
	dispErr   error
}

func (m *mockTaxStore) ListBTCLots(_ context.Context) ([]db.BTCLot, error) {
	return m.lots, m.lotsErr
}
func (m *mockTaxStore) ListDisposals(_ context.Context) ([]db.DisposalRow, error) {
	return m.disposals, m.dispErr
}

// newHandlerWithTax creates a handler with a tax store set.
func newHandlerWithTax(ts *mockTaxStore) *Handler {
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{price: 60000})
	h.SetTaxStore(ts)
	return h
}

// --- HandleTaxPage tests (#19) ---

func TestHandleTaxPage_GET_Renders(t *testing.T) {
	h := newHandlerWithTax(&mockTaxStore{})
	req := httptest.NewRequest(http.MethodGet, "/tax", nil)
	w := httptest.NewRecorder()
	h.HandleTaxPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("expected HTML content type")
	}
}

func TestHandleTaxPage_NoTaxStore_StillRenders(t *testing.T) {
	// Without a taxStore, the page still renders — just no summary data.
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	req := httptest.NewRequest(http.MethodGet, "/tax", nil)
	w := httptest.NewRecorder()
	h.HandleTaxPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even without tax store, got %d", w.Code)
	}
}

func TestHandleTaxPage_MethodParam(t *testing.T) {
	h := newHandlerWithTax(&mockTaxStore{})
	req := httptest.NewRequest(http.MethodGet, "/tax?method=lifo", nil)
	w := httptest.NewRecorder()
	h.HandleTaxPage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleTaxPage_YearFilter(t *testing.T) {
	acq := time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC)
	disp := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)

	ts := &mockTaxStore{
		lots: []db.BTCLot{{
			ID: 1, AcquiredAt: acq, AmountSat: 100000,
			PriceUSD: 30000, Source: "strike", ExternalID: "tx1",
		}},
		disposals: []db.DisposalRow{{
			DisposedAt:  disp,
			AmountSat:   50000,
			ProceedsUSD: 1750,
			TxType:      "sell",
			Source:      "strike",
			ExternalID:  "tx2",
		}},
	}
	h := newHandlerWithTax(ts)
	req := httptest.NewRequest(http.MethodGet, "/tax?year=2024", nil)
	w := httptest.NewRecorder()
	h.HandleTaxPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleTaxPage_HarvestRendered(t *testing.T) {
	// Lot with a high cost basis, current price is low → unrealized loss
	acq := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	ts := &mockTaxStore{
		lots: []db.BTCLot{{
			ID: 1, AcquiredAt: acq, AmountSat: 100_000_000,
			PriceUSD: 60000, Source: "strike", ExternalID: "tx1",
		}},
		disposals: []db.DisposalRow{},
	}
	// mockPrice at 40000 → cost basis $60k, market $40k → unrealized loss
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{price: 40000})
	h.SetTaxStore(ts)

	req := httptest.NewRequest(http.MethodGet, "/tax", nil)
	w := httptest.NewRecorder()
	h.HandleTaxPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- HandleTaxSummary tests (#19) ---

func TestHandleTaxSummary_NoStore_Returns503(t *testing.T) {
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	// no taxStore set

	req := httptest.NewRequest(http.MethodGet, "/api/tax/summary", nil)
	w := httptest.NewRecorder()
	h.HandleTaxSummary(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 without tax store, got %d", w.Code)
	}
}

func TestHandleTaxSummary_ReturnsJSON(t *testing.T) {
	acq := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	disp := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	ts := &mockTaxStore{
		lots: []db.BTCLot{{
			ID: 1, AcquiredAt: acq, AmountSat: 100000,
			PriceUSD: 20000, Source: "strike", ExternalID: "tx1",
		}},
		disposals: []db.DisposalRow{{
			DisposedAt:  disp,
			AmountSat:   100000,
			ProceedsUSD: 25,
			TxType:      "sell",
			Source:      "strike",
			ExternalID:  "tx2",
		}},
	}
	h := newHandlerWithTax(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/tax/summary", nil)
	w := httptest.NewRecorder()
	h.HandleTaxSummary(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content type, got %s", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Proceeds") && !strings.Contains(body, "proceeds") {
		t.Errorf("expected JSON with proceeds-related field, got: %s", body)
	}
}

func TestHandleTaxSummary_LIFOMethod(t *testing.T) {
	ts := &mockTaxStore{lots: []db.BTCLot{}, disposals: []db.DisposalRow{}}
	h := newHandlerWithTax(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/tax/summary?method=lifo", nil)
	w := httptest.NewRecorder()
	h.HandleTaxSummary(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- HandleTaxExport tests (#19) ---

func TestHandleTaxExport_NoStore_Returns503(t *testing.T) {
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	// no taxStore

	req := httptest.NewRequest(http.MethodGet, "/api/tax/export", nil)
	w := httptest.NewRecorder()
	h.HandleTaxExport(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestHandleTaxExport_ReturnsCSV(t *testing.T) {
	acq := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	disp := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	ts := &mockTaxStore{
		lots: []db.BTCLot{{
			ID: 1, AcquiredAt: acq, AmountSat: 200000,
			PriceUSD: 25000, Source: "strike", ExternalID: "x1",
		}},
		disposals: []db.DisposalRow{{
			DisposedAt:  disp,
			AmountSat:   200000,
			ProceedsUSD: 70,
			TxType:      "sell",
			Source:      "strike",
			ExternalID:  "x2",
		}},
	}
	h := newHandlerWithTax(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/tax/export", nil)
	w := httptest.NewRecorder()
	h.HandleTaxExport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "text/csv" {
		t.Errorf("expected text/csv, got %s", ct)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Error("expected attachment content-disposition")
	}
}

func TestHandleTaxExport_JSONFormat(t *testing.T) {
	ts := &mockTaxStore{lots: []db.BTCLot{}, disposals: []db.DisposalRow{}}
	h := newHandlerWithTax(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/tax/export?format=json", nil)
	w := httptest.NewRecorder()
	h.HandleTaxExport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON, got %s", ct)
	}
}

func TestHandleTaxExport_YearFilter(t *testing.T) {
	acq := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	disp := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	ts := &mockTaxStore{
		lots: []db.BTCLot{{
			ID: 1, AcquiredAt: acq, AmountSat: 100000,
			PriceUSD: 25000, Source: "strike", ExternalID: "x1",
		}},
		disposals: []db.DisposalRow{{
			DisposedAt: disp, AmountSat: 100000,
			ProceedsUSD: 35, TxType: "sell",
			Source: "strike", ExternalID: "x2",
		}},
	}
	h := newHandlerWithTax(ts)

	req := httptest.NewRequest(http.MethodGet, "/api/tax/export?year=2024", nil)
	w := httptest.NewRecorder()
	h.HandleTaxExport(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleTaxExport_MethodNotAllowed(t *testing.T) {
	ts := &mockTaxStore{lots: []db.BTCLot{}, disposals: []db.DisposalRow{}}
	h := newHandlerWithTax(ts)

	req := httptest.NewRequest(http.MethodDelete, "/api/tax/export", nil)
	w := httptest.NewRecorder()
	h.HandleTaxExport(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// --- Tax-loss harvesting logic tests (#124) ---

func TestHarvest_IdentifiesUnrealizedLoss(t *testing.T) {
	acq := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	lots := []tax.Lot{{
		ID:         1,
		AcquiredAt: acq,
		AmountSat:  100_000_000, // 1 BTC
		PriceUSD:   60000,
		Source:     "strike",
		ExternalID: "lot1",
	}}
	disposals := []tax.Disposal{}

	// Price dropped from $60k to $40k — unrealized loss of $20k
	result, err := tax.Harvest(lots, disposals, tax.FIFO, 40000, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 harvest candidate, got %d", len(result.Candidates))
	}
	if result.TotalUnrealizedLoss >= 0 {
		t.Errorf("expected negative unrealized loss, got %f", result.TotalUnrealizedLoss)
	}
	if result.Candidates[0].UnrealizedLoss >= 0 {
		t.Errorf("expected negative loss on candidate, got %f", result.Candidates[0].UnrealizedLoss)
	}
}

func TestHarvest_NoLossWhenPriceHigher(t *testing.T) {
	acq := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	lots := []tax.Lot{{
		ID:         1,
		AcquiredAt: acq,
		AmountSat:  100_000_000, // 1 BTC
		PriceUSD:   30000,
		Source:     "strike",
		ExternalID: "lot1",
	}}
	// Price is now higher than cost basis — no loss
	result, err := tax.Harvest(lots, nil, tax.FIFO, 60000, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("expected 0 candidates when price is above cost basis, got %d", len(result.Candidates))
	}
}

func TestHarvest_WashSaleRisk(t *testing.T) {
	now := time.Now()
	// Recent buy (within 30 days) + a lot at a loss
	acq1 := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	acq2 := now.AddDate(0, 0, -5) // recent buy

	lots := []tax.Lot{
		{ID: 1, AcquiredAt: acq1, AmountSat: 100_000_000, PriceUSD: 60000, Source: "strike", ExternalID: "lot1"},
		{ID: 2, AcquiredAt: acq2, AmountSat: 10_000_000, PriceUSD: 45000, Source: "strike", ExternalID: "lot2"},
	}
	result, err := tax.Harvest(lots, nil, tax.FIFO, 40000, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.WashSaleWarning {
		t.Error("expected wash sale warning when recent buy exists")
	}
}

func TestHarvest_LongTermFlag(t *testing.T) {
	now := time.Now()
	// Acquired > 1 year ago
	acq := now.AddDate(-2, 0, 0)
	lots := []tax.Lot{{
		ID:         1,
		AcquiredAt: acq,
		AmountSat:  100_000_000,
		PriceUSD:   60000,
		Source:     "strike",
		ExternalID: "lot1",
	}}
	result, err := tax.Harvest(lots, nil, tax.FIFO, 40000, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Candidates) == 0 {
		t.Fatal("expected at least one candidate")
	}
	if !result.Candidates[0].IsLongTerm {
		t.Error("expected IsLongTerm=true for lot held > 1 year")
	}
}
