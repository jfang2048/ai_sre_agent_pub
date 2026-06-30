package agent

import (
	"fmt"
	"strings"
)

func ensureGuardedActionPlanStep(state *workflowState, candidate ValidationActionCandidate, contract ValidationActionContract, decision ActionPolicyDecision) *AgentPlanStep {
	if state == nil {
		return nil
	}
	stepID := fmt.Sprintf("guarded-action-%s", sanitizeID(firstNonEmpty(candidate.ID, contract.ID, contract.Intent, candidate.Summary)))
	query := map[string]string{
		"action":              firstNonEmpty(contract.Summary, contract.Intent, candidate.Summary),
		"action_intent":       firstNonEmpty(contract.Intent, candidate.ActionIntent),
		"action_category":     firstNonEmpty(contract.ActionCategory, candidate.ActionCategory),
		"scope":               firstNonEmpty(contract.TargetScope, contract.Target.Scope, candidate.Scope),
		"validation_category": firstNonEmpty(contract.ValidationCategory, contract.ExecutionCategory, candidate.Category),
		"safety_tier":         firstNonEmpty(contract.ActuatorSafetyTier, candidate.ActuatorSafetyTier),
		"proposal_only":       fmt.Sprintf("%t", decision.ProposalOnly),
		"execution_eligible":  fmt.Sprintf("%t", decision.ExecutionEligible),
		"requires_approval":   fmt.Sprintf("%t", contract.RequiresApproval),
		"dry_run":             fmt.Sprintf("%t", state.dryRun || contract.DryRunDefault),
		"dry_run_state":       contract.DryRunState,
		"policy_status":       strings.TrimSpace(decision.Status),
		"policy_reason":       strings.TrimSpace(decision.Reason),
	}
	for idx := range state.planSteps {
		if state.planSteps[idx].ID != stepID {
			continue
		}
		state.planSteps[idx].Title = fmt.Sprintf("Guarded action: %s", firstNonEmpty(contract.Summary, candidate.Summary, contract.Intent))
		state.planSteps[idx].Objective = firstNonEmpty(contract.ExpectedImpact, candidate.ExpectedImpact, contract.Summary)
		state.planSteps[idx].Tool = ToolRemediation
		state.planSteps[idx].Query = cloneStringMap(query)
		state.planSteps[idx].ActionContract = cloneValidationActionContract(&contract)
		state.planSteps[idx].Status = firstNonEmpty(state.planSteps[idx].Status, "planned")
		return &state.planSteps[idx]
	}
	state.planSteps = append(state.planSteps, AgentPlanStep{
		ID:             stepID,
		Order:          len(state.planSteps) + 1,
		Iteration:      maxInt(state.planIterations, 1) + 1,
		Title:          fmt.Sprintf("Guarded action: %s", firstNonEmpty(contract.Summary, candidate.Summary, contract.Intent)),
		Objective:      firstNonEmpty(contract.ExpectedImpact, candidate.ExpectedImpact, contract.Summary),
		Tool:           ToolRemediation,
		Required:       false,
		Query:          cloneStringMap(query),
		Status:         "planned",
		ActionContract: cloneValidationActionContract(&contract),
	})
	return &state.planSteps[len(state.planSteps)-1]
}

func findPlanStepByID(state *workflowState, stepID string) *AgentPlanStep {
	if state == nil || strings.TrimSpace(stepID) == "" {
		return nil
	}
	for idx := range state.planSteps {
		if state.planSteps[idx].ID == stepID {
			return &state.planSteps[idx]
		}
	}
	return nil
}

func contractIDFromStep(step *AgentPlanStep) string {
	if step == nil || step.ActionContract == nil {
		return ""
	}
	return strings.TrimSpace(step.ActionContract.ID)
}

func appendGuardedActionRevision(state *workflowState, reason string) {
	if state == nil || len(state.planSteps) == 0 {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "guarded action revision"
	}
	revision := AgentPlanRevision{
		Iteration: maxInt(state.planIterations, 1) + len(state.planRevisions),
		Reason:    reason,
		CreatedAt: state.now,
		Steps:     append([]AgentPlanStep(nil), state.planSteps...),
	}
	if len(state.planRevisions) > 0 {
		last := state.planRevisions[len(state.planRevisions)-1]
		if last.Reason == revision.Reason && len(last.Steps) == len(revision.Steps) {
			state.planRevisions[len(state.planRevisions)-1] = revision
			return
		}
	}
	state.planRevisions = append(state.planRevisions, revision)
}

