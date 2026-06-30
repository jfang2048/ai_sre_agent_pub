package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (e *WorkflowEngine) emitAnalysisHandoffMessage(ctx context.Context, state *workflowState, handoff AnalysisHandoff) *AgentMessageRef {
	if e == nil || state == nil {
		return nil
	}
	ref := e.emitAgentMessage(ctx, state, "analysis_agent", "validation_action_agent", AgentMessageTypeAnalysisHandoff, nil, AnalysisHandoffMessage{
		Handoff: handoff,
	}, fmt.Sprintf("analysis handoff hypotheses=%d targets=%d actions=%d", len(handoff.HypothesisPackets), len(handoff.SuggestedValidationTargets), len(handoff.BoundedActionCandidates)))
	state.analysisHandoffMessage = ref
	return ref
}

func (e *WorkflowEngine) emitValidationRequestMessage(ctx context.Context, state *workflowState) *AgentMessageRef {
	if e == nil || state == nil || state.analysisHandoffMessage == nil {
		return nil
	}
	ref := e.emitAgentMessage(ctx, state, "workflow_runtime", "validation_action_agent", AgentMessageTypeValidationRequest, state.analysisHandoffMessage, ValidationRequestMessage{
		AnalysisMessage: *state.analysisHandoffMessage,
		TargetLimit:     e.cfg.ValidationTargetLimit,
		ReadOnlyOnly:    e.cfg.ValidationReadOnlyOnly,
		RequestedAt:     time.Now().UTC(),
	}, fmt.Sprintf("validation request target_limit=%d read_only=%t", e.cfg.ValidationTargetLimit, e.cfg.ValidationReadOnlyOnly))
	state.validationRequestMessage = ref
	return ref
}

func (e *WorkflowEngine) emitValidationResultMessage(ctx context.Context, state *workflowState, report ValidationActionReport) *AgentMessageRef {
	if e == nil || state == nil {
		return nil
	}
	ref := e.emitAgentMessage(ctx, state, "validation_action_agent", "workflow_runtime", AgentMessageTypeValidationResult, preferredMessageParent(state, AgentMessageTypeValidationResult), ValidationResultMessage{
		Report: report,
	}, fmt.Sprintf("validation result targets=%d results=%d loops=%d stop=%s", len(report.Targets), len(report.Results), len(report.LoopRecords), firstNonEmpty(report.StopReason, "completed")))
	state.validationResultMessage = ref
	if ref != nil {
		report.ResultMessage = cloneAgentMessageRef(ref)
		state.validationReport.ResultMessage = cloneAgentMessageRef(ref)
	}
	return ref
}

func (e *WorkflowEngine) emitActionDecisionMessage(ctx context.Context, state *workflowState, step *AgentPlanStep) *AgentMessageRef {
	if e == nil || state == nil || state.validationReport.Governance == nil {
		return nil
	}
	ref := e.emitAgentMessage(ctx, state, "validation_action_agent", "workflow_runtime", AgentMessageTypeActionDecision, preferredMessageParent(state, AgentMessageTypeActionDecision), ActionDecisionMessage{
		SelectedAction:         cloneValidationActionCandidate(state.validationReport.SelectedAction),
		SelectedActionContract: cloneValidationActionContract(state.validationReport.SelectedActionContract),
		Governance:             cloneValidationGovernanceTrace(state.validationReport.Governance),
		StepID:                 firstNonEmpty(state.validationReport.Governance.StepID, stepID(step)),
		StepStatus:             firstNonEmpty(state.validationReport.Governance.StepStatus, stepStatus(step)),
		Summary:                truncateString(firstNonEmpty(governanceSummary(state.validationReport.Governance), stepSummary(step)), 220),
	}, truncateString(firstNonEmpty(governanceSummary(state.validationReport.Governance), stepSummary(step), "guarded action decision recorded"), 220))
	state.actionDecisionMessage = ref
	if ref != nil {
		state.validationReport.ActionDecisionMessage = cloneAgentMessageRef(ref)
	}
	return ref
}

