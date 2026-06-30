package agent

import "strings"

type DiagnosticActionContract struct {
	ActionContractID             string            `json:"action_contract_id"`
	Intent                       string            `json:"intent,omitempty"`
	Category                     string            `json:"category,omitempty"`
	SafetyTier                   string            `json:"safety_tier,omitempty"`
	TargetScope                  string            `json:"target_scope,omitempty"`
	ExecutionCategory            string            `json:"execution_category,omitempty"`
	ValidationCategory           string            `json:"validation_category,omitempty"`
	Preconditions                []string          `json:"preconditions,omitempty"`
	Rollback                     RollbackContract  `json:"rollback"`
	ExpectedSupportingSignals    []string          `json:"expected_supporting_signals,omitempty"`
	ExpectedContradictingSignals []string          `json:"expected_contradicting_signals,omitempty"`
	RequiresApproval             bool              `json:"requires_approval,omitempty"`
	DryRunDefault                bool              `json:"dry_run_default,omitempty"`
	ProposalOnly                 bool              `json:"proposal_only,omitempty"`
	Summary                      string            `json:"summary,omitempty"`
	BlastRadiusEstimate          int               `json:"blast_radius_estimate,omitempty"`
	BlastRadiusScope             []string          `json:"blast_radius_scope,omitempty"`
	Metadata                     map[string]string `json:"metadata,omitempty"`
}

func buildDiagnosticActionContract(candidate ValidationActionCandidate, state *workflowState) DiagnosticActionContract {
	supporting := []string{}
	contradicting := []string{}
	if len(state.hypotheses) > 0 {
		supporting = append(supporting, state.hypotheses[0].EvidenceIDs...)
		contradicting = append(contradicting, state.hypotheses[0].ContradictingEvidenceIDs...)
	}
	contract := DiagnosticActionContract{
		ActionContractID:   firstNonEmpty(candidate.ID, "diagnostic-action"),
		Intent:             firstNonEmpty(candidate.ActionIntent, actionIntentFromText(candidate.Summary, candidate.ExpectedImpact, candidate.RollbackHint)),
		Category:           firstNonEmpty(candidate.ActionCategory, actionCategoryFromIntent(firstNonEmpty(candidate.ActionIntent, candidate.Summary))),
		SafetyTier:         firstNonEmpty(candidate.ActuatorSafetyTier, actuatorSafetyTierForCandidate(candidate)),
		TargetScope:        firstNonEmpty(candidate.Scope, state.collectorID),
		ExecutionCategory:  normalizeValidationCategory(candidate.Category),
		ValidationCategory: normalizeValidationCategory(candidate.Category),
		Preconditions:      append([]string(nil), candidate.Preconditions...),
		Rollback: RollbackContract{
			Summary:    firstNonEmpty(candidate.RollbackContract.Summary, candidate.RollbackHint),
			Command:    candidate.RollbackContract.Command,
			Required:   candidate.RollbackContract.Required || candidate.Reversible || strings.TrimSpace(candidate.RollbackHint) != "",
			Reversible: candidate.RollbackContract.Reversible || candidate.Reversible,
		},
		ExpectedSupportingSignals:    dedupeStrings(append([]string(nil), supporting...)),
		ExpectedContradictingSignals: dedupeStrings(append([]string(nil), contradicting...)),
		RequiresApproval:             candidate.RequiresApproval,
		DryRunDefault:                candidate.DryRunDefault,
		ProposalOnly:                 candidate.RequiresApproval || (!candidate.Reversible && !candidate.Safe),
		Summary:                      candidate.Summary,
		BlastRadiusEstimate:          candidate.BlastRadiusEstimate,
		BlastRadiusScope:             append([]string(nil), candidate.BlastRadiusScope...),
		Metadata:                     cloneStringMap(candidate.Metadata),
	}
	if contract.Metadata == nil {
		contract.Metadata = map[string]string{}
	}
	contract.Metadata["diagnostic_category"] = contract.Category
	contract.Metadata["target_scope"] = contract.TargetScope
	return contract
}

func compileValidationActionContract(contract DiagnosticActionContract, collectorID string, blastRadiusScope []string) ValidationActionContract {
	return ValidationActionContract{
		ID:                 firstNonEmpty(contract.ActionContractID, "compiled-diagnostic-action"),
		Intent:             contract.Intent,
		ActionCategory:     contract.Category,
		Summary:            contract.Summary,
		ExecutionCategory:  normalizeValidationCategory(contract.ExecutionCategory),
		ValidationCategory: normalizeValidationCategory(firstNonEmpty(contract.ValidationCategory, contract.ExecutionCategory)),
		ActuatorSafetyTier: normalizeActuatorSafetyTier(contract.SafetyTier),
		ExecutionLevel:     recommendationExecutionLevel(WorkflowRecommendation{Safe: contract.SafetyTier == ActuatorSafetyTierReadOnly, RequiresApproval: contract.RequiresApproval, DryRunDefault: contract.DryRunDefault}),
		TargetScope:        firstNonEmpty(contract.TargetScope, collectorID),
		Target: ActionTargetRef{
			CollectorID: collectorID,
			Scope:       firstNonEmpty(contract.TargetScope, collectorID),
			Name:        firstNonEmpty(contract.TargetScope, collectorID),
		},
		Preconditions:       append([]string(nil), contract.Preconditions...),
		BlastRadiusEstimate: maxInt(contract.BlastRadiusEstimate, len(blastRadiusScope)),
		BlastRadiusScope:    dedupeStrings(append(append([]string(nil), contract.BlastRadiusScope...), blastRadiusScope...)),
		BlastRadiusNotes:    compactStrings(firstNonEmpty(contract.TargetScope, collectorID)),
		DryRunDefault:       contract.DryRunDefault,
		DryRunState:         actionDryRunState(contract.DryRunDefault, contract.SafetyTier == ActuatorSafetyTierReadOnly, contract.RequiresApproval),
		RequiresApproval:    contract.RequiresApproval,
		ReadOnly:            contract.SafetyTier == ActuatorSafetyTierReadOnly,
		Safe:                contract.SafetyTier == ActuatorSafetyTierReadOnly || contract.SafetyTier == ActuatorSafetyTierSafeReversible,
		Rollback:            contract.Rollback,
		Metadata:            cloneStringMap(contract.Metadata),
	}
}
