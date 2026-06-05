package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/satsbook/satsbook/internal/db"
)

// WalletsPageData holds data for the /wallets page.
type WalletsPageData struct {
	NodeAlias      string
	NodePubKey     string
	NodeSynced     bool
	BlockHeight    uint32
	LNDVersion     string
	LastSyncedAt   time.Time
	BTCPriceUSD    float64
	PriceFetchedAt time.Time

	Wallets           []db.WatchedWallet
	TotalBalanceSats  int64
	TotalBalanceUSD   float64
	ElectrumAvailable bool
	Toast             string
}

// HandleWalletsPage serves GET /wallets.
func (h *Handler) HandleWalletsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := WalletsPageData{
		ElectrumAvailable: h.walletScanner != nil,
		Toast:             r.URL.Query().Get("toast"),
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	fetch := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	if h.walletStore != nil {
		fetch(func() {
			wallets, err := h.walletStore.ListWallets(ctx)
			if err == nil {
				mu.Lock()
				data.Wallets = wallets
				mu.Unlock()
			}
		})

		fetch(func() {
			total, err := h.walletStore.TotalWatchedBalance(ctx)
			if err == nil {
				mu.Lock()
				data.TotalBalanceSats = total
				mu.Unlock()
			}
		})
	}

	fetch(func() {
		info := h.getNodeInfo(ctx)
		if info != nil {
			mu.Lock()
			data.NodeAlias = info.Alias
			data.NodePubKey = info.PubKey
			data.NodeSynced = info.Synced
			data.BlockHeight = info.BlockHeight
			data.LNDVersion = info.Version
			mu.Unlock()
		}
	})

	fetch(func() {
		t, err := h.store.LastSyncedAt(ctx)
		if err == nil {
			mu.Lock()
			data.LastSyncedAt = t
			mu.Unlock()
		}
	})

	fetch(func() {
		price, err := h.price.GetBTCPrice(ctx)
		if err == nil {
			mu.Lock()
			data.BTCPriceUSD = price
			data.PriceFetchedAt = h.price.FetchedAt()
			mu.Unlock()
		}
	})

	wg.Wait()

	if data.BTCPriceUSD > 0 {
		data.TotalBalanceUSD = satsToUSD(data.TotalBalanceSats, data.BTCPriceUSD)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "wallets_layout", data); err != nil {
		h.logger.Printf("failed to render wallets page: %v", err)
	}
}

// HandleAddWallet handles POST /api/wallets.
func (h *Handler) HandleAddWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.walletStore == nil {
		h.writeError(w, http.StatusServiceUnavailable, "wallet tracking not available")
		return
	}

	if err := r.ParseForm(); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid form data")
		return
	}

	label := r.FormValue("label")
	value := r.FormValue("value")

	if label == "" || value == "" {
		h.writeError(w, http.StatusBadRequest, "label and value are required")
		return
	}

	// Detect wallet type and derivation
	walletType := "address"
	derivationType := "bip84"

	if isDescriptor(value) {
		walletType = "descriptor"
		derivationType = ""
	} else if len(value) > 100 {
		// Likely an extended public key
		walletType = "xpub"
		if len(value) >= 4 {
			switch value[:4] {
			case "zpub":
				derivationType = "bip84"
			case "ypub":
				derivationType = "bip49"
			case "xpub":
				derivationType = "bip44"
			}
		}
	}

	id, err := h.walletStore.AddWallet(r.Context(), label, walletType, value, derivationType)
	if err != nil {
		h.logger.Printf("add wallet failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to add wallet")
		return
	}

	// Trigger a background scan if scanner is available
	if h.walletScanner != nil {
		go h.scanWallet(id, label, walletType, value, derivationType)
	}

	// Redirect back immediately with a toast notification
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/wallets?toast=scanning")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/wallets", http.StatusSeeOther)
}

// HandleRemoveWallet handles POST /api/wallets/delete.
func (h *Handler) HandleRemoveWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.walletStore == nil {
		h.writeError(w, http.StatusServiceUnavailable, "wallet tracking not available")
		return
	}

	if err := r.ParseForm(); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid form data")
		return
	}

	idStr := r.FormValue("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		h.writeError(w, http.StatusBadRequest, "invalid wallet id: "+idStr)
		return
	}

	if err := h.walletStore.RemoveWallet(r.Context(), id); err != nil {
		h.logger.Printf("remove wallet %d failed: %v", id, err)
		h.writeError(w, http.StatusInternalServerError, "failed to remove wallet")
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/wallets")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/wallets", http.StatusSeeOther)
}

// HandleRefreshWallet handles POST /api/wallets/refresh.
func (h *Handler) HandleRefreshWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.walletStore == nil || h.walletScanner == nil {
		h.writeError(w, http.StatusServiceUnavailable, "wallet scanning not available")
		return
	}

	if err := r.ParseForm(); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid form data")
		return
	}

	idStr := r.FormValue("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		h.writeError(w, http.StatusBadRequest, "invalid wallet id")
		return
	}

	wallet, err := h.walletStore.GetWallet(r.Context(), id)
	if err != nil || wallet == nil {
		h.writeError(w, http.StatusNotFound, "wallet not found")
		return
	}

	go h.scanWallet(wallet.ID, wallet.Label, wallet.Type, wallet.Value, wallet.DerivationType)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/wallets?toast=scanning")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/wallets?toast=scanning", http.StatusSeeOther)
}

