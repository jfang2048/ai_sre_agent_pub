#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USE_TSDB=0
USE_HOST_OBSERVER=0
PRUNE=0
CONTROLLER_CONTAINER="${SRE_CONTROLLER_CONTAINER:-ai-sre-agent-controller}"
COLLECTOR_CONTAINER="${SRE_COLLECTOR_CONTAINER:-ai-sre-agent-collector}"
NETWORK_NAME="${SRE_DOCKER_NETWORK:-ai-sre-agent-net}"
CONTROLLER_VOLUME="${SRE_CONTROLLER_VOLUME:-ai-sre-agent-controller-data}"
COLLECTOR_VOLUME="${SRE_COLLECTOR_VOLUME:-ai-sre-agent-collector-data}"

usage() {
  cat <<'USAGE'
usage: ./scripts/docker-stop-stack.sh [--tsdb] [--host-observer] [--prune]

Stops the canonical container stack. Prefer docker compose when available,
fall back to plain docker cleanup otherwise.
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
    --tsdb)
      USE_TSDB=1
      shift
      ;;
    --host-observer)
      USE_HOST_OBSERVER=1
      shift
      ;;
    --prune)
      PRUNE=1
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

if compose_available; then
  append_compose_file "${ROOT_DIR}/docker-compose.yaml"
  if [[ "${USE_TSDB}" == "1" ]]; then
    append_compose_file "${ROOT_DIR}/deploy/docker/docker-compose-tsdb.yml"
  fi
  if [[ "${USE_HOST_OBSERVER}" == "1" ]]; then
    append_compose_file "${ROOT_DIR}/deploy/docker/docker-compose.host-observer.yml"
  fi
  export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-ai-sre-agent}"
  if [[ "${PRUNE}" == "1" ]]; then
    compose_cmd "${compose_files[@]}" down -v --remove-orphans
  else
    compose_cmd "${compose_files[@]}" down --remove-orphans
  fi
else
  docker rm -f "${COLLECTOR_CONTAINER}" >/dev/null 2>&1 || true
  docker rm -f "${CONTROLLER_CONTAINER}" >/dev/null 2>&1 || true
  if [[ "${PRUNE}" == "1" ]]; then
    docker network rm "${NETWORK_NAME}" >/dev/null 2>&1 || true
    docker volume rm "${CONTROLLER_VOLUME}" "${COLLECTOR_VOLUME}" >/dev/null 2>&1 || true
  fi
fi

echo "Container stack stopped"