func (e *WorkflowEngine) emitPostActionValidationMessage(ctx context.Context, state *workflowState) *AgentMessageRef {
	if e == nil || state == nil || state.validationReport.PostActionValidation == nil {
		return nil
	}
	ref := e.emitAgentMessage(ctx, state, "validation_action_agent", "workflow_runtime", AgentMessageTypePostActionValidation, preferredMessageParent(state, AgentMessageTypePostActionValidation), PostActionValidationMessage{
		Result: *state.validationReport.PostActionValidation,
	}, truncateString(firstNonEmpty(state.validationReport.PostActionValidation.Summary, "post-action validation recorded"), 220))
	state.postActionValidationMessage = ref
	if ref != nil {
		state.validationReport.PostActionValidationMessage = cloneAgentMessageRef(ref)
	}
	return ref
}

func (e *WorkflowEngine) emitCompensationResultMessage(ctx context.Context, state *workflowState, step *AgentPlanStep) *AgentMessageRef {
	if e == nil || state == nil || step == nil {
		return nil
	}
	status := strings.TrimSpace(step.Query["rollback_status"])
	if status == "" {
		return nil
	}
	ref := e.emitAgentMessage(ctx, state, "validation_action_agent", "workflow_runtime", AgentMessageTypeCompensationResult, preferredMessageParent(state, AgentMessageTypeCompensationResult), CompensationResultMessage{
		StepID:      step.ID,
		Governance:  cloneValidationGovernanceTrace(state.validationReport.Governance),
		Status:      status,
		Summary:     strings.TrimSpace(step.Query["rollback_summary"]),
		Error:       compensationErrorForStatus(status, step.Query["rollback_summary"]),
		CompletedAt: time.Now().UTC(),
	}, truncateString(firstNonEmpty(step.Query["rollback_summary"], status), 220))
	state.compensationMessage = ref
	if ref != nil {
		state.validationReport.CompensationMessage = cloneAgentMessageRef(ref)
	}
	return ref
}

func (e *WorkflowEngine) emitAgentMessage(ctx context.Context, state *workflowState, fromAgent, toAgent string, messageType AgentMessageType, parent *AgentMessageRef, payload any, summary string) *AgentMessageRef {
	if e == nil || state == nil || !e.cfg.AgentMessageProtocolEnabled || e.messageStore == nil {
		return nil
	}
	ref, manifestRef, err := e.messageStore.Append(state.workflowID, state.workflowType, fromAgent, toAgent, messageType, parent, payload, summary)
	if err != nil {
		state.limitations = append(state.limitations, fmt.Sprintf("agent message persistence failed for %s", messageType))
		e.logger.Warn("failed to persist agent message", zap.String("run_id", state.workflowID), zap.String("message_type", string(messageType)), zap.Error(err))
		return nil
	}
	updateWorkflowStateMessage(state, ref, manifestRef, e.cfg.AgentMessageHistoryLimit)
	if e.orchestrator != nil && state.durableRun != nil {
		_ = e.orchestrator.AttachAgentMessage(ctx, state.workflowID, ref, manifestRef, e.cfg.AgentMessageHistoryLimit)
	}
	return cloneAgentMessageRef(&ref)
}

