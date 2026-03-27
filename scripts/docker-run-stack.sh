#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
USE_TSDB=0
USE_HOST_OBSERVER=0
SKIP_BUILD=0
NETWORK_MODE="${SRE_DOCKER_NETWORK_MODE:-auto}"
CONTROLLER_IMAGE="${SRE_CONTROLLER_IMAGE:-ai-sre-agent/controller:${SRE_VERSION:-v0.7}}"
COLLECTOR_IMAGE="${SRE_COLLECTOR_IMAGE:-ai-sre-agent/collector:${SRE_VERSION:-v0.7}}"
CONTROLLER_CONTAINER="${SRE_CONTROLLER_CONTAINER:-ai-sre-agent-controller}"
COLLECTOR_CONTAINER="${SRE_COLLECTOR_CONTAINER:-ai-sre-agent-collector}"
NETWORK_NAME="${SRE_DOCKER_NETWORK:-ai-sre-agent-net}"
CONTROLLER_VOLUME="${SRE_CONTROLLER_VOLUME:-ai-sre-agent-controller-data}"
COLLECTOR_VOLUME="${SRE_COLLECTOR_VOLUME:-ai-sre-agent-collector-data}"

usage() {
  cat <<'USAGE'
usage: ./scripts/docker-run-stack.sh [--skip-build] [--tsdb] [--host-observer]

Starts the canonical container stack. Prefer docker compose when available,
fall back to plain docker run otherwise.

Environment:
  SRE_DOCKER_NETWORK_MODE=auto|bridge|host
    auto   Try bridge first, then retry with host networking when local bridge
           networking is blocked by the runtime.
    bridge Force the default bridge network.
    host   Use host networking for the plain-docker fallback path.
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

run_plain_stack() {
  local mode="$1"
  local controller_endpoint_default="${CONTROLLER_CONTAINER}:9090"
  local network_args=()
  local controller_publish_args=()
  local collector_extra_args=()

  case "${mode}" in
    auto|bridge)
      docker network inspect "${NETWORK_NAME}" >/dev/null 2>&1 || docker network create "${NETWORK_NAME}" >/dev/null
      network_args=(--network "${NETWORK_NAME}")
      controller_publish_args=(
        -p "${SRE_BIND_HOST:-127.0.0.1}:${SRE_CONTROLLER_HTTP_PORT:-8080}:8080"
        -p "${SRE_BIND_HOST:-127.0.0.1}:${SRE_CONTROLLER_GRPC_PORT:-9090}:9090"
      )
      ;;
    host)
      network_args=(--network host)
      controller_endpoint_default="${SRE_COLLECTOR_CONTROLLER_ENDPOINTS:-127.0.0.1:${SRE_CONTROLLER_GRPC_PORT:-9090}}"
      ;;
    *)
      echo "unsupported SRE_DOCKER_NETWORK_MODE: ${mode}" >&2
      return 1
      ;;
  esac

  docker volume create "${CONTROLLER_VOLUME}" >/dev/null
  docker volume create "${COLLECTOR_VOLUME}" >/dev/null
  docker rm -f "${COLLECTOR_CONTAINER}" >/dev/null 2>&1 || true
  docker rm -f "${CONTROLLER_CONTAINER}" >/dev/null 2>&1 || true

  docker run -d \
    --name "${CONTROLLER_CONTAINER}" \
    --restart unless-stopped \
    --read-only \
    --security-opt no-new-privileges:true \
    --cap-drop ALL \
    --tmpfs /tmp:size=64m \
    "${network_args[@]}" \
    "${controller_publish_args[@]}" \
    -e SRE_CONTROLLER_CONFIG=/etc/ai-sre-agent/controller.yaml \
    -e SRE_CONTROLLER_HTTP_LISTEN=0.0.0.0:8080 \
    -e SRE_CONTROLLER_GRPC_LISTEN=0.0.0.0:9090 \
    -e SRE_CONTROLLER_WEB_PATH=/var/lib/ai-sre-agent/controller/web \
    -e SRE_INGEST_PERSIST_ENABLED="${SRE_INGEST_PERSIST_ENABLED:-1}" \
    -e SRE_AGENT_RAG_ENABLED="${SRE_AGENT_RAG_ENABLED:-1}" \
    -e SRE_AGENT_RAG_REBUILD_POLICY="${SRE_AGENT_RAG_REBUILD_POLICY:-if_missing}" \
    -e SRE_AGENT_LLM_ENABLED="${SRE_AGENT_LLM_ENABLED:-0}" \
    -e SRE_AGENT_DRY_RUN="${SRE_AGENT_DRY_RUN:-1}" \
    -v "${CONTROLLER_VOLUME}:/var/lib/ai-sre-agent/controller/data" \
    "${CONTROLLER_IMAGE}" >/dev/null

  docker run -d \
    --name "${COLLECTOR_CONTAINER}" \
    --restart unless-stopped \
    --read-only \
    --security-opt no-new-privileges:true \
    --cap-drop ALL \
    --tmpfs /tmp:size=64m \
    "${network_args[@]}" \
    "${collector_extra_args[@]}" \
    -e SRE_COLLECTOR_CONFIG=/etc/ai-sre-agent/collector.yaml \
    -e SRE_COLLECTOR_CONTROLLER_ENDPOINTS="${SRE_COLLECTOR_CONTROLLER_ENDPOINTS:-${controller_endpoint_default}}" \
    -e SRE_COLLECTOR_ID="${SRE_COLLECTOR_ID:-collector-local}" \
    -e SRE_COLLECTOR_HOSTNAME="${SRE_COLLECTOR_HOSTNAME:-collector-local}" \
    -e SRE_COLLECTOR_COLLECTION_INTERVAL="${SRE_COLLECTOR_COLLECTION_INTERVAL:-5s}" \
    -e SRE_COLLECTOR_LEVEL="${SRE_COLLECTOR_LEVEL:-5}" \
    -e SRE_COLLECTOR_METRICS_ADDR=:9464 \
    -v "${COLLECTOR_VOLUME}:/var/lib/ai-sre-agent/collector/data" \
    "${COLLECTOR_IMAGE}" >/dev/null
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
    --host-observer)
      USE_HOST_OBSERVER=1
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
  if [[ "${SKIP_BUILD}" != "1" ]]; then
    DOCKER_BUILDKIT="${DOCKER_BUILDKIT:-1}" COMPOSE_DOCKER_CLI_BUILD="${COMPOSE_DOCKER_CLI_BUILD:-1}" \
      compose_cmd "${compose_files[@]}" build
  fi
  compose_cmd "${compose_files[@]}" up -d --remove-orphans
