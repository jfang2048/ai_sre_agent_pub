package agent

import "strings"

type CritiqueReport struct {
	MissingCounterEvidence      []string           `json:"missing_counter_evidence,omitempty"`
	PrematureRemediation        bool               `json:"premature_remediation_detection,omitempty"`
	RepeatedToolWarning         bool               `json:"repeated_tool_warning,omitempty"`
	LowYieldWarning             bool               `json:"low_yield_warning,omitempty"`
	ScopeMismatch               bool               `json:"scope_mismatch,omitempty"`
	ConfidenceInflationDetected bool               `json:"confidence_inflation_detection,omitempty"`
	QueryQualityWarning         string             `json:"query_quality_warning,omitempty"`
	NoProgressWarning           bool               `json:"no_progress_warning,omitempty"`
	CheaperAlternative          string             `json:"cheaper_alternative_recommendation,omitempty"`
	RecommendedBranch           AdaptiveBranchKind `json:"recommended_branch,omitempty"`
	StopReason                  AdaptiveStopReason `json:"stop_reason,omitempty"`
	Summary                     string             `json:"summary"`
}

func critiquePlannerProposal(state *workflowState, proposal PlannerProposal) CritiqueReport {
	report := CritiqueReport{
		MissingCounterEvidence: append([]string(nil), adaptiveContradictions(state)...),
	}
	if proposal.Selected == nil {
		report.StopReason = AdaptiveStopReasonNoSafeNextStep
		report.Summary = "critic found no policy-safe next step"
		return report
	}
	selected := proposal.Selected
	report.RepeatedToolWarning = repeatedToolPenalty(state, selected.Tool) > 0.08
	report.LowYieldWarning = lowYieldPenalty(state, selected.Tool) > 0.08
	report.ScopeMismatch = len(state.adaptiveScopeHints) == 0 && strings.Contains(strings.ToLower(selected.Reason), "scope")
	report.ConfidenceInflationDetected = topHypothesisConfidence(state.hypotheses) >= 0.80 && len(adaptiveEvidenceGaps(state)) > 0
	report.PrematureRemediation = !selected.Contract.ReadOnly
	report.QueryQualityWarning = critiqueQueryQuality(selected.Query)
	report.NoProgressWarning = latestAdaptiveProgress(state) != nil && latestAdaptiveProgress(state).Plateau
	report.CheaperAlternative = cheaperAlternative(state, proposal.CandidateTools, *selected)
	if report.ScopeMismatch {
		report.RecommendedBranch = AdaptiveBranchNarrowScope
	}
	if report.NoProgressWarning && report.RecommendedBranch == "" {
		report.RecommendedBranch = AdaptiveBranchBroadenScope
	}
	switch {
	case report.PrematureRemediation:
		report.Summary = "critic rejected premature remediation and recommends more read-only discrimination"
	case report.CheaperAlternative != "":
		report.Summary = "critic recommends a cheaper discriminator before the selected tool"
	case report.RepeatedToolWarning || report.LowYieldWarning:
		report.Summary = "critic warns that the selected tool is trending low-yield or repetitive"
	default:
		report.Summary = "critic accepts the selected read-only evidence path"
	}
	return report
}

func critiqueQueryQuality(query map[string]string) string {
	if len(query) == 0 {
		return "query is empty"
	}
	if strings.TrimSpace(query["scope"]) == "" || strings.EqualFold(strings.TrimSpace(query["scope"]), "fleet") {
		return "query scope is broad"
	}
	if strings.TrimSpace(query["window"]) == "" {
		return "query window is missing"
	}
	if strings.TrimSpace(query["query"]) == "" {
		return "query text is missing"
	}
	return ""
}

func cheaperAlternative(state *workflowState, candidates []ToolCandidate, selected ToolCandidate) string {
	for _, candidate := range candidates {
		if candidate.Tool == selected.Tool || !candidate.Contract.ReadOnly {
			continue
		}
		if len(candidate.CoveredEvidenceGaps) >= len(selected.CoveredEvidenceGaps) &&
			candidate.Score.Breakdown.CostPenalty <= selected.Score.Breakdown.CostPenalty {
			return string(candidate.Tool)
		}
	}
	return ""
}
