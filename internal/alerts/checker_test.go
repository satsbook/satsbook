package alerts

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/db"
)

// mockStore implements Store for testing.
type mockStore struct {
	closedChannels []Channel
	lowChannels    []Channel
	fees24h        int64
	fees7d         int64
	alerted        map[string]bool
	recorded       []string
}

func newMockStore() *mockStore {
	return &mockStore{alerted: map[string]bool{}}
}

func (m *mockStore) ChannelsWithClosingTx(_ context.Context) ([]Channel, error) {
	return m.closedChannels, nil
}
func (m *mockStore) ChannelsBelowBalancePct(_ context.Context, _ float64) ([]Channel, error) {
	return m.lowChannels, nil
}
func (m *mockStore) FeesMsatSince(_ context.Context, since time.Time) (int64, error) {
	if time.Since(since) < 25*time.Hour {
		return m.fees24h, nil
	}
	return m.fees7d, nil
}
func (m *mockStore) HasAlertedRecently(_ context.Context, alertType, externalID string, _ time.Time) (bool, error) {
	return m.alerted[alertType+"/"+externalID], nil
}
func (m *mockStore) RecordAlert(_ context.Context, alertType, externalID, message string) error {
	m.recorded = append(m.recorded, alertType+"/"+externalID+": "+message)
	return nil
}

// mockSender captures sent messages.
type mockSender struct {
	sent []string
	err  error
}

func (m *mockSender) SendMessage(_ context.Context, text string) error {
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, text)
	return nil
}

func newChecker(store *mockStore, sender *mockSender) *Checker {
	return New(store, sender, log.New(io.Discard, "", 0))
}

func TestCheckChannelCloses_FiresOnce(t *testing.T) {
	store := newMockStore()
	store.closedChannels = []Channel{
		{ChanID: 111, RemotePubKey: "abcdef1234567890abcdef1234567890", Capacity: 1_000_000},
	}
	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	if len(sender.sent) == 0 {
		t.Fatal("expected alert to be sent")
	}
	if !strings.Contains(sender.sent[0], "Channel Closed") {
		t.Errorf("expected channel close alert, got: %s", sender.sent[0])
	}
	if !strings.Contains(sender.sent[0], "111") {
		t.Errorf("expected channel ID in message, got: %s", sender.sent[0])
	}
}

func TestCheckChannelCloses_NoRepeat(t *testing.T) {
	store := newMockStore()
	store.closedChannels = []Channel{
		{ChanID: 111, RemotePubKey: "abcdef1234567890", Capacity: 1_000_000},
	}
	store.alerted["channel_close/111"] = true
	// suppress other alerts
	today := time.Now().Format("2006-01-02")
	store.alerted["daily_summary/"+today] = true

	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	for _, msg := range sender.sent {
		if strings.Contains(msg, "Channel Closed") {
			t.Errorf("expected no repeat channel close alert, got: %s", msg)
		}
	}
}

func TestCheckLowBalance_FiresAlert(t *testing.T) {
	store := newMockStore()
	store.lowChannels = []Channel{
		{ChanID: 222, RemotePubKey: "pubkey222", Capacity: 1_000_000, LocalBalance: 50_000},
	}
	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	found := false
	for _, msg := range sender.sent {
		if strings.Contains(msg, "Low Channel Balance") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected low balance alert, got: %v", sender.sent)
	}
}

func TestCheckLowBalance_NoRepeatWithin24h(t *testing.T) {
	store := newMockStore()
	store.lowChannels = []Channel{
		{ChanID: 222, RemotePubKey: "pk", Capacity: 1_000_000, LocalBalance: 50_000},
	}
	store.alerted["low_balance/222"] = true
	today := time.Now().Format("2006-01-02")
	store.alerted["daily_summary/"+today] = true

	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	for _, msg := range sender.sent {
		if strings.Contains(msg, "Low Channel Balance") {
			t.Errorf("expected no repeat alert within 24h, got: %s", msg)
		}
	}
}

func TestCheckFeeSpike_FiresWhenAboveThreshold(t *testing.T) {
	store := newMockStore()
	// avg24h from 7d = 1_000_000 / 7 ≈ 142_857; fees24h = 400_000 ≈ 2.8x → spike
	store.fees7d = 1_000_000
	store.fees24h = 400_000

	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	found := false
	for _, msg := range sender.sent {
		if strings.Contains(msg, "Fee Spike") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected fee spike alert, got: %v", sender.sent)
	}
}

