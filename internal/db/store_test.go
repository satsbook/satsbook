package db

import (
	"context"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	return d
}

func TestGetSyncState_NotFound(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	syncedAt, offset, err := d.GetSyncState(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !syncedAt.IsZero() {
		t.Errorf("expected zero time, got %v", syncedAt)
	}

	if offset != 0 {
		t.Errorf("expected offset 0, got %d", offset)
	}
}

func TestSetAndGetSyncState(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.SetSyncState("forwarding", now, 42)
	})
	if err != nil {
		t.Fatalf("failed to set sync state: %v", err)
	}

	syncedAt, offset, err := d.GetSyncState(context.Background(), "forwarding")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if syncedAt.Unix() != now.Unix() {
		t.Errorf("expected synced time %v, got %v", now, syncedAt)
	}

	if offset != 42 {
		t.Errorf("expected offset 42, got %d", offset)
	}
}

func TestGetSyncState_WithinTransaction(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	// Set initial state
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.SetSyncState("test", now, 10)
	})
	if err != nil {
		t.Fatalf("failed to set initial sync state: %v", err)
	}

	// Read within a new transaction
	err = d.RunSync(context.Background(), func(tx SyncTx) error {
		syncedAt, offset, err := tx.GetSyncState("test")
		if err != nil {
			return err
		}
		if syncedAt.Unix() != now.Unix() {
			t.Errorf("expected synced time %v, got %v", now, syncedAt)
		}
		if offset != 10 {
			t.Errorf("expected offset 10, got %d", offset)
		}

		// Read non-existent source
		zeroTime, zeroOffset, err := tx.GetSyncState("nonexistent")
		if err != nil {
			return err
		}
		if !zeroTime.IsZero() {
			t.Errorf("expected zero time for nonexistent, got %v", zeroTime)
		}
		if zeroOffset != 0 {
			t.Errorf("expected offset 0 for nonexistent, got %d", zeroOffset)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction failed: %v", err)
	}
}

func TestInsertForwardingEvents(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	events := []ForwardingEvent{
		{
			Timestamp:  now,
			ChanIDIn:   12345,
			ChanIDOut:  67890,
			AmtInMsat:  100000,
			AmtOutMsat: 99000,
			FeeMsat:    1000,
		},
		{
			// Duplicate — same unique key, should be ignored
			Timestamp:  now,
			ChanIDIn:   12345,
			ChanIDOut:  67890,
			AmtInMsat:  100000,
			AmtOutMsat: 99000,
			FeeMsat:    1000,
		},
	}

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.InsertForwardingEvents(1, events)
	})
	if err != nil {
		t.Fatalf("failed to insert events: %v", err)
	}

	var count int
	err = d.db.QueryRow("SELECT COUNT(*) FROM forwarding_events").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count events: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 event (duplicate ignored), got %d", count)
	}
}

func TestInsertForwardingEvents_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.InsertForwardingEvents(1, nil)
	})
	if err != nil {
		t.Fatalf("inserting empty events should not error: %v", err)
	}
}

func TestUpsertChannels(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	channels := []Channel{
		{
			ChanID:        123456,
			RemotePubKey:  "node1",
			LocalBalance:  50000,
			RemoteBalance: 50000,
			Active:        true,
		},
	}

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertChannels(1, channels)
	})
	if err != nil {
		t.Fatalf("failed to upsert channels: %v", err)
	}

	// Update the same channel with new balances
	updatedChannels := []Channel{
		{
			ChanID:        123456,
			RemotePubKey:  "node1",
			LocalBalance:  60000,
			RemoteBalance: 40000,
			Active:        true,
		},
	}

	err = d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertChannels(1, updatedChannels)
	})
	if err != nil {
		t.Fatalf("failed to update channels: %v", err)
	}

	var localBalance int64
	err = d.db.QueryRow("SELECT local_balance FROM channels WHERE chan_id = ?", 123456).Scan(&localBalance)
	if err != nil {
		t.Fatalf("failed to query channel: %v", err)
	}

	if localBalance != 60000 {
		t.Errorf("expected local balance 60000, got %d", localBalance)
	}
}

