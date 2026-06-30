package agent

import (
	"fmt"
	"sort"
	"strings"
)

type ToolCapabilityFamily string

const (
	ToolCapabilityFamilyTelemetry       ToolCapabilityFamily = "telemetry"
	ToolCapabilityFamilyLogs            ToolCapabilityFamily = "logs"
	ToolCapabilityFamilyChange          ToolCapabilityFamily = "change"
	ToolCapabilityFamilyTopology        ToolCapabilityFamily = "topology"
	ToolCapabilityFamilyRuntimeSecurity ToolCapabilityFamily = "runtime_security"
	ToolCapabilityFamilyKnowledge       ToolCapabilityFamily = "knowledge"
	ToolCapabilityFamilyServiceHealth   ToolCapabilityFamily = "service_health"
	ToolCapabilityFamilyKubernetes      ToolCapabilityFamily = "kubernetes"
	ToolCapabilityFamilyDiagnosticExec  ToolCapabilityFamily = "diagnostic_execution"
	ToolCapabilityFamilyRemediation     ToolCapabilityFamily = "remediation"
	ToolCapabilityFamilyGeneric         ToolCapabilityFamily = "generic"
)

type ToolCostClass string

const (
	ToolCostClassLow    ToolCostClass = "low"
	ToolCostClassMedium ToolCostClass = "medium"
	ToolCostClassHigh   ToolCostClass = "high"
)

type ToolFreshnessSensitivity string

const (
	ToolFreshnessSensitivityLow    ToolFreshnessSensitivity = "low"
	ToolFreshnessSensitivityMedium ToolFreshnessSensitivity = "medium"
	ToolFreshnessSensitivityHigh   ToolFreshnessSensitivity = "high"
)

type ToolAutonomyEligibility string

const (
	ToolAutonomyEligibilityAutonomousReadOnly ToolAutonomyEligibility = "autonomous_read_only"
	ToolAutonomyEligibilityProposalOnly       ToolAutonomyEligibility = "proposal_only"
	ToolAutonomyEligibilityApprovalGated      ToolAutonomyEligibility = "approval_gated"
	ToolAutonomyEligibilityBlocked            ToolAutonomyEligibility = "blocked"

	// Backward-compatible symbolic names kept for callers that still use the
	// pre skills-first wording.
	ToolAutonomyEligibilityNever            ToolAutonomyEligibility = ToolAutonomyEligibilityBlocked
	ToolAutonomyEligibilityControllerGated  ToolAutonomyEligibility = ToolAutonomyEligibilityProposalOnly
	ToolAutonomyEligibilityReadOnlyEligible ToolAutonomyEligibility = ToolAutonomyEligibilityAutonomousReadOnly
)

type ToolQueryHints struct {
	DefaultTerms    []string `json:"default_terms,omitempty"`
	ScopeKeys       []string `json:"scope_keys,omitempty"`
	TimeWindowMode  string   `json:"time_window_mode,omitempty"`
	SignalHints     []string `json:"signal_hints,omitempty"`
	SceneHints      []string `json:"scene_hints,omitempty"`
	ExpectedSignals []string `json:"expected_signals,omitempty"`
}

