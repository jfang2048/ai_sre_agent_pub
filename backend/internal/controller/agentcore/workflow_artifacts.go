package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
)

const workflowArtifactSchemaVersion = "ai_sre_agent/workflow_artifacts/v1"

type WorkflowArtifactKind string

const (
	WorkflowArtifactObservationSummary     WorkflowArtifactKind = "observation_summary"
	WorkflowArtifactAnomalyFinding         WorkflowArtifactKind = "anomaly_finding"
	WorkflowArtifactRootCauseHypothesis    WorkflowArtifactKind = "root_cause_hypothesis"
	WorkflowArtifactRemediationProposal    WorkflowArtifactKind = "remediation_proposal"
	WorkflowArtifactExecutionPlan          WorkflowArtifactKind = "execution_plan"
	WorkflowArtifactExecutionResult        WorkflowArtifactKind = "execution_result"
	WorkflowArtifactVerificationResult     WorkflowArtifactKind = "verification_result"
	WorkflowArtifactIncidentReport         WorkflowArtifactKind = "incident_report"
	WorkflowArtifactRuntimeState           WorkflowArtifactKind = "runtime_state"
	WorkflowArtifactObjectiveState         WorkflowArtifactKind = "objective_state"
	WorkflowArtifactEvidenceGapSet         WorkflowArtifactKind = "evidence_gap_set"
	WorkflowArtifactPlannerProposal        WorkflowArtifactKind = "planner_proposal"
	WorkflowArtifactCritiqueReport         WorkflowArtifactKind = "critique_report"
	WorkflowArtifactToolCandidateScores    WorkflowArtifactKind = "tool_candidate_scores"
	WorkflowArtifactToolDecision           WorkflowArtifactKind = "tool_decision"
	WorkflowArtifactNormalizedToolResult   WorkflowArtifactKind = "normalized_tool_result"
	WorkflowArtifactToolResultSummary      WorkflowArtifactKind = "tool_result_summary"
	WorkflowArtifactProgressAssessment     WorkflowArtifactKind = "progress_assessment"
	WorkflowArtifactCritiqueResult         WorkflowArtifactKind = "critique_result"
	WorkflowArtifactHypothesisRevision     WorkflowArtifactKind = "hypothesis_revision"
	WorkflowArtifactBranchDecision         WorkflowArtifactKind = "branch_decision"
	WorkflowArtifactStopReason             WorkflowArtifactKind = "stop_reason"
	WorkflowArtifactStopDecision           WorkflowArtifactKind = "stop_decision"
	WorkflowArtifactExecutionIntent        WorkflowArtifactKind = "execution_intent"
	WorkflowArtifactVerificationDelta      WorkflowArtifactKind = "verification_delta"
	WorkflowArtifactExperienceMemoryUpdate WorkflowArtifactKind = "experience_memory_update"
)

type WorkflowArtifactMeta struct {
	SchemaVersion     string               `json:"schema_version"`
	Version           string               `json:"version"`
	Kind              WorkflowArtifactKind `json:"kind"`
	ArtifactID        string               `json:"artifact_id"`
	RunID             string               `json:"run_id"`
	IncidentID        string               `json:"incident_id,omitempty"`
	CorrelationID     string               `json:"correlation_id,omitempty"`
	Producer          string               `json:"producer"`
	Consumer          string               `json:"consumer,omitempty"`
	Status            string               `json:"status"`
	ProducedAt        time.Time            `json:"produced_at"`
	UpdatedAt         time.Time            `json:"updated_at,omitempty"`
	InputArtifacts    []string             `json:"input_artifacts,omitempty"`
	Replayable        bool                 `json:"replayable"`
	Retryable         bool                 `json:"retryable"`
	ReplayPoint       string               `json:"replay_point,omitempty"`
	Sequence          int                  `json:"sequence,omitempty"`
	Attempt           int                  `json:"attempt,omitempty"`
	ParentArtifactID  string               `json:"parent_artifact_id,omitempty"`
	RetryOfArtifactID string               `json:"retry_of_artifact_id,omitempty"`
	ParentMessageID   string               `json:"parent_message_id,omitempty"`
	MessageID         string               `json:"message_id,omitempty"`
}