func TestUpsertChannels_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertChannels(1, nil)
	})
	if err != nil {
		t.Fatalf("upserting empty channels should not error: %v", err)
	}
}

func TestUpsertInvoices(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	invoices := []Invoice{
		{
			PaymentHash: "hash1",
			AmtPaidMsat: 100000,
			CreatedAt:   now,
			SettledAt:   nil,
		},
	}

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertInvoices(1, invoices)
	})
	if err != nil {
		t.Fatalf("failed to upsert invoices: %v", err)
	}

	// Upsert with settlement
	settledAt := now.Add(time.Hour)
	updatedInvoices := []Invoice{
		{
			PaymentHash: "hash1",
			AmtPaidMsat: 100000,
			CreatedAt:   now,
			SettledAt:   &settledAt,
		},
	}

	err = d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertInvoices(1, updatedInvoices)
	})
	if err != nil {
		t.Fatalf("failed to update invoice: %v", err)
	}

	var amtPaid int64
	err = d.db.QueryRow("SELECT amt_paid_msat FROM invoices WHERE payment_hash = ?", "hash1").Scan(&amtPaid)
	if err != nil {
		t.Fatalf("failed to query invoice: %v", err)
	}

	if amtPaid != 100000 {
		t.Errorf("expected amount 100000, got %d", amtPaid)
	}
}

func TestUpsertInvoices_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertInvoices(1, nil)
	})
	if err != nil {
		t.Fatalf("upserting empty invoices should not error: %v", err)
	}
}

func TestUpsertPayments(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	payments := []Payment{
		{
			PaymentHash: "hash1",
			Status:      "SUCCEEDED",
			ValueMsat:   100000,
			FeeMsat:     1000,
			CreatedAt:   now,
		},
	}

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertPayments(1, payments)
	})
	if err != nil {
		t.Fatalf("failed to upsert payments: %v", err)
	}

	// Verify upsert updates status
	updatedPayments := []Payment{
		{
			PaymentHash: "hash1",
			Status:      "FAILED",
			ValueMsat:   100000,
			FeeMsat:     1000,
			CreatedAt:   now,
		},
	}

	err = d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertPayments(1, updatedPayments)
	})
	if err != nil {
		t.Fatalf("failed to update payment: %v", err)
	}

	var status string
	err = d.db.QueryRow("SELECT status FROM payments WHERE payment_hash = ?", "hash1").Scan(&status)
	if err != nil {
		t.Fatalf("failed to query payment: %v", err)
	}

	if status != "FAILED" {
		t.Errorf("expected status FAILED, got %s", status)
	}
}

func TestUpsertPayments_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertPayments(1, nil)
	})
	if err != nil {
		t.Fatalf("upserting empty payments should not error: %v", err)
	}
}

func TestUpsertOnchainTxns(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	txns := []OnchainTx{
		{
			TxHash:           "abc123",
			AmountSat:        50000,
			NumConfirmations: 3,
			Timestamp:        now,
			Label:            "channel open",
		},
	}

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertOnchainTxns(1, txns)
	})
	if err != nil {
		t.Fatalf("failed to upsert onchain txns: %v", err)
	}

	// Upsert with updated confirmations
	updatedTxns := []OnchainTx{
		{
			TxHash:           "abc123",
			AmountSat:        50000,
			NumConfirmations: 6,
			Timestamp:        now,
			Label:            "channel open",
		},
	}

	err = d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertOnchainTxns(1, updatedTxns)
	})
	if err != nil {
		t.Fatalf("failed to update onchain txn: %v", err)
	}

	var confs int32
	err = d.db.QueryRow("SELECT num_confirmations FROM onchain_txns WHERE tx_hash = ?", "abc123").Scan(&confs)
	if err != nil {
		t.Fatalf("failed to query onchain txn: %v", err)
	}

	if confs != 6 {
		t.Errorf("expected 6 confirmations, got %d", confs)
	}

	// Verify only 1 row (upsert, not duplicate)
	var count int
	err = d.db.QueryRow("SELECT COUNT(*) FROM onchain_txns").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count onchain txns: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 onchain txn, got %d", count)
	}
}

