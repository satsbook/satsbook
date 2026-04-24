#!/usr/bin/env bash
# seed-demo-data.sh — Populate satsbook.db with realistic demo data for screenshots.
# Usage: ./scripts/seed-demo-data.sh [path/to/satsbook.db]
#
# WARNING: This inserts fake data. Back up your real DB first if it has real data.

set -euo pipefail

DB="${1:-./satsbook.db}"

# Create the DB file if it doesn't exist
touch "$DB"

echo "Seeding demo data into $DB ..."

# Apply schema migrations first (matches internal/db/db.go)
sqlite3 "$DB" <<'SCHEMA'
-- Migration 0
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
    tx_type     TEXT NOT NULL DEFAULT '',
    imported_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    raw_data    TEXT NOT NULL,
    UNIQUE(source, external_id, tx_type)
);
CREATE TABLE IF NOT EXISTS sync_state (
    source     TEXT PRIMARY KEY,
    last_synced_at DATETIME,
    last_offset INTEGER DEFAULT 0
);
-- Migration 1
CREATE UNIQUE INDEX IF NOT EXISTS idx_forwarding_events_unique
    ON forwarding_events(timestamp, chan_id_in, chan_id_out, amt_out_msat);
CREATE TABLE IF NOT EXISTS wallet_balance_snapshots (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    captured_at     DATETIME NOT NULL,
    total_sat       INTEGER NOT NULL,
    confirmed_sat   INTEGER NOT NULL,
    unconfirmed_sat INTEGER NOT NULL
);
-- Migration 2
CREATE INDEX IF NOT EXISTS idx_forwarding_events_timestamp ON forwarding_events(timestamp);
CREATE INDEX IF NOT EXISTS idx_forwarding_events_chan_id_in ON forwarding_events(chan_id_in);
CREATE INDEX IF NOT EXISTS idx_forwarding_events_chan_id_out ON forwarding_events(chan_id_out);
SCHEMA

echo "Schema ready."

sqlite3 "$DB" <<'SQL'

-- ============================================================
-- 1. Forwarding events (routing fees) — 6 months of activity
--    ~5-15 events per day, realistic fee range 1-500 sats
-- ============================================================

-- Channel IDs (fake but realistic-looking)
-- Channel A: high-volume, low-fee
-- Channel B: medium-volume
-- Channel C: low-volume, higher-fee
-- Channel D: occasional

DELETE FROM forwarding_events;

-- Generate ~180 days of forwarding events using recursive CTE
WITH RECURSIVE dates(d, n) AS (
  SELECT date('now', '-180 days'), 0
  UNION ALL
  SELECT date(d, '+1 day'), n+1 FROM dates WHERE n < 179
)
INSERT INTO forwarding_events (timestamp, chan_id_in, chan_id_out, amt_in_msat, amt_out_msat, fee_msat)
SELECT
  datetime(d, '+' || (abs(random()) % 86400) || ' seconds'),
  CASE abs(random()) % 4
    WHEN 0 THEN '840123456789012481'
    WHEN 1 THEN '840234567890123522'
    WHEN 2 THEN '840345678901234563'
    ELSE        '840456789012345604'
  END,
  CASE abs(random()) % 4
    WHEN 0 THEN '840234567890123522'
    WHEN 1 THEN '840345678901234563'
    WHEN 2 THEN '840456789012345604'
    ELSE        '840123456789012481'
  END,
  -- amt_in_msat: 10k-5M msat (10-5000 sats)
  (abs(random()) % 4990000) + 10000,
  -- amt_out_msat: slightly less (fee is the diff)
  (abs(random()) % 4990000) + 9000,
  -- fee_msat: 100-500000 msat (0.1-500 sats)
  (abs(random()) % 499900) + 100
FROM dates, (
  -- Generate multiple events per day (5-15)
  WITH RECURSIVE reps(r) AS (SELECT 1 UNION ALL SELECT r+1 FROM reps WHERE r < 10)
  SELECT r FROM reps
);

-- ============================================================
-- 2. Channels — 4 active, 1 inactive
-- ============================================================

