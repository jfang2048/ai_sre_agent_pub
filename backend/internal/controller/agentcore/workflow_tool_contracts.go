package agent

import (
	"fmt"
	"strings"
	"time"
)

const workflowToolContractSchemaVersion = "ai_sre_agent/workflow_tool_contract/v1"

// WorkflowToolContract is the machine-readable control surface for model and
// controller tool routing. Descriptors are operator-facing; contracts are
// enforced before a tool can be selected or invoked.
type WorkflowToolContract struct {
	SchemaVersion              string                          `json:"schema_version"`
	ToolName                   ToolName                        `json:"tool_name"`
	Version                    string                          `json:"version"`
	Purpose                    string                          `json:"purpose"`
	CapabilityFamily           string                          `json:"capability_family"`
	AllowedStages              []string                        `json:"allowed_stages"`
	AllowedRuntimeContexts     []string                        `json:"allowed_runtime_contexts"`
	InputSchema                string                          `json:"input_schema"`
	OutputSchema               string                          `json:"output_schema"`
	EvidenceConsumed           []string                        `json:"evidence_consumed"`
	EvidenceProduced           []string                        `json:"evidence_produced"`
	Determinism                string                          `json:"determinism"`
	ReadOnly                   bool                            `json:"read_only"`
	StateChanging              bool                            `json:"state_changing"`
	SafetyClass                string                          `json:"safety_class"`
	SideEffects                []string                        `json:"side_effects,omitempty"`
	Risks                      []string                        `json:"risks,omitempty"`
	Rollback                   WorkflowToolRollbackSemantics   `json:"rollback"`
	Idempotency                WorkflowToolIdempotencySemantic `json:"idempotency"`
	TimeoutBudget              string                          `json:"timeout_budget"`
	RetryPolicy                WorkflowToolRetryPolicy         `json:"retry_policy"`
	Approval                   WorkflowToolApprovalRequirement `json:"approval"`
	Preconditions              []string                        `json:"preconditions,omitempty"`
	Postconditions             []string                        `json:"postconditions,omitempty"`
	ObservabilityHooks         []string                        `json:"observability_hooks,omitempty"`
	CostClass                  string                          `json:"cost_class"`
	ConfidenceImpact           float64                         `json:"confidence_impact"`
	ExpectedInformationGain    float64                         `json:"expected_information_gain"`
	ExpectedInformationProfile string                          `json:"expected_information_profile,omitempty"`
	EligibleForAutoSelection   bool                            `json:"eligible_for_auto_selection"`
	PreferredQueryHints        []string                        `json:"preferred_query_hints,omitempty"`
	FreshnessSensitivity       string                          `json:"freshness_sensitivity,omitempty"`
	ScopeSensitivity           string                          `json:"scope_sensitivity,omitempty"`
	ReplaySemantics            string                          `json:"replay_semantics"`
	ContractValidationVersion  string                          `json:"contract_validation_version"`
}

type WorkflowToolRollbackSemantics struct {
	Supported bool   `json:"supported"`
	Required  bool   `json:"required"`
	Semantics string `json:"semantics"`
}

type WorkflowToolIdempotencySemantic struct {
	Required bool   `json:"required"`
	Scope    string `json:"scope"`
	Reuse    string `json:"reuse"`
}

type WorkflowToolRetryPolicy struct {
	MaxAttempts        int  `json:"max_attempts"`
	Retryable          bool `json:"retryable"`
	RetryOnTransient   bool `json:"retry_on_transient"`
	RetryOnTimeout     bool `json:"retry_on_timeout"`
	RetryRequiresFresh bool `json:"retry_requires_fresh_preconditions"`
}

type WorkflowToolApprovalRequirement struct {
	Required   bool   `json:"required"`
	Reason     string `json:"reason,omitempty"`
	PolicyGate string `json:"policy_gate,omitempty"`
}