type ObservationSummaryArtifact struct {
	Meta            WorkflowArtifactMeta      `json:"meta"`
	CollectorID     string                    `json:"collector_id,omitempty"`
	Trigger         string                    `json:"trigger,omitempty"`
	Window          string                    `json:"window,omitempty"`
	Summary         string                    `json:"summary"`
	Signals         []string                  `json:"signals,omitempty"`
	EvidenceIDs     []string                  `json:"evidence_ids,omitempty"`
	RawEvidenceRefs []evidencev1.RawReference `json:"raw_evidence_refs,omitempty"`
}

type AnomalyFindingArtifact struct {
	Meta            WorkflowArtifactMeta      `json:"meta"`
	RiskLevel       string                    `json:"risk_level,omitempty"`
	Confidence      float64                   `json:"confidence,omitempty"`
	Summary         string                    `json:"summary"`
	Anomalies       []string                  `json:"anomalies,omitempty"`
	EvidenceIDs     []string                  `json:"evidence_ids,omitempty"`
	RawEvidenceRefs []evidencev1.RawReference `json:"raw_evidence_refs,omitempty"`
}

type RootCauseHypothesisArtifact struct {
	Meta                     WorkflowArtifactMeta `json:"meta"`
	Title                    string               `json:"title,omitempty"`
	Summary                  string               `json:"summary"`
	Confidence               float64              `json:"confidence,omitempty"`
	EvidenceIDs              []string             `json:"evidence_ids,omitempty"`
	ContradictingEvidenceIDs []string             `json:"contradicting_evidence_ids,omitempty"`
	ExpectedSignals          []string             `json:"expected_signals,omitempty"`
}

type RemediationProposalArtifact struct {
	Meta               WorkflowArtifactMeta `json:"meta"`
	Summary            string               `json:"summary"`
	RecommendationIDs  []string             `json:"recommendation_ids,omitempty"`
	ActionCandidateIDs []string             `json:"action_candidate_ids,omitempty"`
	RiskLevel          string               `json:"risk_level,omitempty"`
	ReadOnlyOnly       bool                 `json:"read_only_only,omitempty"`
	RequiresApproval   bool                 `json:"requires_approval,omitempty"`
	RollbackHints      []string             `json:"rollback_hints,omitempty"`
	EvidenceIDs        []string             `json:"evidence_ids,omitempty"`
}

type ExecutionPlanArtifact struct {
	Meta             WorkflowArtifactMeta `json:"meta"`
	Summary          string               `json:"summary"`
	SelectedActionID string               `json:"selected_action_id,omitempty"`
	ActionContractID string               `json:"action_contract_id,omitempty"`
	StepIDs          []string             `json:"step_ids,omitempty"`
	ToolCalls        []string             `json:"tool_calls,omitempty"`
	PolicyStatus     string               `json:"policy_status,omitempty"`
	EvidenceIDs      []string             `json:"evidence_ids,omitempty"`
}

type ExecutionResultArtifact struct {
	Meta             WorkflowArtifactMeta `json:"meta"`
	Summary          string               `json:"summary"`
	Outcome          string               `json:"outcome,omitempty"`
	SelectedActionID string               `json:"selected_action_id,omitempty"`
	ToolCalls        []string             `json:"tool_calls,omitempty"`
	Error            string               `json:"error,omitempty"`
	EvidenceIDs      []string             `json:"evidence_ids,omitempty"`
}

type VerificationResultArtifact struct {
	Meta            WorkflowArtifactMeta `json:"meta"`
	Summary         string               `json:"summary"`
	Verdict         string               `json:"verdict,omitempty"`
	BeforeRisk      float64              `json:"before_risk,omitempty"`
	AfterRisk       float64              `json:"after_risk,omitempty"`
	EvidenceIDs     []string             `json:"evidence_ids,omitempty"`
	RegressionHints []string             `json:"regression_hints,omitempty"`
}

type FinalIncidentReportArtifact struct {
	Meta            WorkflowArtifactMeta `json:"meta"`
	Summary         string               `json:"summary"`
	Status          string               `json:"status,omitempty"`
	MostLikelyCause string               `json:"most_likely_cause,omitempty"`
	Confidence      float64              `json:"confidence,omitempty"`
	EvidenceIDs     []string             `json:"evidence_ids,omitempty"`
	MessageIDs      []string             `json:"message_ids,omitempty"`
}

