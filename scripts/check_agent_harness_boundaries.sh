#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

failures=()

fail() {
  failures+=("$1")
}

require_file() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    fail "missing required file: ${file}. Restore it or update scripts/check_agent_harness_boundaries.sh with the new path."
  fi
}

contains_forbidden() {
  local file="$1"
  local pattern="$2"
  grep -nE "$pattern" "$file" 2>/dev/null || true
}

# 1. Model/LLM analysis files must not directly invoke remediation execution.
check_model_files_do_not_execute() {
  local pattern='exec\.Command|CommandContext|ExecuteIncidentAction|RollbackIncidentAction|ApproveIncidentAction|remediation\.New|NewPlaybookRunner|runShell\(|runKubernetes\('
  local files=()
  while IFS= read -r file; do files+=("$file"); done < <(
    find backend/internal/controller/agentcore backend/internal/controller/analysis \
      -type f \( \
        -name '*llm*.go' -o \
        -name 'workflow_reasoning.go' -o \
        -name 'adaptive_planner.go' -o \
        -name 'adaptive_critic.go' -o \
        -name 'adaptive_verifier.go' -o \
        -name 'prompts.go' -o \
        -path 'backend/internal/controller/analysis/*.go' \
      \) ! -name '*_test.go' | sort
  )

  local hits=""
  local file
  for file in "${files[@]}"; do
    hits+="$(contains_forbidden "$file" "$pattern")"
    hits+=$'\n'
  done
  hits="$(printf '%s' "$hits" | sed '/^[[:space:]]*$/d')"
  if [[ -n "$hits" ]]; then
    fail $'LLM/model analysis files directly reference execution paths. Route through workflow tool manager and policy gates instead. Hits:\n'"$hits"
  fi
}

