package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
)

// DashboardData holds all data needed to render the dashboard page.
type DashboardData struct {
	// Node info
	NodeAlias  string
	NodePubKey string
	NodeSynced bool
	BlockHeight uint32
	LNDVersion string

	// Fee summary
	FeesAllTimeSats int64
	FeesAllTimeUSD  float64
	Fees30dSats     int64
	Fees7dSats      int64

	// Routed counts
	RoutedAllTime int64
	Routed30d     int64
	Routed7d      int64

	// Other stats
	ActiveChannels    int
	WalletBalanceSats int64
	WalletBalanceUSD  float64

	// Exchange balances
	StrikeBalanceSats   int64
	StrikeBalanceUSD    float64
	RiverBalanceSats      int64
	RiverBalanceUSD       float64
	CoinbaseBalanceSats   int64
	CoinbaseBalanceUSD    float64
	ExchangeBalanceSats   int64
	ExchangeBalanceUSD    float64

	// Headline net BTC position (all-time: routing fees + exchange holdings)
	TotalBTCSats int64
	TotalBTCUSD  float64

	// YTD section
	YTDRoutingFeesSats int64
	YTDRoutingFeesUSD  float64
	YTDPurchasedSats   int64
	YTDPurchasedUSD    float64
	YTDNetChangeSats   int64
	YTDNetChangeUSD    float64

	// Onboarding state flags
	LNDConnected       bool
	HasFeeHistory      bool
	HasExchangeImports bool
	ShowOnboarding     bool // !HasFeeHistory && !HasExchangeImports
	ShowImportBanner   bool // HasFeeHistory && !HasExchangeImports
	ShowLNDBanner      bool // HasExchangeImports && !LNDConnected

	// Price
	BTCPriceUSD    float64
	PriceFetchedAt time.Time

	// Sync
	LastSyncedAt time.Time

	// Chart data
	DailyFees []db.DailyFeeStat

	// Channel list
	Channels []db.ChannelStat

	// Forwarding defaults
	DefaultFrom string
	DefaultTo   string
}

// ForwardingTableData holds data for the forwarding events partial.
type ForwardingTableData struct {
	Events    []db.ForwardingEvent
	Page      int
	Limit     int
	Total     int64
	PageStart int
	PageEnd   int
	HasNext   bool
	From      string
	To        string
}

