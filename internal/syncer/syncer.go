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
	WalletBalance(ctx context.Context) (*lnd.WalletBalance, error)
}

// SyncTx represents a transaction context for syncing operations.
// Defined here at the use-site per idiomatic Go.
type SyncTx interface {
	SetSyncState(source string, syncedAt time.Time, offset int64) error
	InsertForwardingEvents(events []db.ForwardingEvent) error
	UpsertChannels(channels []db.Channel) error
	UpsertInvoices(invoices []db.Invoice) error
	UpsertPayments(payments []db.Payment) error
	InsertWalletBalanceSnapshot(s db.WalletBalanceSnapshot) error
}

// Store defines the interface for persisting sync data.
type Store interface {
	GetSyncState(ctx context.Context, source string) (time.Time, int64, error)
	RunSync(ctx context.Context, fn func(interface{}) error) error // fn receives SyncTx implementer (see db package)
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
	return s.store.RunSync(ctx, func(tx interface{}) error {
		syncTx := tx.(SyncTx)
		return s.syncCycle(ctx, syncTx)
	})
}

// syncCycle is the core sync logic, executed within a transaction.
func (s *Syncer) syncCycle(ctx context.Context, tx SyncTx) error {
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

	if err := s.syncWalletBalance(ctx, tx); err != nil {
		return err
	}

	return nil
}

// syncForwardingEvents syncs forwarding events since the last sync timestamp.
func (s *Syncer) syncForwardingEvents(ctx context.Context, tx SyncTx) error {
	// Get the last sync time for forwarding events
	lastSyncedAt, _, err := s.store.GetSyncState(ctx, "forwarding")
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

	// Fetch forwarding events
	events, err := s.lnd.ForwardingHistory(ctx, startTime, endTime)
	if err != nil {
		return err
	}

	// Convert to DB types and insert
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

	// Update sync state
	if err := tx.SetSyncState("forwarding", endTime, 0); err != nil {
		return err
	}

	s.logger.Printf("synced %d forwarding events since %s", len(dbEvents), startTime.Format(time.RFC3339))
	return nil
}

// syncChannels syncs the current channel state.
func (s *Syncer) syncChannels(ctx context.Context, tx SyncTx) error {
	// Fetch current channels
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return err
	}

	// Convert to DB types
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
func (s *Syncer) syncInvoices(ctx context.Context, tx SyncTx) error {
	// Get the last sync offset for invoices
	_, lastOffset, err := s.store.GetSyncState(ctx, "invoices")
	if err != nil {
		return err
	}

	// Fetch invoices starting from the offset
	invoices, nextOffset, err := s.lnd.ListInvoices(ctx, uint64(lastOffset))
	if err != nil {
		return err
	}

	// Convert to DB types
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

	// Update sync state with the next offset
	if err := tx.SetSyncState("invoices", time.Now(), int64(nextOffset)); err != nil {
		return err
	}

	s.logger.Printf("synced %d invoices", len(dbInvoices))
	return nil
}

// syncPayments syncs payments since the last sync offset.
func (s *Syncer) syncPayments(ctx context.Context, tx SyncTx) error {
	// Get the last sync offset for payments
	_, lastOffset, err := s.store.GetSyncState(ctx, "payments")
	if err != nil {
		return err
	}

	// Fetch payments starting from the offset
	payments, nextOffset, err := s.lnd.ListPayments(ctx, uint64(lastOffset))
	if err != nil {
		return err
	}

	// Convert to DB types
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

	// Update sync state with the next offset
	if err := tx.SetSyncState("payments", time.Now(), int64(nextOffset)); err != nil {
		return err
	}

	s.logger.Printf("synced %d payments", len(dbPayments))
	return nil
}

// syncWalletBalance syncs the current wallet balance.
func (s *Syncer) syncWalletBalance(ctx context.Context, tx SyncTx) error {
	// Fetch wallet balance
	balance, err := s.lnd.WalletBalance(ctx)
	if err != nil {
		return err
	}

	now := time.Now()

	// Insert snapshot
	snapshot := db.WalletBalanceSnapshot{
		CapturedAt:     now,
		TotalSat:       balance.TotalBalance,
		ConfirmedSat:   balance.ConfirmedBalance,
		UnconfirmedSat: balance.UnconfirmedBalance,
	}

	if err := tx.InsertWalletBalanceSnapshot(snapshot); err != nil {
		return err
	}

	// Update sync state
	if err := tx.SetSyncState("wallet", now, 0); err != nil {
		return err
	}

	s.logger.Printf("synced wallet balance: %d sats total, %d confirmed, %d unconfirmed",
		balance.TotalBalance, balance.ConfirmedBalance, balance.UnconfirmedBalance)
	return nil
}
