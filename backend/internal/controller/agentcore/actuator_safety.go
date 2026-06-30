package agent

import "strings"

const (
	ActuatorSafetyTierReadOnly       = "read_only"
	ActuatorSafetyTierSafeReversible = "safe_reversible"
	ActuatorSafetyTierImpacting      = "impacting"
	ActuatorSafetyTierDestructive    = "destructive"
)

func normalizeActuatorSafetyTier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "":
		return ""
	case ActuatorSafetyTierReadOnly, "read_only_validation":
		return ActuatorSafetyTierReadOnly
	case ActuatorSafetyTierSafeReversible, "safe", "diagnostic":
		return ActuatorSafetyTierSafeReversible
	case ActuatorSafetyTierImpacting, "contained", "remediation":
		return ActuatorSafetyTierImpacting
	case ActuatorSafetyTierDestructive:
		return ActuatorSafetyTierDestructive
	default:
		return value
	}
}

func actuatorSafetyTierForCandidate(candidate ValidationActionCandidate) string {
	if candidate.ActionContract != nil {
		return actuatorSafetyTierForContract(candidate.ActionContract)
	}
	return classifyActuatorSafetyTier(
		strings.TrimSpace(candidate.Category),
		strings.TrimSpace(candidate.ActionIntent),
		strings.TrimSpace(candidate.ActionCategory),
		strings.TrimSpace(candidate.Summary),
		strings.TrimSpace(candidate.ExpectedImpact),
		candidate.Safe,
		candidate.Reversible || candidate.RollbackContract.Reversible,
		candidate.Metadata,
	)
}

func actuatorSafetyTierForContract(contract *ValidationActionContract) string {
	if contract == nil {
		return ActuatorSafetyTierReadOnly
	}
	if tier := normalizeActuatorSafetyTier(firstNonEmpty(contract.ActuatorSafetyTier, contractMetadataValue(contract.Metadata, "actuator_safety_tier"))); tier != "" {
		return tier
	}
	return classifyActuatorSafetyTier(
		strings.TrimSpace(contract.ExecutionCategory),
		strings.TrimSpace(contract.Intent),
		strings.TrimSpace(contract.ActionCategory),
		strings.TrimSpace(contract.Summary),
		strings.TrimSpace(contract.ExpectedImpact),
		contract.Safe,
		contract.Rollback.Reversible,
		contract.Metadata,
	)
}

func classifyActuatorSafetyTier(executionCategory, intent, actionCategory, summary, expectedImpact string, safe, reversible bool, metadata map[string]string) string {
	if tier := normalizeActuatorSafetyTier(contractMetadataValue(metadata, "actuator_safety_tier")); tier != "" {
		return tier
	}
	category := normalizeValidationCategory(executionCategory)
	if category == "profiling" {
		return ActuatorSafetyTierSafeReversible
	}

	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		intent,
		actionCategory,
		summary,
		expectedImpact,
		contractMetadataValue(metadata, "action_type"),
	}, " ")))

	if looksDestructiveAction(text) {
		return ActuatorSafetyTierDestructive
	}
	if strings.Contains(text, "profile") || strings.Contains(text, "diagnostic") || strings.Contains(text, "trace") {
		return ActuatorSafetyTierSafeReversible
	}
	if safe && reversible && (strings.Contains(text, "capture") || strings.Contains(text, "profile") || strings.Contains(text, "diagnostic")) {
		return ActuatorSafetyTierSafeReversible
	}
	if category == "" || category == "read_only_validation" {
		if !looksWriteLikeAction(text) {
			return ActuatorSafetyTierReadOnly
		}
	}
	return ActuatorSafetyTierImpacting
}

