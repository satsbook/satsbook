package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
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
	// Strip Go monotonic clock suffix (e.g. " m=+0.015998844") before parsing.
	if idx := strings.Index(s.String, " m="); idx != -1 {
		s.String = s.String[:idx]
	}
	// Try multiple time formats — the SQLite driver stores Go time values
	// in Go's default format, not RFC3339.
	formats := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 +0000 UTC",
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
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
	Updated      int
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

		// Use TransactionID (Strike "Reference") as the key field.
		// Same reference can appear with different tx_types (e.g. Sale + Withdrawal).
		externalID := row.TransactionID

		// Check if this row already exists
		var existingRaw string
		err := tx.QueryRowContext(ctx,
			`SELECT raw_data FROM exchange_imports WHERE source = ? AND external_id = ? AND tx_type = ?`,
			"strike", externalID, row.Type,
		).Scan(&existingRaw)

		isNew := errors.Is(err, sql.ErrNoRows)
		if err != nil && !isNew {
			return nil, fmt.Errorf("check exchange import %q: %w", row.TransactionID, err)
		}

		rawStr := string(rawData)
		if !isNew && existingRaw == rawStr {
			summary.Duplicates++
			continue
		}

		if isNew {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO exchange_imports (source, external_id, tx_type, raw_data) VALUES (?, ?, ?, ?)`,
				"strike", externalID, row.Type, rawStr,
			)
		} else {
			_, err = tx.ExecContext(ctx,
				`UPDATE exchange_imports SET raw_data = ? WHERE source = ? AND external_id = ? AND tx_type = ?`,
				rawStr, "strike", externalID, row.Type,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("upsert exchange import %q: %w", row.TransactionID, err)
		}

		// Create or update btc_lot for completed purchases
		if row.IsPurchase() && row.AmountSat > 0 {
			priceUSD := row.CostBasisUSD
			if priceUSD == 0 {
				priceUSD = row.AmountUSD
			}
			if priceUSD == 0 {
				continue
			}

			var lotID int64
			err := tx.QueryRowContext(ctx,
				`SELECT id FROM btc_lots WHERE source = ? AND external_id = ?`,
				"strike", externalID,
			).Scan(&lotID)
			if errors.Is(err, sql.ErrNoRows) {
				_, err = tx.ExecContext(ctx,
					`INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id)
					 VALUES (?, ?, ?, ?, ?)`,
					row.Date, row.AmountSat, priceUSD, "strike", externalID,
				)
				if err != nil {
					return nil, fmt.Errorf("insert btc_lot %q: %w", externalID, err)
				}
				summary.NewPurchases++
			} else if err != nil {
				return nil, fmt.Errorf("check btc_lot %q: %w", externalID, err)
			} else {
				// Update cost basis if it changed
				_, err = tx.ExecContext(ctx,
					`UPDATE btc_lots SET price_usd = ? WHERE id = ?`,
					priceUSD, lotID,
				)
				if err != nil {
					return nil, fmt.Errorf("update btc_lot %q: %w", externalID, err)
				}
				summary.Updated++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}

	return summary, nil
}

// ImportRiverCSV imports parsed River CSV rows into exchange_imports and btc_lots.
// The entire operation runs in a single transaction.
func (d *DB) ImportRiverCSV(ctx context.Context, rows []exchange.RiverRow) (*ImportSummary, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin import transaction: %w", err)
	}
	defer tx.Rollback()

	summary := &ImportSummary{Total: len(rows)}

	for _, row := range rows {
		rawData, _ := json.Marshal(row)

		// River has no transaction ID; use composite key: date|type|amount_btc
		externalID := fmt.Sprintf("%s|%s|%.8f", row.Date.Format("2006-01-02T15:04:05"), row.Type, row.AmountBTC)

		var existingRaw string
		err := tx.QueryRowContext(ctx,
			`SELECT raw_data FROM exchange_imports WHERE source = ? AND external_id = ? AND tx_type = ?`,
			"river", externalID, row.Type,
		).Scan(&existingRaw)

		isNew := errors.Is(err, sql.ErrNoRows)
		if err != nil && !isNew {
			return nil, fmt.Errorf("check exchange import: %w", err)
		}

		rawStr := string(rawData)
		if !isNew && existingRaw == rawStr {
			summary.Duplicates++
			continue
		}

		if isNew {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO exchange_imports (source, external_id, tx_type, raw_data) VALUES (?, ?, ?, ?)`,
				"river", externalID, row.Type, rawStr,
			)
		} else {
			_, err = tx.ExecContext(ctx,
				`UPDATE exchange_imports SET raw_data = ? WHERE source = ? AND external_id = ? AND tx_type = ?`,
				rawStr, "river", externalID, row.Type,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("upsert exchange import: %w", err)
		}

		// Create or update btc_lot for completed purchases
		if row.IsPurchase() && row.AmountSat > 0 {
			priceUSD := row.CostBasisUSD
			if priceUSD == 0 {
				priceUSD = row.AmountUSD
			}
			if priceUSD == 0 {
				continue
			}

			var lotID int64
			err := tx.QueryRowContext(ctx,
				`SELECT id FROM btc_lots WHERE source = ? AND external_id = ?`,
				"river", externalID,
			).Scan(&lotID)
			if errors.Is(err, sql.ErrNoRows) {
				_, err = tx.ExecContext(ctx,
					`INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id)
					 VALUES (?, ?, ?, ?, ?)`,
					row.Date, row.AmountSat, priceUSD, "river", externalID,
				)
				if err != nil {
					return nil, fmt.Errorf("insert btc_lot %q: %w", externalID, err)
				}
				summary.NewPurchases++
			} else if err != nil {
				return nil, fmt.Errorf("check btc_lot %q: %w", externalID, err)
			} else {
				_, err = tx.ExecContext(ctx,
					`UPDATE btc_lots SET price_usd = ? WHERE id = ?`,
					priceUSD, lotID,
				)
				if err != nil {
					return nil, fmt.Errorf("update btc_lot %q: %w", externalID, err)
				}
				summary.Updated++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}

	return summary, nil
}

// ImportCoinbaseCSV imports parsed Coinbase CSV rows into exchange_imports and btc_lots.
// The entire operation runs in a single transaction.
func (d *DB) ImportCoinbaseCSV(ctx context.Context, rows []exchange.CoinbaseRow) (*ImportSummary, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin import transaction: %w", err)
	}
	defer tx.Rollback()

	summary := &ImportSummary{Total: len(rows)}

	for _, row := range rows {
		rawData, _ := json.Marshal(row)

		// Use stable API transaction ID when available; fall back to composite key for CSV rows.
		externalID := row.TransactionID
		if externalID == "" {
			externalID = fmt.Sprintf("%s|%s|%.8f", row.Date.Format("2006-01-02T15:04:05"), row.Type, row.AmountBTC)
		}

		var existingRaw string
		err := tx.QueryRowContext(ctx,
			`SELECT raw_data FROM exchange_imports WHERE source = ? AND external_id = ? AND tx_type = ?`,
			"coinbase", externalID, row.Type,
		).Scan(&existingRaw)

		isNew := errors.Is(err, sql.ErrNoRows)
		if err != nil && !isNew {
			return nil, fmt.Errorf("check exchange import: %w", err)
		}

		rawStr := string(rawData)
		if !isNew && existingRaw == rawStr {
			summary.Duplicates++
			continue
		}

		if isNew {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO exchange_imports (source, external_id, tx_type, raw_data) VALUES (?, ?, ?, ?)`,
				"coinbase", externalID, row.Type, rawStr,
			)
		} else {
			_, err = tx.ExecContext(ctx,
				`UPDATE exchange_imports SET raw_data = ? WHERE source = ? AND external_id = ? AND tx_type = ?`,
				rawStr, "coinbase", externalID, row.Type,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("upsert exchange import: %w", err)
		}

		// Create or update btc_lot for completed purchases
		if row.IsPurchase() && row.AmountSat > 0 {
			priceUSD := row.CostBasisUSD
			if priceUSD == 0 {
				priceUSD = row.AmountUSD
			}
			if priceUSD == 0 {
				continue
			}

			var lotID int64
			err := tx.QueryRowContext(ctx,
				`SELECT id FROM btc_lots WHERE source = ? AND external_id = ?`,
				"coinbase", externalID,
			).Scan(&lotID)
			if errors.Is(err, sql.ErrNoRows) {
				_, err = tx.ExecContext(ctx,
					`INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id)
					 VALUES (?, ?, ?, ?, ?)`,
					row.Date, row.AmountSat, priceUSD, "coinbase", externalID,
				)
				if err != nil {
					return nil, fmt.Errorf("insert btc_lot %q: %w", externalID, err)
				}
				summary.NewPurchases++
			} else if err != nil {
				return nil, fmt.Errorf("check btc_lot %q: %w", externalID, err)
			} else {
				_, err = tx.ExecContext(ctx,
					`UPDATE btc_lots SET price_usd = ? WHERE id = ?`,
					priceUSD, lotID,
				)
				if err != nil {
					return nil, fmt.Errorf("update btc_lot %q: %w", externalID, err)
				}
				summary.Updated++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}

	return summary, nil
}

// ImportSwanCSV imports parsed Swan CSV rows into exchange_imports and btc_lots.
// The entire operation runs in a single transaction.
func (d *DB) ImportSwanCSV(ctx context.Context, rows []exchange.SwanRow) (*ImportSummary, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin import transaction: %w", err)
	}
	defer tx.Rollback()

	summary := &ImportSummary{Total: len(rows)}

	for _, row := range rows {
		rawData, _ := json.Marshal(row)

		// Swan has TransactionID for transfers/withdrawals; use composite for trades
		externalID := row.TransactionID
		if externalID == "" {
			externalID = fmt.Sprintf("%s|%s|%.8f", row.Date.Format("2006-01-02T15:04:05"), row.Type, row.AmountBTC)
		}

		var existingRaw string
		err := tx.QueryRowContext(ctx,
			`SELECT raw_data FROM exchange_imports WHERE source = ? AND external_id = ? AND tx_type = ?`,
			"swan", externalID, row.Type,
		).Scan(&existingRaw)

		isNew := errors.Is(err, sql.ErrNoRows)
		if err != nil && !isNew {
			return nil, fmt.Errorf("check exchange import: %w", err)
		}

		rawStr := string(rawData)
		if !isNew && existingRaw == rawStr {
			summary.Duplicates++
			continue
		}

		if isNew {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO exchange_imports (source, external_id, tx_type, raw_data) VALUES (?, ?, ?, ?)`,
				"swan", externalID, row.Type, rawStr,
			)
		} else {
			_, err = tx.ExecContext(ctx,
				`UPDATE exchange_imports SET raw_data = ? WHERE source = ? AND external_id = ? AND tx_type = ?`,
				rawStr, "swan", externalID, row.Type,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("upsert exchange import: %w", err)
		}

		// Create or update btc_lot for purchases, deposits, and withdrawals
		shouldCreateLot := false
		var lotAmount int64
		var lotPrice float64

		switch {
		case row.IsPurchase() && row.AmountSat > 0:
			shouldCreateLot = true
			lotAmount = row.AmountSat
			lotPrice = row.CostBasisUSD
			if lotPrice == 0 {
				lotPrice = row.AmountUSD
			}
		case row.IsDeposit() && row.AmountSat > 0:
			shouldCreateLot = true
			lotAmount = row.AmountSat
			lotPrice = 0
		case row.IsWithdrawal() && row.AmountSat < 0:
			shouldCreateLot = true
			lotAmount = row.AmountSat
			lotPrice = 0
		}

		if shouldCreateLot && lotAmount != 0 {
			var lotID int64
			err := tx.QueryRowContext(ctx,
				`SELECT id FROM btc_lots WHERE source = ? AND external_id = ?`,
				"swan", externalID,
			).Scan(&lotID)
			if errors.Is(err, sql.ErrNoRows) {
				_, err = tx.ExecContext(ctx,
					`INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id)
					 VALUES (?, ?, ?, ?, ?)`,
					row.Date, lotAmount, lotPrice, "swan", externalID,
				)
				if err != nil {
					return nil, fmt.Errorf("insert btc_lot %q: %w", externalID, err)
				}
				if row.IsPurchase() {
					summary.NewPurchases++
				}
			} else if err != nil {
				return nil, fmt.Errorf("check btc_lot %q: %w", externalID, err)
			} else {
				_, err = tx.ExecContext(ctx,
					`UPDATE btc_lots SET price_usd = ? WHERE id = ?`,
					lotPrice, lotID,
				)
				if err != nil {
					return nil, fmt.Errorf("update btc_lot %q: %w", externalID, err)
				}
				summary.Updated++
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit import: %w", err)
	}

	return summary, nil
}

// SourceBalance holds per-source aggregated balances for the portfolio view.
type SourceBalance struct {
	Source           string
	NetSats          int64   // signed: purchases + receives - sales - sends
	PurchasedSats    int64   // for "purchased in period" stats
	TotalCostBasisUSD float64 // USD spent on purchases (CostBasisUSD fallback AmountUSD)
}

// PortfolioPositionResult holds the aggregated portfolio position across all sources.
type PortfolioPositionResult struct {
	BySource        map[string]SourceBalance
	ExchangeNetSats    int64   // sum of NetSats across all sources
	PurchasedSats      int64   // sum of PurchasedSats across all sources (for YTD purchased stat)
	TotalCostBasisUSD  float64 // sum of cost basis across all sources
	RoutingFeesSats    int64   // routing fees from forwarding_events in the period
	RoutedCount        int64   // number of routed payments in the period
}

// PortfolioPosition aggregates exchange balances per source and routing fees in a single
// pair of queries. Pass zero time for all-time stats.
//
// This collapses what was previously 3 ExchangeBalance + 1 FeeSummary calls into 2 round-trips.
func (d *DB) PortfolioPosition(ctx context.Context, since time.Time) (*PortfolioPositionResult, error) {
	result := &PortfolioPositionResult{
		BySource: make(map[string]SourceBalance),
	}

	// Query 1: aggregate exchange_imports by source.
	exchArgs := []interface{}{}
	dateFilter := ""
	if !since.IsZero() {
		dateFilter = `WHERE json_extract(raw_data, '$.Date') >= ?`
		exchArgs = append(exchArgs, since.UTC().Format(time.RFC3339))
	}

	exchQuery := `
		SELECT source,
			COALESCE(SUM(CASE WHEN COALESCE(json_extract(raw_data, '$.AmountBTC'), 0) != 0
				THEN json_extract(raw_data, '$.AmountBTC') ELSE 0 END), 0) AS net_btc,
			COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data, '$.Type')) IN ('purchase', 'buy')
				THEN json_extract(raw_data, '$.AmountBTC') ELSE 0 END), 0) AS purchased_btc,
			COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data, '$.Type')) IN ('purchase', 'buy')
				THEN ABS(COALESCE(json_extract(raw_data, '$.CostBasisUSD'),
					json_extract(raw_data, '$.AmountUSD'), 0)) ELSE 0 END), 0) AS cost_basis_usd
		FROM exchange_imports ` + dateFilter + `
		GROUP BY source`

	rows, err := d.db.QueryContext(ctx, exchQuery, exchArgs...)
	if err != nil {
		return nil, fmt.Errorf("portfolio position exchanges: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var source string
		var netBTC, purchasedBTC, costBasis float64
		if err := rows.Scan(&source, &netBTC, &purchasedBTC, &costBasis); err != nil {
			return nil, fmt.Errorf("scan portfolio source: %w", err)
		}
		sb := SourceBalance{
			Source:            source,
			NetSats:           int64(math.Round(netBTC * 1e8)),
			PurchasedSats:     int64(math.Round(purchasedBTC * 1e8)),
			TotalCostBasisUSD: costBasis,
		}
		result.BySource[source] = sb
		result.ExchangeNetSats += sb.NetSats
		result.PurchasedSats += sb.PurchasedSats
		result.TotalCostBasisUSD += costBasis
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("portfolio position rows: %w", err)
	}

	// For the current (all-time) position, override Strike's CSV-calculated balance with
	// the live API balance when available. Strike's CSV omits BTC amounts for
	// USD-denominated Lightning sends, so the summed AmountBTC overstates the balance.
	if since.IsZero() {
		if liveVal, _ := d.GetSetting(ctx, "strike_live_balance_sats"); liveVal != "" {
			if liveSats, err := strconv.ParseInt(liveVal, 10, 64); err == nil && liveSats > 0 {
				if sb, ok := result.BySource["strike"]; ok {
					result.ExchangeNetSats -= sb.NetSats
					sb.NetSats = liveSats
					result.BySource["strike"] = sb
					result.ExchangeNetSats += liveSats
				}
			}
		}
	}

	// Query 2: routing fees from forwarding_events.
	feeMsat, routedCount, err := d.FeeSummary(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("portfolio position fees: %w", err)
	}
	result.RoutingFeesSats = feeMsat / 1000
	result.RoutedCount = routedCount

	return result, nil
}

// validExchangeSources is the allowlist of source names accepted by destructive
// per-vendor operations. Keep in sync with the import handlers.
var validExchangeSources = map[string]bool{
	"strike":   true,
	"river":    true,
	"coinbase": true,
	"swan":     true,
}

// IsValidExchangeSource reports whether the given source is a known vendor.
func IsValidExchangeSource(source string) bool {
	return validExchangeSources[source]
}

// ClearExchangeResult summarises the rows removed by ClearExchangeSource.
type ClearExchangeResult struct {
	Source          string
	ImportsRemoved  int64
	LotsRemoved     int64
}

// ClearExchangeSource deletes all imported data for a single exchange source
// (both exchange_imports and btc_lots) in a single transaction. Other vendors
// and node-sourced tables are never touched.
func (d *DB) ClearExchangeSource(ctx context.Context, source string) (*ClearExchangeResult, error) {
	if !IsValidExchangeSource(source) {
		return nil, fmt.Errorf("clear exchange: invalid source %q", source)
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("clear exchange: begin tx: %w", err)
	}
	defer tx.Rollback()

	lotsRes, err := tx.ExecContext(ctx, `DELETE FROM btc_lots WHERE source = ?`, source)
	if err != nil {
		return nil, fmt.Errorf("clear exchange: delete btc_lots: %w", err)
	}
	lotsRemoved, err := lotsRes.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("clear exchange: btc_lots rows affected: %w", err)
	}

	impRes, err := tx.ExecContext(ctx, `DELETE FROM exchange_imports WHERE source = ?`, source)
	if err != nil {
		return nil, fmt.Errorf("clear exchange: delete exchange_imports: %w", err)
	}
	importsRemoved, err := impRes.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("clear exchange: exchange_imports rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("clear exchange: commit: %w", err)
	}

	return &ClearExchangeResult{
		Source:         source,
		ImportsRemoved: importsRemoved,
		LotsRemoved:    lotsRemoved,
	}, nil
}

// AddWallet inserts a new watched wallet. Returns the ID of the new row.
func (d *DB) AddWallet(ctx context.Context, label, walletType, value, derivationType string) (int64, error) {
	res, err := d.db.ExecContext(ctx,
		`INSERT INTO watched_wallets (label, type, value, derivation_type) VALUES (?, ?, ?, ?)`,
		label, walletType, value, derivationType,
	)
	if err != nil {
		return 0, fmt.Errorf("add wallet: %w", err)
	}
	return res.LastInsertId()
}

// RemoveWallet deletes a watched wallet by ID.
func (d *DB) RemoveWallet(ctx context.Context, id int64) error {
	res, err := d.db.ExecContext(ctx, `DELETE FROM watched_wallets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove wallet %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remove wallet %d rows affected: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("wallet %d not found", id)
	}
	return nil
}

// ListWallets returns all watched wallets ordered by label.
func (d *DB) ListWallets(ctx context.Context) ([]WatchedWallet, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, label, type, value, derivation_type, balance_sats, last_checked_at, created_at
		 FROM watched_wallets ORDER BY label ASC`)
	if err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	defer rows.Close()

	var wallets []WatchedWallet
	for rows.Next() {
		var w WatchedWallet
		var lastChecked sql.NullTime
		if err := rows.Scan(&w.ID, &w.Label, &w.Type, &w.Value, &w.DerivationType,
			&w.BalanceSats, &lastChecked, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan wallet: %w", err)
		}
		if lastChecked.Valid {
			w.LastCheckedAt = &lastChecked.Time
		}
		wallets = append(wallets, w)
	}
	return wallets, rows.Err()
}

// GetWallet returns a single watched wallet by ID.
func (d *DB) GetWallet(ctx context.Context, id int64) (*WatchedWallet, error) {
	var w WatchedWallet
	var lastChecked sql.NullTime
	err := d.db.QueryRowContext(ctx,
		`SELECT id, label, type, value, derivation_type, balance_sats, last_checked_at, created_at
		 FROM watched_wallets WHERE id = ?`, id,
	).Scan(&w.ID, &w.Label, &w.Type, &w.Value, &w.DerivationType,
		&w.BalanceSats, &lastChecked, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wallet %d: %w", id, err)
	}
	if lastChecked.Valid {
		w.LastCheckedAt = &lastChecked.Time
	}
	return &w, nil
}

// UpdateWalletBalance updates the balance and last_checked_at for a wallet.
func (d *DB) UpdateWalletBalance(ctx context.Context, id int64, balanceSats int64) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE watched_wallets SET balance_sats = ?, last_checked_at = CURRENT_TIMESTAMP WHERE id = ?`,
		balanceSats, id,
	)
	if err != nil {
		return fmt.Errorf("update wallet balance %d: %w", id, err)
	}
	return nil
}

// TotalWatchedBalance returns the sum of all watched wallet balances.
func (d *DB) TotalWatchedBalance(ctx context.Context) (int64, error) {
	var total int64
	err := d.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(balance_sats), 0) FROM watched_wallets`,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("total watched balance: %w", err)
	}
	return total, nil
}

