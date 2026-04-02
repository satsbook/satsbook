package web

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/exchange"
	"github.com/satsbook/satsbook/internal/lnd"
)

// DashboardStore defines the read operations needed by dashboard handlers.
type DashboardStore interface {
	FeeSummary(ctx context.Context, since time.Time) (int64, int64, error)
	ActiveChannelCount(ctx context.Context) (int, error)
	LatestWalletBalance(ctx context.Context) (*db.WalletBalanceSnapshot, error)
	ChannelStats(ctx context.Context) ([]db.ChannelStat, error)
	ForwardingEvents(ctx context.Context, from, to time.Time, limit, offset int) (*db.ForwardingPage, error)
	DailyFees(ctx context.Context, since time.Time) ([]db.DailyFeeStat, error)
	LastSyncedAt(ctx context.Context) (time.Time, error)
	ExchangeBalance(ctx context.Context, source string) (int64, error)
	ExchangeSummary(ctx context.Context, source string, since time.Time) (*db.ExchangeSummaryResult, error)
}

// NodeInfoProvider fetches node info from LND.
type NodeInfoProvider interface {
	GetInfo(ctx context.Context) (*lnd.NodeInfo, error)
}

// PriceProvider fetches the current BTC/USD price.
type PriceProvider interface {
	GetBTCPrice(ctx context.Context) (float64, error)
	FetchedAt() time.Time
}

// ImportStore defines operations for importing exchange data.
type ImportStore interface {
	ImportStrikeCSV(ctx context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error)
	ImportRiverCSV(ctx context.Context, rows []exchange.RiverRow) (*db.ImportSummary, error)
	ImportCoinbaseCSV(ctx context.Context, rows []exchange.CoinbaseRow) (*db.ImportSummary, error)
}

// Handler serves dashboard API and HTML endpoints.
type Handler struct {
	store       DashboardStore
	node        NodeInfoProvider
	price       PriceProvider
	importStore ImportStore
	logger      *log.Logger
	renderer    *Renderer
}

// NewHandler creates a new Handler.
func NewHandler(store DashboardStore, node NodeInfoProvider, price PriceProvider, importStore ImportStore, logger *log.Logger) *Handler {
	return &Handler{
		store:       store,
		node:        node,
		price:       price,
		importStore: importStore,
		logger:      logger,
		renderer:    NewRenderer(),
	}
}

// --- JSON response types ---

type feeWindow struct {
	Sats int64    `json:"sats"`
	USD  *float64 `json:"usd,omitempty"`
}

type summaryResponse struct {
	Fees struct {
		AllTime feeWindow `json:"all_time"`
		Last30d feeWindow `json:"last_30d"`
		Last7d  feeWindow `json:"last_7d"`
	} `json:"fees"`
	Routed struct {
		AllTime int64 `json:"all_time"`
		Last30d int64 `json:"last_30d"`
		Last7d  int64 `json:"last_7d"`
	} `json:"routed"`
	WalletBalanceSats int64    `json:"wallet_balance_sats"`
	ActiveChannels    int      `json:"active_channels"`
	BTCPriceUSD       *float64 `json:"btc_price_usd"`
}

type channelResponse struct {
	ChanID               uint64   `json:"chan_id,string"`
	RemotePubKey         string   `json:"remote_pubkey"`
	LocalBalance         int64    `json:"local_balance"`
	RemoteBalance        int64    `json:"remote_balance"`
	Active               bool     `json:"active"`
	FeesEarnedAllTimeSats int64   `json:"fees_earned_all_time_sats"`
	FeesEarned30dSats    int64    `json:"fees_earned_30d_sats"`
	FeesEarnedAllTimeUSD *float64 `json:"fees_earned_all_time_usd,omitempty"`
	FeesEarned30dUSD     *float64 `json:"fees_earned_30d_usd,omitempty"`
}

type forwardingEventResponse struct {
	Timestamp  time.Time `json:"timestamp"`
	ChanIDIn   uint64    `json:"chan_id_in,string"`
	ChanIDOut  uint64    `json:"chan_id_out,string"`
	AmtInMsat  int64     `json:"amt_in_msat"`
	AmtOutMsat int64     `json:"amt_out_msat"`
	FeeMsat    int64     `json:"fee_msat"`
}

type paginationMeta struct {
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
	Total int64 `json:"total"`
}

type forwardingResponse struct {
	Events     []forwardingEventResponse `json:"events"`
	Pagination paginationMeta            `json:"pagination"`
}

type nodeResponse struct {
	Alias             string `json:"alias"`
	PubKey            string `json:"pubkey"`
	Synced            bool   `json:"synced"`
	NumActiveChannels uint32 `json:"num_active_channels"`
	NumPeers          uint32 `json:"num_peers"`
	BlockHeight       uint32 `json:"block_height"`
	Version           string `json:"version"`
}

// --- Handlers ---

