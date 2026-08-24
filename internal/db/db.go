package db

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite database connection.
type DB struct {
	db   *sql.DB
	path string // file path used to open the database (empty for :memory:)
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
	// Migration 5: Allow 'descriptor' wallet type for multisig / raw descriptor tracking.
	// Also creates watched_wallets if it doesn't exist (handles upgrades from v1.0.0 where
	// migration 3 was the exchange_imports fix, not the watched_wallets creation).
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

	CREATE TABLE IF NOT EXISTS watched_wallets_new (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		label           TEXT NOT NULL,
		type            TEXT NOT NULL CHECK(type IN ('address', 'xpub', 'descriptor')),
		value           TEXT NOT NULL,
		derivation_type TEXT NOT NULL DEFAULT 'bip84',
		balance_sats    INTEGER NOT NULL DEFAULT 0,
		last_checked_at DATETIME,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(type, value)
	);

	INSERT OR IGNORE INTO watched_wallets_new (id, label, type, value, derivation_type, balance_sats, last_checked_at, created_at)
		SELECT id, label, type, value, derivation_type, balance_sats, last_checked_at, created_at
		FROM watched_wallets;

	DROP TABLE watched_wallets;
	ALTER TABLE watched_wallets_new RENAME TO watched_wallets;
	`,

	// Migration 6: Portfolio snapshots for 30-day value chart.
	`
	CREATE TABLE IF NOT EXISTS portfolio_snapshots (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		captured_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		total_sats    INTEGER NOT NULL DEFAULT 0,
		btc_price_usd REAL NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_captured ON portfolio_snapshots(captured_at);
	`,

	// Migration 7: License cache for phone-home validation with grace period.
	`
	CREATE TABLE IF NOT EXISTS license_cache (
		id            INTEGER PRIMARY KEY CHECK (id = 1),
		license_key   TEXT NOT NULL DEFAULT '',
		tier          TEXT NOT NULL DEFAULT 'free',
		signed_token  TEXT NOT NULL DEFAULT '',
		last_verified DATETIME,
		expires_at    DATETIME,
		updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	INSERT OR IGNORE INTO license_cache (id, tier) VALUES (1, 'free');
	`,

	// Migration 8: Key-value settings table for user-configurable options.
	`
	CREATE TABLE IF NOT EXISTS settings (
		key        TEXT PRIMARY KEY,
		value      TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`,
	// Migration 9: Unified BTC transaction view across all sources.
	// NOTE: The view is defined here and also recreated in migration 10 to add the notes JOIN.
	// Keeping the original here for schema history.
	`
	CREATE VIEW IF NOT EXISTS btc_transactions_v AS
	-- LND forwarding fee income
	SELECT
		'lnd_forward' AS source,
		'forward:' || id AS source_id,
		timestamp AS ts,
		'fee_income' AS tx_type,
		fee_msat / 1000 AS amount_sat,
		0.0 AS amount_usd,
		0 AS fee_sat,
		0.0 AS fee_usd,
		0.0 AS price_usd,
		'routing fee: ' || chan_id_in || ' → ' || chan_id_out AS memo
	FROM forwarding_events

	UNION ALL

	-- LND invoices received (settled only)
	SELECT
		'lnd_invoice' AS source,
		payment_hash AS source_id,
		COALESCE(settled_at, created_at) AS ts,
		'receive' AS tx_type,
		amt_paid_msat / 1000 AS amount_sat,
		0.0 AS amount_usd,
		0 AS fee_sat,
		0.0 AS fee_usd,
		0.0 AS price_usd,
		'' AS memo
	FROM invoices
	WHERE settled_at IS NOT NULL

	UNION ALL

	-- LND payments sent (succeeded only)
	SELECT
		'lnd_payment' AS source,
		payment_hash AS source_id,
		created_at AS ts,
		'send' AS tx_type,
		-(value_msat / 1000) AS amount_sat,
		0.0 AS amount_usd,
		fee_msat / 1000 AS fee_sat,
		0.0 AS fee_usd,
		0.0 AS price_usd,
		'' AS memo
	FROM payments
	WHERE status = 'SUCCEEDED'

	UNION ALL

	-- LND on-chain transactions
	SELECT
		'lnd_onchain' AS source,
		tx_hash AS source_id,
		timestamp AS ts,
		CASE WHEN amount_sat >= 0 THEN 'receive' ELSE 'send' END AS tx_type,
		amount_sat AS amount_sat,
		0.0 AS amount_usd,
		0 AS fee_sat,
		0.0 AS fee_usd,
		0.0 AS price_usd,
		COALESCE(label, '') AS memo
	FROM onchain_txns
	WHERE num_confirmations > 0

	UNION ALL

	-- Exchange imports (Strike, River, Coinbase, Swan)
	SELECT
		source AS source,
		source || ':' || external_id || ':' || tx_type AS source_id,
		COALESCE(json_extract(raw_data, '$.Date'), imported_at) AS ts,
		CASE
			WHEN LOWER(tx_type) IN ('buy', 'purchase') THEN 'buy'
			WHEN LOWER(tx_type) IN ('sale', 'sell') THEN 'sell'
			WHEN LOWER(tx_type) IN ('send', 'withdrawal') THEN 'send'
			WHEN LOWER(tx_type) IN ('receive', 'deposit') THEN 'receive'
			ELSE tx_type
		END AS tx_type,
		CAST(COALESCE(json_extract(raw_data, '$.AmountBTC'), 0) * 100000000 AS INTEGER) AS amount_sat,
		COALESCE(json_extract(raw_data, '$.AmountUSD'), 0.0) AS amount_usd,
		0 AS fee_sat,
		COALESCE(json_extract(raw_data, '$.FeeUSD'), 0.0) AS fee_usd,
		COALESCE(json_extract(raw_data, '$.CostBasisUSD'), 0.0) /
			NULLIF(ABS(COALESCE(json_extract(raw_data, '$.AmountBTC'), 0)), 0) AS price_usd,
		'' AS memo
	FROM exchange_imports;
	`,

	// Migration 10: Transaction notes table for user-editable annotations.
	`
	CREATE TABLE IF NOT EXISTS transaction_notes (
		source_id  TEXT PRIMARY KEY,
		note       TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`,

	// Migration 11: Recreate unified view to surface Description in memo column.
	`
	DROP VIEW IF EXISTS btc_transactions_v;
	CREATE VIEW btc_transactions_v AS
	-- LND forwarding fee income
	SELECT
		'lnd_forward' AS source,
		'forward:' || id AS source_id,
		timestamp AS ts,
		'fee_income' AS tx_type,
		fee_msat / 1000 AS amount_sat,
		0.0 AS amount_usd,
		0 AS fee_sat,
		0.0 AS fee_usd,
		0.0 AS price_usd,
		'routing fee: ' || chan_id_in || ' → ' || chan_id_out AS memo
	FROM forwarding_events

	UNION ALL

	-- LND invoices received (settled only)
	SELECT
		'lnd_invoice' AS source,
		payment_hash AS source_id,
		COALESCE(settled_at, created_at) AS ts,
		'receive' AS tx_type,
		amt_paid_msat / 1000 AS amount_sat,
		0.0 AS amount_usd,
		0 AS fee_sat,
		0.0 AS fee_usd,
		0.0 AS price_usd,
		'' AS memo
	FROM invoices
	WHERE settled_at IS NOT NULL

	UNION ALL

	-- LND payments sent (succeeded only)
	SELECT
		'lnd_payment' AS source,
		payment_hash AS source_id,
		created_at AS ts,
		'send' AS tx_type,
		-(value_msat / 1000) AS amount_sat,
		0.0 AS amount_usd,
		fee_msat / 1000 AS fee_sat,
		0.0 AS fee_usd,
		0.0 AS price_usd,
		'' AS memo
	FROM payments
	WHERE status = 'SUCCEEDED'

	UNION ALL

	-- LND on-chain transactions
	SELECT
		'lnd_onchain' AS source,
		tx_hash AS source_id,
		timestamp AS ts,
		CASE WHEN amount_sat >= 0 THEN 'receive' ELSE 'send' END AS tx_type,
		amount_sat AS amount_sat,
		0.0 AS amount_usd,
		0 AS fee_sat,
		0.0 AS fee_usd,
		0.0 AS price_usd,
		COALESCE(label, '') AS memo
	FROM onchain_txns
	WHERE num_confirmations > 0

	UNION ALL

	-- Exchange imports (Strike, River, Coinbase, Swan)
	SELECT
		source AS source,
		source || ':' || external_id || ':' || tx_type AS source_id,
		COALESCE(json_extract(raw_data, '$.Date'), imported_at) AS ts,
		CASE
			WHEN LOWER(tx_type) IN ('buy', 'purchase') THEN 'buy'
			WHEN LOWER(tx_type) IN ('sale', 'sell') THEN 'sell'
			WHEN LOWER(tx_type) IN ('send', 'withdrawal') THEN 'send'
			WHEN LOWER(tx_type) IN ('receive', 'deposit') THEN 'receive'
			ELSE tx_type
		END AS tx_type,
		CAST(COALESCE(json_extract(raw_data, '$.AmountBTC'), 0) * 100000000 AS INTEGER) AS amount_sat,
		COALESCE(json_extract(raw_data, '$.AmountUSD'), 0.0) AS amount_usd,
		0 AS fee_sat,
		COALESCE(json_extract(raw_data, '$.FeeUSD'), 0.0) AS fee_usd,
		COALESCE(json_extract(raw_data, '$.CostBasisUSD'), 0.0) /
			NULLIF(ABS(COALESCE(json_extract(raw_data, '$.AmountBTC'), 0)), 0) AS price_usd,
		COALESCE(json_extract(raw_data, '$.Description'), '') AS memo
	FROM exchange_imports;
	`,
	// Migration 12: Monarch transaction sync tracking.
	`
	CREATE TABLE IF NOT EXISTS monarch_tx_sync (
		source_id     TEXT PRIMARY KEY,
		monarch_tx_id TEXT NOT NULL,
		synced_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`,
	// Migration 13: Per-source portfolio snapshot details for breakdown charts.
	`
	CREATE TABLE IF NOT EXISTS portfolio_snapshot_details (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		snapshot_id INTEGER NOT NULL REFERENCES portfolio_snapshots(id) ON DELETE CASCADE,
		source      TEXT NOT NULL,
		sats        INTEGER NOT NULL DEFAULT 0,
		UNIQUE(snapshot_id, source)
	);
	CREATE INDEX IF NOT EXISTS idx_psd_snapshot ON portfolio_snapshot_details(snapshot_id);
	CREATE INDEX IF NOT EXISTS idx_psd_source ON portfolio_snapshot_details(source);
	`,
	// Migration 14: Add is_transfer flag to transaction_notes for marking internal transfers.
	`
	ALTER TABLE transaction_notes ADD COLUMN is_transfer INTEGER NOT NULL DEFAULT 0;
	`,
	// Migration 15: Add channel_point and closing_tx_hash to channels for auto-tagging transfers.
	`
	ALTER TABLE channels ADD COLUMN channel_point TEXT NOT NULL DEFAULT '';
	ALTER TABLE channels ADD COLUMN closing_tx_hash TEXT NOT NULL DEFAULT '';
	`,
	// Migration 16: Add capacity to channels for per-channel ROI calculation.
	`
	ALTER TABLE channels ADD COLUMN capacity INTEGER NOT NULL DEFAULT 0;
	`,

	// Migration 17: Alert history table for Telegram notification dedup and in-app history.
	`
	CREATE TABLE IF NOT EXISTS alert_history (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		type        TEXT     NOT NULL,
		external_id TEXT     NOT NULL DEFAULT '',
		message     TEXT     NOT NULL,
		sent_at     DATETIME NOT NULL DEFAULT (datetime('now')),
		acknowledged INTEGER  NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_alert_history_type_ext ON alert_history (type, external_id);
	`,

	// Migration 18: API keys for the Power-tier read API.
	`
	CREATE TABLE IF NOT EXISTS api_keys (
		id          INTEGER  PRIMARY KEY AUTOINCREMENT,
		name        TEXT     NOT NULL,
		key_hash    TEXT     NOT NULL UNIQUE,
		key_prefix  TEXT     NOT NULL,
		created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
		last_used_at DATETIME,
		revoked     INTEGER  NOT NULL DEFAULT 0
	);
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

	db := &DB{db: sqldb, path: path}

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

// Path returns the filesystem path of the database file.
func (d *DB) Path() string {
	return d.path
}

// Backup checkpoints the WAL and copies the database file to destPath.
// destPath must not be the same as the database file path.
func (d *DB) Backup(ctx context.Context, destPath string) error {
	if d.path == "" || d.path == ":memory:" {
		return fmt.Errorf("backup: database has no file path")
	}

	// Checkpoint WAL so all committed data is in the main database file.
	if _, err := d.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("backup: wal_checkpoint: %w", err)
	}

	// Copy the database file.
	src, err := os.Open(d.path)
	if err != nil {
		return fmt.Errorf("backup: open source: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("backup: create dest: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("backup: copy: %w", err)
	}

	return dst.Sync()
}
