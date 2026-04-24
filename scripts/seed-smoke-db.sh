#!/usr/bin/env bash
set -euo pipefail

# Creates a smoke-test.db with realistic demo data for screenshots.
# Usage: ./scripts/seed-smoke-db.sh
#        SATSBOOK_DATABASE_PATH=./smoke-test.db make run

DB="smoke-test.db"
rm -f "$DB"

echo "Creating $DB with schema and seed data..."

sqlite3 "$DB" <<'SQL'
-- Schema (matches migrations 0-5)
CREATE TABLE schema_migrations (
    id         INTEGER PRIMARY KEY,
    applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_migrations (id) VALUES (0),(1),(2),(3),(4),(5);

CREATE TABLE forwarding_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp    DATETIME NOT NULL,
    chan_id_in   TEXT NOT NULL,
    chan_id_out  TEXT NOT NULL,
    amt_in_msat  INTEGER NOT NULL,
    amt_out_msat INTEGER NOT NULL,
    fee_msat     INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_forwarding_events_unique
    ON forwarding_events(timestamp, chan_id_in, chan_id_out, amt_out_msat);
CREATE INDEX idx_forwarding_events_timestamp ON forwarding_events(timestamp);
CREATE INDEX idx_forwarding_events_chan_id_in ON forwarding_events(chan_id_in);
CREATE INDEX idx_forwarding_events_chan_id_out ON forwarding_events(chan_id_out);

CREATE TABLE channels (
    chan_id        TEXT PRIMARY KEY,
    remote_pubkey  TEXT NOT NULL,
    local_balance  INTEGER NOT NULL,
    remote_balance INTEGER NOT NULL,
    active         INTEGER NOT NULL,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE invoices (
    payment_hash  TEXT PRIMARY KEY,
    amt_paid_msat INTEGER NOT NULL,
    created_at    DATETIME NOT NULL,
    settled_at    DATETIME
);

CREATE TABLE payments (
    payment_hash TEXT PRIMARY KEY,
    value_msat   INTEGER NOT NULL,
    fee_msat     INTEGER NOT NULL,
    created_at   DATETIME NOT NULL,
    status       TEXT NOT NULL
);

CREATE TABLE onchain_txns (
    tx_hash           TEXT PRIMARY KEY,
    amount_sat        INTEGER NOT NULL,
    num_confirmations INTEGER NOT NULL,
    timestamp         DATETIME NOT NULL,
    label             TEXT
);

CREATE TABLE btc_lots (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    acquired_at DATETIME NOT NULL,
    amount_sat  INTEGER NOT NULL,
    price_usd   REAL NOT NULL,
    source      TEXT NOT NULL,
    external_id TEXT
);

CREATE TABLE disposals (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    disposed_at  DATETIME NOT NULL,
    amount_sat   INTEGER NOT NULL,
    proceeds_usd REAL NOT NULL,
    lot_id       INTEGER NOT NULL
);

CREATE TABLE exchange_imports (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source      TEXT NOT NULL,
    external_id TEXT NOT NULL,
    tx_type     TEXT NOT NULL DEFAULT '',
    imported_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    raw_data    TEXT NOT NULL,
    UNIQUE(source, external_id, tx_type)
);

CREATE TABLE sync_state (
    source         TEXT PRIMARY KEY,
    last_synced_at DATETIME,
    last_offset    INTEGER DEFAULT 0
);

CREATE TABLE wallet_balance_snapshots (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    captured_at     DATETIME NOT NULL,
    total_sat       INTEGER NOT NULL,
    confirmed_sat   INTEGER NOT NULL,
    unconfirmed_sat INTEGER NOT NULL
);

CREATE TABLE watched_wallets (
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

-- ============================================================
-- SEED DATA
-- ============================================================

-- Sync state (so syncer thinks it's been running)
INSERT INTO sync_state (source, last_synced_at, last_offset) VALUES
    ('forwarding', datetime('now'), 0);

-- Channels (6 active, 1 inactive)
INSERT INTO channels (chan_id, remote_pubkey, local_balance, remote_balance, active) VALUES
    ('854123456789012480', '03abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab', 2500000, 1500000, 1),
    ('854234567890123520', '02fedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321fe', 1800000, 3200000, 1),
    ('854345678901234560', '03112233445566778899aabbccddeeff00112233445566778899aabbccddeeff0011', 4200000,  800000, 1),
    ('854456789012345600', '021122334455667788990011223344556677889900112233445566778899001122aa', 3100000, 1900000, 1),
    ('854567890123456640', '03aabb00112233445566778899aabbccddeeff00112233445566778899aabbccddee',  900000, 4100000, 1),
    ('854678901234567680', '02ccdd00112233445566778899aabbccddeeff00112233445566778899aabbccddee', 1600000, 2400000, 1),
    ('854789012345678720', '03eeff00112233445566778899aabbccddeeff00112233445566778899aabbccddee',  500000,  500000, 0);

-- Forwarding events: 90 days of data with realistic variation
-- We'll generate ~5-15 events per day for the last 90 days
SQL

# Generate forwarding events with a loop (easier than pure SQL)
python3 -c "
import random, datetime

random.seed(42)
now = datetime.datetime.now()
chan_ids = [
    '854123456789012480', '854234567890123520', '854345678901234560',
    '854456789012345600', '854567890123456640', '854678901234567680'
]
rows = []
for day_offset in range(90, -1, -1):
    day = now - datetime.timedelta(days=day_offset)
    # More events on weekdays, fewer on weekends
    n_events = random.randint(3, 18) if day.weekday() < 5 else random.randint(1, 8)
    for i in range(n_events):
        ts = day.replace(
            hour=random.randint(0, 23),
            minute=random.randint(0, 59),
            second=random.randint(0, 59)
        )
        c_in = random.choice(chan_ids)
        c_out = random.choice([c for c in chan_ids if c != c_in])
        amt = random.randint(10000, 5000000)  # 10k-5M sats routed
        fee_rate = random.uniform(0.0001, 0.003)
        fee = max(1000, int(amt * fee_rate))  # at least 1 sat fee (in msat)
        amt_in = amt * 1000 + fee
        amt_out = amt * 1000
        rows.append(f\"('{ts.strftime('%Y-%m-%d %H:%M:%S')}','{c_in}','{c_out}',{amt_in},{amt_out},{fee})\")

print('INSERT INTO forwarding_events (timestamp,chan_id_in,chan_id_out,amt_in_msat,amt_out_msat,fee_msat) VALUES')
# chunk to avoid line length issues
for i in range(0, len(rows), 50):
    chunk = rows[i:i+50]
    sep = ',' if i + 50 < len(rows) else ';'
    print(',\n'.join(chunk) + sep)
" | sqlite3 "$DB"

# Seed exchange imports and btc_lots
sqlite3 "$DB" <<'SQL'
-- Strike purchases (DCA pattern - weekly buys)
INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id) VALUES
    ('2025-01-06 12:00:00', 50000, 48.50, 'strike', 'str-001'),
    ('2025-01-13 12:00:00', 50000, 47.25, 'strike', 'str-002'),
    ('2025-01-20 12:00:00', 50000, 51.00, 'strike', 'str-003'),
    ('2025-01-27 12:00:00', 50000, 49.75, 'strike', 'str-004'),
    ('2025-02-03 12:00:00', 50000, 48.00, 'strike', 'str-005'),
    ('2025-02-10 12:00:00', 50000, 50.50, 'strike', 'str-006'),
    ('2025-02-17 12:00:00', 50000, 52.25, 'strike', 'str-007'),
    ('2025-02-24 12:00:00', 50000, 49.00, 'strike', 'str-008'),
    ('2025-03-03 12:00:00', 50000, 47.50, 'strike', 'str-009'),
    ('2025-03-10 12:00:00', 50000, 53.00, 'strike', 'str-010'),
    ('2025-03-17 12:00:00', 50000, 51.75, 'strike', 'str-011'),
    ('2025-03-24 12:00:00', 50000, 50.25, 'strike', 'str-012'),
    ('2025-03-31 12:00:00', 50000, 48.75, 'strike', 'str-013'),
    ('2025-04-07 12:00:00', 50000, 52.50, 'strike', 'str-014'),
    ('2025-04-14 12:00:00', 50000, 54.00, 'strike', 'str-015'),
    ('2025-04-21 12:00:00', 50000, 53.25, 'strike', 'str-016'),
    ('2026-01-05 12:00:00', 100000, 95.00, 'strike', 'str-017'),
    ('2026-01-12 12:00:00', 100000, 97.50, 'strike', 'str-018'),
    ('2026-01-19 12:00:00', 100000, 93.00, 'strike', 'str-019'),
    ('2026-01-26 12:00:00', 100000, 96.25, 'strike', 'str-020'),
    ('2026-02-02 12:00:00', 100000, 98.00, 'strike', 'str-021'),
    ('2026-02-09 12:00:00', 100000, 94.50, 'strike', 'str-022'),
    ('2026-02-16 12:00:00', 100000, 99.00, 'strike', 'str-023'),
    ('2026-02-23 12:00:00', 100000, 97.25, 'strike', 'str-024'),
    ('2026-03-02 12:00:00', 100000, 95.75, 'strike', 'str-025'),
    ('2026-03-09 12:00:00', 100000, 100.50, 'strike', 'str-026'),
    ('2026-03-16 12:00:00', 100000, 98.75, 'strike', 'str-027'),
    ('2026-03-23 12:00:00', 100000, 96.00, 'strike', 'str-028'),
    ('2026-03-30 12:00:00', 100000, 101.25, 'strike', 'str-029'),
    ('2026-04-06 12:00:00', 100000, 99.50, 'strike', 'str-030'),
    ('2026-04-13 12:00:00', 100000, 103.00, 'strike', 'str-031'),
    ('2026-04-20 12:00:00', 100000, 101.75, 'strike', 'str-032');

-- River purchases (monthly larger buys)
INSERT INTO btc_lots (acquired_at, amount_sat, price_usd, source, external_id) VALUES
    ('2025-06-15 10:00:00', 500000, 485.00, 'river', 'riv-001'),
    ('2025-07-15 10:00:00', 500000, 492.50, 'river', 'riv-002'),
    ('2025-08-15 10:00:00', 500000, 478.00, 'river', 'riv-003'),
    ('2025-09-15 10:00:00', 500000, 510.25, 'river', 'riv-004'),
    ('2025-10-15 10:00:00', 500000, 505.00, 'river', 'riv-005'),
    ('2025-11-15 10:00:00', 500000, 525.75, 'river', 'riv-006'),
    ('2025-12-15 10:00:00', 500000, 540.00, 'river', 'riv-007'),
    ('2026-01-15 10:00:00', 500000, 475.50, 'river', 'riv-008'),
    ('2026-02-15 10:00:00', 500000, 490.00, 'river', 'riv-009'),
    ('2026-03-15 10:00:00', 500000, 502.25, 'river', 'riv-010'),
    ('2026-04-15 10:00:00', 500000, 515.00, 'river', 'riv-011');

-- Exchange import records (so the dashboard shows import counts)
INSERT INTO exchange_imports (source, external_id, tx_type, raw_data) VALUES
    ('strike', 'str-batch-1', 'Purchase', '{"Type":"Purchase","Amount":"50000","Note":"weekly DCA"}'),
    ('river', 'riv-batch-1', 'Purchase', '{"Type":"Purchase","Amount":"500000","Note":"monthly buy"}');

-- Cold storage wallets
INSERT INTO watched_wallets (label, type, value, derivation_type, balance_sats, last_checked_at) VALUES
    ('Coldcard Main', 'xpub', 'zpub6rFR7y4Q2AijBEqTUquhVz398htDFrtymY9tizGWWKF2YpXkSLLNqwKjT1caY1GiYqe5XGx9gzwMGb7RTPTt6W5FRjsAHvPMVp8kp3DF5Wn5', 'bip84', 8500000, datetime('now', '-2 hours')),
    ('Trezor Savings', 'xpub', 'zpub6s2RJ4YBMSykwBeXHgpTwbpcXMpJY3FXNGrXKJ9GxqBMu8JFhJtHcYxevg2pBgXfBfMFv3XUm4DSwjT1fqDEjMCpH5Fw9K6R5dKJBPignSd1', 'bip84', 15200000, datetime('now', '-1 hours')),
    ('Gringotts', 'descriptor', 'wsh(sortedmulti(2,[82db5bed/45h/0/0/1]xpub6EbP4DbNt2Mazd32RuNfGj629QX5ADMDvNDDaYZJvq3StthyV4YTdua9oopRUHafiwZLabbVYKX2WkyVj9zCSuwxTkTd2hwDFQAMoykeDuf/0/*,[5af676d4/45h/0/0/5]xpub6Ecac3iV39fsRtXcmVnKo5phVBdkiGfSjUN2z27yXTiNUqx4LVgKSuJRNifq2MMPeUFhAiTtkAHnNbzt6Gc41C6oxv9hzwzsQADkbsKSiRp/0/*,[243f7c8c/0/0/0/0]xpub6EDykLBC5EqA9HYTFeGRfMoWP5VZhJrfgtbztNnvpR5TSBejH7nJEvnqvNNbrAWgpT6mhcSNzsELEHbBKYgWN6XmgUep6HGTt6Bkd3My3Xv/0/*))', '', 42100000, datetime('now', '-30 minutes')),
    ('Iron Bank', 'descriptor', 'sh(sortedmulti(2,[3b68c791/0/0/0/0]xpub6EDykLBC5EkwC3Tv2KSVxeqBrdZ1E2ug3FqtePKeRcN8qLe27KUxxttiAne1gjk4RKTzvuffwUz2AMUmywXj3UUjr5noy9yaQaz4wzdKhMT/0/*,[42ee7919/45h/0/0/0]xpub6DwUpwQwCdVvBehJqJMPqVqTFqVYUqZmaZM3hLWb2c7rVx6nJsFkb7VThajhHKjLK4jv2Axux6HD4ThVtryZddpnRPFYtQVWTJ9JfQTFicj/0/*,[82db5bed/45h/0/0/0]xpub6EbP4DbNt2MaxMvmakPziooDJAdc23eE2J3vcQukSa8X89fYJQBJdW4E94oqqpApQE82CGmE3aCGhrkLy7kVPqhuBUbuW71WgwSav3ibS1D/0/*))', '', 21300000, datetime('now', '-30 minutes'));

SQL

# Summary
EVENTS=$(sqlite3 "$DB" "SELECT COUNT(*) FROM forwarding_events;")
FEES=$(sqlite3 "$DB" "SELECT SUM(fee_msat)/1000 FROM forwarding_events;")
LOTS=$(sqlite3 "$DB" "SELECT COUNT(*) FROM btc_lots;")
WALLETS=$(sqlite3 "$DB" "SELECT COUNT(*) FROM watched_wallets;")
COLD=$(sqlite3 "$DB" "SELECT SUM(balance_sats) FROM watched_wallets;")

echo ""
echo "Seeded $DB:"
echo "  Forwarding events: $EVENTS"
echo "  Total fees earned: ${FEES} sats"
echo "  Exchange lots:     $LOTS"
echo "  Cold wallets:      $WALLETS (${COLD} sats)"
echo ""
echo "Run with:"
echo "  SATSBOOK_DATABASE_PATH=./smoke-test.db make run"
