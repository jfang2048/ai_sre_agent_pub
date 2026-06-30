package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ActionTargetRef struct {
	CollectorID string `json:"collector_id,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Resource    string `json:"resource,omitempty"`
	Name        string `json:"name,omitempty"`
}

type RollbackContract struct {
	Summary    string `json:"summary,omitempty"`
	Command    string `json:"command,omitempty"`
	Required   bool   `json:"required,omitempty"`
	Reversible bool   `json:"reversible,omitempty"`
}

type ValidationActionContract struct {
	ID                  string            `json:"id"`
	Intent              string            `json:"intent,omitempty"`
	ActionCategory      string            `json:"action_category,omitempty"`
	Summary             string            `json:"summary,omitempty"`
	ExecutionCategory   string            `json:"execution_category,omitempty"`
	ValidationCategory  string            `json:"validation_category,omitempty"`
	ActuatorSafetyTier  string            `json:"actuator_safety_tier,omitempty"`
	ExecutionLevel      string            `json:"execution_level,omitempty"`
	TargetScope         string            `json:"target_scope,omitempty"`
	Target              ActionTargetRef   `json:"target"`
	Preconditions       []string          `json:"preconditions,omitempty"`
	ExpectedImpact      string            `json:"expected_impact,omitempty"`
	BlastRadiusEstimate int               `json:"blast_radius_estimate,omitempty"`
	BlastRadiusScope    []string          `json:"blast_radius_scope,omitempty"`
	BlastRadiusNotes    []string          `json:"blast_radius_notes,omitempty"`
	DryRunDefault       bool              `json:"dry_run_default,omitempty"`
	DryRunState         string            `json:"dry_run_state,omitempty"`
	RequiresApproval    bool              `json:"requires_approval,omitempty"`
	ReadOnly            bool              `json:"read_only,omitempty"`
	Safe                bool              `json:"safe,omitempty"`
	Rollback            RollbackContract  `json:"rollback"`
	Execution           *ActionSpec       `json:"execution,omitempty"`
	Metadata            map[string]string `json:"metadata,omitempty"`
}

func buildValidationActionContract(candidate ValidationActionCandidate, collectorID string, blastRadiusScope []string) ValidationActionContract {
	contract := ValidationActionContract{
		ID:                 firstNonEmpty(strings.TrimSpace(candidate.ID), fmt.Sprintf("contract-%s", sanitizeID(candidate.Summary))),
		Intent:             firstNonEmpty(strings.TrimSpace(candidate.ActionIntent), actionIntentFromText(candidate.Summary, candidate.ExpectedImpact, candidate.RollbackHint)),
		ActionCategory:     firstNonEmpty(strings.TrimSpace(candidate.ActionCategory), actionCategoryFromIntent(firstNonEmpty(strings.TrimSpace(candidate.ActionIntent), actionIntentFromText(candidate.Summary, candidate.ExpectedImpact, candidate.RollbackHint)))),
		Summary:            strings.TrimSpace(candidate.Summary),
		ExecutionCategory:  normalizeValidationCategory(candidate.Category),
		ValidationCategory: normalizeValidationCategory(candidate.Category),
		ActuatorSafetyTier: normalizeActuatorSafetyTier(candidate.ActuatorSafetyTier),
		ExecutionLevel:     strings.TrimSpace(candidate.ExecutionLevel),
		TargetScope:        strings.TrimSpace(firstNonEmpty(candidate.Scope, collectorID)),
		Target: ActionTargetRef{
			CollectorID: strings.TrimSpace(collectorID),
			Scope:       strings.TrimSpace(firstNonEmpty(candidate.Scope, collectorID)),
			Name:        strings.TrimSpace(firstNonEmpty(candidate.Scope, collectorID)),
		},
		Preconditions:       append([]string(nil), candidate.Preconditions...),
		ExpectedImpact:      strings.TrimSpace(candidate.ExpectedImpact),
		BlastRadiusEstimate: maxInt(candidate.BlastRadiusEstimate, len(blastRadiusScope)),
		BlastRadiusScope:    dedupeStrings(append([]string(nil), blastRadiusScope...)),
		BlastRadiusNotes:    actionBlastRadiusNotes(candidate, collectorID, blastRadiusScope),
		DryRunDefault:       candidate.DryRunDefault,
		DryRunState:         actionDryRunState(candidate.DryRunDefault, candidate.Safe, candidate.RequiresApproval),
		RequiresApproval:    candidate.RequiresApproval,
		ReadOnly:            normalizeValidationCategory(candidate.Category) == "read_only_validation",
		Safe:                candidate.Safe,
		Rollback: RollbackContract{
			Summary:    strings.TrimSpace(firstNonEmpty(candidate.RollbackContract.Summary, candidate.RollbackHint)),
			Command:    strings.TrimSpace(candidate.RollbackContract.Command),
			Required:   candidate.Reversible || strings.TrimSpace(candidate.RollbackHint) != "" || strings.TrimSpace(candidate.RollbackContract.Summary) != "",
			Reversible: candidate.Reversible || candidate.RollbackContract.Reversible,
		},
		Metadata: cloneStringMap(candidate.Metadata),
	}
	if contract.Metadata == nil {
		contract.Metadata = map[string]string{}
	}
	if contract.ActuatorSafetyTier == "" {
		contract.ActuatorSafetyTier = actuatorSafetyTierForContract(&contract)
	}
	contract.Metadata["action_category"] = contract.ActionCategory
	contract.Metadata["validation_category"] = contract.ValidationCategory
	contract.Metadata["target_scope"] = contract.TargetScope
	contract.Metadata["dry_run_state"] = contract.DryRunState
	contract.Metadata["actuator_safety_tier"] = contract.ActuatorSafetyTier
	if contract.BlastRadiusEstimate > 0 {
		contract.Metadata["blast_radius_estimate"] = fmt.Sprintf("%d", contract.BlastRadiusEstimate)
	}
	if contract.Target.Scope != "" {
		contract.Metadata["scope"] = contract.Target.Scope
	}
	if candidate.ActionContract != nil && candidate.ActionContract.Execution != nil {
		contract.Execution = cloneActionSpec(candidate.ActionContract.Execution)
	}
	return contract
}

func encodeValidationActionContract(contract ValidationActionContract) string {
	raw, err := json.Marshal(contract)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeValidationActionContract(raw string) (*ValidationActionContract, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var contract ValidationActionContract
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		return nil, err
	}
	if strings.TrimSpace(contract.ID) == "" {
		contract.ID = fmt.Sprintf("contract-%s", sanitizeID(firstNonEmpty(contract.Summary, contract.Intent, "remediation")))
	}
	contract.ExecutionCategory = normalizeValidationCategory(contract.ExecutionCategory)
	contract.ValidationCategory = normalizeValidationCategory(firstNonEmpty(contract.ValidationCategory, contract.ExecutionCategory))
	contract.ActuatorSafetyTier = normalizeActuatorSafetyTier(contract.ActuatorSafetyTier)
	contract.ActionCategory = strings.TrimSpace(firstNonEmpty(contract.ActionCategory, actionCategoryFromIntent(contract.Intent)))
	contract.TargetScope = strings.TrimSpace(firstNonEmpty(contract.TargetScope, contract.Target.Scope))
	contract.BlastRadiusScope = dedupeStrings(contract.BlastRadiusScope)
	contract.BlastRadiusNotes = dedupeStrings(contract.BlastRadiusNotes)
	contract.Preconditions = dedupeStrings(contract.Preconditions)
	contract.DryRunState = strings.TrimSpace(firstNonEmpty(contract.DryRunState, actionDryRunState(contract.DryRunDefault, contract.Safe, contract.RequiresApproval)))
	contract.Metadata = cloneStringMap(contract.Metadata)
	if contract.ActuatorSafetyTier == "" {
		contract.ActuatorSafetyTier = actuatorSafetyTierForContract(&contract)
	}
	if contract.Metadata == nil {
		contract.Metadata = map[string]string{}
	}
	contract.Metadata["actuator_safety_tier"] = contract.ActuatorSafetyTier
	return &contract, nil
}

func cloneValidationActionContract(in *ValidationActionContract) *ValidationActionContract {
	if in == nil {
		return nil
	}
	copy := *in
	copy.Preconditions = append([]string(nil), in.Preconditions...)
	copy.BlastRadiusScope = append([]string(nil), in.BlastRadiusScope...)
	copy.BlastRadiusNotes = append([]string(nil), in.BlastRadiusNotes...)
	copy.Metadata = cloneStringMap(in.Metadata)
	copy.Execution = cloneActionSpec(in.Execution)
	return &copy
}

func actionIntentFromText(values ...string) string {
	text := strings.ToLower(strings.TrimSpace(strings.Join(values, " ")))
	switch {
	case strings.Contains(text, "rollback"), strings.Contains(text, "revert"):
		return "rollback_revision"
	case strings.Contains(text, "restart"):
		return "restart_workload"
	case strings.Contains(text, "scale"):
		return "scale_workload"
	case strings.Contains(text, "isolate"), strings.Contains(text, "contain"):
		return "contain_workload"
	case strings.Contains(text, "drain"):
		return "drain_target"
	case strings.Contains(text, "profile"):
		return "capture_profile"
	default:
		return "targeted_remediation"
	}
}

func actionCategoryFromIntent(intent string) string {
	switch strings.TrimSpace(intent) {
	case "rollback_revision":
		return "rollback"
	case "restart_workload", "scale_workload", "contain_workload", "drain_target":
		return "containment"
	case "capture_profile":
		return "diagnostic"
	default:
		return "remediation"
	}
}

func actionBlastRadiusNotes(candidate ValidationActionCandidate, collectorID string, blastRadiusScope []string) []string {
	notes := make([]string, 0, 4)
	scope := strings.TrimSpace(firstNonEmpty(candidate.Scope, collectorID))
	if scope != "" {
		notes = append(notes, fmt.Sprintf("target scope: %s", scope))
	}
	if len(blastRadiusScope) > 0 {
		notes = append(notes, fmt.Sprintf("downstream scope touched: %s", strings.Join(dedupeStrings(blastRadiusScope), ", ")))
	}
	if candidate.BlastRadiusEstimate > 1 {
		notes = append(notes, fmt.Sprintf("blast radius estimate: %d scoped targets", candidate.BlastRadiusEstimate))
	}
	if candidate.RequiresApproval {
		notes = append(notes, "human approval remains mandatory before any non-read-only execution")
	}
	return dedupeStrings(notes)
}

func actionDryRunState(dryRunDefault, safe, requiresApproval bool) string {
	switch {
	case dryRunDefault:
		return "dry_run_default"
	case requiresApproval || !safe:
		return "approval_gated"
	default:
		return "execution_ready_when_allowed"
	}
}
