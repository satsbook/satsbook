package db

import (
	"context"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/exchange"
)

func seedForwardingEvents(t *testing.T, d *DB, events []ForwardingEvent) {
	t.Helper()
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.InsertForwardingEvents(events)
	})
	if err != nil {
		t.Fatalf("failed to seed forwarding events: %v", err)
	}
}

func seedChannels(t *testing.T, d *DB, channels []Channel) {
	t.Helper()
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertChannels(channels)
	})
	if err != nil {
		t.Fatalf("failed to seed channels: %v", err)
	}
}

func seedWalletSnapshot(t *testing.T, d *DB, s WalletBalanceSnapshot) {
	t.Helper()
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.InsertWalletBalanceSnapshot(s)
	})
	if err != nil {
		t.Fatalf("failed to seed wallet snapshot: %v", err)
	}
}

func TestFeeSummary_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	fees, count, err := d.FeeSummary(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fees != 0 || count != 0 {
		t.Errorf("expected 0/0 on empty table, got %d/%d", fees, count)
	}
}

func TestFeeSummary_WithData(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	seedForwardingEvents(t, d, []ForwardingEvent{
		{Timestamp: now.Add(-48 * time.Hour), ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
		{Timestamp: now.Add(-2 * time.Hour), ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 20000, AmtOutMsat: 18000, FeeMsat: 2000},
		{Timestamp: now.Add(-1 * time.Hour), ChanIDIn: 3, ChanIDOut: 4, AmtInMsat: 30000, AmtOutMsat: 27000, FeeMsat: 3000},
	})

	// All time
	fees, count, err := d.FeeSummary(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fees != 6000 {
		t.Errorf("expected all-time fees 6000, got %d", fees)
	}
	if count != 3 {
		t.Errorf("expected all-time count 3, got %d", count)
	}

	// Last 24 hours
	fees, count, err = d.FeeSummary(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fees != 5000 {
		t.Errorf("expected 24h fees 5000, got %d", fees)
	}
	if count != 2 {
		t.Errorf("expected 24h count 2, got %d", count)
	}
}

func TestActiveChannelCount(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	seedChannels(t, d, []Channel{
		{ChanID: 1, RemotePubKey: "a", LocalBalance: 100, RemoteBalance: 100, Active: true},
		{ChanID: 2, RemotePubKey: "b", LocalBalance: 100, RemoteBalance: 100, Active: false},
		{ChanID: 3, RemotePubKey: "c", LocalBalance: 100, RemoteBalance: 100, Active: true},
	})

	count, err := d.ActiveChannelCount(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 active channels, got %d", count)
	}
}

func TestActiveChannelCount_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	count, err := d.ActiveChannelCount(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 active channels, got %d", count)
	}
}

func TestLatestWalletBalance(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	seedWalletSnapshot(t, d, WalletBalanceSnapshot{CapturedAt: now.Add(-1 * time.Hour), TotalSat: 50000, ConfirmedSat: 40000, UnconfirmedSat: 10000})
	seedWalletSnapshot(t, d, WalletBalanceSnapshot{CapturedAt: now, TotalSat: 60000, ConfirmedSat: 55000, UnconfirmedSat: 5000})

	s, err := d.LatestWalletBalance(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if s.TotalSat != 60000 {
		t.Errorf("expected total 60000, got %d", s.TotalSat)
	}
}

func TestLatestWalletBalance_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	s, err := d.LatestWalletBalance(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != nil {
		t.Errorf("expected nil snapshot, got %+v", s)
	}
}

func TestChannelStats(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	seedChannels(t, d, []Channel{
		{ChanID: 100, RemotePubKey: "nodeA", LocalBalance: 50000, RemoteBalance: 50000, Active: true},
		{ChanID: 200, RemotePubKey: "nodeB", LocalBalance: 30000, RemoteBalance: 70000, Active: true},
	})

	seedForwardingEvents(t, d, []ForwardingEvent{
		// Old event through channel 100 (in) -> 200 (out)
		{Timestamp: now.Add(-60 * 24 * time.Hour), ChanIDIn: 100, ChanIDOut: 200, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
		// Recent event through channel 200 (in) -> 100 (out)
		{Timestamp: now.Add(-2 * time.Hour), ChanIDIn: 200, ChanIDOut: 100, AmtInMsat: 20000, AmtOutMsat: 18000, FeeMsat: 2000},
	})

	stats, err := d.ChannelStats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(stats))
	}

	// Both channels should see fees (each event credits both in and out channels)
	statMap := map[uint64]ChannelStat{}
	for _, s := range stats {
		statMap[s.ChanID] = s
	}

	ch100 := statMap[100]
	// Channel 100: in on old event (1000), out on recent event (2000) = 3000 all-time
	if ch100.FeesEarnedAllTimeMsat != 3000 {
		t.Errorf("channel 100 all-time fees: expected 3000, got %d", ch100.FeesEarnedAllTimeMsat)
	}
	// 30d: only the recent event (2000)
	if ch100.FeesEarned30dMsat != 2000 {
		t.Errorf("channel 100 30d fees: expected 2000, got %d", ch100.FeesEarned30dMsat)
	}

	ch200 := statMap[200]
	// Channel 200: out on old event (1000), in on recent event (2000) = 3000 all-time
	if ch200.FeesEarnedAllTimeMsat != 3000 {
		t.Errorf("channel 200 all-time fees: expected 3000, got %d", ch200.FeesEarnedAllTimeMsat)
	}
}

func TestChannelStats_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	stats, err := d.ChannelStats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected 0 channel stats, got %d", len(stats))
	}
}