func updateWorkflowStateMessage(state *workflowState, ref AgentMessageRef, manifestRef *DurableArtifactRef, historyLimit int) {
	if state == nil {
		return
	}
	if manifestRef != nil {
		state.messageManifestPath = firstNonEmpty(manifestRef.LocalCachePath, manifestRef.Path, state.messageManifestPath)
		if state.durableRun != nil {
			copy := *manifestRef
			state.durableRun.MessageHistoryArtifact = &copy
			state.durableRun.MessageManifestPath = state.messageManifestPath
		}
	}
	state.messageHistory = appendAgentMessageRef(state.messageHistory, ref, historyLimit)
	if state.durableRun != nil {
		state.durableRun.MessageHistory = cloneAgentMessageHistoryRefs(state.messageHistory)
	}
	switch ref.MessageType {
	case AgentMessageTypeAnalysisHandoff:
		state.analysisHandoffMessage = cloneAgentMessageRef(&ref)
		state.validationReport.SourceAnalysisMessage = cloneAgentMessageRef(&ref)
		if state.durableRun != nil {
			state.durableRun.LatestAnalysisHandoffMessage = cloneAgentMessageRef(&ref)
		}
	case AgentMessageTypeValidationRequest:
		state.validationRequestMessage = cloneAgentMessageRef(&ref)
		state.validationReport.SourceValidationRequest = cloneAgentMessageRef(&ref)
		if state.durableRun != nil {
			state.durableRun.LatestValidationRequestMessage = cloneAgentMessageRef(&ref)
		}
	case AgentMessageTypeValidationResult:
		state.validationResultMessage = cloneAgentMessageRef(&ref)
		state.validationReport.ResultMessage = cloneAgentMessageRef(&ref)
		if state.durableRun != nil {
			state.durableRun.LatestValidationResultMessage = cloneAgentMessageRef(&ref)
		}
	case AgentMessageTypeActionDecision:
		state.actionDecisionMessage = cloneAgentMessageRef(&ref)
		state.validationReport.ActionDecisionMessage = cloneAgentMessageRef(&ref)
		if state.durableRun != nil {
			state.durableRun.LatestActionDecisionMessage = cloneAgentMessageRef(&ref)
		}
	case AgentMessageTypePostActionValidation:
		state.postActionValidationMessage = cloneAgentMessageRef(&ref)
		state.validationReport.PostActionValidationMessage = cloneAgentMessageRef(&ref)
		if state.durableRun != nil {
			state.durableRun.LatestPostActionValidationMessage = cloneAgentMessageRef(&ref)
		}
	case AgentMessageTypeCompensationResult:
		state.compensationMessage = cloneAgentMessageRef(&ref)
		state.validationReport.CompensationMessage = cloneAgentMessageRef(&ref)
		if state.durableRun != nil {
			state.durableRun.LatestCompensationMessage = cloneAgentMessageRef(&ref)
		}
	}
}

func workflowMessageMetadata(state *workflowState) map[string]string {
	if state == nil || len(state.messageHistory) == 0 {
		return nil
	}
	meta := map[string]string{
		"agent_message_protocol": "json_file_history",
	}
	if path := strings.TrimSpace(state.messageManifestPath); path != "" {
		meta["agent_message_manifest_path"] = path
	}
	if state.durableRun != nil && state.durableRun.MessageHistoryArtifact != nil {
		meta["agent_message_manifest_artifact_id"] = state.durableRun.MessageHistoryArtifact.ArtifactID
		meta["agent_message_manifest_storage_key"] = state.durableRun.MessageHistoryArtifact.StorageKey
	}
	if state.analysisHandoffMessage != nil {
		meta["analysis_handoff_message_id"] = state.analysisHandoffMessage.MessageID
		meta["analysis_handoff_message_path"] = firstNonEmpty(state.analysisHandoffMessage.LocalCachePath, state.analysisHandoffMessage.Path)
		meta["analysis_handoff_message_artifact_id"] = state.analysisHandoffMessage.ArtifactID
	}
	if state.validationRequestMessage != nil {
		meta["validation_request_message_id"] = state.validationRequestMessage.MessageID
		meta["validation_request_message_path"] = firstNonEmpty(state.validationRequestMessage.LocalCachePath, state.validationRequestMessage.Path)
		meta["validation_request_message_artifact_id"] = state.validationRequestMessage.ArtifactID
	}
	if state.validationResultMessage != nil {
		meta["validation_result_message_id"] = state.validationResultMessage.MessageID
		meta["validation_result_message_path"] = firstNonEmpty(state.validationResultMessage.LocalCachePath, state.validationResultMessage.Path)
		meta["validation_result_message_artifact_id"] = state.validationResultMessage.ArtifactID
	}
	if state.actionDecisionMessage != nil {
		meta["action_decision_message_id"] = state.actionDecisionMessage.MessageID
		meta["action_decision_message_path"] = firstNonEmpty(state.actionDecisionMessage.LocalCachePath, state.actionDecisionMessage.Path)
		meta["action_decision_message_artifact_id"] = state.actionDecisionMessage.ArtifactID
	}
	if state.postActionValidationMessage != nil {
		meta["post_action_validation_message_id"] = state.postActionValidationMessage.MessageID
		meta["post_action_validation_message_path"] = firstNonEmpty(state.postActionValidationMessage.LocalCachePath, state.postActionValidationMessage.Path)
		meta["post_action_validation_message_artifact_id"] = state.postActionValidationMessage.ArtifactID
	}
	if state.compensationMessage != nil {
		meta["compensation_message_id"] = state.compensationMessage.MessageID
		meta["compensation_message_path"] = firstNonEmpty(state.compensationMessage.LocalCachePath, state.compensationMessage.Path)
		meta["compensation_message_artifact_id"] = state.compensationMessage.ArtifactID
	}
	return meta
}

