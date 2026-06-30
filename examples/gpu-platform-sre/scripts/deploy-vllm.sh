#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
MANIFEST_DIR="${ROOT_DIR}/examples/gpu-platform-sre/manifests"

if [[ "${SRE_AGENT_ENABLE_REAL_GPU_TESTS:-0}" != "1" ]]; then
  cat <<MSG
Real GPU deployment is disabled.
Set SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 to deploy vLLM.
Dry-run validation: make gpu-platform-validate
MSG
  exit 0
fi

"${ROOT_DIR}/examples/gpu-platform-sre/scripts/preflight.sh" --require-cluster --require-gpu
kubectl apply -f "${MANIFEST_DIR}/namespaces.yaml"
kubectl apply -f "${MANIFEST_DIR}/vllm-deployment.yaml"
kubectl apply -f "${MANIFEST_DIR}/vllm-service.yaml"
if kubectl get crd podmonitors.monitoring.coreos.com >/dev/null 2>&1; then
  kubectl apply -f "${MANIFEST_DIR}/podmonitor-vllm.yaml"
else
  echo "PodMonitor CRD missing; vLLM metrics scrape not applied"
fi
kubectl -n ai-serving rollout status deployment/vllm-openai --timeout="${VLLM_ROLLOUT_TIMEOUT:-10m}"
