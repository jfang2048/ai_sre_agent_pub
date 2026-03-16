#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

resolve_first_existing() {
  local path
  for path in "$@"; do
    if [[ -n "${path}" && -f "${path}" ]]; then
      printf '%s\n' "${path}"
      return 0
    fi
  done
  return 1
}

resolve_first_executable() {
  local path
  for path in "$@"; do
    if [[ -n "${path}" && -x "${path}" ]]; then
      printf '%s\n' "${path}"
      return 0
    fi
  done
  return 1
}

COLLECTOR_BIN="${SRE_COLLECTOR_BIN:-}"
if [[ -z "${COLLECTOR_BIN}" ]]; then
  COLLECTOR_BIN="$(resolve_first_executable \
    "${ROOT_DIR}/build/sre-collector" \
    "/usr/local/bin/sre-collector" \
  )" || {
    echo "collector binary not found; set SRE_COLLECTOR_BIN or run make build" >&2
    exit 1
  }
fi

COLLECTOR_CONFIG="${SRE_COLLECTOR_CONFIG:-}"
if [[ -z "${COLLECTOR_CONFIG}" ]]; then
  COLLECTOR_CONFIG="$(resolve_first_existing \
    "${ROOT_DIR}/configs/collector.yaml" \
    "/etc/ai-sre-agent/collector.yaml" \
    "/etc/sre-collector/config.yaml" \
  )" || {
    echo "collector config not found; set SRE_COLLECTOR_CONFIG" >&2
    exit 1
  }
fi
if [[ ! -f "${COLLECTOR_CONFIG}" ]]; then
  echo "collector config not found: ${COLLECTOR_CONFIG}" >&2
  exit 1
fi

if [[ -z "${SRE_COLLECTOR_PROBE_CORE_BINARY_PATH:-}" ]]; then
  if PROBE_CORE_BIN="$(resolve_first_executable \
    "${ROOT_DIR}/build/sre-probe-core" \
    "/usr/local/bin/sre-probe-core" \
  )"; then
    export SRE_COLLECTOR_PROBE_CORE_BINARY_PATH="${PROBE_CORE_BIN}"
  fi
fi

export SRE_COLLECTOR_CONFIG="${COLLECTOR_CONFIG}"

args=("--config" "${COLLECTOR_CONFIG}")

if [[ -n "${SRE_COLLECTOR_COLLECTION_INTERVAL:-}" ]]; then
  args+=("--interval=${SRE_COLLECTOR_COLLECTION_INTERVAL}")
fi
if [[ -n "${SRE_COLLECTOR_LEVEL:-}" ]]; then
  args+=("--level=${SRE_COLLECTOR_LEVEL}")
fi
if [[ ${SRE_COLLECTOR_METRICS_ADDR+x} == x ]]; then
  args+=("--metrics-listen=${SRE_COLLECTOR_METRICS_ADDR}")
fi
if [[ -n "${SRE_COLLECTOR_CONTROLLER_ENDPOINTS:-}" ]]; then
  IFS=',' read -r -a collector_endpoints <<< "${SRE_COLLECTOR_CONTROLLER_ENDPOINTS}"
  for endpoint in "${collector_endpoints[@]}"; do
    endpoint="${endpoint#"${endpoint%%[![:space:]]*}"}"
    endpoint="${endpoint%"${endpoint##*[![:space:]]}"}"
    if [[ -n "${endpoint}" ]]; then
      args+=("--endpoint=${endpoint}")
    fi
  done
fi

exec "${COLLECTOR_BIN}" "${args[@]}" "$@"
