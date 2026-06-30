#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
MANIFEST_DIR="${ROOT_DIR}/examples/gpu-platform-sre/manifests"

if [[ "${SRE_AGENT_ENABLE_REAL_GPU_TESTS:-0}" != "1" ]]; then
  cat <<MSG
Real cluster writes are disabled.
Set SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 to apply observability manifests.
Dry-run validation: make gpu-platform-validate
MSG
  exit 0
fi

"${ROOT_DIR}/examples/gpu-platform-sre/scripts/preflight.sh" --require-cluster
kubectl apply -f "${MANIFEST_DIR}/namespaces.yaml"
kubectl apply -f "${MANIFEST_DIR}/otel-collector-config.yaml"

if kubectl get crd servicemonitors.monitoring.coreos.com podmonitors.monitoring.coreos.com prometheusrules.monitoring.coreos.com >/dev/null 2>&1; then
  kubectl apply -f "${MANIFEST_DIR}/servicemonitor-sre-agent.yaml"
  kubectl apply -f "${MANIFEST_DIR}/podmonitor-vllm.yaml"
  kubectl apply -f "${MANIFEST_DIR}/prometheusrules-sre-agent.yaml"
else
  echo "Prometheus Operator CRDs are missing; install them before applying ServiceMonitor/PodMonitor/PrometheusRule" >&2
  exit 1
fi

echo "observability manifests applied"
