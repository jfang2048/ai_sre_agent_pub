#!/usr/bin/env bash
set -euo pipefail

# Simple one-shot local demo: build controller and collector, run both,
# and clean up on exit. Uses default configs under ./configs/.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="$ROOT/build"
CTL_BIN="$BIN_DIR/sre-controller"
COL_BIN="$BIN_DIR/sre-collector"
ENABLE_AGENT=0
AGENT_ENV_FILE="${SRE_AGENT_ENV_FILE:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --enable-agent)
      ENABLE_AGENT=1
      shift
      ;;
    --agent-env)
      if [[ $# -lt 2 ]]; then
        echo "--agent-env requires a file path"
        exit 1
      fi
      AGENT_ENV_FILE="$2"
      shift 2
      ;;
    *)
      echo "unknown argument: $1"
      echo "usage: $0 [--enable-agent] [--agent-env <path>]"
      exit 1
      ;;
  esac
done

cleanup() {
  echo "Stopping SRE Agent..."
  [[ -n "${CTL_PID:-}" ]] && kill "${CTL_PID}" 2>/dev/null || true
  [[ -n "${COL_PID:-}" ]] && kill "${COL_PID}" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "Building binaries..."
make -C "$ROOT" build >/dev/null

if [[ "${SRE_SKIP_UI_BUILD:-0}" != "1" ]]; then
  if command -v npm >/dev/null 2>&1; then
    echo "Building web UI assets..."
    npm -C "$ROOT/frontend" run build >/dev/null
  else
    echo "npm not found; skipping UI build (set SRE_SKIP_UI_BUILD=1 to silence)"
  fi
fi

echo "Starting controller (http :8080, grpc :9090)..."
cd "$ROOT"
if [[ "$ENABLE_AGENT" == "1" ]]; then
  if [[ -z "$AGENT_ENV_FILE" ]]; then
    AGENT_ENV_FILE="$ROOT/configs/agent.env"
  fi
  if [[ -f "$AGENT_ENV_FILE" ]]; then
    echo "Loading AGENT env: $AGENT_ENV_FILE"
    set -a
    # shellcheck disable=SC1090
    source "$AGENT_ENV_FILE"
    set +a
  elif [[ "${SRE_AGENT_ENV_FILE:-}" != "" ]]; then
    echo "agent env file not found: $AGENT_ENV_FILE"
    exit 1
  fi

  export SRE_AGENT_LLM_ENABLED="${SRE_AGENT_LLM_ENABLED:-1}"
  export SRE_AGENT_DRY_RUN="${SRE_AGENT_DRY_RUN:-1}"
  if [[ -z "${SRE_AGENT_LLM_API_KEY:-}" && -z "${OPENAI_API_KEY:-}" ]]; then
    echo "AGENT enabled without API key; controller will use mock LLM client."
  fi
fi

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
if [[ "$ENABLE_AGENT" == "1" ]]; then
  echo "AGENT APIs: http://127.0.0.1:8080/api/v1/agent/query"
fi
echo "Web UI/API: http://127.0.0.1:8080/"
echo "Fleet JSON: http://127.0.0.1:8080/api/v1/fleet"
echo "Press Ctrl+C to stop."
wait
