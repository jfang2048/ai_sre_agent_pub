#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
usage: ./scripts/docker-build.sh [--no-cache]

Build both canonical images:
  - controller
  - collector

Use REPO_URL / REPO_REF to force the Docker build to clone a different fork/ref.
USAGE
}

ARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-cache)
      ARGS+=("$1")
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

"${ROOT_DIR}/scripts/docker-build-controller.sh" "${ARGS[@]}"
"${ROOT_DIR}/scripts/docker-build-collector.sh" "${ARGS[@]}"