// HandleDashboard serves GET /.
func (h *Handler) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	now := time.Now()
	since30d := now.AddDate(0, 0, -30)
	since7d := now.AddDate(0, 0, -7)
	sinceYTD := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)

	data := DashboardData{
		DefaultFrom: since30d.Format("2006-01-02"),
		DefaultTo:   now.Format("2006-01-02"),
	}

	// Fetch all data concurrently
	var mu sync.Mutex
	var wg sync.WaitGroup

	type result struct {
		fees30d, routed30d int64
		fees7d, routed7d   int64
		activeChannels     int
		balance            *db.WalletBalanceSnapshot
		channels           []db.ChannelStat
		dailyFees          []db.DailyFeeStat
		nodeInfo           *lnd.NodeInfo
		lastSynced         time.Time
		btcPrice           float64
		priceFetched       time.Time
		portfolioAll       *db.PortfolioPositionResult
		portfolioYTD       *db.PortfolioPositionResult
	}
	var res result

	fetch := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	fetch(func() {
		// PortfolioPosition replaces the previous all-time FeeSummary + 3x ExchangeBalance
		// calls with one query for exchange totals + one for routing fees.
		p, err := h.store.PortfolioPosition(ctx, time.Time{})
		if err == nil {
			mu.Lock()
			res.portfolioAll = p
			mu.Unlock()
		}
	})

	fetch(func() {
		p, err := h.store.PortfolioPosition(ctx, sinceYTD)
		if err == nil {
			mu.Lock()
			res.portfolioYTD = p
			mu.Unlock()
		}
	})

	fetch(func() {
		fees30d, routed30d, err := h.store.FeeSummary(ctx, since30d)
		if err == nil {
			mu.Lock()
			res.fees30d = fees30d
			res.routed30d = routed30d
			mu.Unlock()
		}
	})

	fetch(func() {
		fees7d, routed7d, err := h.store.FeeSummary(ctx, since7d)
		if err == nil {
			mu.Lock()
			res.fees7d = fees7d
			res.routed7d = routed7d
			mu.Unlock()
		}
	})

	fetch(func() {
		count, err := h.store.ActiveChannelCount(ctx)
		if err == nil {
			mu.Lock()
			res.activeChannels = count
			mu.Unlock()
		}
	})

	fetch(func() {
		bal, err := h.store.LatestWalletBalance(ctx)
		if err == nil {
			mu.Lock()
			res.balance = bal
			mu.Unlock()
		}
	})

	fetch(func() {
		stats, err := h.store.ChannelStats(ctx)
		if err == nil {
			mu.Lock()
			res.channels = stats
			mu.Unlock()
		}
	})

	fetch(func() {
		fees, err := h.store.DailyFees(ctx, since30d)
		if err == nil {
			mu.Lock()
			res.dailyFees = fees
			mu.Unlock()
		}
	})

	fetch(func() {
		info, err := h.node.GetInfo(ctx)
		if err == nil {
			mu.Lock()
			res.nodeInfo = info
			mu.Unlock()
		}
	})

	fetch(func() {
		t, err := h.store.LastSyncedAt(ctx)
		if err == nil {
			mu.Lock()
			res.lastSynced = t
			mu.Unlock()
		}
	})

	fetch(func() {
		price, err := h.price.GetBTCPrice(ctx)
		if err == nil {
			mu.Lock()
			res.btcPrice = price
			res.priceFetched = h.price.FetchedAt()
			mu.Unlock()
		}
	})

	wg.Wait()

	// Assemble template data
	if res.nodeInfo != nil {
		data.NodeAlias = res.nodeInfo.Alias
		data.NodePubKey = res.nodeInfo.PubKey
		data.NodeSynced = res.nodeInfo.Synced
		data.BlockHeight = res.nodeInfo.BlockHeight
		data.LNDVersion = res.nodeInfo.Version
	}

	// All-time fees + per-source balances come from PortfolioPosition.
	if res.portfolioAll != nil {
		data.FeesAllTimeSats = res.portfolioAll.RoutingFeesSats
		data.RoutedAllTime = res.portfolioAll.RoutedCount
		data.StrikeBalanceSats = res.portfolioAll.BySource["strike"].NetSats
		data.RiverBalanceSats = res.portfolioAll.BySource["river"].NetSats
		data.CoinbaseBalanceSats = res.portfolioAll.BySource["coinbase"].NetSats
		data.ExchangeBalanceSats = res.portfolioAll.ExchangeNetSats
		// Headline: routing fees + net exchange holdings
		data.TotalBTCSats = res.portfolioAll.RoutingFeesSats + res.portfolioAll.ExchangeNetSats
	}

	data.Fees30dSats = res.fees30d / 1000
	data.Fees7dSats = res.fees7d / 1000
	data.Routed30d = res.routed30d
	data.Routed7d = res.routed7d
	data.ActiveChannels = res.activeChannels
	data.LastSyncedAt = res.lastSynced
	data.DailyFees = res.dailyFees
	data.Channels = res.channels

	if res.balance != nil {
		data.WalletBalanceSats = res.balance.TotalSat
	}

	// YTD section
	if res.portfolioYTD != nil {
		data.YTDRoutingFeesSats = res.portfolioYTD.RoutingFeesSats
		data.YTDPurchasedSats = res.portfolioYTD.PurchasedSats
		data.YTDNetChangeSats = res.portfolioYTD.RoutingFeesSats + res.portfolioYTD.ExchangeNetSats
	}

	// Onboarding flags
	data.LNDConnected = res.nodeInfo != nil
	data.HasFeeHistory = data.FeesAllTimeSats > 0
	data.HasExchangeImports = data.ExchangeBalanceSats != 0 ||
		data.StrikeBalanceSats != 0 || data.RiverBalanceSats != 0 || data.CoinbaseBalanceSats != 0
	data.ShowOnboarding = !data.HasFeeHistory && !data.HasExchangeImports
	data.ShowImportBanner = data.HasFeeHistory && !data.HasExchangeImports
	data.ShowLNDBanner = data.HasExchangeImports && !data.LNDConnected

	if res.btcPrice > 0 {
		data.BTCPriceUSD = res.btcPrice
		data.PriceFetchedAt = res.priceFetched
		data.FeesAllTimeUSD = satsToUSD(data.FeesAllTimeSats, res.btcPrice)
		data.WalletBalanceUSD = satsToUSD(data.WalletBalanceSats, res.btcPrice)
		data.StrikeBalanceUSD = satsToUSD(data.StrikeBalanceSats, res.btcPrice)
		data.RiverBalanceUSD = satsToUSD(data.RiverBalanceSats, res.btcPrice)
		data.CoinbaseBalanceUSD = satsToUSD(data.CoinbaseBalanceSats, res.btcPrice)
		data.ExchangeBalanceUSD = satsToUSD(data.ExchangeBalanceSats, res.btcPrice)
		data.TotalBTCUSD = satsToUSD(data.TotalBTCSats, res.btcPrice)
		data.YTDRoutingFeesUSD = satsToUSD(data.YTDRoutingFeesSats, res.btcPrice)
		data.YTDPurchasedUSD = satsToUSD(data.YTDPurchasedSats, res.btcPrice)
		data.YTDNetChangeUSD = satsToUSD(data.YTDNetChangeSats, res.btcPrice)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "layout", data); err != nil {
		h.logger.Printf("failed to render dashboard: %v", err)
	}
}

