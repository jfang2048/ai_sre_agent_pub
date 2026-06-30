#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
EXAMPLE_DIR="${ROOT_DIR}/examples/gpu-platform-sre"
MANIFEST_DIR="${EXAMPLE_DIR}/manifests"
fail=0

ok() { printf '[gpu-platform-validate] OK: %s\n' "$*"; }
warn() { printf '[gpu-platform-validate] SKIP: %s\n' "$*"; }
err() { printf '[gpu-platform-validate] ERROR: %s\n' "$*" >&2; fail=1; }

required=(
  README.md
  manifests/namespaces.yaml manifests/sre-agent-values.example.yaml manifests/servicemonitor-sre-agent.yaml manifests/podmonitor-vllm.yaml
  manifests/prometheusrules-sre-agent.yaml manifests/otel-collector-config.yaml manifests/vllm-deployment.yaml manifests/vllm-service.yaml
  manifests/kserve-vllm-inferenceservice.yaml manifests/training-job.yaml
  scripts/preflight.sh scripts/deploy-observability.sh scripts/deploy-agent.sh scripts/deploy-vllm.sh scripts/deploy-kserve-vllm.sh
  scripts/run-training-job.sh scripts/run-smoke.sh scripts/inject-incident.sh scripts/rollback-serving.sh scripts/collect-evidence.sh scripts/validate-all.sh
)

for rel in "${required[@]}"; do
  path="${EXAMPLE_DIR}/${rel}"
  [[ -s "${path}" ]] && ok "${rel}" || err "missing or empty ${rel}"
done