type ToolContract struct {
	Name                           ToolName                        `json:"name"`
	Version                        string                          `json:"version"`
	Purpose                        string                          `json:"purpose"`
	Description                    string                          `json:"description,omitempty"`
	CapabilityFamily               ToolCapabilityFamily            `json:"capability_family"`
	Determinism                    string                          `json:"determinism"`
	ReadOnly                       bool                            `json:"read_only"`
	StateChanging                  bool                            `json:"state_changing"`
	SafetyClass                    string                          `json:"safety_class"`
	SideEffects                    []string                        `json:"side_effects,omitempty"`
	AllowedRuntimeContexts         []string                        `json:"allowed_runtime_contexts"`
	InputSchema                    string                          `json:"input_schema"`
	OutputSchema                   string                          `json:"output_schema"`
	EvidenceConsumed               []string                        `json:"evidence_consumed,omitempty"`
	EvidenceProduced               []string                        `json:"evidence_produced,omitempty"`
	Preconditions                  []string                        `json:"preconditions,omitempty"`
	Postconditions                 []string                        `json:"postconditions,omitempty"`
	ApprovalRequirement            WorkflowToolApprovalRequirement `json:"approval_requirement"`
	ApprovalRequired               bool                            `json:"approval_required"`
	IdempotencySemantics           WorkflowToolIdempotencySemantic `json:"idempotency_semantics"`
	Idempotency                    WorkflowToolIdempotencySemantic `json:"idempotency"`
	RollbackSemantics              WorkflowToolRollbackSemantics   `json:"rollback_semantics"`
	TimeoutBudget                  string                          `json:"timeout_budget"`
	RetryPolicy                    WorkflowToolRetryPolicy         `json:"retry_policy"`
	CostClass                      ToolCostClass                   `json:"cost_class"`
	FreshnessSensitivity           ToolFreshnessSensitivity        `json:"freshness_sensitivity"`
	ScopeSensitivity               string                          `json:"scope_sensitivity,omitempty"`
	ExpectedInformationGain        float64                         `json:"expected_information_gain"`
	ExpectedInformationGainProfile string                          `json:"expected_information_gain_profile,omitempty"`
	AutonomousSelectionEligible    ToolAutonomyEligibility         `json:"autonomous_selection_eligible"`
	AutonomyEligibility            ToolAutonomyEligibility         `json:"autonomy_eligibility"`
	PreferredQueryHints            ToolQueryHints                  `json:"preferred_query_hints,omitempty"`
	QueryHints                     ToolQueryHints                  `json:"query_hints,omitempty"`
	LikelyFollowupToolFamilies     []ToolCapabilityFamily          `json:"likely_followup_tool_families,omitempty"`
	LikelyFollowUpFamilies         []ToolCapabilityFamily          `json:"likely_follow_up_families,omitempty"`
	ReplaySemantics                string                          `json:"replay_semantics"`
	LegacyContractID               string                          `json:"legacy_contract_id,omitempty"`
	LegacyContract                 WorkflowToolContract            `json:"legacy_contract"`
}

type ToolContractRegistry struct {
	contracts []ToolContract
	byName    map[ToolName]ToolContract
}

func NewToolContractRegistry(descriptors []WorkflowToolDescriptor) *ToolContractRegistry {
	registry := &ToolContractRegistry{
		contracts: make([]ToolContract, 0, len(descriptors)),
		byName:    make(map[ToolName]ToolContract, len(descriptors)),
	}
	for _, desc := range descriptors {
		contract := toolContractFromLegacy(desc.Contract)
		registry.contracts = append(registry.contracts, contract)
		registry.byName[contract.Name] = contract
	}
	sort.Slice(registry.contracts, func(i, j int) bool {
		return registry.contracts[i].Name < registry.contracts[j].Name
	})
	return registry
}

func (r *ToolContractRegistry) List() []ToolContract {
	if r == nil {
		return nil
	}
	out := make([]ToolContract, len(r.contracts))
	copy(out, r.contracts)
	return out
}

func (r *ToolContractRegistry) Get(name ToolName) (ToolContract, bool) {
	if r == nil {
		return ToolContract{}, false
	}
	contract, ok := r.byName[name]
	return contract, ok
}

