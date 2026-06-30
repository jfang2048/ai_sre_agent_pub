#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
MANIFEST_DIR="${ROOT_DIR}/examples/gpu-platform-sre/manifests"

if [[ "${SRE_AGENT_ENABLE_REAL_GPU_TESTS:-0}" != "1" ]]; then
  cat <<MSG
Real GPU deployment is disabled.
Set SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 to deploy the optional KServe vLLM InferenceService.
Dry-run validation: make gpu-platform-validate
MSG
  exit 0
fi

"${ROOT_DIR}/examples/gpu-platform-sre/scripts/preflight.sh" --require-cluster --require-gpu --require-kserve
kubectl apply -f "${MANIFEST_DIR}/namespaces.yaml"
kubectl apply -f "${MANIFEST_DIR}/kserve-vllm-inferenceservice.yaml"
kubectl -n ai-serving get inferenceservice vllm-openai
