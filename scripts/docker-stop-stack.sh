#!/usr/bin/env bash
set -euo pipefail

CONTROLLER_CONTAINER="${SRE_CONTROLLER_CONTAINER:-sre-controller}"
COLLECTOR_CONTAINER="${SRE_COLLECTOR_CONTAINER:-sre-collector}"
NETWORK_NAME="${SRE_DOCKER_NETWORK:-sre-agent-net}"
CONTROLLER_VOLUME="${SRE_CONTROLLER_VOLUME:-sre-controller-data}"
COLLECTOR_VOLUME="${SRE_COLLECTOR_VOLUME:-sre-collector-data}"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/docker-stop-stack.sh [--prune]

Stops and removes controller + collector containers created by docker-run-stack.sh.

Options:
  --prune   Also remove docker network and data volumes.

Environment overrides:
  SRE_CONTROLLER_CONTAINER  Controller container name (default: sre-controller)
  SRE_COLLECTOR_CONTAINER   Collector container name (default: sre-collector)
  SRE_DOCKER_NETWORK        Docker network name (default: sre-agent-net)
  SRE_CONTROLLER_VOLUME     Controller data volume (default: sre-controller-data)
  SRE_COLLECTOR_VOLUME      Collector data volume (default: sre-collector-data)
EOF
}

PRUNE=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prune)
      PRUNE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1"
      usage
      exit 1
      ;;
  esac
done

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required"
  exit 1
fi

if docker ps -a --format '{{.Names}}' | grep -qx "${COLLECTOR_CONTAINER}"; then
  echo "Removing collector container: ${COLLECTOR_CONTAINER}"
  docker rm -f "${COLLECTOR_CONTAINER}" >/dev/null
fi

if docker ps -a --format '{{.Names}}' | grep -qx "${CONTROLLER_CONTAINER}"; then
  echo "Removing controller container: ${CONTROLLER_CONTAINER}"
  docker rm -f "${CONTROLLER_CONTAINER}" >/dev/null
fi

if [[ "${PRUNE}" -eq 1 ]]; then
  if docker network inspect "${NETWORK_NAME}" >/dev/null 2>&1; then
    echo "Removing docker network: ${NETWORK_NAME}"
    docker network rm "${NETWORK_NAME}" >/dev/null || true
  fi
  echo "Removing volumes: ${CONTROLLER_VOLUME}, ${COLLECTOR_VOLUME}"
  docker volume rm "${CONTROLLER_VOLUME}" "${COLLECTOR_VOLUME}" >/dev/null 2>&1 || true
fi

echo "Done."
