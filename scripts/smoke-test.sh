#!/usr/bin/env bash
#
# Satsbook Smoke Test — run on a machine with LND + Bitcoin Core
#
# Usage:
#   ./scripts/smoke-test.sh                         # uses .env or environment
#   ./scripts/smoke-test.sh --host 192.168.1.50     # override LND host
#   ./scripts/smoke-test.sh --binary ./satsbook     # use prebuilt binary
#
# Prerequisites:
#   - LND running and reachable
#   - Macaroon + TLS cert accessible at the configured paths
#   - sqlite3 CLI installed (for verification queries)
#   - Go toolchain OR a prebuilt satsbook binary
#
# The script runs 5 checks:
#   1. CONNECT  — binary starts without TLS/macaroon errors
#   2. SYNC     — first sync populates all tables
#   3. INCREMENTAL — second sync adds data without duplicating
#   4. SOAK     — runs for a configurable duration with no errors
#   5. SHUTDOWN — clean exit on SIGTERM

set -euo pipefail

# ─── Colors ───────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}PASS${NC} $1"; }
fail() { echo -e "${RED}FAIL${NC} $1"; FAILURES=$((FAILURES + 1)); }
info() { echo -e "${YELLOW}----${NC} $1"; }

FAILURES=0

# ─── Defaults ─────────────────────────────────────────────────────────
SATSBOOK_BINARY=""
SOAK_DURATION=120  # seconds (default: 2 minutes, use 600 for full 10-min soak)
SYNC_WAIT=30       # seconds to wait for initial sync cycle
DB_PATH=""
CLEANUP_DB=false

# ─── Parse CLI args (override env/defaults) ───────────────────────────
while [[ $# -gt 0 ]]; do
  case $1 in
    --host)          export SATSBOOK_LND_HOST="$2"; shift 2 ;;
    --port)          export SATSBOOK_LND_PORT="$2"; shift 2 ;;
    --macaroon)      export SATSBOOK_LND_MACAROON_PATH="$2"; shift 2 ;;
    --tls-cert)      export SATSBOOK_LND_TLS_CERT_PATH="$2"; shift 2 ;;
    --db)            DB_PATH="$2"; shift 2 ;;
    --binary)        SATSBOOK_BINARY="$2"; shift 2 ;;
    --soak-duration) SOAK_DURATION="$2"; shift 2 ;;
    --sync-wait)     SYNC_WAIT="$2"; shift 2 ;;
    --help|-h)
      echo "Usage: $0 [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --host HOST            LND host (default: from env or localhost)"
      echo "  --port PORT            LND gRPC port (default: from env or 10009)"
      echo "  --macaroon PATH        Path to macaroon file"
      echo "  --tls-cert PATH        Path to TLS cert file"
      echo "  --db PATH              Database path (default: temp file, auto-cleaned)"
      echo "  --binary PATH          Path to prebuilt binary (default: go run)"
      echo "  --soak-duration SECS   Soak test duration (default: 120)"
      echo "  --sync-wait SECS       Seconds to wait per sync cycle (default: 15)"
      echo ""
      echo "Environment variables (from .env or shell):"
      echo "  SATSBOOK_LND_HOST, SATSBOOK_LND_PORT"
      echo "  SATSBOOK_LND_MACAROON_PATH, SATSBOOK_LND_TLS_CERT_PATH"
      exit 0
      ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# ─── Load .env if present ────────────────────────────────────────────
if [[ -f .env ]]; then
  info "Loading .env file"
  set -a
  source .env
  set +a
fi

# ─── Resolve database path ───────────────────────────────────────────
if [[ -z "$DB_PATH" ]]; then
  DB_PATH=$(mktemp /tmp/satsbook-smoke-XXXXXX.db)
  CLEANUP_DB=true
  info "Using temp database: $DB_PATH"
else
  info "Using database: $DB_PATH"
fi
export SATSBOOK_DATABASE_PATH="$DB_PATH"

# Use a short sync interval for testing (minimum allowed: 1m)
export SATSBOOK_SYNC_INTERVAL="1m"
export SATSBOOK_MAX_HISTORY_DAYS="7"
export SATSBOOK_LOG_LEVEL="info"

# ─── Resolve binary ──────────────────────────────────────────────────
if [[ -n "$SATSBOOK_BINARY" ]]; then
  if [[ ! -x "$SATSBOOK_BINARY" ]]; then
    echo "Error: binary not found or not executable: $SATSBOOK_BINARY"
    exit 1
  fi
  RUN_CMD="$SATSBOOK_BINARY"
else
  if command -v go &>/dev/null; then
    info "No --binary specified, building from source"
    go build -o /tmp/satsbook-smoke-bin ./cmd/satsbook
    RUN_CMD="/tmp/satsbook-smoke-bin"
  else
    echo "Error: no --binary specified and go toolchain not found"
    exit 1
  fi