func workflowToolContractFromDescriptor(desc WorkflowToolDescriptor) WorkflowToolContract {
	readOnly := desc.ReadOnly
	timeout := defaultToolContractTimeout(desc.Name)
	retryAttempts := 1
	if readOnly {
		retryAttempts = 2
	}
	purpose := firstNonEmpty(desc.Purpose, desc.Description, string(desc.Name))
	sideEffects := compactStrings(desc.SideEffects)
	if len(sideEffects) == 1 && strings.EqualFold(sideEffects[0], "none") {
		sideEffects = nil
	}
	contract := WorkflowToolContract{
		SchemaVersion:              workflowToolContractSchemaVersion,
		ToolName:                   desc.Name,
		Version:                    firstNonEmpty(desc.Version, workflowToolVersion),
		Purpose:                    purpose,
		CapabilityFamily:           workflowToolCapabilityFamily(desc.Name),
		AllowedStages:              workflowToolAllowedStages(desc),
		AllowedRuntimeContexts:     []string{"joint_risk", "rca"},
		InputSchema:                firstNonEmpty(desc.InputSchema, "{}"),
		OutputSchema:               firstNonEmpty(desc.OutputSchema, "{}"),
		EvidenceConsumed:           workflowToolEvidenceConsumed(desc.Name),
		EvidenceProduced:           workflowToolEvidenceProduced(desc.Name),
		Determinism:                workflowToolDeterminism(desc),
		ReadOnly:                   readOnly,
		StateChanging:              !readOnly,
		SafetyClass:                firstNonEmpty(desc.SafetyClass, "read_only"),
		SideEffects:                sideEffects,
		Risks:                      workflowToolRisks(desc),
		Rollback:                   workflowToolRollback(desc),
		Idempotency:                workflowToolIdempotency(desc),
		TimeoutBudget:              timeout.String(),
		RetryPolicy:                WorkflowToolRetryPolicy{MaxAttempts: retryAttempts, Retryable: readOnly, RetryOnTransient: readOnly, RetryOnTimeout: false, RetryRequiresFresh: true},
		Approval:                   WorkflowToolApprovalRequirement{Required: desc.RequiresApproval, Reason: workflowToolApprovalReason(desc), PolicyGate: "PolicyEngine.Evaluate"},
		Preconditions:              workflowToolPreconditions(desc),
		Postconditions:             workflowToolPostconditions(desc),
		ObservabilityHooks:         []string{"WorkflowToolCall", "WorkflowAuditRecord", "DurableRun.Events"},
		CostClass:                  workflowToolCostClass(desc.Name),
		ConfidenceImpact:           workflowToolConfidenceImpact(desc.Name),
		ExpectedInformationGain:    workflowToolExpectedInformationGain(desc.Name),
		ExpectedInformationProfile: workflowToolInformationProfile(desc.Name),
		EligibleForAutoSelection:   readOnly && !desc.Unsafe && !desc.RequiresApproval,
		PreferredQueryHints:        workflowToolPreferredQueryHints(desc.Name),
		FreshnessSensitivity:       workflowToolFreshnessSensitivity(desc.Name),
		ScopeSensitivity:           workflowToolScopeSensitivity(desc.Name),
		ReplaySemantics:            workflowToolReplaySemantics(desc),
		ContractValidationVersion:  "v1",
	}
	if desc.Name == ToolProfiling || desc.Name == ToolRemediation {
		contract.EligibleForAutoSelection = false
	}
	return contract
}

func validateWorkflowToolContract(contract WorkflowToolContract) error {
	missing := []string{}
	if strings.TrimSpace(string(contract.ToolName)) == "" {
		missing = append(missing, "tool_name")
	}
	if strings.TrimSpace(contract.Version) == "" {
		missing = append(missing, "version")
	}
	if strings.TrimSpace(contract.Purpose) == "" {
		missing = append(missing, "purpose")
	}
	if strings.TrimSpace(contract.CapabilityFamily) == "" {
		missing = append(missing, "capability_family")
	}
	if len(contract.AllowedStages) == 0 {
		missing = append(missing, "allowed_stages")
	}
	if strings.TrimSpace(contract.InputSchema) == "" {
		missing = append(missing, "input_schema")
	}
	if strings.TrimSpace(contract.OutputSchema) == "" {
		missing = append(missing, "output_schema")
	}
	if strings.TrimSpace(contract.SafetyClass) == "" {
		missing = append(missing, "safety_class")
	}
	if strings.TrimSpace(contract.TimeoutBudget) == "" {
		missing = append(missing, "timeout_budget")
	}
	if len(missing) > 0 {
		return fmt.Errorf("tool contract %s missing required fields: %s", contract.ToolName, strings.Join(missing, ","))
	}
	if contract.EligibleForAutoSelection && !contract.ReadOnly {
		return fmt.Errorf("tool contract %s cannot be auto-selected because it changes state", contract.ToolName)
	}
	if contract.StateChanging && !contract.Approval.Required {
		return fmt.Errorf("tool contract %s requires approval for state-changing or impactful action", contract.ToolName)
	}
	if contract.StateChanging && !contract.Idempotency.Required {
		return fmt.Errorf("tool contract %s requires idempotency for state-changing action", contract.ToolName)
	}
	if contract.CapabilityFamily == string(ToolCapabilityFamilyRemediation) && !contract.Rollback.Required {
		return fmt.Errorf("tool contract %s requires rollback semantics for remediation", contract.ToolName)
	}
	if _, err := time.ParseDuration(contract.TimeoutBudget); err != nil {
		return fmt.Errorf("tool contract %s has invalid timeout budget: %w", contract.ToolName, err)
	}
	if contract.RetryPolicy.MaxAttempts <= 0 {
		return fmt.Errorf("tool contract %s retry policy must allow at least one attempt", contract.ToolName)
	}
	return nil
}

