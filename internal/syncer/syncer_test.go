package syncer

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
	"github.com/satsbook/satsbook/internal/monarch"
)

// mockLNDClient mocks the LNDClient interface with per-method error control.
type mockLNDClient struct {
	channels          []lnd.Channel
	forwardingHistory []lnd.ForwardingEvent
	invoices          []lnd.Invoice
	payments          []lnd.Payment
	onchainTxns       []lnd.OnchainTx
	walletBalance     *lnd.WalletBalance

	// Per-method errors for targeted failure testing
	channelsErr          error
	forwardingHistoryErr error
	invoicesErr          error
	paymentsErr          error
	onchainTxnsErr       error
	walletBalanceErr     error
}

func (m *mockLNDClient) ListChannels(ctx context.Context) ([]lnd.Channel, error) {
	if m.channelsErr != nil {
		return nil, m.channelsErr
	}
	return m.channels, nil
}

func (m *mockLNDClient) ForwardingHistory(ctx context.Context, start, end time.Time) ([]lnd.ForwardingEvent, error) {
	if m.forwardingHistoryErr != nil {
		return nil, m.forwardingHistoryErr
	}
	return m.forwardingHistory, nil
}

func (m *mockLNDClient) ListInvoices(ctx context.Context, offset uint64) ([]lnd.Invoice, uint64, error) {
	if m.invoicesErr != nil {
		return nil, 0, m.invoicesErr
	}
	return m.invoices, offset + uint64(len(m.invoices)), nil
}

func (m *mockLNDClient) ListPayments(ctx context.Context, offset uint64) ([]lnd.Payment, uint64, error) {
	if m.paymentsErr != nil {
		return nil, 0, m.paymentsErr
	}
	return m.payments, offset + uint64(len(m.payments)), nil
}

func (m *mockLNDClient) GetTransactions(ctx context.Context) ([]lnd.OnchainTx, error) {
	if m.onchainTxnsErr != nil {
		return nil, m.onchainTxnsErr
	}
	return m.onchainTxns, nil
}

func (m *mockLNDClient) WalletBalance(ctx context.Context) (*lnd.WalletBalance, error) {
	if m.walletBalanceErr != nil {
		return nil, m.walletBalanceErr
	}
	return m.walletBalance, nil
}

// mockStore mocks the Store interface.
type mockStore struct {
	syncState map[string]syncStateEntry
	txConfig  *mockSyncTx // optional pre-configured tx for error injection
}

type syncStateEntry struct {
	lastSyncedAt time.Time
	lastOffset   int64
}

func newMockStore() *mockStore {
	return &mockStore{
		syncState: make(map[string]syncStateEntry),
	}
}

func (m *mockStore) RunSync(ctx context.Context, fn func(db.SyncTx) error) error {
	tx := &mockSyncTx{
		store: m,
	}
	if m.txConfig != nil {
		tx.insertForwardingErr = m.txConfig.insertForwardingErr
		tx.upsertChannelsErr = m.txConfig.upsertChannelsErr
		tx.upsertInvoicesErr = m.txConfig.upsertInvoicesErr
		tx.upsertPaymentsErr = m.txConfig.upsertPaymentsErr
		tx.upsertOnchainErr = m.txConfig.upsertOnchainErr
		tx.insertBalanceErr = m.txConfig.insertBalanceErr
		tx.setSyncStateErr = m.txConfig.setSyncStateErr
		tx.getSyncStateErr = m.txConfig.getSyncStateErr
	}
	return fn(tx)
}

// mockSyncTx mocks the db.SyncTx interface.
type mockSyncTx struct {
	store                    *mockStore
	forwardingEventsInserted []db.ForwardingEvent
	channelsUpserted         []db.Channel
	invoicesUpserted         []db.Invoice
	paymentsUpserted         []db.Payment
	onchainTxnsUpserted      []db.OnchainTx
	balanceSnapshots         []db.WalletBalanceSnapshot

	// Per-method errors for targeted failure testing
	insertForwardingErr error
	upsertChannelsErr   error
	upsertInvoicesErr   error
	upsertPaymentsErr   error
	upsertOnchainErr    error
	insertBalanceErr    error
	setSyncStateErr     map[string]error // keyed by source
	getSyncStateErr     map[string]error // keyed by source
}