fi

# ─── Preflight checks ────────────────────────────────────────────────
if ! command -v sqlite3 &>/dev/null; then
  echo "Error: sqlite3 is required but not found"
  exit 1
fi

info "LND target: ${SATSBOOK_LND_HOST:-localhost}:${SATSBOOK_LND_PORT:-10009}"

# ─── Helpers ──────────────────────────────────────────────────────────
LOGFILE=$(mktemp /tmp/satsbook-smoke-log-XXXXXX.log)

cleanup() {
  # Kill any lingering satsbook process from this script
  [[ -n "${SATSBOOK_PID:-}" ]] && kill "$SATSBOOK_PID" 2>/dev/null && wait "$SATSBOOK_PID" 2>/dev/null || true
  if [[ "$CLEANUP_DB" == "true" ]]; then
    rm -f "$DB_PATH"
  fi
  rm -f "$LOGFILE" /tmp/satsbook-smoke-bin
}
trap cleanup EXIT

start_satsbook() {
  $RUN_CMD > "$LOGFILE" 2>&1 &
  SATSBOOK_PID=$!
}

stop_satsbook() {
  local signal="${1:-TERM}"
  kill "-$signal" "$SATSBOOK_PID" 2>/dev/null || true
  wait "$SATSBOOK_PID" 2>/dev/null || true
  SATSBOOK_PID=""
}

