package syncer

import (
	"context"
	"log"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
)

// LNDClient defines the interface for interacting with the LND node.
type LNDClient interface {
	ListChannels(ctx context.Context) ([]lnd.Channel, error)
	ForwardingHistory(ctx context.Context, start, end time.Time) ([]lnd.ForwardingEvent, error)
	ListInvoices(ctx context.Context, offset uint64) ([]lnd.Invoice, uint64, error)
	ListPayments(ctx context.Context, offset uint64) ([]lnd.Payment, uint64, error)
	GetTransactions(ctx context.Context) ([]lnd.OnchainTx, error)
	WalletBalance(ctx context.Context) (*lnd.WalletBalance, error)
}

// Store defines the interface for persisting sync data.
type Store interface {
	RunSync(ctx context.Context, fn func(db.SyncTx) error) error
}

// Syncer orchestrates LND data synchronization.
type Syncer struct {
	lnd            LNDClient
	store          Store
	logger         *log.Logger
	syncInterval   time.Duration
	maxHistoryDays int
}

// New creates a new Syncer.
func New(lnd LNDClient, store Store, logger *log.Logger, syncInterval time.Duration, maxHistoryDays int) *Syncer {
	return &Syncer{
		lnd:            lnd,
		store:          store,
		logger:         logger,
		syncInterval:   syncInterval,
		maxHistoryDays: maxHistoryDays,
	}
}

// Run blocks until ctx is cancelled, syncing on startup and then on the configured interval.
func (s *Syncer) Run(ctx context.Context) {
	// Sync immediately on startup
	if err := s.Sync(ctx); err != nil {
		s.logger.Printf("initial sync failed: %v", err)
	}

	ticker := time.NewTicker(s.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Sync(ctx); err != nil {
				s.logger.Printf("sync failed: %v", err)
			}
		}
	}
}

// Sync performs one full synchronization cycle.
func (s *Syncer) Sync(ctx context.Context) error {
	return s.store.RunSync(ctx, func(tx db.SyncTx) error {
		return s.syncCycle(ctx, tx)
	})
}

// syncCycle is the core sync logic, executed within a transaction.
func (s *Syncer) syncCycle(ctx context.Context, tx db.SyncTx) error {
	if err := s.syncForwardingEvents(ctx, tx); err != nil {
		return err
	}

	if err := s.syncChannels(ctx, tx); err != nil {
		return err
	}

	if err := s.syncInvoices(ctx, tx); err != nil {
		return err
	}

	if err := s.syncPayments(ctx, tx); err != nil {
		return err
	}

	if err := s.syncOnchainTxns(ctx, tx); err != nil {
		return err
	}

	if err := s.syncWalletBalance(ctx, tx); err != nil {
		return err
	}

	return nil
}

// syncForwardingEvents syncs forwarding events since the last sync timestamp.
func (s *Syncer) syncForwardingEvents(ctx context.Context, tx db.SyncTx) error {
	// Read state within the transaction for consistent isolation
	lastSyncedAt, _, err := tx.GetSyncState("forwarding")
	if err != nil {
		return err
	}

	// If never synced, start from maxHistoryDays ago
	var startTime time.Time
	if lastSyncedAt.IsZero() {
		startTime = time.Now().AddDate(0, 0, -s.maxHistoryDays)
	} else {
		startTime = lastSyncedAt
	}

	endTime := time.Now()

	// Fetch forwarding events from LND
	events, err := s.lnd.ForwardingHistory(ctx, startTime, endTime)
	if err != nil {
		return err
	}

	// Convert to DB types
	dbEvents := make([]db.ForwardingEvent, len(events))
	for i, ev := range events {
		dbEvents[i] = db.ForwardingEvent{
			Timestamp:  ev.Timestamp,
			ChanIDIn:   ev.ChanIDIn,
			ChanIDOut:  ev.ChanIDOut,
			AmtInMsat:  int64(ev.AmountIn),
			AmtOutMsat: int64(ev.AmountOut),
			FeeMsat:    int64(ev.Fee),
		}
	}

	if err := tx.InsertForwardingEvents(dbEvents); err != nil {
		return err
	}

	if err := tx.SetSyncState("forwarding", endTime, 0); err != nil {
		return err
	}

	s.logger.Printf("synced %d forwarding events since %s", len(dbEvents), startTime.Format(time.RFC3339))
	return nil
}

