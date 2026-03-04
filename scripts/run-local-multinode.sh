#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${ROOT_DIR}/build"
CONTROLLER_BIN="${BIN_DIR}/sre-controller"
COLLECTOR_BIN="${BIN_DIR}/sre-collector"

CONTROLLER_CONFIG="${SRE_CONTROLLER_CONFIG:-${ROOT_DIR}/configs/controller.yaml}"
COLLECTOR_CONFIG="${SRE_COLLECTOR_CONFIG:-${ROOT_DIR}/configs/collector.yaml}"
COLLECTOR_COUNT="${SRE_MULTI_NODE_COLLECTORS:-3}"
PORT_FILE="${SRE_STACK_PORT_FILE:-}"
PORT_FILE_OWNED=0

COLLECTOR_PIDS=()
COLLECTOR_SPOOLS=()

usage() {
  cat <<'EOF'
usage: ./scripts/run-local-multinode.sh [--collectors <n>] [--help]

options:
  --collectors <n>   Number of local collector processes to start (default: 3).
  --help             Show this message.

environment:
  SRE_SKIP_BUILD=1      Skip `make build` and require prebuilt binaries.
  SRE_SKIP_UI_BUILD=1   Skip frontend build.
EOF
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --collectors)
        if [[ $# -lt 2 ]]; then
          echo "--collectors requires a value" >&2
          exit 1
        fi
        COLLECTOR_COUNT="$2"
        shift 2
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        echo "unknown argument: $1" >&2
        usage
        exit 1
        ;;
    esac
  done
}

cleanup() {
  if [[ -n "${CONTROLLER_PID:-}" || ${#COLLECTOR_PIDS[@]} -gt 0 ]]; then
    echo "Stopping SRE Agent multi-node stack..."
  fi
  for pid in "${COLLECTOR_PIDS[@]}"; do
    kill "${pid}" 2>/dev/null || true
  done
  [[ -n "${CONTROLLER_PID:-}" ]] && kill "${CONTROLLER_PID}" 2>/dev/null || true
  for spool in "${COLLECTOR_SPOOLS[@]}"; do
    rm -rf "${spool}" 2>/dev/null || true
  done
  if [[ "${PORT_FILE_OWNED}" == "1" && -n "${PORT_FILE:-}" && -f "${PORT_FILE}" ]]; then
    rm -f "${PORT_FILE}"
  fi
}
trap cleanup EXIT INT TERM

ensure_port_file() {
  if [[ -n "${PORT_FILE}" ]]; then
    mkdir -p "$(dirname "${PORT_FILE}")"
    : >"${PORT_FILE}"
    return
  fi
  PORT_FILE="$(mktemp -t sre-ports-XXXXXX.json)"
  PORT_FILE_OWNED=1
}

read_port_value() {
  local key="$1"
  if [[ ! -f "${PORT_FILE}" ]]; then
    return
  fi
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" "${PORT_FILE}" | head -n1
}

normalize_addr() {
  local addr="$1"
  if [[ -z "${addr}" ]]; then
    echo ""
    return
  fi
  if [[ "${addr}" == :* ]]; then
    echo "127.0.0.1${addr}"
  elif [[ "${addr}" == 0.0.0.0:* ]]; then
    echo "127.0.0.1:${addr#0.0.0.0:}"
  elif [[ "${addr}" == "[::]:"* ]] || [[ "${addr}" == "::"* ]]; then
    echo "127.0.0.1:${addr##*:}"
  else
    echo "${addr}"
  fi
}

build_binaries() {
  if [[ "${SRE_SKIP_BUILD:-0}" == "1" ]]; then
    if [[ ! -x "${CONTROLLER_BIN}" || ! -x "${COLLECTOR_BIN}" ]]; then
      echo "SRE_SKIP_BUILD=1 but binaries are missing. Run: make build" >&2
      exit 1
    fi
    return
  fi
  echo "Building binaries..."
  make -C "${ROOT_DIR}" build >/dev/null
}

build_web_assets() {
  if [[ "${SRE_SKIP_UI_BUILD:-0}" == "1" ]]; then
    return
  fi
  if ! command -v npm >/dev/null 2>&1; then
    echo "npm not found; skipping UI build (set SRE_SKIP_UI_BUILD=1 to silence)"
    return
  fi
  echo "Building web UI assets..."
  npm -C "${ROOT_DIR}/frontend" run build >/dev/null
}

start_controller() {
  if [[ ! -f "${CONTROLLER_CONFIG}" ]]; then
    echo "controller config not found: ${CONTROLLER_CONFIG}" >&2
    exit 1
  fi

  local env_vars=(
    "SRE_CONTROLLER_CONFIG=${CONTROLLER_CONFIG}"
  )
  if [[ -n "${SRE_CONTROLLER_HTTP_LISTEN:-}" ]]; then
    env_vars+=("SRE_CONTROLLER_HTTP_LISTEN=${SRE_CONTROLLER_HTTP_LISTEN}")
  fi
  if [[ -n "${SRE_CONTROLLER_GRPC_LISTEN:-}" ]]; then
    env_vars+=("SRE_CONTROLLER_GRPC_LISTEN=${SRE_CONTROLLER_GRPC_LISTEN}")
  fi
  if [[ -n "${SRE_CONTROLLER_WEB_PATH:-}" ]]; then
    env_vars+=("SRE_CONTROLLER_WEB_PATH=${SRE_CONTROLLER_WEB_PATH}")
  fi

  echo "Starting controller..."
  (cd "${ROOT_DIR}" && env "${env_vars[@]}" "${CONTROLLER_BIN}" --port-file "${PORT_FILE}") &
  CONTROLLER_PID=$!

  sleep 1
  if ! kill -0 "${CONTROLLER_PID}" 2>/dev/null; then
    echo "Controller exited during startup. Check config: ${CONTROLLER_CONFIG}" >&2
    wait "${CONTROLLER_PID}" || true
    exit 1
  fi
}

resolve_runtime_addrs() {
  HTTP_LISTEN=""
  GRPC_LISTEN=""
  for _ in $(seq 1 30); do
    HTTP_LISTEN="$(read_port_value "http_listen")"
    GRPC_LISTEN="$(read_port_value "grpc_listen")"
    if [[ -n "${HTTP_LISTEN}" && -n "${GRPC_LISTEN}" ]]; then
      break
    fi
    sleep 0.1
  done

  HTTP_LISTEN="${HTTP_LISTEN:-${SRE_CONTROLLER_HTTP_LISTEN:-:8080}}"
  GRPC_LISTEN="${GRPC_LISTEN:-${SRE_CONTROLLER_GRPC_LISTEN:-:9090}}"
  HTTP_DIAL="$(normalize_addr "${HTTP_LISTEN}")"
  GRPC_DIAL="$(normalize_addr "${GRPC_LISTEN}")"
}

start_collectors() {
  if [[ ! -f "${COLLECTOR_CONFIG}" ]]; then
    echo "collector config not found: ${COLLECTOR_CONFIG}" >&2
    exit 1
  fi

  if ! [[ "${COLLECTOR_COUNT}" =~ ^[0-9]+$ ]] || [[ "${COLLECTOR_COUNT}" -lt 1 ]]; then
    echo "--collectors must be a positive integer" >&2
    exit 1
  fi

  for i in $(seq 1 "${COLLECTOR_COUNT}"); do
    local collector_id="collector-${i}"
    local hostname="node-${i}"
    local spool_dir
    local ebpf_socket_path
    spool_dir="$(mktemp -d -t "sre-collector-${i}-XXXXXX")"
    ebpf_socket_path="${spool_dir}/collector.ebpf.sock"
    COLLECTOR_SPOOLS+=("${spool_dir}")

    local env_vars=(
      "SRE_COLLECTOR_CONFIG=${COLLECTOR_CONFIG}"
      "SRE_COLLECTOR_CONTROLLER_ENDPOINTS=${GRPC_DIAL}"
      "SRE_COLLECTOR_ID=${collector_id}"
      "SRE_COLLECTOR_HOSTNAME=${hostname}"
      "SRE_COLLECTOR_SPOOL_DIR=${spool_dir}"
      "SRE_COLLECTOR_METRICS_ADDR=127.0.0.1:0"
      "SRE_COLLECTOR_EBPF_SOCKET_PATH=${ebpf_socket_path}"
    )

    echo "Starting collector ${collector_id} (${hostname})..."
    (cd "${ROOT_DIR}" && env "${env_vars[@]}" "${COLLECTOR_BIN}") &
    COLLECTOR_PIDS+=("$!")
  done
}

main() {
  parse_args "$@"
  ensure_port_file
  build_binaries
  build_web_assets
  start_controller
  resolve_runtime_addrs
  start_collectors

  echo "SRE Agent multi-node stack running."
  echo "Collectors: ${COLLECTOR_COUNT}"
  echo "Web UI/API: http://${HTTP_DIAL}/"
  echo "Inventory:  http://${HTTP_DIAL}/api/v1/inventory/probes"
  echo "Press Ctrl+C to stop."

  wait "${CONTROLLER_PID}" "${COLLECTOR_PIDS[@]}"
}

main "$@"
