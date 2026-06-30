#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
MANIFEST_DIR="${ROOT_DIR}/examples/gpu-platform-sre/manifests"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="${ROOT_DIR}/build/gpu-platform-training/${STAMP}"

if [[ "${SRE_AGENT_ENABLE_REAL_GPU_TESTS:-0}" != "1" ]]; then
  cat <<MSG
Cluster writes are disabled.
Set SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 to run the training placeholder Job.
Dry-run validation: make gpu-platform-smoke
MSG
  exit 0
fi

"${ROOT_DIR}/examples/gpu-platform-sre/scripts/preflight.sh" --require-cluster
mkdir -p "${OUT_DIR}"
kubectl apply -f "${MANIFEST_DIR}/namespaces.yaml"
kubectl apply -f "${MANIFEST_DIR}/training-job.yaml"
kubectl -n ai-serving wait --for=condition=complete job/tiny-training-placeholder --timeout="${TRAINING_JOB_TIMEOUT:-5m}"
kubectl -n ai-serving logs job/tiny-training-placeholder >"${OUT_DIR}/training-job.log"
kubectl -n ai-serving get job tiny-training-placeholder -o yaml >"${OUT_DIR}/training-job.yaml"
echo "training placeholder evidence: ${OUT_DIR}"
