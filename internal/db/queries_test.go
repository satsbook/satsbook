package db

import (
	"context"
	"fmt"
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

func TestChannelStats_ROI(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	openedAt := time.Now().UTC().Add(-30 * 24 * time.Hour)
	// Capacity 1_000_000, open tx spent 1_001_500 sats (open fee = 1500 sats)
	seedChannels(t, d, []Channel{
		{
			ChanID: 100, RemotePubKey: "nodeA",
			ChannelPoint: "abc123:0",
			Capacity:     1_000_000,
			LocalBalance: 500_000, RemoteBalance: 500_000, Active: true,
		},
	})
	// Seed the funding onchain tx: amount_sat = -1_001_500 (outgoing)
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertOnchainTxns([]OnchainTx{
			{TxHash: "abc123", AmountSat: -1_001_500, NumConfirmations: 100, Timestamp: openedAt},
		})
	})
	if err != nil {
		t.Fatalf("seed onchain tx: %v", err)
	}
	// Seed 3000 msat (3 sats) of routing fees
	seedForwardingEvents(t, d, []ForwardingEvent{
		{Timestamp: openedAt.Add(time.Hour), ChanIDIn: 100, ChanIDOut: 200, AmtInMsat: 3000, AmtOutMsat: 2000, FeeMsat: 3_000_000},
	})

	stats, err := d.ChannelStats(context.Background())
	if err != nil {
		t.Fatalf("ChannelStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(stats))
	}
	s := stats[0]

	if s.OpenFeeSats != 1500 {
		t.Errorf("OpenFeeSats: expected 1500, got %d", s.OpenFeeSats)
	}
	if s.CapacitySats != 1_000_000 {
		t.Errorf("CapacitySats: expected 1000000, got %d", s.CapacitySats)
	}
	if s.DataPartial {
		t.Errorf("expected DataPartial=false for channel with matching onchain tx")
	}
	if s.DaysOpen < 29 || s.DaysOpen > 31 {
		t.Errorf("DaysOpen: expected ~30, got %d", s.DaysOpen)
	}
	// 3000 sats earned / 1500 sats open fee = 200%
	if s.ROIPct < 199 || s.ROIPct > 201 {
		t.Errorf("ROIPct: expected ~200, got %.2f", s.ROIPct)
	}
	// ROI > 100%, so break-even already passed
	if s.BreakEvenDays != 0 {
		t.Errorf("BreakEvenDays: expected 0 (already recovered), got %d", s.BreakEvenDays)
	}
}

func TestChannelStats_ROI_Partial(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	// Channel with no channel_point — predates tracking
	seedChannels(t, d, []Channel{
		{ChanID: 200, RemotePubKey: "nodeB", Capacity: 500_000,
			LocalBalance: 250_000, RemoteBalance: 250_000, Active: true},
	})

	stats, err := d.ChannelStats(context.Background())
	if err != nil {
		t.Fatalf("ChannelStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(stats))
	}
	s := stats[0]
	if !s.DataPartial {
		t.Errorf("expected DataPartial=true for channel with no channel_point")
	}
	if s.OpenFeeSats != 0 {
		t.Errorf("expected OpenFeeSats=0 for partial channel, got %d", s.OpenFeeSats)
	}
	if s.ROIPct != 0 {
		t.Errorf("expected ROIPct=0 for partial channel, got %.2f", s.ROIPct)
	}
}

func TestChannelStats_ROI_BreakEven(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	openedAt := time.Now().UTC().Add(-10 * 24 * time.Hour)
	// Open fee = 2000 sats, earned = 500 sats over 10 days → avg 50/day → break-even in ~30 more days
	seedChannels(t, d, []Channel{
		{ChanID: 300, RemotePubKey: "nodeC", ChannelPoint: "def456:0",
			Capacity: 1_000_000, LocalBalance: 500_000, RemoteBalance: 500_000, Active: true},
	})
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertOnchainTxns([]OnchainTx{
			{TxHash: "def456", AmountSat: -1_002_000, NumConfirmations: 50, Timestamp: openedAt},
		})
	})
	if err != nil {
		t.Fatalf("seed onchain tx: %v", err)
	}
	seedForwardingEvents(t, d, []ForwardingEvent{
		{Timestamp: openedAt.Add(time.Hour), ChanIDIn: 300, ChanIDOut: 400, AmtInMsat: 1000, AmtOutMsat: 500, FeeMsat: 500_000},
	})

	stats, err := d.ChannelStats(context.Background())
	if err != nil {
		t.Fatalf("ChannelStats: %v", err)
	}
	s := stats[0]
	if s.ROIPct >= 100 {
		t.Errorf("expected ROIPct < 100 (not yet profitable), got %.2f", s.ROIPct)
	}
	if s.BreakEvenDays <= 0 {
		t.Errorf("expected BreakEvenDays > 0, got %d", s.BreakEvenDays)
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

func TestImportStrikeCSV_CostBasisUpdate(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	rows := []exchange.StrikeRow{
		{TransactionID: "tx-100", Date: now, Type: "Purchase", AmountSat: 100000, AmountBTC: 0.001, AmountUSD: 67.00, CostBasisUSD: 67.00, RawLine: "original"},
	}

	s1, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if s1.NewPurchases != 1 {
		t.Fatalf("expected 1 new purchase, got %d", s1.NewPurchases)
	}

	// Re-import same transaction with updated cost basis
	rows[0].CostBasisUSD = 72.50
	rows[0].RawLine = "updated"
	s2, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if s2.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", s2.Updated)
	}
	if s2.NewPurchases != 0 {
		t.Errorf("expected 0 new purchases on update, got %d", s2.NewPurchases)
	}

	// Verify cost basis was updated in btc_lots
	var priceUSD float64
	err = d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'strike' AND external_id = 'tx-100'").Scan(&priceUSD)
	if err != nil {
		t.Fatalf("query btc_lot: %v", err)
	}
	if priceUSD != 72.50 {
		t.Errorf("expected price_usd 72.50, got %.2f", priceUSD)
	}

	// Verify only 1 lot exists (not duplicated)
	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'strike'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 btc_lot, got %d", count)
	}
}

func TestImportRiverCSV_CostBasisUpdate(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	rows := []exchange.RiverRow{
		{Date: now, Type: "buy", AmountBTC: 0.002, AmountSat: 200000, AmountUSD: 134.00, CostBasisUSD: 134.00, RawLine: "original"},
	}

	_, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	// Re-import with updated cost basis
	rows[0].CostBasisUSD = 140.00
	rows[0].RawLine = "updated"
	s2, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if s2.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", s2.Updated)
	}

	// Verify updated price
	externalID := fmt.Sprintf("%s|%s|%.8f", now.Format("2006-01-02T15:04:05"), "buy", 0.002)
	var priceUSD float64
	err = d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'river' AND external_id = ?", externalID).Scan(&priceUSD)
	if err != nil {
		t.Fatalf("query btc_lot: %v", err)
	}
	if priceUSD != 140.00 {
		t.Errorf("expected price_usd 140.00, got %.2f", priceUSD)
	}
}

func TestImportCoinbaseCSV_CostBasisUpdate(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	rows := []exchange.CoinbaseRow{
		{Date: now, Type: "buy", AmountBTC: 0.003, AmountSat: 300000, AmountUSD: 200.00, CostBasisUSD: 200.00, RawLine: "original"},
	}

	_, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	rows[0].CostBasisUSD = 210.00
	rows[0].RawLine = "updated"
	s2, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if s2.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", s2.Updated)
	}

	externalID := fmt.Sprintf("%s|%s|%.8f", now.Format("2006-01-02T15:04:05"), "buy", 0.003)
	var priceUSD float64
	err = d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'coinbase' AND external_id = ?", externalID).Scan(&priceUSD)
	if err != nil {
		t.Fatalf("query btc_lot: %v", err)
	}
	if priceUSD != 210.00 {
		t.Errorf("expected price_usd 210.00, got %.2f", priceUSD)
	}
}