DELETE FROM channels;

INSERT INTO channels (chan_id, remote_pubkey, local_balance, remote_balance, active, updated_at) VALUES
  ('840123456789012481', '02a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2', 2500000, 1500000, 1, datetime('now')),
  ('840234567890123522', '03b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3', 1800000, 2200000, 1, datetime('now')),
  ('840345678901234563', '02c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4', 3200000, 800000, 1, datetime('now')),
  ('840456789012345604', '03d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5', 900000, 3100000, 1, datetime('now')),
  ('840567890123456645', '02e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6', 0, 0, 0, datetime('now', '-30 days'));

-- ============================================================
-- 3. Strike imports — monthly DCA purchases, ~$100-200/mo
-- ============================================================

DELETE FROM exchange_imports WHERE source = 'strike';
DELETE FROM btc_lots WHERE source = 'strike';

-- 12 months of Strike DCA purchases
INSERT INTO exchange_imports (source, external_id, tx_type, imported_at, raw_data) VALUES
  ('strike', 'demo-strike-001', 'Purchase', datetime('now', '-12 months'), '{"TransactionID":"STK-001","Date":"' || datetime('now', '-12 months') || '","Type":"Purchase","AmountBTC":0.0025,"AmountUSD":150.00,"FeeUSD":0.00,"BTCPrice":60000.00,"Status":"completed"}'),
  ('strike', 'demo-strike-002', 'Purchase', datetime('now', '-11 months'), '{"TransactionID":"STK-002","Date":"' || datetime('now', '-11 months') || '","Type":"Purchase","AmountBTC":0.0022,"AmountUSD":140.00,"FeeUSD":0.00,"BTCPrice":63636.36,"Status":"completed"}'),
  ('strike', 'demo-strike-003', 'Purchase', datetime('now', '-10 months'), '{"TransactionID":"STK-003","Date":"' || datetime('now', '-10 months') || '","Type":"Purchase","AmountBTC":0.0028,"AmountUSD":175.00,"FeeUSD":0.00,"BTCPrice":62500.00,"Status":"completed"}'),
  ('strike', 'demo-strike-004', 'Purchase', datetime('now', '-9 months'), '{"TransactionID":"STK-004","Date":"' || datetime('now', '-9 months') || '","Type":"Purchase","AmountBTC":0.0020,"AmountUSD":130.00,"FeeUSD":0.00,"BTCPrice":65000.00,"Status":"completed"}'),
  ('strike', 'demo-strike-005', 'Purchase', datetime('now', '-8 months'), '{"TransactionID":"STK-005","Date":"' || datetime('now', '-8 months') || '","Type":"Purchase","AmountBTC":0.0024,"AmountUSD":160.00,"FeeUSD":0.00,"BTCPrice":66666.67,"Status":"completed"}'),
  ('strike', 'demo-strike-006', 'Purchase', datetime('now', '-7 months'), '{"TransactionID":"STK-006","Date":"' || datetime('now', '-7 months') || '","Type":"Purchase","AmountBTC":0.0018,"AmountUSD":125.00,"FeeUSD":0.00,"BTCPrice":69444.44,"Status":"completed"}'),
  ('strike', 'demo-strike-007', 'Purchase', datetime('now', '-6 months'), '{"TransactionID":"STK-007","Date":"' || datetime('now', '-6 months') || '","Type":"Purchase","AmountBTC":0.0021,"AmountUSD":150.00,"FeeUSD":0.00,"BTCPrice":71428.57,"Status":"completed"}'),
  ('strike', 'demo-strike-008', 'Purchase', datetime('now', '-5 months'), '{"TransactionID":"STK-008","Date":"' || datetime('now', '-5 months') || '","Type":"Purchase","AmountBTC":0.0019,"AmountUSD":140.00,"FeeUSD":0.00,"BTCPrice":73684.21,"Status":"completed"}'),
  ('strike', 'demo-strike-009', 'Purchase', datetime('now', '-4 months'), '{"TransactionID":"STK-009","Date":"' || datetime('now', '-4 months') || '","Type":"Purchase","AmountBTC":0.0023,"AmountUSD":175.00,"FeeUSD":0.00,"BTCPrice":76086.96,"Status":"completed"}'),
  ('strike', 'demo-strike-010', 'Purchase', datetime('now', '-3 months'), '{"TransactionID":"STK-010","Date":"' || datetime('now', '-3 months') || '","Type":"Purchase","AmountBTC":0.0017,"AmountUSD":135.00,"FeeUSD":0.00,"BTCPrice":79411.76,"Status":"completed"}'),
  ('strike', 'demo-strike-011', 'Purchase', datetime('now', '-2 months'), '{"TransactionID":"STK-011","Date":"' || datetime('now', '-2 months') || '","Type":"Purchase","AmountBTC":0.0015,"AmountUSD":125.00,"FeeUSD":0.00,"BTCPrice":83333.33,"Status":"completed"}'),
  ('strike', 'demo-strike-012', 'Purchase', datetime('now', '-1 months'), '{"TransactionID":"STK-012","Date":"' || datetime('now', '-1 months') || '","Type":"Purchase","AmountBTC":0.0016,"AmountUSD":140.00,"FeeUSD":0.00,"BTCPrice":87500.00,"Status":"completed"}');

