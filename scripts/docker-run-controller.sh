#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/version.sh"
IMAGE="${SRE_CONTROLLER_IMAGE:-ai-sre-agent/controller:${SRE_VERSION:-$(repo_version "${ROOT_DIR}")}}"
CONTAINER_NAME="${SRE_CONTROLLER_CONTAINER:-ai-sre-agent-controller}"
HTTP_PORT="${SRE_CONTROLLER_HTTP_PORT:-8080}"
GRPC_PORT="${SRE_CONTROLLER_GRPC_PORT:-9090}"
BIND_HOST="${SRE_BIND_HOST:-127.0.0.1}"
CONFIG_FILE="${SRE_CONTROLLER_CONFIG_FILE:-}"
TARGETS_FILE="${SRE_CONTROLLER_TARGETS_MOUNT:-}"
DATA_DIR="${SRE_CONTROLLER_DATA_DIR:-}"
VOLUME_NAME="${SRE_CONTROLLER_VOLUME:-ai-sre-agent-controller-data}"
NETWORK_MODE="${SRE_DOCKER_NETWORK_MODE:-bridge}"
SKIP_BUILD=0
RUN_STDOUT=""
RUN_STDERR=""

usage() {
  cat <<'USAGE'
usage: ./scripts/docker-run-controller.sh [--skip-build] [--config-file <path>] [--targets-file <path>]

Run only the controller container.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --config-file)
      CONFIG_FILE="$2"
      shift 2
      ;;
    --targets-file)
      TARGETS_FILE="$2"
      shift 2
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

if [[ "${SKIP_BUILD}" != "1" ]]; then
  "${ROOT_DIR}/scripts/docker-build-controller.sh"
fi

docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

volume_args=()
if [[ -n "${DATA_DIR}" ]]; then
  mkdir -p "${DATA_DIR}"
  volume_args+=(-v "${DATA_DIR}:/var/lib/ai-sre-agent/controller/data")
else
  docker volume create "${VOLUME_NAME}" >/dev/null
  volume_args+=(-v "${VOLUME_NAME}:/var/lib/ai-sre-agent/controller/data")
fi

config_args=()
if [[ -n "${CONFIG_FILE}" ]]; then
  if [[ ! -f "${CONFIG_FILE}" ]]; then
    echo "controller config file not found: ${CONFIG_FILE}" >&2
    exit 1
  fi
  config_args+=(-v "${CONFIG_FILE}:/etc/ai-sre-agent/controller.yaml:ro")
fi
if [[ -n "${TARGETS_FILE}" ]]; then
  if [[ ! -f "${TARGETS_FILE}" ]]; then
    echo "controller targets file not found: ${TARGETS_FILE}" >&2
    exit 1
  fi
  config_args+=(-v "${TARGETS_FILE}:/etc/ai-sre-agent/controller_targets.yaml:ro")
fi

base_args=(
  --restart unless-stopped \
  -e SRE_CONTROLLER_CONFIG=/etc/ai-sre-agent/controller.yaml \
  -e SRE_CONTROLLER_TARGETS_FILE=/etc/ai-sre-agent/controller_targets.yaml \
  -e SRE_CONTROLLER_HTTP_LISTEN=0.0.0.0:8080 \
  -e SRE_CONTROLLER_GRPC_LISTEN=0.0.0.0:9090 \
  -e SRE_CONTROLLER_WEB_PATH=/var/lib/ai-sre-agent/controller/web \
  -e SRE_INGEST_PERSIST_ENABLED="${SRE_INGEST_PERSIST_ENABLED:-1}" \
  -e SRE_AGENT_RAG_ENABLED="${SRE_AGENT_RAG_ENABLED:-1}" \
  -e SRE_AGENT_RAG_REBUILD_POLICY="${SRE_AGENT_RAG_REBUILD_POLICY:-if_missing}" \
  -e SRE_AGENT_LLM_ENABLED="${SRE_AGENT_LLM_ENABLED:-0}" \
  -e SRE_AGENT_DRY_RUN="${SRE_AGENT_DRY_RUN:-1}" \
  "${volume_args[@]}" \
  "${config_args[@]}" \
)

build_runtime_args() {
  local mode="$1"
  case "${mode}" in
    host)
      printf '%s\0' \
        --network host \
        --read-only \
        --security-opt no-new-privileges:true \
        --cap-drop ALL \
        --tmpfs /tmp:size=64m \
        "${base_args[@]}"
      ;;
    bridge)
      printf '%s\0' \
        --read-only \
        --security-opt no-new-privileges:true \
        --cap-drop ALL \
        --tmpfs /tmp:size=64m \
        -p "${BIND_HOST}:${HTTP_PORT}:8080" \
        -p "${BIND_HOST}:${GRPC_PORT}:9090" \
        "${base_args[@]}"
      ;;
    *)
      return 1
      ;;
  esac
}

run_container() {
  local mode="$1"
  local -a runtime_args
  mapfile -d '' runtime_args < <(build_runtime_args "${mode}")
  local stdout_file stderr_file
  stdout_file="$(mktemp)"
  stderr_file="$(mktemp)"
  if docker run -d \
    --name "${CONTAINER_NAME}" \
    "${runtime_args[@]}" \
    "${IMAGE}" >"${stdout_file}" 2>"${stderr_file}"; then
    RUN_STDOUT="${stdout_file}"
    RUN_STDERR="${stderr_file}"
    return 0
  fi
  RUN_STDOUT="${stdout_file}"
  RUN_STDERR="${stderr_file}"
  return 1
}

cleanup_run_files() {
  rm -f "${RUN_STDOUT:-}" "${RUN_STDERR:-}"
}

trap cleanup_run_files EXIT

case "${NETWORK_MODE}" in
  host)
    run_container host
    ;;
  bridge)
    run_container bridge
    ;;
  auto)
    if ! run_container bridge; then
      if grep -qiE "failed to set up container networking|operation not supported|failed to create endpoint|veth" "${RUN_STDERR}"; then
        docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
        run_container host
      else
        cat "${RUN_STDERR}" >&2
        exit 1
      fi
    fi
    ;;
  *)
    echo "unsupported SRE_DOCKER_NETWORK_MODE: ${NETWORK_MODE}" >&2
    exit 1
    ;;
esac

for _ in $(seq 1 90); do
  if curl -fsS "http://${BIND_HOST}:${HTTP_PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "http://${BIND_HOST}:${HTTP_PORT}/healthz" >/dev/null

echo "Controller is up"
echo "UI/API:  http://${BIND_HOST}:${HTTP_PORT}"
echo "Health:  http://${BIND_HOST}:${HTTP_PORT}/healthz"
echo "Fleet:   http://${BIND_HOST}:${HTTP_PORT}/api/v1/fleet"