// ExchangeTransaction represents a single exchange import row for display.
type ExchangeTransaction struct {
	ID           int64
	Source       string
	TxType       string
	Date         time.Time
	AmountBTC    float64
	AmountSats   int64
	AmountUSD    float64
	FeeUSD       float64
	CostBasisUSD float64
}

// ExchangeTransactionPage holds paginated exchange transaction results.
type ExchangeTransactionPage struct {
	Transactions []ExchangeTransaction
	Total        int
	Page         int
	Limit        int
}

// ListExchangeTransactions returns paginated exchange import rows for a source.
func (d *DB) ListExchangeTransactions(ctx context.Context, source string, limit, offset int) (*ExchangeTransactionPage, error) {
	// Count total
	var total int
	err := d.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM exchange_imports WHERE source = ?`, source,
	).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("count exchange transactions for %s: %w", source, err)
	}

	rows, err := d.db.QueryContext(ctx,
		`SELECT id, source, tx_type,
			COALESCE(json_extract(raw_data, '$.Date'), imported_at) as tx_date,
			COALESCE(json_extract(raw_data, '$.AmountBTC'), 0),
			COALESCE(json_extract(raw_data, '$.AmountUSD'), 0),
			COALESCE(json_extract(raw_data, '$.FeeUSD'), 0),
			COALESCE(json_extract(raw_data, '$.CostBasisUSD'), 0)
		FROM exchange_imports
		WHERE source = ?
		ORDER BY COALESCE(json_extract(raw_data, '$.Date'), imported_at) DESC
		LIMIT ? OFFSET ?`, source, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list exchange transactions for %s: %w", source, err)
	}
	defer rows.Close()

	var txns []ExchangeTransaction
	for rows.Next() {
		var t ExchangeTransaction
		var dateStr string
		if err := rows.Scan(&t.ID, &t.Source, &t.TxType, &dateStr, &t.AmountBTC, &t.AmountUSD, &t.FeeUSD, &t.CostBasisUSD); err != nil {
			return nil, fmt.Errorf("scan exchange transaction: %w", err)
		}
		t.Date, _ = time.Parse(time.RFC3339, dateStr)
		if t.Date.IsZero() {
			t.Date, _ = time.Parse("2006-01-02T15:04:05Z", dateStr)
		}
		t.AmountSats = int64(math.Round(t.AmountBTC * 1e8))
		txns = append(txns, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate exchange transactions: %w", err)
	}

	return &ExchangeTransactionPage{
		Transactions: txns,
		Total:        total,
		Page:         offset/limit + 1,
		Limit:        limit,
	}, nil
}

// ExchangeBalance returns the net BTC balance (in sats) for a given exchange source
// by summing AmountBTC only from rows that actually move BTC (non-zero AmountBTC).
// USD-only transactions (e.g. cash withdrawals) are excluded.
//
// For Strike, the live API balance (strike_live_balance_sats) is preferred when available
// because Strike's CSV export omits BTC amounts for USD-denominated Lightning sends,
// which would otherwise cause the calculated balance to be overstated.
func (d *DB) ExchangeBalance(ctx context.Context, source string) (int64, error) {
	if source == "strike" {
		if liveVal, _ := d.GetSetting(ctx, "strike_live_balance_sats"); liveVal != "" {
			if sats, err := strconv.ParseInt(liveVal, 10, 64); err == nil && sats > 0 {
				return sats, nil
			}
		}
	}
	if source == "coinbase" {
		if liveVal, _ := d.GetSetting(ctx, "coinbase_live_balance_sats"); liveVal != "" {
			if sats, err := strconv.ParseInt(liveVal, 10, 64); err == nil && sats >= 0 {
				return sats, nil
			}
		}
	}

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

// TotalPortfolioSats computes total BTC across on-chain wallet, channels, cold storage, and exchanges.
func (d *DB) TotalPortfolioSats(ctx context.Context) (int64, error) {
	var total int64

	// On-chain wallet balance (latest snapshot)
	var walletSats sql.NullInt64
	_ = d.db.QueryRowContext(ctx,
		`SELECT total_sat FROM wallet_balance_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(&walletSats)
	if walletSats.Valid {
		total += walletSats.Int64
	}

	// Channel local balances
	var channelSats sql.NullInt64
	_ = d.db.QueryRowContext(ctx,
		`SELECT SUM(local_balance) FROM channels WHERE active = 1`,
	).Scan(&channelSats)
	if channelSats.Valid {
		total += channelSats.Int64
	}

	// Cold storage wallets
	var coldSats sql.NullInt64
	_ = d.db.QueryRowContext(ctx,
		`SELECT SUM(balance_sats) FROM watched_wallets`,
	).Scan(&coldSats)
	if coldSats.Valid {
		total += coldSats.Int64
	}

	// Exchange balances (all sources)
	for _, source := range []string{"strike", "river", "coinbase", "swan"} {
		bal, err := d.ExchangeBalance(ctx, source)
		if err == nil {
			total += bal
		}
	}

	return total, nil
}

