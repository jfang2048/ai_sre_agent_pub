#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${SRE_MANIFEST_VALIDATE_OUT_DIR:-${ROOT_DIR}/build/manifests}"
mkdir -p "${OUT_DIR}"

if ! command -v helm >/dev/null 2>&1; then
  echo "helm is required for manifest validation" >&2
  exit 1
fi
if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is required for manifest validation" >&2
  exit 1
fi

helm lint "${ROOT_DIR}/deploy/charts/sre-agent"
helm template sre-agent "${ROOT_DIR}/deploy/charts/sre-agent" >"${OUT_DIR}/helm-rendered.yaml"
kubectl kustomize "${ROOT_DIR}/deploy/k8s/push-first" >"${OUT_DIR}/kustomize-rendered.yaml"

echo "manifest validation ok"
