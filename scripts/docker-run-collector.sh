#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/version.sh"
IMAGE="${SRE_COLLECTOR_IMAGE:-ai-sre-agent/collector:${SRE_VERSION:-$(repo_version "${ROOT_DIR}")}}"
CONTAINER_NAME="${SRE_COLLECTOR_CONTAINER:-ai-sre-agent-collector}"
CONFIG_FILE="${SRE_COLLECTOR_CONFIG_FILE:-}"
DATA_DIR="${SRE_COLLECTOR_DATA_DIR:-}"
VOLUME_NAME="${SRE_COLLECTOR_VOLUME:-ai-sre-agent-collector-data}"
METRICS_PORT="${SRE_COLLECTOR_METRICS_PORT:-9464}"
METRICS_BIND_HOST="${SRE_COLLECTOR_METRICS_BIND_HOST:-127.0.0.1}"
NETWORK_MODE="${SRE_DOCKER_NETWORK_MODE:-bridge}"
HOST_OBSERVER=0
SKIP_BUILD=0
RUN_STDOUT=""
RUN_STDERR=""

usage() {
  cat <<'USAGE'
usage: ./scripts/docker-run-collector.sh [--skip-build] [--host-observer] [--config-file <path>]

Run only the collector container.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --host-observer)
      HOST_OBSERVER=1
      shift
      ;;
    --config-file)
      CONFIG_FILE="$2"
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
  "${ROOT_DIR}/scripts/docker-build-collector.sh"
fi

docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true

volume_args=()
if [[ -n "${DATA_DIR}" ]]; then
  mkdir -p "${DATA_DIR}"
  volume_args+=(-v "${DATA_DIR}:/var/lib/ai-sre-agent/collector/data")
else
  docker volume create "${VOLUME_NAME}" >/dev/null
  volume_args+=(-v "${VOLUME_NAME}:/var/lib/ai-sre-agent/collector/data")
fi

config_args=()
if [[ -n "${CONFIG_FILE}" ]]; then
  if [[ ! -f "${CONFIG_FILE}" ]]; then
    echo "collector config file not found: ${CONFIG_FILE}" >&2
    exit 1
  fi
  config_args+=(-v "${CONFIG_FILE}:/etc/ai-sre-agent/collector.yaml:ro")
fi

base_args=(
  --restart unless-stopped
  -e SRE_COLLECTOR_CONFIG=/etc/ai-sre-agent/collector.yaml
  -e SRE_COLLECTOR_CONTROLLER_ENDPOINTS="${SRE_COLLECTOR_CONTROLLER_ENDPOINTS:-}"
  -e SRE_COLLECTOR_ID="${SRE_COLLECTOR_ID:-}"
  -e SRE_COLLECTOR_HOSTNAME="${SRE_COLLECTOR_HOSTNAME:-}"
  -e SRE_COLLECTOR_COLLECTION_INTERVAL="${SRE_COLLECTOR_COLLECTION_INTERVAL:-}"
  -e SRE_COLLECTOR_LEVEL="${SRE_COLLECTOR_LEVEL:-}"
  -e SRE_COLLECTOR_METRICS_ADDR=:9464
  "${volume_args[@]}"
  "${config_args[@]}"
)

if [[ "${HOST_OBSERVER}" == "1" ]]; then
  base_args=(
    --restart unless-stopped
    --read-only
    --security-opt no-new-privileges:true
    --security-opt seccomp:unconfined
    --cap-add BPF
    --cap-add PERFMON
    --cap-add NET_ADMIN
    --cap-add SYS_RESOURCE
    --pid host
    --user 0:0
    --tmpfs /tmp:size=64m
    -v /sys:/sys:ro
    -v /sys/kernel/debug:/sys/kernel/debug
    -v /lib/modules:/lib/modules:ro
    -v /var/log:/var/log:ro
    -v /sys/fs/bpf:/sys/fs/bpf
    -e SRE_COLLECTOR_LOG_PATHS="${SRE_COLLECTOR_LOG_PATHS:-/var/log/syslog,/var/log/messages,/var/log/kern.log}"
    -e SRE_COLLECTOR_CONFIG=/etc/ai-sre-agent/collector.yaml
    -e SRE_COLLECTOR_CONTROLLER_ENDPOINTS="${SRE_COLLECTOR_CONTROLLER_ENDPOINTS:-}"
    -e SRE_COLLECTOR_ID="${SRE_COLLECTOR_ID:-}"
    -e SRE_COLLECTOR_HOSTNAME="${SRE_COLLECTOR_HOSTNAME:-}"
    -e SRE_COLLECTOR_COLLECTION_INTERVAL="${SRE_COLLECTOR_COLLECTION_INTERVAL:-}"
    -e SRE_COLLECTOR_LEVEL="${SRE_COLLECTOR_LEVEL:-}"
    -e SRE_COLLECTOR_METRICS_ADDR=:9464
    "${volume_args[@]}"
    "${config_args[@]}"
  )
fi

build_runtime_args() {
  local mode="$1"
  if [[ "${HOST_OBSERVER}" == "1" ]]; then
    case "${mode}" in
      host)
        printf '%s\0' --network host "${base_args[@]}"
        ;;
      bridge)
        printf '%s\0' -p "${METRICS_BIND_HOST}:${METRICS_PORT}:9464" "${base_args[@]}"
        ;;
      *)
        return 1
        ;;
    esac
    return 0
  fi

  case "${mode}" in
    host)
      printf '%s\0' --network host "${base_args[@]}"
      ;;
    bridge)
      printf '%s\0' \
        --read-only \
        --security-opt no-new-privileges:true \
        --cap-drop ALL \
        --tmpfs /tmp:size=64m \
        -p "${METRICS_BIND_HOST}:${METRICS_PORT}:9464" \
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

for _ in $(seq 1 60); do
  if curl -fsS "http://${METRICS_BIND_HOST}:${METRICS_PORT}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
curl -fsS "http://${METRICS_BIND_HOST}:${METRICS_PORT}/healthz" >/dev/null

echo "Collector is up"
echo "Metrics: http://${METRICS_BIND_HOST}:${METRICS_PORT}/metrics"
