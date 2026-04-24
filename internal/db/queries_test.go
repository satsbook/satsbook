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
		{TransactionID: "tx-001", Date: now, Type: "Purchase", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 67.00, CostBasisUSD: 67.00, FeeUSD: 0.50, RawLine: "tx-001,Purchase,0.001,67.00"},
		{TransactionID: "tx-002", Date: now, Type: "Withdrawal", AmountSat: 50000, AmountBTC: 0.0005, AmountUSD: 33.50, FeeUSD: 1.00, RawLine: "tx-002,Withdrawal,0.0005,33.50"},
		{TransactionID: "tx-003", Date: now, Type: "Receive", AmountSat: 10000, AmountBTC: 0.0001, RawLine: "tx-003,Receive,0.0001"},
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
	d.db.QueryRow("SELECT amount_sat, price_usd FROM btc_lots WHERE source = 'strike' LIMIT 1").Scan(&amountSat, &priceUSD)
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
		{TransactionID: "tx-w1", Date: time.Now(), Type: "Withdrawal", AmountSat: 50000, AmountBTC: 0.0005, AmountUSD: 33.50, RawLine: "tx-w1,Withdrawal,0.0005,33.50"},
		{TransactionID: "tx-d1", Date: time.Now(), Type: "Receive", AmountSat: 100000, AmountBTC: 0.001, RawLine: "tx-d1,Receive,0.001"},
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
		{TransactionID: "tx-buy1", Date: time.Now(), Type: "Buy", AmountSat: 50000000, AmountBTC: 0.5, AmountUSD: 33500.00, CostBasisUSD: 33500.00, RawLine: "tx-buy1,Buy,0.5,33500.00"},
	}

	summary, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 1 {
		t.Errorf("expected 1 purchase for Buy type, got %d", summary.NewPurchases)
	}
}

func TestExchangeBalance(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	// Empty — should return 0
	bal, err := d.ExchangeBalance(context.Background(), "strike")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bal != 0 {
		t.Errorf("expected 0 balance before import, got %d", bal)
	}

	// Import rows with mixed amounts
	rows := testStrikeRows() // Purchase +0.001, Withdrawal +0.0005 BTC, Receive +0.0001
	_, err = d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	bal, err = d.ExchangeBalance(context.Background(), "strike")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only rows with non-zero AmountBTC count: 0.001 + 0.0005 + 0.0001 = 0.0016 BTC = 160000 sats
	if bal != 160000 {
		t.Errorf("expected 160000 sats, got %d", bal)
	}

	// Different source should return 0
	bal, err = d.ExchangeBalance(context.Background(), "river")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bal != 0 {
		t.Errorf("expected 0 for river, got %d", bal)
	}
}

func TestImportStrikeCSV_SameReferenceID(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	// Same reference ID, different transaction types (real Strike behavior)
	rows := []exchange.StrikeRow{
		{TransactionID: "76333fda-5647-42a9-a463-5ef618b4fa7b", Date: now, Type: "Withdrawal", AmountSat: 0, AmountBTC: 0, AmountUSD: -475.24, RawLine: "76333fda,Withdrawal,-475.24,,,"},
		{TransactionID: "76333fda-5647-42a9-a463-5ef618b4fa7b", Date: now, Type: "Sale", AmountSat: -523038, AmountBTC: -0.00523038, AmountUSD: 475.24, BTCPrice: 91584.17, RawLine: "76333fda,Sale,475.24,-0.00523038,,91584.17"},
	}

	summary, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both rows should be imported (not deduped)
	if summary.Duplicates != 0 {
		t.Errorf("expected 0 duplicates, got %d", summary.Duplicates)
	}

	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM exchange_imports WHERE source = 'strike'").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 exchange_imports rows, got %d", count)
	}

	// Balance should only reflect the Sale (BTC out), not the USD-only Withdrawal
	bal, err := d.ExchangeBalance(context.Background(), "strike")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bal != -523038 {
		t.Errorf("expected -523038 sats (BTC sold), got %d", bal)
	}
}

