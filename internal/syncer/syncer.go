package syncer

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/exchange"
	"github.com/satsbook/satsbook/internal/lnd"
	"github.com/satsbook/satsbook/internal/monarch"
)

// LNDClient defines the interface for interacting with the LND node.
type LNDClient interface {
	ListChannels(ctx context.Context) ([]lnd.Channel, error)
	ClosedChannels(ctx context.Context) ([]lnd.ClosedChannel, error)
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

// SnapshotStore captures portfolio snapshots after each sync cycle.
type SnapshotStore interface {
	TotalPortfolioSats(ctx context.Context) (int64, error)
	InsertPortfolioSnapshot(ctx context.Context, totalSats int64, btcPriceUSD float64) error
	PortfolioBreakdownQuery(ctx context.Context) (*db.PortfolioBreakdown, error)
	InsertPortfolioSnapshotWithDetails(ctx context.Context, totalSats int64, btcPriceUSD float64, details map[string]int64) error
	AutoTagChannelTransfers(ctx context.Context) (int, error)
}

// PriceProvider fetches the current BTC/USD price for snapshots.
type PriceProvider interface {
	GetBTCPrice(ctx context.Context) (float64, error)
}

// MonarchSyncer pushes BTC holdings to Monarch Money.
type MonarchSyncer interface {
	SyncHolding(ctx context.Context, btcQuantity float64) error
	SyncTransactions(ctx context.Context, txns []monarch.TxToSync) (*monarch.TxSyncResult, map[string]string, error)
}

// MonarchTxStore reads unsynced transactions and records synced ones.
type MonarchTxStore interface {
	ListUnsyncedTransactions(ctx context.Context, txTypes []string, sources []string) ([]db.UnifiedTransaction, error)
	MarkTransactionSynced(ctx context.Context, sourceID, monarchTxID string) error
}

// SettingsReader reads app settings.
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// StrikeRowFetcher fetches transaction rows and live balance from the Strike REST API.
type StrikeRowFetcher interface {
	FetchRows(ctx context.Context) ([]exchange.StrikeRow, error)
	FetchBalance(ctx context.Context) (int64, error)
}

// StrikeImporter persists Strike rows to the database.
type StrikeImporter interface {
	ImportStrikeCSV(ctx context.Context, rows []exchange.StrikeRow) (*db.ImportSummary, error)
	SetSetting(ctx context.Context, key, value string) error
}

// CoinbaseRowFetcher fetches transaction rows and live balance from the Coinbase CDP API.
type CoinbaseRowFetcher interface {
	FetchRows(ctx context.Context) ([]exchange.CoinbaseRow, error)
	FetchBalance(ctx context.Context) (int64, error)
}

// CoinbaseImporter persists Coinbase rows to the database.
type CoinbaseImporter interface {
	ImportCoinbaseCSV(ctx context.Context, rows []exchange.CoinbaseRow) (*db.ImportSummary, error)
	SetSetting(ctx context.Context, key, value string) error
}

// Syncer orchestrates LND data synchronization.
type Syncer struct {
	lnd              LNDClient
	store            Store
	snapshot         SnapshotStore
	price            PriceProvider
	monarch          MonarchSyncer
	monarchTxStore   MonarchTxStore
	settings         SettingsReader
	strikeClient     StrikeRowFetcher
	strikeImporter   StrikeImporter
	strikeTrigger    chan struct{}
	coinbaseClient   CoinbaseRowFetcher
	coinbaseImporter CoinbaseImporter
	coinbaseTrigger  chan struct{}
	logger           *log.Logger
	syncInterval     time.Duration
	maxHistoryDays   int
}

// New creates a new Syncer.
func New(lnd LNDClient, store Store, logger *log.Logger, syncInterval time.Duration, maxHistoryDays int) *Syncer {
	return &Syncer{
		lnd:             lnd,
		store:           store,
		logger:          logger,
		syncInterval:    syncInterval,
		maxHistoryDays:  maxHistoryDays,
		strikeTrigger:   make(chan struct{}, 1),
		coinbaseTrigger: make(chan struct{}, 1),
	}
}

// SetSnapshotStore sets the snapshot store and price provider for portfolio snapshots.
func (s *Syncer) SetSnapshotStore(ss SnapshotStore, pp PriceProvider) {
	s.snapshot = ss
	s.price = pp
}

// SetMonarchSyncer sets the Monarch Money syncer for recurring holdings sync.
func (s *Syncer) SetMonarchSyncer(ms MonarchSyncer) {
	s.monarch = ms
}

// SetMonarchTxSync sets the stores needed for automatic Monarch transaction syncing.
func (s *Syncer) SetMonarchTxSync(txStore MonarchTxStore, settings SettingsReader) {
	s.monarchTxStore = txStore
	s.settings = settings
}

// SetStrikeClient sets the Strike API client and importer for automatic syncing.
// If a non-nil client is provided, an immediate sync is triggered.
func (s *Syncer) SetStrikeClient(client StrikeRowFetcher, importer StrikeImporter) {
	s.strikeClient = client
	s.strikeImporter = importer
	if client != nil {
		select {
		case s.strikeTrigger <- struct{}{}:
		default:
		}
	}
}

// SetCoinbaseClient sets the Coinbase CDP API client and importer for automatic syncing.
// If a non-nil client is provided, an immediate sync is triggered.
func (s *Syncer) SetCoinbaseClient(client CoinbaseRowFetcher, importer CoinbaseImporter) {
	s.coinbaseClient = client
	s.coinbaseImporter = importer
	if client != nil {
		select {
		case s.coinbaseTrigger <- struct{}{}:
		default:
		}
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
		case <-s.strikeTrigger:
			s.syncStrikeAPI(ctx)
		case <-s.coinbaseTrigger:
			s.syncCoinbaseAPI(ctx)
		}
	}
}

