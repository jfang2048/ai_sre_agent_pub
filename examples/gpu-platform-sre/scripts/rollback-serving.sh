#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
MANIFEST_DIR="${ROOT_DIR}/examples/gpu-platform-sre/manifests"

if [[ "${SRE_AGENT_ENABLE_REAL_GPU_TESTS:-0}" != "1" ]]; then
  cat <<MSG
Rollback is disabled because real cluster writes are disabled.
Set SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 to run rollback commands.
MSG
  exit 0
fi

"${ROOT_DIR}/examples/gpu-platform-sre/scripts/preflight.sh" --require-cluster

if kubectl -n ai-serving get deployment vllm-openai >/dev/null 2>&1; then
  kubectl -n ai-serving rollout undo deployment/vllm-openai || true
  kubectl -n ai-serving rollout status deployment/vllm-openai --timeout="${VLLM_ROLLBACK_TIMEOUT:-5m}" || true
fi
kubectl -n ai-serving delete pod vllm-too-many-gpus --ignore-not-found=true
if kubectl get crd podmonitors.monitoring.coreos.com >/dev/null 2>&1; then
  kubectl apply -f "${MANIFEST_DIR}/podmonitor-vllm.yaml" || true
fi
"${ROOT_DIR}/examples/gpu-platform-sre/scripts/collect-evidence.sh" rollback