func setValidationGovernanceFromContract(report *ValidationActionReport, candidate *ValidationActionCandidate, contract *ValidationActionContract, decision ActionPolicyDecision, dryRun bool) {
	if report == nil || contract == nil {
		return
	}
	trace := &ValidationGovernanceTrace{
		ActionSummary:      strings.TrimSpace(firstNonEmpty(contract.Summary, contract.Intent)),
		ActionIntent:       strings.TrimSpace(contract.Intent),
		ActionCategory:     strings.TrimSpace(contract.ActionCategory),
		TargetScope:        strings.TrimSpace(firstNonEmpty(contract.TargetScope, contract.Target.Scope)),
		ExecutionCategory:  normalizeValidationCategory(contract.ExecutionCategory),
		ValidationCategory: normalizeValidationCategory(firstNonEmpty(contract.ValidationCategory, contract.ExecutionCategory)),
		ActuatorSafetyTier: normalizeActuatorSafetyTier(firstNonEmpty(contract.ActuatorSafetyTier, decision.SafetyTier)),
		ProposalOnly:       decision.ProposalOnly,
		ExecutionEligible:  decision.ExecutionEligible,
		DryRun:             dryRun || contract.DryRunDefault,
		DryRunState:        strings.TrimSpace(contract.DryRunState),
		RequiresApproval:   contract.RequiresApproval,
		PolicyStatus:       strings.TrimSpace(decision.Status),
		PolicyReason:       strings.TrimSpace(decision.Reason),
		BlastRadiusNotes:   append([]string(nil), contract.BlastRadiusNotes...),
		ActionContractID:   strings.TrimSpace(contract.ID),
	}
	if candidate != nil {
		trace.ActionCandidateID = strings.TrimSpace(candidate.ID)
		trace.ActionSummary = firstNonEmpty(trace.ActionSummary, strings.TrimSpace(candidate.Summary))
		trace.ActionIntent = firstNonEmpty(trace.ActionIntent, strings.TrimSpace(candidate.ActionIntent))
		trace.ActionCategory = firstNonEmpty(trace.ActionCategory, strings.TrimSpace(candidate.ActionCategory))
		trace.ActuatorSafetyTier = firstNonEmpty(trace.ActuatorSafetyTier, normalizeActuatorSafetyTier(candidate.ActuatorSafetyTier))
		trace.TargetScope = firstNonEmpty(trace.TargetScope, strings.TrimSpace(candidate.Scope))
	}
	report.SelectedActionContract = cloneValidationActionContract(contract)
	report.Governance = trace
}

func updateValidationGovernanceFromStep(report *ValidationActionReport, step *AgentPlanStep) {
	if report == nil || step == nil {
		return
	}
	if report.Governance == nil {
		report.Governance = &ValidationGovernanceTrace{}
	}
	report.Governance.StepID = step.ID
	report.Governance.StepStatus = step.Status
	report.Governance.TargetScope = firstNonEmpty(report.Governance.TargetScope, strings.TrimSpace(step.Query["scope"]))
	report.Governance.ActionIntent = firstNonEmpty(report.Governance.ActionIntent, strings.TrimSpace(step.Query["action_intent"]))
	report.Governance.ActionCategory = firstNonEmpty(report.Governance.ActionCategory, strings.TrimSpace(step.Query["action_category"]))
	report.Governance.ExecutionCategory = firstNonEmpty(report.Governance.ExecutionCategory, normalizeValidationCategory(step.Query["validation_category"]))
	report.Governance.ValidationCategory = firstNonEmpty(report.Governance.ValidationCategory, normalizeValidationCategory(step.Query["validation_category"]))
	report.Governance.ActuatorSafetyTier = firstNonEmpty(report.Governance.ActuatorSafetyTier, normalizeActuatorSafetyTier(step.Query["safety_tier"]))
	report.Governance.ProposalOnly = report.Governance.ProposalOnly || parseBoolFromString(step.Query["proposal_only"], false)
	report.Governance.ExecutionEligible = report.Governance.ExecutionEligible || parseBoolFromString(step.Query["execution_eligible"], false)
	report.Governance.DryRun = report.Governance.DryRun || parseBoolFromString(step.Query["dry_run"], false)
	report.Governance.DryRunState = firstNonEmpty(report.Governance.DryRunState, strings.TrimSpace(step.Query["dry_run_state"]))
	report.Governance.RequiresApproval = report.Governance.RequiresApproval || parseBoolFromString(step.Query["requires_approval"], false)
	report.Governance.PolicyStatus = firstNonEmpty(report.Governance.PolicyStatus, strings.TrimSpace(step.Query["policy_status"]))
	report.Governance.PolicyReason = firstNonEmpty(report.Governance.PolicyReason, strings.TrimSpace(step.Query["policy_reason"]))
}