// PortfolioSnapshot represents a point-in-time portfolio value.
type PortfolioSnapshot struct {
	CapturedAt  time.Time
	TotalSats   int64
	BTCPriceUSD float64
}

// InsertPortfolioSnapshot records the current total portfolio value.
func (d *DB) InsertPortfolioSnapshot(ctx context.Context, totalSats int64, btcPriceUSD float64) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO portfolio_snapshots (total_sats, btc_price_usd) VALUES (?, ?)`,
		totalSats, btcPriceUSD,
	)
	if err != nil {
		return fmt.Errorf("insert portfolio snapshot: %w", err)
	}
	return nil
}

// PortfolioSnapshots returns up to `days` days of portfolio snapshots, one per day (latest per day).
func (d *DB) PortfolioSnapshots(ctx context.Context, days int) ([]PortfolioSnapshot, error) {
	since := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	rows, err := d.db.QueryContext(ctx,
		`SELECT DATE(p.captured_at) as day,
			p.total_sats,
			MAX(p.btc_price_usd, 0)
		FROM portfolio_snapshots p
		INNER JOIN (
			SELECT MAX(id) as max_id
			FROM portfolio_snapshots
			WHERE captured_at >= ?
			GROUP BY DATE(captured_at)
		) g ON p.id = g.max_id
		ORDER BY day ASC`, since,
	)
	if err != nil {
		return nil, fmt.Errorf("portfolio snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []PortfolioSnapshot
	for rows.Next() {
		var s PortfolioSnapshot
		var day string
		if err := rows.Scan(&day, &s.TotalSats, &s.BTCPriceUSD); err != nil {
			return nil, fmt.Errorf("scan portfolio snapshot: %w", err)
		}
		s.CapturedAt, _ = time.Parse("2006-01-02", day)
		snapshots = append(snapshots, s)
	}
	return snapshots, rows.Err()
}

// BackfillPortfolioSnapshots reconstructs historical portfolio snapshots by replaying
// exchange transactions and wallet balance snapshots. For each unique date found in
// the data, it computes the cumulative exchange balance (from CSV imports) plus the
// wallet balance (from the nearest prior wallet_balance_snapshot). Channel and cold
// storage balances are not available historically and are excluded from backfill.
// Only dates without an existing snapshot are filled. Price data is not available
// historically and is set to 0.
// BackfillPortfolioSnapshots reconstructs historical portfolio snapshots by replaying
// exchange transactions and wallet balance snapshots. For each unique date found in
// the data, it computes the cumulative exchange balance (from CSV imports) plus the
// wallet balance (from the nearest prior wallet_balance_snapshot). Channel and cold
// storage balances are not available historically and are excluded from backfill.
//
// Backfilled snapshots use btc_price_usd = -1 as a marker so they can be distinguished
// from live snapshots and safely replaced on re-import. This function is idempotent:
// calling it multiple times (e.g. after each CSV import) replaces stale backfill data
// without affecting live syncer snapshots (which have btc_price_usd >= 0).
func (d *DB) BackfillPortfolioSnapshots(ctx context.Context) (int, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("backfill: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Remove previous backfilled snapshots (marker: btc_price_usd = -1)
	_, err = tx.ExecContext(ctx,
		`DELETE FROM portfolio_snapshots WHERE btc_price_usd = -1`)
	if err != nil {
		return 0, fmt.Errorf("backfill: clear old: %w", err)
	}

	// Collect all unique dates from exchange transactions and wallet snapshots.
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT DATE(d) as day FROM (
			SELECT json_extract(raw_data, '$.Date') as d FROM exchange_imports
			WHERE json_extract(raw_data, '$.Date') IS NOT NULL
			UNION
			SELECT captured_at as d FROM wallet_balance_snapshots
		)
		WHERE d IS NOT NULL AND DATE(d) IS NOT NULL
		ORDER BY day ASC`,
	)
	if err != nil {
		return 0, fmt.Errorf("backfill: collect dates: %w", err)
	}

	var dates []string
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			rows.Close()
			return 0, fmt.Errorf("backfill: scan date: %w", err)
		}
		dates = append(dates, day)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("backfill: iterate dates: %w", err)
	}

	if len(dates) == 0 {
		// Still commit to apply the delete (clears stale backfill).
		return 0, tx.Commit()
	}

	// For each date, compute the portfolio total and insert.
	// Skip days that already have a live snapshot (btc_price_usd >= 0).
	inserted := 0
	for _, day := range dates {
		var liveExists int
		err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM portfolio_snapshots
			 WHERE DATE(captured_at) = ? AND btc_price_usd >= 0`, day,
		).Scan(&liveExists)
		if err != nil {
			return inserted, fmt.Errorf("backfill: check existing for %s: %w", day, err)
		}
		if liveExists > 0 {
			continue
		}

		var total int64

		// Exchange balance: sum of all AmountBTC where Date <= this day
		var exchBTC sql.NullFloat64
		_ = tx.QueryRowContext(ctx,
			`SELECT SUM(json_extract(raw_data, '$.AmountBTC'))
			 FROM exchange_imports
			 WHERE COALESCE(json_extract(raw_data, '$.AmountBTC'), 0) != 0
			   AND DATE(json_extract(raw_data, '$.Date')) <= ?`, day,
		).Scan(&exchBTC)
		if exchBTC.Valid {
			total += int64(math.Round(exchBTC.Float64 * 1e8))
		}

		// Wallet balance: latest snapshot on or before this day
		var walletSats sql.NullInt64
		_ = tx.QueryRowContext(ctx,
			`SELECT total_sat FROM wallet_balance_snapshots
			 WHERE DATE(captured_at) <= ?
			 ORDER BY captured_at DESC LIMIT 1`, day,
		).Scan(&walletSats)
		if walletSats.Valid {
			total += walletSats.Int64
		}

		// Insert with marker btc_price_usd = -1
		capturedAt := day + " 12:00:00"
		_, err = tx.ExecContext(ctx,
			`INSERT INTO portfolio_snapshots (captured_at, total_sats, btc_price_usd) VALUES (?, ?, -1)`,
			capturedAt, total,
		)
		if err != nil {
			return inserted, fmt.Errorf("backfill: insert for %s: %w", day, err)
		}
		inserted++
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("backfill: commit: %w", err)
	}
	return inserted, nil
}

// GetSetting retrieves a value from the settings table. Returns empty string if not found.
func (d *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := d.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetSetting upserts a value in the settings table.
func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.db.ExecContext(ctx,
		"INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at",
		key, value)
	return err
}

// UnifiedTransaction represents a single BTC transaction from any source.
type UnifiedTransaction struct {
	Source     string    // lnd_forward, lnd_invoice, lnd_payment, lnd_onchain, strike, river, etc.
	SourceID   string   // unique ID within the source
	Time       time.Time
	TxType     string   // buy, sell, send, receive, fee_income
	AmountSat  int64    // positive = inflow, negative = outflow
	AmountUSD  float64
	FeeSat     int64
	FeeUSD     float64
	PriceUSD   float64
	Memo       string
	Note       string   // user-editable annotation
	IsTransfer bool     // true if marked as internal transfer
}

// parseFlexibleTime tries multiple time formats to parse a timestamp string from SQLite.
func parseFlexibleTime(ts string) time.Time {
	for _, fmt := range []string{
		"2006-01-02T15:04:05Z",
		"2006-01-02 15:04:05",
		time.RFC3339,
	} {
		if t, err := time.Parse(fmt, ts); err == nil {
			return t
		}
	}
	return time.Time{}
}

// UnifiedTransactionPage holds paginated results from the unified view.
type UnifiedTransactionPage struct {
	Transactions []UnifiedTransaction
	Total        int
}

// DistinctTransactionValues returns the distinct sources and tx_types from the unified view.
func (d *DB) DistinctTransactionValues(ctx context.Context) (sources []string, txTypes []string, err error) {
	rows, err := d.db.QueryContext(ctx, `SELECT DISTINCT source FROM btc_transactions_v WHERE source != '' ORDER BY source`)
	if err != nil {
		return nil, nil, fmt.Errorf("distinct sources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, nil, err
		}
		sources = append(sources, s)
	}

	rows2, err := d.db.QueryContext(ctx, `SELECT DISTINCT tx_type FROM btc_transactions_v WHERE tx_type != '' ORDER BY tx_type`)
	if err != nil {
		return nil, nil, fmt.Errorf("distinct tx_types: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var t string
		if err := rows2.Scan(&t); err != nil {
			return nil, nil, err
		}
		txTypes = append(txTypes, t)
	}

	return sources, txTypes, nil
}

// TransactionFilter holds filter/sort parameters for the unified transaction query.
type TransactionFilter struct {
	DateFrom string // "YYYY-MM-DD" or empty
	DateTo   string // "YYYY-MM-DD" or empty
	TxType   string // e.g. "buy", "send", or empty for all
	Source   string // e.g. "strike", "lnd_forward", or empty for all
	Search   string // free-text search across memo and notes
	Flow     string // "inflow" (amount_sat > 0), "outflow" (amount_sat < 0), or empty for all
	SortCol  string // "ts", "amount_sat", "source", "tx_type", "amount_usd"
	SortDir  string // "asc" or "desc"
	Limit    int
	Offset   int
}

// validSortColumns is the allowlist of columns that can be sorted on.
var validSortColumns = map[string]string{
	"ts":         "v.ts",
	"source":     "v.source",
	"tx_type":    "v.tx_type",
	"amount_sat": "v.amount_sat",
	"amount_usd": "v.amount_usd",
	"fee":        "v.fee_sat",
}

// ListUnifiedTransactions returns paginated transactions from the unified view,
// with user notes joined from the transaction_notes table.
// Supports filtering by date range, type, source, search, and custom sort.
func (d *DB) ListUnifiedTransactions(ctx context.Context, f TransactionFilter) (*UnifiedTransactionPage, error) {
	var where []string
	var args []interface{}

	if f.DateFrom != "" {
		where = append(where, "v.ts >= ?")
		args = append(args, f.DateFrom+" 00:00:00")
	}
	if f.DateTo != "" {
		where = append(where, "v.ts <= ?")
		args = append(args, f.DateTo+" 23:59:59")
	}
	if f.TxType != "" {
		where = append(where, "v.tx_type = ?")
		args = append(args, f.TxType)
	}
	if f.Source != "" {
		where = append(where, "v.source = ?")
		args = append(args, f.Source)
	}
	if f.Search != "" {
		where = append(where, "(v.memo LIKE ? OR COALESCE(n.note, '') LIKE ? OR v.source_id LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like, like)
	}
	if f.Flow == "inflow" {
		where = append(where, "v.amount_sat > 0")
	} else if f.Flow == "outflow" {
		where = append(where, "v.amount_sat < 0")
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Count query
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM btc_transactions_v v
		LEFT JOIN transaction_notes n ON n.source_id = v.source_id %s`, whereClause)
	var total int
	err := d.db.QueryRowContext(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("count unified transactions: %w", err)
	}

	// Sort
	orderCol := "v.ts"
	if col, ok := validSortColumns[f.SortCol]; ok {
		orderCol = col
	}
	orderDir := "DESC"
	if f.SortDir == "asc" {
		orderDir = "ASC"
	}

	dataQuery := fmt.Sprintf(`SELECT v.source, v.source_id, v.ts, v.tx_type, v.amount_sat, v.amount_usd,
		v.fee_sat, v.fee_usd, COALESCE(v.price_usd, 0), v.memo,
		COALESCE(n.note, ''),
		COALESCE(n.is_transfer, 0)
		FROM btc_transactions_v v
		LEFT JOIN transaction_notes n ON n.source_id = v.source_id
		%s ORDER BY %s %s LIMIT ? OFFSET ?`, whereClause, orderCol, orderDir)

	dataArgs := append(args, f.Limit, f.Offset)
	rows, err := d.db.QueryContext(ctx, dataQuery, dataArgs...)
	if err != nil {
		return nil, fmt.Errorf("list unified transactions: %w", err)
	}
	defer rows.Close()

	var txns []UnifiedTransaction
	for rows.Next() {
		var tx UnifiedTransaction
		var ts string
		var isTransfer int
		if err := rows.Scan(&tx.Source, &tx.SourceID, &ts, &tx.TxType, &tx.AmountSat, &tx.AmountUSD, &tx.FeeSat, &tx.FeeUSD, &tx.PriceUSD, &tx.Memo, &tx.Note, &isTransfer); err != nil {
			return nil, fmt.Errorf("scan unified transaction: %w", err)
		}
		tx.Time = parseFlexibleTime(ts)
		tx.IsTransfer = isTransfer == 1
		txns = append(txns, tx)
	}

	return &UnifiedTransactionPage{Transactions: txns, Total: total}, nil
}