func preferredMessageParent(state *workflowState, messageType AgentMessageType) *AgentMessageRef {
	if state == nil {
		return nil
	}
	switch messageType {
	case AgentMessageTypeValidationResult:
		if state.validationRequestMessage != nil {
			return state.validationRequestMessage
		}
		return state.analysisHandoffMessage
	case AgentMessageTypeActionDecision:
		if state.validationResultMessage != nil {
			return state.validationResultMessage
		}
		if state.validationRequestMessage != nil {
			return state.validationRequestMessage
		}
		return state.analysisHandoffMessage
	case AgentMessageTypePostActionValidation:
		if state.actionDecisionMessage != nil {
			return state.actionDecisionMessage
		}
		return preferredMessageParent(state, AgentMessageTypeActionDecision)
	case AgentMessageTypeCompensationResult:
		if state.postActionValidationMessage != nil {
			return state.postActionValidationMessage
		}
		return preferredMessageParent(state, AgentMessageTypePostActionValidation)
	default:
		return nil
	}
}

func syncRCAMessageRefs(report *RCAWorkflowReport, state *workflowState) {
	if report == nil || state == nil {
		return
	}
	report.MessageManifestPath = strings.TrimSpace(state.messageManifestPath)
	if state.durableRun != nil && state.durableRun.MessageHistoryArtifact != nil {
		copy := *state.durableRun.MessageHistoryArtifact
		report.MessageHistoryArtifact = &copy
	}
	report.MessageHistory = cloneAgentMessageHistoryRefs(state.messageHistory)
	report.LatestAnalysisHandoffMessage = cloneAgentMessageRef(state.analysisHandoffMessage)
	report.LatestValidationRequestMessage = cloneAgentMessageRef(state.validationRequestMessage)
	report.LatestValidationResultMessage = cloneAgentMessageRef(state.validationResultMessage)
	report.LatestActionDecisionMessage = cloneAgentMessageRef(state.actionDecisionMessage)
	report.LatestPostActionValidationMessage = cloneAgentMessageRef(state.postActionValidationMessage)
	report.LatestCompensationMessage = cloneAgentMessageRef(state.compensationMessage)
}

func cloneValidationGovernanceTrace(trace *ValidationGovernanceTrace) *ValidationGovernanceTrace {
	if trace == nil {
		return nil
	}
	copy := *trace
	copy.BlastRadiusNotes = append([]string(nil), trace.BlastRadiusNotes...)
	return &copy
}

func cloneValidationActionCandidate(candidate *ValidationActionCandidate) *ValidationActionCandidate {
	if candidate == nil {
		return nil
	}
	copy := *candidate
	copy.BlastRadiusScope = append([]string(nil), candidate.BlastRadiusScope...)
	copy.Preconditions = append([]string(nil), candidate.Preconditions...)
	copy.ActionContract = cloneValidationActionContract(candidate.ActionContract)
	copy.Metadata = cloneStringMap(candidate.Metadata)
	return &copy
}

func governanceSummary(trace *ValidationGovernanceTrace) string {
	if trace == nil {
		return ""
	}
	return firstNonEmpty(trace.ActionSummary, trace.ActionIntent, trace.ActionCandidateID, trace.ActionContractID)
}

func compensationErrorForStatus(status, summary string) string {
	switch strings.TrimSpace(status) {
	case "rollback_failed":
		return strings.TrimSpace(summary)
	default:
		return ""
	}
}

func stepID(step *AgentPlanStep) string {
	if step == nil {
		return ""
	}
	return strings.TrimSpace(step.ID)
}

func stepStatus(step *AgentPlanStep) string {
	if step == nil {
		return ""
	}
	return strings.TrimSpace(step.Status)
}

func stepSummary(step *AgentPlanStep) string {
	if step == nil {
		return ""
	}
	return firstNonEmpty(strings.TrimSpace(step.ResultSummary), strings.TrimSpace(step.VerificationNote), strings.TrimSpace(step.Title))
}