type WorkflowArtifactChain struct {
	SchemaVersion   string                      `json:"schema_version"`
	RunID           string                      `json:"run_id"`
	IncidentID      string                      `json:"incident_id,omitempty"`
	CorrelationID   string                      `json:"correlation_id,omitempty"`
	GeneratedAt     time.Time                   `json:"generated_at"`
	Scene           SceneArtifactContext        `json:"scene,omitempty"`
	Observation     ObservationSummaryArtifact  `json:"observation"`
	Anomaly         AnomalyFindingArtifact      `json:"anomaly"`
	Hypothesis      RootCauseHypothesisArtifact `json:"hypothesis"`
	Proposal        RemediationProposalArtifact `json:"proposal"`
	ExecutionPlan   ExecutionPlanArtifact       `json:"execution_plan"`
	ExecutionResult ExecutionResultArtifact     `json:"execution_result"`
	Verification    VerificationResultArtifact  `json:"verification"`
	Incident        FinalIncidentReportArtifact `json:"incident"`
	Adaptive        []AdaptiveArtifact          `json:"adaptive,omitempty"`
}

type WorkflowArtifactRef struct {
	ArtifactID        string               `json:"artifact_id"`
	Kind              WorkflowArtifactKind `json:"kind"`
	RunID             string               `json:"run_id"`
	IncidentID        string               `json:"incident_id,omitempty"`
	CorrelationID     string               `json:"correlation_id,omitempty"`
	Status            string               `json:"status"`
	Sequence          int                  `json:"sequence"`
	Attempt           int                  `json:"attempt,omitempty"`
	Producer          string               `json:"producer"`
	Consumer          string               `json:"consumer,omitempty"`
	ParentArtifactID  string               `json:"parent_artifact_id,omitempty"`
	RetryOfArtifactID string               `json:"retry_of_artifact_id,omitempty"`
	MessageID         string               `json:"message_id,omitempty"`
	ProducedAt        time.Time            `json:"produced_at"`
	StorageBackend    string               `json:"storage_backend,omitempty"`
	StorageKey        string               `json:"storage_key,omitempty"`
	Path              string               `json:"path,omitempty"`
	LocalCachePath    string               `json:"local_cache_path,omitempty"`
	Summary           string               `json:"summary,omitempty"`
}

type WorkflowArtifactManifest struct {
	SchemaVersion string                `json:"schema_version"`
	RunID         string                `json:"run_id"`
	IncidentID    string                `json:"incident_id,omitempty"`
	CorrelationID string                `json:"correlation_id,omitempty"`
	UpdatedAt     time.Time             `json:"updated_at"`
	Artifacts     []WorkflowArtifactRef `json:"artifacts,omitempty"`
}

func buildWorkflowArtifactManifest(chain WorkflowArtifactChain) WorkflowArtifactManifest {
	refs := []WorkflowArtifactRef{
		workflowArtifactRefFromMeta(chain.Observation.Meta, chain.Observation.Summary),
		workflowArtifactRefFromMeta(chain.Anomaly.Meta, chain.Anomaly.Summary),
		workflowArtifactRefFromMeta(chain.Hypothesis.Meta, chain.Hypothesis.Summary),
		workflowArtifactRefFromMeta(chain.Proposal.Meta, chain.Proposal.Summary),
		workflowArtifactRefFromMeta(chain.ExecutionPlan.Meta, chain.ExecutionPlan.Summary),
		workflowArtifactRefFromMeta(chain.ExecutionResult.Meta, chain.ExecutionResult.Summary),
		workflowArtifactRefFromMeta(chain.Verification.Meta, chain.Verification.Summary),
		workflowArtifactRefFromMeta(chain.Incident.Meta, chain.Incident.Summary),
	}
	for _, artifact := range chain.Adaptive {
		refs = append(refs, workflowArtifactRefFromAdaptiveArtifact(artifact))
	}
	return WorkflowArtifactManifest{
		SchemaVersion: workflowArtifactSchemaVersion,
		RunID:         chain.RunID,
		IncidentID:    chain.IncidentID,
		CorrelationID: chain.CorrelationID,
		UpdatedAt:     chain.GeneratedAt,
		Artifacts:     refs,
	}
}