func TestExchangeBalance_ExcludesUSDOnly(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	rows := []exchange.StrikeRow{
		{TransactionID: "tx-p1", Date: now, Type: "Purchase", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 67.00, CostBasisUSD: 67.00, RawLine: "tx-p1,Purchase,0.001,67.00"},
		{TransactionID: "tx-w1", Date: now, Type: "Withdrawal", AmountSat: 0, AmountBTC: 0, AmountUSD: -500.00, RawLine: "tx-w1,Withdrawal,0,-500.00"},  // USD-only, no BTC
		{TransactionID: "tx-d1", Date: now, Type: "Deposit", AmountSat: 0, AmountBTC: 0, AmountUSD: 1000.00, RawLine: "tx-d1,Deposit,0,1000.00"},        // USD deposit, no BTC
	}

	_, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	bal, err := d.ExchangeBalance(context.Background(), "strike")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the Purchase has BTC: 100000 sats
	if bal != 100000 {
		t.Errorf("expected 100000 sats (USD-only rows excluded), got %d", bal)
	}
}

func TestExchangeSummary_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	result, err := d.ExchangeSummary(context.Background(), "strike", time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PurchasedSats != 0 || result.SoldSats != 0 || result.TotalCostBasisUSD != 0 {
		t.Errorf("expected all zeros for empty DB, got %+v", result)
	}
}

func TestExchangeSummary_MixedTypes(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	rows := []exchange.StrikeRow{
		{TransactionID: "p1", Date: now, Type: "Purchase", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 67.00, CostBasisUSD: 67.00, FeeUSD: 0.50, RawLine: "p1,Purchase,0.001,67.00,0.50"},
		{TransactionID: "r1", Date: now, Type: "Receive", AmountSat: 50000, AmountBTC: 0.0005, RawLine: "r1,Receive,0.0005"},
		{TransactionID: "s1", Date: now, Type: "Sale", AmountSat: -30000, AmountBTC: -0.0003, AmountUSD: 20.10, FeeUSD: 0.25, RawLine: "s1,Sale,-0.0003,20.10,0.25"},
		{TransactionID: "w1", Date: now, Type: "Send", AmountSat: -20000, AmountBTC: -0.0002, RawLine: "w1,Send,-0.0002"},
		{TransactionID: "d1", Date: now, Type: "Deposit", AmountSat: 0, AmountBTC: 0, AmountUSD: 500.00, RawLine: "d1,Deposit,0,500.00"},
	}

	_, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	result, err := d.ExchangeSummary(context.Background(), "strike", time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.PurchasedSats != 100000 {
		t.Errorf("expected 100000 purchased sats, got %d", result.PurchasedSats)
	}
	if result.ReceivedSats != 50000 {
		t.Errorf("expected 50000 received sats, got %d", result.ReceivedSats)
	}
	if result.SoldSats != 30000 {
		t.Errorf("expected 30000 sold sats, got %d", result.SoldSats)
	}
	if result.SentSats != 20000 {
		t.Errorf("expected 20000 sent sats, got %d", result.SentSats)
	}
	if result.TotalCostBasisUSD != 67.00 {
		t.Errorf("expected 67.00 cost basis, got %f", result.TotalCostBasisUSD)
	}
	if result.TotalSaleProceedsUSD != 20.10 {
		t.Errorf("expected 20.10 sale proceeds, got %f", result.TotalSaleProceedsUSD)
	}
	// Fees: 0.50 + 0.25 = 0.75
	if result.FeesPaidUSD != 0.75 {
		t.Errorf("expected 0.75 fees, got %f", result.FeesPaidUSD)
	}
}

func TestExchangeSummary_DateFiltering(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	old := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	rows := []exchange.StrikeRow{
		{TransactionID: "old-p", Date: old, Type: "Purchase", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 40.00, CostBasisUSD: 40.00, RawLine: "old-p,Purchase,0.001,40.00"},
		{TransactionID: "new-p", Date: recent, Type: "Purchase", AmountSat: 200000, AmountBTC: 0.002, AmountUSD: 150.00, CostBasisUSD: 150.00, RawLine: "new-p,Purchase,0.002,150.00"},
	}

	_, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	// All time
	all, err := d.ExchangeSummary(context.Background(), "strike", time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if all.PurchasedSats != 300000 {
		t.Errorf("all time: expected 300000, got %d", all.PurchasedSats)
	}

	// Since 2026
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	filtered, err := d.ExchangeSummary(context.Background(), "strike", since)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filtered.PurchasedSats != 200000 {
		t.Errorf("filtered: expected 200000, got %d", filtered.PurchasedSats)
	}
	if filtered.TotalCostBasisUSD != 150.00 {
		t.Errorf("filtered cost basis: expected 150.00, got %f", filtered.TotalCostBasisUSD)
	}
}

// --- River CSV import tests ---

func testRiverRows() []exchange.RiverRow {
	return []exchange.RiverRow{
		{
			Date:         time.Date(2024, 9, 26, 19, 59, 41, 0, time.UTC),
			Type:         "buy",
			AmountBTC:    0.00006093,
			AmountSat:    6093,
			AmountUSD:    3.96,
			FeeUSD:       0.04,
			CostBasisUSD: 3.96,
			RawLine:      "2024-09-26 19:59:41,3.96,USD,0.00006093,BTC,0.04,USD,Buy",
		},
		{
			Date:         time.Date(2024, 10, 3, 20, 0, 2, 0, time.UTC),
			Type:         "buy",
			AmountBTC:    0.00006536,
			AmountSat:    6536,
			AmountUSD:    4.00,
			CostBasisUSD: 4.00,
			RawLine:      "2024-10-03 20:00:02,4.00,USD,0.00006536,BTC,,,Buy",
		},
		{
			Date:      time.Date(2024, 11, 27, 0, 36, 42, 0, time.UTC),
			Type:      "send",
			AmountBTC: -0.00482484,
			AmountSat: -482484,
			FeeBTC:    0.00000242,
			RawLine:   "2024-11-27 00:36:42,0.00482484,BTC,,,0.00000242,BTC,",
		},
	}
}

func TestImportRiverCSV_Basic(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testRiverRows()
	summary, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Total != 3 {
		t.Errorf("expected total 3, got %d", summary.Total)
	}
	if summary.NewPurchases != 2 {
		t.Errorf("expected 2 new purchases, got %d", summary.NewPurchases)
	}
	if summary.Duplicates != 0 {
		t.Errorf("expected 0 duplicates, got %d", summary.Duplicates)
	}

	// Verify exchange_imports has 3 rows
	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM exchange_imports WHERE source = 'river'").Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 exchange_imports rows, got %d", count)
	}

	// Verify btc_lots has 2 rows (only purchases)
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'river'").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 btc_lots rows, got %d", count)
	}
}