// ListUnifiedTransactionsSince returns transactions after a given timestamp, ordered oldest first.
// Used for incremental sync (e.g. pushing new transactions to Monarch).
func (d *DB) ListUnifiedTransactionsSince(ctx context.Context, since time.Time, limit int) ([]UnifiedTransaction, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT source, source_id, ts, tx_type, amount_sat, amount_usd, fee_sat, fee_usd, COALESCE(price_usd, 0), memo
		 FROM btc_transactions_v
		 WHERE ts > ?
		 ORDER BY ts ASC
		 LIMIT ?`, since.UTC().Format("2006-01-02 15:04:05"), limit)
	if err != nil {
		return nil, fmt.Errorf("list unified transactions since: %w", err)
	}
	defer rows.Close()

	var txns []UnifiedTransaction
	for rows.Next() {
		var tx UnifiedTransaction
		var ts string
		if err := rows.Scan(&tx.Source, &tx.SourceID, &ts, &tx.TxType, &tx.AmountSat, &tx.AmountUSD, &tx.FeeSat, &tx.FeeUSD, &tx.PriceUSD, &tx.Memo); err != nil {
			return nil, fmt.Errorf("scan unified transaction: %w", err)
		}
		tx.Time = parseFlexibleTime(ts)
		txns = append(txns, tx)
	}

	return txns, nil
}

// GetTransactionNote returns the user note for a transaction, or empty string if none.
func (d *DB) GetTransactionNote(ctx context.Context, sourceID string) (string, error) {
	var note string
	err := d.db.QueryRowContext(ctx, `SELECT note FROM transaction_notes WHERE source_id = ?`, sourceID).Scan(&note)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return note, err
}

// SetTransactionNote upserts a user note for a transaction. Empty note deletes the row.
func (d *DB) SetTransactionNote(ctx context.Context, sourceID, note string) error {
	if strings.TrimSpace(note) == "" {
		_, err := d.db.ExecContext(ctx, `DELETE FROM transaction_notes WHERE source_id = ?`, sourceID)
		return err
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO transaction_notes (source_id, note, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(source_id) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at`,
		sourceID, strings.TrimSpace(note))
	return err
}