func workflowToolContractAllowsStage(contract WorkflowToolContract, stage string) bool {
	stage = strings.TrimSpace(stage)
	for _, allowed := range contract.AllowedStages {
		switch strings.TrimSpace(allowed) {
		case "*", stage:
			return true
		}
	}
	return false
}

func workflowToolContractID(contract WorkflowToolContract) string {
	if strings.TrimSpace(string(contract.ToolName)) == "" {
		return ""
	}
	return fmt.Sprintf("%s@%s", contract.ToolName, firstNonEmpty(contract.Version, workflowToolVersion))
}

func workflowToolTimeoutFromContract(contract WorkflowToolContract, fallback ToolName) time.Duration {
	if timeout, err := time.ParseDuration(contract.TimeoutBudget); err == nil && timeout > 0 {
		return timeout
	}
	return toolTimeoutForName(fallback)
}

func workflowToolMaxAttemptsFromContract(contract WorkflowToolContract, cfg WorkflowConfig) int {
	maxAttempts := maxInt(contract.RetryPolicy.MaxAttempts, 1)
	if contract.ReadOnly && cfg.ToolRetryCount > 0 {
		maxAttempts = maxInt(maxAttempts, 1+cfg.ToolRetryCount)
	}
	if !contract.RetryPolicy.Retryable {
		return 1
	}
	return maxAttempts
}

func workflowToolCapabilityFamily(name ToolName) string {
	switch name {
	case ToolMetrics, ToolMemoryPressure, ToolStorageHealth, ToolGPU:
		return "telemetry"
	case ToolLogs:
		return "logs"
	case ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolContainerRevision:
		return "change"
	case ToolTopology, ToolNetworkBlastRadius:
		return "topology"
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery:
		return "runtime_security"
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		return "knowledge"
	case ToolConnectivityCheck, ToolDNSCheck, ToolServiceHealth:
		return "service_health"
	case ToolKubernetesResource:
		return "kubernetes"
	case ToolProfiling:
		return "diagnostic_execution"
	case ToolRemediation:
		return "remediation"
	default:
		return "generic"
	}
}

func workflowToolAllowedStages(desc WorkflowToolDescriptor) []string {
	switch desc.Name {
	case ToolProfiling:
		return []string{"recommendation_generation", "guarded_execution_plan", "plan_act_verify_loop"}
	case ToolRemediation:
		return []string{"guarded_execution_plan", "plan_act_verify_loop"}
	default:
		return []string{"*"}
	}
}

func workflowToolEvidenceConsumed(name ToolName) []string {
	switch name {
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		return []string{"incident_summary", "hypothesis", "evidence_gap"}
	case ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolContainerRevision:
		return []string{"incident_window", "scope", "change_hint"}
	case ToolRemediation:
		return []string{"validation_action_contract", "policy_posture", "rollback_plan"}
	default:
		return []string{"collector_id", "incident_window", "evidence_gap"}
	}
}