func (m *mockSyncTx) GetSyncState(source string) (time.Time, int64, error) {
	if m.getSyncStateErr != nil {
		if err, ok := m.getSyncStateErr[source]; ok {
			return time.Time{}, 0, err
		}
	}
	if entry, ok := m.store.syncState[source]; ok {
		return entry.lastSyncedAt, entry.lastOffset, nil
	}
	return time.Time{}, 0, nil
}

func (m *mockSyncTx) SetSyncState(source string, syncedAt time.Time, offset int64) error {
	if m.setSyncStateErr != nil {
		if err, ok := m.setSyncStateErr[source]; ok {
			return err
		}
	}
	m.store.syncState[source] = syncStateEntry{
		lastSyncedAt: syncedAt,
		lastOffset:   offset,
	}
	return nil
}

func (m *mockSyncTx) InsertForwardingEvents(events []db.ForwardingEvent) error {
	if m.insertForwardingErr != nil {
		return m.insertForwardingErr
	}
	m.forwardingEventsInserted = append(m.forwardingEventsInserted, events...)
	return nil
}

func (m *mockSyncTx) UpsertChannels(channels []db.Channel) error {
	if m.upsertChannelsErr != nil {
		return m.upsertChannelsErr
	}
	m.channelsUpserted = append(m.channelsUpserted, channels...)
	return nil
}

func (m *mockSyncTx) UpsertInvoices(invoices []db.Invoice) error {
	if m.upsertInvoicesErr != nil {
		return m.upsertInvoicesErr
	}
	m.invoicesUpserted = append(m.invoicesUpserted, invoices...)
	return nil
}

func (m *mockSyncTx) UpsertPayments(payments []db.Payment) error {
	if m.upsertPaymentsErr != nil {
		return m.upsertPaymentsErr
	}
	m.paymentsUpserted = append(m.paymentsUpserted, payments...)
	return nil
}

func (m *mockSyncTx) UpsertOnchainTxns(txns []db.OnchainTx) error {
	if m.upsertOnchainErr != nil {
		return m.upsertOnchainErr
	}
	m.onchainTxnsUpserted = append(m.onchainTxnsUpserted, txns...)
	return nil
}

func (m *mockSyncTx) InsertWalletBalanceSnapshot(s db.WalletBalanceSnapshot) error {
	if m.insertBalanceErr != nil {
		return m.insertBalanceErr
	}
	m.balanceSnapshots = append(m.balanceSnapshots, s)
	return nil
}

func defaultMockBalance() *lnd.WalletBalance {
	return &lnd.WalletBalance{
		TotalBalance:       100000,
		ConfirmedBalance:   90000,
		UnconfirmedBalance: 10000,
	}
}

func newTestSyncer(lndClient LNDClient, store Store) *Syncer {
	logger := log.New(os.Stderr, "[syncer-test] ", 0)
	return New(lndClient, store, logger, 5*time.Minute, 90)
}

func TestSync_FirstRun(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		channels: []lnd.Channel{
			{
				ChannelID:     123,
				RemotePubKey:  "node1",
				LocalBalance:  50000,
				RemoteBalance: 50000,
				Active:        true,
			},
		},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Verify all sync sources wrote state
	for _, source := range []string{"forwarding", "invoices", "payments", "onchain", "wallet"} {
		entry, ok := store.syncState[source]
		if !ok {
			t.Errorf("expected sync state for %q", source)
			continue
		}
		if entry.lastSyncedAt.IsZero() {
			t.Errorf("expected non-zero sync time for %q", source)
		}
	}
}

func TestSync_IncrementalForwarding(t *testing.T) {
	store := newMockStore()
	previousSyncTime := time.Now().Add(-1 * time.Hour)
	store.syncState["forwarding"] = syncStateEntry{
		lastSyncedAt: previousSyncTime,
		lastOffset:   0,
	}

	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	entry := store.syncState["forwarding"]
	if entry.lastSyncedAt.Before(previousSyncTime) {
		t.Errorf("expected sync time to advance, got %v (previous: %v)", entry.lastSyncedAt, previousSyncTime)
	}
}