// ListUnsyncedTransactions returns unified transactions of the given types and sources
// that have not yet been synced to Monarch Money.
// If sources is empty, all sources are included.
func (d *DB) ListUnsyncedTransactions(ctx context.Context, txTypes []string, sources []string) ([]UnifiedTransaction, error) {
	if len(txTypes) == 0 {
		return nil, nil
	}

	typePH := make([]string, len(txTypes))
	args := make([]interface{}, len(txTypes))
	for i, t := range txTypes {
		typePH[i] = "?"
		args[i] = t
	}

	whereClause := fmt.Sprintf("v.tx_type IN (%s)", strings.Join(typePH, ","))

	if len(sources) > 0 {
		srcPH := make([]string, len(sources))
		for i, s := range sources {
			srcPH[i] = "?"
			args = append(args, s)
		}
		whereClause += fmt.Sprintf(" AND v.source IN (%s)", strings.Join(srcPH, ","))
	}

	query := fmt.Sprintf(`
		SELECT v.source, v.source_id, v.ts, v.tx_type, v.amount_sat, v.amount_usd,
		       v.fee_sat, v.fee_usd, COALESCE(v.price_usd, 0), v.memo
		FROM btc_transactions_v v
		LEFT JOIN monarch_tx_sync m ON m.source_id = v.source_id
		WHERE %s AND m.source_id IS NULL
		ORDER BY v.ts ASC`, whereClause)

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list unsynced transactions: %w", err)
	}
	defer rows.Close()

	var txns []UnifiedTransaction
	for rows.Next() {
		var tx UnifiedTransaction
		var ts string
		if err := rows.Scan(&tx.Source, &tx.SourceID, &ts, &tx.TxType, &tx.AmountSat, &tx.AmountUSD, &tx.FeeSat, &tx.FeeUSD, &tx.PriceUSD, &tx.Memo); err != nil {
			return nil, fmt.Errorf("scan unsynced transaction: %w", err)
		}
		tx.Time = parseFlexibleTime(ts)
		txns = append(txns, tx)
	}
	return txns, nil
}

