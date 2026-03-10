#!/usr/bin/env bash
set -euo pipefail

INFLUX_URL="${INFLUX_URL:-http://127.0.0.1:8086}"
INFLUX_ORG="${INFLUX_ORG:-ai-sre-agent}"
INFLUX_TOKEN="${INFLUX_TOKEN:-}"
BACKUP_ROOT="${BACKUP_DIR:-${SRE_TSDB_BACKUP_DIRECTORY:-./data/backups/influx}}"
BACKUP_NAME="${BACKUP_NAME:-$(date -u +%Y%m%dT%H%M%SZ)}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
PORTABLE_FLAG="${INFLUX_BACKUP_PORTABLE:-1}"

if ! command -v influx >/dev/null 2>&1; then
  echo "influx CLI not found in PATH" >&2
  exit 1
fi

if [[ -z "${INFLUX_TOKEN}" ]]; then
  echo "INFLUX_TOKEN is required" >&2
  exit 1
fi

dest="${BACKUP_ROOT%/}/${BACKUP_NAME}"
mkdir -p "${dest}"

args=(backup "${dest}" --host "${INFLUX_URL}" --org "${INFLUX_ORG}" --token "${INFLUX_TOKEN}")
if [[ "${PORTABLE_FLAG}" == "1" || "${PORTABLE_FLAG}" == "true" ]]; then
  args+=(--portable)
fi

echo "running influx ${args[*]}"
influx "${args[@]}"

if [[ "${RETENTION_DAYS}" =~ ^[0-9]+$ ]] && [[ "${RETENTION_DAYS}" -gt 0 ]]; then
  find "${BACKUP_ROOT}" -mindepth 1 -maxdepth 1 -type d -mtime "+${RETENTION_DAYS}" -print -exec rm -rf {} +
fi

echo "backup complete: ${dest}"