wait_for_sync() {
  local timeout=$1
  local start=$SECONDS
  while (( SECONDS - start < timeout )); do
    if grep -q "synced.*wallet balance" "$LOGFILE" 2>/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

db_count() {
  sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM $1;" 2>/dev/null
}

# ─── Test 1: CONNECT ─────────────────────────────────────────────────
echo ""
info "Test 1: CONNECT — binary starts and connects to LND"

start_satsbook
sleep 3

if ! kill -0 "$SATSBOOK_PID" 2>/dev/null; then
  fail "Binary exited immediately"
  echo "  Log output:"
  cat "$LOGFILE" | sed 's/^/  /'
  exit 1
fi

if grep -qi "failed\|fatal\|panic" "$LOGFILE" 2>/dev/null; then
  fail "Errors in startup log"
  grep -i "failed\|fatal\|panic" "$LOGFILE" | sed 's/^/  /'
  stop_satsbook
  exit 1
fi

pass "Binary started and connected to LND"
stop_satsbook

# ─── Test 2: FIRST SYNC ──────────────────────────────────────────────
echo ""
info "Test 2: SYNC — first run populates all tables"

# Clean DB for fresh start
rm -f "$DB_PATH"
> "$LOGFILE"

start_satsbook

if ! wait_for_sync "$SYNC_WAIT"; then
  fail "Sync did not complete within ${SYNC_WAIT}s"
  echo "  Log output:"
  cat "$LOGFILE" | sed 's/^/  /'
  stop_satsbook
  exit 1
fi

stop_satsbook

# Check all tables have data
SYNC_STATES=$(db_count "sync_state")
SNAPSHOTS=$(db_count "wallet_balance_snapshots")
CHANNELS=$(db_count "channels")

if [[ "$SYNC_STATES" -ge 4 ]]; then
  pass "sync_state has $SYNC_STATES entries (expected >= 4)"
else
  fail "sync_state has $SYNC_STATES entries (expected >= 4)"
fi

if [[ "$SNAPSHOTS" -ge 1 ]]; then
  pass "wallet_balance_snapshots has $SNAPSHOTS row(s)"
else
  fail "wallet_balance_snapshots is empty"
fi

if [[ "$CHANNELS" -ge 0 ]]; then
  # Channels might be 0 if the node has none — that's ok, just report
  info "channels: $CHANNELS, forwarding_events: $(db_count forwarding_events), invoices: $(db_count invoices), payments: $(db_count payments)"
fi

SNAP_AFTER_FIRST=$SNAPSHOTS

# ─── Test 3: INCREMENTAL ─────────────────────────────────────────────
echo ""
info "Test 3: INCREMENTAL — second run adds data, no duplicates"

> "$LOGFILE"
start_satsbook

if ! wait_for_sync "$SYNC_WAIT"; then
  fail "Second sync did not complete within ${SYNC_WAIT}s"
  stop_satsbook
  exit 1
fi

stop_satsbook

SNAP_AFTER_SECOND=$(db_count "wallet_balance_snapshots")

if [[ "$SNAP_AFTER_SECOND" -gt "$SNAP_AFTER_FIRST" ]]; then
  pass "wallet_balance_snapshots grew from $SNAP_AFTER_FIRST to $SNAP_AFTER_SECOND"
else
  fail "wallet_balance_snapshots did not grow ($SNAP_AFTER_FIRST -> $SNAP_AFTER_SECOND)"
fi

# Sync state timestamps should all be recent (within last 2 minutes)
STALE_STATES=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM sync_state WHERE last_synced_at < datetime('now', '-2 minutes');" 2>/dev/null)
if [[ "$STALE_STATES" -eq 0 ]]; then
  pass "All sync_state timestamps are recent"
else
  fail "$STALE_STATES sync_state entries have stale timestamps"
fi

# ─── Test 4: SOAK ────────────────────────────────────────────────────
echo ""
info "Test 4: SOAK — running for ${SOAK_DURATION}s watching for errors"

> "$LOGFILE"
start_satsbook

# Wait for soak duration, checking process is alive periodically
SOAK_START=$SECONDS
SOAK_OK=true
while (( SECONDS - SOAK_START < SOAK_DURATION )); do
  if ! kill -0 "$SATSBOOK_PID" 2>/dev/null; then
    fail "Process died during soak test"
    echo "  Last log lines:"
    tail -10 "$LOGFILE" | sed 's/^/  /'
    SOAK_OK=false
    break
  fi

  if grep -qi "panic\|fatal" "$LOGFILE" 2>/dev/null; then
    fail "Panic or fatal error during soak"
    grep -i "panic\|fatal" "$LOGFILE" | tail -5 | sed 's/^/  /'
    SOAK_OK=false
    break
  fi

  # Print progress every 30s
  ELAPSED=$((SECONDS - SOAK_START))
  if (( ELAPSED % 30 == 0 && ELAPSED > 0 )); then
    SYNC_COUNT=$(grep -c "synced.*wallet balance" "$LOGFILE" 2>/dev/null || echo 0)
    info "  ${ELAPSED}s elapsed, $SYNC_COUNT sync cycles completed"
  fi

  sleep 5
done

if [[ "$SOAK_OK" == "true" ]]; then
  TOTAL_SYNCS=$(grep -c "synced.*wallet balance" "$LOGFILE" 2>/dev/null || echo 0)
  ERROR_COUNT=$(grep -ci "sync failed\|error" "$LOGFILE" 2>/dev/null || echo 0)

  pass "Soak completed: $TOTAL_SYNCS sync cycles, $ERROR_COUNT errors"

  if [[ "$ERROR_COUNT" -gt 0 ]]; then
    info "  Non-fatal errors found in log:"
    grep -i "sync failed\|error" "$LOGFILE" | head -5 | sed 's/^/    /'
  fi
fi

# ─── Test 5: GRACEFUL SHUTDOWN ────────────────────────────────────────
echo ""
info "Test 5: SHUTDOWN — clean exit on SIGTERM"

if kill -0 "$SATSBOOK_PID" 2>/dev/null; then
  kill -TERM "$SATSBOOK_PID"
  SHUTDOWN_START=$SECONDS

  while kill -0 "$SATSBOOK_PID" 2>/dev/null && (( SECONDS - SHUTDOWN_START < 10 )); do
    sleep 1
  done

  if kill -0 "$SATSBOOK_PID" 2>/dev/null; then
    fail "Process did not exit within 10s of SIGTERM"
    kill -9 "$SATSBOOK_PID" 2>/dev/null || true
  else
    SHUTDOWN_TIME=$((SECONDS - SHUTDOWN_START))
    if grep -q "shutdown complete" "$LOGFILE" 2>/dev/null; then
      pass "Clean shutdown in ${SHUTDOWN_TIME}s"
    else
      fail "Process exited but no 'shutdown complete' log message"
    fi
  fi
  wait "$SATSBOOK_PID" 2>/dev/null || true
  SATSBOOK_PID=""
else
  info "Process already stopped (from soak test failure)"
fi

# ─── Summary ──────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════════"
if [[ "$FAILURES" -eq 0 ]]; then
  echo -e "${GREEN}All tests passed${NC}"
else
  echo -e "${RED}$FAILURES test(s) failed${NC}"
fi
echo "═══════════════════════════════════════════"

# Final data summary
echo ""
info "Final database contents ($DB_PATH):"
sqlite3 "$DB_PATH" "
  SELECT 'forwarding_events' as tbl, COUNT(*) as rows FROM forwarding_events
  UNION ALL SELECT 'channels', COUNT(*) FROM channels
  UNION ALL SELECT 'invoices', COUNT(*) FROM invoices
  UNION ALL SELECT 'payments', COUNT(*) FROM payments
  UNION ALL SELECT 'wallet_snapshots', COUNT(*) FROM wallet_balance_snapshots;
" 2>/dev/null | sed 's/|/: /' | sed 's/^/  /'

exit "$FAILURES"