// MarkTransactionSynced records that a transaction was synced to Monarch.
func (d *DB) MarkTransactionSynced(ctx context.Context, sourceID, monarchTxID string) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO monarch_tx_sync (source_id, monarch_tx_id) VALUES (?, ?)`,
		sourceID, monarchTxID)
	return err
}

// MonarchSyncedCount returns the number of transactions synced to Monarch.
func (d *DB) MonarchSyncedCount(ctx context.Context) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM monarch_tx_sync`).Scan(&count)
	return count, err
}

// ClearMonarchSyncState removes all Monarch sync tracking records.
func (d *DB) ClearMonarchSyncState(ctx context.Context) error {
	_, err := d.db.ExecContext(ctx, `DELETE FROM monarch_tx_sync`)
	return err
}

// --- Tax / Cost Basis Queries ---

// BTCLot represents a row from the btc_lots table.
type BTCLot struct {
	ID         int64
	AcquiredAt time.Time
	AmountSat  int64
	PriceUSD   float64
	Source     string
	ExternalID string
}

// ListBTCLots returns all BTC acquisition lots, ordered by acquisition date.
func (d *DB) ListBTCLots(ctx context.Context) ([]BTCLot, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, acquired_at, amount_sat, price_usd, source, COALESCE(external_id, '')
		 FROM btc_lots
		 ORDER BY acquired_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list btc lots: %w", err)
	}
	defer rows.Close()

	var lots []BTCLot
	for rows.Next() {
		var l BTCLot
		if err := rows.Scan(&l.ID, &l.AcquiredAt, &l.AmountSat, &l.PriceUSD, &l.Source, &l.ExternalID); err != nil {
			return nil, fmt.Errorf("scan btc lot: %w", err)
		}
		lots = append(lots, l)
	}
	return lots, rows.Err()
}

// DisposalRow represents a sale or send from exchange_imports.
type DisposalRow struct {
	DisposedAt  time.Time
	AmountSat   int64
	ProceedsUSD float64
	TxType      string
	Source      string
	ExternalID  string
}

// ListDisposals returns disposal events (sales only) from exchange_imports.
// Sends and withdrawals are excluded because they are typically self-transfers
// (e.g. exchange → personal wallet) and not taxable events.
func (d *DB) ListDisposals(ctx context.Context) ([]DisposalRow, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT
			COALESCE(json_extract(raw_data, '$.Date'), imported_at) as tx_date,
			ABS(COALESCE(json_extract(raw_data, '$.AmountBTC'), 0)) as abs_btc,
			ABS(COALESCE(json_extract(raw_data, '$.AmountUSD'), 0)) as amount_usd,
			tx_type,
			source,
			external_id
		 FROM exchange_imports
		 WHERE LOWER(tx_type) IN ('sale', 'sell')
		   AND COALESCE(json_extract(raw_data, '$.AmountBTC'), 0) != 0
		 ORDER BY tx_date ASC`)
	if err != nil {
		return nil, fmt.Errorf("list disposals: %w", err)
	}
	defer rows.Close()

	var disposals []DisposalRow
	for rows.Next() {
		var d DisposalRow
		var dateStr string
		var absBTC float64
		if err := rows.Scan(&dateStr, &absBTC, &d.ProceedsUSD, &d.TxType, &d.Source, &d.ExternalID); err != nil {
			return nil, fmt.Errorf("scan disposal: %w", err)
		}
		d.DisposedAt, _ = time.Parse(time.RFC3339, dateStr)
		if d.DisposedAt.IsZero() {
			d.DisposedAt, _ = time.Parse("2006-01-02T15:04:05Z", dateStr)
		}
		d.AmountSat = int64(math.Round(absBTC * 1e8))
		if d.ProceedsUSD < 0 {
			d.ProceedsUSD = -d.ProceedsUSD
		}
		disposals = append(disposals, d)
	}
	return disposals, rows.Err()
}

// SaveDisposals writes matched disposal events to the disposals table.
// It clears existing disposals first (full recalculation).
func (d *DB) SaveDisposals(ctx context.Context, events []struct {
	DisposedAt  time.Time
	AmountSat   int64
	ProceedsUSD float64
	LotID       int64
}) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM disposals`); err != nil {
		return fmt.Errorf("clear disposals: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO disposals (disposed_at, amount_sat, proceeds_usd, lot_id) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, e := range events {
		if _, err := stmt.ExecContext(ctx, e.DisposedAt, e.AmountSat, e.ProceedsUSD, e.LotID); err != nil {
			return fmt.Errorf("insert disposal: %w", err)
		}
	}

	return tx.Commit()
}

// --- Portfolio Breakdown Queries ---

// PortfolioBreakdown holds the current BTC holdings broken down by source category.
type PortfolioBreakdown struct {
	OnChainSats     int64
	ChannelSats     int64
	ColdStorageSats int64
	CollateralSats  int64            // BTC locked as Strike line-of-credit collateral
	ExchangeSats    map[string]int64 // per-exchange (strike, river, coinbase, swan)
	TotalSats       int64
}

// StrikeCollateralSats returns the total BTC (in sats) currently locked as Strike
// line-of-credit collateral. Strike records these as "Loan collateral" rows with
// negative AmountBTC; we return the absolute value so callers treat it as an asset
// held in a different location rather than a loss.
func (d *DB) StrikeCollateralSats(ctx context.Context) (int64, error) {
	var total sql.NullFloat64
	err := d.db.QueryRowContext(ctx,
		`SELECT SUM(json_extract(raw_data, '$.AmountBTC'))
		 FROM exchange_imports
		 WHERE source = 'strike'
		   AND json_extract(raw_data, '$.Type') = 'Loan collateral'`,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("strike collateral sats: %w", err)
	}
	if !total.Valid || total.Float64 >= 0 {
		return 0, nil
	}
	return int64(math.Round(-total.Float64 * 1e8)), nil
}

// PortfolioBreakdownQuery computes the current point-in-time breakdown of BTC across all sources.
func (d *DB) PortfolioBreakdownQuery(ctx context.Context) (*PortfolioBreakdown, error) {
	b := &PortfolioBreakdown{
		ExchangeSats: make(map[string]int64),
	}

	// On-chain wallet balance (latest snapshot)
	var walletSats sql.NullInt64
	_ = d.db.QueryRowContext(ctx,
		`SELECT total_sat FROM wallet_balance_snapshots ORDER BY captured_at DESC LIMIT 1`,
	).Scan(&walletSats)
	if walletSats.Valid {
		b.OnChainSats = walletSats.Int64
	}

	// Channel local balances
	var channelSats sql.NullInt64
	_ = d.db.QueryRowContext(ctx,
		`SELECT SUM(local_balance) FROM channels WHERE active = 1`,
	).Scan(&channelSats)
	if channelSats.Valid {
		b.ChannelSats = channelSats.Int64
	}

	// Cold storage wallets
	var coldSats sql.NullInt64
	_ = d.db.QueryRowContext(ctx,
		`SELECT SUM(balance_sats) FROM watched_wallets`,
	).Scan(&coldSats)
	if coldSats.Valid {
		b.ColdStorageSats = coldSats.Int64
	}

	// Exchange balances (per source)
	for _, source := range []string{"strike", "river", "coinbase", "swan"} {
		bal, err := d.ExchangeBalance(ctx, source)
		if err == nil && bal != 0 {
			b.ExchangeSats[source] = bal
		}
	}

	// BTC locked as Strike collateral (tracked separately from available balance)
	if collateral, err := d.StrikeCollateralSats(ctx); err == nil && collateral > 0 {
		b.CollateralSats = collateral
	}

	b.TotalSats = b.OnChainSats + b.ChannelSats + b.ColdStorageSats + b.CollateralSats
	for _, v := range b.ExchangeSats {
		b.TotalSats += v
	}

	return b, nil
}

// InsertPortfolioSnapshotWithDetails records a portfolio snapshot with per-source breakdown.
func (d *DB) InsertPortfolioSnapshotWithDetails(ctx context.Context, totalSats int64, btcPriceUSD float64, details map[string]int64) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("snapshot details: begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO portfolio_snapshots (total_sats, btc_price_usd) VALUES (?, ?)`,
		totalSats, btcPriceUSD)
	if err != nil {
		return fmt.Errorf("snapshot details: insert snapshot: %w", err)
	}

	snapshotID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("snapshot details: last insert id: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO portfolio_snapshot_details (snapshot_id, source, sats) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("snapshot details: prepare: %w", err)
	}
	defer stmt.Close()

	for source, sats := range details {
		if _, err := stmt.ExecContext(ctx, snapshotID, source, sats); err != nil {
			return fmt.Errorf("snapshot details: insert %s: %w", source, err)
		}
	}

	return tx.Commit()
}

// PortfolioSnapshotDetail represents a single source's value at a point in time.
type PortfolioSnapshotDetail struct {
	CapturedAt  time.Time
	Source      string
	Sats        int64
	BTCPriceUSD float64
}