func toolContractFromLegacy(legacy WorkflowToolContract) ToolContract {
	contract := ToolContract{
		Name:                           legacy.ToolName,
		Version:                        firstNonEmpty(legacy.Version, workflowToolVersion),
		Purpose:                        legacy.Purpose,
		Description:                    legacy.Purpose,
		CapabilityFamily:               ToolCapabilityFamily(firstNonEmpty(legacy.CapabilityFamily, string(ToolCapabilityFamilyGeneric))),
		Determinism:                    legacy.Determinism,
		ReadOnly:                       legacy.ReadOnly,
		StateChanging:                  legacy.StateChanging,
		SafetyClass:                    legacy.SafetyClass,
		SideEffects:                    append([]string(nil), legacy.SideEffects...),
		AllowedRuntimeContexts:         append([]string(nil), legacy.AllowedRuntimeContexts...),
		InputSchema:                    legacy.InputSchema,
		OutputSchema:                   legacy.OutputSchema,
		EvidenceConsumed:               append([]string(nil), legacy.EvidenceConsumed...),
		EvidenceProduced:               append([]string(nil), legacy.EvidenceProduced...),
		Preconditions:                  append([]string(nil), legacy.Preconditions...),
		Postconditions:                 append([]string(nil), legacy.Postconditions...),
		ApprovalRequirement:            legacy.Approval,
		ApprovalRequired:               legacy.Approval.Required,
		IdempotencySemantics:           legacy.Idempotency,
		Idempotency:                    legacy.Idempotency,
		RollbackSemantics:              legacy.Rollback,
		TimeoutBudget:                  legacy.TimeoutBudget,
		RetryPolicy:                    legacy.RetryPolicy,
		CostClass:                      ToolCostClass(firstNonEmpty(legacy.CostClass, string(ToolCostClassLow))),
		FreshnessSensitivity:           ToolFreshnessSensitivity(firstNonEmpty(legacy.FreshnessSensitivity, string(ToolFreshnessSensitivityLow))),
		ScopeSensitivity:               legacy.ScopeSensitivity,
		ExpectedInformationGain:        legacy.ExpectedInformationGain,
		ExpectedInformationGainProfile: firstNonEmpty(legacy.ExpectedInformationProfile, "balanced"),
		AutonomousSelectionEligible:    toolAutonomyEligibilityFromLegacy(legacy),
		AutonomyEligibility:            toolAutonomyEligibilityFromLegacy(legacy),
		PreferredQueryHints:            toolQueryHintsFromLegacy(legacy),
		QueryHints:                     toolQueryHintsFromLegacy(legacy),
		LikelyFollowupToolFamilies:     toolLikelyFollowupFamilies(legacy.ToolName),
		LikelyFollowUpFamilies:         toolLikelyFollowupFamilies(legacy.ToolName),
		ReplaySemantics:                legacy.ReplaySemantics,
		LegacyContractID:               workflowToolContractID(legacy),
		LegacyContract:                 legacy,
	}
	return normalizeToolContractDefaults(contract)
}

func toolAutonomyEligibilityFromLegacy(legacy WorkflowToolContract) ToolAutonomyEligibility {
	switch {
	case !legacy.ReadOnly:
		if legacy.Approval.Required {
			return ToolAutonomyEligibilityApprovalGated
		}
		return ToolAutonomyEligibilityBlocked
	case legacy.EligibleForAutoSelection:
		return ToolAutonomyEligibilityAutonomousReadOnly
	default:
		return ToolAutonomyEligibilityProposalOnly
	}
}

func toolQueryHintsFromLegacy(legacy WorkflowToolContract) ToolQueryHints {
	hints := ToolQueryHints{
		DefaultTerms:   append([]string(nil), legacy.PreferredQueryHints...),
		TimeWindowMode: firstNonEmpty(legacy.FreshnessSensitivity, "medium"),
	}
	switch strings.TrimSpace(legacy.ScopeSensitivity) {
	case "collector_or_service":
		hints.ScopeKeys = []string{"collector_id", "service", "scope"}
	case "workload_or_revision":
		hints.ScopeKeys = []string{"service", "workload", "revision", "scope"}
	case "topology_or_graph":
		hints.ScopeKeys = []string{"collector_id", "scope", "downstream_nodes"}
	default:
		hints.ScopeKeys = []string{"collector_id", "scope"}
	}
	hints.ExpectedSignals = append([]string(nil), legacy.EvidenceProduced...)
	hints.SignalHints = compactStrings(strings.Join(legacy.EvidenceProduced, ","), strings.Join(legacy.EvidenceConsumed, ","))
	return hints
}