else
  if [[ "${USE_TSDB}" == "1" || "${USE_HOST_OBSERVER}" == "1" ]]; then
    echo "--tsdb and --host-observer require docker compose support" >&2
    exit 1
  fi
  if [[ "${SKIP_BUILD}" != "1" ]]; then
    "${ROOT_DIR}/scripts/docker-build.sh"
  fi
  if ! run_plain_stack "${NETWORK_MODE}"; then
    if [[ "${NETWORK_MODE}" == "auto" ]]; then
      echo "bridge networking failed; retrying with host networking" >&2
      docker rm -f "${COLLECTOR_CONTAINER}" >/dev/null 2>&1 || true
      docker rm -f "${CONTROLLER_CONTAINER}" >/dev/null 2>&1 || true
      docker network rm "${NETWORK_NAME}" >/dev/null 2>&1 || true
      run_plain_stack host
    else
      exit 1
    fi
  fi
fi

for _ in $(seq 1 90); do
  if curl -fsS "http://${SRE_BIND_HOST:-127.0.0.1}:${SRE_CONTROLLER_HTTP_PORT:-8080}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "http://${SRE_BIND_HOST:-127.0.0.1}:${SRE_CONTROLLER_HTTP_PORT:-8080}/healthz" >/dev/null

echo "Container stack is up"
echo "UI/API:  http://${SRE_BIND_HOST:-127.0.0.1}:${SRE_CONTROLLER_HTTP_PORT:-8080}"
echo "Health:  http://${SRE_BIND_HOST:-127.0.0.1}:${SRE_CONTROLLER_HTTP_PORT:-8080}/healthz"
echo "Fleet:   http://${SRE_BIND_HOST:-127.0.0.1}:${SRE_CONTROLLER_HTTP_PORT:-8080}/api/v1/fleet"
