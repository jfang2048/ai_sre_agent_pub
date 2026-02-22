#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${ROOT_DIR}/build"
CONTROLLER_BIN="${BIN_DIR}/sre-controller"
COLLECTOR_BIN="${BIN_DIR}/sre-collector"

ENABLE_AGENT=0
AGENT_ENV_FILE="${SRE_AGENT_ENV_FILE:-}"
AGENT_ENV_EXPLICIT=0

CONTROLLER_CONFIG="${SRE_CONTROLLER_CONFIG:-${ROOT_DIR}/configs/controller.yaml}"
COLLECTOR_CONFIG="${SRE_COLLECTOR_CONFIG:-${ROOT_DIR}/configs/collector.yaml}"

PORT_FILE="${SRE_STACK_PORT_FILE:-}"
PORT_FILE_OWNED=0

usage() {
  cat <<'EOF'
usage: ./scripts/run-local.sh [--enable-agent] [--agent-env <path>] [--help]

options:
  --enable-agent      Enable AGENT APIs and runtime integrations.
  --agent-env <path>  Load AGENT environment variables from this file.
  --help              Show this message.

environment:
  SRE_SKIP_BUILD=1        Skip `make build` and require prebuilt binaries.
  SRE_SKIP_UI_BUILD=1     Skip frontend build.
  SRE_STACK_PORT_FILE     Optional path to write resolved controller listen addresses.
EOF
}

cleanup() {
  if [[ -n "${COLLECTOR_PID:-}" || -n "${CONTROLLER_PID:-}" ]]; then
    echo "Stopping SRE Agent..."
  fi
  [[ -n "${COLLECTOR_PID:-}" ]] && kill "${COLLECTOR_PID}" 2>/dev/null || true
  [[ -n "${CONTROLLER_PID:-}" ]] && kill "${CONTROLLER_PID}" 2>/dev/null || true
  if [[ "${PORT_FILE_OWNED}" == "1" && -n "${PORT_FILE:-}" && -f "${PORT_FILE}" ]]; then
    rm -f "${PORT_FILE}"
  fi
}
trap cleanup EXIT INT TERM

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --enable-agent)
        ENABLE_AGENT=1
        shift
        ;;
      --agent-env)
        if [[ $# -lt 2 ]]; then
          echo "--agent-env requires a file path" >&2
          exit 1
        fi
        AGENT_ENV_FILE="$2"
        AGENT_ENV_EXPLICIT=1
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

load_agent_env() {
  if [[ "${ENABLE_AGENT}" != "1" ]]; then
    return
  fi

  if [[ -z "${AGENT_ENV_FILE}" ]]; then
    AGENT_ENV_FILE="${ROOT_DIR}/configs/agent.env"
  fi

  if [[ -f "${AGENT_ENV_FILE}" ]]; then
    echo "Loading AGENT env: ${AGENT_ENV_FILE}"
    set -a
    # shellcheck disable=SC1090
    source "${AGENT_ENV_FILE}"
    set +a
  elif [[ "${AGENT_ENV_EXPLICIT}" == "1" || -n "${SRE_AGENT_ENV_FILE:-}" ]]; then
    echo "agent env file not found: ${AGENT_ENV_FILE}" >&2
    exit 1
  fi

  export SRE_AGENT_LLM_ENABLED="${SRE_AGENT_LLM_ENABLED:-1}"
  export SRE_AGENT_DRY_RUN="${SRE_AGENT_DRY_RUN:-1}"
  if [[ -z "${SRE_AGENT_LLM_API_KEY:-}" && -z "${OPENAI_API_KEY:-}" ]]; then
    echo "AGENT enabled without API key; controller will use mock LLM client."
  fi
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

start_collector() {
  if [[ ! -f "${COLLECTOR_CONFIG}" ]]; then
    echo "collector config not found: ${COLLECTOR_CONFIG}" >&2
    exit 1
  fi

  local env_vars=(
    "SRE_COLLECTOR_CONFIG=${COLLECTOR_CONFIG}"
    "SRE_COLLECTOR_CONTROLLER_ENDPOINTS=${GRPC_DIAL}"
  )

  echo "Starting collector (controller ${GRPC_DIAL})..."
  (cd "${ROOT_DIR}" && env "${env_vars[@]}" "${COLLECTOR_BIN}") &
  COLLECTOR_PID=$!
}

main() {
  parse_args "$@"
  ensure_port_file
  build_binaries
  build_web_assets
  load_agent_env
  start_controller
  resolve_runtime_addrs
  start_collector

  echo "SRE Agent running."
  if [[ "${ENABLE_AGENT}" == "1" ]]; then
    echo "AGENT APIs: http://${HTTP_DIAL}/api/v1/agent/query"
  fi
  echo "Web UI/API: http://${HTTP_DIAL}/"
  echo "Fleet JSON: http://${HTTP_DIAL}/api/v1/fleet"
  echo "Press Ctrl+C to stop."
  wait "${CONTROLLER_PID}" "${COLLECTOR_PID}"
}

main "$@"