func workflowToolEvidenceProduced(name ToolName) []string {
	switch name {
	case ToolMetrics:
		return []string{"metric_baseline", "resource_pressure", "trend"}
	case ToolLogs:
		return []string{"recent_logs", "log_patterns", "deployment_hints"}
	case ToolChangeQuery, ToolDeploymentHistory:
		return []string{"change_correlation", "rollout_adjacency"}
	case ToolConfigState, ToolContainerRevision:
		return []string{"runtime_config", "revision_context"}
	case ToolTopology, ToolNetworkBlastRadius:
		return []string{"blast_radius", "topology_neighbors"}
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery:
		return []string{"runtime_security", "process_lineage", "contradiction_evidence"}
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		return []string{"corroborating_knowledge", "prior_outcome", "runbook_step"}
	case ToolMemoryPressure:
		return []string{"memory_pressure", "oom_hint"}
	case ToolConnectivityCheck, ToolDNSCheck, ToolServiceHealth:
		return []string{"service_health", "network_health", "dns_health"}
	case ToolGPU:
		return []string{"gpu_pressure", "gpu_process_attribution"}
	case ToolKubernetesResource:
		return []string{"workload_identity", "pod_context"}
	case ToolStorageHealth:
		return []string{"storage_pressure", "io_latency"}
	case ToolProfiling:
		return []string{"diagnostic_profile_intent"}
	case ToolRemediation:
		return []string{"execution_intent", "action_result", "rollback_state"}
	default:
		return []string{"tool_observation"}
	}
}

func workflowToolDeterminism(desc WorkflowToolDescriptor) string {
	if desc.Deterministic {
		return "deterministic"
	}
	return "probabilistic_or_external"
}

func workflowToolRisks(desc WorkflowToolDescriptor) []string {
	if desc.ReadOnly {
		if desc.Name == ToolKnowledge || desc.Name == ToolRAGQuery || desc.Name == ToolHistoricalIncident || desc.Name == ToolRunbookRetrieval || desc.Name == ToolSimilarCase {
			return []string{"retrieval may return stale or non-authoritative context"}
		}
		return []string{"stale telemetry can reduce confidence"}
	}
	return compactStrings(desc.SideEffects, "state-changing tool requires policy and approval checks")
}

func workflowToolRollback(desc WorkflowToolDescriptor) WorkflowToolRollbackSemantics {
	if desc.ReadOnly {
		return WorkflowToolRollbackSemantics{Supported: false, Required: false, Semantics: "not_applicable_read_only"}
	}
	return WorkflowToolRollbackSemantics{Supported: desc.SupportsRollback, Required: desc.Name == ToolRemediation, Semantics: "controller records rollback or compensation state before final verification"}
}

func workflowToolIdempotency(desc WorkflowToolDescriptor) WorkflowToolIdempotencySemantic {
	if desc.ReadOnly {
		return WorkflowToolIdempotencySemantic{Required: false, Scope: "tool+workflow+stage+collector+query+dry_run", Reuse: "read_only_result_cache"}
	}
	return WorkflowToolIdempotencySemantic{Required: true, Scope: "action_contract+workflow+stage+collector+dry_run", Reuse: "durable_successful_action_reuse"}
}

func workflowToolApprovalReason(desc WorkflowToolDescriptor) string {
	if !desc.RequiresApproval {
		return ""
	}
	if desc.Name == ToolRemediation {
		return "remediation may change live state"
	}
	return "diagnostic execution may add runtime overhead"
}

func workflowToolPreconditions(desc WorkflowToolDescriptor) []string {
	base := []string{"workflow_id present", "stage allowed by contract"}
	if desc.Name == ToolRemediation {
		return append(base, "valid ValidationActionContract", "rollback guidance present for impactful actions")
	}
	if desc.Name == ToolProfiling {
		return append(base, "profiling execution enabled or dry-run posture forced")
	}
	return append(base, "fresh-enough telemetry or explicit degraded-mode limitation")
}

func workflowToolPostconditions(desc WorkflowToolDescriptor) []string {
	if desc.ReadOnly {
		return []string{"tool call audited", "result summarized", "no live state mutation"}
	}
	return []string{"policy decision recorded", "approval state recorded", "idempotency key recorded", "verification path remains explicit"}
}

func workflowToolCostClass(name ToolName) string {
	switch name {
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase:
		return "medium"
	case ToolProfiling, ToolRemediation:
		return "high"
	default:
		return "low"
	}
}