-- Matching btc_lots for Strike purchases (amount_sat, price_usd = total cost)
INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id) VALUES
  (datetime('now', '-12 months'), 250000, 150.00, 'strike', 'demo-strike-001'),
  (datetime('now', '-11 months'), 220000, 140.00, 'strike', 'demo-strike-002'),
  (datetime('now', '-10 months'), 280000, 175.00, 'strike', 'demo-strike-003'),
  (datetime('now', '-9 months'),  200000, 130.00, 'strike', 'demo-strike-004'),
  (datetime('now', '-8 months'),  240000, 160.00, 'strike', 'demo-strike-005'),
  (datetime('now', '-7 months'),  180000, 125.00, 'strike', 'demo-strike-006'),
  (datetime('now', '-6 months'),  210000, 150.00, 'strike', 'demo-strike-007'),
  (datetime('now', '-5 months'),  190000, 140.00, 'strike', 'demo-strike-008'),
  (datetime('now', '-4 months'),  230000, 175.00, 'strike', 'demo-strike-009'),
  (datetime('now', '-3 months'),  170000, 135.00, 'strike', 'demo-strike-010'),
  (datetime('now', '-2 months'),  150000, 125.00, 'strike', 'demo-strike-011'),
  (datetime('now', '-1 months'),  160000, 140.00, 'strike', 'demo-strike-012');

-- ============================================================
-- 4. River imports — weekly DCA, smaller amounts
-- ============================================================

DELETE FROM exchange_imports WHERE source = 'river';
DELETE FROM btc_lots WHERE source = 'river';

