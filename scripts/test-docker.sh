#!/usr/bin/env bash
# test-docker.sh — Build and run Satsbook in a Docker container locally,
# simulating the Umbrel community store environment.
#
# Usage:
#   ./scripts/test-docker.sh          # build + run
#   ./scripts/test-docker.sh --no-build  # run existing image
#
# The app will be available at http://localhost:3000
# Press Ctrl+C to stop.

set -euo pipefail

IMAGE="ghcr.io/satsbook/satsbook:local-test"
CONTAINER="satsbook-test"
DATA_DIR="$(mktemp -d)"
PORT=3000

BUILD=true
if [[ "${1:-}" == "--no-build" ]]; then
  BUILD=false
fi

cleanup() {
  echo ""
  echo "Stopping container..."
  docker rm -f "$CONTAINER" 2>/dev/null || true
  echo "Test data was at: $DATA_DIR"
}
trap cleanup EXIT

if $BUILD; then
  echo "==> Building Docker image..."
  docker build -t "$IMAGE" .
fi

mkdir -p "$DATA_DIR/data"

echo "==> Starting Satsbook container (port $PORT)..."
echo "    Data dir: $DATA_DIR/data"
echo "    LND: not connected (dashboard-only mode)"
echo ""

docker run --rm \
  --name "$CONTAINER" \
  -p "$PORT:$PORT" \
  -v "$DATA_DIR/data:/data" \
  -e SATSBOOK_DATABASE_PATH=/data/satsbook.db \
  -e SATSBOOK_APP_PORT=$PORT \
  -e SATSBOOK_LOG_LEVEL=debug \
  "$IMAGE"
