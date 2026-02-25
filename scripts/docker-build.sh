#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE="${DOCKERFILE:-${ROOT_DIR}/deploy/docker/Dockerfile}"

COLLECTOR_IMAGE="${COLLECTOR_IMAGE:-sre-collector:latest}"
CONTROLLER_IMAGE="${CONTROLLER_IMAGE:-sre-controller:latest}"

usage() {
  cat <<'EOF'
Usage:
  ./scripts/docker-build.sh [--no-cache]

Builds both images from deploy/docker/Dockerfile:
  - collector target -> sre-collector:latest
  - controller target -> sre-controller:latest

Environment overrides:
  DOCKERFILE          Path to Dockerfile (default: deploy/docker/Dockerfile)
  COLLECTOR_IMAGE     Collector image tag (default: sre-collector:latest)
  CONTROLLER_IMAGE    Controller image tag (default: sre-controller:latest)
EOF
}

NO_CACHE=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-cache)
      NO_CACHE="--no-cache"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1"
      usage
      exit 1
      ;;
  esac
done

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required"
  exit 1
fi

echo "Building collector image: ${COLLECTOR_IMAGE}"
docker build ${NO_CACHE} \
  -f "${DOCKERFILE}" \
  --target collector \
  -t "${COLLECTOR_IMAGE}" \
  "${ROOT_DIR}"

echo "Building controller image: ${CONTROLLER_IMAGE}"
docker build ${NO_CACHE} \
  -f "${DOCKERFILE}" \
  --target controller \
  -t "${CONTROLLER_IMAGE}" \
  "${ROOT_DIR}"

echo "Done."
