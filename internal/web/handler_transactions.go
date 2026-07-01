package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/satsbook/satsbook/internal/db"
)

// TransactionsPageData holds data for the /transactions page.
type TransactionsPageData struct {
	Transactions []db.UnifiedTransaction
	Total        int
	Page         int
	Limit        int
	TotalPages   int
	BTCPriceUSD  float64

	// Current filter/sort state (for preserving in links and form)
	DateFrom string
	DateTo   string
	TxType   string
	Source   string
	Search   string
	Flow     string // "inflow", "outflow", or ""
	SortCol  string
	SortDir  string

	// Available filter options
	Sources []string
	TxTypes []string
}

// HandleTransactionsPage serves GET /transactions.
func (h *Handler) HandleTransactionsPage(w http.ResponseWriter, r *http.Request) {
	if h.txStore == nil {
		http.Error(w, "transaction store not available", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	page := parseIntParam(r, "page", 1)
	if page < 1 {
		page = 1
	}
	limit := 50
	offset := (page - 1) * limit

	q := r.URL.Query()
	sortCol := q.Get("sort")
	sortDir := q.Get("dir")
	if sortDir == "" {
		sortDir = "desc"
	}

	flow := q.Get("flow")
	f := db.TransactionFilter{
		DateFrom: q.Get("from"),
		DateTo:   q.Get("to"),
		TxType:   q.Get("type"),
		Source:   q.Get("source"),
		Search:   q.Get("q"),
		Flow:     flow,
		SortCol:  sortCol,
		SortDir:  sortDir,
		Limit:    limit,
		Offset:   offset,
	}

	result, err := h.txStore.ListUnifiedTransactions(ctx, f)
	if err != nil {
		h.logger.Printf("transactions page: %v", err)
		http.Error(w, "failed to load transactions", http.StatusInternalServerError)
		return
	}

	// Pull actual sources and types from the data
	sources, txTypes, _ := h.txStore.DistinctTransactionValues(ctx)

	data := TransactionsPageData{
		Transactions: result.Transactions,
		Total:        result.Total,
		Page:         page,
		Limit:        limit,
		DateFrom:     f.DateFrom,
		DateTo:       f.DateTo,
		TxType:       f.TxType,
		Source:       f.Source,
		Search:       f.Search,
		Flow:         flow,
		SortCol:      sortCol,
		SortDir:      sortDir,
		Sources:      sources,
		TxTypes:      txTypes,
	}
	if result.Total > 0 {
		data.TotalPages = (result.Total + limit - 1) / limit
	}

	if price, err := h.price.GetBTCPrice(ctx); err == nil {
		data.BTCPriceUSD = price
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "transactions_layout", data); err != nil {
		h.logger.Printf("failed to render transactions page: %v", err)
	}
}

// txNoteData is passed to the transaction_note_display and _edit partials.
type txNoteData struct {
	SourceID string
	Note     string
}

// HandleTransactionNoteEdit serves GET /api/transactions/note/edit — returns edit form partial.
func (h *Handler) HandleTransactionNoteEdit(w http.ResponseWriter, r *http.Request) {
	sourceID := r.URL.Query().Get("source_id")
	note := r.URL.Query().Get("note")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "transaction_note_edit", txNoteData{SourceID: sourceID, Note: note}); err != nil {
		h.logger.Printf("render note edit: %v", err)
	}
}

// HandleTransactionNoteSave serves POST /api/transactions/note — saves and returns display partial.
func (h *Handler) HandleTransactionNoteSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.txStore == nil {
		h.writeError(w, http.StatusInternalServerError, "transaction store not available")
		return
	}

	if err := r.ParseForm(); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid form")
		return
	}

	sourceID := r.FormValue("source_id")
	note := r.FormValue("note")
	if sourceID == "" {
		h.writeError(w, http.StatusBadRequest, "source_id required")
		return
	}

	if err := h.txStore.SetTransactionNote(r.Context(), sourceID, note); err != nil {
		h.logger.Printf("set transaction note: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to save note")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "transaction_note_display", txNoteData{SourceID: sourceID, Note: strings.TrimSpace(note)}); err != nil {
		h.logger.Printf("render note display: %v", err)
	}
}

// HandleTransferToggle serves POST /api/transactions/transfer — toggles is_transfer flag.
func (h *Handler) HandleTransferToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sourceID := r.FormValue("source_id")
	if sourceID == "" {
		http.Error(w, "source_id required", http.StatusBadRequest)
		return
	}

	// Toggle: read current, flip it
	current, _ := h.store.GetTransferFlag(r.Context(), sourceID)
	newVal := !current
	if err := h.store.SetTransferFlag(r.Context(), sourceID, newVal); err != nil {
		h.logger.Printf("set transfer flag: %v", err)
		http.Error(w, "failed to update", http.StatusInternalServerError)
		return
	}

	// Return updated transfer badge partial
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "transfer_badge", transferBadgeData{SourceID: sourceID, IsTransfer: newVal}); err != nil {
		h.logger.Printf("render transfer badge: %v", err)
	}
}

// HandleTransferBulk serves POST /api/transactions/transfer/bulk — marks multiple transactions.
func (h *Handler) HandleTransferBulk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	sourceID := r.FormValue("source_id")
	candidateID := r.FormValue("candidate_id")
	if sourceID == "" {
		http.Error(w, "source_id required", http.StatusBadRequest)
		return
	}

	if err := h.store.SetTransferFlag(ctx, sourceID, true); err != nil {
		h.logger.Printf("bulk transfer: %v", err)
		http.Error(w, "failed to update", http.StatusInternalServerError)
		return
	}
	if candidateID != "" {
		if err := h.store.SetTransferFlag(ctx, candidateID, true); err != nil {
			h.logger.Printf("bulk transfer candidate: %v", err)
		}
	}

	// Return updated badge for the primary transaction
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "transfer_badge", transferBadgeData{SourceID: sourceID, IsTransfer: true}); err != nil {
		h.logger.Printf("render transfer badge: %v", err)
	}
	// OOB swap: also update the candidate's badge in-place (no refresh needed)
	if candidateID != "" {
		if err := h.renderer.Render(w, "transfer_badge_oob", transferBadgeData{SourceID: candidateID, IsTransfer: true}); err != nil {
			h.logger.Printf("render transfer badge oob: %v", err)
		}
	}
}

// HandleTransferCandidates serves GET /api/transactions/transfer/candidates — auto-suggest.
func (h *Handler) HandleTransferCandidates(w http.ResponseWriter, r *http.Request) {
	sourceID := r.URL.Query().Get("source_id")
	amountSat, _ := strconv.ParseInt(r.URL.Query().Get("amount_sat"), 10, 64)
	tsStr := r.URL.Query().Get("ts")
	ts, _ := time.Parse(time.RFC3339, tsStr)

	if sourceID == "" || amountSat == 0 || ts.IsZero() {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Still render the popover so user sees "no matches" feedback
		_ = h.renderer.Render(w, "transfer_candidates", transferCandidatesData{SourceID: sourceID})
		return
	}

	candidates, err := h.store.ListTransferCandidates(r.Context(), sourceID, amountSat, ts)
	if err != nil {
		h.logger.Printf("transfer candidates: %v", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "transfer_candidates", transferCandidatesData{
		SourceID:   sourceID,
		Candidates: candidates,
	}); err != nil {
		h.logger.Printf("render transfer candidates: %v", err)
	}
}

type transferBadgeData struct {
	SourceID   string
	IsTransfer bool
	AmountSat  int64
	Time       time.Time
}

type transferCandidatesData struct {
	SourceID   string
	Candidates []db.TransferCandidate
}
