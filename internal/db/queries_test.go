package db

import (
	"context"
	"testing"
	"time"
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
