package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestNewDB_CreatesDatabase verifies that NewDB creates a database successfully
// and that the connection is valid.
func TestNewDB_CreatesDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}

	// Should be able to close without error
	if err := db.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// TestNewDB_InvalidPath verifies that NewDB returns an error for invalid paths.
func TestNewDB_InvalidPath(t *testing.T) {
	// Try to create a database in a non-existent directory
	dbPath := filepath.Join(t.TempDir(), "nonexistent", "subdir", "test.db")

	_, err := NewDB(dbPath)
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

// TestMigrations_TablesExist verifies that all expected tables are created.
func TestMigrations_TablesExist(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	defer db.Close()

	expectedTables := []string{
		"schema_migrations",
		"forwarding_events",
		"channels",
		"invoices",
		"payments",
		"onchain_txns",
		"btc_lots",
		"disposals",
		"exchange_imports",
		"sync_state",
		"wallet_balance_snapshots",
	}

	for _, tableName := range expectedTables {
		var name string
		err := db.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			tableName,
		).Scan(&name)

		if err == sql.ErrNoRows {
			t.Errorf("table %q was not created", tableName)
		} else if err != nil {
			t.Errorf("failed to query table %q: %v", tableName, err)
		}
	}
}

// TestMigrations_IdempotentOnReopen verifies that opening the database twice
// does not fail or re-run migrations.
func TestMigrations_IdempotentOnReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// First open
	db1, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("first NewDB failed: %v", err)
	}
	db1.Close()

	// Second open
	db2, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("second NewDB failed: %v", err)
	}
	defer db2.Close()
}

// TestClose_NilDB verifies that closing a DB with nil inner db does not panic.
func TestClose_NilDB(t *testing.T) {
	d := &DB{db: nil}
	if err := d.Close(); err != nil {
		t.Errorf("Close on nil db should return nil, got %v", err)
	}
}

// TestMigrations_UpgradeFromV100 simulates upgrading from v1.0.0 which had 4 migrations
// (0-3) but no watched_wallets table. Migration 3 in v1.0.0 was the exchange_imports fix,
// which is now migration 4. The current migration 5 must handle missing watched_wallets.
func TestMigrations_UpgradeFromV100(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Simulate v1.0.0 database: apply only the v1.0.0 migrations manually
	sqldb, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// v1.0.0 migration 0: initial schema (same as current)
	if _, err := sqldb.Exec(migrations[0]); err != nil {
		t.Fatalf("migration 0: %v", err)
	}
	// v1.0.0 migration 1: forwarding events index + wallet balance (same as current)
	if _, err := sqldb.Exec(migrations[1]); err != nil {
		t.Fatalf("migration 1: %v", err)
	}
	// v1.0.0 migration 2: dashboard indexes (same as current)
	if _, err := sqldb.Exec(migrations[2]); err != nil {
		t.Fatalf("migration 2: %v", err)
	}
	// v1.0.0 migration 3 was the exchange_imports fix (current migration 4)
	if _, err := sqldb.Exec(migrations[4]); err != nil {
		t.Fatalf("v1.0.0 migration 3 (exchange_imports fix): %v", err)
	}

	// Record 4 migrations in schema_migrations (matching v1.0.0 state)
	for i := 0; i < 4; i++ {
		if _, err := sqldb.Exec("INSERT INTO schema_migrations (id) VALUES (?)", i); err != nil {
			t.Fatalf("record migration %d: %v", i, err)
		}
	}
	sqldb.Close()

	// Now open with current code — should apply migrations 4-6 without error
	db, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("NewDB upgrade from v1.0.0 failed: %v", err)
	}
	defer db.Close()

	// Verify watched_wallets exists with descriptor type support
	var name string
	err = db.db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='watched_wallets'").Scan(&name)
	if err != nil {
		t.Errorf("watched_wallets table should exist after upgrade: %v", err)
	}
}

// TestMigrations_NeverRerun verifies that migrations are only applied once.
// After opening the database twice, schema_migrations should have the same number of rows.
func TestMigrations_NeverRerun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// First open
	db1, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("first NewDB failed: %v", err)
	}

	var countAfterFirst int
	err = db1.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&countAfterFirst)
	if err != nil {
		t.Fatalf("failed to count schema_migrations rows: %v", err)
	}

	db1.Close()

	// Second open
	db2, err := NewDB(dbPath)
	if err != nil {
		t.Fatalf("second NewDB failed: %v", err)
	}
	defer db2.Close()

	// Count rows in schema_migrations
	var countAfterSecond int
	err = db2.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&countAfterSecond)
	if err != nil {
		t.Fatalf("failed to count schema_migrations rows: %v", err)
	}

	if countAfterSecond != countAfterFirst {
		t.Errorf("expected %d rows in schema_migrations after reopen, got %d", countAfterFirst, countAfterSecond)
	}
}
