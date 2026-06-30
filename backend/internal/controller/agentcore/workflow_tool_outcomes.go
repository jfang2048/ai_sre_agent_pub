package agent

import "strings"

const (
	WorkflowToolOutcomeExecutedSuccess = "executed_success"
	WorkflowToolOutcomeExecutedFailure = "executed_failure"
	WorkflowToolOutcomeProposedOnly    = "proposed_only"
	WorkflowToolOutcomeBlocked         = "blocked"
	WorkflowToolOutcomeSkipped         = "skipped"
	WorkflowToolOutcomeReadOnlySuccess = "read_only_success"
	WorkflowToolOutcomeReadOnlyFailure = "read_only_failure"
	WorkflowToolOutcomePlannedOnly     = "planned_only"
)

func workflowToolReadOnly(name ToolName) bool {
	switch name {
	case ToolRemediation, ToolProfiling:
		return false
	default:
		return true
	}
}

func workflowToolCallOutcome(name ToolName, status string, data any, decision ActionPolicyDecision) string {
	if outcome := workflowToolOutcomeFromData(name, data); outcome != "" {
		return outcome
	}
	status = strings.TrimSpace(status)
	switch status {
	case "blocked":
		return WorkflowToolOutcomeBlocked
	case "approval_required", "execution_requested", "planned_only":
		return WorkflowToolOutcomePlannedOnly
	case "proposal_only":
		return WorkflowToolOutcomeProposedOnly
	case "skipped":
		return WorkflowToolOutcomeSkipped
	case "dry_run_success":
		if workflowToolReadOnly(name) {
			return WorkflowToolOutcomeReadOnlySuccess
		}
		return WorkflowToolOutcomePlannedOnly
	case "success", "cached_success":
		if workflowToolReadOnly(name) {
			return WorkflowToolOutcomeReadOnlySuccess
		}
		if decision.ProposalOnly {
			return WorkflowToolOutcomeProposedOnly
		}
		if decision.DryRunRequired {
			return WorkflowToolOutcomePlannedOnly
		}
		return WorkflowToolOutcomeExecutedSuccess
	case "executed":
		return WorkflowToolOutcomeExecutedSuccess
	case "failed":
		if decision.Status == "blocked" {
			return WorkflowToolOutcomeBlocked
		}
		if workflowToolReadOnly(name) {
			return WorkflowToolOutcomeReadOnlyFailure
		}
		return WorkflowToolOutcomeExecutedFailure
	}
	if decision.ProposalOnly {
		return WorkflowToolOutcomeProposedOnly
	}
	if decision.Status == "blocked" {
		return WorkflowToolOutcomeBlocked
	}
	if workflowToolReadOnly(name) {
		return WorkflowToolOutcomeReadOnlySuccess
	}
	return WorkflowToolOutcomeExecutedSuccess
}

func workflowToolOutcomeCanonical(status string) bool {
	switch strings.TrimSpace(status) {
	case WorkflowToolOutcomeExecutedSuccess,
		WorkflowToolOutcomeExecutedFailure,
		WorkflowToolOutcomeProposedOnly,
		WorkflowToolOutcomeBlocked,
		WorkflowToolOutcomeSkipped,
		WorkflowToolOutcomeReadOnlySuccess,
		WorkflowToolOutcomeReadOnlyFailure,
		WorkflowToolOutcomePlannedOnly:
		return true
	default:
		return false
	}
}

func workflowToolOutcomeFromData(name ToolName, data any) string {
	switch name {
	case ToolRemediation:
		switch typed := data.(type) {
		case remediationToolData:
			return remediationOutcomeFromMode(typed.Mode)
		case ActionResult:
			return actionResultOutcome(typed.Status)
		}
	case ToolProfiling:
		if typed, ok := data.(profilingToolData); ok {
			switch strings.TrimSpace(typed.Mode) {
			case "planned", "dry_run":
				return WorkflowToolOutcomePlannedOnly
			case "guarded":
				return WorkflowToolOutcomeBlocked
			}
		}
	}
	return ""
}

func remediationOutcomeFromMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "proposal_only":
		return WorkflowToolOutcomeProposedOnly
	case "planned_only", "approved_execution_requested", "dry_run":
		return WorkflowToolOutcomePlannedOnly
	case "blocked_by_policy":
		return WorkflowToolOutcomeBlocked
	case "executed":
		return WorkflowToolOutcomeExecutedSuccess
	case "failed":
		return WorkflowToolOutcomeExecutedFailure
	}
	return ""
}

func actionResultOutcome(status string) string {
	switch strings.TrimSpace(status) {
	case ActionResultExecuted:
		return WorkflowToolOutcomeExecutedSuccess
	case ActionResultDryRun:
		return WorkflowToolOutcomePlannedOnly
	case ActionResultSkipped:
		return WorkflowToolOutcomeSkipped
	case ActionResultBlocked:
		return WorkflowToolOutcomeBlocked
	case ActionResultFailed:
		return WorkflowToolOutcomeExecutedFailure
	}
	return ""
}

func workflowToolStoredOutcome(call WorkflowToolCall) string {
	if status := strings.TrimSpace(call.Status); workflowToolOutcomeCanonical(status) {
		return status
	}
	if outcome := strings.TrimSpace(call.Outcome); outcome != "" {
		return outcome
	}
	status := workflowToolInvocationStatus(call)
	if decoded, ok := decodeWorkflowToolPayload(call.Tool, call.ResultPayload); ok {
		return workflowToolCallOutcome(call.Tool, status, decoded, call.Policy)
	}
	return workflowToolCallOutcome(call.Tool, status, nil, call.Policy)
}

func workflowToolInvocationStatus(call WorkflowToolCall) string {
	if status := strings.TrimSpace(call.InvocationStatus); status != "" {
		return status
	}
	if status := strings.TrimSpace(call.Status); status != "" && !workflowToolOutcomeCanonical(status) {
		return status
	}
	return ""
}

func normalizeWorkflowToolCall(call *WorkflowToolCall) bool {
	if call == nil {
		return false
	}
	changed := false
	rawStatus := workflowToolInvocationStatus(*call)
	canonical := workflowToolStoredOutcome(*call)

	if rawStatus == "" {
		switch canonical {
		case WorkflowToolOutcomeExecutedSuccess:
			rawStatus = "executed"
		case WorkflowToolOutcomeExecutedFailure:
			rawStatus = "failed"
		case WorkflowToolOutcomeReadOnlySuccess:
			rawStatus = "success"
		case WorkflowToolOutcomeReadOnlyFailure:
			rawStatus = "failed"
		case WorkflowToolOutcomePlannedOnly:
			rawStatus = "planned_only"
		case WorkflowToolOutcomeProposedOnly:
			rawStatus = "proposal_only"
		case WorkflowToolOutcomeBlocked:
			rawStatus = "blocked"
		case WorkflowToolOutcomeSkipped:
			rawStatus = "skipped"
		}
	}
	if call.InvocationStatus != rawStatus {
		call.InvocationStatus = rawStatus
		changed = true
	}
	if call.Status != canonical {
		call.Status = canonical
		changed = true
	}
	if call.Outcome != canonical {
		call.Outcome = canonical
		changed = true
	}
	return changed
}

func workflowToolCallApproved(call WorkflowToolCall) bool {
	switch workflowToolStoredOutcome(call) {
	case WorkflowToolOutcomeReadOnlySuccess, WorkflowToolOutcomeExecutedSuccess:
		return true
	default:
		return false
	}
}

func workflowToolOutcomeIsFailure(outcome string) bool {
	switch strings.TrimSpace(outcome) {
	case WorkflowToolOutcomeExecutedFailure, WorkflowToolOutcomeReadOnlyFailure:
		return true
	default:
		return false
	}
}

