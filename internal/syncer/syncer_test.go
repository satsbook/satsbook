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

// mockLNDClient mocks the LNDClient interface.
type mockLNDClient struct {
	channels          []lnd.Channel
	forwardingHistory []lnd.ForwardingEvent
	invoices          []lnd.Invoice
	payments          []lnd.Payment
	walletBalance     *lnd.WalletBalance
	err               error
}

func (m *mockLNDClient) ListChannels(ctx context.Context) ([]lnd.Channel, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.channels, nil
}

func (m *mockLNDClient) ForwardingHistory(ctx context.Context, start, end time.Time) ([]lnd.ForwardingEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.forwardingHistory, nil
}

func (m *mockLNDClient) ListInvoices(ctx context.Context, offset uint64) ([]lnd.Invoice, uint64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.invoices, offset + uint64(len(m.invoices)), nil
}

func (m *mockLNDClient) ListPayments(ctx context.Context, offset uint64) ([]lnd.Payment, uint64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	return m.payments, offset + uint64(len(m.payments)), nil
}

func (m *mockLNDClient) WalletBalance(ctx context.Context) (*lnd.WalletBalance, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.walletBalance, nil
}

// mockStore mocks the Store interface.
type mockStore struct {
	syncState map[string]syncStateEntry
	syncFn    func(db.SyncTx) error
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

func (m *mockStore) GetSyncState(ctx context.Context, source string) (time.Time, int64, error) {
	if entry, ok := m.syncState[source]; ok {
		return entry.lastSyncedAt, entry.lastOffset, nil
	}
	return time.Time{}, 0, nil
}

func (m *mockStore) RunSync(ctx context.Context, fn func(db.SyncTx) error) error {
	tx := &mockSyncTx{
		store:      m,
		syncStates: make(map[string]syncStateEntry),
	}
	return fn(tx)
}

// mockSyncTx mocks the SyncTx interface.
type mockSyncTx struct {
	store                    *mockStore
	forwardingEventsInserted []db.ForwardingEvent
	channelsUpserted         []db.Channel
	invoicesUpserted         []db.Invoice
	paymentsUpserted         []db.Payment
	balanceSnapshots         []db.WalletBalanceSnapshot
	syncStates               map[string]syncStateEntry
}

func newMockSyncTx(store *mockStore) *mockSyncTx {
	return &mockSyncTx{
		store:      store,
		syncStates: make(map[string]syncStateEntry),
	}
}

func (m *mockSyncTx) SetSyncState(source string, syncedAt time.Time, offset int64) error {
	m.syncStates[source] = syncStateEntry{
		lastSyncedAt: syncedAt,
		lastOffset:   offset,
	}
	// Also update the parent store's sync state
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

func (m *mockSyncTx) InsertWalletBalanceSnapshot(s db.WalletBalanceSnapshot) error {
	m.balanceSnapshots = append(m.balanceSnapshots, s)
	return nil
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
		walletBalance: &lnd.WalletBalance{
			TotalBalance:       100000,
			ConfirmedBalance:   90000,
			UnconfirmedBalance: 10000,
		},
	}

	syncer := newTestSyncer(mockLND, store)
	err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// On first run, forwarding events should use maxHistoryDays as start time
	syncedAt, _, err := store.GetSyncState(context.Background(), "forwarding")
	if err != nil {
		t.Fatalf("failed to get sync state: %v", err)
	}

	if syncedAt.IsZero() {
		t.Errorf("expected non-zero sync time for forwarding, got zero")
	}

	// Verify all data was synced (forwarding, invoices, payments, wallet)
	if len(store.syncState) < 4 {
		t.Errorf("expected at least 4 sync state entries, got %d", len(store.syncState))
	}
}

func TestSync_IncrementalForwarding(t *testing.T) {
	// Setup store with a previous sync time
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
		walletBalance: &lnd.WalletBalance{
			TotalBalance:       100000,
			ConfirmedBalance:   90000,
			UnconfirmedBalance: 10000,
		},
	}

	syncer := newTestSyncer(mockLND, store)
	err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Sync state should be updated
	syncedAt, _, err := store.GetSyncState(context.Background(), "forwarding")
	if err != nil {
		t.Fatalf("failed to get sync state: %v", err)
	}

	if syncedAt.Before(previousSyncTime) {
		t.Errorf("expected sync time to be updated, got %v (previous: %v)", syncedAt, previousSyncTime)
	}
}

