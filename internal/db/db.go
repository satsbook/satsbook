package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite database connection.
type DB struct {
	db *sql.DB
}

// migrations is the list of SQL DDL statements applied in order.
// Each migration is applied atomically in a transaction.
var migrations = []string{
	// Migration 0: Initial schema
	`
	CREATE TABLE IF NOT EXISTS schema_migrations (
		id         INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS forwarding_events (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp   DATETIME NOT NULL,
		chan_id_in  TEXT NOT NULL,
		chan_id_out TEXT NOT NULL,
		amt_in_msat INTEGER NOT NULL,
		amt_out_msat INTEGER NOT NULL,
		fee_msat    INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS channels (
		chan_id       TEXT PRIMARY KEY,
		remote_pubkey TEXT NOT NULL,
		local_balance INTEGER NOT NULL,
		remote_balance INTEGER NOT NULL,
		active        INTEGER NOT NULL,
		updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS invoices (
		payment_hash TEXT PRIMARY KEY,
		amt_paid_msat INTEGER NOT NULL,
		created_at   DATETIME NOT NULL,
		settled_at   DATETIME
	);

	CREATE TABLE IF NOT EXISTS payments (
		payment_hash TEXT PRIMARY KEY,
		value_msat   INTEGER NOT NULL,
		fee_msat     INTEGER NOT NULL,
		created_at   DATETIME NOT NULL,
		status       TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS onchain_txns (
		tx_hash         TEXT PRIMARY KEY,
		amount_sat      INTEGER NOT NULL,
		num_confirmations INTEGER NOT NULL,
		timestamp       DATETIME NOT NULL,
		label           TEXT
	);

	CREATE TABLE IF NOT EXISTS btc_lots (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		acquired_at  DATETIME NOT NULL,
		amount_sat   INTEGER NOT NULL,
		price_usd    REAL NOT NULL,
		source       TEXT NOT NULL,
		external_id  TEXT
	);

	CREATE TABLE IF NOT EXISTS disposals (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		disposed_at DATETIME NOT NULL,
		amount_sat  INTEGER NOT NULL,
		proceeds_usd REAL NOT NULL,
		lot_id      INTEGER NOT NULL
	);

	CREATE TABLE IF NOT EXISTS exchange_imports (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		source      TEXT NOT NULL,
		external_id TEXT NOT NULL,
		imported_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		raw_data    TEXT NOT NULL,
		UNIQUE(source, external_id)
	);

	CREATE TABLE IF NOT EXISTS sync_state (
		source     TEXT PRIMARY KEY,
		last_synced_at DATETIME,
		last_offset INTEGER DEFAULT 0
	);
	`,
	// Migration 1: Add unique index on forwarding_events and wallet_balance_snapshots table
	`
	CREATE UNIQUE INDEX IF NOT EXISTS idx_forwarding_events_unique
		ON forwarding_events(timestamp, chan_id_in, chan_id_out, amt_out_msat);

	CREATE TABLE IF NOT EXISTS wallet_balance_snapshots (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		captured_at     DATETIME NOT NULL,
		total_sat       INTEGER NOT NULL,
		confirmed_sat   INTEGER NOT NULL,
		unconfirmed_sat INTEGER NOT NULL
	);
	`,
	// Migration 2: Indexes for dashboard query performance
	`
	CREATE INDEX IF NOT EXISTS idx_forwarding_events_timestamp
		ON forwarding_events(timestamp);
	CREATE INDEX IF NOT EXISTS idx_forwarding_events_chan_id_in
		ON forwarding_events(chan_id_in);
	CREATE INDEX IF NOT EXISTS idx_forwarding_events_chan_id_out
		ON forwarding_events(chan_id_out);
	`,
	// Migration 3: Watched wallets for cold storage / xpub tracking
	`
	CREATE TABLE IF NOT EXISTS watched_wallets (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		label           TEXT NOT NULL,
		type            TEXT NOT NULL CHECK(type IN ('address', 'xpub')),
		value           TEXT NOT NULL,
		derivation_type TEXT NOT NULL DEFAULT 'bip84',
		balance_sats    INTEGER NOT NULL DEFAULT 0,
		last_checked_at DATETIME,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(type, value)
	);
	`,
	// Migration 4: Fix exchange_imports unique constraint.
	// Strike reference IDs are not unique per row — the same reference can have
	// multiple transaction types (e.g. Sale + Withdrawal for the same operation).
	// Add transaction_type column and re-key on (source, external_id, tx_type).
	`
	CREATE TABLE IF NOT EXISTS exchange_imports_new (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		source      TEXT NOT NULL,
		external_id TEXT NOT NULL,
		tx_type     TEXT NOT NULL DEFAULT '',
		imported_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		raw_data    TEXT NOT NULL,
		UNIQUE(source, external_id, tx_type)
	);

	INSERT OR IGNORE INTO exchange_imports_new (id, source, external_id, tx_type, imported_at, raw_data)
		SELECT id, source, external_id,
			COALESCE(json_extract(raw_data, '$.Type'), ''),
			imported_at, raw_data
		FROM exchange_imports;

	DROP TABLE exchange_imports;
	ALTER TABLE exchange_imports_new RENAME TO exchange_imports;
	`,
}

// NewDB opens a SQLite database at the given path, runs migrations, and returns a DB.
func NewDB(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Verify the connection works
	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Enable WAL mode for concurrent read/write (syncer writes while HTTP reads)
	if _, err := sqldb.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}
	// Set busy timeout so readers wait rather than getting SQLITE_BUSY
	if _, err := sqldb.Exec("PRAGMA busy_timeout=5000"); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}

	db := &DB{db: sqldb}

	// Run migrations
	if err := migrate(sqldb); err != nil {
		sqldb.Close()
		return nil, err
	}

	return db, nil
}

// migrate applies pending migrations in order.
// Each migration is wrapped in a transaction.
func migrate(sqldb *sql.DB) error {
	// Create schema_migrations table if not exists
	createMigrationsTable := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		id         INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := sqldb.Exec(createMigrationsTable); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Check how many migrations have been applied
	var appliedCount int
	err := sqldb.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&appliedCount)
	if err != nil {
		return fmt.Errorf("failed to count applied migrations: %w", err)
	}

	// Apply pending migrations
	for i := appliedCount; i < len(migrations); i++ {
		tx, err := sqldb.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction for migration %d: %w", i, err)
		}

		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to apply migration %d: %w", i, err)
		}

		// Record the migration
		if _, err := tx.Exec("INSERT INTO schema_migrations (id) VALUES (?)", i); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %d: %w", i, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %d: %w", i, err)
		}
	}

	return nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}