func TestUpsertOnchainTxns_Empty(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertOnchainTxns(1, nil)
	})
	if err != nil {
		t.Fatalf("upserting empty onchain txns should not error: %v", err)
	}
}

func TestInsertWalletBalanceSnapshot(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()
	snapshot := WalletBalanceSnapshot{
		CapturedAt:     now,
		TotalSat:       100000,
		ConfirmedSat:   90000,
		UnconfirmedSat: 10000,
	}

	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.InsertWalletBalanceSnapshot(1, snapshot)
	})
	if err != nil {
		t.Fatalf("failed to insert snapshot: %v", err)
	}

	var totalSat int64
	err = d.db.QueryRow("SELECT total_sat FROM wallet_balance_snapshots WHERE total_sat = ?", 100000).Scan(&totalSat)
	if err != nil {
		t.Fatalf("failed to query snapshot: %v", err)
	}

	if totalSat != 100000 {
		t.Errorf("expected total 100000, got %d", totalSat)
	}
}

func TestRunSync_RollsBackOnError(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()

	now := time.Now().UTC()

	// Insert a channel successfully
	err := d.RunSync(context.Background(), func(tx SyncTx) error {
		return tx.UpsertChannels(1, []Channel{
			{
				ChanID:        123456,
				RemotePubKey:  "node1",
				LocalBalance:  50000,
				RemoteBalance: 50000,
				Active:        true,
			},
		})
	})
	if err != nil {
		t.Fatalf("failed to insert channel: %v", err)
	}

	// Attempt a transaction that will fail
	testErr := &TestError{"test error"}
	err = d.RunSync(context.Background(), func(tx SyncTx) error {
		// This succeeds within the tx
		_ = tx.SetSyncState("test", now, 0)
		// But we return an error to trigger rollback
		return testErr
	})

	if err != testErr {
		t.Errorf("expected testErr, got %v", err)
	}

	// Verify that the sync_state was NOT written (rollback happened)
	syncedAt, _, err := d.GetSyncState(context.Background(), "test")
	if err != nil {
		t.Fatalf("failed to get sync state: %v", err)
	}

	if !syncedAt.IsZero() {
		t.Errorf("expected zero time (no sync state), but got %v", syncedAt)
	}

	// Verify the pre-existing channel still exists
	var count int
	err = d.db.QueryRow("SELECT COUNT(*) FROM channels").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count channels: %v", err)
	}

	if count != 1 {
		t.Errorf("expected 1 channel, got %d", count)
	}
}

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("expected boolToInt(true) == 1")
	}
	if boolToInt(false) != 0 {
		t.Error("expected boolToInt(false) == 0")
	}
}

// TestError is a test error for rollback testing.
type TestError struct {
	msg string
}

func (e *TestError) Error() string {
	return e.msg
}

// ── Multi-node tests ─────────────────────────────────────────────────────────

func TestEnsurePrimaryNode(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	err := d.EnsurePrimaryNode(ctx, "localhost", 10009, "/mac", "/tls")
	if err != nil {
		t.Fatalf("EnsurePrimaryNode: %v", err)
	}

	nodes, err := d.ListNodes(ctx)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	n := nodes[0]
	if n.ID != 1 {
		t.Errorf("expected node id=1, got %d", n.ID)
	}
	if !n.IsPrimary {
		t.Error("expected primary=true")
	}
	if n.LNDHost != "localhost" || n.LNDPort != 10009 {
		t.Errorf("unexpected host/port: %s:%d", n.LNDHost, n.LNDPort)
	}
}