func TestSync_InvoiceOffset(t *testing.T) {
	// Setup store with a previous invoice offset
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
		walletBalance: &lnd.WalletBalance{
			TotalBalance:       100000,
			ConfirmedBalance:   90000,
			UnconfirmedBalance: 10000,
		},
	}

	syncer := newTestSyncer(mockLND, store)
	err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Verify offset was read and new offset was stored
	_, newOffset, err := store.GetSyncState(context.Background(), "invoices")
	if err != nil {
		t.Fatalf("failed to get sync state: %v", err)
	}

	expectedOffset := 100 + int64(len(invoices))
	if newOffset != expectedOffset {
		t.Errorf("expected invoice offset %d, got %d", expectedOffset, newOffset)
	}
}

func TestSync_LNDError(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		err: errors.New("LND connection failed"),
	}

	syncer := newTestSyncer(mockLND, store)
	err := syncer.Sync(context.Background())
	if err == nil {
		t.Errorf("expected sync to fail, got no error")
	}

	// Verify no sync state was written on error
	_, _, syncErr := store.GetSyncState(context.Background(), "forwarding")
	if syncErr != nil {
		t.Fatalf("failed to check sync state: %v", syncErr)
	}
}

func TestSync_EmptyResults(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance: &lnd.WalletBalance{
			TotalBalance:       100000,
			ConfirmedBalance:   90000,
			UnconfirmedBalance: 10000,
		},
	}

	syncer := newTestSyncer(mockLND, store)
	err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Sync should still complete and write state
	_, _, err = store.GetSyncState(context.Background(), "forwarding")
	if err != nil {
		t.Fatalf("failed to get sync state: %v", err)
	}
}

func TestRun_StopsOnCancel(t *testing.T) {
	store := newMockStore()
	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          []lnd.Payment{},
		walletBalance: &lnd.WalletBalance{
			TotalBalance:       100000,
			ConfirmedBalance:   90000,
			UnconfirmedBalance: 10000,
		},
	}

	// Create a syncer with a very long interval to ensure Run won't tick during the test
	logger := log.New(os.Stderr, "[syncer-test] ", 0)
	syncer := New(mockLND, store, logger, 1*time.Hour, 90)

	ctx, cancel := context.WithCancel(context.Background())

	// Run syncer in a goroutine
	done := make(chan struct{})
	go func() {
		syncer.Run(ctx)
		close(done)
	}()

	// Give Run time to do the initial sync
	time.Sleep(100 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for Run to finish
	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Errorf("Run did not stop within timeout after context cancel")
	}
}

func TestSync_PaymentOffset(t *testing.T) {
	// Setup store with a previous payment offset
	store := newMockStore()
	store.syncState["payments"] = syncStateEntry{
		lastSyncedAt: time.Now(),
		lastOffset:   50,
	}

	payments := []lnd.Payment{
		{
			PaymentHash:   "hash1",
			Value:         100000,
			Fee:           1000,
			CreationDate:  time.Now(),
			Status:        "SUCCEEDED",
		},
	}

	mockLND := &mockLNDClient{
		channels:          []lnd.Channel{},
		forwardingHistory: []lnd.ForwardingEvent{},
		invoices:          []lnd.Invoice{},
		payments:          payments,
		walletBalance: &lnd.WalletBalance{
			TotalBalance:       100000,
			ConfirmedBalance:   90000,
			UnconfirmedBalance: 10000,
		},
	}

	syncer := newTestSyncer(mockLND, store)
	err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Verify new offset was stored
	_, newOffset, err := store.GetSyncState(context.Background(), "payments")
	if err != nil {
		t.Fatalf("failed to get sync state: %v", err)
	}

	expectedOffset := 50 + int64(len(payments))
	if newOffset != expectedOffset {
		t.Errorf("expected payment offset %d, got %d", expectedOffset, newOffset)
	}
}