// Sync performs one full synchronization cycle.
func (s *Syncer) Sync(ctx context.Context) error {
	err := s.store.RunSync(ctx, func(tx db.SyncTx) error {
		return s.syncCycle(ctx, tx)
	})
	if err != nil {
		return err
	}

	// Auto-tag channel open/close transactions as transfers
	if s.snapshot != nil {
		if n, err := s.snapshot.AutoTagChannelTransfers(ctx); err != nil {
			s.logger.Printf("auto-tag channel transfers: %v", err)
		} else {
			s.logger.Printf("auto-tag channel transfers: %d updated", n)
		}
	}

	// Capture portfolio snapshot after successful sync
	if s.snapshot != nil && s.price != nil {
		s.captureSnapshot(ctx)
	}

	// Sync transactions to Monarch if configured
	if s.monarch != nil && s.monarchTxStore != nil && s.settings != nil {
		s.syncMonarchTransactions(ctx)
	}

	// Sync Strike API transactions if configured
	if s.strikeClient != nil && s.strikeImporter != nil {
		s.syncStrikeAPI(ctx)
	}

	return nil
}

// captureSnapshot records the current total portfolio value with per-source breakdown.
func (s *Syncer) captureSnapshot(ctx context.Context) {
	breakdown, err := s.snapshot.PortfolioBreakdownQuery(ctx)
	if err != nil {
		s.logger.Printf("snapshot: failed to compute breakdown: %v", err)
		return
	}

	var btcPrice float64
	price, err := s.price.GetBTCPrice(ctx)
	if err == nil {
		btcPrice = price
	}

	// Build per-source detail map
	details := make(map[string]int64)
	if breakdown.OnChainSats != 0 {
		details["onchain"] = breakdown.OnChainSats
	}
	if breakdown.ChannelSats != 0 {
		details["channels"] = breakdown.ChannelSats
	}
	if breakdown.ColdStorageSats != 0 {
		details["cold_storage"] = breakdown.ColdStorageSats
	}
	for source, sats := range breakdown.ExchangeSats {
		details[source] = sats
	}

	if err := s.snapshot.InsertPortfolioSnapshotWithDetails(ctx, breakdown.TotalSats, btcPrice, details); err != nil {
		s.logger.Printf("snapshot: failed to insert: %v", err)
	}

	// Sync holdings to Monarch Money
	if s.monarch != nil {
		btcQuantity := float64(breakdown.TotalSats) / 1e8
		if err := s.monarch.SyncHolding(ctx, btcQuantity); err != nil {
			s.logger.Printf("monarch: sync failed: %v", err)
		}
	}
}

// syncMonarchTransactions syncs unsynced transactions to Monarch Money.
func (s *Syncer) syncMonarchTransactions(ctx context.Context) {
	syncTypes, err := s.settings.GetSetting(ctx, "monarch_sync_types")
	if err != nil || syncTypes == "" {
		return // no types configured, skip
	}

	types := strings.Split(syncTypes, ",")

	var sources []string
	if v, err := s.settings.GetSetting(ctx, "monarch_sync_sources"); err == nil && v != "" {
		sources = strings.Split(v, ",")
	}

	txns, err := s.monarchTxStore.ListUnsyncedTransactions(ctx, types, sources)
	if err != nil {
		s.logger.Printf("monarch tx sync: list unsynced: %v", err)
		return
	}
	if len(txns) == 0 {
		return
	}

	toSync := make([]monarch.TxToSync, len(txns))
	for i, tx := range txns {
		toSync[i] = monarch.TxToSync{
			SourceID:  tx.SourceID,
			Source:    tx.Source,
			TxType:   tx.TxType,
			Time:     tx.Time,
			AmountUSD: tx.AmountUSD,
			Memo:     tx.Memo,
		}
	}

	result, synced, err := s.monarch.SyncTransactions(ctx, toSync)
	if err != nil {
		s.logger.Printf("monarch tx sync failed: %v", err)
		return
	}

	for sourceID, monarchTxID := range synced {
		if err := s.monarchTxStore.MarkTransactionSynced(ctx, sourceID, monarchTxID); err != nil {
			s.logger.Printf("monarch: failed to mark %s synced: %v", sourceID, err)
		}
	}

	s.logger.Printf("monarch tx sync: %d created, %d skipped, %d errors", result.Created, result.Skipped, result.Errors)
}

