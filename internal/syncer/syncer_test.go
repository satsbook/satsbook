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
}

func (m *mockSyncTx) GetSyncState(source string) (time.Time, int64, error) {
	if entry, ok := m.store.syncState[source]; ok {
		return entry.lastSyncedAt, entry.lastOffset, nil
	}
	return time.Time{}, 0, nil
}

func (m *mockSyncTx) SetSyncState(source string, syncedAt time.Time, offset int64) error {
	m.store.syncState[source] = syncStateEntry{
		lastSyncedAt: syncedAt,
		lastOffset:   offset,
	}
	return nil
}

func (m *mockSyncTx) InsertForwardingEvents(events []db.ForwardingEvent) error {
	m.forwardingEventsInserted = append(m.forwardingEventsInserted, events...)
	return nil
}

func (m *mockSyncTx) UpsertChannels(channels []db.Channel) error {
	m.channelsUpserted = append(m.channelsUpserted, channels...)
	return nil
}

func (m *mockSyncTx) UpsertInvoices(invoices []db.Invoice) error {
	m.invoicesUpserted = append(m.invoicesUpserted, invoices...)
	return nil
}

func (m *mockSyncTx) UpsertPayments(payments []db.Payment) error {
	m.paymentsUpserted = append(m.paymentsUpserted, payments...)
	return nil
}

func (m *mockSyncTx) UpsertOnchainTxns(txns []db.OnchainTx) error {
	m.onchainTxnsUpserted = append(m.onchainTxnsUpserted, txns...)
	return nil
}

func (m *mockSyncTx) InsertWalletBalanceSnapshot(s db.WalletBalanceSnapshot) error {
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