-- 6 months of River weekly DCA (~$50/week), using days (SQLite doesn't support weeks)
INSERT INTO exchange_imports (source, external_id, tx_type, imported_at, raw_data) VALUES
  ('river', 'demo-river-001', 'buy', datetime('now', '-182 days'), '{"Date":"' || datetime('now', '-182 days') || '","Type":"buy","AmountBTC":0.00075,"AmountUSD":50.00,"BTCPrice":66666.67}'),
  ('river', 'demo-river-002', 'buy', datetime('now', '-175 days'), '{"Date":"' || datetime('now', '-175 days') || '","Type":"buy","AmountBTC":0.00073,"AmountUSD":50.00,"BTCPrice":68493.15}'),
  ('river', 'demo-river-003', 'buy', datetime('now', '-168 days'), '{"Date":"' || datetime('now', '-168 days') || '","Type":"buy","AmountBTC":0.00071,"AmountUSD":50.00,"BTCPrice":70422.54}'),
  ('river', 'demo-river-004', 'buy', datetime('now', '-161 days'), '{"Date":"' || datetime('now', '-161 days') || '","Type":"buy","AmountBTC":0.00069,"AmountUSD":50.00,"BTCPrice":72463.77}'),
  ('river', 'demo-river-005', 'buy', datetime('now', '-154 days'), '{"Date":"' || datetime('now', '-154 days') || '","Type":"buy","AmountBTC":0.00068,"AmountUSD":50.00,"BTCPrice":73529.41}'),
  ('river', 'demo-river-006', 'buy', datetime('now', '-147 days'), '{"Date":"' || datetime('now', '-147 days') || '","Type":"buy","AmountBTC":0.00066,"AmountUSD":50.00,"BTCPrice":75757.58}'),
  ('river', 'demo-river-007', 'buy', datetime('now', '-140 days'), '{"Date":"' || datetime('now', '-140 days') || '","Type":"buy","AmountBTC":0.00065,"AmountUSD":50.00,"BTCPrice":76923.08}'),
  ('river', 'demo-river-008', 'buy', datetime('now', '-133 days'), '{"Date":"' || datetime('now', '-133 days') || '","Type":"buy","AmountBTC":0.00064,"AmountUSD":50.00,"BTCPrice":78125.00}'),
  ('river', 'demo-river-009', 'buy', datetime('now', '-126 days'), '{"Date":"' || datetime('now', '-126 days') || '","Type":"buy","AmountBTC":0.00062,"AmountUSD":50.00,"BTCPrice":80645.16}'),
  ('river', 'demo-river-010', 'buy', datetime('now', '-119 days'), '{"Date":"' || datetime('now', '-119 days') || '","Type":"buy","AmountBTC":0.00061,"AmountUSD":50.00,"BTCPrice":81967.21}'),
  ('river', 'demo-river-011', 'buy', datetime('now', '-112 days'), '{"Date":"' || datetime('now', '-112 days') || '","Type":"buy","AmountBTC":0.00060,"AmountUSD":50.00,"BTCPrice":83333.33}'),
  ('river', 'demo-river-012', 'buy', datetime('now', '-105 days'), '{"Date":"' || datetime('now', '-105 days') || '","Type":"buy","AmountBTC":0.00059,"AmountUSD":50.00,"BTCPrice":84745.76}'),
  ('river', 'demo-river-013', 'buy', datetime('now', '-98 days'),  '{"Date":"' || datetime('now', '-98 days') ||  '","Type":"buy","AmountBTC":0.00058,"AmountUSD":50.00,"BTCPrice":86206.90}'),
  ('river', 'demo-river-014', 'buy', datetime('now', '-91 days'),  '{"Date":"' || datetime('now', '-91 days') ||  '","Type":"buy","AmountBTC":0.00057,"AmountUSD":50.00,"BTCPrice":87719.30}'),
  ('river', 'demo-river-015', 'buy', datetime('now', '-84 days'),  '{"Date":"' || datetime('now', '-84 days') ||  '","Type":"buy","AmountBTC":0.00056,"AmountUSD":50.00,"BTCPrice":89285.71}'),
  ('river', 'demo-river-016', 'buy', datetime('now', '-77 days'),  '{"Date":"' || datetime('now', '-77 days') ||  '","Type":"buy","AmountBTC":0.00055,"AmountUSD":50.00,"BTCPrice":90909.09}'),
  ('river', 'demo-river-017', 'buy', datetime('now', '-70 days'),  '{"Date":"' || datetime('now', '-70 days') ||  '","Type":"buy","AmountBTC":0.00054,"AmountUSD":50.00,"BTCPrice":92592.59}'),
  ('river', 'demo-river-018', 'buy', datetime('now', '-63 days'),  '{"Date":"' || datetime('now', '-63 days') ||  '","Type":"buy","AmountBTC":0.00054,"AmountUSD":50.00,"BTCPrice":92592.59}'),
  ('river', 'demo-river-019', 'buy', datetime('now', '-56 days'),  '{"Date":"' || datetime('now', '-56 days') ||  '","Type":"buy","AmountBTC":0.00053,"AmountUSD":50.00,"BTCPrice":94339.62}'),
  ('river', 'demo-river-020', 'buy', datetime('now', '-49 days'),  '{"Date":"' || datetime('now', '-49 days') ||  '","Type":"buy","AmountBTC":0.00052,"AmountUSD":50.00,"BTCPrice":96153.85}'),
  ('river', 'demo-river-021', 'buy', datetime('now', '-42 days'),  '{"Date":"' || datetime('now', '-42 days') ||  '","Type":"buy","AmountBTC":0.00052,"AmountUSD":50.00,"BTCPrice":96153.85}'),
  ('river', 'demo-river-022', 'buy', datetime('now', '-35 days'),  '{"Date":"' || datetime('now', '-35 days') ||  '","Type":"buy","AmountBTC":0.00051,"AmountUSD":50.00,"BTCPrice":98039.22}'),
  ('river', 'demo-river-023', 'buy', datetime('now', '-28 days'),  '{"Date":"' || datetime('now', '-28 days') ||  '","Type":"buy","AmountBTC":0.00050,"AmountUSD":50.00,"BTCPrice":100000.00}'),
  ('river', 'demo-river-024', 'buy', datetime('now', '-21 days'),  '{"Date":"' || datetime('now', '-21 days') ||  '","Type":"buy","AmountBTC":0.00049,"AmountUSD":50.00,"BTCPrice":102040.82}'),
  ('river', 'demo-river-025', 'buy', datetime('now', '-14 days'),  '{"Date":"' || datetime('now', '-14 days') ||  '","Type":"buy","AmountBTC":0.00048,"AmountUSD":50.00,"BTCPrice":104166.67}'),
  ('river', 'demo-river-026', 'buy', datetime('now', '-7 days'),   '{"Date":"' || datetime('now', '-7 days') ||   '","Type":"buy","AmountBTC":0.00048,"AmountUSD":50.00,"BTCPrice":104166.67}');

-- Matching btc_lots for River
INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id) VALUES
  (datetime('now', '-182 days'), 75000, 50.00, 'river', 'demo-river-001'),
  (datetime('now', '-175 days'), 73000, 50.00, 'river', 'demo-river-002'),
  (datetime('now', '-168 days'), 71000, 50.00, 'river', 'demo-river-003'),
  (datetime('now', '-161 days'), 69000, 50.00, 'river', 'demo-river-004'),
  (datetime('now', '-154 days'), 68000, 50.00, 'river', 'demo-river-005'),
  (datetime('now', '-147 days'), 66000, 50.00, 'river', 'demo-river-006'),
  (datetime('now', '-140 days'), 65000, 50.00, 'river', 'demo-river-007'),
  (datetime('now', '-133 days'), 64000, 50.00, 'river', 'demo-river-008'),
  (datetime('now', '-126 days'), 62000, 50.00, 'river', 'demo-river-009'),
  (datetime('now', '-119 days'), 61000, 50.00, 'river', 'demo-river-010'),
  (datetime('now', '-112 days'), 60000, 50.00, 'river', 'demo-river-011'),
  (datetime('now', '-105 days'), 59000, 50.00, 'river', 'demo-river-012'),
  (datetime('now', '-98 days'),  58000, 50.00, 'river', 'demo-river-013'),
  (datetime('now', '-91 days'),  57000, 50.00, 'river', 'demo-river-014'),
  (datetime('now', '-84 days'),  56000, 50.00, 'river', 'demo-river-015'),
  (datetime('now', '-77 days'),  55000, 50.00, 'river', 'demo-river-016'),
  (datetime('now', '-70 days'),  54000, 50.00, 'river', 'demo-river-017'),
  (datetime('now', '-63 days'),  54000, 50.00, 'river', 'demo-river-018'),
  (datetime('now', '-56 days'),  53000, 50.00, 'river', 'demo-river-019'),
  (datetime('now', '-49 days'),  52000, 50.00, 'river', 'demo-river-020'),
  (datetime('now', '-42 days'),  52000, 50.00, 'river', 'demo-river-021'),
  (datetime('now', '-35 days'),  51000, 50.00, 'river', 'demo-river-022'),
  (datetime('now', '-28 days'),  50000, 50.00, 'river', 'demo-river-023'),
  (datetime('now', '-21 days'),  49000, 50.00, 'river', 'demo-river-024'),
  (datetime('now', '-14 days'),  48000, 50.00, 'river', 'demo-river-025'),
  (datetime('now', '-7 days'),   48000, 50.00, 'river', 'demo-river-026');