func toolLikelyFollowupFamilies(name ToolName) []ToolCapabilityFamily {
	switch name {
	case ToolMetrics:
		return []ToolCapabilityFamily{ToolCapabilityFamilyServiceHealth, ToolCapabilityFamilyLogs}
	case ToolLogs:
		return []ToolCapabilityFamily{ToolCapabilityFamilyChange, ToolCapabilityFamilyServiceHealth}
	case ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolContainerRevision:
		return []ToolCapabilityFamily{ToolCapabilityFamilyKubernetes, ToolCapabilityFamilyServiceHealth}
	case ToolTopology, ToolNetworkBlastRadius:
		return []ToolCapabilityFamily{ToolCapabilityFamilyServiceHealth, ToolCapabilityFamilyTopology}
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery:
		return []ToolCapabilityFamily{ToolCapabilityFamilyRuntimeSecurity, ToolCapabilityFamilyLogs}
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		return []ToolCapabilityFamily{ToolCapabilityFamilyTelemetry, ToolCapabilityFamilyServiceHealth, ToolCapabilityFamilyChange}
	case ToolMemoryPressure, ToolStorageHealth, ToolGPU:
		return []ToolCapabilityFamily{ToolCapabilityFamilyTelemetry, ToolCapabilityFamilyLogs}
	case ToolConnectivityCheck, ToolDNSCheck, ToolServiceHealth:
		return []ToolCapabilityFamily{ToolCapabilityFamilyServiceHealth, ToolCapabilityFamilyTopology}
	case ToolKubernetesResource:
		return []ToolCapabilityFamily{ToolCapabilityFamilyKubernetes, ToolCapabilityFamilyChange}
	default:
		return []ToolCapabilityFamily{ToolCapabilityFamilyGeneric}
	}
}

func validateToolContract(contract ToolContract) error {
	contract = normalizeToolContractDefaults(contract)
	missing := []string{}
	if strings.TrimSpace(string(contract.Name)) == "" {
		missing = append(missing, "name")
	}
	if strings.TrimSpace(contract.Version) == "" {
		missing = append(missing, "version")
	}
	if strings.TrimSpace(contract.Purpose) == "" {
		missing = append(missing, "purpose")
	}
	if strings.TrimSpace(contract.Description) == "" {
		missing = append(missing, "description")
	}
	if strings.TrimSpace(string(contract.CapabilityFamily)) == "" {
		missing = append(missing, "capability_family")
	}
	if strings.TrimSpace(contract.InputSchema) == "" {
		missing = append(missing, "input_schema")
	}
	if strings.TrimSpace(contract.OutputSchema) == "" {
		missing = append(missing, "output_schema")
	}
	if strings.TrimSpace(contract.TimeoutBudget) == "" {
		missing = append(missing, "timeout_budget")
	}
	if len(contract.AllowedRuntimeContexts) == 0 {
		missing = append(missing, "allowed_runtime_contexts")
	}
	if len(missing) > 0 {
		return fmt.Errorf("tool contract %s missing required fields: %s", contract.Name, strings.Join(missing, ","))
	}
	if contract.ReadOnly && contract.ApprovalRequirement.Required {
		return fmt.Errorf("tool contract %s cannot require approval when read-only", contract.Name)
	}
	if !contract.ReadOnly && contract.AutonomyEligibility == ToolAutonomyEligibilityAutonomousReadOnly {
		return fmt.Errorf("tool contract %s cannot grant autonomous read-only eligibility to state-changing skill", contract.Name)
	}
	if contract.StateChanging && !contract.ApprovalRequired && !contract.ApprovalRequirement.Required {
		return fmt.Errorf("tool contract %s requires approval for state-changing or impactful action", contract.Name)
	}
	if contract.StateChanging && contract.Idempotency.Required == false {
		return fmt.Errorf("tool contract %s requires idempotency for state-changing action", contract.Name)
	}
	if contract.CapabilityFamily == ToolCapabilityFamilyRemediation && !contract.RollbackSemantics.Required {
		return fmt.Errorf("tool contract %s requires rollback semantics for remediation", contract.Name)
	}
	if contract.RetryPolicy.MaxAttempts <= 0 {
		return fmt.Errorf("tool contract %s retry policy must allow at least one attempt", contract.Name)
	}
	switch strings.TrimSpace(contract.ReplaySemantics) {
	case "",
		"replayable_when_source_window_and_artifact_refs_are_available",
		"intent_replay_only; execution_result_history_is_not_replayed",
		"replay_safe",
		"checkpoint_only",
		"non_replayable":
	default:
		return fmt.Errorf("tool contract %s has invalid replay semantics %q", contract.Name, contract.ReplaySemantics)
	}
	if err := validateWorkflowToolContract(contract.LegacyContract); err != nil {
		return err
	}
	return nil
}