func workflowToolCallReusable(call WorkflowToolCall) bool {
	switch workflowToolStoredOutcome(call) {
	case WorkflowToolOutcomeExecutedSuccess, WorkflowToolOutcomeProposedOnly, WorkflowToolOutcomePlannedOnly:
		return true
	default:
		return false
	}
}

func workflowStageStoredOutcome(stage PipelineStageResult) string {
	if workflowToolOutcomeCanonical(stage.Status) {
		return stage.Status
	}
	if workflowToolOutcomeCanonical(stage.Outcome) {
		return stage.Outcome
	}
	switch strings.TrimSpace(firstNonEmpty(stage.DetailStatus, stage.Status)) {
	case "completed", "success":
		return WorkflowToolOutcomeExecutedSuccess
	case "failed", "error":
		return WorkflowToolOutcomeExecutedFailure
	case "skipped":
		return WorkflowToolOutcomeSkipped
	case "blocked":
		return WorkflowToolOutcomeBlocked
	case "proposal_only":
		return WorkflowToolOutcomeProposedOnly
	case "planned_only", "approval_required", "execution_requested":
		return WorkflowToolOutcomePlannedOnly
	default:
		return ""
	}
}

func normalizePipelineStageResult(stage *PipelineStageResult) bool {
	if stage == nil {
		return false
	}
	changed := false
	detail := strings.TrimSpace(stage.DetailStatus)
	if detail == "" && stage.Status != "" && !workflowToolOutcomeCanonical(stage.Status) {
		detail = stage.Status
	}
	canonical := workflowStageStoredOutcome(*stage)
	if stage.DetailStatus != detail {
		stage.DetailStatus = detail
		changed = true
	}
	if canonical != "" && stage.Status != canonical {
		stage.Status = canonical
		changed = true
	}
	if canonical != "" && stage.Outcome != canonical {
		stage.Outcome = canonical
		changed = true
	}
	return changed
}

func workflowAuditStoredOutcome(status string) string {
	if workflowToolOutcomeCanonical(status) {
		return status
	}
	switch strings.TrimSpace(status) {
	case "success", "completed", "approved", "active":
		return WorkflowToolOutcomeExecutedSuccess
	case "failed", "error":
		return WorkflowToolOutcomeExecutedFailure
	case "blocked", "forbidden", "rejected":
		return WorkflowToolOutcomeBlocked
	case "stopped", "skipped":
		return WorkflowToolOutcomeSkipped
	case "proposal_only":
		return WorkflowToolOutcomeProposedOnly
	case "planned_only", "approval_required", "execution_requested", "pending", "standby":
		return WorkflowToolOutcomePlannedOnly
	default:
		return ""
	}
}

func normalizeWorkflowAuditRecord(record *WorkflowAuditRecord) bool {
	if record == nil {
		return false
	}
	changed := false
	detail := strings.TrimSpace(record.DetailStatus)
	if detail == "" && record.Status != "" && !workflowToolOutcomeCanonical(record.Status) {
		detail = record.Status
	}
	if rawToolStatus := strings.TrimSpace(record.ToolCallStatus); rawToolStatus != "" && (detail == "" || detail == strings.TrimSpace(record.Status)) {
		detail = rawToolStatus
	}
	canonical := workflowAuditStoredOutcome(firstNonEmpty(record.Status, record.Outcome))
	if record.DetailStatus != detail {
		record.DetailStatus = detail
		changed = true
	}
	if canonical != "" && record.Status != canonical {
		record.Status = canonical
		changed = true
	}
	if canonical != "" && record.Outcome != canonical {
		record.Outcome = canonical
		changed = true
	}
	if record.Tool != "" && strings.TrimSpace(record.ToolCallStatus) == "" && record.Input != nil {
		if raw := strings.TrimSpace(record.Input["tool_call_status"]); raw != "" {
			record.ToolCallStatus = raw
			if record.DetailStatus == "" || record.DetailStatus == strings.TrimSpace(record.Status) {
				record.DetailStatus = raw
			}
			changed = true
		}
	}
	return changed
}
