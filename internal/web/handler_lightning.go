package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
)

// HandleLightningPage serves GET /lightning (Lightning node stats page).
func (h *Handler) HandleLightningPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	since30d := now.AddDate(0, 0, -30)
	since7d := now.AddDate(0, 0, -7)
	data := DashboardData{
		DefaultFrom: since30d.Format("2006-01-02"),
		DefaultTo:   now.Format("2006-01-02"),
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	type result struct {
		feesAllTime, routedAllTime int64
		fees30d, routed30d         int64
		fees7d, routed7d           int64
		activeChannels             int
		balance                    *db.WalletBalanceSnapshot
		channels                   []db.ChannelStat
		dailyFees                  []db.DailyFeeStat
		nodeInfo                   *lnd.NodeInfo
		lastSynced                 time.Time
		btcPrice                   float64
		priceFetched               time.Time
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
		fees, routed, err := h.store.FeeSummary(ctx, time.Time{})
		if err == nil {
			mu.Lock()
			res.feesAllTime = fees
			res.routedAllTime = routed
			mu.Unlock()
		}
	})

	fetch(func() {
		fees, routed, err := h.store.FeeSummary(ctx, since30d)
		if err == nil {
			mu.Lock()
			res.fees30d = fees
			res.routed30d = routed
			mu.Unlock()
		}
	})

	fetch(func() {
		fees, routed, err := h.store.FeeSummary(ctx, since7d)
		if err == nil {
			mu.Lock()
			res.fees7d = fees
			res.routed7d = routed
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

	wg.Wait()

	if res.nodeInfo != nil {
		data.NodeAlias = res.nodeInfo.Alias
		data.NodePubKey = res.nodeInfo.PubKey
		data.NodeSynced = res.nodeInfo.Synced
		data.BlockHeight = res.nodeInfo.BlockHeight
		data.LNDVersion = res.nodeInfo.Version
	}

	data.FeesAllTimeSats = res.feesAllTime / 1000
	data.RoutedAllTime = res.routedAllTime
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

	if res.btcPrice > 0 {
		data.BTCPriceUSD = res.btcPrice
		data.PriceFetchedAt = res.priceFetched
		data.FeesAllTimeUSD = satsToUSD(data.FeesAllTimeSats, res.btcPrice)
		data.WalletBalanceUSD = satsToUSD(data.WalletBalanceSats, res.btcPrice)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.renderer.Render(w, "lightning_layout", data); err != nil {
		h.logger.Printf("failed to render lightning page: %v", err)
	}
}
