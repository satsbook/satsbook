package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/exchange"
	"github.com/satsbook/satsbook/internal/license"
	"github.com/satsbook/satsbook/internal/lnd"
	"github.com/satsbook/satsbook/internal/monarch"
	"github.com/satsbook/satsbook/internal/tax"
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
	ListExchangeTransactions(ctx context.Context, source string, limit, offset int) (*db.ExchangeTransactionPage, error)
	PortfolioPosition(ctx context.Context, since time.Time) (*db.PortfolioPositionResult, error)
	PortfolioSnapshots(ctx context.Context, days int) ([]db.PortfolioSnapshot, error)
	BackfillPortfolioSnapshots(ctx context.Context) (int, error)
	PortfolioBreakdownQuery(ctx context.Context) (*db.PortfolioBreakdown, error)
	NetFlowSummary(ctx context.Context, since time.Time, excludeTransfers bool) (*db.NetFlowResult, error)
	NetFlowSummaryBySource(ctx context.Context, since time.Time, sources []string, excludeTransfers bool) (*db.NetFlowResult, error)
	SetTransferFlag(ctx context.Context, sourceID string, isTransfer bool) error
	GetTransferFlag(ctx context.Context, sourceID string) (bool, error)
	ListTransferCandidates(ctx context.Context, sourceID string, amountSat int64, ts time.Time) ([]db.TransferCandidate, error)
	StrikeCollateralSats(ctx context.Context) (int64, error)
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

// WalletStore defines operations for wallet tracking.
type WalletStore interface {
	AddWallet(ctx context.Context, label, walletType, value, derivationType string) (int64, error)
	RemoveWallet(ctx context.Context, id int64) error
	ListWallets(ctx context.Context) ([]db.WatchedWallet, error)
	GetWallet(ctx context.Context, id int64) (*db.WatchedWallet, error)
	UpdateWalletBalance(ctx context.Context, id int64, balanceSats int64) error
	TotalWatchedBalance(ctx context.Context) (int64, error)
}

// ImportStore defines operations for importing exchange data.
type ImportStore interface {
	ImportStrikeCSV(ctx context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error)
	ImportRiverCSV(ctx context.Context, rows []exchange.RiverRow) (*db.ImportSummary, error)
	ImportCoinbaseCSV(ctx context.Context, rows []exchange.CoinbaseRow) (*db.ImportSummary, error)
	ImportSwanCSV(ctx context.Context, rows []exchange.SwanRow) (*db.ImportSummary, error)
	ClearExchangeSource(ctx context.Context, source string) (*db.ClearExchangeResult, error)
}

// WalletScanner scans wallet/xpub balances.
type WalletScanner interface {
	ScanAddress(ctx context.Context, address string) (int64, error)
	ScanXpub(ctx context.Context, xpub string, derivationType string) (int64, error)
	ScanDescriptor(ctx context.Context, descriptor string) (int64, error)
}

// SettingsStore defines operations for user-configurable settings.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

// TransactionStore defines operations for the unified transaction ledger.
type TransactionStore interface {
	ListUnifiedTransactions(ctx context.Context, f db.TransactionFilter) (*db.UnifiedTransactionPage, error)
	SetTransactionNote(ctx context.Context, sourceID, note string) error
	DistinctTransactionValues(ctx context.Context) (sources []string, txTypes []string, err error)
}

// MonarchSyncer syncs BTC holdings to Monarch Money.
type MonarchSyncer interface {
	SyncHolding(ctx context.Context, btcQuantity float64) error
	SyncTransactions(ctx context.Context, txns []monarch.TxToSync) (*monarch.TxSyncResult, map[string]string, error)
}

// MonarchTxStore defines DB operations for Monarch transaction sync tracking.
type MonarchTxStore interface {
	ListUnsyncedTransactions(ctx context.Context, txTypes []string, sources []string) ([]db.UnifiedTransaction, error)
	MarkTransactionSynced(ctx context.Context, sourceID, monarchTxID string) error
	MonarchSyncedCount(ctx context.Context) (int, error)
	DistinctTransactionValues(ctx context.Context) (sources []string, txTypes []string, err error)
}