func TestSync_InvoiceOffset(t *testing.T) {
	store := newMockStore()
	store.syncState["invoices"] = syncStateEntry{
		lastSyncedAt: time.Now(),
		lastOffset:   100,
	}

	invoices := []lnd.Invoice{
		{
			RHash:        "hash1",
			ValueMsat:    100000,
			CreationDate: time.Now(),
			Settled:      true,
			SettleDate:   time.Now(),
		},
	}

	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          invoices,
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	entry := store.syncState["invoices"]
	expectedOffset := int64(100 + len(invoices))
	if entry.lastOffset != expectedOffset {
		t.Errorf("expected invoice offset %d, got %d", expectedOffset, entry.lastOffset)
	}
}

func TestSync_PaymentOffset(t *testing.T) {
	store := newMockStore()
	store.syncState["payments"] = syncStateEntry{
		lastSyncedAt: time.Now(),
		lastOffset:   50,
	}

	payments := []lnd.Payment{
		{
			PaymentHash:  "hash1",
			Value:        100000,
			Fee:          1000,
			CreationDate: time.Now(),
			Status:       "SUCCEEDED",
		},
	}

	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          payments,
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	entry := store.syncState["payments"]
	expectedOffset := int64(50 + len(payments))
	if entry.lastOffset != expectedOffset {
		t.Errorf("expected payment offset %d, got %d", expectedOffset, entry.lastOffset)
	}
}

func TestSync_ForwardingHistoryError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		forwardingHistoryErr: errors.New("forwarding history unavailable"),
		walletBalance:        defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from forwarding history failure")
	}

	// No sync state should be written on error
	if _, ok := store.syncState["forwarding"]; ok {
		t.Error("expected no sync state for forwarding after error")
	}
}

func TestSync_ChannelsError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		forwardingHistory: []lnd.ForwardingEvent{},
		channelsErr:       errors.New("channels unavailable"),
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from channels failure")
	}
}

func TestSync_InvoicesError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		forwardingHistory: []lnd.ForwardingEvent{},
		channels:          []lnd.Channel{},
		invoicesErr:       errors.New("invoices unavailable"),
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from invoices failure")
	}
}

func TestSync_PaymentsError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		forwardingHistory: []lnd.ForwardingEvent{},
		channels:          []lnd.Channel{},
		invoices:          []lnd.Invoice{},
		paymentsErr:       errors.New("payments unavailable"),
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from payments failure")
	}
}

func TestSync_OnchainTxnsError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		forwardingHistory: []lnd.ForwardingEvent{},
		channels:          []lnd.Channel{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		onchainTxnsErr:    errors.New("transactions unavailable"),
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from onchain txns failure")
	}
}

func TestSync_WalletBalanceError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		forwardingHistory: []lnd.ForwardingEvent{},
		channels:          []lnd.Channel{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		onchainTxns:       []lnd.OnchainTx{},
		walletBalanceErr:  errors.New("wallet unavailable"),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from wallet balance failure")
	}
}

func TestSync_EmptyResults(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Sync should still complete and write state for all sources
	if len(store.syncState) < 5 {
		t.Errorf("expected at least 5 sync state entries, got %d", len(store.syncState))
	}
}