func TestImportSwanCSV_CostBasisUpdate(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	rows := []exchange.SwanRow{
		{TransactionID: "swan-100", Date: now, Type: "purchase", AmountBTC: 0.004, AmountSat: 400000, AmountUSD: 268.00, CostBasisUSD: 268.00, RawLine: "original"},
	}

	_, err := d.ImportSwanCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	rows[0].CostBasisUSD = 280.00
	rows[0].RawLine = "updated"
	s2, err := d.ImportSwanCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if s2.Updated != 1 {
		t.Errorf("expected 1 updated, got %d", s2.Updated)
	}

	var priceUSD float64
	err = d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'swan' AND external_id = 'swan-100'").Scan(&priceUSD)
	if err != nil {
		t.Fatalf("query btc_lot: %v", err)
	}
	if priceUSD != 280.00 {
		t.Errorf("expected price_usd 280.00, got %.2f", priceUSD)
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

func TestImportSwanCSV_PurchaseFallbackToAmountUSD(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.SwanRow{
		{
			Date:         time.Date(2024, 11, 16, 17, 39, 46, 0, time.UTC),
			Type:         "purchase",
			AmountBTC:    0.001,
			AmountSat:    100000,
			AmountUSD:    95.00, // fallback when CostBasisUSD is 0
			CostBasisUSD: 0,
			RawLine:      "swan-fallback-1",
		},
	}

	summary, err := d.ImportSwanCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 1 {
		t.Errorf("expected 1 purchase, got %d", summary.NewPurchases)
	}

	var priceUSD float64
	d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'swan'").Scan(&priceUSD)
	if priceUSD != 95.00 {
		t.Errorf("expected fallback price 95.00, got %f", priceUSD)
	}
}

// --- Import cost basis fallback tests (covers AmountUSD fallback path) ---

func TestImportStrikeCSV_CostBasisFallbackToAmountUSD(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.StrikeRow{
		{
			TransactionID: "tx-fb1",
			Date:          time.Now(),
			Type:          "Purchase",
			AmountSat:     50000,
			AmountBTC:     0.0005,
			AmountUSD:     47.50, // fallback
			CostBasisUSD:  0,     // no cost basis
			RawLine:       "tx-fb1,Purchase,0.0005,47.50,0",
		},
	}

	summary, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 1 {
		t.Errorf("expected 1 purchase, got %d", summary.NewPurchases)
	}

	var priceUSD float64
	d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'strike'").Scan(&priceUSD)
	if priceUSD != 47.50 {
		t.Errorf("expected fallback price 47.50, got %f", priceUSD)
	}
}

func TestImportStrikeCSV_NoCostBasisSkipsLot(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.StrikeRow{
		{
			TransactionID: "tx-skip1",
			Date:          time.Now(),
			Type:          "Purchase",
			AmountSat:     50000,
			AmountBTC:     0.0005,
			AmountUSD:     0, // no cost basis anywhere
			CostBasisUSD:  0,
			RawLine:       "tx-skip1,Purchase,0.0005,0,0",
		},
	}

	summary, err := d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 0 {
		t.Errorf("expected 0 purchases (skipped), got %d", summary.NewPurchases)
	}

	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'strike'").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 lots (skipped), got %d", count)
	}
}

func TestImportRiverCSV_CostBasisFallbackToAmountUSD(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.RiverRow{
		{
			Date:         time.Date(2024, 9, 26, 19, 59, 41, 0, time.UTC),
			Type:         "buy",
			AmountBTC:    0.001,
			AmountSat:    100000,
			AmountUSD:    65.00,
			CostBasisUSD: 0, // no cost basis
			RawLine:      "river-fallback-1",
		},
	}

	summary, err := d.ImportRiverCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 1 {
		t.Errorf("expected 1 purchase, got %d", summary.NewPurchases)
	}

	var priceUSD float64
	d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'river'").Scan(&priceUSD)
	if priceUSD != 65.00 {
		t.Errorf("expected fallback price 65.00, got %f", priceUSD)
	}
}

func TestImportCoinbaseCSV_CostBasisFallbackToAmountUSD(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	rows := []exchange.CoinbaseRow{
		{
			Date:         time.Date(2023, 8, 1, 19, 50, 7, 0, time.UTC),
			Type:         "buy",
			AmountBTC:    0.001,
			AmountSat:    100000,
			AmountUSD:    30.00,
			CostBasisUSD: 0, // no cost basis
			RawLine:      "coinbase-fallback-1",
		},
	}

	summary, err := d.ImportCoinbaseCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.NewPurchases != 1 {
		t.Errorf("expected 1 purchase, got %d", summary.NewPurchases)
	}

	var priceUSD float64
	d.db.QueryRow("SELECT price_usd FROM btc_lots WHERE source = 'coinbase'").Scan(&priceUSD)
	if priceUSD != 30.00 {
		t.Errorf("expected fallback price 30.00, got %f", priceUSD)
	}
}

// --- LastSyncedAt monotonic clock stripping ---

func TestLastSyncedAt_MonotonicClockStripped(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	// Insert a timestamp with Go's monotonic clock suffix
	_, err := d.db.Exec(
		`INSERT INTO sync_state (source, last_synced_at, last_offset) VALUES (?, ?, ?)`,
		"forwarding", "2026-04-24 14:36:58.876607065 +0000 UTC m=+0.015998844", 0,
	)
	if err != nil {
		t.Fatalf("insert sync_state: %v", err)
	}

	ts, err := d.LastSyncedAt(context.Background())
	if err != nil {
		t.Fatalf("LastSyncedAt: %v", err)
	}
	if ts.IsZero() {
		t.Fatal("expected non-zero time, got zero")
	}
	if ts.Year() != 2026 || ts.Month() != 4 || ts.Day() != 24 {
		t.Errorf("expected 2026-04-24, got %v", ts)
	}
}

func TestLastSyncedAt_PlainDatetime(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	_, err := d.db.Exec(
		`INSERT INTO sync_state (source, last_synced_at, last_offset) VALUES (?, ?, ?)`,
		"forwarding", "2026-04-10 19:20:33", 0,
	)
	if err != nil {
		t.Fatalf("insert sync_state: %v", err)
	}

	ts, err := d.LastSyncedAt(context.Background())
	if err != nil {
		t.Fatalf("LastSyncedAt: %v", err)
	}
	if ts.IsZero() {
		t.Fatal("expected non-zero time")
	}
}

func TestLastSyncedAt_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ts, err := d.LastSyncedAt(context.Background())
	if err != nil {
		t.Fatalf("LastSyncedAt: %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("expected zero time for empty sync_state, got %v", ts)
	}
}

// --- ClearExchangeSource with Swan data ---

func TestClearExchangeSource_Swan(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	if _, err := d.ImportSwanCSV(context.Background(), testSwanRows()); err != nil {
		t.Fatalf("import: %v", err)
	}

	res, err := d.ClearExchangeSource(context.Background(), "swan")
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if res.ImportsRemoved != 3 {
		t.Errorf("expected 3 imports removed, got %d", res.ImportsRemoved)
	}
	if res.LotsRemoved != 3 {
		t.Errorf("expected 3 lots removed, got %d", res.LotsRemoved)
	}

	var count int
	d.db.QueryRow("SELECT COUNT(*) FROM exchange_imports WHERE source = 'swan'").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 imports after clear, got %d", count)
	}
	d.db.QueryRow("SELECT COUNT(*) FROM btc_lots WHERE source = 'swan'").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 lots after clear, got %d", count)
	}
}

// --- Wallet CRUD coverage ---

func TestWalletCRUD_FullLifecycle(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()

	// Add
	id, err := d.AddWallet(ctx, "Test Wallet", "address", "bc1qtest", "")
	if err != nil {
		t.Fatalf("AddWallet: %v", err)
	}

	// Get
	w, err := d.GetWallet(ctx, id)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if w.Label != "Test Wallet" {
		t.Errorf("label = %q, want 'Test Wallet'", w.Label)
	}
	if w.BalanceSats != 0 {
		t.Errorf("initial balance = %d, want 0", w.BalanceSats)
	}

	// Update balance
	if err := d.UpdateWalletBalance(ctx, id, 500000); err != nil {
		t.Fatalf("UpdateWalletBalance: %v", err)
	}

	// Verify update
	w, _ = d.GetWallet(ctx, id)
	if w.BalanceSats != 500000 {
		t.Errorf("balance = %d, want 500000", w.BalanceSats)
	}
	if w.LastCheckedAt == nil {
		t.Error("expected LastCheckedAt to be set after update")
	}

	// List
	wallets, err := d.ListWallets(ctx)
	if err != nil {
		t.Fatalf("ListWallets: %v", err)
	}
	if len(wallets) != 1 {
		t.Fatalf("expected 1 wallet, got %d", len(wallets))
	}
	if wallets[0].BalanceSats != 500000 {
		t.Errorf("listed balance = %d, want 500000", wallets[0].BalanceSats)
	}

	// Total watched balance
	total, err := d.TotalWatchedBalance(ctx)
	if err != nil {
		t.Fatalf("TotalWatchedBalance: %v", err)
	}
	if total != 500000 {
		t.Errorf("total = %d, want 500000", total)
	}

	// Remove
	if err := d.RemoveWallet(ctx, id); err != nil {
		t.Fatalf("RemoveWallet: %v", err)
	}

	// Verify removed
	w, _ = d.GetWallet(ctx, id)
	if w != nil {
		t.Error("expected nil after removal")
	}
}

// --- PortfolioPosition ---

func TestPortfolioPosition_WithSwanData(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	if _, err := d.ImportSwanCSV(context.Background(), testSwanRows()); err != nil {
		t.Fatalf("import: %v", err)
	}

	pos, err := d.PortfolioPosition(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("PortfolioPosition: %v", err)
	}

	swan, ok := pos.BySource["swan"]
	if !ok {
		t.Fatal("expected swan in BySource")
	}
	// Net: 10975 + 98195 - 74465 = 34705
	if swan.NetSats != 34705 {
		t.Errorf("swan net sats = %d, want 34705", swan.NetSats)
	}
}

// --- ExchangeBalance for Swan ---

