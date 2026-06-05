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
	// License tier (free, pro, power)
	Tier string

	// Node info
	NodeAlias   string
	NodePubKey  string
	NodeSynced  bool
	BlockHeight uint32
	LNDVersion  string

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
	ActiveChannels          int
	WalletBalanceSats       int64
	WalletBalanceUSD        float64
	ChannelLocalBalanceSats int64
	ChannelLocalBalanceUSD  float64

	// Exchange balances
	StrikeBalanceSats   int64
	StrikeBalanceUSD    float64
	RiverBalanceSats    int64
	RiverBalanceUSD     float64
	CoinbaseBalanceSats int64
	CoinbaseBalanceUSD  float64
	SwanBalanceSats     int64
	SwanBalanceUSD      float64
	ExchangeBalanceSats int64
	ExchangeBalanceUSD  float64

	// Cost basis (from exchange imports)
	TotalCostBasisUSD float64
	AvgCostPerBTC     float64 // TotalCostBasisUSD / total purchased BTC

	// Cold storage (watched wallets)
	ColdStorageSats int64
	ColdStorageUSD  float64

	// Headline: total BTC under control (wallet + channels + exchange + cold storage)
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
	DailyFees          []db.DailyFeeStat
	PortfolioSnapshots []db.PortfolioSnapshot

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

// HandleDashboard serves GET / (main dashboard page).
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
		Tier:        string(TierFromContext(ctx)),
		DefaultFrom: since30d.Format("2006-01-02"),
		DefaultTo:   now.Format("2006-01-02"),
	}

	// Fetch all data concurrently for the main dashboard
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
		coldStorageSats    int64
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
		info := h.getNodeInfo(ctx)
		if info != nil {
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

	if h.walletStore != nil {
		fetch(func() {
			total, err := h.walletStore.TotalWatchedBalance(ctx)
			if err == nil {
				mu.Lock()
				res.coldStorageSats = total
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	// Assemble dashboard template data
	data.ColdStorageSats = res.coldStorageSats

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
		data.SwanBalanceSats = res.portfolioAll.BySource["swan"].NetSats
		data.ExchangeBalanceSats = res.portfolioAll.ExchangeNetSats
		data.TotalCostBasisUSD = res.portfolioAll.TotalCostBasisUSD
		if res.portfolioAll.PurchasedSats > 0 {
			data.AvgCostPerBTC = res.portfolioAll.TotalCostBasisUSD / (float64(res.portfolioAll.PurchasedSats) / 1e8)
		}
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

	// Sum channel local balances
	for _, ch := range res.channels {
		data.ChannelLocalBalanceSats += ch.LocalBalance
	}

	// Headline: total BTC = wallet + channels + exchange + cold storage (routing fees already in channels)
	data.TotalBTCSats = data.WalletBalanceSats + data.ChannelLocalBalanceSats + data.ExchangeBalanceSats + data.ColdStorageSats

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
		data.StrikeBalanceSats != 0 || data.RiverBalanceSats != 0 || data.CoinbaseBalanceSats != 0 || data.SwanBalanceSats != 0
	data.ShowOnboarding = !data.HasFeeHistory && !data.HasExchangeImports
	data.ShowImportBanner = data.HasFeeHistory && !data.HasExchangeImports
	data.ShowLNDBanner = data.HasExchangeImports && !data.LNDConnected

	if res.btcPrice > 0 {
		data.BTCPriceUSD = res.btcPrice
		data.PriceFetchedAt = res.priceFetched
		data.FeesAllTimeUSD = satsToUSD(data.FeesAllTimeSats, res.btcPrice)
		data.WalletBalanceUSD = satsToUSD(data.WalletBalanceSats, res.btcPrice)
		data.ChannelLocalBalanceUSD = satsToUSD(data.ChannelLocalBalanceSats, res.btcPrice)
		data.ColdStorageUSD = satsToUSD(data.ColdStorageSats, res.btcPrice)
		data.StrikeBalanceUSD = satsToUSD(data.StrikeBalanceSats, res.btcPrice)
		data.RiverBalanceUSD = satsToUSD(data.RiverBalanceSats, res.btcPrice)
		data.CoinbaseBalanceUSD = satsToUSD(data.CoinbaseBalanceSats, res.btcPrice)
		data.SwanBalanceUSD = satsToUSD(data.SwanBalanceSats, res.btcPrice)
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