func TestSync_WithForwardingEvents(t *testing.T) {
	store := newMockStore()
	now := time.Now()
	mockLND := &mockLNDClient{
		channels: []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{
			{
				Timestamp: now,
				ChanIDIn:  100,
				ChanIDOut: 200,
				AmountIn:  50000,
				AmountOut: 49000,
				Fee:       1000,
			},
		},
		invoices:      []lnd.Invoice{},
		payments:      []lnd.Payment{},
		walletBalance: defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
}

// mockSnapshotStore mocks the SnapshotStore interface.
type mockSnapshotStore struct {
	totalSats int64
	totalErr  error
	inserted  []struct {
		totalSats   int64
		btcPriceUSD float64
	}
	insertErr error
}

func (m *mockSnapshotStore) TotalPortfolioSats(ctx context.Context) (int64, error) {
	return m.totalSats, m.totalErr
}

func (m *mockSnapshotStore) InsertPortfolioSnapshot(ctx context.Context, totalSats int64, btcPriceUSD float64) error {
	m.inserted = append(m.inserted, struct {
		totalSats   int64
		btcPriceUSD float64
	}{totalSats, btcPriceUSD})
	return m.insertErr
}

func (m *mockSnapshotStore) PortfolioBreakdownQuery(ctx context.Context) (*db.PortfolioBreakdown, error) {
	if m.totalErr != nil {
		return nil, m.totalErr
	}
	return &db.PortfolioBreakdown{
		OnChainSats:     m.totalSats,
		ExchangeSats:    make(map[string]int64),
		TotalSats:       m.totalSats,
	}, nil
}

func (m *mockSnapshotStore) InsertPortfolioSnapshotWithDetails(ctx context.Context, totalSats int64, btcPriceUSD float64, details map[string]int64) error {
	m.inserted = append(m.inserted, struct {
		totalSats   int64
		btcPriceUSD float64
	}{totalSats, btcPriceUSD})
	return m.insertErr
}

// mockPriceProvider mocks the PriceProvider interface.
type mockPriceProvider struct {
	price float64
	err   error
}

func (m *mockPriceProvider) GetBTCPrice(ctx context.Context) (float64, error) {
	return m.price, m.err
}

func TestSync_CapturesSnapshot(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	ss := &mockSnapshotStore{totalSats: 5_000_000}
	pp := &mockPriceProvider{price: 65000.0}

	s := newTestSyncer(mockLND, store)
	s.SetSnapshotStore(ss, pp)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if len(ss.inserted) != 1 {
		t.Fatalf("expected 1 snapshot inserted, got %d", len(ss.inserted))
	}
	if ss.inserted[0].totalSats != 5_000_000 {
		t.Errorf("expected 5000000 sats, got %d", ss.inserted[0].totalSats)
	}
	if ss.inserted[0].btcPriceUSD != 65000.0 {
		t.Errorf("expected price 65000, got %f", ss.inserted[0].btcPriceUSD)
	}
}

func TestSync_SnapshotPriceError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	ss := &mockSnapshotStore{totalSats: 1_000_000}
	pp := &mockPriceProvider{err: errors.New("price unavailable")}

	s := newTestSyncer(mockLND, store)
	s.SetSnapshotStore(ss, pp)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Snapshot should still be inserted with price=0
	if len(ss.inserted) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(ss.inserted))
	}
	if ss.inserted[0].btcPriceUSD != 0 {
		t.Errorf("expected price 0 on error, got %f", ss.inserted[0].btcPriceUSD)
	}
}

func TestSync_NoSnapshotWithoutStore(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	// No SetSnapshotStore call

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	// No panic, no snapshot — just passes
}

// --- Mock helpers for Monarch transaction sync ---

// mockMonarchSyncer mocks the MonarchSyncer interface.
type mockMonarchSyncer struct {
	syncHoldingErr error
	syncHoldingCalls []float64

	syncTxResult *monarch.TxSyncResult
	syncTxSynced map[string]string
	syncTxErr    error
	syncTxCalls  int
}

func (m *mockMonarchSyncer) SyncHolding(ctx context.Context, btcQuantity float64) error {
	m.syncHoldingCalls = append(m.syncHoldingCalls, btcQuantity)
	return m.syncHoldingErr
}

func (m *mockMonarchSyncer) SyncTransactions(ctx context.Context, txns []monarch.TxToSync) (*monarch.TxSyncResult, map[string]string, error) {
	m.syncTxCalls++
	if m.syncTxErr != nil {
		return nil, nil, m.syncTxErr
	}
	return m.syncTxResult, m.syncTxSynced, nil
}

// mockMonarchTxStore mocks the MonarchTxStore interface.
type mockMonarchTxStore struct {
	unsyncedTxns    []db.UnifiedTransaction
	unsyncedErr     error
	markedSynced    map[string]string // sourceID -> monarchTxID
	markSyncedErr   error
}

func newMockMonarchTxStore() *mockMonarchTxStore {
	return &mockMonarchTxStore{
		markedSynced: make(map[string]string),
	}
}