func TestImportRiverCSV_Dedup(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testRiverRows()

	// First import
	_, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("first import error: %v", err)
	}

	// Second import — all should be duplicates
	summary, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("second import error: %v", err)
	}
	if summary.Duplicates != 3 {
		t.Errorf("expected 3 duplicates, got %d", summary.Duplicates)
	}
	if summary.NewPurchases != 0 {
		t.Errorf("expected 0 new purchases, got %d", summary.NewPurchases)
	}
}

func TestImportRiverCSV_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	summary, err := d.ImportRiverCSV(context.Background(), []exchange.RiverRow{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Total != 0 {
		t.Errorf("expected total 0, got %d", summary.Total)
	}
}

func TestImportRiverCSV_SendNoLot(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.RiverRow{
		{
			Date:      time.Date(2024, 11, 27, 0, 36, 42, 0, time.UTC),
			Type:      "send",
			AmountBTC: -0.00482484,
			AmountSat: -482484,
			RawLine:   "2024-11-27 00:36:42,0.00482484,BTC,,,,,Withdrawal",
		},
	}

	summary, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 0 {
		t.Errorf("expected 0 new purchases for send, got %d", summary.NewPurchases)
	}

	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'river'").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 btc_lots for send, got %d", count)
	}
}

func TestRiverExchangeBalance(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testRiverRows()
	_, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	bal, err := d.ExchangeBalance(context.Background(), "river")
	if err != nil {
		t.Fatalf("balance error: %v", err)
	}

	// 6093 + 6536 - 482484 = -469855
	expected := int64(-469855)
	if bal != expected {
		t.Errorf("expected balance %d sats, got %d", expected, bal)
	}
}

