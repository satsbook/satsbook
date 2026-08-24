package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/satsbook/satsbook/internal/apikey"
	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/license"
)

// APIKeyStore defines DB operations needed for API key auth.
type APIKeyStore interface {
	LookupAPIKey(ctx context.Context, keyHash string) (*db.APIKey, error)
	TouchAPIKeyLastUsed(ctx context.Context, id int64) error
	CreateAPIKey(ctx context.Context, name, keyHash, keyPrefix string) (int64, error)
	ListAPIKeys(ctx context.Context) ([]db.APIKey, error)
	RevokeAPIKey(ctx context.Context, id int64) error
}

// APIv1Store defines DB read operations needed by the v1 API endpoints.
type APIv1Store interface {
	ForwardingEvents(ctx context.Context, from, to time.Time, limit, offset int) (*db.ForwardingPage, error)
	ChannelStats(ctx context.Context) ([]db.ChannelStat, error)
	FeeSummary(ctx context.Context, since time.Time) (int64, int64, error)
	ActiveChannelCount(ctx context.Context) (int, error)
	LatestWalletBalance(ctx context.Context) (*db.WalletBalanceSnapshot, error)
	ListUnifiedTransactions(ctx context.Context, f db.TransactionFilter) (*db.UnifiedTransactionPage, error)
	ListBTCLots(ctx context.Context) ([]db.BTCLot, error)
}

// apiMeta is the pagination envelope included in list responses.
type apiMeta struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeAPIError writes a JSON error response.
func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type apiKeyCtxKey struct{}

// apiV1Auth is middleware that validates the Bearer token and enforces the rate limit.
func (h *Handler) apiV1Auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.apiKeyStore == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "api key store not available")
			return
		}

		authHeader := r.Header.Get("Authorization")
		raw, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || raw == "" {
			writeAPIError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}

		hash := apikey.Hash(raw)
		key, err := h.apiKeyStore.LookupAPIKey(r.Context(), hash)
		if err != nil {
			h.logger.Printf("api v1 auth: lookup: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if key == nil {
			writeAPIError(w, http.StatusUnauthorized, "invalid or revoked API key")
			return
		}

		if !h.rateLimiter.Allow(key.ID) {
			w.Header().Set("Retry-After", "60")
			writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded: 100 requests/minute")
			return
		}

		go func() {
			_ = h.apiKeyStore.TouchAPIKeyLastUsed(context.Background(), key.ID)
		}()

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiKeyCtxKey{}, key)))
	}
}

// HandleV1Forwarding serves GET /api/v1/forwarding
func (h *Handler) HandleV1Forwarding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	from := v1ParseTime(q.Get("from"), time.Now().AddDate(-1, 0, 0))
	to := v1ParseTime(q.Get("to"), time.Now())
	limit := clampInt(v1ParseInt(q.Get("limit"), 100), 1, 1000)
	offset := max0(v1ParseInt(q.Get("offset"), 0))

	page, err := h.apiv1Store.ForwardingEvents(r.Context(), from, to, limit, offset)
	if err != nil {
		h.logger.Printf("api v1 forwarding: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	type row struct {
		Timestamp  time.Time `json:"timestamp"`
		ChanIDIn   uint64    `json:"chan_id_in"`
		ChanIDOut  uint64    `json:"chan_id_out"`
		AmtInMsat  int64     `json:"amt_in_msat"`
		AmtOutMsat int64     `json:"amt_out_msat"`
		FeeMsat    int64     `json:"fee_msat"`
	}
	events := make([]row, len(page.Events))
	for i, e := range page.Events {
		events[i] = row{
			Timestamp:  e.Timestamp,
			ChanIDIn:   e.ChanIDIn,
			ChanIDOut:  e.ChanIDOut,
			AmtInMsat:  e.AmtInMsat,
			AmtOutMsat: e.AmtOutMsat,
			FeeMsat:    e.FeeMsat,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": events,
		"meta": apiMeta{Total: int(page.Total), Limit: limit, Offset: offset},
	})
}