func TestCheckFeeSpike_NoAlertBelowThreshold(t *testing.T) {
	store := newMockStore()
	store.fees7d = 1_000_000
	store.fees24h = 100_000 // below 2× of avg ≈ 142k

	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	for _, msg := range sender.sent {
		if strings.Contains(msg, "Fee Spike") {
			t.Errorf("expected no fee spike, got: %s", msg)
		}
	}
}

func TestCheckFeeSpike_NoAlertWhenZeroAvg(t *testing.T) {
	store := newMockStore()
	store.fees7d = 0
	store.fees24h = 999_999

	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	for _, msg := range sender.sent {
		if strings.Contains(msg, "Fee Spike") {
			t.Errorf("no spike expected when avg is zero, got: %s", msg)
		}
	}
}

func TestCheckDailySummary_SendsOnce(t *testing.T) {
	store := newMockStore()
	store.fees24h = 5000
	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	found := false
	for _, msg := range sender.sent {
		if strings.Contains(msg, "Daily Node Summary") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected daily summary, got: %v", sender.sent)
	}
}

func TestCheckDailySummary_NoRepeat(t *testing.T) {
	store := newMockStore()
	today := time.Now().Format("2006-01-02")
	store.alerted["daily_summary/"+today] = true

	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	for _, msg := range sender.sent {
		if strings.Contains(msg, "Daily Node Summary") {
			t.Errorf("expected no repeat daily summary, got: %s", msg)
		}
	}
}

func TestAlertRecorded(t *testing.T) {
	store := newMockStore()
	store.closedChannels = []Channel{
		{ChanID: 333, RemotePubKey: "pk333", Capacity: 500_000},
	}
	sender := &mockSender{}
	c := newChecker(store, sender)
	c.Check(context.Background())

	found := false
	for _, r := range store.recorded {
		if strings.Contains(r, "channel_close/333") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected alert to be recorded, got: %v", store.recorded)
	}
}

func TestSendFailure_DoesNotPanic(t *testing.T) {
	store := newMockStore()
	store.closedChannels = []Channel{
		{ChanID: 444, RemotePubKey: "pk", Capacity: 100_000},
	}
	sender := &mockSender{err: fmt.Errorf("network error")}
	c := newChecker(store, sender)
	// Should not panic — errors are logged
	c.Check(context.Background())
}