func buildWorkflowArtifactChain(state *workflowState, report RCAWorkflowReport, status string) WorkflowArtifactChain {
	now := report.GeneratedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	runID := firstNonEmpty(report.WorkflowID, workflowRunID(state))
	incidentID := firstNonEmpty(report.IncidentID, fmt.Sprintf("inc-%s", sanitizeID(runID)))
	correlationID := firstNonEmpty(report.TraceID, runID)
	parentMessageID := messageIDFromRef(report.LatestAnalysisHandoffMessage)

	observationRefs, observationIDs := normalizedEvidenceRefs(report.NormalizedEvidence)
	bestHypothesis := primaryHypothesis(report.Hypotheses)
	primaryRecommendation := firstRecommendation(report.Recommendations)
	selectedAction := report.Validation.SelectedAction
	selectedContractID := ""
	if report.Validation.SelectedActionContract != nil {
		selectedContractID = strings.TrimSpace(report.Validation.SelectedActionContract.ID)
	}
	parentArtifactID := ""
	if report.MessageHistoryArtifact != nil {
		parentArtifactID = strings.TrimSpace(report.MessageHistoryArtifact.ArtifactID)
	}
	stepIDs := planStepIDs(report.Validation)
	toolCalls := toolCallIDs(report.ToolCalls)
	governance := report.Validation.Governance
	if governance == nil {
		governance = &ValidationGovernanceTrace{}
	}
	verificationEvidence := []string(nil)
	if report.Validation.PostActionValidation != nil {
		verificationEvidence = append(verificationEvidence, report.Validation.PostActionValidation.SupportingEvidenceIDs...)
		verificationEvidence = append(verificationEvidence, report.Validation.PostActionValidation.ContradictingEvidenceIDs...)
	}
	verificationEvidence = dedupeStrings(verificationEvidence)
	messageIDs := messageIDsFromRefs(
		report.LatestAnalysisHandoffMessage,
		report.LatestValidationRequestMessage,
		report.LatestValidationResultMessage,
		report.LatestActionDecisionMessage,
		report.LatestPostActionValidationMessage,
		report.LatestCompensationMessage,
	)

	observationWindow := workflowObservationWindow(report)
	sceneContext := buildSceneArtifactContext(state)
	if sceneContext.SceneClassification == nil && sceneContext.CollectionPlan == nil && sceneContext.RecollectionResult == nil && sceneContext.EvidenceGapState == nil && sceneContext.EscalationDecision == nil {
		sceneContext = buildSceneArtifactContextFromReport(report)
	}

	chain := WorkflowArtifactChain{
		SchemaVersion: workflowArtifactSchemaVersion,
		RunID:         runID,
		IncidentID:    incidentID,
		CorrelationID: correlationID,
		GeneratedAt:   now,
		Scene:         sceneContext,
		Observation: ObservationSummaryArtifact{
			Meta: workflowArtifactMeta(
				WorkflowArtifactObservationSummary,
				runID,
				incidentID,
				correlationID,
				"observer_agent",
				"analysis_agent",
				"collected",
				now,
				parentMessageID,
				parentArtifactID,
				report.LatestAnalysisHandoffMessage,
			),
			CollectorID:     report.CollectorID,
			Trigger:         report.Trigger,
			Window:          observationWindow,
			Summary:         firstNonEmpty(report.SynthesizedIncident.Summary, report.StructuredReport.IncidentSummary),
			Signals:         dedupeStrings(append([]string(nil), report.Anomalies...)),
			EvidenceIDs:     observationIDs,
			RawEvidenceRefs: observationRefs,
		},
		Anomaly: AnomalyFindingArtifact{
			Meta: workflowArtifactMeta(
				WorkflowArtifactAnomalyFinding,
				runID,
				incidentID,
				correlationID,
				"analysis_agent",
				"planner_agent",
				"detected",
				now,
				parentMessageID,
				parentArtifactID,
				report.LatestAnalysisHandoffMessage,
			),
			RiskLevel:       report.Status,
			Confidence:      maxFloat(report.StructuredReport.Confidence, report.RetrievalConfidence),
			Summary:         joinSummary(report.Anomalies, report.UnresolvedGaps),
			Anomalies:       append([]string(nil), report.Anomalies...),
			EvidenceIDs:     uniqueIDs(report.Evidence),
			RawEvidenceRefs: observationRefs,
		},
		Hypothesis: RootCauseHypothesisArtifact{
			Meta: workflowArtifactMeta(
				WorkflowArtifactRootCauseHypothesis,
				runID,
				incidentID,
				correlationID,
				"analysis_agent",
				"planner_agent",
				"ranked",
				now,
				parentMessageID,
				parentArtifactID,
				report.LatestAnalysisHandoffMessage,
			),
			Title:                    bestHypothesis.Title,
			Summary:                  bestHypothesis.Description,
			Confidence:               bestHypothesis.Confidence,
			EvidenceIDs:              append([]string(nil), bestHypothesis.EvidenceIDs...),
			ContradictingEvidenceIDs: append([]string(nil), bestHypothesis.ContradictingEvidenceIDs...),
			ExpectedSignals:          append([]string(nil), bestHypothesis.RecommendedActions...),
		},
		Proposal: RemediationProposalArtifact{
			Meta: workflowArtifactMeta(
				WorkflowArtifactRemediationProposal,
				runID,
				incidentID,
				correlationID,
				"planner_agent",
				"policy_agent",
				"proposed",
				now,
				parentMessageID,
				parentArtifactID,
				report.LatestAnalysisHandoffMessage,
			),
			Summary:            primaryRecommendation.Summary,
			RecommendationIDs:  recommendationIDs(report.Recommendations),
			ActionCandidateIDs: actionCandidateIDs(report.Validation.ActionCandidates),
			RiskLevel:          firstNonEmpty(primaryRecommendation.RiskLevel, report.Status),
			ReadOnlyOnly:       report.Validation.ReadOnlyOnly,
			RequiresApproval:   firstRecommendationApproval(primaryRecommendation, report.Validation.SelectedAction),
			RollbackHints:      rollbackHints(report.Recommendations, report.Validation.SelectedAction),
			EvidenceIDs:        recommendationEvidenceIDs(report.Recommendations),
		},
		ExecutionPlan: ExecutionPlanArtifact{
			Meta: workflowArtifactMeta(
				WorkflowArtifactExecutionPlan,
				runID,
				incidentID,
				correlationID,
				"policy_agent",
				"executor_agent",
				"planned",
				now,
				messageIDFromRef(report.LatestValidationRequestMessage),
				parentArtifactID,
				report.LatestValidationRequestMessage,
			),
			Summary:          firstNonEmpty(governance.ActionSummary, primaryRecommendation.Summary),
			SelectedActionID: firstNonEmpty(actionCandidateID(selectedAction), governance.ActionCandidateID),
			ActionContractID: selectedContractID,
			StepIDs:          stepIDs,
			ToolCalls:        toolCalls,
			PolicyStatus:     firstNonEmpty(governance.PolicyStatus, "proposal_only"),
			EvidenceIDs:      append([]string(nil), report.Validation.ValidatedRecommendationIDs...),
		},
		ExecutionResult: ExecutionResultArtifact{
			Meta: workflowArtifactMeta(
				WorkflowArtifactExecutionResult,
				runID,
				incidentID,
				correlationID,
				"executor_agent",
				"verifier_agent",
				"recorded",
				now,
				messageIDFromRef(report.LatestActionDecisionMessage),
				parentArtifactID,
				report.LatestActionDecisionMessage,
			),
			Summary:          firstNonEmpty(governance.ActionSummary, report.Validation.StopReason),
			Outcome:          firstNonEmpty(governance.StepStatus, report.Validation.Mode),
			SelectedActionID: actionCandidateID(selectedAction),
			ToolCalls:        toolCalls,
			Error:            validationFailureSummary(report.Validation),
			EvidenceIDs:      append([]string(nil), report.Validation.RejectedRecommendationIDs...),
		},
		Verification: VerificationResultArtifact{
			Meta: workflowArtifactMeta(
				WorkflowArtifactVerificationResult,
				runID,
				incidentID,
				correlationID,
				"verifier_agent",
				"workflow_runtime",
				"verified",
				now,
				messageIDFromRef(report.LatestPostActionValidationMessage),
				parentArtifactID,
				report.LatestPostActionValidationMessage,
			),
			Summary:         verificationSummary(report.Validation),
			Verdict:         verificationVerdict(report.Validation),
			BeforeRisk:      verificationBeforeRisk(report.Validation),
			AfterRisk:       verificationAfterRisk(report.Validation),
			EvidenceIDs:     verificationEvidence,
			RegressionHints: append([]string(nil), report.Validation.UnresolvedUncertainty...),
		},
		Incident: FinalIncidentReportArtifact{
			Meta: workflowArtifactMeta(
				WorkflowArtifactIncidentReport,
				runID,
				incidentID,
				correlationID,
				"workflow_runtime",
				"operator",
				status,
				now,
				messageIDFromRef(report.LatestPostActionValidationMessage),
				parentArtifactID,
				report.LatestPostActionValidationMessage,
			),
			Summary:         firstNonEmpty(report.SynthesizedIncident.Summary, report.StructuredReport.IncidentSummary),
			Status:          status,
			MostLikelyCause: firstNonEmpty(report.StructuredReport.MostLikelyCause, report.SuspectedRootCauseEntity),
			Confidence:      maxFloat(report.StructuredReport.Confidence, report.Validation.Confidence),
			EvidenceIDs:     dedupeStrings(append(uniqueIDs(report.Evidence), observationIDs...)),
			MessageIDs:      messageIDs,
		},
	}
	if state != nil && len(state.adaptiveArtifacts) > 0 {
		chain.Adaptive = append([]AdaptiveArtifact(nil), state.adaptiveArtifacts...)
	} else if len(report.AdaptiveArtifacts) > 0 {
		chain.Adaptive = append([]AdaptiveArtifact(nil), report.AdaptiveArtifacts...)
	}
	return chain
}