func normalizeToolContractDefaults(contract ToolContract) ToolContract {
	if strings.TrimSpace(contract.Description) == "" {
		contract.Description = contract.Purpose
	}
	if contract.ApprovalRequired != contract.ApprovalRequirement.Required {
		contract.ApprovalRequired = contract.ApprovalRequirement.Required
	}
	if contract.Idempotency == (WorkflowToolIdempotencySemantic{}) {
		contract.Idempotency = contract.IdempotencySemantics
	}
	if contract.IdempotencySemantics == (WorkflowToolIdempotencySemantic{}) {
		contract.IdempotencySemantics = contract.Idempotency
	}
	if contract.AutonomyEligibility == "" {
		contract.AutonomyEligibility = contract.AutonomousSelectionEligible
	}
	if contract.AutonomousSelectionEligible == "" {
		contract.AutonomousSelectionEligible = contract.AutonomyEligibility
	}
	if toolQueryHintsEmpty(contract.QueryHints) {
		contract.QueryHints = contract.PreferredQueryHints
	}
	if toolQueryHintsEmpty(contract.PreferredQueryHints) {
		contract.PreferredQueryHints = contract.QueryHints
	}
	if len(contract.LikelyFollowUpFamilies) == 0 {
		contract.LikelyFollowUpFamilies = append([]ToolCapabilityFamily(nil), contract.LikelyFollowupToolFamilies...)
	}
	if len(contract.LikelyFollowupToolFamilies) == 0 {
		contract.LikelyFollowupToolFamilies = append([]ToolCapabilityFamily(nil), contract.LikelyFollowUpFamilies...)
	}
	if contract.CostClass == "" {
		contract.CostClass = ToolCostClassLow
	}
	if contract.FreshnessSensitivity == "" {
		contract.FreshnessSensitivity = ToolFreshnessSensitivityLow
	}
	if contract.ExpectedInformationGainProfile == "" {
		contract.ExpectedInformationGainProfile = "balanced"
	}
	if contract.ReplaySemantics == "" && contract.ReadOnly {
		contract.ReplaySemantics = "replayable_when_source_window_and_artifact_refs_are_available"
	}
	if contract.ReplaySemantics == "" && contract.StateChanging {
		contract.ReplaySemantics = "intent_replay_only; execution_result_history_is_not_replayed"
	}
	return contract
}

func toolQueryHintsEmpty(hints ToolQueryHints) bool {
	return len(hints.DefaultTerms) == 0 &&
		len(hints.ScopeKeys) == 0 &&
		strings.TrimSpace(hints.TimeWindowMode) == "" &&
		len(hints.SignalHints) == 0 &&
		len(hints.SceneHints) == 0 &&
		len(hints.ExpectedSignals) == 0
}