func TestExchangeBalance_Swan(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	if _, err := d.ImportSwanCSV(context.Background(), testSwanRows()); err != nil {
		t.Fatalf("import: %v", err)
	}

	bal, err := d.ExchangeBalance(context.Background(), "swan")
	if err != nil {
		t.Fatalf("ExchangeBalance: %v", err)
	}
	// 0.00010975 + 0.00098195 + (-0.00074465) = 0.00034705 BTC = 34705 sats
	if bal != 34705 {
		t.Errorf("swan balance = %d, want 34705", bal)
	}
}

func TestListExchangeTransactions(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	// Empty source returns empty page
	page, err := d.ListExchangeTransactions(context.Background(), "strike", 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != 0 || len(page.Transactions) != 0 {
		t.Errorf("expected empty page, got total=%d txns=%d", page.Total, len(page.Transactions))
	}

	// Import some rows
	rows := testStrikeRows()
	_, err = d.ImportStrikeCSV(context.Background(), rows)
	if err != nil {
		t.Fatalf("import error: %v", err)
	}

	// Fetch all
	page, err = d.ListExchangeTransactions(context.Background(), "strike", 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != len(rows) {
		t.Errorf("total = %d, want %d", page.Total, len(rows))
	}
	if len(page.Transactions) != len(rows) {
		t.Errorf("txns = %d, want %d", len(page.Transactions), len(rows))
	}

	// Verify fields on first transaction (sorted by date desc)
	for _, tx := range page.Transactions {
		if tx.Source != "strike" {
			t.Errorf("source = %q, want %q", tx.Source, "strike")
		}
		if tx.TxType == "" {
			t.Error("tx_type should not be empty")
		}
	}

	// Pagination: limit 1
	page, err = d.ListExchangeTransactions(context.Background(), "strike", 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != len(rows) {
		t.Errorf("total = %d, want %d", page.Total, len(rows))
	}
	if len(page.Transactions) != 1 {
		t.Errorf("txns = %d, want 1", len(page.Transactions))
	}
	if page.Page != 1 {
		t.Errorf("page = %d, want 1", page.Page)
	}

	// Different source returns 0
	page, err = d.ListExchangeTransactions(context.Background(), "river", 50, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Total != 0 {
		t.Errorf("river total = %d, want 0", page.Total)
	}
}

func TestInsertPortfolioSnapshot(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	err := d.InsertPortfolioSnapshot(context.Background(), 5_000_000, 65000.0)
	if err != nil {
		t.Fatalf("insert snapshot: %v", err)
	}

	// Insert another
	err = d.InsertPortfolioSnapshot(context.Background(), 5_100_000, 66000.0)
	if err != nil {
		t.Fatalf("insert second snapshot: %v", err)
	}

	snaps, err := d.PortfolioSnapshots(context.Background(), 30)
	if err != nil {
		t.Fatalf("fetch snapshots: %v", err)
	}
	// Both inserted today, so grouped into 1 day
	if len(snaps) != 1 {
		t.Fatalf("expected 1 day of snapshots, got %d", len(snaps))
	}
	// Should pick the latest values (second insert)
	if snaps[0].TotalSats != 5_100_000 {
		t.Errorf("expected 5100000 sats, got %d", snaps[0].TotalSats)
	}
}

func TestPortfolioSnapshots_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	snaps, err := d.PortfolioSnapshots(context.Background(), 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(snaps))
	}
}

func TestBackfillPortfolioSnapshots(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()

	// Backfill with no data should insert 0
	n, err := d.BackfillPortfolioSnapshots(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 inserted with no data, got %d", n)
	}

	// Insert some exchange transactions with dates
	now := time.Now()
	day1 := now.AddDate(0, 0, -20).Format("2006-01-02")
	day2 := now.AddDate(0, 0, -10).Format("2006-01-02")
	day3 := now.AddDate(0, 0, -5).Format("2006-01-02")

	for _, row := range []struct {
		date      string
		amountBTC float64
	}{
		{day1, 0.01},
		{day2, 0.02},
		{day3, -0.005},
	} {
		rawData := fmt.Sprintf(`{"Date":"%s","AmountBTC":%f,"Type":"purchase"}`, row.date, row.amountBTC)
		_, err := d.db.ExecContext(ctx,
			`INSERT INTO exchange_imports (source, external_id, tx_type, raw_data)
			 VALUES ('strike', ?, 'purchase', ?)`,
			row.date, rawData)
		if err != nil {
			t.Fatalf("seed exchange: %v", err)
		}
	}

	// Backfill should create 3 snapshots
	n, err = d.BackfillPortfolioSnapshots(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 inserted, got %d", n)
	}

	// Verify snapshots
	snaps, err := d.PortfolioSnapshots(ctx, 365)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(snaps))
	}

	// Jan 15: 0.01 BTC = 1,000,000 sats
	if snaps[0].TotalSats != 1_000_000 {
		t.Errorf("jan snapshot: expected 1000000 sats, got %d", snaps[0].TotalSats)
	}
	// Feb 1: 0.01 + 0.02 = 0.03 BTC = 3,000,000 sats
	if snaps[1].TotalSats != 3_000_000 {
		t.Errorf("feb snapshot: expected 3000000 sats, got %d", snaps[1].TotalSats)
	}
	// Mar 1: 0.03 - 0.005 = 0.025 BTC = 2,500,000 sats
	if snaps[2].TotalSats != 2_500_000 {
		t.Errorf("mar snapshot: expected 2500000 sats, got %d", snaps[2].TotalSats)
	}

	// Price should be 0 (backfilled, marker -1 converted to 0)
	if snaps[0].BTCPriceUSD != 0 {
		t.Errorf("expected price 0 for backfilled, got %f", snaps[0].BTCPriceUSD)
	}

	// Re-running backfill is idempotent (deletes old backfill, re-inserts)
	n, err = d.BackfillPortfolioSnapshots(ctx)
	if err != nil {
		t.Fatalf("re-backfill: %v", err)
	}
	if n != 3 {
		t.Errorf("re-backfill: expected 3, got %d", n)
	}

	// Should still have exactly 3 snapshots
	snaps, err = d.PortfolioSnapshots(ctx, 365)
	if err != nil {
		t.Fatalf("fetch after re-backfill: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("expected 3 snapshots after re-backfill, got %d", len(snaps))
	}
}

func TestBackfillPortfolioSnapshots_PreservesLiveSnapshots(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()

	// Insert a live snapshot (btc_price_usd >= 0)
	err := d.InsertPortfolioSnapshot(ctx, 5_000_000, 65000.0)
	if err != nil {
		t.Fatalf("insert live: %v", err)
	}

	// Insert exchange data for today (use UTC to match SQLite CURRENT_TIMESTAMP)
	today := time.Now().UTC().Format("2006-01-02")
	rawData := fmt.Sprintf(`{"Date":"%s","AmountBTC":0.01,"Type":"purchase"}`, today)
	_, err = d.db.ExecContext(ctx,
		`INSERT INTO exchange_imports (source, external_id, tx_type, raw_data)
		 VALUES ('strike', 'today-tx', 'purchase', ?)`, rawData)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Backfill should skip today (live snapshot exists)
	n, err := d.BackfillPortfolioSnapshots(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 (live snapshot exists), got %d", n)
	}

	// Live snapshot should be preserved
	snaps, err := d.PortfolioSnapshots(ctx, 1)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].TotalSats != 5_000_000 {
		t.Errorf("live snapshot should be preserved, got %d sats", snaps[0].TotalSats)
	}
}

