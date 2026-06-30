package agent

type VerifierReport struct {
	UncertaintyDelta         float64            `json:"uncertainty_delta"`
	ConfidenceDelta          float64            `json:"confidence_delta"`
	ContradictionDelta       int                `json:"contradiction_delta"`
	EvidenceGapCoverageDelta int                `json:"evidence_gap_coverage_delta"`
	RiskDelta                float64            `json:"risk_delta"`
	ActionEffectDelta        float64            `json:"action_effect_delta"`
	ProgressClassification   string             `json:"progress_classification"`
	Directive                AdaptiveDirective  `json:"directive"`
	StopReason               AdaptiveStopReason `json:"stop_reason,omitempty"`
	Summary                  string             `json:"summary"`
}

func verifyAdaptiveProgress(before, after AdaptiveRuntimeState, normalized *NormalizedToolResult, progress AdaptiveProgressAssessment) VerifierReport {
	report := VerifierReport{
		UncertaintyDelta:         progress.UncertaintyDelta,
		ConfidenceDelta:          progress.ConfidenceDelta,
		ContradictionDelta:       progress.ContradictionDelta,
		EvidenceGapCoverageDelta: progress.EvidenceGapCoverageDelta,
		RiskDelta:                progress.RiskDelta,
		ActionEffectDelta:        progress.ActionEffectDelta,
		Directive:                AdaptiveDirectiveContinue,
		Summary:                  progress.Summary,
	}
	switch {
	case progress.Progress && len(after.UnresolvedEvidenceGaps) == 0 && after.ConfidenceScore >= before.ConfidenceScore:
		report.ProgressClassification = "objective_closing"
		report.Directive = AdaptiveDirectiveStop
		report.StopReason = AdaptiveStopReasonConfidenceSufficient
	case progress.Progress:
		report.ProgressClassification = "progress"
	case progress.Plateau && normalized != nil && normalized.LowYieldSignal:
		report.ProgressClassification = "low_yield_plateau"
		report.Directive = AdaptiveDirectiveStop
		report.StopReason = AdaptiveStopReasonUncertaintyPlateau
	case progress.Plateau:
		report.ProgressClassification = "plateau"
		report.Directive = AdaptiveDirectiveBranch
		report.StopReason = AdaptiveStopReasonNoProgress
	default:
		report.ProgressClassification = "mixed"
	}
	return report
}
