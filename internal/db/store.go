package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ForwardingEvent represents a routing event stored in the database.
type ForwardingEvent struct {
	Timestamp      time.Time
	ChanIDIn       uint64
	ChanIDOut      uint64
	AmtInMsat      int64
	AmtOutMsat     int64
	FeeMsat        int64
}

// Channel represents a Lightning Network channel stored in the database.
type Channel struct {
	ChanID        uint64
	RemotePubKey  string
	ChannelPoint  string
	ClosingTxHash string
	Capacity      int64
	LocalBalance  int64
	RemoteBalance int64
	Active        bool
}

// Invoice represents a received payment stored in the database.
type Invoice struct {
	PaymentHash string
	AmtPaidMsat int64
	CreatedAt   time.Time
	SettledAt   *time.Time
}

// Payment represents a sent payment stored in the database.
type Payment struct {
	PaymentHash string
	Status      string
	ValueMsat   int64
	FeeMsat     int64
	CreatedAt   time.Time
}

// OnchainTx represents an on-chain transaction stored in the database.
type OnchainTx struct {
	TxHash           string
	AmountSat        int64
	NumConfirmations int32
	Timestamp        time.Time
	Label            string
}

// WalletBalanceSnapshot represents a wallet balance snapshot.
type WalletBalanceSnapshot struct {
	CapturedAt      time.Time
	TotalSat        int64
	ConfirmedSat    int64
	UnconfirmedSat  int64
}

// WatchedWallet represents a tracked wallet (address, xpub, or descriptor).
type WatchedWallet struct {
	ID             int64
	Label          string
	Type           string // "address", "xpub", or "descriptor"
	Value          string
	DerivationType string // "bip44", "bip49", "bip84" (unused for descriptor type)
	BalanceSats    int64
	LastCheckedAt  *time.Time
	CreatedAt      time.Time
}

// dbSyncTx provides transactional sync operations for a SQL transaction.
type dbSyncTx struct {
	tx  *sql.Tx
	ctx context.Context
}

// GetSyncState retrieves the sync state for a given source within the transaction.
// Returns zero time and 0 offset if not found (no error).
func (t *dbSyncTx) GetSyncState(source string) (time.Time, int64, error) {
	var lastSyncedAt sql.NullTime
	var lastOffset int64

	err := t.tx.QueryRowContext(t.ctx,
		`SELECT last_synced_at, last_offset FROM sync_state WHERE source = ?`,
		source,
	).Scan(&lastSyncedAt, &lastOffset)

	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, 0, nil
	}
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("get sync state for %q: %w", source, err)
	}

	if !lastSyncedAt.Valid {
		return time.Time{}, lastOffset, nil
	}

	return lastSyncedAt.Time, lastOffset, nil
}

// SetSyncState sets the sync state for a given source.
func (t *dbSyncTx) SetSyncState(source string, syncedAt time.Time, offset int64) error {
	_, err := t.tx.Exec(
		`INSERT OR REPLACE INTO sync_state (source, last_synced_at, last_offset) VALUES (?, ?, ?)`,
		source, syncedAt, offset,
	)
	if err != nil {
		return fmt.Errorf("set sync state for %q: %w", source, err)
	}
	return nil
}

