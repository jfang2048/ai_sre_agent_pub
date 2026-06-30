package agent

type SceneArtifactContext struct {
	SceneClassification *SceneClassification   `json:"scene_classification,omitempty"`
	CollectionPlan      *CollectionPlanSummary `json:"collection_plan,omitempty"`
	RecollectionResult  *RecollectionResult    `json:"recollection_result,omitempty"`
	EvidenceGapState    *EvidenceGapState      `json:"evidence_gap_state,omitempty"`
	EscalationDecision  *EscalationDecision    `json:"escalation_decision,omitempty"`
}

func buildSceneArtifactContext(state *workflowState) SceneArtifactContext {
	ctx := SceneArtifactContext{}
	if state == nil {
		return ctx
	}
	if state.sceneClassification.SceneFamily != "" {
		copy := state.sceneClassification
		ctx.SceneClassification = &copy
	}
	if state.collectionPlan.PlanID != "" {
		summary := summarizeCollectionPlan(state.collectionPlan)
		ctx.CollectionPlan = &summary
	}
	if len(state.recollectionResults) > 0 {
		copy := state.recollectionResults[len(state.recollectionResults)-1]
		ctx.RecollectionResult = &copy
	}
	if state.evidenceGapState.UpdatedAt.IsZero() == false || len(state.evidenceGapState.MissingEvidence) > 0 {
		copy := state.evidenceGapState
		ctx.EvidenceGapState = &copy
	}
	if state.escalationDecision.DecidedAt.IsZero() == false || state.escalationDecision.Escalate {
		copy := state.escalationDecision
		ctx.EscalationDecision = &copy
	}
	return ctx
}

func buildSceneArtifactContextFromReport(report RCAWorkflowReport) SceneArtifactContext {
	ctx := SceneArtifactContext{}
	if report.SceneClassification.SceneFamily != "" {
		copy := report.SceneClassification
		ctx.SceneClassification = &copy
	}
	if report.CollectionPlan.PlanID != "" {
		summary := summarizeCollectionPlan(report.CollectionPlan)
		ctx.CollectionPlan = &summary
	}
	if len(report.RecollectionResults) > 0 {
		copy := report.RecollectionResults[len(report.RecollectionResults)-1]
		ctx.RecollectionResult = &copy
	}
	if report.EvidenceGapState.UpdatedAt.IsZero() == false || len(report.EvidenceGapState.MissingEvidence) > 0 {
		copy := report.EvidenceGapState
		ctx.EvidenceGapState = &copy
	}
	if report.EscalationDecision.DecidedAt.IsZero() == false || report.EscalationDecision.Escalate {
		copy := report.EscalationDecision
		ctx.EscalationDecision = &copy
	}
	return ctx
}
