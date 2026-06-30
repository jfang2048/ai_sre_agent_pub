#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${SRE_MANIFEST_VALIDATE_OUT_DIR:-${ROOT_DIR}/build/manifests}"
SKIP_KUSTOMIZE=0
if [[ "${1:-}" == "--skip-kustomize" ]]; then
  SKIP_KUSTOMIZE=1
fi
mkdir -p "${OUT_DIR}"

if ! command -v helm >/dev/null 2>&1; then
  echo "helm is required for manifest validation" >&2
  exit 1
fi
CHART_DIR="${ROOT_DIR}/deploy/charts/sre-agent"

render_chart() {
  local name="$1"
  shift || true
  helm lint "${CHART_DIR}" "$@"
  helm template "sre-agent-${name}" "${CHART_DIR}" "$@" >"${OUT_DIR}/helm-${name}.yaml"
}

render_chart default
render_chart local-dev -f "${CHART_DIR}/examples/local-dev-values.yaml"
render_chart cluster-lite -f "${CHART_DIR}/examples/cluster-lite-values.yaml"
render_chart production-like -f "${CHART_DIR}/examples/production-like-values.yaml"
render_chart distributed -f "${CHART_DIR}/examples/distributed-values.yaml"
render_chart shared-state -f "${CHART_DIR}/examples/shared-state-values.yaml"
render_chart secure-transport -f "${CHART_DIR}/examples/secure-transport-values.yaml"
render_chart reduced-privilege -f "${CHART_DIR}/examples/reduced-privilege-collector-values.yaml"

if [[ "${SKIP_KUSTOMIZE}" -eq 0 ]]; then
  if command -v kubectl >/dev/null 2>&1 && kubectl kustomize --help >/dev/null 2>&1; then
    kubectl kustomize "${ROOT_DIR}/deploy/k8s/push-first" >"${OUT_DIR}/kustomize-rendered.yaml"
  elif command -v kustomize >/dev/null 2>&1; then
    kustomize build "${ROOT_DIR}/deploy/k8s/push-first" >"${OUT_DIR}/kustomize-rendered.yaml"
  else
    echo "kubectl with kustomize support or standalone kustomize is required for manifest validation" >&2
    exit 1
  fi
fi

echo "manifest validation ok"