func (m *mockMonarchTxStore) ListUnsyncedTransactions(ctx context.Context, txTypes []string, sources []string) ([]db.UnifiedTransaction, error) {
	if m.unsyncedErr != nil {
		return nil, m.unsyncedErr
	}
	return m.unsyncedTxns, nil
}

func (m *mockMonarchTxStore) MarkTransactionSynced(ctx context.Context, sourceID, monarchTxID string) error {
	if m.markSyncedErr != nil {
		return m.markSyncedErr
	}
	m.markedSynced[sourceID] = monarchTxID
	return nil
}

// mockSettingsReader mocks the SettingsReader interface.
type mockSettingsReader struct {
	settings map[string]string
	err      error
}

func (m *mockSettingsReader) GetSetting(ctx context.Context, key string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.settings[key], nil
}

// defaultEmptyLND creates a mock LND client with empty data and valid balance.
func defaultEmptyLND() *mockLNDClient {
	return &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}
}

// --- Tests for SetMonarchSyncer / SetMonarchTxSync ---

func TestSetMonarchSyncer(t *testing.T) {
	s := newTestSyncer(defaultEmptyLND(), newMockStore())
	ms := &mockMonarchSyncer{}
	s.SetMonarchSyncer(ms)
	if s.monarch == nil {
		t.Error("expected monarch syncer to be set")
	}
}

func TestSetMonarchTxSync(t *testing.T) {
	s := newTestSyncer(defaultEmptyLND(), newMockStore())
	txStore := newMockMonarchTxStore()
	settings := &mockSettingsReader{settings: map[string]string{}}
	s.SetMonarchTxSync(txStore, settings)
	if s.monarchTxStore != txStore {
		t.Error("expected monarchTxStore to be set")
	}
	if s.settings != settings {
		t.Error("expected settings to be set")
	}
}

// --- Tests for captureSnapshot edge cases ---

func TestSync_SnapshotTotalSatsError(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	ss := &mockSnapshotStore{totalErr: errors.New("db error")}
	pp := &mockPriceProvider{price: 65000.0}

	s := newTestSyncer(mockLND, store)
	s.SetSnapshotStore(ss, pp)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync should not fail due to snapshot error: %v", err)
	}
	// No snapshot should be inserted
	if len(ss.inserted) != 0 {
		t.Errorf("expected 0 snapshots, got %d", len(ss.inserted))
	}
}

func TestSync_SnapshotInsertError(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	ss := &mockSnapshotStore{totalSats: 1_000_000, insertErr: errors.New("insert failed")}
	pp := &mockPriceProvider{price: 65000.0}

	s := newTestSyncer(mockLND, store)
	s.SetSnapshotStore(ss, pp)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync should not fail due to snapshot insert error: %v", err)
	}
	// Insert was attempted but failed — that's logged, not returned
}

func TestSync_CaptureSnapshotWithMonarchHoldings(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	ss := &mockSnapshotStore{totalSats: 5_000_000}
	pp := &mockPriceProvider{price: 65000.0}
	ms := &mockMonarchSyncer{}

	s := newTestSyncer(mockLND, store)
	s.SetSnapshotStore(ss, pp)
	s.SetMonarchSyncer(ms)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Should have called SyncHolding with the correct BTC quantity
	if len(ms.syncHoldingCalls) != 1 {
		t.Fatalf("expected 1 SyncHolding call, got %d", len(ms.syncHoldingCalls))
	}
	expected := float64(5_000_000) / 1e8
	if ms.syncHoldingCalls[0] != expected {
		t.Errorf("expected BTC quantity %f, got %f", expected, ms.syncHoldingCalls[0])
	}
}

func TestSync_CaptureSnapshotMonarchSyncError(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	ss := &mockSnapshotStore{totalSats: 5_000_000}
	pp := &mockPriceProvider{price: 65000.0}
	ms := &mockMonarchSyncer{syncHoldingErr: errors.New("monarch down")}

	s := newTestSyncer(mockLND, store)
	s.SetSnapshotStore(ss, pp)
	s.SetMonarchSyncer(ms)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync should not fail due to monarch error: %v", err)
	}
	// Snapshot should still be inserted
	if len(ss.inserted) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(ss.inserted))
	}
}

