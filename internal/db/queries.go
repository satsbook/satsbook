package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// FeeSummary returns aggregate fee stats for forwarding events since the given time.
// Pass zero time for all-time stats.
func (d *DB) FeeSummary(ctx context.Context, since time.Time) (totalFeeMsat int64, routedCount int64, err error) {
	var query string
	var args []interface{}

	if since.IsZero() {
		query = `SELECT COALESCE(SUM(fee_msat), 0), COUNT(*) FROM forwarding_events`
	} else {
		query = `SELECT COALESCE(SUM(fee_msat), 0), COUNT(*) FROM forwarding_events WHERE timestamp >= ?`
		args = append(args, since)
	}

	err = d.db.QueryRowContext(ctx, query, args...).Scan(&totalFeeMsat, &routedCount)
	if err != nil {
		return 0, 0, fmt.Errorf("fee summary: %w", err)
	}
	return totalFeeMsat, routedCount, nil
}

// ActiveChannelCount returns the number of active channels.
func (d *DB) ActiveChannelCount(ctx context.Context) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM channels WHERE active = 1`,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("active channel count: %w", err)
	}
	return count, nil
}

// LatestWalletBalance returns the most recent wallet balance snapshot.
// Returns nil, nil if no snapshots exist.
func (d *DB) LatestWalletBalance(ctx context.Context) (*WalletBalanceSnapshot, error) {
	var s WalletBalanceSnapshot
	err := d.db.QueryRowContext(ctx,
		`SELECT captured_at, total_sat, confirmed_sat, unconfirmed_sat
		 FROM wallet_balance_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(&s.CapturedAt, &s.TotalSat, &s.ConfirmedSat, &s.UnconfirmedSat)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest wallet balance: %w", err)
	}
	return &s, nil
}

// ChannelStat holds per-channel stats for the dashboard.
type ChannelStat struct {
	ChanID               uint64
	RemotePubKey         string
	LocalBalance         int64
	RemoteBalance        int64
	Active               bool
	FeesEarnedAllTimeMsat int64
	FeesEarned30dMsat    int64
}

// ChannelStats returns per-channel stats with fee aggregations.
func (d *DB) ChannelStats(ctx context.Context) ([]ChannelStat, error) {
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)

	rows, err := d.db.QueryContext(ctx, `
		SELECT
			c.chan_id,
			c.remote_pubkey,
			c.local_balance,
			c.remote_balance,
			c.active,
			COALESCE(all_fees.total_fee, 0) AS fees_all_time,
			COALESCE(recent_fees.total_fee, 0) AS fees_30d
		FROM channels c
		LEFT JOIN (
			SELECT chan_id, SUM(fee_msat) AS total_fee FROM (
				SELECT chan_id_in AS chan_id, fee_msat FROM forwarding_events
				UNION ALL
				SELECT chan_id_out AS chan_id, fee_msat FROM forwarding_events
			) GROUP BY chan_id
		) all_fees ON all_fees.chan_id = c.chan_id
		LEFT JOIN (
			SELECT chan_id, SUM(fee_msat) AS total_fee FROM (
				SELECT chan_id_in AS chan_id, fee_msat FROM forwarding_events WHERE timestamp >= ?
				UNION ALL
				SELECT chan_id_out AS chan_id, fee_msat FROM forwarding_events WHERE timestamp >= ?
			) GROUP BY chan_id
		) recent_fees ON recent_fees.chan_id = c.chan_id
		ORDER BY fees_all_time DESC
	`, thirtyDaysAgo, thirtyDaysAgo)
	if err != nil {
		return nil, fmt.Errorf("channel stats: %w", err)
	}
	defer rows.Close()

	var stats []ChannelStat
	for rows.Next() {
		var s ChannelStat
		var active int
		if err := rows.Scan(&s.ChanID, &s.RemotePubKey, &s.LocalBalance, &s.RemoteBalance,
			&active, &s.FeesEarnedAllTimeMsat, &s.FeesEarned30dMsat); err != nil {
			return nil, fmt.Errorf("scan channel stat: %w", err)
		}
		s.Active = active == 1
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// ForwardingPage holds a page of forwarding events with total count.
type ForwardingPage struct {
	Events []ForwardingEvent
	Total  int64
}

// ForwardingEvents returns paginated forwarding events in a date range.
func (d *DB) ForwardingEvents(ctx context.Context, from, to time.Time, limit, offset int) (*ForwardingPage, error) {
	var total int64
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM forwarding_events WHERE timestamp >= ? AND timestamp <= ?`,
		from, to,
	).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("forwarding events count: %w", err)
	}

	rows, err := d.db.QueryContext(ctx,
		`SELECT timestamp, chan_id_in, chan_id_out, amt_in_msat, amt_out_msat, fee_msat
		 FROM forwarding_events
		 WHERE timestamp >= ? AND timestamp <= ?
		 ORDER BY timestamp DESC
		 LIMIT ? OFFSET ?`,
		from, to, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("forwarding events: %w", err)
	}
	defer rows.Close()

	var events []ForwardingEvent
	for rows.Next() {
		var e ForwardingEvent
		if err := rows.Scan(&e.Timestamp, &e.ChanIDIn, &e.ChanIDOut, &e.AmtInMsat, &e.AmtOutMsat, &e.FeeMsat); err != nil {
			return nil, fmt.Errorf("scan forwarding event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("forwarding events rows: %w", err)
	}

	return &ForwardingPage{Events: events, Total: total}, nil
}
