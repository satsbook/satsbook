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