-- ============================================================
-- 5. Coinbase imports — older, less frequent buys
-- ============================================================

DELETE FROM exchange_imports WHERE source = 'coinbase';
DELETE FROM btc_lots WHERE source = 'coinbase';

INSERT INTO exchange_imports (source, external_id, tx_type, imported_at, raw_data) VALUES
  ('coinbase', 'demo-cb-001', 'buy', datetime('now', '-18 months'), '{"Timestamp":"' || datetime('now', '-18 months') || '","TransactionType":"buy","Asset":"BTC","QuantityTransacted":0.005,"SpotPriceCurrency":"USD","SpotPriceAtTransaction":45000.00,"Subtotal":225.00,"Total":230.00,"Fees":5.00}'),
  ('coinbase', 'demo-cb-002', 'buy', datetime('now', '-15 months'), '{"Timestamp":"' || datetime('now', '-15 months') || '","TransactionType":"buy","Asset":"BTC","QuantityTransacted":0.004,"SpotPriceCurrency":"USD","SpotPriceAtTransaction":52000.00,"Subtotal":208.00,"Total":213.00,"Fees":5.00}'),
  ('coinbase', 'demo-cb-003', 'buy', datetime('now', '-14 months'), '{"Timestamp":"' || datetime('now', '-14 months') || '","TransactionType":"buy","Asset":"BTC","QuantityTransacted":0.003,"SpotPriceCurrency":"USD","SpotPriceAtTransaction":55000.00,"Subtotal":165.00,"Total":170.00,"Fees":5.00}');

INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id) VALUES
  (datetime('now', '-18 months'), 500000, 230.00, 'coinbase', 'demo-cb-001'),
  (datetime('now', '-15 months'), 400000, 213.00, 'coinbase', 'demo-cb-002'),
  (datetime('now', '-14 months'), 300000, 170.00, 'coinbase', 'demo-cb-003');

-- ============================================================
-- 6. Wallet balance snapshot (current node balance)
-- ============================================================

DELETE FROM wallet_balance_snapshots;

INSERT INTO wallet_balance_snapshots (captured_at, total_sat, confirmed_sat, unconfirmed_sat) VALUES
  (datetime('now'), 850000, 850000, 0);

-- ============================================================
-- 7. Sync state — mark LND as synced so dashboard doesn't
--    show "syncing" state
-- ============================================================

INSERT OR REPLACE INTO sync_state (source, last_synced_at, last_offset) VALUES
  ('lnd:forwarding', datetime('now'), 9999),
  ('lnd:channels',   datetime('now'), 0),
  ('lnd:wallet',     datetime('now'), 0);

-- ============================================================
-- Done! Summary of what was inserted:
-- ============================================================

SELECT 'forwarding_events' AS "table", count(*) AS rows FROM forwarding_events
UNION ALL
SELECT 'channels', count(*) FROM channels
UNION ALL
SELECT 'exchange_imports', count(*) FROM exchange_imports
UNION ALL
SELECT 'btc_lots', count(*) FROM btc_lots
UNION ALL
SELECT 'wallet_balance_snapshots', count(*) FROM wallet_balance_snapshots
UNION ALL
SELECT 'sync_state', count(*) FROM sync_state;

SQL

echo ""
echo "Done! Start satsbook and open http://localhost:3000"
echo ""
echo "To undo: sqlite3 $DB 'DELETE FROM forwarding_events; DELETE FROM channels; DELETE FROM exchange_imports; DELETE FROM btc_lots; DELETE FROM wallet_balance_snapshots; DELETE FROM sync_state;'"