// --- Tests for syncMonarchTransactions ---

func TestSync_MonarchTxSync_NoTypesConfigured(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	ms := &mockMonarchSyncer{}
	txStore := newMockMonarchTxStore()
	settings := &mockSettingsReader{settings: map[string]string{}} // no monarch_sync_types

	s := newTestSyncer(mockLND, store)
	s.SetMonarchSyncer(ms)
	s.SetMonarchTxSync(txStore, settings)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	// No SyncTransactions call should happen
	if ms.syncTxCalls != 0 {
		t.Errorf("expected 0 SyncTransactions calls, got %d", ms.syncTxCalls)
	}
}

func TestSync_MonarchTxSync_SettingsError(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	ms := &mockMonarchSyncer{}
	txStore := newMockMonarchTxStore()
	settings := &mockSettingsReader{err: errors.New("db error")}

	s := newTestSyncer(mockLND, store)
	s.SetMonarchSyncer(ms)
	s.SetMonarchTxSync(txStore, settings)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if ms.syncTxCalls != 0 {
		t.Errorf("expected 0 SyncTransactions calls, got %d", ms.syncTxCalls)
	}
}

func TestSync_MonarchTxSync_ListUnsyncedError(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	ms := &mockMonarchSyncer{}
	txStore := newMockMonarchTxStore()
	txStore.unsyncedErr = errors.New("db error")
	settings := &mockSettingsReader{settings: map[string]string{
		"monarch_sync_types": "buy,sell",
	}}

	s := newTestSyncer(mockLND, store)
	s.SetMonarchSyncer(ms)
	s.SetMonarchTxSync(txStore, settings)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync should not fail due to tx list error: %v", err)
	}
	if ms.syncTxCalls != 0 {
		t.Errorf("expected 0 SyncTransactions calls, got %d", ms.syncTxCalls)
	}
}

func TestSync_MonarchTxSync_NoUnsyncedTxns(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	ms := &mockMonarchSyncer{}
	txStore := newMockMonarchTxStore()
	txStore.unsyncedTxns = []db.UnifiedTransaction{} // empty
	settings := &mockSettingsReader{settings: map[string]string{
		"monarch_sync_types": "buy,sell",
	}}

	s := newTestSyncer(mockLND, store)
	s.SetMonarchSyncer(ms)
	s.SetMonarchTxSync(txStore, settings)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if ms.syncTxCalls != 0 {
		t.Errorf("expected 0 SyncTransactions calls for empty list, got %d", ms.syncTxCalls)
	}
}

func TestSync_MonarchTxSync_Success(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	now := time.Now()
	txStore := newMockMonarchTxStore()
	txStore.unsyncedTxns = []db.UnifiedTransaction{
		{SourceID: "strike:123", Source: "strike", TxType: "buy", Time: now, AmountUSD: 100.50, Memo: "DCA"},
		{SourceID: "strike:456", Source: "strike", TxType: "buy", Time: now, AmountUSD: 200.00, Memo: "Purchase"},
	}

	ms := &mockMonarchSyncer{
		syncTxResult: &monarch.TxSyncResult{Created: 2, Skipped: 0, Errors: 0},
		syncTxSynced: map[string]string{
			"strike:123": "monarch-tx-1",
			"strike:456": "monarch-tx-2",
		},
	}

	settings := &mockSettingsReader{settings: map[string]string{
		"monarch_sync_types": "buy,sell",
	}}

	s := newTestSyncer(mockLND, store)
	s.SetMonarchSyncer(ms)
	s.SetMonarchTxSync(txStore, settings)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if ms.syncTxCalls != 1 {
		t.Fatalf("expected 1 SyncTransactions call, got %d", ms.syncTxCalls)
	}

	// Both transactions should be marked synced
	if len(txStore.markedSynced) != 2 {
		t.Fatalf("expected 2 marked synced, got %d", len(txStore.markedSynced))
	}
	if txStore.markedSynced["strike:123"] != "monarch-tx-1" {
		t.Errorf("expected monarch-tx-1 for strike:123, got %s", txStore.markedSynced["strike:123"])
	}
	if txStore.markedSynced["strike:456"] != "monarch-tx-2" {
		t.Errorf("expected monarch-tx-2 for strike:456, got %s", txStore.markedSynced["strike:456"])
	}
}