func TestRiverExchangeSummary(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testRiverRows()
	_, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	summary, err := d.ExchangeSummary(context.Background(), "river", time.Time{})
	if err != nil {
		t.Fatalf("summary error: %v", err)
	}

	if summary.PurchasedSats != 12629 {
		t.Errorf("expected purchased 12629 sats, got %d", summary.PurchasedSats)
	}
	if summary.SentSats != 482484 {
		t.Errorf("expected sent 482484 sats, got %d", summary.SentSats)
	}
	if summary.TotalCostBasisUSD != 7.96 {
		t.Errorf("expected cost basis 7.96, got %f", summary.TotalCostBasisUSD)
	}
}

// --- Coinbase CSV DB tests ---

func testCoinbaseRows() []exchange.CoinbaseRow {
	return []exchange.CoinbaseRow{
		{
			Date:         time.Date(2023, 8, 1, 19, 50, 7, 0, time.UTC),
			Type:         "buy",
			AmountBTC:    0.001,
			AmountSat:    100000,
			AmountUSD:    29.24,
			FeeUSD:       1.00,
			CostBasisUSD: 30.24,
			RawLine:      "abc123,2023-08-01 19:50:07 UTC,Buy,BTC,0.001,USD,$29244.04,$29.24,$30.24,$1.00,Bought BTC",
		},
		{
			Date:         time.Date(2023, 8, 1, 19, 50, 7, 0, time.UTC),
			Type:         "buy",
			AmountBTC:    0.002,
			AmountSat:    200000,
			AmountUSD:    58.48,
			FeeUSD:       2.00,
			CostBasisUSD: 60.48,
			RawLine:      "abc456,2023-08-01 19:50:07 UTC,Buy,BTC,0.002,USD,$29244.04,$58.48,$60.48,$2.00,Bought BTC",
		},
		{
			Date:      time.Date(2023, 8, 1, 19, 50, 7, 0, time.UTC),
			Type:      "send",
			AmountBTC: -0.006132,
			AmountSat: -613200,
			RawLine:   "def789,2023-08-01 19:50:07 UTC,Send,BTC,-0.006132,USD,$29244.04,-$179.32,-$179.32,$0.00,Sent BTC",
		},
	}
}

func TestImportCoinbaseCSV_Basic(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testCoinbaseRows()
	summary, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Total != 3 {
		t.Errorf("expected total 3, got %d", summary.Total)
	}
	if summary.NewPurchases != 2 {
		t.Errorf("expected 2 new purchases, got %d", summary.NewPurchases)
	}
	if summary.Duplicates != 0 {
		t.Errorf("expected 0 duplicates, got %d", summary.Duplicates)
	}

	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM exchange_imports WHERE source = 'coinbase'").Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 exchange_imports rows, got %d", count)
	}

	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'coinbase'").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 btc_lots rows, got %d", count)
	}
}

func TestImportCoinbaseCSV_Dedup(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testCoinbaseRows()
	_, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("first import error: %v", err)
	}

	summary, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("second import error: %v", err)
	}
	if summary.Duplicates != 3 {
		t.Errorf("expected 3 duplicates, got %d", summary.Duplicates)
	}
	if summary.NewPurchases != 0 {
		t.Errorf("expected 0 new purchases, got %d", summary.NewPurchases)
	}
}

func TestImportCoinbaseCSV_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	summary, err := d.ImportCoinbaseCSV(context.Background(), []exchange.CoinbaseRow{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Total != 0 {
		t.Errorf("expected total 0, got %d", summary.Total)
	}
}

func TestImportCoinbaseCSV_SendNoLot(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.CoinbaseRow{
		{
			Date:      time.Date(2023, 8, 1, 19, 50, 7, 0, time.UTC),
			Type:      "send",
			AmountBTC: -0.006132,
			AmountSat: -613200,
			RawLine:   "def789,2023-08-01 19:50:07 UTC,Send,BTC,-0.006132,USD,$29244.04,-$179.32,-$179.32,$0.00,Sent BTC",
		},
	}

	summary, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 0 {
		t.Errorf("expected 0 new purchases for send, got %d", summary.NewPurchases)
	}

	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'coinbase'").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 btc_lots for send, got %d", count)
	}
}

func TestCoinbaseExchangeBalance(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testCoinbaseRows()
	_, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	bal, err := d.ExchangeBalance(context.Background(), "coinbase")
	if err != nil {
		t.Fatalf("balance error: %v", err)
	}

	// 100000 + 200000 - 613200 = -313200
	if bal != -313200 {
		t.Errorf("expected balance -313200, got %d", bal)
	}
}

