#!/usr/bin/env bash
set -euo pipefail

# Simple one-shot local demo: build controller and collector, run both,
# and clean up on exit. Uses default configs under ./configs/.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT/build"
CTL_BIN="$BIN_DIR/sre-controller"
COL_BIN="$BIN_DIR/sre-collector"

cleanup() {
  echo "Stopping SRE Agent..."
  [[ -n "${CTL_PID:-}" ]] && kill "${CTL_PID}" 2>/dev/null || true
  [[ -n "${COL_PID:-}" ]] && kill "${COL_PID}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "Building binaries..."
make -C "$ROOT" build >/dev/null

echo "Starting controller (http :8080, grpc :9090)..."
cd "$ROOT"
SRE_CONTROLLER_CONFIG="$ROOT/configs/controller.yaml" \
SRE_CONTROLLER_HTTP_LISTEN=127.0.0.1:8080 \
SRE_CONTROLLER_GRPC_LISTEN=127.0.0.1:9090 \
  "$CTL_BIN" &
CTL_PID=$!
sleep 1

echo "Starting collector (pushes to 127.0.0.1:9090)..."
SRE_COLLECTOR_CONFIG="$ROOT/configs/collector.yaml" \
SRE_COLLECTOR_CONTROLLER_ENDPOINTS=127.0.0.1:9090 \
  "$COL_BIN" &
COL_PID=$!

echo "SRE Agent running."
echo "Web UI/API: http://127.0.0.1:8080/"
echo "Fleet JSON: http://127.0.0.1:8080/api/v1/fleet"
echo "Press Ctrl+C to stop."
wait
