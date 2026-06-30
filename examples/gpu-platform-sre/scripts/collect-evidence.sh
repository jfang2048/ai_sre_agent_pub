#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
EXAMPLE_DIR="${ROOT_DIR}/examples/gpu-platform-sre"

write_recap_template() {
  local file="$1"
  cat >"${file}" <<'EOF'
# GPU Platform Incident Recap

## Summary

- incident_name:
- captured_at_utc:
- status:

## Evidence

- commands: `commands.sh`
- cluster events: `kubectl-events.txt`
- pods: `pods.txt`
- descriptions: `describe.txt`
- prometheus alerts: `prometheus-alerts.json`
- SRE Agent status/workflow files: `sre-agent-*.json`

## Decision

- suspected cause:
- action taken:
- verification result:
- follow-up:
EOF
}

if [[ "${1:-}" == "--template-only" ]]; then
  out="${ROOT_DIR}/build/gpu-platform-evidence-template"
  mkdir -p "${out}"
  write_recap_template "${out}/incident-recap.md"
  echo "evidence recap template: ${out}/incident-recap.md"
  exit 0
fi

incident="${1:-manual}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
evidence_root="${SRE_GPU_PLATFORM_EVIDENCE_DIR:-${EXAMPLE_DIR}/evidence}"
out="${evidence_root}/${stamp}-${incident}"
mkdir -p "${out}"

write_missing() {
  local file="$1"
  local reason="$2"
  printf 'MISSING: %s\n' "${reason}" >"${out}/${file}"
}

cat >"${out}/commands.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
# Incident: ${incident}
# Captured: ${stamp}
# Re-run useful checks:
kubectl -n ai-serving get pods -o wide || true
kubectl -n ai-sre-agent get pods -o wide || true
kubectl -n ai-serving describe deployment/vllm-openai || true
curl -fsS "\${PROMETHEUS_URL:-http://127.0.0.1:9090}/api/v1/alerts" || true
curl -fsS "\${SRE_AGENT_URL:-http://127.0.0.1:8080}/api/v1/status" || true
EOF
chmod +x "${out}/commands.sh"

if command -v kubectl >/dev/null 2>&1 && kubectl version --request-timeout=5s >/dev/null 2>&1; then
  kubectl get events -A --sort-by=.lastTimestamp >"${out}/kubectl-events.txt" 2>&1 || write_missing kubectl-events.txt "kubectl events query failed"
  kubectl -n ai-serving get pods -o wide >"${out}/pods.txt" 2>&1 || write_missing pods.txt "ai-serving pods query failed"
  {
    kubectl -n ai-serving describe deployment/vllm-openai || true
    kubectl -n ai-serving describe pod -l app.kubernetes.io/name=vllm || true
    kubectl -n ai-sre-agent describe pods || true
  } >"${out}/describe.txt" 2>&1 || write_missing describe.txt "kubectl describe failed"
else
  write_missing kubectl-events.txt "kubectl unavailable or cluster unreachable"
  write_missing pods.txt "kubectl unavailable or cluster unreachable"
  write_missing describe.txt "kubectl unavailable or cluster unreachable"
fi

prom_url="${PROMETHEUS_URL:-}"
if [[ -n "${prom_url}" ]] && command -v curl >/dev/null 2>&1; then
  curl -fsS "${prom_url%/}/api/v1/alerts" >"${out}/prometheus-alerts.json" 2>/dev/null || printf '{"missing":"Prometheus alerts endpoint unavailable at %s"}\n' "${prom_url}" >"${out}/prometheus-alerts.json"
else
  printf '{"missing":"PROMETHEUS_URL not set or curl unavailable"}\n' >"${out}/prometheus-alerts.json"
fi

sre_url="${SRE_AGENT_URL:-http://127.0.0.1:8080}"
if command -v curl >/dev/null 2>&1; then
  curl -fsS "${sre_url%/}/api/v1/status" >"${out}/sre-agent-status.json" 2>/dev/null || printf '{"missing":"SRE Agent status unavailable at %s"}\n' "${sre_url}" >"${out}/sre-agent-status.json"
  curl -fsS "${sre_url%/}/api/v1/agent/workflow/runs?limit=20" >"${out}/sre-agent-workflow-runs.json" 2>/dev/null || printf '{"missing":"SRE Agent workflow runs unavailable at %s"}\n' "${sre_url}" >"${out}/sre-agent-workflow-runs.json"
  if [[ -n "${SRE_AGENT_RUN_ID:-}" ]]; then
    curl -fsS "${sre_url%/}/api/v1/agent/workflow/evidence/${SRE_AGENT_RUN_ID}" >"${out}/sre-agent-evidence.json" 2>/dev/null || printf '{"missing":"SRE Agent evidence unavailable for run_id %s"}\n' "${SRE_AGENT_RUN_ID}" >"${out}/sre-agent-evidence.json"
  else
    printf '{"missing":"SRE_AGENT_RUN_ID not set; no single workflow evidence package requested"}\n' >"${out}/sre-agent-evidence.json"
  fi
else
  printf '{"missing":"curl unavailable"}\n' >"${out}/sre-agent-status.json"
  printf '{"missing":"curl unavailable"}\n' >"${out}/sre-agent-workflow-runs.json"
  printf '{"missing":"curl unavailable"}\n' >"${out}/sre-agent-evidence.json"
fi

write_recap_template "${out}/incident-recap.md"
cat >>"${out}/incident-recap.md" <<EOF

## Capture metadata

- incident_name: ${incident}
- captured_at_utc: ${stamp}
- evidence_dir: ${out}
EOF

echo "evidence collected: ${out}"