func TestCoinbaseExchangeSummary(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testCoinbaseRows()
	_, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	summary, err := d.ExchangeSummary(context.Background(), "coinbase", time.Time{})
	if err != nil {
		t.Fatalf("summary error: %v", err)
	}

	if summary.PurchasedSats != 300000 {
		t.Errorf("expected purchased 300000 sats, got %d", summary.PurchasedSats)
	}
	if summary.SentSats != 613200 {
		t.Errorf("expected sent 613200 sats, got %d", summary.SentSats)
	}
	if summary.TotalCostBasisUSD != 90.72 {
		t.Errorf("expected cost basis 90.72, got %f", summary.TotalCostBasisUSD)
	}
	if summary.FeesPaidUSD != 3.00 {
		t.Errorf("expected fees 3.00, got %f", summary.FeesPaidUSD)
	}
}

func TestPortfolioPosition_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	result, err := d.PortfolioPosition(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ExchangeNetSats != 0 || result.RoutingFeesSats != 0 || result.PurchasedSats != 0 {
		t.Errorf("expected all zeros for empty DB, got %+v", result)
	}
	if len(result.BySource) != 0 {
		t.Errorf("expected empty BySource, got %d entries", len(result.BySource))
	}
}

func TestPortfolioPosition_AggregatesAcrossSources(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()

	// Strike: +0.001 BTC purchase
	_, err := d.ImportStrikeCSV(context.Background(), []exchange.StrikeRow{
		{TransactionID: "s1", Date: now, Type: "Purchase", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 67, CostBasisUSD: 67, RawLine: "s1,Purchase,0.001,67"},
	})
	if err != nil {
		t.Fatalf("strike import: %v", err)
	}

	// River: +0.002 BTC buy
	_, err = d.ImportRiverCSV(context.Background(), []exchange.RiverRow{
		{Date: now, Type: "buy", AmountSat: 200000, AmountBTC: 0.002, AmountUSD: 134, CostBasisUSD: 134, RawLine: "r1,buy,0.002,134"},
	})
	if err != nil {
		t.Fatalf("river import: %v", err)
	}

	// Coinbase: +0.003 buy and -0.0005 sale
	_, err = d.ImportCoinbaseCSV(context.Background(), []exchange.CoinbaseRow{
		{Date: now, Type: "buy", AmountSat: 300000, AmountBTC: 0.003, AmountUSD: 200, CostBasisUSD: 200, RawLine: "c1,buy,0.003,200"},
		{Date: now, Type: "sale", AmountSat: -50000, AmountBTC: -0.0005, AmountUSD: 35, RawLine: "c2,sale,-0.0005,35"},
	})
	if err != nil {
		t.Fatalf("coinbase import: %v", err)
	}

	// Routing fees: 5_000_000 msat = 5000 sats
	seedForwardingEvents(t, d, []ForwardingEvent{
		{Timestamp: now.Add(-1 * time.Hour), ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 1000000, AmtOutMsat: 995000, FeeMsat: 5_000_000},
	})

	result, err := d.PortfolioPosition(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Strike: 100000, River: 200000, Coinbase: 300000 - 50000 = 250000
	// Net exchange: 550000 sats
	if result.ExchangeNetSats != 550000 {
		t.Errorf("expected ExchangeNetSats 550000, got %d", result.ExchangeNetSats)
	}
	if result.BySource["strike"].NetSats != 100000 {
		t.Errorf("expected strike net 100000, got %d", result.BySource["strike"].NetSats)
	}
	if result.BySource["river"].NetSats != 200000 {
		t.Errorf("expected river net 200000, got %d", result.BySource["river"].NetSats)
	}
	if result.BySource["coinbase"].NetSats != 250000 {
		t.Errorf("expected coinbase net 250000, got %d", result.BySource["coinbase"].NetSats)
	}

	// Purchased: strike 100k + river 200k + coinbase 300k = 600k
	if result.PurchasedSats != 600000 {
		t.Errorf("expected purchased 600000, got %d", result.PurchasedSats)
	}

	// Routing fees: 5_000_000 msat → 5000 sats
	if result.RoutingFeesSats != 5000 {
		t.Errorf("expected routing fees 5000 sats, got %d", result.RoutingFeesSats)
	}
	if result.RoutedCount != 1 {
		t.Errorf("expected routed count 1, got %d", result.RoutedCount)
	}
}

func TestPortfolioPosition_SinceFiltering(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	old := now.Add(-365 * 24 * time.Hour)
	recent := now.Add(-1 * time.Hour)

	// Old purchase + recent purchase
	_, err := d.ImportStrikeCSV(context.Background(), []exchange.StrikeRow{
		{TransactionID: "old", Date: old, Type: "Purchase", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 50, CostBasisUSD: 50, RawLine: "old,Purchase,0.001,50"},
		{TransactionID: "new", Date: recent, Type: "Purchase", AmountSat: 200000, AmountBTC: 0.002, AmountUSD: 130, CostBasisUSD: 130, RawLine: "new,Purchase,0.002,130"},
	})
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	// Old fee + recent fee
	seedForwardingEvents(t, d, []ForwardingEvent{
		{Timestamp: old, ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 1000, AmtOutMsat: 900, FeeMsat: 100_000},
		{Timestamp: recent, ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 1000, AmtOutMsat: 900, FeeMsat: 200_000},
	})

	// All time
	all, err := d.PortfolioPosition(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("all-time err: %v", err)
	}
	if all.ExchangeNetSats != 300000 {
		t.Errorf("all-time exchange: expected 300000, got %d", all.ExchangeNetSats)
	}
	if all.RoutingFeesSats != 300 {
		t.Errorf("all-time fees: expected 300 sats, got %d", all.RoutingFeesSats)
	}

	// Last 24h only
	since := now.Add(-24 * time.Hour)
	recentOnly, err := d.PortfolioPosition(context.Background(), since)
	if err != nil {
		t.Fatalf("since err: %v", err)
	}
	if recentOnly.ExchangeNetSats != 200000 {
		t.Errorf("recent exchange: expected 200000, got %d", recentOnly.ExchangeNetSats)
	}
	if recentOnly.RoutingFeesSats != 200 {
		t.Errorf("recent fees: expected 200 sats, got %d", recentOnly.RoutingFeesSats)
	}
	if recentOnly.PurchasedSats != 200000 {
		t.Errorf("recent purchased: expected 200000, got %d", recentOnly.PurchasedSats)
	}
}

// --- ClearExchangeSource tests ---

func TestClearExchangeSource_InvalidSource(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	if _, err := d.ClearExchangeSource(context.Background(), "kraken"); err == nil {
		t.Fatal("expected error for invalid source, got nil")
	}
	if _, err := d.ClearExchangeSource(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty source, got nil")
	}
}

func TestClearExchangeSource_EmptyIsNoop(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	res, err := d.ClearExchangeSource(context.Background(), "strike")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ImportsRemoved != 0 || res.LotsRemoved != 0 {
		t.Errorf("expected 0/0, got %d/%d", res.ImportsRemoved, res.LotsRemoved)
	}
}

func TestClearExchangeSource_IsolatesVendors(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	// Seed strike (3 imports, 1 lot) and river (3 imports, 2 lots).
	if _, err := d.ImportStrikeCSV(context.Background(), testStrikeRows()); err != nil {
		t.Fatalf("strike import: %v", err)
	}
	if _, err := d.ImportRiverCSV(context.Background(), testRiverRows()); err != nil {
		t.Fatalf("river import: %v", err)
	}

	res, err := d.ClearExchangeSource(context.Background(), "strike")
	if err != nil {
		t.Fatalf("clear strike: %v", err)
	}
	if res.ImportsRemoved != 3 {
		t.Errorf("expected 3 strike imports removed, got %d", res.ImportsRemoved)
	}
	if res.LotsRemoved != 1 {
		t.Errorf("expected 1 strike lot removed, got %d", res.LotsRemoved)
	}

	// Strike fully gone.
	var strikeImports, strikeLots int
	d.db.QueryRow("SELECT COUNT(*) FROM exchange_imports WHERE source = 'strike'").Scan(&strikeImports)
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'strike'").Scan(&strikeLots)
	if strikeImports != 0 || strikeLots != 0 {
		t.Errorf("strike not fully cleared: imports=%d lots=%d", strikeImports, strikeLots)
	}

	// River untouched.
	var riverImports, riverLots int
	d.db.QueryRow("SELECT COUNT(*) FROM exchange_imports WHERE source = 'river'").Scan(&riverImports)
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'river'").Scan(&riverLots)
	if riverImports != 3 {
		t.Errorf("river imports should be untouched, got %d", riverImports)
	}
	if riverLots != 2 {
		t.Errorf("river lots should be untouched, got %d", riverLots)
	}
}

// --- Watched Wallets tests ---

func TestAddWallet(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	id, err := d.AddWallet(context.Background(), "Cold Storage", "xpub", "xpub6CDEark...", "bip84")
	if err != nil {
		t.Fatalf("AddWallet: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive ID, got %d", id)
	}
}

func TestAddWallet_DuplicateRejected(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()
	_, err := d.AddWallet(ctx, "Wallet A", "xpub", "xpub123", "bip84")
	if err != nil {
		t.Fatalf("first AddWallet: %v", err)
	}

	_, err = d.AddWallet(ctx, "Wallet B", "xpub", "xpub123", "bip84")
	if err == nil {
		t.Fatal("expected error on duplicate xpub")
	}
}

func TestListWallets_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	wallets, err := d.ListWallets(context.Background())
	if err != nil {
		t.Fatalf("ListWallets: %v", err)
	}
	if len(wallets) != 0 {
		t.Errorf("expected 0 wallets, got %d", len(wallets))
	}
}