// syncStrikeAPI fetches new transactions and live balance from Strike and persists them.
func (s *Syncer) syncStrikeAPI(ctx context.Context) {
	s.logger.Printf("strike api sync: starting")

	// Fetch and store live balance.
	if sats, err := s.strikeClient.FetchBalance(ctx); err != nil {
		s.logger.Printf("strike api sync: fetch balance: %v", err)
	} else {
		if err := s.strikeImporter.SetSetting(ctx, "strike_live_balance_sats", strconv.FormatInt(sats, 10)); err != nil {
			s.logger.Printf("strike api sync: store balance: %v", err)
		} else {
			s.logger.Printf("strike api sync: balance %d sats", sats)
		}
	}

	rows, err := s.strikeClient.FetchRows(ctx)
	if err != nil {
		s.logger.Printf("strike api sync: fetch rows: %v", err)
		return
	}
	s.logger.Printf("strike api sync: fetched %d rows", len(rows))
	if len(rows) == 0 {
		return
	}
	summary, err := s.strikeImporter.ImportStrikeCSV(ctx, rows)
	if err != nil {
		s.logger.Printf("strike api sync: import: %v", err)
		return
	}
	s.logger.Printf("strike api sync: %d new, %d updated, %d duplicates", summary.NewPurchases, summary.Updated, summary.Duplicates)
}

// syncCoinbaseAPI fetches new transactions and live balance from Coinbase and persists them.
func (s *Syncer) syncCoinbaseAPI(ctx context.Context) {
	if s.coinbaseClient == nil || s.coinbaseImporter == nil {
		return
	}
	s.logger.Printf("coinbase api sync: starting")

	if sats, err := s.coinbaseClient.FetchBalance(ctx); err != nil {
		s.logger.Printf("coinbase api sync: fetch balance: %v", err)
	} else {
		if err := s.coinbaseImporter.SetSetting(ctx, "coinbase_live_balance_sats", strconv.FormatInt(sats, 10)); err != nil {
			s.logger.Printf("coinbase api sync: store balance: %v", err)
		} else {
			s.logger.Printf("coinbase api sync: balance %d sats", sats)
		}
	}

	rows, err := s.coinbaseClient.FetchRows(ctx)
	if err != nil {
		s.logger.Printf("coinbase api sync: fetch rows: %v", err)
		return
	}
	s.logger.Printf("coinbase api sync: fetched %d rows", len(rows))
	if len(rows) == 0 {
		return
	}
	summary, err := s.coinbaseImporter.ImportCoinbaseCSV(ctx, rows)
	if err != nil {
		s.logger.Printf("coinbase api sync: import: %v", err)
		return
	}
	s.logger.Printf("coinbase api sync: %d new, %d updated, %d duplicates", summary.NewPurchases, summary.Updated, summary.Duplicates)
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
			ChannelPoint:  ch.ChannelPoint,
			Capacity:      ch.Capacity,
			LocalBalance:  ch.LocalBalance,
			RemoteBalance: ch.RemoteBalance,
			Active:        ch.Active,
		}
	}

	if err := tx.UpsertChannels(dbChannels); err != nil {
		return err
	}

	// Also sync closed channels to capture closing tx hashes
	closedChannels, err := s.lnd.ClosedChannels(ctx)
	if err != nil {
		s.logger.Printf("warning: failed to list closed channels: %v", err)
	} else if len(closedChannels) > 0 {
		dbClosed := make([]db.Channel, len(closedChannels))
		for i, ch := range closedChannels {
			dbClosed[i] = db.Channel{
				ChanID:        ch.ChannelID,
				RemotePubKey:  ch.RemotePubKey,
				ChannelPoint:  ch.ChannelPoint,
				ClosingTxHash: ch.ClosingTxHash,
				Capacity:      ch.Capacity,
				Active:        false,
			}
		}
		if err := tx.UpsertChannels(dbClosed); err != nil {
			return err
		}
		s.logger.Printf("synced %d closed channels", len(dbClosed))
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