// PortfolioSnapshotsWithDetails returns per-source snapshot data for the given number of days.
func (d *DB) PortfolioSnapshotsWithDetails(ctx context.Context, days int) ([]PortfolioSnapshotDetail, error) {
	since := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	rows, err := d.db.QueryContext(ctx,
		`SELECT DATE(p.captured_at) as day, d.source, d.sats, MAX(p.btc_price_usd, 0)
		 FROM portfolio_snapshots p
		 INNER JOIN portfolio_snapshot_details d ON d.snapshot_id = p.id
		 INNER JOIN (
			SELECT MAX(id) as max_id
			FROM portfolio_snapshots
			WHERE captured_at >= ?
			GROUP BY DATE(captured_at)
		 ) g ON p.id = g.max_id
		 ORDER BY day ASC, d.source ASC`, since)
	if err != nil {
		return nil, fmt.Errorf("portfolio snapshot details: %w", err)
	}
	defer rows.Close()

	var details []PortfolioSnapshotDetail
	for rows.Next() {
		var d PortfolioSnapshotDetail
		var day string
		if err := rows.Scan(&day, &d.Source, &d.Sats, &d.BTCPriceUSD); err != nil {
			return nil, fmt.Errorf("scan portfolio snapshot detail: %w", err)
		}
		d.CapturedAt, _ = time.Parse("2006-01-02", day)
		details = append(details, d)
	}
	return details, rows.Err()
}

// NetFlowResult holds inflow/outflow totals for a period.
type NetFlowResult struct {
	InflowSats   int64
	OutflowSats  int64
	InflowCount  int
	OutflowCount int
	InflowUSD    float64
	OutflowUSD   float64
}

// NetFlowSummary computes inflows and outflows over a period from the unified transaction view.
// When excludeTransfers is true, transactions marked as internal transfers are excluded.
func (d *DB) NetFlowSummary(ctx context.Context, since time.Time, excludeTransfers bool) (*NetFlowResult, error) {
	var result NetFlowResult

	transferJoin := ""
	transferWhere := ""
	if excludeTransfers {
		transferJoin = " LEFT JOIN transaction_notes tn ON tn.source_id = v.source_id"
		transferWhere = " AND COALESCE(tn.is_transfer, 0) = 0"
	}

	query := fmt.Sprintf(`SELECT
			COALESCE(SUM(CASE WHEN v.amount_sat > 0 THEN v.amount_sat ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN v.amount_sat < 0 THEN ABS(v.amount_sat) ELSE 0 END), 0),
			COALESCE(COUNT(CASE WHEN v.amount_sat > 0 THEN 1 END), 0),
			COALESCE(COUNT(CASE WHEN v.amount_sat < 0 THEN 1 END), 0),
			COALESCE(SUM(CASE WHEN v.amount_usd > 0 THEN v.amount_usd ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN v.amount_usd < 0 THEN ABS(v.amount_usd) ELSE 0 END), 0)
		 FROM btc_transactions_v v%s
		 WHERE v.ts >= ?%s`, transferJoin, transferWhere)

	err := d.db.QueryRowContext(ctx, query, since.UTC().Format("2006-01-02 15:04:05")).
		Scan(&result.InflowSats, &result.OutflowSats, &result.InflowCount, &result.OutflowCount,
			&result.InflowUSD, &result.OutflowUSD)
	if err != nil {
		return nil, fmt.Errorf("net flow summary: %w", err)
	}
	return &result, nil
}

// NetFlowSummaryBySource returns inflow/outflow totals filtered to specific transaction sources.
func (d *DB) NetFlowSummaryBySource(ctx context.Context, since time.Time, sources []string, excludeTransfers bool) (*NetFlowResult, error) {
	if len(sources) == 0 {
		return &NetFlowResult{}, nil
	}

	var result NetFlowResult

	transferJoin := ""
	transferWhere := ""
	if excludeTransfers {
		transferJoin = " LEFT JOIN transaction_notes tn ON tn.source_id = v.source_id"
		transferWhere = " AND COALESCE(tn.is_transfer, 0) = 0"
	}

	placeholders := make([]string, len(sources))
	args := make([]interface{}, 0, len(sources)+1)
	args = append(args, since.UTC().Format("2006-01-02 15:04:05"))
	for i, s := range sources {
		placeholders[i] = "?"
		args = append(args, s)
	}

	query := fmt.Sprintf(`SELECT
			COALESCE(SUM(CASE WHEN v.amount_sat > 0 THEN v.amount_sat ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN v.amount_sat < 0 THEN ABS(v.amount_sat) ELSE 0 END), 0),
			COALESCE(COUNT(CASE WHEN v.amount_sat > 0 THEN 1 END), 0),
			COALESCE(COUNT(CASE WHEN v.amount_sat < 0 THEN 1 END), 0),
			COALESCE(SUM(CASE WHEN v.amount_usd > 0 THEN v.amount_usd ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN v.amount_usd < 0 THEN ABS(v.amount_usd) ELSE 0 END), 0)
		 FROM btc_transactions_v v%s
		 WHERE v.ts >= ? AND v.source IN (%s)%s`,
		transferJoin, strings.Join(placeholders, ","), transferWhere)

	err := d.db.QueryRowContext(ctx, query, args...).
		Scan(&result.InflowSats, &result.OutflowSats, &result.InflowCount, &result.OutflowCount,
			&result.InflowUSD, &result.OutflowUSD)
	if err != nil {
		return nil, fmt.Errorf("net flow summary by source: %w", err)
	}
	return &result, nil
}

// AutoTagChannelTransfers finds on-chain transactions that match channel open/close
// tx hashes and automatically marks them as transfers with specific notes.
// Channel opens are noted as "Channel Open"; channel closes as "Channel Close".
// Returns the number of newly tagged transactions.
func (d *DB) AutoTagChannelTransfers(ctx context.Context) (int, error) {
	// Channel opens: channel_point is "txid:output_index" — extract txid portion.
	// The on-chain source_id in btc_transactions_v is just the tx_hash.
	resOpens, err := d.db.ExecContext(ctx, `
		INSERT INTO transaction_notes (source_id, note, is_transfer, updated_at)
		SELECT v.source_id, 'Channel Open', 1, CURRENT_TIMESTAMP
		FROM btc_transactions_v v
		WHERE v.source = 'lnd_onchain'
		  AND EXISTS (
		    SELECT 1 FROM channels c
		    WHERE c.channel_point != ''
		      AND SUBSTR(c.channel_point, 1, INSTR(c.channel_point, ':') - 1) = v.source_id
		  )
		ON CONFLICT(source_id) DO UPDATE SET
		  note = CASE WHEN note IN ('', 'Channel Open', 'Channel Close') THEN 'Channel Open' ELSE note END,
		  is_transfer = 1,
		  updated_at = CURRENT_TIMESTAMP
		WHERE is_transfer != 1 OR note IN ('', 'Channel Close')`)
	if err != nil {
		return 0, fmt.Errorf("auto-tag channel opens: %w", err)
	}
	nOpens, _ := resOpens.RowsAffected()

	// Channel closes: closing_tx_hash is the tx_hash directly.
	// A tx may be both a funding tx (open) for one channel and a close for another;
	// opens take priority and are not overwritten here.
	resCloses, err := d.db.ExecContext(ctx, `
		INSERT INTO transaction_notes (source_id, note, is_transfer, updated_at)
		SELECT v.source_id, 'Channel Close', 1, CURRENT_TIMESTAMP
		FROM btc_transactions_v v
		WHERE v.source = 'lnd_onchain'
		  AND EXISTS (
		    SELECT 1 FROM channels c
		    WHERE c.closing_tx_hash != ''
		      AND c.closing_tx_hash = v.source_id
		  )
		  -- Skip if already tagged as a Channel Open by the previous step
		  AND NOT EXISTS (
		    SELECT 1 FROM channels c
		    WHERE c.channel_point != ''
		      AND SUBSTR(c.channel_point, 1, INSTR(c.channel_point, ':') - 1) = v.source_id
		  )
		ON CONFLICT(source_id) DO UPDATE SET
		  note = CASE WHEN note IN ('', 'Channel Open', 'Channel Close') THEN 'Channel Close' ELSE note END,
		  is_transfer = 1,
		  updated_at = CURRENT_TIMESTAMP
		WHERE is_transfer != 1 OR note IN ('', 'Channel Open')`)
	if err != nil {
		return 0, fmt.Errorf("auto-tag channel closes: %w", err)
	}
	nCloses, _ := resCloses.RowsAffected()

	return int(nOpens) + int(nCloses), nil
}