func TestListWallets_OrderedByLabel(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()
	d.AddWallet(ctx, "Zulu", "address", "bc1qz...", "bip84")
	d.AddWallet(ctx, "Alpha", "xpub", "xpub6...", "bip44")

	wallets, err := d.ListWallets(ctx)
	if err != nil {
		t.Fatalf("ListWallets: %v", err)
	}
	if len(wallets) != 2 {
		t.Fatalf("expected 2, got %d", len(wallets))
	}
	if wallets[0].Label != "Alpha" {
		t.Errorf("expected Alpha first, got %s", wallets[0].Label)
	}
}

func TestRemoveWallet(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()
	id, _ := d.AddWallet(ctx, "Test", "address", "bc1q...", "bip84")

	if err := d.RemoveWallet(ctx, id); err != nil {
		t.Fatalf("RemoveWallet: %v", err)
	}

	wallets, _ := d.ListWallets(ctx)
	if len(wallets) != 0 {
		t.Errorf("expected 0 after remove, got %d", len(wallets))
	}
}

func TestRemoveWallet_NotFound(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	err := d.RemoveWallet(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for non-existent wallet")
	}
}

func TestGetWallet(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()
	id, _ := d.AddWallet(ctx, "Coldcard", "xpub", "xpub6CDEark...", "bip84")

	w, err := d.GetWallet(ctx, id)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if w == nil {
		t.Fatal("expected wallet, got nil")
	}
	if w.Label != "Coldcard" {
		t.Errorf("label = %s, want Coldcard", w.Label)
	}
	if w.Type != "xpub" {
		t.Errorf("type = %s, want xpub", w.Type)
	}
	if w.DerivationType != "bip84" {
		t.Errorf("derivation = %s, want bip84", w.DerivationType)
	}
	if w.LastCheckedAt != nil {
		t.Errorf("expected nil LastCheckedAt, got %v", w.LastCheckedAt)
	}
}