// InsertForwardingEvents inserts forwarding events into the database.
// Duplicate events are ignored via the unique index.
func (t *dbSyncTx) InsertForwardingEvents(events []ForwardingEvent) error {
	if len(events) == 0 {
		return nil
	}

	for _, event := range events {
		_, err := t.tx.Exec(
			`INSERT OR IGNORE INTO forwarding_events (timestamp, chan_id_in, chan_id_out, amt_in_msat, amt_out_msat, fee_msat)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			event.Timestamp, event.ChanIDIn, event.ChanIDOut, event.AmtInMsat, event.AmtOutMsat, event.FeeMsat,
		)
		if err != nil {
			return fmt.Errorf("insert forwarding event for channels %d→%d: %w", event.ChanIDIn, event.ChanIDOut, err)
		}
	}

	return nil
}

// UpsertChannels inserts or updates channels in the database.
func (t *dbSyncTx) UpsertChannels(channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}

	for _, ch := range channels {
		_, err := t.tx.Exec(
			`INSERT INTO channels (chan_id, remote_pubkey, channel_point, closing_tx_hash, capacity, local_balance, remote_balance, active)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(chan_id) DO UPDATE SET
			   remote_pubkey = excluded.remote_pubkey,
			   channel_point = CASE WHEN excluded.channel_point != '' THEN excluded.channel_point ELSE channels.channel_point END,
			   closing_tx_hash = CASE WHEN excluded.closing_tx_hash != '' THEN excluded.closing_tx_hash ELSE channels.closing_tx_hash END,
			   capacity = CASE WHEN excluded.capacity > 0 THEN excluded.capacity ELSE channels.capacity END,
			   local_balance = excluded.local_balance,
			   remote_balance = excluded.remote_balance,
			   active = excluded.active`,
			ch.ChanID, ch.RemotePubKey, ch.ChannelPoint, ch.ClosingTxHash, ch.Capacity, ch.LocalBalance, ch.RemoteBalance, boolToInt(ch.Active),
		)
		if err != nil {
			return fmt.Errorf("upsert channel %d: %w", ch.ChanID, err)
		}
	}

	return nil
}

// UpsertInvoices inserts or updates invoices in the database.
func (t *dbSyncTx) UpsertInvoices(invoices []Invoice) error {
	if len(invoices) == 0 {
		return nil
	}

	for _, inv := range invoices {
		_, err := t.tx.Exec(
			`INSERT OR REPLACE INTO invoices (payment_hash, amt_paid_msat, created_at, settled_at)
			 VALUES (?, ?, ?, ?)`,
			inv.PaymentHash, inv.AmtPaidMsat, inv.CreatedAt, inv.SettledAt,
		)
		if err != nil {
			return fmt.Errorf("upsert invoice %s: %w", inv.PaymentHash, err)
		}
	}

	return nil
}

// UpsertPayments inserts or updates payments in the database.
func (t *dbSyncTx) UpsertPayments(payments []Payment) error {
	if len(payments) == 0 {
		return nil
	}

	for _, pmt := range payments {
		_, err := t.tx.Exec(
			`INSERT OR REPLACE INTO payments (payment_hash, status, value_msat, fee_msat, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			pmt.PaymentHash, pmt.Status, pmt.ValueMsat, pmt.FeeMsat, pmt.CreatedAt,
		)
		if err != nil {
			return fmt.Errorf("upsert payment %s (%s): %w", pmt.PaymentHash, pmt.Status, err)
		}
	}

	return nil
}

// UpsertOnchainTxns inserts or updates on-chain transactions.
// Confirmation count and label may change between syncs.
func (t *dbSyncTx) UpsertOnchainTxns(txns []OnchainTx) error {
	if len(txns) == 0 {
		return nil
	}

	for _, tx := range txns {
		_, err := t.tx.Exec(
			`INSERT OR REPLACE INTO onchain_txns (tx_hash, amount_sat, num_confirmations, timestamp, label)
			 VALUES (?, ?, ?, ?, ?)`,
			tx.TxHash, tx.AmountSat, tx.NumConfirmations, tx.Timestamp, tx.Label,
		)
		if err != nil {
			return fmt.Errorf("upsert onchain tx %s: %w", tx.TxHash, err)
		}
	}

	return nil
}

// InsertWalletBalanceSnapshot inserts a wallet balance snapshot.
func (t *dbSyncTx) InsertWalletBalanceSnapshot(s WalletBalanceSnapshot) error {
	_, err := t.tx.Exec(
		`INSERT INTO wallet_balance_snapshots (captured_at, total_sat, confirmed_sat, unconfirmed_sat)
		 VALUES (?, ?, ?, ?)`,
		s.CapturedAt, s.TotalSat, s.ConfirmedSat, s.UnconfirmedSat,
	)
	if err != nil {
		return fmt.Errorf("insert wallet balance snapshot: %w", err)
	}
	return nil
}

// GetSyncState retrieves the sync state for a given source.
// Returns zero time and 0 offset if not found (no error).
func (d *DB) GetSyncState(ctx context.Context, source string) (time.Time, int64, error) {
	var lastSyncedAt sql.NullTime
	var lastOffset int64

	err := d.db.QueryRowContext(ctx,
		`SELECT last_synced_at, last_offset FROM sync_state WHERE source = ?`,
		source,
	).Scan(&lastSyncedAt, &lastOffset)

	if errors.Is(err, sql.ErrNoRows) {
		// Not synced yet; return zero values
		return time.Time{}, 0, nil
	}
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("get sync state for %q: %w", source, err)
	}

	if !lastSyncedAt.Valid {
		return time.Time{}, lastOffset, nil
	}

	return lastSyncedAt.Time, lastOffset, nil
}

// SyncTx defines the transactional operations available during a sync cycle.
type SyncTx interface {
	SetSyncState(source string, syncedAt time.Time, offset int64) error
	GetSyncState(source string) (time.Time, int64, error)
	InsertForwardingEvents(events []ForwardingEvent) error
	UpsertChannels(channels []Channel) error
	UpsertInvoices(invoices []Invoice) error
	UpsertPayments(payments []Payment) error
	UpsertOnchainTxns(txns []OnchainTx) error
	InsertWalletBalanceSnapshot(s WalletBalanceSnapshot) error
}

// RunSync wraps a sync operation in a transaction.
// If the function returns an error, the transaction is rolled back.
func (d *DB) RunSync(ctx context.Context, fn func(SyncTx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	syncTx := &dbSyncTx{tx: tx, ctx: ctx}
	if err := fn(syncTx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("fn error: %w; rollback error: %v", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// boolToInt converts a boolean to an integer (1 or 0).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
