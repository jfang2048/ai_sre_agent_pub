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

CONTROLLER_BIN="${SRE_CONTROLLER_BIN:-}"
if [[ -z "${CONTROLLER_BIN}" ]]; then
  CONTROLLER_BIN="$(resolve_first_executable \
    "${ROOT_DIR}/build/sre-controller" \
    "/usr/local/bin/sre-controller" \
  )" || {
    echo "controller binary not found; set SRE_CONTROLLER_BIN or run make build" >&2
    exit 1
  }
fi

CONTROLLER_CONFIG="${SRE_CONTROLLER_CONFIG:-}"
if [[ -z "${CONTROLLER_CONFIG}" ]]; then
  CONTROLLER_CONFIG="$(resolve_first_existing \
    "${ROOT_DIR}/configs/controller.yaml" \
    "/etc/ai-sre-agent/controller.yaml" \
    "/etc/sre-controller/config.yaml" \
  )" || {
    echo "controller config not found; set SRE_CONTROLLER_CONFIG" >&2
    exit 1
  }
fi
if [[ ! -f "${CONTROLLER_CONFIG}" ]]; then
  echo "controller config not found: ${CONTROLLER_CONFIG}" >&2
  exit 1
fi

TARGETS_FILE="${SRE_CONTROLLER_TARGETS_FILE:-${SRE_INVENTORY_TARGETS_FILE:-}}"
if [[ -z "${TARGETS_FILE}" ]]; then
  TARGETS_FILE="$(resolve_first_existing \
    "${ROOT_DIR}/configs/controller_targets.yaml" \
    "/etc/ai-sre-agent/controller_targets.yaml" \
  )" || true
fi
if [[ -n "${TARGETS_FILE}" && ! -f "${TARGETS_FILE}" ]]; then
  echo "controller targets file not found: ${TARGETS_FILE}" >&2
  exit 1
fi

if [[ -z "${SRE_CONTROLLER_WEB_PATH:-}" ]]; then
  WEB_PATH="$(resolve_first_existing \
    "${ROOT_DIR}/web/index.html" \
    "/var/lib/ai-sre-agent/controller/web/index.html" \
  )" || true
  if [[ -n "${WEB_PATH:-}" ]]; then
    export SRE_CONTROLLER_WEB_PATH="$(dirname "${WEB_PATH}")"
  fi
fi

export SRE_CONTROLLER_CONFIG="${CONTROLLER_CONFIG}"
if [[ -n "${TARGETS_FILE}" ]]; then
  export SRE_CONTROLLER_TARGETS_FILE="${TARGETS_FILE}"
  export SRE_INVENTORY_TARGETS_FILE="${TARGETS_FILE}"
fi

args=("--config" "${CONTROLLER_CONFIG}")

if [[ ${SRE_CONTROLLER_HTTP_LISTEN+x} == x ]]; then
  args+=("--listen=${SRE_CONTROLLER_HTTP_LISTEN}")
fi
if [[ ${SRE_CONTROLLER_GRPC_LISTEN+x} == x ]]; then
  args+=("--grpc-listen=${SRE_CONTROLLER_GRPC_LISTEN}")
fi
if [[ -n "${SRE_CONTROLLER_WEB_PATH:-}" ]]; then
  args+=("--web-path=${SRE_CONTROLLER_WEB_PATH}")
fi

exec "${CONTROLLER_BIN}" "${args[@]}" "$@"