// TaxStore defines DB operations for tax/cost basis calculations.
type TaxStore interface {
	ListBTCLots(ctx context.Context) ([]db.BTCLot, error)
	ListDisposals(ctx context.Context) ([]db.DisposalRow, error)
}

// Handler serves dashboard API and HTML endpoints.
type Handler struct {
	store            DashboardStore
	node             NodeInfoProvider
	price            PriceProvider
	importStore      ImportStore
	walletStore      WalletStore
	walletScanner    WalletScanner
	settingsStore    SettingsStore
	txStore          TransactionStore
	monarchSyncer    MonarchSyncer
	monarchTxStore   MonarchTxStore
	taxStore         TaxStore
	licenseChecker    *license.DefaultChecker
	checkoutBaseURL   string // e.g. "https://api.satsbook.io"
	onMonarchChange   func(MonarchSyncer)
	onStrikeKeyChange func(apiKey string)
	pendingMonarch    *monarch.PendingClient
	logger            *log.Logger
	renderer          *Renderer
	version           string
	startTime         time.Time
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
		startTime:   time.Now(),
	}
}

// SetVersion records the build version for the health endpoint.
func (h *Handler) SetVersion(v string) {
	h.version = v
}

// getNodeInfo returns node info if an LND connection is configured, otherwise nil.
func (h *Handler) getNodeInfo(ctx context.Context) *lnd.NodeInfo {
	if h.node == nil {
		return nil
	}
	info, err := h.node.GetInfo(ctx)
	if err != nil {
		return nil
	}
	return info
}

// SetWalletStore sets the wallet store (optional, may not be available without Electrum).
func (h *Handler) SetWalletStore(ws WalletStore) {
	h.walletStore = ws
}

// SetWalletScanner sets the wallet scanner (optional, requires Electrum).
func (h *Handler) SetWalletScanner(ws WalletScanner) {
	h.walletScanner = ws
}

// SetSettingsStore sets the settings store.
func (h *Handler) SetSettingsStore(ss SettingsStore) {
	h.settingsStore = ss
}

// SetTransactionStore sets the transaction store for the unified ledger.
func (h *Handler) SetTransactionStore(ts TransactionStore) {
	h.txStore = ts
}

// SetMonarchTxStore sets the store for Monarch transaction sync tracking.
func (h *Handler) SetMonarchTxStore(ts MonarchTxStore) {
	h.monarchTxStore = ts
}

// SetMonarchSyncer sets the Monarch syncer and notifies any registered listener.
func (h *Handler) SetMonarchSyncer(ms MonarchSyncer) {
	h.monarchSyncer = ms
	if h.onMonarchChange != nil {
		h.onMonarchChange(ms)
	}
}

// SetTaxStore sets the tax store for cost basis calculations.
func (h *Handler) SetTaxStore(ts TaxStore) {
	h.taxStore = ts
}

// SetLicenseChecker sets the license checker for runtime activation.
func (h *Handler) SetLicenseChecker(lc *license.DefaultChecker) {
	h.licenseChecker = lc
}

// SetCheckoutBaseURL sets the base URL for the license server (e.g. "https://api.satsbook.io").
func (h *Handler) SetCheckoutBaseURL(url string) {
	h.checkoutBaseURL = url
}

// OnMonarchChange registers a callback invoked when the Monarch syncer changes.
func (h *Handler) OnMonarchChange(fn func(MonarchSyncer)) {
	h.onMonarchChange = fn
}

// OnStrikeKeyChange registers a callback invoked when the Strike API key is saved or cleared.
// The callback receives the new key value ("" means disconnected).
func (h *Handler) OnStrikeKeyChange(fn func(apiKey string)) {
	h.onStrikeKeyChange = fn
}

