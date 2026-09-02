#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "${SCRIPT_DIR}/common.sh"

TARGET_DIR="${SRE_PUBLISH_TARGET_DIR:-${PUBLISH_ROOT_DIR}/build/publish-tree}"
CHECK_ONLY=0

usage() {
  cat <<'USAGE'
usage: scripts/publish/prepare_public_tree.sh [options]

options:
  --target-dir <dir>  Destination directory for the prepared public tree.
  --check-only        Run publish audits against the current working tree copy in target-dir.
  --help              Show this message.
USAGE
  publish_usage_common
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target-dir)
      TARGET_DIR="$2"
      shift 2
      ;;
    --check-only)
      CHECK_ONLY=1
      shift
      ;;
    --help|-h)
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

if [[ "${CHECK_ONLY}" == "1" ]]; then
  publish_history_audit
  publish_file_audit "${TARGET_DIR}"
  publish_secret_name_audit "${TARGET_DIR}"
  publish_sensitive_content_audit "${TARGET_DIR}"
  echo "publish tree audit passed: ${TARGET_DIR}"
  exit 0
fi

publish_prepare_tree "${TARGET_DIR}"
echo "prepared public tree: ${TARGET_DIR}"