func workflowToolConfidenceImpact(name ToolName) float64 {
	switch workflowToolCapabilityFamily(name) {
	case "knowledge", "runtime_security":
		return 0.08
	case "change", "service_health":
		return 0.07
	case "diagnostic_execution", "remediation":
		return 0.10
	default:
		return 0.05
	}
}

func workflowToolExpectedInformationGain(name ToolName) float64 {
	switch name {
	case ToolMetrics, ToolLogs, ToolChangeQuery, ToolServiceHealth:
		return 0.70
	case ToolKnowledge, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase:
		return 0.62
	case ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery, ToolGPU:
		return 0.60
	case ToolProfiling:
		return 0.55
	case ToolRemediation:
		return 0.20
	default:
		return 0.45
	}
}

func workflowToolInformationProfile(name ToolName) string {
	switch name {
	case ToolMetrics, ToolLogs, ToolChangeQuery, ToolServiceHealth:
		return "high-value discriminator for primary hypothesis narrowing"
	case ToolKnowledge, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		return "context enrichment and prior verification reuse"
	case ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery, ToolTopology, ToolNetworkBlastRadius:
		return "scope and contradiction clarifier"
	case ToolProfiling:
		return "expensive final disambiguation step"
	case ToolRemediation:
		return "execution intent rather than evidence collection"
	default:
		return "general-purpose evidence enrichment"
	}
}

func workflowToolPreferredQueryHints(name ToolName) []string {
	switch name {
	case ToolMetrics:
		return []string{"prefer bounded recent time windows", "include collector_id", "filter to triggered metrics first"}
	case ToolLogs:
		return []string{"prefer error keywords and recent rollout markers", "keep query concise", "narrow to impacted workload when known"}
	case ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolContainerRevision:
		return []string{"anchor on rollout window", "include revision or config keywords", "prefer impacted service scope"}
	case ToolTopology, ToolNetworkBlastRadius:
		return []string{"prefer impacted entity scope", "expand one hop before full blast radius"}
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		return []string{"use incident summary plus top evidence gaps", "prefer top_k <= 5", "include scene family when available"}
	case ToolConnectivityCheck, ToolDNSCheck, ToolServiceHealth:
		return []string{"shape query to current service or node scope", "use network/dns/latency signal hints"}
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery:
		return []string{"prefer suspicious process or runtime-event scope", "limit to contradiction-relevant filters first"}
	case ToolProfiling:
		return []string{"only after cheaper read-only disambiguation", "keep profiling reason explicit"}
	case ToolRemediation:
		return []string{"require validated action contract", "include rollback summary and target scope"}
	default:
		return []string{"keep query bounded to the current objective"}
	}
}

func workflowToolFreshnessSensitivity(name ToolName) string {
	switch name {
	case ToolMetrics, ToolLogs, ToolConnectivityCheck, ToolDNSCheck, ToolServiceHealth, ToolSecurity, ToolEBPFQuery, ToolGPU, ToolStorageHealth:
		return "high"
	case ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolContainerRevision, ToolTopology, ToolNetworkBlastRadius:
		return "medium"
	default:
		return "low"
	}
}

func workflowToolScopeSensitivity(name ToolName) string {
	switch name {
	case ToolMetrics, ToolLogs, ToolConnectivityCheck, ToolDNSCheck, ToolServiceHealth, ToolMemoryPressure, ToolStorageHealth, ToolGPU:
		return "collector_or_service"
	case ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolContainerRevision, ToolKubernetesResource:
		return "workload_or_revision"
	case ToolTopology, ToolNetworkBlastRadius, ToolSecurityGraph, ToolProcessLineage:
		return "topology_or_graph"
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		return "objective_or_evidence_gap"
	case ToolProfiling, ToolRemediation:
		return "explicit_target_scope_required"
	default:
		return "collector"
	}
}

func workflowToolReplaySemantics(desc WorkflowToolDescriptor) string {
	if desc.ReadOnly {
		return "replayable_when_source_window_and_artifact_refs_are_available"
	}
	return "intent_replay_only; execution_result_history_is_not_replayed"
}

func defaultToolContractTimeout(name ToolName) time.Duration {
	return toolTimeoutForName(name)
}
