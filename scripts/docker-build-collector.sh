#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/version.sh"
DOCKERFILE="${DOCKERFILE:-${ROOT_DIR}/deploy/docker/Dockerfile.collector}"
VERSION="${SRE_VERSION:-$(repo_version "${ROOT_DIR}")}"
COMMIT="${SRE_BUILD_COMMIT:-$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo dev)}"
BUILD_DATE="${SRE_BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
IMAGE="${SRE_COLLECTOR_IMAGE:-ai-sre-agent/collector:${VERSION}}"
BUILD_NETWORK="${DOCKER_BUILD_NETWORK:-}"
REPO_URL="${REPO_URL:-}"
REPO_REF="${REPO_REF:-}"
NO_CACHE=""

usage() {
  cat <<USAGE
usage: ./scripts/docker-build-collector.sh [--no-cache]

Build the collector image.

Environment:
  DOCKERFILE           Path to Dockerfile (default: deploy/docker/Dockerfile.collector)
  SRE_COLLECTOR_IMAGE  Image tag (default: ai-sre-agent/collector:\${SRE_VERSION})
  REPO_URL             Optional Git repository URL used inside the Docker build
  REPO_REF             Optional branch/tag/commit fetched from REPO_URL
  DOCKER_BUILD_NETWORK Optional docker build network mode (for example: host)
USAGE
}

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
      echo "unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi

build_cmd=(docker build)
if docker buildx version >/dev/null 2>&1; then
  build_cmd=(docker buildx build --load)
fi

build_args=()
if [[ -n "${NO_CACHE}" ]]; then
  build_args+=("${NO_CACHE}")
fi
if [[ -n "${BUILD_NETWORK}" ]]; then
  build_args+=("--network" "${BUILD_NETWORK}")
fi

run_build() {
  if "${build_cmd[@]}" "${build_args[@]}" \
    -f "${DOCKERFILE}" \
    --build-arg VERSION="${VERSION}" \
    --build-arg COMMIT="${COMMIT}" \
    --build-arg BUILD_DATE="${BUILD_DATE}" \
    --build-arg REPO_URL="${REPO_URL}" \
    --build-arg REPO_REF="${REPO_REF}" \
    -t "${IMAGE}" \
    "${ROOT_DIR}"; then
    return 0
  fi

  if [[ -n "${BUILD_NETWORK}" ]]; then
    return 1
  fi

  echo "standard docker build failed; retrying collector with --network host" >&2
  "${build_cmd[@]}" "${build_args[@]}" --network host \
    -f "${DOCKERFILE}" \
    --build-arg VERSION="${VERSION}" \
    --build-arg COMMIT="${COMMIT}" \
    --build-arg BUILD_DATE="${BUILD_DATE}" \
    --build-arg REPO_URL="${REPO_URL}" \
    --build-arg REPO_REF="${REPO_REF}" \
    -t "${IMAGE}" \
    "${ROOT_DIR}"
}

echo "Building ${IMAGE}"
run_build
echo "collector image build complete"