func TestSync_MonarchTxSync_WithSources(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	txStore := newMockMonarchTxStore()
	txStore.unsyncedTxns = []db.UnifiedTransaction{
		{SourceID: "river:1", Source: "river", TxType: "buy", Time: time.Now(), AmountUSD: 50.0},
	}

	ms := &mockMonarchSyncer{
		syncTxResult: &monarch.TxSyncResult{Created: 1},
		syncTxSynced: map[string]string{"river:1": "mtx-1"},
	}

	settings := &mockSettingsReader{settings: map[string]string{
		"monarch_sync_types":   "buy",
		"monarch_sync_sources": "river,strike",
	}}

	s := newTestSyncer(mockLND, store)
	s.SetMonarchSyncer(ms)
	s.SetMonarchTxSync(txStore, settings)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if ms.syncTxCalls != 1 {
		t.Errorf("expected 1 SyncTransactions call, got %d", ms.syncTxCalls)
	}
}

func TestSync_MonarchTxSync_SyncError(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	txStore := newMockMonarchTxStore()
	txStore.unsyncedTxns = []db.UnifiedTransaction{
		{SourceID: "s:1", Source: "strike", TxType: "buy", Time: time.Now(), AmountUSD: 10.0},
	}

	ms := &mockMonarchSyncer{syncTxErr: errors.New("monarch API error")}
	settings := &mockSettingsReader{settings: map[string]string{
		"monarch_sync_types": "buy",
	}}

	s := newTestSyncer(mockLND, store)
	s.SetMonarchSyncer(ms)
	s.SetMonarchTxSync(txStore, settings)

	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync should not fail due to monarch tx error: %v", err)
	}
	// Nothing should be marked synced
	if len(txStore.markedSynced) != 0 {
		t.Errorf("expected 0 marked synced on error, got %d", len(txStore.markedSynced))
	}
}

func TestSync_MonarchTxSync_MarkSyncedError(t *testing.T) {
	store := newMockStore()
	mockLND := defaultEmptyLND()

	txStore := newMockMonarchTxStore()
	txStore.unsyncedTxns = []db.UnifiedTransaction{
		{SourceID: "s:1", Source: "strike", TxType: "buy", Time: time.Now(), AmountUSD: 10.0},
	}
	txStore.markSyncedErr = errors.New("db write error")

	ms := &mockMonarchSyncer{
		syncTxResult: &monarch.TxSyncResult{Created: 1},
		syncTxSynced: map[string]string{"s:1": "mtx-1"},
	}
	settings := &mockSettingsReader{settings: map[string]string{
		"monarch_sync_types": "buy",
	}}

	s := newTestSyncer(mockLND, store)
	s.SetMonarchSyncer(ms)
	s.SetMonarchTxSync(txStore, settings)

	// Should not fail — mark error is logged, not returned
	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync should not fail due to mark synced error: %v", err)
	}
}

// --- Tests for insert/upsert and SetSyncState error paths ---

func TestSync_InsertForwardingEventsError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{insertForwardingErr: errors.New("insert failed")}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from InsertForwardingEvents failure")
	}
}

func TestSync_SetSyncStateForwardingError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{setSyncStateErr: map[string]error{"forwarding": errors.New("state error")}}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from SetSyncState forwarding failure")
	}
}

func TestSync_UpsertChannelsError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{upsertChannelsErr: errors.New("upsert failed")}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from UpsertChannels failure")
	}
}

func TestSync_UpsertInvoicesError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{upsertInvoicesErr: errors.New("upsert failed")}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from UpsertInvoices failure")
	}
}

func TestSync_SetSyncStateInvoicesError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{setSyncStateErr: map[string]error{"invoices": errors.New("state error")}}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from SetSyncState invoices failure")
	}
}