func TestFormatSats(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1_000_000, "1,000,000"},
		{21_000_000_00000000, "2,100,000,000,000,000"},
		{-1000, "-1,000"},
	}
	for _, tt := range tests {
		got := formatSats(tt.in)
		if got != tt.want {
			t.Errorf("formatSats(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTruncatePubKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"short", "short"},
		{"abcdefgh12345678abcdefgh12345678", "abcdefgh...12345678"},
	}
	for _, tt := range tests {
		got := truncatePubKey(tt.in)
		if got != tt.want {
			t.Errorf("truncatePubKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- DBStore integration tests (issue #31) ---
// These tests exercise the DBStore adapter to verify it correctly bridges the
// alerts.Store interface to the underlying database methods.

func newTestAlertsDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestDBStore_NewDBStore verifies the constructor returns a non-nil store.
func TestDBStore_NewDBStore(t *testing.T) {
	d := newTestAlertsDB(t)
	s := NewDBStore(d)
	if s == nil {
		t.Fatal("expected non-nil DBStore")
	}
}

// TestDBStore_HasAlertedRecently_And_RecordAlert verifies the core dedup loop:
// HasAlertedRecently returns false before an alert is recorded, and true after.
// This is the mechanism that ensures "fires once per channel" (issue #31).
func TestDBStore_HasAlertedRecently_And_RecordAlert(t *testing.T) {
	ctx := context.Background()
	d := newTestAlertsDB(t)
	s := NewDBStore(d)

	// Not alerted yet
	alerted, err := s.HasAlertedRecently(ctx, string(TypeChannelClose), "chan-123", time.Time{})
	if err != nil {
		t.Fatalf("HasAlertedRecently: %v", err)
	}
	if alerted {
		t.Error("expected false before any alert is recorded")
	}

	// Record the alert
	if err := s.RecordAlert(ctx, string(TypeChannelClose), "chan-123", "test msg"); err != nil {
		t.Fatalf("RecordAlert: %v", err)
	}

	// Now HasAlertedRecently must return true — dedup prevents second send
	alerted, err = s.HasAlertedRecently(ctx, string(TypeChannelClose), "chan-123", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("HasAlertedRecently after record: %v", err)
	}
	if !alerted {
		t.Error("expected true after alert was recorded — dedup should block a second send")
	}
}

// TestDBStore_HasAlertedRecently_DifferentType verifies that alerts of different
// types do not interfere with each other's dedup state.
func TestDBStore_HasAlertedRecently_DifferentType(t *testing.T) {
	ctx := context.Background()
	d := newTestAlertsDB(t)
	s := NewDBStore(d)

	if err := s.RecordAlert(ctx, string(TypeChannelClose), "chan-1", "closed"); err != nil {
		t.Fatalf("RecordAlert: %v", err)
	}

	// LowBalance for same external ID must NOT be blocked by channel_close dedup
	alerted, err := s.HasAlertedRecently(ctx, string(TypeLowBalance), "chan-1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("HasAlertedRecently different type: %v", err)
	}
	if alerted {
		t.Error("low_balance dedup must not be affected by channel_close record")
	}
}

// TestDBStore_ChannelsWithClosingTx verifies the adapter correctly returns
// channels that have a closing tx hash set.
func TestDBStore_ChannelsWithClosingTx(t *testing.T) {
	ctx := context.Background()
	d := newTestAlertsDB(t)
	s := NewDBStore(d)

	// Seed channels via the db SyncTx interface
	err := d.RunSync(ctx, func(tx db.SyncTx) error {
		return tx.UpsertChannels([]db.Channel{
			{ChanID: 1, RemotePubKey: "pk1", ChannelPoint: "tx:0", Capacity: 1_000_000, LocalBalance: 500_000, Active: true},
			{ChanID: 2, RemotePubKey: "pk2", ChannelPoint: "tx:1", Capacity: 500_000, LocalBalance: 0, Active: false, ClosingTxHash: "deadbeef"},
		})
	})
	if err != nil {
		t.Fatalf("seed channels: %v", err)
	}

	channels, err := s.ChannelsWithClosingTx(ctx)
	if err != nil {
		t.Fatalf("ChannelsWithClosingTx: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("expected 1 closing channel, got %d", len(channels))
	}
	if channels[0].ChanID != 2 {
		t.Errorf("expected chan ID 2, got %d", channels[0].ChanID)
	}
}

// TestDBStore_ChannelsBelowBalancePct verifies the adapter correctly applies
// the balance threshold (issue #31: "Low balance — any channel local balance <10% capacity").
func TestDBStore_ChannelsBelowBalancePct(t *testing.T) {
	ctx := context.Background()
	d := newTestAlertsDB(t)
	s := NewDBStore(d)

	err := d.RunSync(ctx, func(tx db.SyncTx) error {
		return tx.UpsertChannels([]db.Channel{
			// 5% — below 10% threshold
			{ChanID: 10, RemotePubKey: "pk10", ChannelPoint: "tx:10", Capacity: 1_000_000, LocalBalance: 50_000, Active: true},
			// 50% — above threshold
			{ChanID: 11, RemotePubKey: "pk11", ChannelPoint: "tx:11", Capacity: 1_000_000, LocalBalance: 500_000, Active: true},
		})
	})
	if err != nil {
		t.Fatalf("seed channels: %v", err)
	}

	channels, err := s.ChannelsBelowBalancePct(ctx, 0.10)
	if err != nil {
		t.Fatalf("ChannelsBelowBalancePct: %v", err)
	}
	if len(channels) != 1 {
		t.Fatalf("expected 1 low-balance channel, got %d", len(channels))
	}
	if channels[0].ChanID != 10 {
		t.Errorf("expected chan 10, got %d", channels[0].ChanID)
	}
}

// TestDBStore_FeesMsatSince verifies the adapter correctly sums routing fees
// since the given time. Used by fee spike detection (issue #31).
func TestDBStore_FeesMsatSince(t *testing.T) {
	ctx := context.Background()
	d := newTestAlertsDB(t)
	s := NewDBStore(d)

	now := time.Now().UTC()
	err := d.RunSync(ctx, func(tx db.SyncTx) error {
		return tx.InsertForwardingEvents([]db.ForwardingEvent{
			{Timestamp: now.Add(-1 * time.Hour), AmtInMsat: 10_000, AmtOutMsat: 9_000, FeeMsat: 1_000},
			{Timestamp: now.Add(-30 * time.Hour), AmtInMsat: 20_000, AmtOutMsat: 18_000, FeeMsat: 2_000},
		})
	})
	if err != nil {
		t.Fatalf("seed forwarding events: %v", err)
	}

	total, err := s.FeesMsatSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("FeesMsatSince (all): %v", err)
	}
	if total != 3_000 {
		t.Errorf("expected 3000 total msat, got %d", total)
	}

	recent, err := s.FeesMsatSince(ctx, now.Add(-5*time.Hour))
	if err != nil {
		t.Fatalf("FeesMsatSince (recent): %v", err)
	}
	if recent != 1_000 {
		t.Errorf("expected 1000 recent msat, got %d", recent)
	}
}