# 2. Validation/workflow action path must not shell out directly; shell execution is
# allowed only in the bounded legacy runner backends that carry their own guards.
check_validation_shell_boundary() {
  local forbidden='os/exec|exec\.Command|CommandContext|/bin/sh|bash -c|sh -c|kubectl'
  local files=()
  while IFS= read -r file; do files+=("$file"); done < <(
    {
      find backend/internal/controller/agentcore -maxdepth 1 -type f \
        \( -name 'validation_*.go' -o -name 'adaptive_*.go' -o -name 'workflow_engine.go' -o -name 'llm_*.go' \) \
        ! -name '*_test.go'
      find backend/internal/controller/analysis -maxdepth 1 -type f -name '*.go' ! -name '*_test.go'
      find backend/internal/controller/agent -maxdepth 1 -type f -name '*.go' ! -name '*_test.go'
    } | sort -u
  )

  local hits=""
  local file
  for file in "${files[@]}"; do
    hits+="$(contains_forbidden "$file" "$forbidden")"
    hits+=$'\n'
  done
  hits="$(printf '%s' "$hits" | sed '/^[[:space:]]*$/d')"
  if [[ -n "$hits" ]]; then
    fail $'Validation/model/workflow files shell out directly. Put shell-capable behavior behind the governed tool/action backend and policy path. Hits:\n'"$hits"
  fi

  require_file backend/internal/controller/agentcore/actions.go
  require_file backend/internal/controller/remediation/engine.go
  require_file backend/internal/controller/agentcore/workflow_tools.go
  require_file backend/internal/controller/agentcore/workflow_engine.go

  if ! grep -q 'func (m \*workflowToolManager) call' backend/internal/controller/agentcore/workflow_tools.go; then
    fail "backend/internal/controller/agentcore/workflow_tools.go: workflowToolManager.call is missing; keep tool execution behind the manager."
  fi
  if ! grep -q 'm.policy.Evaluate' backend/internal/controller/agentcore/workflow_tools.go; then
    fail "backend/internal/controller/agentcore/workflow_tools.go: policy evaluation is missing from tool manager call path."
  fi
  if ! grep -q 'FindToolCallByIdempotency' backend/internal/controller/agentcore/workflow_tools.go; then
    fail "backend/internal/controller/agentcore/workflow_tools.go: durable idempotency lookup is missing from tool manager call path."
  fi
  if ! grep -q 'approval required' backend/internal/controller/agentcore/workflow_tools.go; then
    fail "backend/internal/controller/agentcore/workflow_tools.go: approval-required stop is missing from tool manager call path."
  fi
  if ! grep -n 'ToolRemediation' backend/internal/controller/agentcore/workflow_engine.go | grep -q 'state.callTool'; then
    fail "backend/internal/controller/agentcore/workflow_engine.go: ToolRemediation must be invoked through state.callTool so policy/audit/idempotency are applied."
  fi

  local actions_guard='authorize\(|DefaultRunnerConfig|DryRun|AllowedShellCommands|IdempotencyTTL'
  local missing_actions_guard=()
  local token
  for token in 'authorize(' 'DefaultRunnerConfig' 'DryRun' 'AllowedShellCommands' 'IdempotencyTTL'; do
    if ! grep -qF "$token" backend/internal/controller/agentcore/actions.go; then
      missing_actions_guard+=("$token")
    fi
  done
  if [[ ${#missing_actions_guard[@]} -gt 0 ]]; then
    fail "backend/internal/controller/agentcore/actions.go: shell-capable legacy runner is missing guard markers (${missing_actions_guard[*]}). Keep authorization, dry-run, allowlist, and idempotency guards."
  fi
  for token in 'ExecutionLevelApprovalRequired' 'ExecutionLevelDryRun' 'validateScriptPath' 'validateScriptAllowlist'; do
    if ! grep -qF "$token" backend/internal/controller/remediation/engine.go; then
      fail "backend/internal/controller/remediation/engine.go: remediation backend is missing guard marker ${token}. Keep direct script execution behind validation and approval/dry-run levels."
    fi
  done
  if ! grep -q 'exec\.Command\|CommandContext' backend/internal/controller/agentcore/actions.go backend/internal/controller/remediation/engine.go; then
    fail "expected shell-capable backends were not found. If action execution moved, update this boundary check to the new governed backend paths."
  fi
}

# 3. Base artifact schema names and replay semantics must stay stable.
check_artifact_chain_stability() {
  local file='backend/internal/controller/agentcore/workflow_artifacts.go'
  require_file "$file"
  local expected='observation_summary anomaly_finding root_cause_hypothesis remediation_proposal execution_plan execution_result verification_result incident_report'
  local actual
  actual="$(awk '
    /WorkflowArtifactObservationSummary/ {capture=1}
    capture {print}
    /WorkflowArtifactIncidentReport/ {exit}
  ' "$file" | sed -n 's/.*= "\([^"]*\)".*/\1/p' | paste -sd ' ' -)"
  if [[ "$actual" != "$expected" ]]; then
    fail "${file}: base artifact kinds changed. Expected order: ${expected}. Actual: ${actual}. Do not rename/reorder without a schema migration."
  fi
  if ! grep -q 'Replayable:[[:space:]]*kind != WorkflowArtifactExecutionResult' "$file"; then
    fail "${file}: execution_result must remain non-replayable history; restore Replayable: kind != WorkflowArtifactExecutionResult."
  fi
  require_file backend/internal/controller/agentcore/workflow_artifacts_test.go
  if ! grep -q 'require.False(t, chain.ExecutionResult.Meta.Replayable)' backend/internal/controller/agentcore/workflow_artifacts_test.go; then
    fail "backend/internal/controller/agentcore/workflow_artifacts_test.go: add/restore a regression asserting execution_result is not replayable."
  fi
}

# 4. Documentation must not claim a fully distributed runtime while hot state is single-writer.
check_distributed_claims() {
  local hits=""
  while IFS= read -r line; do
    local lower="${line,,}"
    if [[ "$lower" == *"fully distributed workflow runtime"* && "$lower" != *"not"* && "$lower" != *"does not"* && "$lower" != *"no "* && "$lower" != *"不是"* ]]; then
      hits+="$line"$'\n'
    fi
  done < <(grep -RIn --include='*.md' -E 'fully distributed workflow runtime' README.md README.zh-CN.md docs 2>/dev/null || true)
  if [[ -n "$hits" ]]; then
    fail $'Docs claim a fully distributed workflow runtime, but hot state is still single-writer/in-process. Remove the claim or implement shared hot state first. Hits:\n'"$hits"
  fi
}

# 5. Documentation must not claim enterprise RBAC/OIDC readiness without implementation.
check_enterprise_auth_claims() {
  local hits=""
  while IFS= read -r line; do
    local lower="${line,,}"
    if [[ "$lower" == *"oidc"* || "$lower" == *"enterprise rbac"* || "$lower" == *"tenant isolation"* || "$lower" == *"user lifecycle"* ]]; then
      if [[ "$lower" != *"no "* && "$lower" != *"not"* && "$lower" != *"missing"* && "$lower" != *"lacks"* && "$lower" != *"gap"* && "$lower" != *"design"* && "$lower" != *"recommended"* && "$lower" != *"next"* && "$lower" != *"does not"* ]]; then
        hits+="$line"$'\n'
      fi
    fi
  done < <(grep -RIn --include='*.md' -Ei 'OIDC|enterprise RBAC|tenant isolation|user lifecycle' README.md README.zh-CN.md docs deploy 2>/dev/null || true)
  if [[ -n "$hits" ]]; then
    fail $'Docs appear to claim enterprise RBAC/OIDC/tenant readiness. Keep it documented as a gap until code/config enforces it. Hits:\n'"$hits"
  fi
}

# 6. Artifact/message writing code must not carry unclassified secret-looking fields.
check_secret_terms_are_classified() {
  local secret_pattern='api_key|apikey|token|secret|password'
  local files=(
    backend/internal/controller/agentcore/workflow_artifacts.go
    backend/internal/controller/agentcore/workflow_agent_messages.go
    backend/internal/controller/agentcore/agent_messages.go
    backend/internal/controller/agentcore/analysis_handoff.go
    backend/internal/controller/agentcore/workflow_evidence.go
    backend/internal/controller/agentcore/workflow_memory.go
    backend/internal/controller/agentcore/workflow_orchestrator.go
    backend/internal/controller/artifacts/manager.go
    backend/internal/controller/artifacts/payload_s3.go
    backend/internal/controller/artifacts/types.go
    backend/internal/controller/evidence/schema.go
  )
  local uncertain=""
  local file line lower
  for file in "${files[@]}"; do
    require_file "$file"
    while IFS= read -r line; do
      lower="${line,,}"
      case "$lower" in
        *'json:"-"'*|*'yaml:"-"'*|*'payloads3secretkey'*|*'payloads3sessiontoken'*|*'payloads3accesskey'*|*'credentials.newstaticv4'*|*'api_key_env'*|*'api_key_present'*|*'apikeyenv'*|*'apikeyconfigured'*|*'token_cost'*|*'tokencost'*|*'estimatedprompttokens'*|*'estimatedcompletiontokens'*|*'token-efficient'*|*'redactsecrets'*|*'redacted'*|*'secretpatterns'*)
          ;;
        *)
          uncertain+="$line"$'\n'
          ;;
      esac
    done < <(grep -nEi "$secret_pattern" "$file" 2>/dev/null || true)
  done
  if [[ -n "$uncertain" ]]; then
    fail $'Unclassified secret-looking terms found in artifact/message writing code. Redact, store metadata-only, mark json/yaml "-", or add a narrow allowlist with a comment. Hits:\n'"$uncertain"
  fi

  require_file backend/internal/controller/agentcore/llm_safety.go
  if ! grep -q 'RedactSecrets' backend/internal/controller/agentcore/llm_safety.go; then
    fail "backend/internal/controller/agentcore/llm_safety.go: RedactSecrets is missing; LLM prompt/context paths need explicit secret redaction."
  fi
  if ! grep -q 'api.*key\|token\|password\|Bearer' backend/internal/controller/agentcore/llm_safety.go; then
    fail "backend/internal/controller/agentcore/llm_safety.go: secret redaction patterns no longer mention api keys/tokens/passwords/Bearer values."
  fi
}

check_model_files_do_not_execute
check_validation_shell_boundary
check_artifact_chain_stability
check_distributed_claims
check_enterprise_auth_claims
check_secret_terms_are_classified

if [[ ${#failures[@]} -gt 0 ]]; then
  printf 'agent harness boundary check failed (%d violation(s))\n' "${#failures[@]}" >&2
  printf '%s\n' '---' >&2
  for item in "${failures[@]}"; do
    printf '%s\n---\n' "$item" >&2
  done
  exit 1
fi

printf '%s\n' 'agent harness boundary check passed'
