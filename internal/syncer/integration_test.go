//go:build integration

package syncer

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/satsbook/satsbook/internal/config"
	"github.com/satsbook/satsbook/internal/db"
	"github.com/satsbook/satsbook/internal/lnd"
)

// TestSync_RealNode tests sync against a real LND node.
// Requires environment variables:
//   SATSBOOK_LND_HOST
//   SATSBOOK_LND_PORT
//   SATSBOOK_LND_MACAROON_PATH
//   SATSBOOK_LND_TLS_CERT_PATH
//
// Run with: go test -tags integration ./internal/syncer/
func TestSync_RealNode(t *testing.T) {
	// Load config from environment
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("skipping integration test: failed to load config: %v", err)
	}

	// Create LND client
	lndClient, err := lnd.NewClient(cfg.LNDHost, cfg.LNDPort, cfg.LNDMacaroonPath, cfg.LNDTLSCertPath)
	if err != nil {
		t.Skipf("skipping integration test: failed to connect to LND: %v", err)
	}
	defer lndClient.Close()

	// Create in-memory test database
	database, err := db.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	defer database.Close()

	// Create syncer
	logger := log.New(os.Stderr, "[integration-test] ", log.LstdFlags)
	syncer := New(lndClient, database, logger, 5*time.Minute, 7) // 7 days of history

	// Run sync
	err = syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Verify that data was synced
	var forwardingCount int
	err = database.db.QueryRow("SELECT COUNT(*) FROM forwarding_events").Scan(&forwardingCount)
	if err != nil {
		t.Fatalf("failed to count forwarding events: %v", err)
	}
	t.Logf("Synced %d forwarding events", forwardingCount)

	var channelCount int
	err = database.db.QueryRow("SELECT COUNT(*) FROM channels").Scan(&channelCount)
	if err != nil {
		t.Fatalf("failed to count channels: %v", err)
	}
	t.Logf("Synced %d channels", channelCount)

	var invoiceCount int
	err = database.db.QueryRow("SELECT COUNT(*) FROM invoices").Scan(&invoiceCount)
	if err != nil {
		t.Fatalf("failed to count invoices: %v", err)
	}
	t.Logf("Synced %d invoices", invoiceCount)

	var paymentCount int
	err = database.db.QueryRow("SELECT COUNT(*) FROM payments").Scan(&paymentCount)
	if err != nil {
		t.Fatalf("failed to count payments: %v", err)
	}
	t.Logf("Synced %d payments", paymentCount)

	var snapshotCount int
	err = database.db.QueryRow("SELECT COUNT(*) FROM wallet_balance_snapshots").Scan(&snapshotCount)
	if err != nil {
		t.Fatalf("failed to count balance snapshots: %v", err)
	}
	t.Logf("Synced %d wallet balance snapshots", snapshotCount)

	// At minimum, we should have a wallet balance snapshot (always synced)
	if snapshotCount < 1 {
		t.Errorf("expected at least 1 wallet balance snapshot, got %d", snapshotCount)
	}
}