func TestEnsurePrimaryNode_Idempotent(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	_ = d.EnsurePrimaryNode(ctx, "host1", 10009, "/mac1", "/tls1")
	// Second call should update, not duplicate
	_ = d.EnsurePrimaryNode(ctx, "host2", 10010, "/mac2", "/tls2")

	nodes, _ := d.ListNodes(ctx)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node after two EnsurePrimaryNode calls, got %d", len(nodes))
	}
	if nodes[0].LNDHost != "host2" || nodes[0].LNDPort != 10010 {
		t.Errorf("expected updated host/port after second call, got %s:%d", nodes[0].LNDHost, nodes[0].LNDPort)
	}
}

func TestAddNode(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	_ = d.EnsurePrimaryNode(ctx, "primary", 10009, "/m1", "/t1")

	id, err := d.AddNode(ctx, "secondary", 10009, "/m2", "/t2")
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if id <= 1 {
		t.Errorf("expected id>1, got %d", id)
	}

	nodes, _ := d.ListNodes(ctx)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestAddNode_MaxThree(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	_ = d.EnsurePrimaryNode(ctx, "primary", 10009, "/m1", "/t1")
	_, _ = d.AddNode(ctx, "secondary2", 10009, "/m2", "/t2")
	_, _ = d.AddNode(ctx, "secondary3", 10009, "/m3", "/t3")

	// Fourth add should fail — already at 3 nodes
	_, err := d.AddNode(ctx, "secondary4", 10009, "/m4", "/t4")
	if err == nil {
		t.Error("expected error when adding 4th node, got nil")
	}
}

func TestRemoveNode(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	_ = d.EnsurePrimaryNode(ctx, "primary", 10009, "/m1", "/t1")
	id, _ := d.AddNode(ctx, "secondary", 10009, "/m2", "/t2")

	// Seed some data for the secondary node
	_ = d.RunSync(ctx, func(tx SyncTx) error {
		return tx.InsertForwardingEvents(id, []ForwardingEvent{
			{Timestamp: time.Now(), ChanIDIn: 1, ChanIDOut: 2, AmtInMsat: 1000, AmtOutMsat: 900, FeeMsat: 100},
		})
	})

	if err := d.RemoveNode(ctx, id); err != nil {
		t.Fatalf("RemoveNode: %v", err)
	}

	nodes, _ := d.ListNodes(ctx)
	if len(nodes) != 1 {
		t.Errorf("expected 1 node after remove, got %d", len(nodes))
	}

	// Verify data was deleted
	var count int
	_ = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM forwarding_events WHERE node_id=?", id).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 forwarding events for removed node, got %d", count)
	}
}

func TestRemoveNode_PrimaryFails(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	_ = d.EnsurePrimaryNode(ctx, "primary", 10009, "/m1", "/t1")

	if err := d.RemoveNode(ctx, 1); err == nil {
		t.Error("expected error removing primary node, got nil")
	}
}

func TestNodeIDScopingInSyncTx(t *testing.T) {
	d := newTestDB(t)
	defer d.Close()
	ctx := context.Background()

	// Insert events for node 1 and node 2
	_ = d.RunSync(ctx, func(tx SyncTx) error {
		if err := tx.InsertForwardingEvents(1, []ForwardingEvent{
			{Timestamp: time.Now(), ChanIDIn: 10, ChanIDOut: 11, AmtInMsat: 5000, AmtOutMsat: 4500, FeeMsat: 500},
		}); err != nil {
			return err
		}
		return tx.InsertForwardingEvents(2, []ForwardingEvent{
			{Timestamp: time.Now(), ChanIDIn: 20, ChanIDOut: 21, AmtInMsat: 9000, AmtOutMsat: 8000, FeeMsat: 1000},
		})
	})

	var count1, count2 int
	_ = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM forwarding_events WHERE node_id=1").Scan(&count1)
	_ = d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM forwarding_events WHERE node_id=2").Scan(&count2)
	if count1 != 1 {
		t.Errorf("expected 1 event for node 1, got %d", count1)
	}
	if count2 != 1 {
		t.Errorf("expected 1 event for node 2, got %d", count2)
	}
}