// backfillSnapshots runs portfolio snapshot backfill in the background after imports.
func (h *Handler) backfillSnapshots(ctx context.Context) {
	go func() {
		n, err := h.store.BackfillPortfolioSnapshots(ctx)
		if err != nil {
			h.logger.Printf("portfolio backfill error: %v", err)
		} else if n > 0 {
			h.logger.Printf("portfolio backfill: inserted %d historical snapshots", n)
		}
	}()
}

// HandlePortfolioBackfill serves POST /api/portfolio/backfill.
// Reconstructs historical portfolio snapshots from exchange CSVs and wallet data.
// Uses a detached context so the operation completes even if the user navigates away.
func (h *Handler) HandlePortfolioBackfill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Use background context — this can take 30s+ and we don't want a
	// cancelled HTTP request to abort the DB writes mid-transaction.
	ctx := context.Background()

	n, err := h.store.BackfillPortfolioSnapshots(ctx)
	if err != nil {
		h.logger.Printf("portfolio backfill error: %v", err)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<span style="color:var(--danger,#ef4444);">Backfill failed: %s</span>`, err.Error())
			return
		}
		h.writeError(w, http.StatusInternalServerError, "backfill failed: "+err.Error())
		return
	}

	h.logger.Printf("portfolio backfill: inserted %d historical snapshots", n)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		msg := "No new data to backfill."
		if n > 0 {
			msg = fmt.Sprintf("Done — rebuilt %d days of portfolio history.", n)
		}
		fmt.Fprintf(w, `<span style="color:var(--success,#4ade80);">%s</span>`, msg)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]int{"inserted": n})
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

func satsToUSD(sats int64, btcPrice float64) float64 {
	return float64(sats) / 100_000_000.0 * btcPrice
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

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		h.writeError(w, http.StatusBadRequest, "no files uploaded")
		return
	}

	var allRows []exchange.StrikeRow
	var allErrors []string
	for _, fh := range files {
		file, err := fh.Open()
		if err != nil {
			allErrors = append(allErrors, fmt.Sprintf("%s: failed to open: %v", fh.Filename, err))
			continue
		}
		h.logger.Printf("strike import: received file %q (%d bytes)", fh.Filename, fh.Size)
		result, err := exchange.ParseStrikeCSV(file)
		file.Close()
		if err != nil {
			allErrors = append(allErrors, fmt.Sprintf("%s: %v", fh.Filename, err))
			continue
		}
		allRows = append(allRows, result.Rows...)
		allErrors = append(allErrors, result.Errors...)
	}

	if len(allRows) == 0 {
		msg := "no valid data rows found"
		if len(allErrors) > 0 {
			msg = allErrors[0]
		}
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	summary, err := h.importStore.ImportStrikeCSV(r.Context(), allRows)
	if err != nil {
		h.logger.Printf("strike import failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to import transactions")
		return
	}

	h.backfillSnapshots(r.Context())

	resp := importResponse{
		Total:          summary.Total,
		NewPurchases:   summary.NewPurchases,
		Updated:        summary.Updated,
		Duplicates:     summary.Duplicates,
		FilesProcessed: len(files),
		ParseErrors:    allErrors,
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
	Total          int      `json:"total"`
	NewPurchases   int      `json:"new_purchases"`
	Updated        int      `json:"updated,omitempty"`
	Duplicates     int      `json:"duplicates"`
	FilesProcessed int      `json:"files_processed,omitempty"`
	ParseErrors    []string `json:"parse_errors,omitempty"`
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

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		h.writeError(w, http.StatusBadRequest, "no files uploaded")
		return
	}

	var allRows []exchange.RiverRow
	var allErrors []string
	for _, fh := range files {
		file, err := fh.Open()
		if err != nil {
			allErrors = append(allErrors, fmt.Sprintf("%s: failed to open: %v", fh.Filename, err))
			continue
		}
		h.logger.Printf("river import: received file %q (%d bytes)", fh.Filename, fh.Size)
		result, err := exchange.ParseRiverCSV(file)
		file.Close()
		if err != nil {
			allErrors = append(allErrors, fmt.Sprintf("%s: %v", fh.Filename, err))
			continue
		}
		allRows = append(allRows, result.Rows...)
		allErrors = append(allErrors, result.Errors...)
	}

	if len(allRows) == 0 {
		msg := "no valid data rows found"
		if len(allErrors) > 0 {
			msg = allErrors[0]
		}
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	summary, err := h.importStore.ImportRiverCSV(r.Context(), allRows)
	if err != nil {
		h.logger.Printf("river import failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to import transactions")
		return
	}

	h.backfillSnapshots(r.Context())

	resp := importResponse{
		Total:          summary.Total,
		NewPurchases:   summary.NewPurchases,
		Updated:        summary.Updated,
		Duplicates:     summary.Duplicates,
		FilesProcessed: len(files),
		ParseErrors:    allErrors,
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

	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		h.writeError(w, http.StatusBadRequest, "no files uploaded")
		return
	}

	var allRows []exchange.CoinbaseRow
	var allErrors []string
	for _, fh := range files {
		file, err := fh.Open()
		if err != nil {
			allErrors = append(allErrors, fmt.Sprintf("%s: failed to open: %v", fh.Filename, err))
			continue
		}
		h.logger.Printf("coinbase import: received file %q (%d bytes)", fh.Filename, fh.Size)
		result, err := exchange.ParseCoinbaseCSV(file)
		file.Close()
		if err != nil {
			allErrors = append(allErrors, fmt.Sprintf("%s: %v", fh.Filename, err))
			continue
		}
		allRows = append(allRows, result.Rows...)
		allErrors = append(allErrors, result.Errors...)
	}

	if len(allRows) == 0 {
		msg := "no valid BTC transactions found"
		if len(allErrors) > 0 {
			msg = allErrors[0]
		}
		h.writeError(w, http.StatusBadRequest, msg)
		return
	}

	summary, err := h.importStore.ImportCoinbaseCSV(r.Context(), allRows)
	if err != nil {
		h.logger.Printf("coinbase import failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to import transactions")
		return
	}

	h.backfillSnapshots(r.Context())

	resp := importResponse{
		Total:          summary.Total,
		NewPurchases:   summary.NewPurchases,
		Updated:        summary.Updated,
		Duplicates:     summary.Duplicates,
		FilesProcessed: len(files),
		ParseErrors:    allErrors,
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

// HandleSwanImport serves POST /api/import/swan.
// Accepts up to three files: "trades" (purchases), "transfers" (BTC deposits),
// and "withdrawals". All are optional — upload whichever you have.
func (h *Handler) HandleSwanImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if err := r.ParseMultipartForm(10 << 20); err != nil {
		h.logger.Printf("swan import: failed to parse multipart form: %v", err)
		h.writeError(w, http.StatusBadRequest, "failed to parse upload: file may be too large (10MB max)")
		return
	}

	var allRows []exchange.SwanRow
	var allErrors []string
	var filesProcessed int

	// Parse trades file (purchases)
	if file, fh, err := r.FormFile("trades"); err == nil {
		defer file.Close()
		h.logger.Printf("swan import: trades file %q (%d bytes)", fh.Filename, fh.Size)
		result, err := exchange.ParseSwanTradesCSV(file)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "Trades CSV: "+err.Error())
			return
		}
		allRows = append(allRows, result.Rows...)
		allErrors = append(allErrors, result.Errors...)
		filesProcessed++
	}

	// Parse transfers file (BTC deposits only)
	if file, fh, err := r.FormFile("transfers"); err == nil {
		defer file.Close()
		h.logger.Printf("swan import: transfers file %q (%d bytes)", fh.Filename, fh.Size)
		result, err := exchange.ParseSwanTransfersCSV(file)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "Transfers CSV: "+err.Error())
			return
		}
		allRows = append(allRows, result.Rows...)
		allErrors = append(allErrors, result.Errors...)
		filesProcessed++
	}

	// Parse withdrawals file
	if file, fh, err := r.FormFile("withdrawals"); err == nil {
		defer file.Close()
		h.logger.Printf("swan import: withdrawals file %q (%d bytes)", fh.Filename, fh.Size)
		result, err := exchange.ParseSwanWithdrawalsCSV(file)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, "Withdrawals CSV: "+err.Error())
			return
		}
		allRows = append(allRows, result.Rows...)
		allErrors = append(allErrors, result.Errors...)
		filesProcessed++
	}

	if filesProcessed == 0 {
		h.writeError(w, http.StatusBadRequest, "No files uploaded. Please upload at least one Swan CSV.")
		return
	}

	if len(allRows) == 0 {
		h.writeError(w, http.StatusBadRequest, "CSV files contain no valid data rows")
		return
	}

	summary, err := h.importStore.ImportSwanCSV(r.Context(), allRows)
	if err != nil {
		h.logger.Printf("swan import failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to import transactions")
		return
	}

	h.backfillSnapshots(r.Context())

	resp := importResponse{
		Total:        summary.Total,
		NewPurchases: summary.NewPurchases,
		Updated:      summary.Updated,
		Duplicates:   summary.Duplicates,
		ParseErrors:  allErrors,
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

type clearImportResponse struct {
	Source         string `json:"source"`
	ImportsRemoved int64  `json:"imports_removed"`
	LotsRemoved    int64  `json:"lots_removed"`
}

// HandleClearImport serves POST /api/import/{vendor}/clear. It wipes every row
// in exchange_imports and btc_lots belonging to the given vendor after a
// `confirm=yes` form field is present. Only HTMX requests are accepted to
// prevent accidental wipes from stray curl commands.
func (h *Handler) HandleClearImport(source string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if r.Header.Get("HX-Request") != "true" {
			h.writeError(w, http.StatusBadRequest, "clear must be invoked from the UI")
			return
		}
		if !db.IsValidExchangeSource(source) {
			h.writeError(w, http.StatusBadRequest, "invalid source")
			return
		}
		if err := r.ParseForm(); err != nil {
			h.writeError(w, http.StatusBadRequest, "failed to parse form")
			return
		}
		if r.FormValue("confirm") != "yes" {
			h.writeError(w, http.StatusBadRequest, "confirmation required")
			return
		}

		result, err := h.importStore.ClearExchangeSource(r.Context(), source)
		if err != nil {
			h.logger.Printf("clear %s: %v", source, err)
			h.writeError(w, http.StatusInternalServerError, "failed to clear data")
			return
		}
		h.logger.Printf("cleared %s: %d imports, %d lots removed", source, result.ImportsRemoved, result.LotsRemoved)

		resp := clearImportResponse{
			Source:         result.Source,
			ImportsRemoved: result.ImportsRemoved,
			LotsRemoved:    result.LotsRemoved,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Trigger", "satsbook:data-cleared")
		if err := h.renderer.Render(w, "clear_import_result", resp); err != nil {
			h.logger.Printf("failed to render clear result: %v", err)
		}
	}
}

// HandleTaxExport generates and downloads a Form 8949 CSV file.
func (h *Handler) HandleTaxExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.taxStore == nil {
		http.Error(w, "tax export not available", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()

	// Load lots from DB.
	dbLots, err := h.taxStore.ListBTCLots(ctx)
	if err != nil {
		h.logger.Printf("tax export: list lots: %v", err)
		http.Error(w, "failed to load lots", http.StatusInternalServerError)
		return
	}

	// Load disposals from DB.
	dbDisposals, err := h.taxStore.ListDisposals(ctx)
	if err != nil {
		h.logger.Printf("tax export: list disposals: %v", err)
		http.Error(w, "failed to load disposals", http.StatusInternalServerError)
		return
	}

	// Convert to tax engine types.
	lots := make([]tax.Lot, len(dbLots))
	for i, l := range dbLots {
		lots[i] = tax.Lot{
			ID:         l.ID,
			AcquiredAt: l.AcquiredAt,
			AmountSat:  l.AmountSat,
			PriceUSD:   l.PriceUSD,
			Source:     l.Source,
			ExternalID: l.ExternalID,
		}
	}

	disposals := make([]tax.Disposal, len(dbDisposals))
	for i, d := range dbDisposals {
		disposals[i] = tax.Disposal{
			DisposedAt:  d.DisposedAt,
			AmountSat:   d.AmountSat,
			ProceedsUSD: d.ProceedsUSD,
			TxType:      d.TxType,
			Source:      d.Source,
			ExternalID:  d.ExternalID,
		}
	}

	// Determine method (default FIFO).
	method := tax.FIFO
	if m := r.URL.Query().Get("method"); m == "lifo" {
		method = tax.LIFO
	}

	// Run the cost basis engine.
	result, err := tax.Match(lots, disposals, method)
	if err != nil {
		h.logger.Printf("tax export: match: %v", err)
		http.Error(w, "failed to calculate cost basis", http.StatusInternalServerError)
		return
	}

	// Filter by tax year if specified.
	year := r.URL.Query().Get("year")
	if year != "" {
		yearInt, err := strconv.Atoi(year)
		if err == nil {
			var filtered []tax.TaxableEvent
			for _, e := range result.Events {
				if e.DisposedAt.Year() == yearInt {
					filtered = append(filtered, e)
				}
			}
			result.Events = filtered
		}
	}

	// Return JSON summary or CSV download.
	format := r.URL.Query().Get("format")
	if format == "json" {
		summary := tax.Summarize(result)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"summary":      summary,
			"events_count": len(result.Events),
			"method":       string(method),
		})
		return
	}

	// Default: CSV download.
	filename := "form8949"
	if year != "" {
		filename += "_" + year
	}
	filename += "_" + string(method) + ".csv"

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	if err := tax.WriteForm8949CSV(w, result.Events); err != nil {
		h.logger.Printf("tax export: write csv: %v", err)
	}
}

// HandleTaxSummary returns a JSON summary of tax calculations.
func (h *Handler) HandleTaxSummary(w http.ResponseWriter, r *http.Request) {
	if h.taxStore == nil {
		http.Error(w, "tax not available", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	dbLots, err := h.taxStore.ListBTCLots(ctx)
	if err != nil {
		http.Error(w, "failed to load lots", http.StatusInternalServerError)
		return
	}

	dbDisposals, err := h.taxStore.ListDisposals(ctx)
	if err != nil {
		http.Error(w, "failed to load disposals", http.StatusInternalServerError)
		return
	}

	lots := make([]tax.Lot, len(dbLots))
	for i, l := range dbLots {
		lots[i] = tax.Lot{
			ID: l.ID, AcquiredAt: l.AcquiredAt, AmountSat: l.AmountSat,
			PriceUSD: l.PriceUSD, Source: l.Source, ExternalID: l.ExternalID,
		}
	}
	disposals := make([]tax.Disposal, len(dbDisposals))
	for i, d := range dbDisposals {
		disposals[i] = tax.Disposal{
			DisposedAt: d.DisposedAt, AmountSat: d.AmountSat, ProceedsUSD: d.ProceedsUSD,
			TxType: d.TxType, Source: d.Source, ExternalID: d.ExternalID,
		}
	}

	method := tax.FIFO
	if m := r.URL.Query().Get("method"); m == "lifo" {
		method = tax.LIFO
	}

	result, err := tax.Match(lots, disposals, method)
	if err != nil {
		http.Error(w, "failed to calculate", http.StatusInternalServerError)
		return
	}

	summary := tax.Summarize(result)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

// HandleLicenseActivate handles POST /api/settings/license.
// Accepts a license key, saves it to settings, and verifies it.
func (h *Handler) HandleLicenseActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key := r.FormValue("license_key")
	if key == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">License key is required.</div>`)
		return
	}

	ctx := r.Context()

	// Save to settings DB
	if h.settingsStore != nil {
		if err := h.settingsStore.SetSetting(ctx, "license_key", key); err != nil {
			h.logger.Printf("license: save setting: %v", err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<div class="alert alert-error">Failed to save license key.</div>`)
			return
		}
	}

	// Verify with license server
	if h.licenseChecker != nil {
		if err := h.licenseChecker.SetKeyAndVerify(ctx, key); err != nil {
			h.logger.Printf("license: verify: %v", err)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<div class="alert alert-warning">Key saved but verification failed: %s. It will be retried automatically.</div>`, err)
			return
		}
		tier := h.licenseChecker.CurrentTier()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="alert alert-success">License activated! Tier: <strong>%s</strong>. Refresh the page to see your new features.</div>`, tier)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<div class="alert alert-success">License key saved. Restart the app to activate.</div>`)
}

