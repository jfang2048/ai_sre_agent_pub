#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Backward-compatible shim. Keep this path for existing automation.
exec "${ROOT_DIR}/scripts/smoke-local.sh" "$@"
