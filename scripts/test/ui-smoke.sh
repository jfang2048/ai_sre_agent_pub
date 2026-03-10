#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PORT_FILE="$(mktemp -t sre-ui-ports-XXXXXX.json)"
STACK_LOG="$(mktemp -t sre-ui-stack-XXXXXX.log)"
UI_WEB_DIR="$(mktemp -d -t sre-ui-web-XXXXXX)"
STACK_PID=""

cleanup() {
  if [[ -n "${STACK_PID}" ]]; then
    kill "${STACK_PID}" 2>/dev/null || true
    wait "${STACK_PID}" 2>/dev/null || true
  fi
  rm -f "${PORT_FILE}" "${STACK_LOG}"
  rm -rf "${UI_WEB_DIR}"
}
trap cleanup EXIT INT TERM

read_port_value() {
  local key="$1"
  sed -n "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "${PORT_FILE}" | head -n1
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
  elif [[ "${addr}" == "[::]:"* ]] || [[ "${addr}" == ::* ]]; then
    echo "127.0.0.1:${addr##*:}"
  else
    echo "${addr}"
  fi
}

if ! command -v npm >/dev/null 2>&1; then
  echo "npm not found; cannot run UI smoke tests" >&2
  exit 1
fi

if [[ ! -d "${ROOT_DIR}/tests/ui/node_modules" ]]; then
  echo "Installing UI test dependencies..."
  npm -C "${ROOT_DIR}/tests/ui" install >/dev/null
fi

echo "Building backend binaries..."
make -C "${ROOT_DIR}" build >/dev/null

echo "Building frontend assets..."
npm -C "${ROOT_DIR}/frontend" run build -- --outDir "${UI_WEB_DIR}" >/dev/null

echo "Rebuilding local RAG index for UI tests..."
make -C "${ROOT_DIR}" rag-rebuild >/dev/null

echo "Starting local stack for UI tests..."
SRE_SKIP_BUILD=1 \
SRE_SKIP_UI_BUILD=1 \
SRE_CONTROLLER_HTTP_LISTEN="127.0.0.1:0" \
SRE_CONTROLLER_WEB_PATH="${UI_WEB_DIR}" \
SRE_STACK_PORT_FILE="${PORT_FILE}" \
SRE_AGENT_RAG_ENABLED=1 \
SRE_AGENT_RAG_DATASET_PATH="${ROOT_DIR}/dataset" \
SRE_AGENT_RAG_INDEX_PATH="${ROOT_DIR}/data/agent/rag/index.json" \
SRE_AGENT_RAG_REBUILD_POLICY=if_missing \
  "${ROOT_DIR}/scripts/run-local.sh" --enable-agent --demo --llm=stub >"${STACK_LOG}" 2>&1 &
STACK_PID=$!

HTTP_LISTEN=""
for _ in $(seq 1 100); do
  if ! kill -0 "${STACK_PID}" 2>/dev/null; then
    echo "Local stack exited before publishing listen addresses." >&2
    sed -n '1,200p' "${STACK_LOG}" >&2
    exit 1
  fi
  HTTP_LISTEN="$(read_port_value "http_listen")"
  if [[ -n "${HTTP_LISTEN}" ]]; then
    break
  fi
  sleep 0.2
done

if [[ -z "${HTTP_LISTEN}" ]]; then
  echo "Local stack did not publish an HTTP listen address." >&2
  sed -n '1,200p' "${STACK_LOG}" >&2
  exit 1
fi

HTTP_DIAL="$(normalize_addr "${HTTP_LISTEN}")"
BASE_URL="http://${HTTP_DIAL}"

echo "Waiting for controller health at ${BASE_URL}/healthz ..."
for _ in $(seq 1 50); do
  if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${STACK_PID}" 2>/dev/null; then
    echo "Local stack exited before becoming healthy." >&2
    sed -n '1,200p' "${STACK_LOG}" >&2
    exit 1
  fi
  sleep 0.5
done

if ! curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
  echo "Controller did not become healthy in time." >&2
  sed -n '1,200p' "${STACK_LOG}" >&2
  exit 1
fi

echo "Running Playwright suite against ${BASE_URL} ..."
BASE_URL="${BASE_URL}" npm -C "${ROOT_DIR}/tests/ui" run test

echo "UI smoke tests passed."