func workflowArtifactMeta(kind WorkflowArtifactKind, runID, incidentID, correlationID, producer, consumer, status string, at time.Time, parentMessageID, parentArtifactID string, messageRef *AgentMessageRef) WorkflowArtifactMeta {
	meta := WorkflowArtifactMeta{
		SchemaVersion:  workflowArtifactSchemaVersion,
		Version:        "v1",
		Kind:           kind,
		ArtifactID:     artifactIDForWorkflow(kind, runID),
		RunID:          strings.TrimSpace(runID),
		IncidentID:     strings.TrimSpace(incidentID),
		CorrelationID:  strings.TrimSpace(correlationID),
		Producer:       strings.TrimSpace(producer),
		Consumer:       strings.TrimSpace(consumer),
		Status:         strings.TrimSpace(status),
		ProducedAt:     at.UTC(),
		UpdatedAt:      at.UTC(),
		InputArtifacts: inputArtifactsForKind(runID, kind),
		Replayable:     kind != WorkflowArtifactExecutionResult,
		Retryable:      kind != WorkflowArtifactExecutionResult,
		ReplayPoint:    string(kind),
	}
	if strings.TrimSpace(parentArtifactID) != "" {
		meta.ParentArtifactID = strings.TrimSpace(parentArtifactID)
	}
	if strings.TrimSpace(parentMessageID) != "" {
		meta.ParentMessageID = strings.TrimSpace(parentMessageID)
	}
	if messageRef != nil {
		meta.MessageID = strings.TrimSpace(messageRef.MessageID)
		if strings.TrimSpace(messageRef.ParentMessageID) != "" && meta.ParentMessageID == "" {
			meta.ParentMessageID = strings.TrimSpace(messageRef.ParentMessageID)
		}
	}
	return meta
}