// syncChannels syncs the current channel state.
func (s *Syncer) syncChannels(ctx context.Context, tx db.SyncTx) error {
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}

	dbChannels := make([]db.Channel, len(channels))
	for i, ch := range channels {
		dbChannels[i] = db.Channel{
			ChanID:        ch.ChannelID,
			RemotePubKey:  ch.RemotePubKey,
			LocalBalance:  ch.LocalBalance,
			RemoteBalance: ch.RemoteBalance,
			Active:        ch.Active,
		}
	}

	if err := tx.UpsertChannels(dbChannels); err != nil {
		return err
	}

	s.logger.Printf("synced %d channels", len(dbChannels))
	return nil
}

// syncInvoices syncs invoices since the last sync offset.
func (s *Syncer) syncInvoices(ctx context.Context, tx db.SyncTx) error {
	_, lastOffset, err := tx.GetSyncState("invoices")
	if err != nil {
		return err
	}

	invoices, nextOffset, err := s.lnd.ListInvoices(ctx, uint64(lastOffset))
	if err != nil {
		return err
	}

	dbInvoices := make([]db.Invoice, len(invoices))
	for i, inv := range invoices {
		var settledAt *time.Time
		if !inv.SettleDate.IsZero() {
			settledAt = &inv.SettleDate
		}

		dbInvoices[i] = db.Invoice{
			PaymentHash: inv.RHash,
			AmtPaidMsat: inv.ValueMsat,
			CreatedAt:   inv.CreationDate,
			SettledAt:   settledAt,
		}
	}

	if err := tx.UpsertInvoices(dbInvoices); err != nil {
		return err
	}

	if err := tx.SetSyncState("invoices", time.Now(), int64(nextOffset)); err != nil {
		return err
	}

	s.logger.Printf("synced %d invoices", len(dbInvoices))
	return nil
}

// syncPayments syncs payments since the last sync offset.
func (s *Syncer) syncPayments(ctx context.Context, tx db.SyncTx) error {
	_, lastOffset, err := tx.GetSyncState("payments")
	if err != nil {
		return err
	}

	payments, nextOffset, err := s.lnd.ListPayments(ctx, uint64(lastOffset))
	if err != nil {
		return err
	}

	dbPayments := make([]db.Payment, len(payments))
	for i, pmt := range payments {
		dbPayments[i] = db.Payment{
			PaymentHash: pmt.PaymentHash,
			Status:      pmt.Status,
			ValueMsat:   pmt.ValueMsat,
			FeeMsat:     pmt.FeeMsat,
			CreatedAt:   pmt.CreationDate,
		}
	}

	if err := tx.UpsertPayments(dbPayments); err != nil {
		return err
	}

	if err := tx.SetSyncState("payments", time.Now(), int64(nextOffset)); err != nil {
		return err
	}

	s.logger.Printf("synced %d payments", len(dbPayments))
	return nil
}

// syncOnchainTxns syncs on-chain transactions from the wallet.
// This always fetches the full list and upserts — confirmation counts change over time.
func (s *Syncer) syncOnchainTxns(ctx context.Context, tx db.SyncTx) error {
	txns, err := s.lnd.GetTransactions(ctx)
	if err != nil {
		return err
	}

	dbTxns := make([]db.OnchainTx, len(txns))
	for i, t := range txns {
		dbTxns[i] = db.OnchainTx{
			TxHash:           t.TxHash,
			AmountSat:        t.Amount,
			NumConfirmations: t.NumConfirmations,
			Timestamp:        t.Timestamp,
			Label:            t.Label,
		}
	}

	if err := tx.UpsertOnchainTxns(dbTxns); err != nil {
		return err
	}

	if err := tx.SetSyncState("onchain", time.Now(), 0); err != nil {
		return err
	}

	s.logger.Printf("synced %d on-chain transactions", len(dbTxns))
	return nil
}

// syncWalletBalance syncs the current wallet balance.
func (s *Syncer) syncWalletBalance(ctx context.Context, tx db.SyncTx) error {
	balance, err := s.lnd.WalletBalance(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	snapshot := db.WalletBalanceSnapshot{
		CapturedAt:     now,
		TotalSat:       balance.TotalBalance,
		ConfirmedSat:   balance.ConfirmedBalance,
		UnconfirmedSat: balance.UnconfirmedBalance,
	}

	if err := tx.InsertWalletBalanceSnapshot(snapshot); err != nil {
		return err
	}

	if err := tx.SetSyncState("wallet", now, 0); err != nil {
		return err
	}

	s.logger.Printf("synced wallet balance: %d sats total, %d confirmed, %d unconfirmed",
		balance.TotalBalance, balance.ConfirmedBalance, balance.UnconfirmedBalance)
	return nil
}
