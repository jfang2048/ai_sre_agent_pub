#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=1
fi
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="${ROOT_DIR}/build/gpu-platform-smoke/${STAMP}"
mkdir -p "${OUT_DIR}"

summary="${OUT_DIR}/summary.json"
printf '{\n  "timestamp": "%s",\n  "dry_run": %s,\n' "${STAMP}" "${DRY_RUN}" >"${summary}"

# Static smoke: required execution files exist.
required=(
  "examples/gpu-platform-sre/manifests/vllm-deployment.yaml"
  "examples/gpu-platform-sre/manifests/vllm-service.yaml"
  "examples/gpu-platform-sre/scripts/collect-evidence.sh"
)
for rel in "${required[@]}"; do
  [[ -s "${ROOT_DIR}/${rel}" ]] || { echo "missing ${rel}" >&2; exit 1; }
done

if [[ "${DRY_RUN}" -eq 1 ]]; then
  cat >>"${summary}" <<JSON
  "cluster": "skipped",
  "status": "dry-run smoke passed; no GPU or Kubernetes execution attempted",
  "model_name": "skipped",
  "pod_name": "skipped",
  "gpu_allocation_state": "skipped"
}
JSON
  echo "dry-run smoke evidence: ${OUT_DIR}"
  exit 0
fi

if ! command -v kubectl >/dev/null 2>&1 || ! kubectl version --request-timeout=5s >/dev/null 2>&1; then
  cat >>"${summary}" <<JSON
  "cluster": "missing",
  "status": "skipped: kubectl or reachable cluster unavailable"
}
JSON
  echo "cluster unavailable; smoke evidence: ${OUT_DIR}"
  exit 0
fi

kubectl -n ai-serving get pods -l app.kubernetes.io/name=vllm -o wide >"${OUT_DIR}/vllm-pods.txt" 2>&1 || true
kubectl -n ai-serving get pods -l app.kubernetes.io/name=vllm -o json >"${OUT_DIR}/vllm-pods.json" 2>&1 || true
if ! kubectl -n ai-serving get service vllm-openai >/dev/null 2>&1; then
  cat >>"${summary}" <<JSON
  "cluster": "reachable",
  "status": "skipped: service ai-serving/vllm-openai not found"
}
JSON
  echo "vLLM service missing; smoke evidence: ${OUT_DIR}"
  exit 0
fi

if ! command -v curl >/dev/null 2>&1; then
  cat >>"${summary}" <<JSON
  "cluster": "reachable",
  "status": "skipped: curl not installed"
}
JSON
  echo "curl missing; smoke evidence: ${OUT_DIR}"
  exit 0
fi

port="${VLLM_LOCAL_PORT:-18080}"
kubectl -n ai-serving port-forward svc/vllm-openai "${port}:8000" >"${OUT_DIR}/port-forward.log" 2>&1 &
pf_pid=$!
cleanup() { kill "${pf_pid}" >/dev/null 2>&1 || true; }
trap cleanup EXIT
sleep 3
start_ms="$(date +%s%3N)"
status_code="$(curl -sS -o "${OUT_DIR}/models.json" -w '%{http_code}' "http://127.0.0.1:${port}/v1/models" || true)"
end_ms="$(date +%s%3N)"
latency_ms=$((end_ms - start_ms))
model_name="unavailable"
pod_name="unavailable"
gpu_allocation_state="unavailable"
if command -v python3 >/dev/null 2>&1; then
  model_name="$(python3 - "${OUT_DIR}/models.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1], encoding='utf-8'))
    models = data.get('data') if isinstance(data, dict) else []
    first = models[0].get('id') if models and isinstance(models[0], dict) else 'unavailable'
    print(first or 'unavailable')
except Exception:
    print('unavailable')
PY
)"
  read -r pod_name gpu_allocation_state < <(python3 - "${OUT_DIR}/vllm-pods.json" <<'PY'
import json, sys
try:
    data = json.load(open(sys.argv[1], encoding='utf-8'))
    items = data.get('items') if isinstance(data, dict) else []
    if not items:
        print('unavailable unavailable')
        raise SystemExit
    pod = items[0]
    name = pod.get('metadata', {}).get('name') or 'unavailable'
    limits = []
    for container in pod.get('spec', {}).get('containers', []):
        value = container.get('resources', {}).get('limits', {}).get('nvidia.com/gpu')
        if value is not None:
            limits.append(f"{container.get('name','container')}={value}")
    print(name, ','.join(limits) if limits else 'none')
except Exception:
    print('unavailable unavailable')
PY
)
fi
python3 - "${summary}" "${STAMP}" "${DRY_RUN}" "${status_code}" "${latency_ms}" "${model_name}" "${pod_name}" "${gpu_allocation_state}" "${OUT_DIR}/models.json" "${OUT_DIR}/vllm-pods.txt" <<'PY'
import json, sys
summary, stamp, dry_run, status_code, latency_ms, model_name, pod_name, gpu_state, models_path, pods_path = sys.argv[1:]
payload = {
    'timestamp': stamp,
    'dry_run': bool(int(dry_run)),
    'cluster': 'reachable',
    'status_code': status_code,
    'latency_ms': int(latency_ms),
    'model_name': model_name,
    'pod_name': pod_name,
    'gpu_allocation_state': gpu_state,
    'model_endpoint_output': models_path,
    'pod_state_output': pods_path,
}
with open(summary, 'w', encoding='utf-8') as fh:
    json.dump(payload, fh, indent=2)
    fh.write('\n')
PY
echo "smoke evidence: ${OUT_DIR}"