// HandleForwardingPartial serves GET /partials/forwarding (HTMX partial).
func (h *Handler) HandleForwardingPartial(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	now := time.Now()
	from, err := parseDateParam(r, "from", now.AddDate(0, 0, -30))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid 'from' date")
		return
	}
	to, err := parseDateParam(r, "to", now)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid 'to' date")
		return
	}
	// Extend 'to' to end of day
	to = to.Add(24*time.Hour - time.Second)

	page := parseIntParam(r, "page", 1)
	if page < 1 {
		page = 1
	}
	limit := parseIntParam(r, "limit", 20)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	result, err := h.store.ForwardingEvents(ctx, from, to, limit, offset)
	if err != nil {
		h.logger.Printf("failed to query forwarding events: %v", err)
		http.Error(w, "failed to load forwarding events", http.StatusInternalServerError)
		return
	}

	pageStart := offset + 1
	if result.Total == 0 {
		pageStart = 0
	}
	pageEnd := offset + len(result.Events)

	data := ForwardingTableData{
		Events:    result.Events,
		Page:      page,
		Limit:     limit,
		Total:     result.Total,
		PageStart: pageStart,
		PageEnd:   pageEnd,
		HasNext:   int64(pageEnd) < result.Total,
		From:      r.URL.Query().Get("from"),
		To:        r.URL.Query().Get("to"),
	}
	if data.From == "" {
		data.From = now.AddDate(0, 0, -30).Format("2006-01-02")
	}
	if data.To == "" {
		data.To = now.Format("2006-01-02")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "forwarding_table", data); err != nil {
		h.logger.Printf("failed to render forwarding partial: %v", err)
	}
}

// HandleImportPage serves GET /import.
func (h *Handler) HandleImportPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "import_layout", nil); err != nil {
		h.logger.Printf("failed to render import page: %v", err)
	}
}

// PeriodOption represents a selectable time period for the P&L page.
type PeriodOption struct {
	Value  string
	Label  string
	Active bool
}

// PLPageData holds all data needed to render the P&L page.
type PLPageData struct {
	// Layout fields (required by pl_layout.html)
	NodeAlias      string
	NodePubKey     string
	NodeSynced     bool
	BlockHeight    uint32
	LNDVersion     string
	LastSyncedAt   time.Time
	BTCPriceUSD    float64
	PriceFetchedAt time.Time

	// Period selector
	SelectedPeriod string
	Periods        []PeriodOption

	// Routing income
	RoutingFeeSats int64
	RoutingFeeUSD  float64
	RoutedCount    int64

	// Exchange summary
	PurchasedSats        int64
	ReceivedSats         int64
	SoldSats             int64
	SentSats             int64
	TotalCostBasisUSD    float64
	TotalSaleProceedsUSD float64
	FeesPaidUSD          float64
	StrikeFeesPaidUSD    float64
	RiverFeesPaidUSD     float64
	CoinbaseFeesPaidUSD  float64

	// Net position
	NetBTCSats int64
	NetUSDSpent float64
}

