#!/usr/bin/env bash
set -euo pipefail

INFLUX_URL="${INFLUX_URL:-http://127.0.0.1:8086}"
INFLUX_ORG="${INFLUX_ORG:-ai-sre-agent}"
INFLUX_TOKEN="${INFLUX_TOKEN:-}"
BACKUP_PATH="${1:-}"
PORTABLE_FLAG="${INFLUX_BACKUP_PORTABLE:-1}"

if ! command -v influx >/dev/null 2>&1; then
  echo "influx CLI not found in PATH" >&2
  exit 1
fi

if [[ -z "${BACKUP_PATH}" ]]; then
  echo "usage: $0 <backup-path>" >&2
  exit 1
fi

if [[ ! -d "${BACKUP_PATH}" ]]; then
  echo "backup path does not exist: ${BACKUP_PATH}" >&2
  exit 1
fi

if [[ -z "${INFLUX_TOKEN}" ]]; then
  echo "INFLUX_TOKEN is required" >&2
  exit 1
fi

args=(restore "${BACKUP_PATH}" --host "${INFLUX_URL}" --org "${INFLUX_ORG}" --token "${INFLUX_TOKEN}")
if [[ "${PORTABLE_FLAG}" == "1" || "${PORTABLE_FLAG}" == "true" ]]; then
  args+=(--portable)
fi

echo "running influx ${args[*]}"
influx "${args[@]}"
echo "restore complete from ${BACKUP_PATH}"