func TestListUnifiedTransactions(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Seed data from multiple sources
	err := d.RunSync(ctx, func(tx SyncTx) error {
		// Forwarding event (fee income)
		if err := tx.InsertForwardingEvents([]ForwardingEvent{{
			Timestamp:  now.Add(-4 * time.Hour),
			ChanIDIn:   1001,
			ChanIDOut:  1002,
			AmtInMsat:  10_000_000,
			AmtOutMsat: 9_500_000,
			FeeMsat:    500_000,
		}}); err != nil {
			return err
		}

		// Invoice received
		settled := now.Add(-3 * time.Hour)
		if err := tx.UpsertInvoices([]Invoice{{
			PaymentHash: "inv-hash-1",
			AmtPaidMsat: 50_000_000,
			CreatedAt:   now.Add(-4 * time.Hour),
			SettledAt:   &settled,
		}}); err != nil {
			return err
		}

		// Unsettled invoice (should NOT appear)
		if err := tx.UpsertInvoices([]Invoice{{
			PaymentHash: "inv-hash-unsettled",
			AmtPaidMsat: 10_000_000,
			CreatedAt:   now.Add(-2 * time.Hour),
		}}); err != nil {
			return err
		}

		// Payment sent
		if err := tx.UpsertPayments([]Payment{{
			PaymentHash: "pay-hash-1",
			ValueMsat:   25_000_000,
			FeeMsat:     1_000,
			CreatedAt:   now.Add(-2 * time.Hour),
			Status:      "SUCCEEDED",
		}}); err != nil {
			return err
		}

		// Failed payment (should NOT appear)
		if err := tx.UpsertPayments([]Payment{{
			PaymentHash: "pay-hash-failed",
			ValueMsat:   5_000_000,
			FeeMsat:     500,
			CreatedAt:   now.Add(-1 * time.Hour),
			Status:      "FAILED",
		}}); err != nil {
			return err
		}

		// On-chain receive (confirmed)
		if err := tx.UpsertOnchainTxns([]OnchainTx{{
			TxHash:           "onchain-tx-1",
			AmountSat:        100_000,
			NumConfirmations: 6,
			Timestamp:        now.Add(-1 * time.Hour),
			Label:            "deposit",
		}}); err != nil {
			return err
		}

		// On-chain unconfirmed (should NOT appear)
		if err := tx.UpsertOnchainTxns([]OnchainTx{{
			TxHash:           "onchain-tx-unconfirmed",
			AmountSat:        50_000,
			NumConfirmations: 0,
			Timestamp:        now,
		}}); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		t.Fatalf("seed data: %v", err)
	}

	page, err := d.ListUnifiedTransactions(ctx, TransactionFilter{Limit: 100, Offset: 0})
	if err != nil {
		t.Fatalf("ListUnifiedTransactions: %v", err)
	}

	// Expect: forward + invoice + payment + onchain = 4
	// (unsettled invoice, failed payment, unconfirmed onchain excluded)
	if page.Total != 4 {
		t.Errorf("expected 4 transactions, got %d", page.Total)
		for _, tx := range page.Transactions {
			t.Logf("  %s %s %s %d sat", tx.Source, tx.TxType, tx.SourceID, tx.AmountSat)
		}
	}

	// Check that results are ordered by time DESC
	for i := 1; i < len(page.Transactions); i++ {
		if page.Transactions[i].Time.After(page.Transactions[i-1].Time) {
			t.Errorf("transactions not in descending order at index %d", i)
		}
	}

	// Verify types
	typeMap := map[string]bool{}
	for _, tx := range page.Transactions {
		typeMap[tx.TxType] = true
		switch tx.Source {
		case "lnd_forward":
			if tx.TxType != "fee_income" {
				t.Errorf("forward should be fee_income, got %s", tx.TxType)
			}
			if tx.AmountSat != 500 { // 500_000 msat = 500 sat
				t.Errorf("forward fee: expected 500 sat, got %d", tx.AmountSat)
			}
		case "lnd_invoice":
			if tx.TxType != "receive" {
				t.Errorf("invoice should be receive, got %s", tx.TxType)
			}
			if tx.AmountSat != 50_000 {
				t.Errorf("invoice: expected 50000 sat, got %d", tx.AmountSat)
			}
		case "lnd_payment":
			if tx.TxType != "send" {
				t.Errorf("payment should be send, got %s", tx.TxType)
			}
			if tx.AmountSat != -25_000 {
				t.Errorf("payment: expected -25000 sat, got %d", tx.AmountSat)
			}
		case "lnd_onchain":
			if tx.TxType != "receive" {
				t.Errorf("onchain should be receive, got %s", tx.TxType)
			}
			if tx.AmountSat != 100_000 {
				t.Errorf("onchain: expected 100000 sat, got %d", tx.AmountSat)
			}
		}
	}
}

func TestListUnifiedTransactionsFilters(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed diverse data
	err := d.RunSync(ctx, func(tx SyncTx) error {
		if err := tx.InsertForwardingEvents([]ForwardingEvent{{
			Timestamp: time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC),
			ChanIDIn: 1001, ChanIDOut: 1002,
			AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000,
		}}); err != nil {
			return err
		}
		if err := tx.UpsertInvoices([]Invoice{{
			PaymentHash: "inv-filter-1", AmtPaidMsat: 50_000_000,
			CreatedAt: time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			SettledAt: func() *time.Time { t := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC); return &t }(),
		}}); err != nil {
			return err
		}
		return tx.UpsertPayments([]Payment{{
			PaymentHash: "pay-filter-1", ValueMsat: 25_000_000, FeeMsat: 100,
			CreatedAt: time.Date(2024, 6, 20, 12, 0, 0, 0, time.UTC),
			Status: "SUCCEEDED",
		}})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Filter by type
	page, err := d.ListUnifiedTransactions(ctx, TransactionFilter{TxType: "fee_income", Limit: 100})
	if err != nil {
		t.Fatalf("filter by type: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("type filter: expected 1, got %d", page.Total)
	}

	// Filter by source
	page, err = d.ListUnifiedTransactions(ctx, TransactionFilter{Source: "lnd_invoice", Limit: 100})
	if err != nil {
		t.Fatalf("filter by source: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("source filter: expected 1, got %d", page.Total)
	}

	// Filter by date range
	page, err = d.ListUnifiedTransactions(ctx, TransactionFilter{
		DateFrom: "2024-06-01", DateTo: "2024-06-30", Limit: 100,
	})
	if err != nil {
		t.Fatalf("filter by date: %v", err)
	}
	if page.Total != 2 { // invoice + payment
		t.Errorf("date filter: expected 2, got %d", page.Total)
	}

	// Sort by amount ascending
	page, err = d.ListUnifiedTransactions(ctx, TransactionFilter{SortCol: "amount_sat", SortDir: "asc", Limit: 100})
	if err != nil {
		t.Fatalf("sort: %v", err)
	}
	if len(page.Transactions) == 3 && page.Transactions[0].AmountSat > page.Transactions[2].AmountSat {
		t.Errorf("expected ascending sort by amount")
	}

	// Search by memo
	page, err = d.ListUnifiedTransactions(ctx, TransactionFilter{Search: "routing fee", Limit: 100})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if page.Total != 1 {
		t.Errorf("search: expected 1, got %d", page.Total)
	}
}

func TestListUnifiedTransactionsSince(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	err := d.RunSync(ctx, func(tx SyncTx) error {
		return tx.UpsertOnchainTxns([]OnchainTx{
			{TxHash: "old-tx", AmountSat: 1000, NumConfirmations: 6, Timestamp: now.Add(-2 * time.Hour)},
			{TxHash: "new-tx", AmountSat: 2000, NumConfirmations: 3, Timestamp: now.Add(-30 * time.Minute)},
		})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Only get transactions after 1 hour ago
	txns, err := d.ListUnifiedTransactionsSince(ctx, now.Add(-1*time.Hour), 100)
	if err != nil {
		t.Fatalf("ListUnifiedTransactionsSince: %v", err)
	}

	if len(txns) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txns))
	}
	if txns[0].SourceID != "new-tx" {
		t.Errorf("expected new-tx, got %s", txns[0].SourceID)
	}
}

func TestTotalPortfolioSats(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	// With no data, total should be 0
	total, err := d.TotalPortfolioSats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 0 {
		t.Errorf("expected 0, got %d", total)
	}

	// Add a wallet balance
	err = d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.InsertWalletBalanceSnapshot(WalletBalanceSnapshot{
			CapturedAt:     time.Now(),
			TotalSat:       100_000,
			ConfirmedSat:   90_000,
			UnconfirmedSat: 10_000,
		})
	})
	if err != nil {
		t.Fatalf("seed wallet balance: %v", err)
	}

	total, err = d.TotalPortfolioSats(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 100_000 {
		t.Errorf("expected 100000, got %d", total)
	}
}

func TestTransactionNotes(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	// Get note for non-existent source ID returns empty
	note, err := d.GetTransactionNote(ctx, "forward:1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if note != "" {
		t.Errorf("expected empty note, got %q", note)
	}

	// Set a note
	if err := d.SetTransactionNote(ctx, "forward:1", "test routing fee"); err != nil {
		t.Fatalf("set note: %v", err)
	}

	// Read it back
	note, err = d.GetTransactionNote(ctx, "forward:1")
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if note != "test routing fee" {
		t.Errorf("expected 'test routing fee', got %q", note)
	}

	// Update the note
	if err := d.SetTransactionNote(ctx, "forward:1", "updated note"); err != nil {
		t.Fatalf("update note: %v", err)
	}
	note, err = d.GetTransactionNote(ctx, "forward:1")
	if err != nil {
		t.Fatalf("get updated note: %v", err)
	}
	if note != "updated note" {
		t.Errorf("expected 'updated note', got %q", note)
	}

	// Delete note by setting empty
	if err := d.SetTransactionNote(ctx, "forward:1", ""); err != nil {
		t.Fatalf("delete note: %v", err)
	}
	note, err = d.GetTransactionNote(ctx, "forward:1")
	if err != nil {
		t.Fatalf("get deleted note: %v", err)
	}
	if note != "" {
		t.Errorf("expected empty after delete, got %q", note)
	}
}

func TestUnifiedTransactionsWithNotes(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	// Seed a forwarding event
	seedForwardingEvents(t, d, []ForwardingEvent{{
		Timestamp: time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC),
		ChanIDIn:  1001, ChanIDOut: 1002,
		AmtInMsat: 10000, AmtOutMsat: 9000, FeeMsat: 1000,
	}})

	// Add a note for it
	if err := d.SetTransactionNote(ctx, "forward:1", "my routing note"); err != nil {
		t.Fatalf("set note: %v", err)
	}

	// List should include the note
	page, err := d.ListUnifiedTransactions(ctx, TransactionFilter{Limit: 50, Offset: 0})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(page.Transactions))
	}
	if page.Transactions[0].Note != "my routing note" {
		t.Errorf("expected note 'my routing note', got %q", page.Transactions[0].Note)
	}
}

