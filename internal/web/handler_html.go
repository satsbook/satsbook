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

	data := DashboardData{
		DefaultFrom: since30d.Format("2006-01-02"),
		DefaultTo:   now.Format("2006-01-02"),
	}

	// Fetch all data concurrently
	var mu sync.Mutex
	var wg sync.WaitGroup

	type result struct {
		feesAll, routedAll     int64
		fees30d, routed30d     int64
		fees7d, routed7d       int64
		activeChannels         int
		balance                *db.WalletBalanceSnapshot
		channels               []db.ChannelStat
		dailyFees              []db.DailyFeeStat
		nodeInfo               *lnd.NodeInfo
		lastSynced             time.Time
		btcPrice               float64
		priceFetched           time.Time
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
		feesAll, routedAll, err := h.store.FeeSummary(ctx, time.Time{})
		if err == nil {
			mu.Lock()
			res.feesAll = feesAll
			res.routedAll = routedAll
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

	data.FeesAllTimeSats = res.feesAll / 1000
	data.Fees30dSats = res.fees30d / 1000
	data.Fees7dSats = res.fees7d / 1000
	data.RoutedAllTime = res.routedAll
	data.Routed30d = res.routed30d
	data.Routed7d = res.routed7d
	data.ActiveChannels = res.activeChannels
	data.LastSyncedAt = res.lastSynced
	data.DailyFees = res.dailyFees
	data.Channels = res.channels

	if res.balance != nil {
		data.WalletBalanceSats = res.balance.TotalSat
	}

	if res.btcPrice > 0 {
		data.BTCPriceUSD = res.btcPrice
		data.PriceFetchedAt = res.priceFetched
		data.FeesAllTimeUSD = msatToUSD(res.feesAll, res.btcPrice)
		data.WalletBalanceUSD = float64(data.WalletBalanceSats) / 100_000_000.0 * res.btcPrice
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

// parseDateParam parses a "YYYY-MM-DD" query parameter.
func parseDateParam(r *http.Request, key string, defaultVal time.Time) (time.Time, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return defaultVal, nil
	}
	return time.Parse("2006-01-02", v)
}
