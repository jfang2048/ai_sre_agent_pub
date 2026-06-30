#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
VALUES="${ROOT_DIR}/examples/gpu-platform-sre/manifests/sre-agent-values.example.yaml"

if [[ "${SRE_AGENT_ENABLE_REAL_GPU_TESTS:-0}" != "1" ]]; then
  cat <<MSG
Real cluster writes are disabled.
Set SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 to deploy the SRE Agent Helm chart.
Dry-run validation: make gpu-platform-validate
MSG
  exit 0
fi

"${ROOT_DIR}/examples/gpu-platform-sre/scripts/preflight.sh" --require-cluster --require-helm
kubectl apply -f "${ROOT_DIR}/examples/gpu-platform-sre/manifests/namespaces.yaml"

missing=0
if ! kubectl -n ai-sre-agent get secret sre-controller-auth >/dev/null 2>&1; then
  echo "missing secret ai-sre-agent/sre-controller-auth" >&2
  echo "example: kubectl -n ai-sre-agent create secret generic sre-controller-auth --from-literal=token-secret=\"<controller-signing-secret>\"" >&2
  missing=1
fi
if ! kubectl -n ai-sre-agent get secret sre-collector-ingest-auth >/dev/null 2>&1; then
  echo "missing secret ai-sre-agent/sre-collector-ingest-auth" >&2
  echo "example: kubectl -n ai-sre-agent create secret generic sre-collector-ingest-auth --from-literal=token=\"<controller-ingest-token>\"" >&2
  missing=1
fi
if [[ "${missing}" -ne 0 ]]; then
  echo "create required auth secrets before deploying; no insecure bypass was applied" >&2
  exit 1
fi

helm upgrade --install sre-agent "${ROOT_DIR}/deploy/charts/sre-agent" \
  --namespace ai-sre-agent \
  -f "${VALUES}" \
  --wait \
  --timeout 10m

echo "SRE Agent Helm release deployed"
