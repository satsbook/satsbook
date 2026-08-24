// Package alerts detects node events and dispatches Telegram notifications.
package alerts

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Type identifies a category of alert for dedup tracking.
type Type string

const (
	TypeChannelClose  Type = "channel_close"
	TypeLowBalance    Type = "low_balance"
	TypeFeeSpike      Type = "fee_spike"
	TypeDailySummary  Type = "daily_summary"

	// lowBalancePct is the local/capacity threshold for a low-balance alert.
	lowBalancePct = 0.10
	// feeSpikeMultiplier triggers when 24h fees exceed this multiple of the 7d average.
	feeSpikeMultiplier = 2.0
)

// Channel is a minimal channel representation for alert logic.
type Channel struct {
	ChanID        uint64
	RemotePubKey  string
	Capacity      int64
	LocalBalance  int64
	ClosingTxHash string
}

// Store is the database interface required by the Checker.
type Store interface {
	ChannelsWithClosingTx(ctx context.Context) ([]Channel, error)
	ChannelsBelowBalancePct(ctx context.Context, pct float64) ([]Channel, error)
	FeesMsatSince(ctx context.Context, since time.Time) (int64, error)
	HasAlertedRecently(ctx context.Context, alertType, externalID string, since time.Time) (bool, error)
	RecordAlert(ctx context.Context, alertType, externalID, message string) error
}

// Sender delivers a formatted message to the user.
type Sender interface {
	SendMessage(ctx context.Context, text string) error
}

// Checker runs alert checks after each sync cycle.
type Checker struct {
	store  Store
	sender Sender
	logger *log.Logger
}

// New creates a Checker that reads state from store and sends via sender.
func New(store Store, sender Sender, logger *log.Logger) *Checker {
	return &Checker{store: store, sender: sender, logger: logger}
}

// Check runs all alert checks. Errors are logged but do not propagate — a
// failing alert check should never block the sync cycle.
func (c *Checker) Check(ctx context.Context) {
	checks := []func(context.Context) error{
		c.checkChannelCloses,
		c.checkLowBalance,
		c.checkFeeSpike,
		c.checkDailySummary,
	}
	for _, fn := range checks {
		if err := fn(ctx); err != nil {
			c.logger.Printf("alert check failed: %v", err)
		}
	}
}

// checkChannelCloses fires once per channel when a closing_tx_hash first appears.
func (c *Checker) checkChannelCloses(ctx context.Context) error {
	channels, err := c.store.ChannelsWithClosingTx(ctx)
	if err != nil {
		return fmt.Errorf("channel closes: %w", err)
	}

	for _, ch := range channels {
		extID := fmt.Sprintf("%d", ch.ChanID)
		alerted, err := c.store.HasAlertedRecently(ctx, string(TypeChannelClose), extID, time.Time{})
		if err != nil {
			return fmt.Errorf("channel close has-alerted: %w", err)
		}
		if alerted {
			continue
		}

		msg := fmt.Sprintf(
			"🔴 *Channel Closed*\nChannel ID: `%d`\nPeer: `%s`\nCapacity: %s sats",
			ch.ChanID,
			truncatePubKey(ch.RemotePubKey),
			formatSats(ch.Capacity),
		)
		if err := c.send(ctx, string(TypeChannelClose), extID, msg); err != nil {
			return err
		}
	}
	return nil
}

// checkLowBalance fires at most once per 24 hours per channel.
func (c *Checker) checkLowBalance(ctx context.Context) error {
	channels, err := c.store.ChannelsBelowBalancePct(ctx, lowBalancePct)
	if err != nil {
		return fmt.Errorf("low balance: %w", err)
	}

	for _, ch := range channels {
		extID := fmt.Sprintf("%d", ch.ChanID)
		alerted, err := c.store.HasAlertedRecently(ctx, string(TypeLowBalance), extID, time.Now().Add(-24*time.Hour))
		if err != nil {
			return fmt.Errorf("low balance has-alerted: %w", err)
		}
		if alerted {
			continue
		}

		pct := 0.0
		if ch.Capacity > 0 {
			pct = float64(ch.LocalBalance) / float64(ch.Capacity) * 100
		}
		msg := fmt.Sprintf(
			"⚠️ *Low Channel Balance*\nChannel ID: `%d`\nPeer: `%s`\nLocal: %s sats (%.1f%% of capacity)",
			ch.ChanID,
			truncatePubKey(ch.RemotePubKey),
			formatSats(ch.LocalBalance),
			pct,
		)
		if err := c.send(ctx, string(TypeLowBalance), extID, msg); err != nil {
			return err
		}
	}
	return nil
}

// checkFeeSpike fires at most once per 24 hours when 24h fees exceed 2× the 7d average.
func (c *Checker) checkFeeSpike(ctx context.Context) error {
	now := time.Now()
	fees24h, err := c.store.FeesMsatSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return fmt.Errorf("fee spike 24h: %w", err)
	}
	fees7d, err := c.store.FeesMsatSince(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		return fmt.Errorf("fee spike 7d: %w", err)
	}

	avg24h := fees7d / 7
	if avg24h == 0 || fees24h < int64(float64(avg24h)*feeSpikeMultiplier) {
		return nil
	}

	extID := now.Format("2006-01-02")
	alerted, err := c.store.HasAlertedRecently(ctx, string(TypeFeeSpike), extID, now.Add(-24*time.Hour))
	if err != nil {
		return fmt.Errorf("fee spike has-alerted: %w", err)
	}
	if alerted {
		return nil
	}

	msg := fmt.Sprintf(
		"📈 *Routing Fee Spike*\nLast 24h: %s sats\n7d daily avg: %s sats\nYour node is routing above average — check fee settings.",
		formatSats(fees24h/1000),
		formatSats(avg24h/1000),
	)
	return c.send(ctx, string(TypeFeeSpike), extID, msg)
}

// checkDailySummary sends one summary per calendar day.
func (c *Checker) checkDailySummary(ctx context.Context) error {
	today := time.Now().Format("2006-01-02")
	alerted, err := c.store.HasAlertedRecently(ctx, string(TypeDailySummary), today, time.Now().Truncate(24*time.Hour))
	if err != nil {
		return fmt.Errorf("daily summary has-alerted: %w", err)
	}
	if alerted {
		return nil
	}

	now := time.Now()
	fees24h, err := c.store.FeesMsatSince(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return fmt.Errorf("daily summary fees: %w", err)
	}

	msg := fmt.Sprintf(
		"📊 *Daily Node Summary* — %s\nRouting fees (24h): *%s sats*",
		today,
		formatSats(fees24h/1000),
	)
	return c.send(ctx, string(TypeDailySummary), today, msg)
}

// send delivers the message and records it in the alert history.
func (c *Checker) send(ctx context.Context, alertType, externalID, message string) error {
	if err := c.sender.SendMessage(ctx, message); err != nil {
		return fmt.Errorf("send alert %s/%s: %w", alertType, externalID, err)
	}
	if err := c.store.RecordAlert(ctx, alertType, externalID, message); err != nil {
		// Non-fatal: message was sent; just log the recording failure.
		c.logger.Printf("alert: failed to record %s/%s: %v", alertType, externalID, err)
	}
	return nil
}

// truncatePubKey shortens a public key for display.
func truncatePubKey(pk string) string {
	if len(pk) <= 16 {
		return pk
	}
	return pk[:8] + "..." + pk[len(pk)-8:]
}

// formatSats formats an integer as a comma-separated satoshi count.
func formatSats(n int64) string {
	if n < 0 {
		return "-" + formatSats(-n)
	}
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(ch))
	}
	return string(out)
}