func TestForwardingEvents_Pagination(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	var events []ForwardingEvent
	for i := 0; i < 10; i++ {
		events = append(events, ForwardingEvent{
			Timestamp:  now.Add(time.Duration(-i) * time.Hour),
			ChanIDIn:   uint64(i + 1),
			ChanIDOut:  uint64(i + 100),
			AmtInMsat:  10000,
			AmtOutMsat: 9000,
			FeeMsat:    1000,
		})
	}
	seedForwardingEvents(t, d, events)

	from := now.Add(-24 * time.Hour)
	to := now.Add(time.Hour)

	// First page
	page, err := d.ForwardingEvents(context.Background(), from, to, 3, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != 10 {
		t.Errorf("expected total 10, got %d", page.Total)
	}
	if len(page.Events) != 3 {
		t.Errorf("expected 3 events on page, got %d", len(page.Events))
	}

	// Second page
	page, err = d.ForwardingEvents(context.Background(), from, to, 3, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page.Events) != 3 {
		t.Errorf("expected 3 events on second page, got %d", len(page.Events))
	}
}

func TestForwardingEvents_DateRange(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	seedForwardingEvents(t, d, []ForwardingEvent{
		{Timestamp: now.Add(-72 * time.Hour), ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
		{Timestamp: now.Add(-2 * time.Hour), ChanIDIn: 3, ChanIDOut: 4, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
	})

	// Only last 24 hours
	page, err := d.ForwardingEvents(context.Background(), now.Add(-24*time.Hour), now, 100, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("expected 1 event in range, got %d", page.Total)
	}
}

func TestForwardingEvents_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	page, err := d.ForwardingEvents(context.Background(), now.Add(-24*time.Hour), now, 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != 0 {
		t.Errorf("expected 0 total, got %d", page.Total)
	}
	if len(page.Events) != 0 {
		t.Errorf("expected 0 events, got %d", len(page.Events))
	}
}

func TestDailyFees_WithData(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1)
	seedForwardingEvents(t, d, []ForwardingEvent{
		{Timestamp: yesterday, ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000},
		{Timestamp: yesterday.Add(time.Hour), ChanIDIn: 3, ChanIDOut: 4, AmtInMsat: 20000, AmtOutMsat: 18000, FeeMsat: 2000},
		{Timestamp: now, ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 30000, AmtOutMsat: 27000, FeeMsat: 3000},
	})

	stats, err := d.DailyFees(context.Background(), now.AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) < 1 {
		t.Fatalf("expected at least 1 day, got %d", len(stats))
	}

	// Total fees across all days should be 6000
	var totalFees int64
	for _, s := range stats {
		totalFees += s.TotalFeeMsat
	}
	if totalFees != 6000 {
		t.Errorf("expected total fees 6000, got %d", totalFees)
	}
}

func TestDailyFees_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	stats, err := d.DailyFees(context.Background(), time.Now().AddDate(0, 0, -7))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected 0 days, got %d", len(stats))
	}
}

