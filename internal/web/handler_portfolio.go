package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/satsbook/satsbook/internal/db"
)

// BreakdownItem represents a single source in the portfolio donut chart / table.
type BreakdownItem struct {
	Label     string
	SourceKey string // machine key for API calls (e.g. "strike", "onchain")
	Clickable bool   // false for channels/cold_storage (no transaction flows)
	Sats      int64
	USD       float64
	Pct       float64
}

// PortfolioPageData holds data for the /portfolio page.
type PortfolioPageData struct {
	// Layout fields
	Tier           string
	NodeAlias      string
	NodePubKey     string
	NodeSynced     bool
	BlockHeight    uint32
	LNDVersion     string
	BTCPriceUSD    float64
	PriceFetchedAt time.Time
	LastSyncedAt   time.Time

	// Breakdown
	Items       []BreakdownItem
	DonutColors []string
	TotalSats   int64
	TotalUSD    float64

	// Date filter for linking to transactions
	DateFrom string

	// Net flows
	NetFlow *db.NetFlowResult

	// Cost basis
	TotalCostBasisUSD float64
	AvgCostPerBTC     float64
	UnrealizedGainUSD float64
	CurrentValueUSD   float64

	// Period selector
	SelectedPeriod string
	Periods        []PeriodOption
}

// HandlePortfolioPage serves GET /portfolio.
func (h *Handler) HandlePortfolioPage(w http.ResponseWriter, r *http.Request) {
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

	dateFrom := ""
	if !since.IsZero() {
		dateFrom = since.Format("2006-01-02")
	}

	data := PortfolioPageData{
		Tier:           string(TierFromContext(ctx)),
		SelectedPeriod: period,
		DateFrom:       dateFrom,
		Periods: []PeriodOption{
			{Value: "30d", Label: "30 days", Active: period == "30d"},
			{Value: "90d", Label: "90 days", Active: period == "90d"},
			{Value: "ytd", Label: "YTD", Active: period == "ytd"},
			{Value: "all", Label: "All time", Active: period == "all"},
		},
	}

	// Concurrent data fetch
	var mu sync.Mutex
	var wg sync.WaitGroup

	fetch := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	var breakdown *db.PortfolioBreakdown
	fetch(func() {
		b, err := h.store.PortfolioBreakdownQuery(ctx)
		if err == nil {
			mu.Lock()
			breakdown = b
			mu.Unlock()
		}
	})

	var netFlow *db.NetFlowResult
	fetch(func() {
		nf, err := h.store.NetFlowSummary(ctx, since, true)
		if err == nil {
			mu.Lock()
			netFlow = nf
			mu.Unlock()
		}
	})

	var position *db.PortfolioPositionResult
	fetch(func() {
		pos, err := h.store.PortfolioPosition(ctx, time.Time{})
		if err == nil {
			mu.Lock()
			position = pos
			mu.Unlock()
		}
	})

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
		t, _ := h.store.LastSyncedAt(ctx)
		mu.Lock()
		data.LastSyncedAt = t
		mu.Unlock()
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

	// Build breakdown items
	if breakdown != nil {
		satsToUSD := func(sats int64) float64 {
			if data.BTCPriceUSD <= 0 {
				return 0
			}
			return float64(sats) / 1e8 * data.BTCPriceUSD
		}

		data.TotalSats = breakdown.TotalSats
		data.TotalUSD = satsToUSD(breakdown.TotalSats)

		type srcEntry struct {
			label     string
			sourceKey string
			clickable bool
			sats      int64
		}
		var entries []srcEntry
		if breakdown.OnChainSats != 0 {
			entries = append(entries, srcEntry{"On-chain Wallet", "onchain", true, breakdown.OnChainSats})
		}
		if breakdown.ChannelSats != 0 {
			entries = append(entries, srcEntry{"Lightning Channels", "channels", false, breakdown.ChannelSats})
		}
		if breakdown.ColdStorageSats != 0 {
			entries = append(entries, srcEntry{"Cold Storage", "cold_storage", false, breakdown.ColdStorageSats})
		}
		for _, src := range []string{"strike", "river", "coinbase", "swan"} {
			if sats, ok := breakdown.ExchangeSats[src]; ok && sats != 0 {
				entries = append(entries, srcEntry{templateSourceLabel(src), src, true, sats})
			}
		}

		for _, e := range entries {
			pct := 0.0
			if breakdown.TotalSats > 0 {
				pct = float64(e.sats) / float64(breakdown.TotalSats) * 100
			}
			data.Items = append(data.Items, BreakdownItem{
				Label:     e.label,
				SourceKey: e.sourceKey,
				Clickable: e.clickable,
				Sats:      e.sats,
				USD:       satsToUSD(e.sats),
				Pct:       pct,
			})
		}
		for i := range data.Items {
			data.DonutColors = append(data.DonutColors, donutChartColors[i%len(donutChartColors)])
		}
	}

	// Net flows
	data.NetFlow = netFlow

	// Cost basis from portfolio position
	if position != nil {
		data.TotalCostBasisUSD = position.TotalCostBasisUSD
		if position.PurchasedSats > 0 {
			data.AvgCostPerBTC = position.TotalCostBasisUSD / (float64(position.PurchasedSats) / 1e8)
		}
		if data.BTCPriceUSD > 0 && data.TotalSats > 0 {
			data.CurrentValueUSD = float64(data.TotalSats) / 1e8 * data.BTCPriceUSD
			data.UnrealizedGainUSD = data.CurrentValueUSD - position.TotalCostBasisUSD
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "portfolio_layout", data); err != nil {
		h.logger.Printf("failed to render portfolio page: %v", err)
	}
}

// portfolioSourceToTxSources maps a donut segment source key to transaction view source values.
var portfolioSourceToTxSources = map[string][]string{
	"onchain":  {"lnd_onchain"},
	"strike":   {"strike"},
	"river":    {"river"},
	"coinbase": {"coinbase"},
	"swan":     {"swan"},
}

// HandlePortfolioSourceFlows serves GET /portfolio/source-flows — per-source net flows partial.
func (h *Handler) HandlePortfolioSourceFlows(w http.ResponseWriter, r *http.Request) {
	sourceKey := r.URL.Query().Get("source")
	txSources, ok := portfolioSourceToTxSources[sourceKey]
	if !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}

	now := time.Now()
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
		since = now.AddDate(0, 0, -30)
	}

	nf, err := h.store.NetFlowSummaryBySource(r.Context(), since, txSources, true)
	if err != nil {
		h.logger.Printf("source flows: %v", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "source_flows", map[string]interface{}{
		"SourceKey": sourceKey,
		"Label":     portfolioSourceLabel(sourceKey),
		"NetFlow":   nf,
	}); err != nil {
		h.logger.Printf("render source flows: %v", err)
	}
}