func looksDestructiveAction(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	keywords := []string{
		"delete",
		"destroy",
		"drop",
		"wipe",
		"terminate",
		"shutdown",
		"purge",
		"decommission",
		"rm -",
		"truncate",
	}
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func looksWriteLikeAction(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	keywords := []string{
		"restart",
		"rollback",
		"scale",
		"contain",
		"drain",
		"remediation",
		"revert",
		"patch",
		"apply",
	}
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func contractMetadataValue(metadata map[string]string, key string) string {
	if len(metadata) == 0 {
		return ""
	}
	return strings.TrimSpace(metadata[key])
}

func evaluateActuatorExecutionPolicy(cfg WorkflowConfig, contract *ValidationActionContract, requestedDryRun bool) ActionPolicyDecision {
	tier := actuatorSafetyTierForContract(contract)
	decision := ActionPolicyDecision{
		SafetyTier:        tier,
		ExecutionEligible: false,
		ProposalOnly:      false,
	}

	switch tier {
	case ActuatorSafetyTierReadOnly:
		decision.Status = "allowed"
		decision.Reason = "read-only validation remains executable"
		decision.ExecutionLevel = "read_only"
		decision.ExecutionEligible = true
		return decision
	case ActuatorSafetyTierSafeReversible:
		if !safeReversibleExecutionAllowed(cfg, contractExecutionCategory(contract)) {
			decision.Status = "proposal_only"
			decision.Reason = "safe-reversible actuator execution is disabled by deterministic policy"
			decision.ExecutionLevel = "suggest_only"
			decision.ProposalOnly = true
			decision.MissingConditions = []string{"allow_safe_reversible_exec"}
			return decision
		}
		decision.Status = "allowed"
		decision.Reason = "safe-reversible actuator is eligible under deterministic policy"
		decision.ExecutionEligible = true
		if contractRequiresApproval(contract) {
			decision.ExecutionLevel = "approval_required"
			decision.RequiresApproval = true
		} else if requestedDryRun || contractDryRunDefault(contract) {
			decision.ExecutionLevel = "dry_run_only"
			decision.DryRunRequired = true
		} else {
			decision.ExecutionLevel = "auto_execute"
		}
		return decision
	case ActuatorSafetyTierImpacting:
		if !impactingExecutionAllowed(cfg, contractExecutionCategory(contract)) {
			decision.Status = "proposal_only"
			decision.Reason = "impacting actuator stays proposal-only until deterministic policy explicitly enables execution"
			decision.ExecutionLevel = "suggest_only"
			decision.ProposalOnly = true
			decision.MissingConditions = []string{"allow_impacting_exec"}
			return decision
		}
		decision.Status = "allowed"
		decision.Reason = "impacting actuator is eligible only behind explicit policy and approval gates"
		decision.ExecutionLevel = "approval_required"
		decision.ExecutionEligible = true
		decision.RequiresApproval = true
		decision.RollbackRequired = true
		return decision
	case ActuatorSafetyTierDestructive:
		fallthrough
	default:
		decision.Status = "proposal_only"
		decision.Reason = "destructive actuators are proposal-only in the current controller posture"
		decision.ExecutionLevel = "suggest_only"
		decision.ProposalOnly = true
		decision.RollbackRequired = true
		return decision
	}
}

func safeReversibleExecutionAllowed(cfg WorkflowConfig, category string) bool {
	return cfg.AllowSafeReversibleExec || cfg.AllowProfilingExec || validationExecutionAllowed(cfg, category)
}

func impactingExecutionAllowed(cfg WorkflowConfig, category string) bool {
	if !cfg.AllowImpactingExec {
		return false
	}
	return cfg.AllowRemediationExec || validationExecutionAllowed(cfg, category)
}

func contractExecutionCategory(contract *ValidationActionContract) string {
	if contract == nil {
		return ""
	}
	return normalizeValidationCategory(firstNonEmpty(contract.ExecutionCategory, contract.ValidationCategory))
}

func contractRequiresApproval(contract *ValidationActionContract) bool {
	if contract == nil {
		return false
	}
	return contract.RequiresApproval
}

func contractDryRunDefault(contract *ValidationActionContract) bool {
	if contract == nil {
		return false
	}
	return contract.DryRunDefault
}
