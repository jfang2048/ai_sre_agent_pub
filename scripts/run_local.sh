#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT_DIR}/build"

CONTROLLER_BIN="${BUILD_DIR}/sre-controller"
COLLECTOR_BIN="${BUILD_DIR}/sre-collector"

PORT_FILE="$(mktemp -t sre-ports-XXXXXX.json)"
CONFIG_DIR="${SRE_AGENT_CONFIG_DIR:-${ROOT_DIR}/configs}"
CONTROLLER_CONFIG="${SRE_CONTROLLER_CONFIG:-${CONFIG_DIR}/controller.yaml}"
COLLECTOR_CONFIG="${SRE_COLLECTOR_CONFIG:-${CONFIG_DIR}/collector.yaml}"
HTTP_LISTEN=""
GRPC_LISTEN=""

cleanup() {
  if [[ -n "${COLLECTOR_PID:-}" ]]; then
    kill "${COLLECTOR_PID}" 2>/dev/null || true
  fi
  if [[ -n "${CONTROLLER_PID:-}" ]]; then
    kill "${CONTROLLER_PID}" 2>/dev/null || true
  fi
  if [[ -f "${PORT_FILE:-}" ]]; then
    rm -f "${PORT_FILE}"
  fi
}
trap cleanup EXIT INT TERM

if [[ ! -x "${CONTROLLER_BIN}" || ! -x "${COLLECTOR_BIN}" ]]; then
  echo "Missing binaries; building..."
  (cd "${ROOT_DIR}" && make build)
fi

echo "Starting controller..."
SRE_CONTROLLER_CONFIG="${CONTROLLER_CONFIG}" "${CONTROLLER_BIN}" --port-file "${PORT_FILE}" &
CONTROLLER_PID=$!

# Best-effort: wait briefly for the controller process to at least start.
sleep 1

# If the controller had to fall back to an ephemeral port, pick it up from the port file.
if [[ -f "${PORT_FILE}" ]]; then
  HTTP_LISTEN=$(python3 - <<'PY'
import json,sys
path = sys.argv[1]
try:
    with open(path) as f:
        data = json.load(f)
    print(data.get("http_listen",""))
except Exception:
    print("")
PY "${PORT_FILE}")

  GRPC_LISTEN=$(python3 - <<'PY'
import json,sys
path = sys.argv[1]
try:
    with open(path) as f:
        data = json.load(f)
    print(data.get("grpc_listen",""))
except Exception:
    print("")
PY "${PORT_FILE}")
fi

normalize_addr() {
  local addr="$1"
  if [[ -z "${addr}" ]]; then
    echo ""
    return
  fi
  if [[ "$addr" == :* ]]; then
    echo "127.0.0.1${addr}"
  elif [[ "$addr" == 0.0.0.0:* ]]; then
    echo "127.0.0.1:${addr#0.0.0.0:}"
  elif [[ "$addr" == "[::]:"* ]] || [[ "$addr" == "::"* ]]; then
    echo "127.0.0.1:${addr##*:}"
  else
    echo "$addr"
  fi
}

HTTP_DIAL="$(normalize_addr "${HTTP_LISTEN}")"
GRPC_DIAL="$(normalize_addr "${GRPC_LISTEN}")"

if [[ -n "${GRPC_LISTEN}" ]]; then
  export SRE_COLLECTOR_CONTROLLER_ENDPOINTS="${GRPC_DIAL}"
fi

echo "Starting collector using ${COLLECTOR_CONFIG}..."
SRE_COLLECTOR_CONFIG="${COLLECTOR_CONFIG}" "${COLLECTOR_BIN}" &
COLLECTOR_PID=$!

echo "Running. API: http://${HTTP_DIAL:-127.0.0.1:8080}/api/v1/fleet"
wait "${CONTROLLER_PID}" "${COLLECTOR_PID}"
