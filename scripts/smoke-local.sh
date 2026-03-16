#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT_FILE="$(mktemp -t sre-smoke-ports-XXXXXX.json)"
STACK_LOG="$(mktemp -t sre-smoke-stack-XXXXXX.log)"
STACK_PID=""

cleanup() {
  if [[ -n "${STACK_PID}" ]]; then
    kill "${STACK_PID}" 2>/dev/null || true
  fi
  rm -f "${PORT_FILE}"
  rm -f "${STACK_LOG}"
}
trap cleanup EXIT INT TERM

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

echo "Running build checks..."
(cd "${ROOT_DIR}" && make ci && make build)

echo "Starting local stack..."
SRE_SKIP_BUILD=1 \
SRE_SKIP_UI_BUILD=1 \
SRE_STACK_PORT_FILE="${PORT_FILE}" \
  "${ROOT_DIR}/scripts/run-local.sh" >"${STACK_LOG}" 2>&1 &
STACK_PID=$!

HTTP_LISTEN=""
for _ in $(seq 1 40); do
  HTTP_LISTEN="$(read_port_value "http_listen")"
  if [[ -n "${HTTP_LISTEN}" ]]; then
    break
  fi
  sleep 0.1
done

HTTP_DIAL="$(normalize_addr "${HTTP_LISTEN:-:8080}")"
HTTP_URL="http://${HTTP_DIAL}"

if ! kill -0 "${STACK_PID}" 2>/dev/null; then
  if rg -qi "operation not permitted|socket: operation not permitted" "${STACK_LOG}"; then
    echo "network operations not permitted in this environment; skipping live HTTP checks"
    exit 0
  fi
  echo "local stack exited before smoke checks completed" >&2
  sed -n '1,200p' "${STACK_LOG}" >&2
  exit 1
fi

echo "Waiting for controller health at ${HTTP_URL}/healthz ..."
health_out="$(python3 - <<PY
import time, urllib.request, sys
base = "${HTTP_URL}"
deadline = time.time() + 20
last = None
while time.time() < deadline:
    try:
        with urllib.request.urlopen(base + "/healthz", timeout=2) as r:
            if r.status == 200:
                sys.exit(0)
    except Exception as e:
        last = e
    time.sleep(0.5)
msg = str(last) if last is not None else ""
if "Operation not permitted" in msg or "Errno 1" in msg:
    print("network operations not permitted in this environment; skipping live HTTP checks")
    sys.exit(0)
print("controller did not become healthy:", last, file=sys.stderr)
sys.exit(1)
PY
)"

if echo "${health_out}" | grep -q "skipping live HTTP checks"; then
  exit 0
fi

echo "Fetching /api/v1/fleet ..."
python3 - <<PY
import json, urllib.request
base = "${HTTP_URL}"
with urllib.request.urlopen(base + "/api/v1/fleet", timeout=5) as r:
    data = json.load(r)
print("ok: /api/v1/fleet keys:", sorted(list(data.keys()))[:20])
PY

echo "Smoke test OK"