// HandlePLPage serves GET /pl.
func (h *Handler) HandlePLPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()

	// Parse period
	period := r.URL.Query().Get("period")
	var since time.Time
	switch period {
	case "90d":
		since = now.AddDate(0, 0, -90)
	case "ytd":
		since = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	case "all":
		since = time.Time{}
	default:
		period = "30d"
		since = now.AddDate(0, 0, -30)
	}

	periods := []PeriodOption{
		{Value: "30d", Label: "30 days", Active: period == "30d"},
		{Value: "90d", Label: "90 days", Active: period == "90d"},
		{Value: "ytd", Label: "YTD", Active: period == "ytd"},
		{Value: "all", Label: "All time", Active: period == "all"},
	}

	data := PLPageData{
		SelectedPeriod: period,
		Periods:        periods,
	}

	// Concurrent data fetch
	var mu sync.Mutex
	var wg sync.WaitGroup

	type result struct {
		feesMsat       int64
		routedCount    int64
		strikeSummary  *db.ExchangeSummaryResult
		riverSummary    *db.ExchangeSummaryResult
		coinbaseSummary *db.ExchangeSummaryResult
		nodeInfo        *lnd.NodeInfo
		lastSynced     time.Time
		btcPrice       float64
		priceFetched   time.Time
	}
	var res result

	fetch := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	fetch(func() {
		fees, routed, err := h.store.FeeSummary(ctx, since)
		if err == nil {
			mu.Lock()
			res.feesMsat = fees
			res.routedCount = routed
			mu.Unlock()
		}
	})

	fetch(func() {
		summary, err := h.store.ExchangeSummary(ctx, "strike", since)
		if err == nil {
			mu.Lock()
			res.strikeSummary = summary
			mu.Unlock()
		}
	})

	fetch(func() {
		summary, err := h.store.ExchangeSummary(ctx, "river", since)
		if err == nil {
			mu.Lock()
			res.riverSummary = summary
			mu.Unlock()
		}
	})

	fetch(func() {
		summary, err := h.store.ExchangeSummary(ctx, "coinbase", since)
		if err == nil {
			mu.Lock()
			res.coinbaseSummary = summary
			mu.Unlock()
		}
	})

	fetch(func() {
		info, err := h.node.GetInfo(ctx)
		if err == nil {
			mu.Lock()
			res.nodeInfo = info
			mu.Unlock()
		}
	})

	fetch(func() {
		t, err := h.store.LastSyncedAt(ctx)
		if err == nil {
			mu.Lock()
			res.lastSynced = t
			mu.Unlock()
		}
	})

	fetch(func() {
		price, err := h.price.GetBTCPrice(ctx)
		if err == nil {
			mu.Lock()
			res.btcPrice = price
			res.priceFetched = h.price.FetchedAt()
			mu.Unlock()
		}
	})

	wg.Wait()

	// Assemble template data
	if res.nodeInfo != nil {
		data.NodeAlias = res.nodeInfo.Alias
		data.NodePubKey = res.nodeInfo.PubKey
		data.NodeSynced = res.nodeInfo.Synced
		data.BlockHeight = res.nodeInfo.BlockHeight
		data.LNDVersion = res.nodeInfo.Version
	}

	data.LastSyncedAt = res.lastSynced
	data.RoutingFeeSats = res.feesMsat / 1000
	data.RoutedCount = res.routedCount

	// Aggregate exchange summaries across all sources
	for _, s := range []*db.ExchangeSummaryResult{res.strikeSummary, res.riverSummary, res.coinbaseSummary} {
		if s != nil {
			data.PurchasedSats += s.PurchasedSats
			data.ReceivedSats += s.ReceivedSats
			data.SoldSats += s.SoldSats
			data.SentSats += s.SentSats
			data.TotalCostBasisUSD += s.TotalCostBasisUSD
			data.TotalSaleProceedsUSD += s.TotalSaleProceedsUSD
			data.FeesPaidUSD += s.FeesPaidUSD
		}
	}
	if res.strikeSummary != nil {
		data.StrikeFeesPaidUSD = res.strikeSummary.FeesPaidUSD
	}
	if res.riverSummary != nil {
		data.RiverFeesPaidUSD = res.riverSummary.FeesPaidUSD
	}
	if res.coinbaseSummary != nil {
		data.CoinbaseFeesPaidUSD = res.coinbaseSummary.FeesPaidUSD
	}

	// Net BTC = routing fees + purchased + received - sold - sent
	data.NetBTCSats = data.RoutingFeeSats + data.PurchasedSats + data.ReceivedSats - data.SoldSats - data.SentSats
	// Net USD spent = cost basis - sale proceeds + fees paid
	data.NetUSDSpent = data.TotalCostBasisUSD - data.TotalSaleProceedsUSD + data.FeesPaidUSD

	if res.btcPrice > 0 {
		data.BTCPriceUSD = res.btcPrice
		data.PriceFetchedAt = res.priceFetched
		data.RoutingFeeUSD = msatToUSD(res.feesMsat, res.btcPrice)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "pl_layout", data); err != nil {
		h.logger.Printf("failed to render P&L page: %v", err)
	}
}

// parseDateParam parses a "YYYY-MM-DD" query parameter.
func parseDateParam(r *http.Request, key string, defaultVal time.Time) (time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal, nil
	}
	return time.Parse("2006-01-02", v)
}