// HandleLicenseVerify handles POST /api/settings/license/verify.
// Re-verifies the current license key with the license server.
func (h *Handler) HandleLicenseVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.licenseChecker == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="alert alert-error">No license checker configured.</div>`)
		return
	}

	if err := h.licenseChecker.Verify(r.Context()); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<div class="alert alert-error">Verification failed: %s</div>`, err)
		return
	}

	tier := h.licenseChecker.CurrentTier()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div class="alert alert-success">License verified. Tier: <strong>%s</strong></div>`, tier)
}

// HandleSubscribe handles GET /api/subscribe?tier=pro|power.
// It calls the license server's checkout API and redirects to Stripe Checkout.
func (h *Handler) HandleSubscribe(w http.ResponseWriter, r *http.Request) {
	tier := r.URL.Query().Get("tier")
	if tier != "pro" && tier != "power" {
		http.Error(w, "invalid tier — must be pro or power", http.StatusBadRequest)
		return
	}

	if h.checkoutBaseURL == "" {
		http.Error(w, "subscription not configured", http.StatusServiceUnavailable)
		return
	}

	// Build the return URL — after Stripe payment, redirect back to our settings page
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	returnURL := fmt.Sprintf("%s://%s/settings", scheme, r.Host)

	// Call the license server's checkout endpoint
	reqBody, _ := json.Marshal(map[string]string{
		"tier":       tier,
		"return_url": returnURL,
	})

	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.checkoutBaseURL+"/v1/checkout", bytes.NewReader(reqBody))
	if err != nil {
		h.logger.Printf("subscribe: create request: %v", err)
		http.Error(w, "failed to create checkout", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.logger.Printf("subscribe: call checkout API: %v", err)
		http.Error(w, "failed to reach subscription service", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		h.logger.Printf("subscribe: checkout API returned %d", resp.StatusCode)
		http.Error(w, "subscription service error", http.StatusBadGateway)
		return
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || result.URL == "" {
		h.logger.Printf("subscribe: decode response: %v", err)
		http.Error(w, "invalid response from subscription service", http.StatusBadGateway)
		return
	}

	http.Redirect(w, r, result.URL, http.StatusFound)
}

// healthResponse is the JSON payload returned by GET /health.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Uptime  int64  `json:"uptime_seconds"`
	DBOK    bool   `json:"db_ok"`
	LND     bool   `json:"lnd_configured"`
}

// HandleHealth returns basic system health information.
// It is always accessible regardless of license tier.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dbOK := false
	if _, err := h.store.LastSyncedAt(ctx); err == nil {
		dbOK = true
	}

	v := h.version
	if v == "" {
		v = "dev"
	}

	resp := healthResponse{
		Status:  "ok",
		Version: v,
		Uptime:  int64(time.Since(h.startTime).Seconds()),
		DBOK:    dbOK,
		LND:     h.node != nil,
	}
	h.writeJSON(w, http.StatusOK, resp)
}
