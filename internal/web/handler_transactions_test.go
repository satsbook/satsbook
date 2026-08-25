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
)

// --- HandleTransactionsPage tests (#71) ---

func newHandlerWithTxStore(result *db.UnifiedTransactionPage, err error) *Handler {
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{price: 60000})
	txStore := &mockTransactionStore{result: result, err: err}
	h.SetTransactionStore(txStore)
	return h
}

func TestHandleTransactionsPage_GET_Renders(t *testing.T) {
	now := time.Now()
	h := newHandlerWithTxStore(&db.UnifiedTransactionPage{
		Transactions: []db.UnifiedTransaction{
			{Source: "strike", SourceID: "tx-1", Time: now, TxType: "buy", AmountSat: 500000},
		},
		Total: 1,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("expected HTML content type")
	}
}

func TestHandleTransactionsPage_NoStore_Returns500(t *testing.T) {
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	// txStore not set

	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionsPage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleTransactionsPage_StoreError_Returns500(t *testing.T) {
	h := newHandlerWithTxStore(nil, context.DeadlineExceeded)

	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionsPage(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleTransactionsPage_FilterParams_Accepted(t *testing.T) {
	var capturedFilter db.TransactionFilter
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	txMock := &mockTransactionStore{}
	txMock.result = &db.UnifiedTransactionPage{Total: 0}
	// Override to capture the filter
	h.SetTransactionStore(&captureFilterStore{inner: txMock, captured: &capturedFilter})

	req := httptest.NewRequest(http.MethodGet, "/transactions?from=2024-01-01&to=2024-12-31&type=buy&source=strike&q=bitcoin&flow=inflow", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify filters were passed through
	if capturedFilter.DateFrom != "2024-01-01" {
		t.Errorf("DateFrom = %q, want 2024-01-01", capturedFilter.DateFrom)
	}
	if capturedFilter.DateTo != "2024-12-31" {
		t.Errorf("DateTo = %q, want 2024-12-31", capturedFilter.DateTo)
	}
	if capturedFilter.TxType != "buy" {
		t.Errorf("TxType = %q, want buy", capturedFilter.TxType)
	}
	if capturedFilter.Source != "strike" {
		t.Errorf("Source = %q, want strike", capturedFilter.Source)
	}
	if capturedFilter.Search != "bitcoin" {
		t.Errorf("Search = %q, want bitcoin", capturedFilter.Search)
	}
	if capturedFilter.Flow != "inflow" {
		t.Errorf("Flow = %q, want inflow", capturedFilter.Flow)
	}
}

func TestHandleTransactionsPage_Pagination(t *testing.T) {
	var capturedFilter db.TransactionFilter
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	txMock := &mockTransactionStore{result: &db.UnifiedTransactionPage{Total: 200}}
	h.SetTransactionStore(&captureFilterStore{inner: txMock, captured: &capturedFilter})

	req := httptest.NewRequest(http.MethodGet, "/transactions?page=3", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Page 3, limit 50 → offset 100
	if capturedFilter.Offset != 100 {
		t.Errorf("expected offset 100 (page 3), got %d", capturedFilter.Offset)
	}
}

func TestHandleTransactionsPage_SortParams(t *testing.T) {
	var capturedFilter db.TransactionFilter
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	txMock := &mockTransactionStore{result: &db.UnifiedTransactionPage{Total: 0}}
	h.SetTransactionStore(&captureFilterStore{inner: txMock, captured: &capturedFilter})

	req := httptest.NewRequest(http.MethodGet, "/transactions?sort=amount_sat&dir=asc", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionsPage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if capturedFilter.SortCol != "amount_sat" {
		t.Errorf("SortCol = %q, want amount_sat", capturedFilter.SortCol)
	}
	if capturedFilter.SortDir != "asc" {
		t.Errorf("SortDir = %q, want asc", capturedFilter.SortDir)
	}
}

func TestHandleTransactionsPage_DefaultSortDir(t *testing.T) {
	var capturedFilter db.TransactionFilter
	store := &mockStore{
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	txMock := &mockTransactionStore{result: &db.UnifiedTransactionPage{Total: 0}}
	h.SetTransactionStore(&captureFilterStore{inner: txMock, captured: &capturedFilter})

	req := httptest.NewRequest(http.MethodGet, "/transactions", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionsPage(w, req)

	if capturedFilter.SortDir != "desc" {
		t.Errorf("expected default sortDir=desc, got %q", capturedFilter.SortDir)
	}
}

// captureFilterStore wraps mockTransactionStore and captures the filter passed to ListUnifiedTransactions.
type captureFilterStore struct {
	inner    *mockTransactionStore
	captured *db.TransactionFilter
}

func (c *captureFilterStore) ListUnifiedTransactions(ctx context.Context, f db.TransactionFilter) (*db.UnifiedTransactionPage, error) {
	*c.captured = f
	return c.inner.ListUnifiedTransactions(ctx, f)
}
func (c *captureFilterStore) SetTransactionNote(ctx context.Context, sourceID, note string) error {
	return c.inner.SetTransactionNote(ctx, sourceID, note)
}
func (c *captureFilterStore) DistinctTransactionValues(ctx context.Context) ([]string, []string, error) {
	return c.inner.DistinctTransactionValues(ctx)
}

// --- HandleTransactionNoteEdit tests (Issue #71: Transaction history detail views) ---
// The spec says: users can inline-edit notes on any transaction row.

func TestHandleTransactionNoteEdit_RendersEditForm(t *testing.T) {
	// GET with source_id and note params — renders edit inline form.
	h := newHandlerWithTxStore(&db.UnifiedTransactionPage{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/note/edit?source_id=tx-abc&note=my+note", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionNoteEdit(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("expected HTML content type")
	}
	// Form should contain the source_id and current note.
	body := w.Body.String()
	if !strings.Contains(body, "tx-abc") {
		t.Errorf("expected source_id in edit form, got: %s", body)
	}
}

func TestHandleTransactionNoteEdit_EmptyNote_RendersEmptyInput(t *testing.T) {
	h := newHandlerWithTxStore(&db.UnifiedTransactionPage{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/transactions/note/edit?source_id=tx-xyz&note=", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionNoteEdit(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "tx-xyz") {
		t.Errorf("expected source_id in form, got: %s", body)
	}
}

// --- HandleTransactionNoteSave tests (Issue #71: Transaction history detail views) ---
// The spec says: POST saves a note and returns the display partial.

func newHandlerWithNoteStore(setNoteErr error) *Handler {
	store := &mockStore{
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	txStore := &noteTransactionStore{setNoteErr: setNoteErr}
	h.SetTransactionStore(txStore)
	return h
}

// noteTransactionStore implements TransactionStore with configurable SetTransactionNote error.
type noteTransactionStore struct {
	setNoteErr error
	lastID     string
	lastNote   string
}

func (n *noteTransactionStore) ListUnifiedTransactions(_ context.Context, _ db.TransactionFilter) (*db.UnifiedTransactionPage, error) {
	return &db.UnifiedTransactionPage{}, nil
}

func (n *noteTransactionStore) SetTransactionNote(_ context.Context, sourceID, note string) error {
	n.lastID = sourceID
	n.lastNote = note
	return n.setNoteErr
}

func (n *noteTransactionStore) DistinctTransactionValues(_ context.Context) ([]string, []string, error) {
	return nil, nil, nil
}

func TestHandleTransactionNoteSave_SavesAndRendersDisplay(t *testing.T) {
	h := newHandlerWithNoteStore(nil)

	body := strings.NewReader("source_id=tx-123&note=My+cool+note")
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/note", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransactionNoteSave(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Error("expected HTML content type")
	}
	// Should render the display partial containing the note text.
	if !strings.Contains(w.Body.String(), "My cool note") {
		t.Errorf("expected note in display partial, got: %s", w.Body.String())
	}
}

func TestHandleTransactionNoteSave_WrongMethod(t *testing.T) {
	h := newHandlerWithNoteStore(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/transactions/note", nil)
	w := httptest.NewRecorder()
	h.HandleTransactionNoteSave(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleTransactionNoteSave_NoTxStore_Returns500(t *testing.T) {
	store := &mockStore{
		latestWalletFn:  func(_ context.Context) (*db.WalletBalanceSnapshot, error) { return nil, nil },
		feeSummaryFn:    func(_ context.Context, _ time.Time) (int64, int64, error) { return 0, 0, nil },
		activeChannelFn: func(_ context.Context) (int, error) { return 0, nil },
	}
	h := newTestHandler(store, nil, &mockPrice{})
	// txStore is nil

	body := strings.NewReader("source_id=tx-1&note=hello")
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/note", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransactionNoteSave(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 without tx store, got %d", w.Code)
	}
}

func TestHandleTransactionNoteSave_MissingSourceID_Returns400(t *testing.T) {
	h := newHandlerWithNoteStore(nil)

	body := strings.NewReader("note=hello") // no source_id
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/note", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransactionNoteSave(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing source_id, got %d", w.Code)
	}
}

func TestHandleTransactionNoteSave_StoreError_Returns500(t *testing.T) {
	h := newHandlerWithNoteStore(errors.New("db write failed"))

	body := strings.NewReader("source_id=tx-1&note=hello")
	req := httptest.NewRequest(http.MethodPost, "/api/transactions/note", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleTransactionNoteSave(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for store error, got %d", w.Code)
	}
}