func TestSync_UpsertPaymentsError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{upsertPaymentsErr: errors.New("upsert failed")}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from UpsertPayments failure")
	}
}

func TestSync_SetSyncStatePaymentsError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{setSyncStateErr: map[string]error{"payments": errors.New("state error")}}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from SetSyncState payments failure")
	}
}

func TestSync_UpsertOnchainTxnsError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{upsertOnchainErr: errors.New("upsert failed")}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from UpsertOnchainTxns failure")
	}
}

func TestSync_SetSyncStateOnchainError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{setSyncStateErr: map[string]error{"onchain": errors.New("state error")}}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from SetSyncState onchain failure")
	}
}

func TestSync_InsertWalletBalanceSnapshotError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{insertBalanceErr: errors.New("insert failed")}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from InsertWalletBalanceSnapshot failure")
	}
}

func TestSync_GetSyncStateForwardingError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{getSyncStateErr: map[string]error{"forwarding": errors.New("state read error")}}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from GetSyncState forwarding failure")
	}
}

func TestSync_GetSyncStateInvoicesError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{getSyncStateErr: map[string]error{"invoices": errors.New("state read error")}}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from GetSyncState invoices failure")
	}
}

func TestSync_GetSyncStatePaymentsError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{getSyncStateErr: map[string]error{"payments": errors.New("state read error")}}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from GetSyncState payments failure")
	}
}

func TestSync_WithOnchainTransactions(t *testing.T) {
	store := newMockStore()
	now := time.Now()
	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		onchainTxns: []lnd.OnchainTx{
			{
				TxHash:           "abc123",
				Amount:           50000,
				NumConfirmations: 6,
				Timestamp:        now,
				Label:            "channel open",
			},
		},
		walletBalance: defaultMockBalance(),
	}

	s := newTestSyncer(mockLND, store)
	err := s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
}

func TestSync_SetSyncStateWalletError(t *testing.T) {
	store := newMockStore()
	store.txConfig = &mockSyncTx{setSyncStateErr: map[string]error{"wallet": errors.New("state error")}}
	s := newTestSyncer(defaultEmptyLND(), store)
	err := s.Sync(context.Background())
	if err == nil {
		t.Fatal("expected error from SetSyncState wallet failure")
	}
}

// --- Tests for Run error logging paths ---

func TestRun_InitialSyncError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		forwardingHistoryErr: errors.New("lnd down"),
		walletBalance:        defaultMockBalance(),
	}

	logger := log.New(os.Stderr, "[syncer-test] ", 0)
	s := New(mockLND, store, logger, 1*time.Hour, 90)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Give Run time to attempt the initial sync (which will fail)
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Success — Run logged the error and continued to wait
	case <-time.After(2 * time.Second):
		t.Error("Run did not stop within timeout")
	}
}

func TestRun_TickerSyncError(t *testing.T) {
	// Use a failing store that errors on the second call onward
	store := &countingMockStore{
		inner:    newMockStore(),
		failFrom: 2, // first call (initial sync) succeeds, second (ticker) fails
	}

	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	logger := log.New(os.Stderr, "[syncer-test] ", 0)
	s := New(mockLND, store, logger, 50*time.Millisecond, 90)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Wait for initial sync + at least one ticker sync (which will fail)
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Success — Run logged the ticker error and continued
	case <-time.After(2 * time.Second):
		t.Error("Run did not stop within timeout")
	}
}

// countingMockStore wraps mockStore but fails RunSync from the Nth call onward.
type countingMockStore struct {
	inner    *mockStore
	failFrom int
	calls    int
}

func (c *countingMockStore) RunSync(ctx context.Context, fn func(db.SyncTx) error) error {
	c.calls++
	if c.calls >= c.failFrom {
		return errors.New("deliberate sync failure")
	}
	return c.inner.RunSync(ctx, fn)
}

func TestRun_StopsOnCancel(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance:     defaultMockBalance(),
	}

	// Use a very long interval to prevent ticks during the test
	logger := log.New(os.Stderr, "[syncer-test] ", 0)
	s := New(mockLND, store, logger, 1*time.Hour, 90)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Give Run time to do the initial sync
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Run did not stop within timeout after context cancel")
	}
}