// HandleSummary serves GET /api/summary.
func (h *Handler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	now := time.Now()
	since30d := now.AddDate(0, 0, -30)
	since7d := now.AddDate(0, 0, -7)

	feesAll, routedAll, err := h.store.FeeSummary(ctx, time.Time{})
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to query fee summary")
		return
	}

	fees30d, routed30d, err := h.store.FeeSummary(ctx, since30d)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to query 30d fee summary")
		return
	}

	fees7d, routed7d, err := h.store.FeeSummary(ctx, since7d)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to query 7d fee summary")
		return
	}

	activeChannels, err := h.store.ActiveChannelCount(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to query active channels")
		return
	}

	balance, err := h.store.LatestWalletBalance(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to query wallet balance")
		return
	}

	resp := summaryResponse{
		ActiveChannels: activeChannels,
	}
	resp.Fees.AllTime = feeWindow{Sats: msatToSat(feesAll)}
	resp.Fees.Last30d = feeWindow{Sats: msatToSat(fees30d)}
	resp.Fees.Last7d = feeWindow{Sats: msatToSat(fees7d)}
	resp.Routed.AllTime = routedAll
	resp.Routed.Last30d = routed30d
	resp.Routed.Last7d = routed7d

	if balance != nil {
		resp.WalletBalanceSats = balance.TotalSat
	}

	// Add USD values if price is available
	btcPrice, err := h.price.GetBTCPrice(ctx)
	if err == nil && btcPrice > 0 {
		resp.BTCPriceUSD = &btcPrice
		usdAll := msatToUSD(feesAll, btcPrice)
		usd30d := msatToUSD(fees30d, btcPrice)
		usd7d := msatToUSD(fees7d, btcPrice)
		resp.Fees.AllTime.USD = &usdAll
		resp.Fees.Last30d.USD = &usd30d
		resp.Fees.Last7d.USD = &usd7d
	}

	h.writeJSON(w, http.StatusOK, resp)
}

// HandleChannels serves GET /api/channels.
func (h *Handler) HandleChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stats, err := h.store.ChannelStats(ctx)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to query channel stats")
		return
	}

	btcPrice, priceErr := h.price.GetBTCPrice(ctx)
	hasPrice := priceErr == nil && btcPrice > 0

	channels := make([]channelResponse, len(stats))
	for i, s := range stats {
		channels[i] = channelResponse{
			ChanID:                s.ChanID,
			RemotePubKey:          s.RemotePubKey,
			LocalBalance:          s.LocalBalance,
			RemoteBalance:         s.RemoteBalance,
			Active:                s.Active,
			FeesEarnedAllTimeSats: msatToSat(s.FeesEarnedAllTimeMsat),
			FeesEarned30dSats:     msatToSat(s.FeesEarned30dMsat),
		}
		if hasPrice {
			usdAll := msatToUSD(s.FeesEarnedAllTimeMsat, btcPrice)
			usd30d := msatToUSD(s.FeesEarned30dMsat, btcPrice)
			channels[i].FeesEarnedAllTimeUSD = &usdAll
			channels[i].FeesEarned30dUSD = &usd30d
		}
	}

	h.writeJSON(w, http.StatusOK, channels)
}

// HandleForwarding serves GET /api/forwarding.
func (h *Handler) HandleForwarding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, err := parseTimeParam(r, "from", time.Now().AddDate(0, 0, -30))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid 'from' parameter: use RFC3339 format")
		return
	}

	to, err := parseTimeParam(r, "to", time.Now())
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid 'to' parameter: use RFC3339 format")
		return
	}

	page := parseIntParam(r, "page", 1)
	if page < 1 {
		page = 1
	}

	limit := parseIntParam(r, "limit", 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	result, err := h.store.ForwardingEvents(ctx, from, to, limit, offset)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to query forwarding events")
		return
	}

	events := make([]forwardingEventResponse, len(result.Events))
	for i, e := range result.Events {
		events[i] = forwardingEventResponse{
			Timestamp:  e.Timestamp,
			ChanIDIn:   e.ChanIDIn,
			ChanIDOut:  e.ChanIDOut,
			AmtInMsat:  e.AmtInMsat,
			AmtOutMsat: e.AmtOutMsat,
			FeeMsat:    e.FeeMsat,
		}
	}

	h.writeJSON(w, http.StatusOK, forwardingResponse{
		Events: events,
		Pagination: paginationMeta{
			Page:  page,
			Limit: limit,
			Total: result.Total,
		},
	})
}

// HandleNode serves GET /api/node.
func (h *Handler) HandleNode(w http.ResponseWriter, r *http.Request) {
	info, err := h.node.GetInfo(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "failed to get node info")
		return
	}

	h.writeJSON(w, http.StatusOK, nodeResponse{
		Alias:             info.Alias,
		PubKey:            info.PubKey,
		Synced:            info.Synced,
		NumActiveChannels: info.NumActiveChannels,
		NumPeers:          info.NumPeers,
		BlockHeight:       info.BlockHeight,
		Version:           info.Version,
	})
}

// --- Helpers ---

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.Printf("failed to encode JSON response: %v", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}

func parseTimeParam(r *http.Request, key string, defaultVal time.Time) (time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal, nil
	}
	return time.Parse(time.RFC3339, v)
}

