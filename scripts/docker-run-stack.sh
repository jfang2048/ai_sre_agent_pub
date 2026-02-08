#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

COLLECTOR_IMAGE="${COLLECTOR_IMAGE:-sre-collector:latest}"
CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-sre-controller:latest}"

NETWORK_NAME="${SRE_DOCKER_NETWORK:-sre-agent-net}"
CONTROLLER_CONTAINER="${SRE_CONTROLLER_CONTAINER:-sre-controller}"
COLLECTOR_CONTAINER="${SRE_COLLECTOR_CONTAINER:-sre-collector}"

CONTROLLER_VOLUME="${SRE_CONTROLLER_VOLUME:-sre-controller-data}"
COLLECTOR_VOLUME="${SRE_COLLECTOR_VOLUME:-sre-collector-data}"

HTTP_PORT="${SRE_CONTROLLER_HTTP_PORT:-8080}"
GRPC_PORT="${SRE_CONTROLLER_GRPC_PORT:-9090}"
COLLECTOR_LEVEL="${SRE_COLLECTOR_LEVEL:-5}"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/docker-run-stack.sh [--skip-build]

Starts controller + collector with plain docker run.

Options:
  --skip-build   Skip image build step.

Environment overrides:
  COLLECTOR_IMAGE         Collector image tag (default: sre-collector:latest)
  CONTROLLER_IMAGE        Controller image tag (default: sre-controller:latest)
  SRE_DOCKER_NETWORK      Docker network name (default: sre-agent-net)
  SRE_CONTROLLER_CONTAINER Controller container name (default: sre-controller)
  SRE_COLLECTOR_CONTAINER Collector container name (default: sre-collector)
  SRE_CONTROLLER_VOLUME   Controller data volume (default: sre-controller-data)
  SRE_COLLECTOR_VOLUME    Collector data volume (default: sre-collector-data)
  SRE_CONTROLLER_HTTP_PORT Host HTTP port for controller (default: 8080)
  SRE_CONTROLLER_GRPC_PORT Host gRPC port for controller (default: 9090)
  SRE_COLLECTOR_LEVEL     Collector level (default: 5)
EOF
}

SKIP_BUILD=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      SKIP_BUILD=1
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

if [[ "${SKIP_BUILD}" -eq 0 ]]; then
  "${ROOT_DIR}/scripts/docker-build.sh"
fi

if ! docker network inspect "${NETWORK_NAME}" >/dev/null 2>&1; then
  echo "Creating docker network: ${NETWORK_NAME}"
  docker network create "${NETWORK_NAME}" >/dev/null
fi

docker volume create "${CONTROLLER_VOLUME}" >/dev/null
docker volume create "${COLLECTOR_VOLUME}" >/dev/null

if docker ps -a --format '{{.Names}}' | grep -qx "${COLLECTOR_CONTAINER}"; then
  echo "Removing existing collector container: ${COLLECTOR_CONTAINER}"
  docker rm -f "${COLLECTOR_CONTAINER}" >/dev/null
fi

if docker ps -a --format '{{.Names}}' | grep -qx "${CONTROLLER_CONTAINER}"; then
  echo "Removing existing controller container: ${CONTROLLER_CONTAINER}"
  docker rm -f "${CONTROLLER_CONTAINER}" >/dev/null
fi

echo "Starting controller container: ${CONTROLLER_CONTAINER}"
docker run -d \
  --name "${CONTROLLER_CONTAINER}" \
  --restart unless-stopped \
  --network "${NETWORK_NAME}" \
  -p "${HTTP_PORT}:8080" \
  -p "${GRPC_PORT}:9090" \
  -e SRE_CONTROLLER_CONFIG=/etc/sre-controller/config.yaml \
  -e SRE_CONTROLLER_HTTP_LISTEN=:8080 \
  -e SRE_CONTROLLER_GRPC_LISTEN=:9090 \
  -e SRE_CONTROLLER_WEB_PATH=/var/lib/sre-controller/web \
  -v "${CONTROLLER_VOLUME}:/var/lib/sre-controller/data" \
  "${CONTROLLER_IMAGE}" >/dev/null

echo "Waiting for controller health..."
ready=0
for i in $(seq 1 90); do
  status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${CONTROLLER_CONTAINER}" 2>/dev/null || true)"
  if [[ "${status}" == "healthy" || "${status}" == "running" ]]; then
    ready=1
    break
  fi
  if [[ "${status}" == "exited" || "${status}" == "dead" ]]; then
    echo "Controller failed to start. Logs:"
    docker logs --tail 200 "${CONTROLLER_CONTAINER}" || true
    exit 1
  fi
  sleep 1
done
if [[ "${ready}" -ne 1 ]]; then
  echo "Controller did not become ready in time. Last status: ${status:-unknown}"
  docker logs --tail 200 "${CONTROLLER_CONTAINER}" || true
  exit 1
fi

echo "Starting collector container: ${COLLECTOR_CONTAINER}"
docker run -d \
  --name "${COLLECTOR_CONTAINER}" \
  --restart unless-stopped \
  --network "${NETWORK_NAME}" \
  -e SRE_COLLECTOR_CONTROLLER_ENDPOINTS="${CONTROLLER_CONTAINER}:9090" \
  -e SRE_COLLECTOR_SPOOL_DIR=/var/lib/sre-collector/spool \
  -e SRE_COLLECTOR_CONFIG=/etc/sre-collector/config.yaml \
  -e SRE_COLLECTOR_LEVEL="${COLLECTOR_LEVEL}" \
  -v "${COLLECTOR_VOLUME}:/var/lib/sre-collector" \
  "${COLLECTOR_IMAGE}" >/dev/null

echo "Stack is up."
echo "API/UI: http://127.0.0.1:${HTTP_PORT}"
echo "Health: http://127.0.0.1:${HTTP_PORT}/healthz"
echo "Fleet:  http://127.0.0.1:${HTTP_PORT}/api/v1/fleet"
echo "Logs:"
echo "  docker logs -f ${CONTROLLER_CONTAINER}"
echo "  docker logs -f ${COLLECTOR_CONTAINER}"