func TestListBTCLots(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Insert lots directly
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id) VALUES
		 (?, 100000, 50.00, 'strike', 'tx1'),
		 (?, 200000, 120.00, 'river', 'tx2')`,
		time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2023, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	lots, err := d.ListBTCLots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lots) != 2 {
		t.Fatalf("expected 2 lots, got %d", len(lots))
	}
	// Should be ordered by acquired_at ASC
	if lots[0].Source != "strike" || lots[0].AmountSat != 100000 {
		t.Errorf("lot 0: source=%s amount=%d", lots[0].Source, lots[0].AmountSat)
	}
	if lots[1].Source != "river" || lots[1].AmountSat != 200000 {
		t.Errorf("lot 1: source=%s amount=%d", lots[1].Source, lots[1].AmountSat)
	}
	if lots[0].PriceUSD != 50.00 {
		t.Errorf("lot 0 price: %f", lots[0].PriceUSD)
	}
}

func TestListBTCLots_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	lots, err := d.ListBTCLots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(lots) != 0 {
		t.Errorf("expected 0 lots, got %d", len(lots))
	}
}

func TestListDisposals(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Insert exchange_imports with sale and non-sale types
	for _, tc := range []struct {
		txType string
		extID  string
		raw    string
	}{
		{"Sale", "s1", `{"Date":"2024-06-01T10:00:00Z","AmountBTC":-0.01,"AmountUSD":500}`},
		{"sell", "s2", `{"Date":"2024-07-01T10:00:00Z","AmountBTC":-0.02,"AmountUSD":1200}`},
		{"send", "s3", `{"Date":"2024-08-01T10:00:00Z","AmountBTC":-0.005,"AmountUSD":0}`},
		{"Purchase", "p1", `{"Date":"2024-01-01T10:00:00Z","AmountBTC":0.05,"AmountUSD":2000}`},
	} {
		_, err := d.db.ExecContext(ctx,
			`INSERT INTO exchange_imports (source, external_id, tx_type, raw_data) VALUES ('strike', ?, ?, ?)`,
			tc.extID, tc.txType, tc.raw)
		if err != nil {
			t.Fatal(err)
		}
	}

	disposals, err := d.ListDisposals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Only Sale and sell should be included (not send, not Purchase)
	if len(disposals) != 2 {
		t.Fatalf("expected 2 disposals, got %d", len(disposals))
	}
	// Ordered by date ASC
	if disposals[0].AmountSat != 1_000_000 { // 0.01 BTC
		t.Errorf("disposal 0 amount: %d", disposals[0].AmountSat)
	}
	if disposals[0].ProceedsUSD != 500 {
		t.Errorf("disposal 0 proceeds: %f", disposals[0].ProceedsUSD)
	}
	if disposals[1].AmountSat != 2_000_000 { // 0.02 BTC
		t.Errorf("disposal 1 amount: %d", disposals[1].AmountSat)
	}
}

func TestListDisposals_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	disposals, err := d.ListDisposals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(disposals) != 0 {
		t.Errorf("expected 0 disposals, got %d", len(disposals))
	}
}

func TestSaveDisposals(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	events := []struct {
		DisposedAt  time.Time
		AmountSat   int64
		ProceedsUSD float64
		LotID       int64
	}{
		{time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), 500_000, 300.00, 1},
		{time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC), 200_000, 150.00, 2},
	}

	if err := d.SaveDisposals(ctx, events); err != nil {
		t.Fatal(err)
	}

	// Verify rows were inserted
	var count int
	d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM disposals`).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 disposals, got %d", count)
	}

	// Save again — should clear and rewrite
	if err := d.SaveDisposals(ctx, events[:1]); err != nil {
		t.Fatal(err)
	}
	d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM disposals`).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 disposal after re-save, got %d", count)
	}
}

func TestSaveDisposals_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	if err := d.SaveDisposals(context.Background(), nil); err != nil {
		t.Fatal(err)
	}

	var count int
	d.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM disposals`).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

// --- Settings tests ---

func TestGetSetting_NotFound(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	val, err := d.GetSetting(context.Background(), "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if val != "" {
		t.Errorf("expected empty string, got %q", val)
	}
}

func TestSetAndGetSetting(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	if err := d.SetSetting(ctx, "theme", "dark"); err != nil {
		t.Fatal(err)
	}

	val, err := d.GetSetting(ctx, "theme")
	if err != nil {
		t.Fatal(err)
	}
	if val != "dark" {
		t.Errorf("expected %q, got %q", "dark", val)
	}

	// Upsert: overwrite same key
	if err := d.SetSetting(ctx, "theme", "light"); err != nil {
		t.Fatal(err)
	}
	val, err = d.GetSetting(ctx, "theme")
	if err != nil {
		t.Fatal(err)
	}
	if val != "light" {
		t.Errorf("expected %q after upsert, got %q", "light", val)
	}
}

// --- Monarch sync tests ---

func seedExchangeImport(t *testing.T, d *DB, source, externalID, txType string, amountBTC, amountUSD float64, date string) {
	t.Helper()
	raw := fmt.Sprintf(`{"AmountBTC": %f, "AmountUSD": %f, "Date": %q}`, amountBTC, amountUSD, date)
	_, err := d.db.ExecContext(context.Background(),
		`INSERT INTO exchange_imports (source, external_id, tx_type, raw_data) VALUES (?, ?, ?, ?)`,
		source, externalID, txType, raw)
	if err != nil {
		t.Fatalf("seed exchange import: %v", err)
	}
}