func parseIntParam(r *http.Request, key string, defaultVal int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

func msatToSat(msat int64) int64 {
	return msat / 1000
}

func msatToUSD(msat int64, btcPrice float64) float64 {
	return (float64(msat) / 100_000_000_000.0) * btcPrice
}

// HandleStrikeImport serves POST /api/import/strike.
func (h *Handler) HandleStrikeImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 10MB max upload
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.logger.Printf("strike import: failed to parse multipart form: %v", err)
		h.writeError(w, http.StatusBadRequest, "failed to parse upload: file may be too large (10MB max)")
		return
	}

	file, fh, err := r.FormFile("file")
	if err != nil {
		h.logger.Printf("strike import: missing file field: %v", err)
		h.writeError(w, http.StatusBadRequest, "missing 'file' field in upload")
		return
	}
	defer file.Close()
	h.logger.Printf("strike import: received file %q (%d bytes)", fh.Filename, fh.Size)

	result, err := exchange.ParseStrikeCSV(file)
	if err != nil {
		h.logger.Printf("strike import: CSV parse error: %v", err)
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(result.Rows) == 0 {
		h.writeError(w, http.StatusBadRequest, "CSV contains no valid data rows")
		return
	}

	summary, err := h.importStore.ImportStrikeCSV(r.Context(), result.Rows)
	if err != nil {
		h.logger.Printf("strike import failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to import transactions")
		return
	}

	resp := importResponse{
		Total:        summary.Total,
		NewPurchases: summary.NewPurchases,
		Duplicates:   summary.Duplicates,
		ParseErrors:  result.Errors,
	}

	// HTMX request — render partial
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.renderer.Render(w, "import_result", resp); err != nil {
			h.logger.Printf("failed to render import result: %v", err)
		}
		return
	}

	h.writeJSON(w, http.StatusOK, resp)
}

type importResponse struct {
	Total        int      `json:"total"`
	NewPurchases int      `json:"new_purchases"`
	Duplicates   int      `json:"duplicates"`
	ParseErrors  []string `json:"parse_errors,omitempty"`
}

// HandleRiverImport serves POST /api/import/river.
func (h *Handler) HandleRiverImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// 10MB max upload
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.logger.Printf("river import: failed to parse multipart form: %v", err)
		h.writeError(w, http.StatusBadRequest, "failed to parse upload: file may be too large (10MB max)")
		return
	}

	file, fh, err := r.FormFile("file")
	if err != nil {
		h.logger.Printf("river import: missing file field: %v", err)
		h.writeError(w, http.StatusBadRequest, "missing 'file' field in upload")
		return
	}
	defer file.Close()
	h.logger.Printf("river import: received file %q (%d bytes)", fh.Filename, fh.Size)

	result, err := exchange.ParseRiverCSV(file)
	if err != nil {
		h.logger.Printf("river import: CSV parse error: %v", err)
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(result.Rows) == 0 {
		h.writeError(w, http.StatusBadRequest, "CSV contains no valid data rows")
		return
	}

	summary, err := h.importStore.ImportRiverCSV(r.Context(), result.Rows)
	if err != nil {
		h.logger.Printf("river import failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to import transactions")
		return
	}

	resp := importResponse{
		Total:        summary.Total,
		NewPurchases: summary.NewPurchases,
		Duplicates:   summary.Duplicates,
		ParseErrors:  result.Errors,
	}

	// HTMX request — render partial
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.renderer.Render(w, "import_result", resp); err != nil {
			h.logger.Printf("failed to render import result: %v", err)
		}
		return
	}

	h.writeJSON(w, http.StatusOK, resp)
}

// HandleCoinbaseImport serves POST /api/import/coinbase.
func (h *Handler) HandleCoinbaseImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.logger.Printf("coinbase import: failed to parse multipart form: %v", err)
		h.writeError(w, http.StatusBadRequest, "failed to parse upload: file may be too large (10MB max)")
		return
	}

	file, fh, err := r.FormFile("file")
	if err != nil {
		h.logger.Printf("coinbase import: missing file field: %v", err)
		h.writeError(w, http.StatusBadRequest, "missing 'file' field in upload")
		return
	}
	defer file.Close()
	h.logger.Printf("coinbase import: received file %q (%d bytes)", fh.Filename, fh.Size)

	result, err := exchange.ParseCoinbaseCSV(file)
	if err != nil {
		h.logger.Printf("coinbase import: CSV parse error: %v", err)
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if len(result.Rows) == 0 {
		h.writeError(w, http.StatusBadRequest, "CSV contains no BTC transactions")
		return
	}

	summary, err := h.importStore.ImportCoinbaseCSV(r.Context(), result.Rows)
	if err != nil {
		h.logger.Printf("coinbase import failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to import transactions")
		return
	}

	resp := importResponse{
		Total:        summary.Total,
		NewPurchases: summary.NewPurchases,
		Duplicates:   summary.Duplicates,
		ParseErrors:  result.Errors,
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.renderer.Render(w, "import_result", resp); err != nil {
			h.logger.Printf("failed to render import result: %v", err)
		}
		return
	}

	h.writeJSON(w, http.StatusOK, resp)
}
