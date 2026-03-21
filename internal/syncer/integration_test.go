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
//
//	SATSBOOK_LND_HOST
//	SATSBOOK_LND_PORT
//	SATSBOOK_LND_MACAROON_PATH
//	SATSBOOK_LND_TLS_CERT_PATH
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
	s := New(lndClient, database, logger, 5*time.Minute, 7) // 7 days of history

	// Run sync
	err = s.Sync(context.Background())
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// Verify sync state was written for all sources
	for _, source := range []string{"forwarding", "invoices", "payments", "wallet"} {
		syncedAt, _, err := database.GetSyncState(context.Background(), source)
		if err != nil {
			t.Fatalf("failed to get sync state for %s: %v", source, err)
		}
		if syncedAt.IsZero() {
			t.Errorf("expected non-zero sync time for %s", source)
		}
		t.Logf("Sync state for %s: %s", source, syncedAt.Format(time.RFC3339))
	}
}