for md in "${EXAMPLE_DIR}"/*.md; do
  [[ -f "${md}" ]] || continue
  grep -q '^#' "${md}" || err "markdown missing heading: ${md#${ROOT_DIR}/}"
done

for script in "${EXAMPLE_DIR}/scripts"/*.sh; do
  bash -n "${script}" && ok "bash -n ${script#${ROOT_DIR}/}" || err "bash syntax failed: ${script#${ROOT_DIR}/}"
  [[ -x "${script}" ]] || err "script is not executable: ${script#${ROOT_DIR}/}"
done

mapfile -t yaml_files < <(find "${MANIFEST_DIR}" -maxdepth 1 -type f -name '*.yaml' | sort)
if command -v python3 >/dev/null 2>&1; then
  python3 - "${yaml_files[@]}" <<'PY' || fail=1
import pathlib, sys
paths = [pathlib.Path(p) for p in sys.argv[1:]]
try:
    import yaml  # type: ignore
except Exception:
    print('[gpu-platform-validate] SKIP: PyYAML not installed; using minimal YAML shape checks')
    for path in paths:
        text = path.read_text(encoding='utf-8')
        if '\t' in text:
            print(f'[gpu-platform-validate] ERROR: tab indentation in {path}', file=sys.stderr)
            sys.exit(1)
        if path.name == 'sre-agent-values.example.yaml':
            required = ['namespace:', 'controller:', 'collector:']
            missing = [r for r in required if r not in text]
            if missing:
                print(f'[gpu-platform-validate] ERROR: {path} missing {missing}', file=sys.stderr)
                sys.exit(1)
            continue
        docs = [d.strip() for d in text.split('---') if d.strip()]
        for i, doc in enumerate(docs, 1):
            if 'apiVersion:' not in doc or 'kind:' not in doc:
                print(f'[gpu-platform-validate] ERROR: {path} doc {i} missing apiVersion/kind', file=sys.stderr)
                sys.exit(1)
    sys.exit(0)
for path in paths:
    with path.open('r', encoding='utf-8') as fh:
        docs = [doc for doc in yaml.safe_load_all(fh) if doc is not None]
    if path.name == 'sre-agent-values.example.yaml':
        for key in ('namespace', 'controller', 'collector'):
            if key not in docs[0]:
                raise SystemExit(f'[gpu-platform-validate] ERROR: {path} missing {key}')
        continue
    for i, doc in enumerate(docs, 1):
        if not isinstance(doc, dict) or 'apiVersion' not in doc or 'kind' not in doc:
            raise SystemExit(f'[gpu-platform-validate] ERROR: {path} doc {i} missing apiVersion/kind')
print('[gpu-platform-validate] OK: YAML parse/shape')
PY
else
  warn "python3 not found; YAML parse reduced to grep checks"
fi

grep -q 'nvidia.com/gpu: "1"' "${MANIFEST_DIR}/vllm-deployment.yaml" || err "vLLM manifest must request one GPU"
grep -q 'sre-agent.io/model-runtime: vllm' "${MANIFEST_DIR}/vllm-deployment.yaml" || err "vLLM labels missing"
grep -q 'SRECollectorScrapeMissing' "${MANIFEST_DIR}/prometheusrules-sre-agent.yaml" || err "collector alert missing"
grep -q 'SREGPUTelemetryMissingOnGPUNode' "${MANIFEST_DIR}/prometheusrules-sre-agent.yaml" || err "GPU telemetry alert missing"
grep -q '^gpu-platform-validate:' "${ROOT_DIR}/Makefile" || err "Makefile gpu-platform-validate target missing"
grep -q '^gpu-platform-smoke:' "${ROOT_DIR}/Makefile" || err "Makefile gpu-platform-smoke target missing"
grep -q '^gpu-platform-evidence-template:' "${ROOT_DIR}/Makefile" || err "Makefile gpu-platform-evidence-template target missing"

if command -v helm >/dev/null 2>&1; then
  mkdir -p "${ROOT_DIR}/build/gpu-platform-validate"
  helm template sre-agent-gpu "${ROOT_DIR}/deploy/charts/sre-agent" -f "${MANIFEST_DIR}/sre-agent-values.example.yaml" >"${ROOT_DIR}/build/gpu-platform-validate/sre-agent-rendered.yaml" && ok "helm template sre-agent" || err "helm template failed"
else
  warn "helm not found; chart render skipped"
fi

if command -v kubectl >/dev/null 2>&1 && kubectl version --request-timeout=5s >/dev/null 2>&1; then
  kubectl apply --dry-run=server -f "${MANIFEST_DIR}/namespaces.yaml" >/dev/null && ok "server dry-run namespaces" || err "server dry-run namespaces failed"
  kubectl apply --dry-run=server -f "${MANIFEST_DIR}/vllm-deployment.yaml" -f "${MANIFEST_DIR}/vllm-service.yaml" -f "${MANIFEST_DIR}/training-job.yaml" >/dev/null && ok "server dry-run serving/training" || err "server dry-run serving/training failed"
  if kubectl get crd servicemonitors.monitoring.coreos.com podmonitors.monitoring.coreos.com prometheusrules.monitoring.coreos.com >/dev/null 2>&1; then
    kubectl apply --dry-run=server -f "${MANIFEST_DIR}/servicemonitor-sre-agent.yaml" -f "${MANIFEST_DIR}/podmonitor-vllm.yaml" -f "${MANIFEST_DIR}/prometheusrules-sre-agent.yaml" >/dev/null && ok "server dry-run monitoring CRDs" || err "server dry-run monitoring failed"
  else
    warn "Prometheus Operator CRDs missing; monitoring server dry-run skipped"
  fi
  if kubectl get crd inferenceservices.serving.kserve.io >/dev/null 2>&1; then
    kubectl apply --dry-run=server -f "${MANIFEST_DIR}/kserve-vllm-inferenceservice.yaml" >/dev/null && ok "server dry-run KServe" || err "server dry-run KServe failed"
  else
    warn "KServe CRD missing; KServe server dry-run skipped"
  fi
else
  warn "kubectl or reachable cluster missing; server dry-run skipped"
fi

if [[ "${fail}" -ne 0 ]]; then
  exit 1
fi
ok "GPU platform SRE demo validation complete"