func TestGetWallet_NotFound(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	w, err := d.GetWallet(context.Background(), 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil {
		t.Errorf("expected nil, got %+v", w)
	}
}

func TestUpdateWalletBalance(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()
	id, _ := d.AddWallet(ctx, "Test", "address", "bc1q...", "bip84")

	if err := d.UpdateWalletBalance(ctx, id, 500000); err != nil {
		t.Fatalf("UpdateWalletBalance: %v", err)
	}

	w, _ := d.GetWallet(ctx, id)
	if w.BalanceSats != 500000 {
		t.Errorf("balance = %d, want 500000", w.BalanceSats)
	}
	if w.LastCheckedAt == nil {
		t.Error("expected LastCheckedAt to be set")
	}
}

func TestTotalWatchedBalance(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()
	id1, _ := d.AddWallet(ctx, "W1", "address", "bc1q1...", "bip84")
	id2, _ := d.AddWallet(ctx, "W2", "address", "bc1q2...", "bip84")

	d.UpdateWalletBalance(ctx, id1, 100000)
	d.UpdateWalletBalance(ctx, id2, 250000)

	total, err := d.TotalWatchedBalance(ctx)
	if err != nil {
		t.Fatalf("TotalWatchedBalance: %v", err)
	}
	if total != 350000 {
		t.Errorf("total = %d, want 350000", total)
	}
}

func TestTotalWatchedBalance_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	total, err := d.TotalWatchedBalance(context.Background())
	if err != nil {
		t.Fatalf("TotalWatchedBalance: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
}

// --- Swan import tests ---

func testSwanRows() []exchange.SwanRow {
	return []exchange.SwanRow{
		{
			Date:         time.Date(2024, 11, 16, 17, 39, 46, 0, time.UTC),
			Type:         "purchase",
			AmountBTC:    0.00010975,
			AmountSat:    10975,
			AmountUSD:    10.00,
			CostBasisUSD: 10.00,
			RawLine:      "11/16/2024 17:36:30,0.00010975,BTC,10.0000000000000000,USD,0.00,USD,\"\"",
		},
		{
			Date:      time.Date(2024, 12, 26, 12, 6, 43, 0, time.UTC),
			Type:      "deposit",
			AmountBTC: 0.00098195,
			AmountSat: 98195,
			RawLine:   "deposit,2024-12-26 12:06:43+00,UTC,settled,,,,,0.00098195,BTC,,Custodial Transfer,,",
		},
		{
			Date:          time.Date(2024, 11, 27, 4, 41, 25, 0, time.UTC),
			Type:          "withdrawal",
			TransactionID: "64d3be26d327c4ca",
			AmountBTC:     -0.00074465,
			AmountSat:     -74465,
			RawLine:       "2024-11-27 00:30:25+00,UTC,64d3be26d327c4ca,2024-11-27 04:41:25+00,,settled,0.00074465,f,108.189.12.201",
		},
	}
}

func TestImportSwanCSV_Basic(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testSwanRows()
	summary, err := d.ImportSwanCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Total != 3 {
		t.Errorf("expected total 3, got %d", summary.Total)
	}
	if summary.NewPurchases != 1 {
		t.Errorf("expected 1 new purchase, got %d", summary.NewPurchases)
	}
	if summary.Duplicates != 0 {
		t.Errorf("expected 0 duplicates, got %d", summary.Duplicates)
	}

	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM exchange_imports WHERE source = 'swan'").Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 exchange_imports rows, got %d", count)
	}

	// btc_lots: 1 purchase + 1 deposit + 1 withdrawal = 3 lots
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'swan'").Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 btc_lots rows, got %d", count)
	}

	// Net sats: 10975 + 98195 - 74465 = 34705
	var totalSats int64
	d.db.QueryRow("SELECT SUM(amount_sat) FROM btc_lots WHERE source = 'swan'").Scan(&totalSats)
	if totalSats != 34705 {
		t.Errorf("expected net 34705 sats, got %d", totalSats)
	}

	// Only the purchase lot should have a price
	var priceUSD float64
	d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'swan' AND amount_sat > 0 AND price_usd > 0").Scan(&priceUSD)
	if priceUSD != 10.00 {
		t.Errorf("expected purchase cost basis 10.00, got %f", priceUSD)
	}
}

func TestImportSwanCSV_Dedup(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := testSwanRows()
	_, err := d.ImportSwanCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("first import error: %v", err)
	}

	summary, err := d.ImportSwanCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("second import error: %v", err)
	}
	if summary.Duplicates != 3 {
		t.Errorf("expected 3 duplicates, got %d", summary.Duplicates)
	}
	if summary.NewPurchases != 0 {
		t.Errorf("expected 0 new purchases, got %d", summary.NewPurchases)
	}
}

func TestImportSwanCSV_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	summary, err := d.ImportSwanCSV(context.Background(), []exchange.SwanRow{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Total != 0 {
		t.Errorf("expected total 0, got %d", summary.Total)
	}
}
