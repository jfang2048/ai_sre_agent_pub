#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DRILL="${1:-}"

usage() {
  echo "usage: $0 {bad-rollout|gpu-scheduling-failure|telemetry-degradation}" >&2
}

if [[ -z "${DRILL}" ]]; then usage; exit 2; fi

if [[ "${SRE_AGENT_ENABLE_REAL_GPU_TESTS:-0}" != "1" ]]; then
  cat <<MSG
Incident injection is disabled.
Set SRE_AGENT_ENABLE_REAL_GPU_TESTS=1 to mutate the demo cluster.
Requested drill: ${DRILL}
MSG
  exit 0
fi

"${ROOT_DIR}/examples/gpu-platform-sre/scripts/preflight.sh" --require-cluster

case "${DRILL}" in
  bad-rollout)
    kubectl -n ai-serving set image deployment/vllm-openai vllm=vllm/vllm-openai:no-such-tag-gpu-platform-drill --record=false
    kubectl -n ai-serving rollout status deployment/vllm-openai --timeout=60s || true
    ;;
  gpu-scheduling-failure)
    kubectl -n ai-serving apply -f - <<'YAML'
apiVersion: v1
kind: Pod
metadata:
  name: vllm-too-many-gpus
  labels:
    app.kubernetes.io/name: vllm
    app.kubernetes.io/part-of: gpu-platform-sre-demo
    sre-agent.io/workload-kind: model-serving
    sre-agent.io/model-runtime: vllm
    sre-agent.io/drill: gpu-scheduling-failure
spec:
  restartPolicy: Never
  containers:
    - name: pending-gpu-request
      image: registry.k8s.io/pause:3.9
      resources:
        limits:
          nvidia.com/gpu: "999"
YAML
    sleep 5
    kubectl -n ai-serving describe pod vllm-too-many-gpus || true
    ;;
  telemetry-degradation)
    if ! kubectl get crd podmonitors.monitoring.coreos.com >/dev/null 2>&1; then
      echo "PodMonitor CRD missing; cannot run telemetry degradation drill" >&2
      exit 1
    fi
    kubectl -n ai-serving patch podmonitor vllm --type merge -p '{"spec":{"selector":{"matchLabels":{"gpu-platform-sre.io/telemetry-drill":"missing"}}}}'
    ;;
  *) usage; exit 2 ;;
esac

"${ROOT_DIR}/examples/gpu-platform-sre/scripts/collect-evidence.sh" "${DRILL}"
