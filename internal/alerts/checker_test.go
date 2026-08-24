package alerts

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"testing"
	"time"
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