func workflowArtifactRefFromMeta(meta WorkflowArtifactMeta, summary string) WorkflowArtifactRef {
	return WorkflowArtifactRef{
		ArtifactID:        meta.ArtifactID,
		Kind:              meta.Kind,
		RunID:             meta.RunID,
		IncidentID:        meta.IncidentID,
		CorrelationID:     meta.CorrelationID,
		Status:            meta.Status,
		Sequence:          meta.Sequence,
		Attempt:           meta.Attempt,
		Producer:          meta.Producer,
		Consumer:          meta.Consumer,
		ParentArtifactID:  meta.ParentArtifactID,
		RetryOfArtifactID: meta.RetryOfArtifactID,
		MessageID:         meta.MessageID,
		ProducedAt:        meta.ProducedAt,
		Summary:           truncateString(summary, 220),
	}
}

func workflowArtifactRefFromAdaptiveArtifact(artifact AdaptiveArtifact) WorkflowArtifactRef {
	return WorkflowArtifactRef{
		ArtifactID:    artifact.ArtifactID,
		Kind:          artifact.Kind,
		RunID:         artifact.RunID,
		IncidentID:    artifact.IncidentID,
		CorrelationID: artifact.CorrelationID,
		Status:        artifact.Status,
		Sequence:      artifact.Iteration,
		Producer:      artifact.Producer,
		Consumer:      artifact.Consumer,
		ProducedAt:    artifact.ProducedAt,
		Summary:       truncateString(artifact.Summary, 220),
	}
}

func artifactIDForWorkflow(kind WorkflowArtifactKind, runID string) string {
	return fmt.Sprintf("%s-%s-%s", sanitizeID(runID), sanitizeID(string(kind)), "v1")
}

func workflowRunID(state *workflowState) string {
	if state == nil {
		return ""
	}
	return state.workflowID
}