// HandleV1Channels serves GET /api/v1/channels
func (h *Handler) HandleV1Channels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	stats, err := h.apiv1Store.ChannelStats(r.Context())
	if err != nil {
		h.logger.Printf("api v1 channels: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": stats,
		"meta": apiMeta{Total: len(stats), Limit: len(stats), Offset: 0},
	})
}

// HandleV1Summary serves GET /api/v1/summary
func (h *Handler) HandleV1Summary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	fees24h, _, err := h.apiv1Store.FeeSummary(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		h.logger.Printf("api v1 summary fees: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	fees7d, count7d, err := h.apiv1Store.FeeSummary(ctx, time.Now().AddDate(0, 0, -7))
	if err != nil {
		h.logger.Printf("api v1 summary fees 7d: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	chanCount, err := h.apiv1Store.ActiveChannelCount(ctx)
	if err != nil {
		h.logger.Printf("api v1 summary channels: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	wb, err := h.apiv1Store.LatestWalletBalance(ctx)
	if err != nil {
		h.logger.Printf("api v1 summary balance: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var totalSat int64
	if wb != nil {
		totalSat = wb.TotalSat
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"fees_24h_msat":       fees24h,
		"fees_7d_msat":        fees7d,
		"forwarding_count_7d": count7d,
		"active_channels":     chanCount,
		"wallet_balance_sat":  totalSat,
	})
}

// HandleV1Transactions serves GET /api/v1/transactions
func (h *Handler) HandleV1Transactions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	limit := clampInt(v1ParseInt(q.Get("limit"), 100), 1, 1000)
	offset := max0(v1ParseInt(q.Get("offset"), 0))

	f := db.TransactionFilter{
		Source:   q.Get("source"),
		DateFrom: q.Get("from"),
		DateTo:   q.Get("to"),
		Limit:    limit,
		Offset:   offset,
	}

	page, err := h.apiv1Store.ListUnifiedTransactions(r.Context(), f)
	if err != nil {
		h.logger.Printf("api v1 transactions: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": page.Transactions,
		"meta": apiMeta{Total: page.Total, Limit: limit, Offset: offset},
	})
}

// HandleV1Lots serves GET /api/v1/lots (requires Pro license in addition to Power)
func (h *Handler) HandleV1Lots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if h.licenseChecker != nil {
		if !license.TierAtLeast(h.licenseChecker.CurrentTier(), license.TierPro) {
			writeAPIError(w, http.StatusForbidden, "Pro license required for /api/v1/lots")
			return
		}
	}

	lots, err := h.apiv1Store.ListBTCLots(r.Context())
	if err != nil {
		h.logger.Printf("api v1 lots: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": lots,
		"meta": apiMeta{Total: len(lots), Limit: len(lots), Offset: 0},
	})
}

// HandleV1Score serves GET /api/v1/score
func (h *Handler) HandleV1Score(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	fees7d, count7d, err := h.apiv1Store.FeeSummary(ctx, time.Now().AddDate(0, 0, -7))
	if err != nil {
		h.logger.Printf("api v1 score: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}
	chanCount, err := h.apiv1Store.ActiveChannelCount(ctx)
	if err != nil {
		h.logger.Printf("api v1 score channels: %v", err)
		writeAPIError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Score: avg daily fee in sats × active channel multiplier, capped at 100.
	var score float64
	if chanCount > 0 && fees7d > 0 {
		avgDailySats := float64(fees7d/1000) / 7.0
		score = avgDailySats / float64(chanCount) * 10
		if score > 100 {
			score = 100
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"score":           score,
		"active_channels": chanCount,
		"forwarding_7d":   count7d,
		"fees_7d_msat":    fees7d,
	})
}

// --- helpers ---

func v1ParseTime(s string, def time.Time) time.Time {
	if s == "" {
		return def
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	return def
}

func v1ParseInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func clampInt(n, min, max int) int {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