// HandleRefreshAll handles POST /api/wallets/refresh-all.
// Kicks off background scans for all wallets and redirects immediately.
func (h *Handler) HandleRefreshAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.walletStore == nil || h.walletScanner == nil {
		h.writeError(w, http.StatusServiceUnavailable, "wallet scanning not available")
		return
	}

	wallets, err := h.walletStore.ListWallets(r.Context())
	if err != nil {
		h.logger.Printf("refresh-all: list wallets failed: %v", err)
		h.writeError(w, http.StatusInternalServerError, "failed to list wallets")
		return
	}

	// Scan all wallets sequentially in the background
	go func() {
		for _, wlt := range wallets {
			h.scanWallet(wlt.ID, wlt.Label, wlt.Type, wlt.Value, wlt.DerivationType)
		}
		h.logger.Printf("refresh-all: completed scanning %d wallets", len(wallets))
	}()

	h.logger.Printf("refresh-all: started background scan for %d wallets", len(wallets))

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/wallets?toast=scanning")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/wallets?toast=scanning", http.StatusSeeOther)
}

// scanWallet runs a balance scan for a single wallet in the background.
// It uses context.Background() so it survives after the HTTP request ends.
func (h *Handler) scanWallet(id int64, label, walletType, value, derivationType string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var balance int64
	var err error

	switch walletType {
	case "address":
		balance, err = h.walletScanner.ScanAddress(ctx, value)
	case "xpub":
		balance, err = h.walletScanner.ScanXpub(ctx, value, derivationType)
	case "descriptor":
		balance, err = h.walletScanner.ScanDescriptor(ctx, value)
	default:
		h.logger.Printf("scan %q: unknown wallet type %q", label, walletType)
		return
	}

	if err != nil {
		h.logger.Printf("scan %q: %v", label, err)
		return
	}

	if err := h.walletStore.UpdateWalletBalance(context.Background(), id, balance); err != nil {
		h.logger.Printf("scan %q: failed to save balance: %v", label, err)
		return
	}

	h.logger.Printf("scan %q: %d sats", label, balance)
}

// ExchangeDetailData holds data for the exchange transaction detail page.
type ExchangeDetailData struct {
	Source      string
	SourceLabel string

	// Summary stats
	Summary     *db.ExchangeSummaryResult
	BalanceSats int64

	// Transaction list
	Transactions []db.ExchangeTransaction
	Total        int
	Page         int
	Limit        int
	TotalPages   int

	// Price
	BTCPriceUSD    float64
	PriceFetchedAt time.Time
}

// HandleExchangeDetail serves GET /exchange/{source}.
func (h *Handler) HandleExchangeDetail(w http.ResponseWriter, r *http.Request) {
	source := strings.TrimPrefix(r.URL.Path, "/exchange/")
	if source == "" || (source != "strike" && source != "river" && source != "coinbase" && source != "swan") {
		http.NotFound(w, r)
		return
	}

	labels := map[string]string{"strike": "Strike", "river": "River", "coinbase": "Coinbase", "swan": "Swan"}
	ctx := r.Context()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit := 50
	offset := (page - 1) * limit

	var mu sync.Mutex
	var wg sync.WaitGroup
	data := ExchangeDetailData{
		Source:      source,
		SourceLabel: labels[source],
		Page:        page,
		Limit:       limit,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		summary, err := h.store.ExchangeSummary(ctx, source, time.Time{})
		if err == nil {
			mu.Lock()
			data.Summary = summary
			mu.Unlock()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		bal, err := h.store.ExchangeBalance(ctx, source)
		if err == nil {
			mu.Lock()
			data.BalanceSats = bal
			mu.Unlock()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		result, err := h.store.ListExchangeTransactions(ctx, source, limit, offset)
		if err == nil {
			mu.Lock()
			data.Transactions = result.Transactions
			data.Total = result.Total
			mu.Unlock()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		price, err := h.price.GetBTCPrice(ctx)
		if err == nil {
			mu.Lock()
			data.BTCPriceUSD = price
			data.PriceFetchedAt = h.price.FetchedAt()
			mu.Unlock()
		}
	}()

	wg.Wait()

	if data.Total > 0 {
		data.TotalPages = (data.Total + limit - 1) / limit
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "exchange_detail_layout", data); err != nil {
		h.logger.Printf("failed to render exchange detail: %v", err)
	}
}

// WalletDetailData holds data for the wallet detail page.
type WalletDetailData struct {
	Wallet      *db.WatchedWallet
	BTCPriceUSD float64
	BalanceUSD  float64
}

// HandleWalletDetail serves GET /wallets/{id}.
func (h *Handler) HandleWalletDetail(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/wallets/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}

	if h.walletStore == nil {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	wallet, err := h.walletStore.GetWallet(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := WalletDetailData{Wallet: wallet}
	price, err := h.price.GetBTCPrice(ctx)
	if err == nil && price > 0 {
		data.BTCPriceUSD = price
		data.BalanceUSD = float64(wallet.BalanceSats) / 1e8 * price
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "wallet_detail_layout", data); err != nil {
		h.logger.Printf("failed to render wallet detail: %v", err)
	}
}

// isDescriptor returns true if the value looks like a Bitcoin output descriptor.
func isDescriptor(v string) bool {
	prefixes := []string{
		"wpkh(", "sh(", "wsh(", "pk(", "pkh(", "combo(",
		"multi(", "sortedmulti(", "tr(", "addr(", "raw(",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

// parseDateParam parses a "YYYY-MM-DD" query parameter.
func parseDateParam(r *http.Request, key string, defaultVal time.Time) (time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal, nil
	}
	return time.Parse("2006-01-02", v)
}