// SetTransferFlag marks or unmarks a transaction as an internal transfer.
func (d *DB) SetTransferFlag(ctx context.Context, sourceID string, isTransfer bool) error {
	val := 0
	if isTransfer {
		val = 1
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO transaction_notes (source_id, note, is_transfer, updated_at)
		 VALUES (?, '', ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(source_id) DO UPDATE SET is_transfer = excluded.is_transfer, updated_at = excluded.updated_at`,
		sourceID, val)
	return err
}

// GetTransferFlag returns true if the transaction is marked as an internal transfer.
func (d *DB) GetTransferFlag(ctx context.Context, sourceID string) (bool, error) {
	var val int
	err := d.db.QueryRowContext(ctx,
		`SELECT COALESCE(is_transfer, 0) FROM transaction_notes WHERE source_id = ?`, sourceID).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return val == 1, err
}

// TransferCandidate is a potential matching transaction for auto-suggest.
type TransferCandidate struct {
	SourceID  string
	Source    string
	TxType    string
	AmountSat int64
	Time      time.Time
}

// ListTransferCandidates finds transactions that might be the other side of a transfer.
// It looks for opposite-direction transactions within ±24 hours with amount within ±1%.
func (d *DB) ListTransferCandidates(ctx context.Context, sourceID string, amountSat int64, ts time.Time) ([]TransferCandidate, error) {
	absAmount := amountSat
	if absAmount < 0 {
		absAmount = -absAmount
	}
	// ±1% tolerance for fees
	minAmt := float64(absAmount) * 0.99
	maxAmt := float64(absAmount) * 1.01

	// If the original is positive (inflow), look for negative (outflow) and vice versa
	var amountSign string
	if amountSat > 0 {
		amountSign = "v.amount_sat < 0"
	} else {
		amountSign = "v.amount_sat > 0"
	}

	tsStr := ts.UTC().Format("2006-01-02 15:04:05")
	rows, err := d.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT v.source_id, v.source, v.tx_type, v.amount_sat, v.ts
		 FROM btc_transactions_v v
		 LEFT JOIN transaction_notes tn ON tn.source_id = v.source_id
		 WHERE %s
		   AND ABS(v.amount_sat) BETWEEN ? AND ?
		   AND v.ts BETWEEN datetime(?, '-1 day') AND datetime(?, '+1 day')
		   AND v.source_id != ?
		   AND COALESCE(tn.is_transfer, 0) = 0
		 ORDER BY ABS(julianday(v.ts) - julianday(?))
		 LIMIT 5`, amountSign),
		minAmt, maxAmt, tsStr, tsStr, sourceID, tsStr)
	if err != nil {
		return nil, fmt.Errorf("list transfer candidates: %w", err)
	}
	defer rows.Close()

	var candidates []TransferCandidate
	for rows.Next() {
		var c TransferCandidate
		var tsVal string
		if err := rows.Scan(&c.SourceID, &c.Source, &c.TxType, &c.AmountSat, &tsVal); err != nil {
			return nil, fmt.Errorf("scan transfer candidate: %w", err)
		}
		c.Time = parseFlexibleTime(tsVal)
		candidates = append(candidates, c)
	}
	return candidates, rows.Err()
}

// --- Annual Report ---

// MonthlyFeeStat holds aggregated fee data for a single month.
type MonthlyFeeStat struct {
	Month        string // "2026-01"
	TotalFeeMsat int64
	Count        int64
}

// AnnualReportData holds all data needed to render the year-in-review page.
type AnnualReportData struct {
	Year int

	// Routing (free)
	TotalFeeMsat   int64
	RoutedCount    int64
	RoutedVolMsat  int64
	MonthlyFees    []MonthlyFeeStat
	BestChanID     string
	BestChanFee    int64 // msat

	// Exchange acquisitions (pro)
	AcquiredSats   int64
	AcquisitionUSD float64 // total cost basis of acquisitions this year
	DisposalCount  int     // number of sales/disposals this year
}

// AnnualReport computes all year-in-review data for the given calendar year.
func (d *DB) AnnualReport(ctx context.Context, year int) (*AnnualReportData, error) {
	start := fmt.Sprintf("%d-01-01T00:00:00Z", year)
	end := fmt.Sprintf("%d-01-01T00:00:00Z", year+1)

	data := &AnnualReportData{Year: year}

	// Routing summary for the year
	err := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(fee_msat),0), COUNT(*), COALESCE(SUM(amt_in_msat),0)
		FROM forwarding_events
		WHERE timestamp >= ? AND timestamp < ?
	`, start, end).Scan(&data.TotalFeeMsat, &data.RoutedCount, &data.RoutedVolMsat)
	if err != nil {
		return nil, fmt.Errorf("annual routing summary: %w", err)
	}

	// Monthly fees — always produce 12 entries (Jan–Dec), zeroed if no data.
	{
		monthMap := make(map[string]MonthlyFeeStat, 12)
		for m := 1; m <= 12; m++ {
			key := fmt.Sprintf("%04d-%02d", year, m)
			monthMap[key] = MonthlyFeeStat{Month: key}
		}
		mrows, merr := d.db.QueryContext(ctx, `
			SELECT SUBSTR(timestamp,1,7) AS month, COALESCE(SUM(fee_msat),0), COUNT(*)
			FROM forwarding_events
			WHERE timestamp >= ? AND timestamp < ?
			GROUP BY SUBSTR(timestamp,1,7)
			ORDER BY month ASC
		`, start, end)
		if merr != nil {
			return nil, fmt.Errorf("annual monthly fees: %w", merr)
		}
		defer mrows.Close()
		for mrows.Next() {
			var m MonthlyFeeStat
			if err := mrows.Scan(&m.Month, &m.TotalFeeMsat, &m.Count); err != nil {
				return nil, fmt.Errorf("scan monthly fee: %w", err)
			}
			monthMap[m.Month] = m
		}
		if err := mrows.Err(); err != nil {
			return nil, err
		}
		data.MonthlyFees = make([]MonthlyFeeStat, 0, 12)
		for m := 1; m <= 12; m++ {
			key := fmt.Sprintf("%04d-%02d", year, m)
			data.MonthlyFees = append(data.MonthlyFees, monthMap[key])
		}
	}

	// Best channel for the year (by fees earned)
	_ = d.db.QueryRowContext(ctx, `
		SELECT chan_id, COALESCE(SUM(fee_msat),0) AS total
		FROM (
			SELECT chan_id_in AS chan_id, fee_msat FROM forwarding_events WHERE timestamp >= ? AND timestamp < ?
			UNION ALL
			SELECT chan_id_out AS chan_id, fee_msat FROM forwarding_events WHERE timestamp >= ? AND timestamp < ?
		)
		GROUP BY chan_id
		ORDER BY total DESC
		LIMIT 1
	`, start, end, start, end).Scan(&data.BestChanID, &data.BestChanFee)
	// ignore error — no data is fine

	// Exchange acquisitions for the year (all sources)
	var purchBTC float64
	var costUSD float64
	err = d.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data,'$.Type')) IN ('purchase','buy')
				THEN json_extract(raw_data,'$.AmountBTC') ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN LOWER(json_extract(raw_data,'$.Type')) IN ('purchase','buy')
				THEN ABS(COALESCE(json_extract(raw_data,'$.CostBasisUSD'), json_extract(raw_data,'$.AmountUSD'), 0)) ELSE 0 END), 0)
		FROM exchange_imports
		WHERE json_extract(raw_data,'$.Date') >= ? AND json_extract(raw_data,'$.Date') < ?
	`, start, end).Scan(&purchBTC, &costUSD)
	if err == nil {
		data.AcquiredSats = int64(math.Round(purchBTC * 1e8))
		data.AcquisitionUSD = costUSD
	}

	// Sales/disposals count for the year
	_ = d.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM exchange_imports
		WHERE LOWER(tx_type) IN ('sale','sell')
		  AND json_extract(raw_data,'$.Date') >= ? AND json_extract(raw_data,'$.Date') < ?
	`, start, end).Scan(&data.DisposalCount)

	return data, nil
}

// AvailableReportYears returns years available for the annual report page.
// Always includes the last 4 calendar years plus any additional years that
// have forwarding event data, sorted descending.
func (d *DB) AvailableReportYears(ctx context.Context) ([]int, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT DISTINCT CAST(SUBSTR(timestamp,1,4) AS INTEGER) AS yr
		FROM forwarding_events
		ORDER BY yr DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("available report years: %w", err)
	}
	defer rows.Close()
	seen := map[int]bool{}
	for rows.Next() {
		var y int
		if err := rows.Scan(&y); err != nil {
			return nil, err
		}
		seen[y] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Always include the last 4 calendar years so users can navigate back.
	cur := time.Now().Year()
	for y := cur; y >= cur-3; y-- {
		seen[y] = true
	}
	years := make([]int, 0, len(seen))
	for y := range seen {
		years = append(years, y)
	}
	sort.Slice(years, func(i, j int) bool { return years[i] > years[j] })
	return years, nil
}
