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

reject_chart() {
  local reason="$1"
  shift
  if helm template sre-agent-rejected "${CHART_DIR}" "$@" >"${OUT_DIR}/rejected.yaml" 2>"${OUT_DIR}/rejected.log"; then
    echo "expected Helm to reject: ${reason}" >&2
    exit 1
  fi
  if ! grep -Fq "${reason}" "${OUT_DIR}/rejected.log"; then
    echo "Helm rejected the chart for an unexpected reason" >&2
    cat "${OUT_DIR}/rejected.log" >&2
    exit 1
  fi
}
reject_chart 'collector.persistence.type=emptyDir is rejected' \
  -f "${CHART_DIR}/examples/cluster-lite-values.yaml" --set collector.persistence.type=emptyDir
render_chart explicit-data-loss-override \
  -f "${CHART_DIR}/examples/cluster-lite-values.yaml" \
  --set collector.persistence.type=emptyDir --set collector.persistence.allowDataLossInCluster=true
reject_chart 'distributed ingest requires PostgreSQL' \
  -f "${CHART_DIR}/examples/distributed-values.yaml" --set controller.ingestInbox.backend=bbolt
render_chart distributed-single-writer \
  -f "${CHART_DIR}/examples/distributed-values.yaml" --set controller.ingestInbox.backend=bbolt \
  --set controller.ingestInbox.singleWriter=true --set controller.replicas=1

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
