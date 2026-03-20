package db

import (
	"context"
	"database/sql"
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
	ChanID         uint64
	RemotePubKey   string
	LocalBalance   int64
	RemoteBalance  int64
	Active         bool
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

// WalletBalanceSnapshot represents a wallet balance snapshot.
type WalletBalanceSnapshot struct {
	CapturedAt      time.Time
	TotalSat        int64
	ConfirmedSat    int64
	UnconfirmedSat  int64
}

// SyncTx represents a transaction context for syncing operations.
type SyncTx interface {
	SetSyncState(source string, syncedAt time.Time, offset int64) error
	InsertForwardingEvents(events []ForwardingEvent) error
	UpsertChannels(channels []Channel) error
	UpsertInvoices(invoices []Invoice) error
	UpsertPayments(payments []Payment) error
	InsertWalletBalanceSnapshot(s WalletBalanceSnapshot) error
}

// dbSyncTx implements SyncTx interface for a SQL transaction.
type dbSyncTx struct {
	tx *sql.Tx
}

// SetSyncState sets the sync state for a given source.
func (t *dbSyncTx) SetSyncState(source string, syncedAt time.Time, offset int64) error {
	_, err := t.tx.Exec(
		`INSERT OR REPLACE INTO sync_state (source, last_synced_at, last_offset) VALUES (?, ?, ?)`,
		source, syncedAt, offset,
	)
	return err
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
			return err
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
			`INSERT OR REPLACE INTO channels (chan_id, remote_pubkey, local_balance, remote_balance, active)
			 VALUES (?, ?, ?, ?, ?)`,
			ch.ChanID, ch.RemotePubKey, ch.LocalBalance, ch.RemoteBalance, boolToInt(ch.Active),
		)
		if err != nil {
			return err
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
			return err
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
			return err
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
	return err
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

	if err == sql.ErrNoRows {
		// Not synced yet; return zero values
		return time.Time{}, 0, nil
	}
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("failed to get sync state: %w", err)
	}

	if !lastSyncedAt.Valid {
		return time.Time{}, lastOffset, nil
	}

	return lastSyncedAt.Time, lastOffset, nil
}

// RunSync wraps a sync operation in a transaction.
// If the function returns an error, the transaction is rolled back.
func (d *DB) RunSync(ctx context.Context, fn func(SyncTx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	syncTx := &dbSyncTx{tx: tx}
	if err := fn(syncTx); err != nil {
		tx.Rollback()
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