func updateValidationGovernanceFromToolCall(report *ValidationActionReport, step *AgentPlanStep, call WorkflowToolCall) {
	if report == nil {
		return
	}
	if report.Governance == nil {
		report.Governance = &ValidationGovernanceTrace{}
	}
	report.Governance.ToolCallID = firstNonEmpty(call.ID, report.Governance.ToolCallID)
	report.Governance.PolicyStatus = firstNonEmpty(call.Policy.Status, report.Governance.PolicyStatus)
	report.Governance.PolicyReason = firstNonEmpty(call.Policy.Reason, report.Governance.PolicyReason)
	report.Governance.ActuatorSafetyTier = firstNonEmpty(call.Policy.SafetyTier, report.Governance.ActuatorSafetyTier)
	report.Governance.ProposalOnly = report.Governance.ProposalOnly || call.Policy.ProposalOnly
	report.Governance.ExecutionEligible = report.Governance.ExecutionEligible || call.Policy.ExecutionEligible
	report.Governance.ApprovalState = firstNonEmpty(call.ApprovalState, report.Governance.ApprovalState)
	report.Governance.ExecutionCategory = firstNonEmpty(call.ExecutionCategory, report.Governance.ExecutionCategory)
	report.Governance.ActionIntent = firstNonEmpty(call.ActionIntent, report.Governance.ActionIntent)
	if step != nil {
		updateValidationGovernanceFromStep(report, step)
	}
}

func updateValidationGovernanceRollback(report *ValidationActionReport, status string) {
	if report == nil {
		return
	}
	if report.Governance == nil {
		report.Governance = &ValidationGovernanceTrace{}
	}
	report.Governance.RollbackStatus = strings.TrimSpace(status)
}

func remediationStepStatus(data remediationToolData, call WorkflowToolCall) string {
	switch strings.TrimSpace(data.Mode) {
	case "executed":
		return "executed"
	case "dry_run":
		return "dry_run"
	case "planned_only":
		return "planned_only"
	case "proposal_only":
		return "proposal_only"
	case "approved_execution_requested":
		return "approval_required"
	case "blocked_by_policy":
		return "blocked"
	case "failed":
		return "failed"
	default:
		if call.ApprovalState == "pending" {
			return "approval_required"
		}
		if call.Policy.Status == "blocked" {
			return "blocked"
		}
		return firstNonEmpty(strings.TrimSpace(data.Mode), workflowToolInvocationStatus(call), "planned_only")
	}
}

func guardedActionExecuted(step *AgentPlanStep) bool {
	if step == nil {
		return false
	}
	switch strings.TrimSpace(step.Status) {
	case "executed", "verified", "verification_failed", "reverted", "rollback_failed":
		return true
	default:
		return false
	}
}

func guardedActionSkippedSummary(step *AgentPlanStep) string {
	if step == nil {
		return "no guarded remediation executed; validation agent stayed read-only"
	}
	action := firstNonEmpty(step.Title, step.Query["action"], "guarded remediation")
	switch strings.TrimSpace(step.Status) {
	case "dry_run":
		return fmt.Sprintf("%s stayed in dry-run mode; no live state change was applied", action)
	case "planned_only":
		return fmt.Sprintf("%s was planned only; no executable remediation contract was applied", action)
	case "proposal_only":
		return fmt.Sprintf("%s remained proposal-only under the actuator safety policy; no live state change was applied", action)
	case "approval_required":
		return fmt.Sprintf("%s is waiting for human approval; no live state change was applied", action)
	case "blocked":
		return fmt.Sprintf("%s was blocked by execution policy; no live state change was applied", action)
	case "failed":
		return fmt.Sprintf("%s failed before any verified effect comparison could run", action)
	default:
		return fmt.Sprintf("%s did not reach live execution; post-action validation stayed informational", action)
	}
}