func TestLastSyncedAt_NoData(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ts, err := d.LastSyncedAt(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("expected zero time, got %v", ts)
	}
}

func TestLastSyncedAt_WithData(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	// Insert sync state via RunSync
	now := time.Now().UTC()
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.SetSyncState("forwarding", now, 100)
	})
	if err != nil {
		t.Fatalf("failed to set sync state: %v", err)
	}

	ts, err := d.LastSyncedAt(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be within a second of now
	if now.Sub(ts).Abs() > time.Second {
		t.Errorf("expected time close to %v, got %v", now, ts)
	}
}

// --- ImportStrikeCSV tests ---

func testStrikeRows() []exchange.StrikeRow {
	now := time.Now().UTC()
	return []exchange.StrikeRow{
		{TransactionID: "tx-001", Date: now, Type: "Purchase", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 67.00, FeeUSD: 0.50, Status: "Completed"},
		{TransactionID: "tx-002", Date: now, Type: "Withdrawal", AmountSat: 50000, AmountBTC: 0.0005, AmountUSD: 33.50, FeeUSD: 1.00, Status: "Completed"},
		{TransactionID: "tx-003", Date: now, Type: "Purchase", AmountSat: 10000, AmountBTC: 0.0001, AmountUSD: 6.70, FeeUSD: 0.10, Status: "Pending"},
	}
}

func TestImportStrikeCSV_Basic(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testStrikeRows()
	summary, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Total != 3 {
		t.Errorf("expected total 3, got %d", summary.Total)
	}
	if summary.NewPurchases != 1 {
		t.Errorf("expected 1 new purchase (tx-001 completed), got %d", summary.NewPurchases)
	}
	if summary.Duplicates != 0 {
		t.Errorf("expected 0 duplicates, got %d", summary.Duplicates)
	}

	// Verify exchange_imports has 3 rows
	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM exchange_imports WHERE source = 'strike'").Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 exchange_imports rows, got %d", count)
	}

	// Verify btc_lots has 1 row (only completed purchase)
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'strike'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 btc_lots row, got %d", count)
	}

	// Verify lot details
	var amountSat int64
	var priceUSD float64
	d.db.QueryRow("SELECT amount_sat, price_usd FROM btc_lots WHERE external_id = 'tx-001'").Scan(&amountSat, &priceUSD)
	if amountSat != 100000 {
		t.Errorf("expected 100000 sats, got %d", amountSat)
	}
	if priceUSD != 67.00 {
		t.Errorf("expected 67.00 USD, got %f", priceUSD)
	}
}

func TestImportStrikeCSV_Dedup(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testStrikeRows()

	// First import
	_, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("first import error: %v", err)
	}

	// Second import — all should be duplicates
	summary, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("second import error: %v", err)
	}

	if summary.Duplicates != 3 {
		t.Errorf("expected 3 duplicates on re-import, got %d", summary.Duplicates)
	}
	if summary.NewPurchases != 0 {
		t.Errorf("expected 0 new purchases on re-import, got %d", summary.NewPurchases)
	}

	// Verify btc_lots still has only 1 row
	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'strike'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 btc_lots row after re-import, got %d", count)
	}
}

func TestImportStrikeCSV_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	summary, err := d.ImportStrikeCSV(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Total != 0 {
		t.Errorf("expected total 0, got %d", summary.Total)
	}
}

func TestImportStrikeCSV_NonPurchaseNoLot(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.StrikeRow{
		{TransactionID: "tx-w1", Date: time.Now(), Type: "Withdrawal", AmountSat: 50000, AmountBTC: 0.0005, AmountUSD: 33.50, Status: "Completed"},
		{TransactionID: "tx-d1", Date: time.Now(), Type: "Deposit", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 0, Status: "Completed"},
	}

	summary, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 0 {
		t.Errorf("expected 0 purchases for non-purchase types, got %d", summary.NewPurchases)
	}

	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 btc_lots, got %d", count)
	}
}

func TestImportStrikeCSV_BuyType(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.StrikeRow{
		{TransactionID: "tx-buy1", Date: time.Now(), Type: "Buy", AmountSat: 50000000, AmountBTC: 0.5, AmountUSD: 33500.00, Status: "Completed"},
	}

	summary, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 1 {
		t.Errorf("expected 1 purchase for Buy type, got %d", summary.NewPurchases)
	}
}
