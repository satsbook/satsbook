package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/satsbook/satsbook/internal/exchange"
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

// DailyFeeStat holds aggregated fee data for a single day.
type DailyFeeStat struct {
	Day          string
	TotalFeeMsat int64
	Count        int64
}

// DailyFees returns fee totals grouped by day since the given time.
func (d *DB) DailyFees(ctx context.Context, since time.Time) ([]DailyFeeStat, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT SUBSTR(timestamp, 1, 10) AS day, SUM(fee_msat) AS total_fee_msat, COUNT(*) AS count
		FROM forwarding_events
		WHERE timestamp >= ?
		GROUP BY SUBSTR(timestamp, 1, 10)
		ORDER BY day ASC
	`, since)
	if err != nil {
		return nil, fmt.Errorf("daily fees: %w", err)
	}
	defer rows.Close()

	var stats []DailyFeeStat
	for rows.Next() {
		var s DailyFeeStat
		if err := rows.Scan(&s.Day, &s.TotalFeeMsat, &s.Count); err != nil {
			return nil, fmt.Errorf("scan daily fee: %w", err)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// LastSyncedAt returns the most recent sync timestamp across all sources.
// Returns zero time if no syncs have occurred.
func (d *DB) LastSyncedAt(ctx context.Context) (time.Time, error) {
	var s sql.NullString
	err := d.db.QueryRowContext(ctx,
		`SELECT MAX(last_synced_at) FROM sync_state`,
	).Scan(&s)
	if err != nil {
		return time.Time{}, fmt.Errorf("last synced at: %w", err)
	}
	if !s.Valid || s.String == "" {
		return time.Time{}, nil
	}
	// Try multiple time formats — the SQLite driver stores Go time values
	// in Go's default format, not RFC3339.
	formats := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 +0000 UTC",
		time.RFC3339Nano,
		"2006-01-02 15:04:05-07:00",
		"2006-01-02T15:04:05Z",
	}
	var t time.Time
	var parseErr error
	for _, f := range formats {
		t, parseErr = time.Parse(f, s.String)
		if parseErr == nil {
			break
		}
	}
	if parseErr != nil {
		return time.Time{}, fmt.Errorf("parse last synced time %q: %w", s.String, parseErr)
	}
	return t, nil
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

// ExchangeSummaryResult holds aggregated exchange activity for a period.
type ExchangeSummaryResult struct {
	PurchasedSats        int64
	ReceivedSats         int64
	SoldSats             int64   // positive value (absolute)
	SentSats             int64   // positive value (absolute)
	TotalCostBasisUSD    float64 // USD spent on purchases
	TotalSaleProceedsUSD float64 // USD received from sales
	FeesPaidUSD          float64
}

// ExchangeSummary returns aggregated BTC in/out and USD cost/proceeds
// for a given exchange source since the given time.
func (d *DB) ExchangeSummary(ctx context.Context, source string, since time.Time) (*ExchangeSummaryResult, error) {
	sinceStr := ""
	args := []interface{}{source}
	dateFilter := ""
	if !since.IsZero() {
		sinceStr = since.UTC().Format(time.RFC3339)
		dateFilter = `AND json_extract(raw_data, '$.Date') >= ?`
		args = append(args, sinceStr)
	}

	query := `SELECT
		COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data, '$.Type')) IN ('purchase', 'buy')
			THEN json_extract(raw_data, '$.AmountBTC') ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data, '$.Type')) = 'receive'
			THEN json_extract(raw_data, '$.AmountBTC') ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data, '$.Type')) = 'sale'
			THEN ABS(json_extract(raw_data, '$.AmountBTC')) ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data, '$.Type')) = 'send'
			THEN ABS(json_extract(raw_data, '$.AmountBTC')) ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data, '$.Type')) IN ('purchase', 'buy')
			THEN ABS(COALESCE(json_extract(raw_data, '$.CostBasisUSD'), json_extract(raw_data, '$.AmountUSD'), 0)) ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data, '$.Type')) = 'sale'
			THEN ABS(COALESCE(json_extract(raw_data, '$.AmountUSD'), 0)) ELSE 0 END), 0),
		COALESCE(SUM(ABS(COALESCE(json_extract(raw_data, '$.FeeUSD'), 0))), 0)
	FROM exchange_imports
	WHERE source = ? ` + dateFilter

	var purchBTC, recvBTC, soldBTC, sentBTC float64
	var costBasis, saleProceeds, fees float64

	err := d.db.QueryRowContext(ctx, query, args...).Scan(
		&purchBTC, &recvBTC, &soldBTC, &sentBTC,
		&costBasis, &saleProceeds, &fees,
	)
	if err != nil {
		return nil, fmt.Errorf("exchange summary for %s: %w", source, err)
	}

	return &ExchangeSummaryResult{
		PurchasedSats:        int64(math.Round(purchBTC * 1e8)),
		ReceivedSats:         int64(math.Round(recvBTC * 1e8)),
		SoldSats:             int64(math.Round(soldBTC * 1e8)),
		SentSats:             int64(math.Round(sentBTC * 1e8)),
		TotalCostBasisUSD:    costBasis,
		TotalSaleProceedsUSD: saleProceeds,
		FeesPaidUSD:          fees,
	}, nil
}

// ImportSummary holds the result of an exchange CSV import.
type ImportSummary struct {
	Total        int
	NewPurchases int
	Duplicates   int
}

// ImportStrikeCSV imports parsed Strike CSV rows into exchange_imports and btc_lots.
// The entire operation runs in a single transaction.
func (d *DB) ImportStrikeCSV(ctx context.Context, rows []exchange.StrikeRow) (*ImportSummary, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin import transaction: %w", err)
	}
	defer tx.Rollback()

	summary := &ImportSummary{Total: len(rows)}

	for _, row := range rows {
		rawData, _ := json.Marshal(row)

		// Use content hash for dedup — Strike reference IDs are not unique
		// (same ref can appear for reversals, multi-part transactions, etc.)
		h := sha256.Sum256([]byte(row.RawLine))
		contentHash := hex.EncodeToString(h[:])

		// Insert into exchange_imports (dedup via UNIQUE(source, external_id, tx_type))
		// external_id is the content hash so re-importing the same CSV is idempotent
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO exchange_imports (source, external_id, tx_type, raw_data)
			 VALUES (?, ?, ?, ?)`,
			"strike", contentHash, row.Type, string(rawData),
		)
		if err != nil {
			return nil, fmt.Errorf("insert exchange import %q: %w", row.TransactionID, err)
		}

		affected, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("rows affected for %q: %w", row.TransactionID, err)
		}

		if affected == 0 {
			summary.Duplicates++
			continue
		}

		// Create btc_lot for completed purchases
		if row.IsPurchase() && row.AmountSat > 0 {
			// Dedup check: don't create lot if one already exists for this content hash
			var exists int
			err := tx.QueryRowContext(ctx,
				`SELECT 1 FROM btc_lots WHERE source = ? AND external_id = ?`,
				"strike", contentHash,
			).Scan(&exists)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("check btc_lot %q: %w", contentHash, err)
			}
			if errors.Is(err, sql.ErrNoRows) {
				// Prefer cost basis from Strike; fall back to amount USD
				priceUSD := row.CostBasisUSD
				if priceUSD == 0 {
					priceUSD = row.AmountUSD
				}
				if priceUSD == 0 {
					// No cost basis available, skip lot creation
					continue
				}
				_, err = tx.ExecContext(ctx,
					`INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id)
					 VALUES (?, ?, ?, ?, ?)`,
					row.Date, row.AmountSat, priceUSD, "strike", contentHash,
				)
				if err != nil {
					return nil, fmt.Errorf("insert btc_lot %q: %w", contentHash, err)
				}
				summary.NewPurchases++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}

	return summary, nil
}

// ExchangeBalance returns the net BTC balance (in sats) for a given exchange source
// by summing AmountBTC only from rows that actually move BTC (non-zero AmountBTC).
// USD-only transactions (e.g. cash withdrawals) are excluded.
func (d *DB) ExchangeBalance(ctx context.Context, source string) (int64, error) {
	var totalBTC sql.NullFloat64
	err := d.db.QueryRowContext(ctx,
		`SELECT SUM(json_extract(raw_data, '$.AmountBTC'))
		 FROM exchange_imports
		 WHERE source = ?
		   AND COALESCE(json_extract(raw_data, '$.AmountBTC'), 0) != 0`, source,
	).Scan(&totalBTC)
	if err != nil {
		return 0, fmt.Errorf("exchange balance for %s: %w", source, err)
	}
	if !totalBTC.Valid {
		return 0, nil
	}
	return int64(math.Round(totalBTC.Float64 * 1e8)), nil
}
