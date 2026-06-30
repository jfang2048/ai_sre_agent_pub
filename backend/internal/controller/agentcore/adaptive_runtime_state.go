package agent

import "strings"

type AdaptiveToolYieldRecord struct {
	Tool            ToolName `json:"tool"`
	ToolCallID      string   `json:"tool_call_id,omitempty"`
	ResultQuality   string   `json:"result_quality,omitempty"`
	LowYield        bool     `json:"low_yield"`
	ConfidenceDelta float64  `json:"confidence_delta,omitempty"`
	GapCoverage     int      `json:"gap_coverage,omitempty"`
	Summary         string   `json:"summary,omitempty"`
}

func (s *AdaptiveRuntimeState) applyToolResult(result *NormalizedToolResult) {
	if s == nil || result == nil {
		return
	}
	s.ToolCalls++
	s.ToolHistory = append(s.ToolHistory, string(result.Tool))
	s.ToolYieldHistory = append(s.ToolYieldHistory, AdaptiveToolYieldRecord{
		Tool:            result.Tool,
		ToolCallID:      result.ToolCallID,
		ResultQuality:   result.ResultQuality,
		LowYield:        result.LowYieldSignal,
		ConfidenceDelta: result.ConfidenceDelta,
		Summary:         truncateString(result.Summary, 160),
	})
	s.ScopeHints = dedupeStrings(append(s.ScopeHints, result.AffectedScope...))
	s.ScopeHints = dedupeStrings(append(s.ScopeHints, result.RecommendedScopeRefinement...))
	if strings.TrimSpace(result.RecommendedTimeWindowRefine) != "" {
		s.RemainingTimeBudget = result.RecommendedTimeWindowRefine
	}
}

func (s *AdaptiveRuntimeState) applyHypothesisRevision(hypotheses []RCAHypothesis, contradictions []string) {
	if s == nil {
		return
	}
	s.ActiveHypotheses = append([]RCAHypothesis(nil), hypotheses...)
	s.CurrentHypotheses = append([]RCAHypothesis(nil), hypotheses...)
	s.ContradictionSet = dedupeStrings(append([]string(nil), contradictions...))
	s.HypothesisRewrites++
}

func (s *AdaptiveRuntimeState) applyProgressAssessment(progress AdaptiveProgressAssessment) {
	if s == nil {
		return
	}
	copy := progress
	s.LatestProgress = &copy
	s.ConfidenceScore = clamp01(progress.ConfidenceAfter)
	s.CurrentConfidence = s.ConfidenceScore
	s.RiskScore = maxFloat(0, progress.RiskAfter)
	s.CurrentRisk = s.RiskScore
	if progress.Progress {
		s.NoProgressRounds = 0
	} else {
		s.NoProgressRounds++
	}
	if progress.Plateau {
		s.UncertaintyPlateauRounds++
	} else {
		s.UncertaintyPlateauRounds = 0
	}
}

func (s *AdaptiveRuntimeState) markNoProgress(reason string) {
	if s == nil {
		return
	}
	s.NoProgressRounds++
	if strings.TrimSpace(reason) != "" {
		s.StopReason = strings.TrimSpace(reason)
	}
}

func (s *AdaptiveRuntimeState) refineScope(scope ...string) {
	if s == nil {
		return
	}
	s.ScopeHints = dedupeStrings(append(s.ScopeHints, scope...))
}

func (s *AdaptiveRuntimeState) refineWindow(window string) {
	if s == nil || strings.TrimSpace(window) == "" {
		return
	}
	s.RemainingTimeBudget = strings.TrimSpace(window)
}

func (s *AdaptiveRuntimeState) shouldStop(cfg WorkflowConfig) (bool, string) {
	if s == nil {
		return true, "runtime_state_missing"
	}
	switch {
	case strings.TrimSpace(s.StopReason) != "":
		return true, s.StopReason
	case s.RemainingToolBudget <= 0 && s.Budget.RemainingToolCalls <= 0:
		return true, "budget_exhausted"
	case s.NoProgressRounds >= adaptiveNoProgressLimit(cfg):
		return true, "no_progress"
	case s.UncertaintyPlateauRounds >= adaptivePlateauLimit(cfg):
		return true, "uncertainty_plateau"
	case s.CurrentConfidence >= cfg.RefineConfidenceThreshold && len(s.UnresolvedEvidenceGaps) == 0:
		return true, "confidence_sufficient"
	default:
		return false, ""
	}
}

func adaptiveNoProgressLimit(cfg WorkflowConfig) int {
	if cfg.AdaptiveMaxNoProgressRounds > 0 {
		return cfg.AdaptiveMaxNoProgressRounds
	}
	return maxInt(cfg.MaxNoProgressRounds, 1)
}

func adaptivePlateauLimit(cfg WorkflowConfig) int {
	if cfg.AdaptiveMaxPlateauRounds > 0 {
		return cfg.AdaptiveMaxPlateauRounds
	}
	return maxInt(cfg.MaxUncertaintyPlateauRounds, 1)
}

func adaptiveToolHistory(state *workflowState) []string {
	if state == nil || len(state.toolCalls) == 0 {
		return nil
	}
	out := make([]string, 0, len(state.toolCalls))
	for _, call := range state.toolCalls {
		out = append(out, string(call.Tool))
	}
	return out
}

func adaptiveYieldHistory(state *workflowState) []AdaptiveToolYieldRecord {
	if state == nil || len(state.adaptiveNormalizedResults) == 0 {
		return nil
	}
	out := make([]AdaptiveToolYieldRecord, 0, len(state.adaptiveNormalizedResults))
	for _, result := range state.adaptiveNormalizedResults {
		out = append(out, AdaptiveToolYieldRecord{
			Tool:            result.Tool,
			ToolCallID:      result.ToolCallID,
			ResultQuality:   result.ResultQuality,
			LowYield:        result.LowYieldSignal,
			ConfidenceDelta: result.ConfidenceDelta,
			Summary:         truncateString(result.Summary, 160),
		})
	}
	return out
}