func normalizedEvidenceRefs(records []evidencev1.Record) ([]evidencev1.RawReference, []string) {
	if len(records) == 0 {
		return nil, nil
	}
	refs := make([]evidencev1.RawReference, 0, minInt(len(records), 8))
	ids := make([]string, 0, minInt(len(records), 32))
	for _, record := range records {
		if id := strings.TrimSpace(record.ID); id != "" {
			ids = append(ids, id)
		}
		for _, ref := range record.RawReferences {
			if len(refs) >= 8 {
				break
			}
			refs = append(refs, ref)
		}
		if len(ids) >= 32 && len(refs) >= 8 {
			break
		}
	}
	return refs, dedupeStrings(ids)
}

func uniqueIDs(evidence []RCAEvidence) []string {
	if len(evidence) == 0 {
		return nil
	}
	ids := make([]string, 0, len(evidence))
	for _, item := range evidence {
		if id := strings.TrimSpace(item.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return dedupeStrings(ids)
}

func joinSummary(items ...[]string) string {
	parts := make([]string, 0, len(items))
	for _, block := range items {
		if len(block) == 0 {
			continue
		}
		parts = append(parts, truncateString(strings.Join(block, "; "), 220))
	}
	return truncateString(strings.Join(parts, " | "), 220)
}

func recommendationIDs(items []WorkflowRecommendation) []string {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := strings.TrimSpace(item.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return dedupeStrings(ids)
}

func recommendationEvidenceIDs(items []WorkflowRecommendation) []string {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.EvidenceIDs...)
	}
	return dedupeStrings(ids)
}

func actionCandidateIDs(items []ValidationActionCandidate) []string {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id := strings.TrimSpace(item.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return dedupeStrings(ids)
}

func actionCandidateID(item *ValidationActionCandidate) string {
	if item == nil {
		return ""
	}
	return strings.TrimSpace(item.ID)
}

func firstRecommendationApproval(rec WorkflowRecommendation, action *ValidationActionCandidate) bool {
	if action != nil {
		return action.RequiresApproval
	}
	return rec.RequiresApproval
}

func rollbackHints(recommendations []WorkflowRecommendation, action *ValidationActionCandidate) []string {
	hints := make([]string, 0, len(recommendations)+1)
	for _, rec := range recommendations {
		if hint := strings.TrimSpace(rec.RollbackHint); hint != "" {
			hints = append(hints, hint)
		}
	}
	if action != nil {
		if hint := strings.TrimSpace(action.RollbackHint); hint != "" {
			hints = append(hints, hint)
		}
	}
	return dedupeStrings(hints)
}

func planStepIDs(report ValidationActionReport) []string {
	if len(report.Targets) == 0 && len(report.Results) == 0 {
		return nil
	}
	ids := make([]string, 0, len(report.Targets)+len(report.Results))
	for _, target := range report.Targets {
		if id := strings.TrimSpace(target.ID); id != "" {
			ids = append(ids, id)
		}
	}
	for _, result := range report.Results {
		if id := strings.TrimSpace(result.TargetID); id != "" {
			ids = append(ids, id)
		}
	}
	return dedupeStrings(ids)
}

func toolCallIDs(toolCalls []WorkflowToolCall) []string {
	if len(toolCalls) == 0 {
		return nil
	}
	ids := make([]string, 0, len(toolCalls))
	for _, call := range toolCalls {
		if id := strings.TrimSpace(call.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return dedupeStrings(ids)
}

func messageIDsFromRefs(refs ...*AgentMessageRef) []string {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		if id := strings.TrimSpace(ref.MessageID); id != "" {
			ids = append(ids, id)
		}
	}
	return dedupeStrings(ids)
}

func messageIDFromRef(ref *AgentMessageRef) string {
	if ref == nil {
		return ""
	}
	return strings.TrimSpace(ref.MessageID)
}

func primaryHypothesis(items []RCAHypothesis) RCAHypothesis {
	if len(items) == 0 {
		return RCAHypothesis{}
	}
	ordered := append([]RCAHypothesis(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Rank != ordered[j].Rank {
			return ordered[i].Rank < ordered[j].Rank
		}
		return ordered[i].Confidence > ordered[j].Confidence
	})
	return ordered[0]
}

func firstRecommendation(items []WorkflowRecommendation) WorkflowRecommendation {
	if len(items) == 0 {
		return WorkflowRecommendation{}
	}
	ordered := append([]WorkflowRecommendation(nil), items...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if workflowPriorityRank(ordered[i].Priority) != workflowPriorityRank(ordered[j].Priority) {
			return workflowPriorityRank(ordered[i].Priority) > workflowPriorityRank(ordered[j].Priority)
		}
		return ordered[i].Confidence > ordered[j].Confidence
	})
	return ordered[0]
}

func verificationSummary(report ValidationActionReport) string {
	if report.PostActionValidation == nil {
		return firstNonEmpty(report.StopReason, report.DegradedFallbackReason)
	}
	return firstNonEmpty(report.PostActionValidation.Summary, report.StopReason, report.DegradedFallbackReason)
}

func verificationVerdict(report ValidationActionReport) string {
	if report.PostActionValidation == nil {
		return ""
	}
	return string(report.PostActionValidation.Verdict)
}

func verificationBeforeRisk(report ValidationActionReport) float64 {
	if report.PostActionValidation == nil {
		return 0
	}
	return report.PostActionValidation.BeforeRisk
}

func verificationAfterRisk(report ValidationActionReport) float64 {
	if report.PostActionValidation == nil {
		return 0
	}
	return report.PostActionValidation.AfterRisk
}

func validationFailureSummary(report ValidationActionReport) string {
	if strings.TrimSpace(report.StopReason) != "" {
		return report.StopReason
	}
	if strings.TrimSpace(report.DegradedFallbackReason) != "" {
		return report.DegradedFallbackReason
	}
	if len(report.Results) == 0 {
		return ""
	}
	return report.Results[len(report.Results)-1].StopReason
}

func workflowObservationWindow(report RCAWorkflowReport) string {
	if !report.SynthesizedIncident.TimeWindow.Start.IsZero() || !report.SynthesizedIncident.TimeWindow.End.IsZero() {
		return fmt.Sprintf("%s..%s", report.SynthesizedIncident.TimeWindow.Start.UTC().Format(time.RFC3339), report.SynthesizedIncident.TimeWindow.End.UTC().Format(time.RFC3339))
	}
	return strings.TrimSpace(report.Trigger)
}

func inputArtifactsForKind(runID string, kind WorkflowArtifactKind) []string {
	previous := map[WorkflowArtifactKind]WorkflowArtifactKind{
		WorkflowArtifactAnomalyFinding:      WorkflowArtifactObservationSummary,
		WorkflowArtifactRootCauseHypothesis: WorkflowArtifactAnomalyFinding,
		WorkflowArtifactRemediationProposal: WorkflowArtifactRootCauseHypothesis,
		WorkflowArtifactExecutionPlan:       WorkflowArtifactRemediationProposal,
		WorkflowArtifactExecutionResult:     WorkflowArtifactExecutionPlan,
		WorkflowArtifactVerificationResult:  WorkflowArtifactExecutionResult,
		WorkflowArtifactIncidentReport:      WorkflowArtifactVerificationResult,
	}
	parent, ok := previous[kind]
	if !ok {
		return nil
	}
	return []string{artifactIDForWorkflow(parent, runID)}
}

func workflowPriorityRank(priority string) int {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func artifactKindsForChain(chain WorkflowArtifactChain) []string {
	return []string{
		string(chain.Observation.Meta.Kind),
		string(chain.Anomaly.Meta.Kind),
		string(chain.Hypothesis.Meta.Kind),
		string(chain.Proposal.Meta.Kind),
		string(chain.ExecutionPlan.Meta.Kind),
		string(chain.ExecutionResult.Meta.Kind),
		string(chain.Verification.Meta.Kind),
		string(chain.Incident.Meta.Kind),
	}
}

func BuildRCAWorkflowListItem(report RCAWorkflowReport) RCAWorkflowListItem {
	kinds := artifactKindsForChain(report.Artifacts)
	return RCAWorkflowListItem{
		WorkflowID:           report.WorkflowID,
		IncidentID:           report.IncidentID,
		CollectorID:          report.CollectorID,
		Status:               report.Status,
		Summary:              firstNonEmpty(report.StructuredReport.IncidentSummary, report.SynthesizedIncident.Summary, report.WorkflowID),
		MostLikelyCause:      firstNonEmpty(report.StructuredReport.MostLikelyCause, report.SuspectedRootCauseEntity),
		Confidence:           maxFloat(report.StructuredReport.Confidence, report.Validation.Confidence),
		ArtifactManifestPath: firstNonEmpty(report.ArtifactManifestPath, durableRefPath(report.ArtifactManifestArtifact)),
		ArtifactCount:        len(kinds),
		ArtifactKinds:        kinds,
	}
}

func durableRefPath(ref *DurableArtifactRef) string {
	if ref == nil {
		return ""
	}
	return firstNonEmpty(ref.LocalCachePath, ref.Path)
}
