#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USE_TSDB=0
SKIP_BUILD=0
LOG_DIR="${SRE_DOCKER_SMOKE_LOG_DIR:-${ROOT_DIR}/build/docker-smoke}"
mkdir -p "${LOG_DIR}"

usage() {
  cat <<'USAGE'
usage: ./scripts/docker-smoke.sh [--skip-build] [--tsdb]

Builds and validates the canonical container stack.
USAGE
}

compose_available() {
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1
}

compose_cmd() {
  docker compose "$@"
}

compose_files=()
append_compose_file() {
  compose_files+=( -f "$1" )
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --tsdb)
      USE_TSDB=1
      shift
      ;;
    -h|--help)
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

cleanup() {
  local stop_args=(--prune)
  if [[ "${USE_TSDB}" == "1" ]]; then
    stop_args=(--tsdb --prune)
  fi
  if compose_available; then
    compose_cmd "${compose_files[@]}" logs --no-color >"${LOG_DIR}/compose.log" 2>&1 || true
  else
    docker logs "${SRE_CONTROLLER_CONTAINER:-ai-sre-agent-controller}" >"${LOG_DIR}/controller.log" 2>&1 || true
    docker logs "${SRE_COLLECTOR_CONTAINER:-ai-sre-agent-collector}" >"${LOG_DIR}/collector.log" 2>&1 || true
  fi
  "${ROOT_DIR}/scripts/docker-stop-stack.sh" "${stop_args[@]}" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if compose_available; then
  append_compose_file "${ROOT_DIR}/docker-compose.yaml"
  if [[ "${USE_TSDB}" == "1" ]]; then
    append_compose_file "${ROOT_DIR}/deploy/docker/docker-compose-tsdb.yml"
  fi
  export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-ai-sre-agent-smoke}"
  if [[ "${SKIP_BUILD}" != "1" ]]; then
    DOCKER_BUILDKIT="${DOCKER_BUILDKIT:-1}" COMPOSE_DOCKER_CLI_BUILD="${COMPOSE_DOCKER_CLI_BUILD:-1}" \
      compose_cmd "${compose_files[@]}" build
  fi
  compose_cmd "${compose_files[@]}" up -d --remove-orphans
else
  if [[ "${USE_TSDB}" == "1" ]]; then
    echo "--tsdb smoke requires docker compose support" >&2
    exit 1
  fi
  args=()
  if [[ "${SKIP_BUILD}" == "1" ]]; then
    args+=(--skip-build)
  fi
  "${ROOT_DIR}/scripts/docker-run-stack.sh" "${args[@]}"
fi

BASE_URL="http://${SRE_BIND_HOST:-127.0.0.1}:${SRE_CONTROLLER_HTTP_PORT:-8080}"
for _ in $(seq 1 90); do
  if curl -fsS "${BASE_URL}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "${BASE_URL}/healthz" >/dev/null
curl -fsS "${BASE_URL}/api/v1/status" >"${LOG_DIR}/status.json"
curl -fsS "${BASE_URL}/api/v1/fleet" >"${LOG_DIR}/fleet.json"
curl -fsS "${BASE_URL}/api/v1/inventory/status" >"${LOG_DIR}/inventory-status.json"
curl -fsS "${BASE_URL}/api/v1/inventory/probes" >"${LOG_DIR}/inventory-probes.json"
curl -fsS "${BASE_URL}/api/v1/storage/status" >"${LOG_DIR}/storage-status.json"
curl -fsS "${BASE_URL}/api/v1/rag/status" >"${LOG_DIR}/rag-status.json"
curl -fsS "${BASE_URL}/api/v1/agent/joint-risk?limit=1" >"${LOG_DIR}/joint-risk.json"
curl -fsS "${BASE_URL}/" >"${LOG_DIR}/index.html"

python3 - <<PY
import json, pathlib
log_dir = pathlib.Path(${LOG_DIR@Q})
status = json.loads((log_dir / 'status.json').read_text())
fleet = json.loads((log_dir / 'fleet.json').read_text())
inventory = json.loads((log_dir / 'inventory-probes.json').read_text())
rag = json.loads((log_dir / 'rag-status.json').read_text())
storage = json.loads((log_dir / 'storage-status.json').read_text())
joint_risk = json.loads((log_dir / 'joint-risk.json').read_text())
assert status.get('status') in {'ok', 'degraded'} or status.get('uptime') == 'running', status
assert isinstance(fleet.get('nodes', []), list), fleet
assert isinstance(inventory.get('probes', []), list), inventory
assert 'ready' in rag, rag
assert storage.get('tsdb') is not None or storage.get('storage') is not None, storage
assert isinstance(joint_risk.get('reports', []), list), joint_risk
print('docker smoke ok')
PY

echo "docker smoke ok"