func TestMarkTransactionSynced(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	if err := d.MarkTransactionSynced(ctx, "strike:1:buy", "monarch_abc"); err != nil {
		t.Fatal(err)
	}

	// Duplicate insert should be ignored
	if err := d.MarkTransactionSynced(ctx, "strike:1:buy", "monarch_abc"); err != nil {
		t.Fatal(err)
	}

	count, err := d.MonarchSyncedCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

func TestMonarchSyncedCount_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	count, err := d.MonarchSyncedCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestClearMonarchSyncState(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed some synced records
	for i := range 3 {
		if err := d.MarkTransactionSynced(ctx, fmt.Sprintf("src:%d:buy", i), fmt.Sprintf("m_%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	count, _ := d.MonarchSyncedCount(ctx)
	if count != 3 {
		t.Fatalf("expected 3 before clear, got %d", count)
	}

	if err := d.ClearMonarchSyncState(ctx); err != nil {
		t.Fatal(err)
	}

	count, _ = d.MonarchSyncedCount(ctx)
	if count != 0 {
		t.Errorf("expected 0 after clear, got %d", count)
	}
}

func TestListUnsyncedTransactions(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed exchange imports (these feed btc_transactions_v)
	seedExchangeImport(t, d, "strike", "tx1", "Purchase", 0.001, 50.00, "2024-01-01T12:00:00Z")
	seedExchangeImport(t, d, "strike", "tx2", "Sale", 0.002, 100.00, "2024-01-02T12:00:00Z")
	seedExchangeImport(t, d, "strike", "tx3", "Purchase", 0.003, 150.00, "2024-01-03T12:00:00Z")

	// All should be unsynced initially
	txns, err := d.ListUnsyncedTransactions(ctx, []string{"buy", "sell"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(txns) != 3 {
		t.Fatalf("expected 3 unsynced, got %d", len(txns))
	}

	// Mark one as synced
	if err := d.MarkTransactionSynced(ctx, txns[0].SourceID, "monarch_1"); err != nil {
		t.Fatal(err)
	}

	txns, err = d.ListUnsyncedTransactions(ctx, []string{"buy", "sell"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(txns) != 2 {
		t.Errorf("expected 2 unsynced after marking one, got %d", len(txns))
	}

	// Filter by source
	txns, err = d.ListUnsyncedTransactions(ctx, []string{"buy"}, []string{"strike"})
	if err != nil {
		t.Fatal(err)
	}
	// tx1 was synced, tx3 is a buy from strike
	if len(txns) != 1 {
		t.Errorf("expected 1 unsynced buy from strike, got %d", len(txns))
	}
}

func TestListUnsyncedTransactions_EmptyTypes(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	txns, err := d.ListUnsyncedTransactions(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if txns != nil {
		t.Errorf("expected nil for empty txTypes, got %v", txns)
	}
}

// --- DistinctTransactionValues tests ---

func TestDistinctTransactionValues_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	sources, txTypes, err := d.DistinctTransactionValues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 0 {
		t.Errorf("expected no sources, got %v", sources)
	}
	if len(txTypes) != 0 {
		t.Errorf("expected no txTypes, got %v", txTypes)
	}
}

func TestDistinctTransactionValues(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	seedExchangeImport(t, d, "strike", "a1", "Purchase", 0.001, 50.0, "2024-01-01T12:00:00Z")
	seedExchangeImport(t, d, "strike", "a2", "Sale", 0.002, 100.0, "2024-01-02T12:00:00Z")
	seedExchangeImport(t, d, "river", "b1", "Purchase", 0.003, 150.0, "2024-01-03T12:00:00Z")

	sources, txTypes, err := d.DistinctTransactionValues(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Sources: river, strike (alphabetical)
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d: %v", len(sources), sources)
	}
	if sources[0] != "river" || sources[1] != "strike" {
		t.Errorf("unexpected sources: %v", sources)
	}

	// TxTypes: buy, sell (the view normalizes Purchase→buy, Sale→sell)
	if len(txTypes) != 2 {
		t.Fatalf("expected 2 txTypes, got %d: %v", len(txTypes), txTypes)
	}
	if txTypes[0] != "buy" || txTypes[1] != "sell" {
		t.Errorf("unexpected txTypes: %v", txTypes)
	}
}

// --- Portfolio Breakdown tests ---

func TestPortfolioBreakdownQuery(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed exchange imports
	seedExchangeImport(t, d, "strike", "pb1", "Purchase", 0.01, 500.0, "2024-01-01T12:00:00Z")
	seedExchangeImport(t, d, "river", "pb2", "Purchase", 0.02, 1000.0, "2024-01-02T12:00:00Z")

	b, err := d.PortfolioBreakdownQuery(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Should have exchange balances for strike and river
	if b.ExchangeSats["strike"] == 0 {
		t.Error("expected non-zero strike balance")
	}
	if b.ExchangeSats["river"] == 0 {
		t.Error("expected non-zero river balance")
	}
	if b.TotalSats != b.OnChainSats+b.ChannelSats+b.ColdStorageSats+b.ExchangeSats["strike"]+b.ExchangeSats["river"] {
		t.Errorf("total sats mismatch: got %d", b.TotalSats)
	}
}

func TestPortfolioBreakdownQuery_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	b, err := d.PortfolioBreakdownQuery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalSats != 0 {
		t.Errorf("expected 0 total, got %d", b.TotalSats)
	}
}

func TestInsertPortfolioSnapshotWithDetails(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	details := map[string]int64{
		"onchain":  1_000_000,
		"channels": 500_000,
		"strike":   200_000,
	}

	if err := d.InsertPortfolioSnapshotWithDetails(ctx, 1_700_000, 50000.0, details); err != nil {
		t.Fatal(err)
	}

	// Verify snapshot was inserted
	var totalSats int64
	d.db.QueryRowContext(ctx, `SELECT total_sats FROM portfolio_snapshots ORDER BY id DESC LIMIT 1`).Scan(&totalSats)
	if totalSats != 1_700_000 {
		t.Errorf("expected 1700000, got %d", totalSats)
	}

	// Verify details were inserted
	var detailCount int
	d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM portfolio_snapshot_details`).Scan(&detailCount)
	if detailCount != 3 {
		t.Errorf("expected 3 detail rows, got %d", detailCount)
	}

	// Verify specific detail
	var sats int64
	d.db.QueryRowContext(ctx, `SELECT sats FROM portfolio_snapshot_details WHERE source = 'onchain'`).Scan(&sats)
	if sats != 1_000_000 {
		t.Errorf("expected onchain=1000000, got %d", sats)
	}
}

func TestPortfolioSnapshotsWithDetails(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Insert two snapshots on different recent days
	now := time.Now().UTC()
	day1 := now.AddDate(0, 0, -2).Format("2006-01-02") + " 12:00:00"
	day2 := now.AddDate(0, 0, -1).Format("2006-01-02") + " 12:00:00"

	d.db.ExecContext(ctx,
		`INSERT INTO portfolio_snapshots (captured_at, total_sats, btc_price_usd) VALUES (?, 1000000, 50000)`, day1)
	d.db.ExecContext(ctx,
		`INSERT INTO portfolio_snapshot_details (snapshot_id, source, sats) VALUES (1, 'onchain', 700000)`)
	d.db.ExecContext(ctx,
		`INSERT INTO portfolio_snapshot_details (snapshot_id, source, sats) VALUES (1, 'strike', 300000)`)

	d.db.ExecContext(ctx,
		`INSERT INTO portfolio_snapshots (captured_at, total_sats, btc_price_usd) VALUES (?, 1200000, 51000)`, day2)
	d.db.ExecContext(ctx,
		`INSERT INTO portfolio_snapshot_details (snapshot_id, source, sats) VALUES (2, 'onchain', 800000)`)
	d.db.ExecContext(ctx,
		`INSERT INTO portfolio_snapshot_details (snapshot_id, source, sats) VALUES (2, 'strike', 400000)`)

	details, err := d.PortfolioSnapshotsWithDetails(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}

	// Should have 4 detail rows (2 per day x 2 sources)
	if len(details) != 4 {
		t.Fatalf("expected 4 details, got %d", len(details))
	}

	// First two should be from the same day (day1), last two from day2
	if details[0].CapturedAt != details[1].CapturedAt {
		t.Errorf("expected first two from same day, got %v and %v", details[0].CapturedAt, details[1].CapturedAt)
	}
	if details[2].CapturedAt != details[3].CapturedAt {
		t.Errorf("expected last two from same day, got %v and %v", details[2].CapturedAt, details[3].CapturedAt)
	}
}

func TestPortfolioSnapshotsWithDetails_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	details, err := d.PortfolioSnapshotsWithDetails(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 0 {
		t.Errorf("expected 0 details, got %d", len(details))
	}
}

func TestNetFlowSummary(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed: Purchase (inflow), Sale (outflow)
	seedExchangeImport(t, d, "strike", "nf1", "Purchase", 0.01, 500.0, "2024-06-01T12:00:00Z")
	seedExchangeImport(t, d, "strike", "nf2", "Sale", -0.005, -250.0, "2024-06-15T12:00:00Z")

	since, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")
	nf, err := d.NetFlowSummary(ctx, since, false)
	if err != nil {
		t.Fatal(err)
	}

	if nf.InflowSats == 0 {
		t.Error("expected non-zero inflows")
	}
	if nf.InflowCount == 0 {
		t.Error("expected non-zero inflow count")
	}
	// Sale has negative amount_sat, so outflows should be non-zero
	if nf.OutflowSats == 0 {
		t.Error("expected non-zero outflows")
	}
}

func TestNetFlowSummary_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	nf, err := d.NetFlowSummary(context.Background(), time.Time{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if nf.InflowSats != 0 || nf.OutflowSats != 0 {
		t.Errorf("expected zeros, got inflow=%d outflow=%d", nf.InflowSats, nf.OutflowSats)
	}
}

func TestNetFlowSummary_ExcludeTransfers(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed: two purchases, one of which we'll mark as transfer
	seedExchangeImport(t, d, "strike", "xfer1", "Purchase", 0.01, 500.0, "2024-06-01T12:00:00Z")
	seedExchangeImport(t, d, "strike", "xfer2", "Purchase", 0.02, 1000.0, "2024-06-02T12:00:00Z")

	since, _ := time.Parse(time.RFC3339, "2024-01-01T00:00:00Z")

	// Without excluding transfers: both should count
	nfAll, err := d.NetFlowSummary(ctx, since, false)
	if err != nil {
		t.Fatal(err)
	}
	if nfAll.InflowCount != 2 {
		t.Errorf("expected 2 inflows, got %d", nfAll.InflowCount)
	}

	// Mark one as transfer (source_id format: source:external_id:tx_type from raw column)
	if err := d.SetTransferFlag(ctx, "strike:xfer1:Purchase", true); err != nil {
		t.Fatal(err)
	}

	// Excluding transfers: only one should count
	nfFiltered, err := d.NetFlowSummary(ctx, since, true)
	if err != nil {
		t.Fatal(err)
	}
	if nfFiltered.InflowCount != 1 {
		t.Errorf("expected 1 inflow after excluding transfers, got %d", nfFiltered.InflowCount)
	}
	if nfFiltered.InflowSats >= nfAll.InflowSats {
		t.Errorf("expected fewer inflow sats after excluding, got filtered=%d all=%d", nfFiltered.InflowSats, nfAll.InflowSats)
	}
}

func TestSetTransferFlag(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Get flag for non-existent note — should be false
	val, err := d.GetTransferFlag(ctx, "test:abc:buy")
	if err != nil {
		t.Fatal(err)
	}
	if val {
		t.Error("expected false for non-existent note")
	}

	// Set to true
	if err := d.SetTransferFlag(ctx, "test:abc:buy", true); err != nil {
		t.Fatal(err)
	}
	val, err = d.GetTransferFlag(ctx, "test:abc:buy")
	if err != nil {
		t.Fatal(err)
	}
	if !val {
		t.Error("expected true after setting")
	}

	// Toggle back to false
	if err := d.SetTransferFlag(ctx, "test:abc:buy", false); err != nil {
		t.Fatal(err)
	}
	val, err = d.GetTransferFlag(ctx, "test:abc:buy")
	if err != nil {
		t.Fatal(err)
	}
	if val {
		t.Error("expected false after unsetting")
	}
}

func TestListTransferCandidates(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed: one inflow and one outflow of ~same amount within 24h
	seedExchangeImport(t, d, "strike", "tc1", "Purchase", 0.01, 500.0, "2024-06-01T12:00:00Z")
	seedExchangeImport(t, d, "river", "tc2", "Withdrawal", -0.01, -500.0, "2024-06-01T18:00:00Z")
	// Seed: another that's too far in time
	seedExchangeImport(t, d, "swan", "tc3", "Withdrawal", -0.01, -500.0, "2024-07-01T12:00:00Z")

	ts, _ := time.Parse(time.RFC3339, "2024-06-01T12:00:00Z")
	candidates, err := d.ListTransferCandidates(ctx, "strike:tc1:buy", 1_000_000, ts)
	if err != nil {
		t.Fatal(err)
	}

	// Should find river:tc2 (opposite direction, within 24h, similar amount)
	// Should NOT find swan:tc3 (too far in time)
	if len(candidates) == 0 {
		t.Error("expected at least one candidate")
	}
	for _, c := range candidates {
		if c.SourceID == "strike:tc1:buy" {
			t.Error("should not include the source transaction itself")
		}
	}
}

func TestListTransferCandidates_NoMatch(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Only one transaction — no possible matches
	seedExchangeImport(t, d, "strike", "solo1", "Purchase", 0.01, 500.0, "2024-06-01T12:00:00Z")

	ts, _ := time.Parse(time.RFC3339, "2024-06-01T12:00:00Z")
	candidates, err := d.ListTransferCandidates(ctx, "strike:solo1:buy", 1_000_000, ts)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Errorf("expected no candidates, got %d", len(candidates))
	}
}

func TestAutoTagChannelTransfers(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed a channel with a known channel_point (txid:output_index)
	err := d.RunSync(ctx, func(tx SyncTx) error {
		if err := tx.UpsertChannels([]Channel{{
			ChanID:       12345,
			RemotePubKey: "pubkey1",
			ChannelPoint: "abc123def456:0",
			LocalBalance: 500000,
			Active:       true,
		}}); err != nil {
			return err
		}
		// Seed a closed channel with closing_tx_hash
		if err := tx.UpsertChannels([]Channel{{
			ChanID:        67890,
			RemotePubKey:  "pubkey2",
			ChannelPoint:  "open789:1",
			ClosingTxHash: "close456def",
			Active:        false,
		}}); err != nil {
			return err
		}
		// Seed on-chain txns matching both
		if err := tx.UpsertOnchainTxns([]OnchainTx{
			{TxHash: "abc123def456", AmountSat: -500000, NumConfirmations: 6, Timestamp: time.Now()},   // channel open
			{TxHash: "close456def", AmountSat: 490000, NumConfirmations: 6, Timestamp: time.Now()},     // channel close
			{TxHash: "unrelated999", AmountSat: -100000, NumConfirmations: 6, Timestamp: time.Now()},   // not a channel tx
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	n, err := d.AutoTagChannelTransfers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 auto-tagged, got %d", n)
	}

	// Verify the channel open tx is tagged with correct flag and note
	flagOpen, err := d.GetTransferFlag(ctx, "abc123def456")
	if err != nil {
		t.Fatal(err)
	}
	if !flagOpen {
		t.Error("expected channel open tx to be tagged as transfer")
	}
	noteOpen, err := d.GetTransactionNote(ctx, "abc123def456")
	if err != nil {
		t.Fatal(err)
	}
	if noteOpen != "Channel Open" {
		t.Errorf("expected channel open note %q, got %q", "Channel Open", noteOpen)
	}

	// Verify the channel close tx is tagged with correct flag and note
	flagClose, err := d.GetTransferFlag(ctx, "close456def")
	if err != nil {
		t.Fatal(err)
	}
	if !flagClose {
		t.Error("expected channel close tx to be tagged as transfer")
	}
	noteClose, err := d.GetTransactionNote(ctx, "close456def")
	if err != nil {
		t.Fatal(err)
	}
	if noteClose != "Channel Close" {
		t.Errorf("expected channel close note %q, got %q", "Channel Close", noteClose)
	}

	// Verify unrelated tx is NOT tagged
	flagUnrelated, err := d.GetTransferFlag(ctx, "unrelated999")
	if err != nil {
		t.Fatal(err)
	}
	if flagUnrelated {
		t.Error("expected unrelated tx to NOT be tagged as transfer")
	}

	// Running again should return 0 (idempotent)
	n2, err := d.AutoTagChannelTransfers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 on re-run (idempotent), got %d", n2)
	}
}

// TestAutoTagChannelTransfers_TableDriven covers matching logic and edge cases.
func TestAutoTagChannelTransfers_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		channels    []Channel
		onchainTxns []OnchainTx
		// exchangeImport source_id — must NOT be tagged
		exchangeIDs  []string
		wantTagged  map[string]string // source_id -> expected note ("Channel Open"/"Channel Close")
		wantUntagged []string         // source_id -> must not be tagged
	}{
		{
			name: "channel_open_from_channel_point",
			channels: []Channel{
				{ChanID: 1, RemotePubKey: "pk1", ChannelPoint: "opentx001:0", Active: true},
			},
			onchainTxns: []OnchainTx{
				{TxHash: "opentx001", AmountSat: -500000, NumConfirmations: 6, Timestamp: time.Now()},
				{TxHash: "othertx", AmountSat: 100000, NumConfirmations: 6, Timestamp: time.Now()},
			},
			wantTagged:   map[string]string{"opentx001": "Channel Open"},
			wantUntagged: []string{"othertx"},
		},
		{
			name: "channel_close_from_closing_tx_hash",
			channels: []Channel{
				{ChanID: 2, RemotePubKey: "pk2", ChannelPoint: "fundtx:1", ClosingTxHash: "closetx002", Active: false},
			},
			onchainTxns: []OnchainTx{
				{TxHash: "closetx002", AmountSat: 490000, NumConfirmations: 6, Timestamp: time.Now()},
				{TxHash: "randomtx", AmountSat: 50000, NumConfirmations: 6, Timestamp: time.Now()},
			},
			wantTagged:   map[string]string{"closetx002": "Channel Close"},
			wantUntagged: []string{"randomtx"},
		},
		{
			name: "channel_point_with_nonzero_output_index",
			channels: []Channel{
				{ChanID: 3, RemotePubKey: "pk3", ChannelPoint: "multitx:2", Active: true},
			},
			onchainTxns: []OnchainTx{
				{TxHash: "multitx", AmountSat: -1000000, NumConfirmations: 6, Timestamp: time.Now()},
			},
			wantTagged: map[string]string{"multitx": "Channel Open"},
		},
		{
			name: "open_and_close_both_present",
			channels: []Channel{
				{ChanID: 4, RemotePubKey: "pk4", ChannelPoint: "openfund:0", Active: true},
				{ChanID: 5, RemotePubKey: "pk5", ChannelPoint: "oldfund:0", ClosingTxHash: "closehash5", Active: false},
			},
			onchainTxns: []OnchainTx{
				{TxHash: "openfund", AmountSat: -200000, NumConfirmations: 6, Timestamp: time.Now()},
				{TxHash: "closehash5", AmountSat: 190000, NumConfirmations: 6, Timestamp: time.Now()},
			},
			wantTagged: map[string]string{
				"openfund":   "Channel Open",
				"closehash5": "Channel Close",
			},
		},
		{
			name: "unconfirmed_tx_not_tagged",
			channels: []Channel{
				{ChanID: 6, RemotePubKey: "pk6", ChannelPoint: "pendingtx:0", Active: true},
			},
			onchainTxns: []OnchainTx{
				// 0 confirmations — excluded from btc_transactions_v
				{TxHash: "pendingtx", AmountSat: -300000, NumConfirmations: 0, Timestamp: time.Now()},
			},
			wantUntagged: []string{"pendingtx"},
		},
		{
			name: "no_channels_nothing_tagged",
			channels: []Channel{},
			onchainTxns: []OnchainTx{
				{TxHash: "sometx", AmountSat: 100000, NumConfirmations: 6, Timestamp: time.Now()},
			},
			wantUntagged: []string{"sometx"},
		},
		{
			name: "user_note_preserved_on_second_run",
			channels: []Channel{
				{ChanID: 7, RemotePubKey: "pk7", ChannelPoint: "usernoted:0", Active: true},
			},
			onchainTxns: []OnchainTx{
				{TxHash: "usernoted", AmountSat: -400000, NumConfirmations: 6, Timestamp: time.Now()},
			},
			wantTagged: map[string]string{"usernoted": "Channel Open"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDB(t)
			defer d.Close()
			ctx := context.Background()

			err := d.RunSync(ctx, func(tx SyncTx) error {
				if len(tc.channels) > 0 {
					if err := tx.UpsertChannels(tc.channels); err != nil {
						return err
					}
				}
				if len(tc.onchainTxns) > 0 {
					if err := tx.UpsertOnchainTxns(tc.onchainTxns); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("seed: %v", err)
			}

			_, err = d.AutoTagChannelTransfers(ctx)
			if err != nil {
				t.Fatalf("AutoTagChannelTransfers: %v", err)
			}

			for sourceID, wantNote := range tc.wantTagged {
				flagged, err := d.GetTransferFlag(ctx, sourceID)
				if err != nil {
					t.Fatalf("GetTransferFlag(%q): %v", sourceID, err)
				}
				if !flagged {
					t.Errorf("%s: expected %q to be tagged as transfer", tc.name, sourceID)
				}
				note, err := d.GetTransactionNote(ctx, sourceID)
				if err != nil {
					t.Fatalf("GetTransactionNote(%q): %v", sourceID, err)
				}
				if note != wantNote {
					t.Errorf("%s: note for %q = %q, want %q", tc.name, sourceID, note, wantNote)
				}
			}

			for _, sourceID := range tc.wantUntagged {
				flagged, err := d.GetTransferFlag(ctx, sourceID)
				if err != nil {
					t.Fatalf("GetTransferFlag(%q): %v", sourceID, err)
				}
				if flagged {
					t.Errorf("%s: expected %q to NOT be tagged as transfer", tc.name, sourceID)
				}
			}
		})
	}
}

// TestAutoTagChannelTransfers_ExchangeImportsNotTagged verifies exchange imports are never auto-tagged.
func TestAutoTagChannelTransfers_ExchangeImportsNotTagged(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Seed a channel whose channel_point txid matches the Strike transaction ID.
	// The auto-tagger must not tag the exchange import row.
	err := d.RunSync(ctx, func(tx SyncTx) error {
		return tx.UpsertChannels([]Channel{{
			ChanID:       99,
			RemotePubKey: "pkX",
			ChannelPoint: "exchgtx001:0",
			Active:       true,
		}})
	})
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// Import a Strike row whose TransactionID happens to equal the channel open tx_hash.
	_, err = d.ImportStrikeCSV(ctx, []exchange.StrikeRow{
		{TransactionID: "exchgtx001", Type: "Purchase", AmountBTC: 0.001, AmountUSD: 65, CostBasisUSD: 65, Date: time.Now()},
	})
	if err != nil {
		t.Fatalf("seed exchange import: %v", err)
	}

	_, err = d.AutoTagChannelTransfers(ctx)
	if err != nil {
		t.Fatalf("AutoTagChannelTransfers: %v", err)
	}

	// Exchange import source_id format in the view is "strike:exchgtx001:Purchase".
	// The auto-tag only operates on lnd_onchain source. Verify the exchange row is untouched.
	flagged, err := d.GetTransferFlag(ctx, "strike:exchgtx001:Purchase")
	if err != nil {
		t.Fatalf("GetTransferFlag: %v", err)
	}
	if flagged {
		t.Error("exchange import rows must NOT be auto-tagged as transfers")
	}
}

// TestAutoTagChannelTransfers_UserNotePreserved verifies that a user-set note is not
// overwritten by the auto-tagger on subsequent runs.
func TestAutoTagChannelTransfers_UserNotePreserved(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	err := d.RunSync(ctx, func(tx SyncTx) error {
		if err := tx.UpsertChannels([]Channel{{
			ChanID:       100,
			RemotePubKey: "pkY",
			ChannelPoint: "customtx:0",
			Active:       true,
		}}); err != nil {
			return err
		}
		return tx.UpsertOnchainTxns([]OnchainTx{
			{TxHash: "customtx", AmountSat: -100000, NumConfirmations: 6, Timestamp: time.Now()},
		})
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// First auto-tag run — sets note to "Channel Open"
	if _, err := d.AutoTagChannelTransfers(ctx); err != nil {
		t.Fatalf("first AutoTagChannelTransfers: %v", err)
	}

	// User overwrites the note
	if err := d.SetTransactionNote(ctx, "customtx", "My custom note"); err != nil {
		t.Fatalf("SetTransactionNote: %v", err)
	}

	// Second auto-tag run should be idempotent (already is_transfer=1, so no update)
	n, err := d.AutoTagChannelTransfers(ctx)
	if err != nil {
		t.Fatalf("second AutoTagChannelTransfers: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows affected on second run, got %d", n)
	}

	// User note must be preserved
	note, err := d.GetTransactionNote(ctx, "customtx")
	if err != nil {
		t.Fatalf("GetTransactionNote: %v", err)
	}
	if note != "My custom note" {
		t.Errorf("user note was overwritten; got %q, want %q", note, "My custom note")
	}
}

// --- Alert history ---

func TestRecordAlert_And_HasAlertedRecently(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Not alerted yet
	ok, err := d.HasAlertedRecently(ctx, "channel_close", "123", time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected false before any alert")
	}

	// Record an alert
	if err := d.RecordAlert(ctx, "channel_close", "123", "Channel 123 closed"); err != nil {
		t.Fatalf("RecordAlert: %v", err)
	}

	// Now should be alerted
	ok, err = d.HasAlertedRecently(ctx, "channel_close", "123", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected true after recording alert")
	}

	// Different type — should not count
	ok, err = d.HasAlertedRecently(ctx, "low_balance", "123", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("different alert type should not match")
	}

	// Future `since` — should not count
	ok, err = d.HasAlertedRecently(ctx, "channel_close", "123", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("alert recorded before `since` should not count")
	}
}

func TestListAlertHistory(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Empty
	records, err := d.ListAlertHistory(ctx, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected empty list, got %d records", len(records))
	}

	// Insert two records
	if err := d.RecordAlert(ctx, "daily_summary", "2026-01-01", "msg1"); err != nil {
		t.Fatalf("RecordAlert: %v", err)
	}
	if err := d.RecordAlert(ctx, "fee_spike", "2026-01-01", "msg2"); err != nil {
		t.Fatalf("RecordAlert: %v", err)
	}

	records, err = d.ListAlertHistory(ctx, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}
	// Respect limit
	records, err = d.ListAlertHistory(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record with limit=1, got %d", len(records))
	}
}

func TestChannelsWithClosingTx(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// No channels yet
	chs, err := d.ChannelsWithClosingTx(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 0 {
		t.Errorf("expected 0, got %d", len(chs))
	}

	// Seed one open and one closed channel
	seedChannels(t, d, []Channel{
		{ChanID: 1, RemotePubKey: "pk1", ChannelPoint: "tx:0", Capacity: 1_000_000, LocalBalance: 500_000, Active: true},
		{ChanID: 2, RemotePubKey: "pk2", ChannelPoint: "tx:1", Capacity: 500_000, LocalBalance: 0, Active: false, ClosingTxHash: "deadbeef"},
	})

	chs, err = d.ChannelsWithClosingTx(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("expected 1 closed channel, got %d", len(chs))
	}
	if chs[0].ChanID != 2 {
		t.Errorf("expected chan 2, got %d", chs[0].ChanID)
	}
	if chs[0].ClosingTxHash != "deadbeef" {
		t.Errorf("expected closing_tx_hash 'deadbeef', got %q", chs[0].ClosingTxHash)
	}
}

func TestChannelsBelowBalancePct(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	seedChannels(t, d, []Channel{
		// 5% local — below 10% threshold
		{ChanID: 10, RemotePubKey: "pk10", ChannelPoint: "tx:10", Capacity: 1_000_000, LocalBalance: 50_000, Active: true},
		// 50% local — above threshold
		{ChanID: 11, RemotePubKey: "pk11", ChannelPoint: "tx:11", Capacity: 1_000_000, LocalBalance: 500_000, Active: true},
		// 5% but inactive — should not appear
		{ChanID: 12, RemotePubKey: "pk12", ChannelPoint: "tx:12", Capacity: 1_000_000, LocalBalance: 50_000, Active: false},
	})

	chs, err := d.ChannelsBelowBalancePct(ctx, 0.10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chs) != 1 {
		t.Fatalf("expected 1 channel below threshold, got %d", len(chs))
	}
	if chs[0].ChanID != 10 {
		t.Errorf("expected chan 10, got %d", chs[0].ChanID)
	}
}

func TestFeesMsatSince(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	now := time.Now().UTC()
	seedForwardingEvents(t, d, []ForwardingEvent{
		{Timestamp: now.Add(-2 * time.Hour), AmtInMsat: 100_000, AmtOutMsat: 99_000, FeeMsat: 1_000},
		{Timestamp: now.Add(-10 * time.Hour), AmtInMsat: 200_000, AmtOutMsat: 198_000, FeeMsat: 2_000},
	})

	// All fees (since epoch)
	total, err := d.FeesMsatSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 3_000 {
		t.Errorf("expected 3000 msat total, got %d", total)
	}

	// Only the recent one (within last 5 hours)
	recent, err := d.FeesMsatSince(ctx, now.Add(-5*time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recent != 1_000 {
		t.Errorf("expected 1000 msat recent, got %d", recent)
	}

	// Nothing in the future
	future, err := d.FeesMsatSince(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if future != 0 {
		t.Errorf("expected 0, got %d", future)
	}
}

