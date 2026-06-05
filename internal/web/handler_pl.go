package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
)

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
	NetBTCSats  int64
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
		feesMsat        int64
		routedCount     int64
		strikeSummary   *db.ExchangeSummaryResult
		riverSummary    *db.ExchangeSummaryResult
		coinbaseSummary *db.ExchangeSummaryResult
		nodeInfo        *lnd.NodeInfo
		lastSynced      time.Time
		btcPrice        float64
		priceFetched    time.Time
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
