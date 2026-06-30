package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type validationEffectInput struct {
	ActionID          string
	ExecutionCategory string
	Before            *ValidationEvidenceSnapshot
	After             *ValidationEvidenceSnapshot
	BeforeRisk        float64
	AfterRisk         float64
	Resolved          bool
	FallbackMode      string
	Note              string
}

func captureValidationEvidenceSnapshot(ctx context.Context, state *workflowState, label string) (*ValidationEvidenceSnapshot, error) {
	if state == nil {
		return nil, nil
	}
	snapshot := &ValidationEvidenceSnapshot{
		Label:            label,
		CapturedAt:       time.Now().UTC(),
		RiskScore:        state.risk.RiskScore,
		TriggeredSignals: triggeredSignalIDs(state.riskSignals),
		Security: ValidationSecurityContext{
			Score:             state.security.Score,
			Categories:        append([]string(nil), state.security.Categories...),
			FindingIDs:        append([]string(nil), state.security.FindingIDs...),
			Findings:          append([]string(nil), state.security.Findings...),
			CriticalFindings:  state.security.CriticalFindings,
			HighFindings:      state.security.HighFindings,
			SuspiciousTargets: append([]string(nil), state.security.SuspiciousPortCandidates...),
		},
		ValidationConfidence:    state.validationReport.Confidence,
		RecommendationViability: len(state.validationReport.ValidatedRecommendationIDs) - len(state.validationReport.RejectedRecommendationIDs),
	}

	queries := []struct {
		tool  ToolName
		query map[string]string
	}{
		{tool: ToolServiceHealth, query: map[string]string{"scope": firstNonEmpty(state.collectorID, "fleet"), "validation_category": "read_only_validation"}},
		{tool: ToolLogs, query: map[string]string{"query": state.incident.Summary, "validation_category": "read_only_validation"}},
		{tool: ToolMemoryPressure, query: map[string]string{"signals": "memory,oom,eviction", "validation_category": "read_only_validation"}},
		{tool: ToolConnectivityCheck, query: map[string]string{"signals": "network,dns,latency", "validation_category": "read_only_validation"}},
		{tool: ToolStorageHealth, query: map[string]string{"signals": "disk,storage,io", "validation_category": "read_only_validation"}},
		{tool: ToolSecurity, query: map[string]string{"query": state.incident.Summary, "validation_category": "read_only_validation"}},
	}

	for _, item := range queries {
		result, err := state.callToolAs(ctx, "post_action_validation", "validation_action_agent", item.tool, item.query, "post-action-"+sanitizeID(label))
		if err != nil {
			return snapshot, err
		}
		if len(state.toolCalls) > 0 {
			snapshot.EvidenceIDs = append(snapshot.EvidenceIDs, state.toolCalls[len(state.toolCalls)-1].ID)
		}
		switch item.tool {
		case ToolServiceHealth:
			if data, ok := result.Data.(serviceHealthToolData); ok {
				snapshot.ServiceHealth = data
			}
		case ToolLogs:
			if data, ok := result.Data.(logsToolData); ok {
				snapshot.LogErrors = data.Errors
				snapshot.LogWarnings = data.Warnings
			}
		case ToolMemoryPressure:
			if data, ok := result.Data.(memoryPressureToolData); ok {
				snapshot.MemoryPressure = data
			}
		case ToolConnectivityCheck:
			if data, ok := result.Data.(connectivityCheckToolData); ok {
				snapshot.Connectivity = data
			}
		case ToolStorageHealth:
			if data, ok := result.Data.(storageHealthToolData); ok {
				snapshot.Storage = data
			}
		case ToolSecurity:
			if data, ok := result.Data.(securityToolData); ok {
				snapshot.Security = ValidationSecurityContext{
					Score:             data.Score,
					Categories:        append([]string(nil), data.Categories...),
					FindingIDs:        append([]string(nil), data.FindingIDs...),
					Findings:          append([]string(nil), data.Findings...),
					CriticalFindings:  data.CriticalFindings,
					HighFindings:      data.HighFindings,
					SuspiciousTargets: append([]string(nil), data.SuspiciousPortCandidates...),
				}
			}
		}
	}

	snapshot.EvidenceIDs = dedupeStrings(snapshot.EvidenceIDs)
	return snapshot, nil
}

func buildPostActionValidationSummary(before, after *ValidationEvidenceSnapshot) PostActionValidationSummary {
	return summarizeValidationEffect(validationEffectInput{Before: before, After: after})
}

func summarizeValidationEffect(in validationEffectInput) PostActionValidationSummary {
	summary := PostActionValidationSummary{
		Verdict:           ValidationVerdictInsufficientEvidence,
		Summary:           firstNonEmpty(strings.TrimSpace(in.Note), "post-action verification did not collect comparable snapshots"),
		ActionID:          strings.TrimSpace(in.ActionID),
		ExecutionCategory: normalizeValidationCategory(in.ExecutionCategory),
		FallbackMode:      strings.TrimSpace(in.FallbackMode),
		BeforeSnapshot:    in.Before,
		AfterSnapshot:     in.After,
	}

	beforeRisk := in.BeforeRisk
	if in.Before != nil {
		beforeRisk = in.Before.RiskScore
	}
	afterRisk := in.AfterRisk
	if in.After != nil {
		afterRisk = in.After.RiskScore
	}
	summary.BeforeRisk = beforeRisk
	summary.AfterRisk = afterRisk

	comparison := buildValidationEffectComparison(in, beforeRisk, afterRisk)
	summary.Comparison = comparison
	summary.Delta = legacyValidationDelta(comparison)
	summary.SupportingEvidenceIDs = dedupeStrings(append(validationEvidenceIDs(in.Before), validationEvidenceIDs(in.After)...))

	switch {
	case comparison == nil || !comparison.Comparable:
		return summary
	case effectComparisonContradicted(comparison):
		summary.Verdict = ValidationVerdictContradicted
		summary.Summary = effectSummarySentence(comparison, beforeRisk, afterRisk, "post-action state regressed", firstNonEmpty(strings.TrimSpace(in.Note), "post-action verification did not show improvement"))
		summary.ContradictingEvidenceIDs = append([]string(nil), validationEvidenceIDs(in.After)...)
	case effectComparisonConfirmed(comparison, in.Resolved):
		summary.Verdict = ValidationVerdictConfirmed
		summary.Summary = effectSummarySentence(comparison, beforeRisk, afterRisk, "post-action state improved", firstNonEmpty(strings.TrimSpace(in.Note), "post-action verification confirmed improvement"))
	case effectComparisonPartiallySupported(comparison):
		summary.Verdict = ValidationVerdictPartiallySupported
		summary.Summary = effectSummarySentence(comparison, beforeRisk, afterRisk, "post-action state improved partially", firstNonEmpty(strings.TrimSpace(in.Note), "post-action verification showed mixed improvement"))
	default:
		summary.Verdict = ValidationVerdictInsufficientEvidence
		summary.Summary = firstNonEmpty(strings.TrimSpace(in.Note), "before/after snapshots were collected, but the effect stayed ambiguous")
		if comparison.Incomplete && len(comparison.MissingData) > 0 {
			summary.Summary = truncateString(summary.Summary+fmt.Sprintf(" | missing=%s", strings.Join(comparison.MissingData, ",")), 260)
		}
	}
	return summary
}

func beforeRiskDiff(before, after float64) float64 {
	return after - before
}

func triggeredSignalIDs(items []JointRiskSignal) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Triggered {
			out = append(out, item.ID)
		}
	}
	return dedupeStrings(out)
}

func buildValidationEffectComparison(in validationEffectInput, beforeRisk, afterRisk float64) *ValidationEffectComparison {
	comparison := &ValidationEffectComparison{}
	missing := make([]string, 0, 6)
	if in.Before == nil {
		missing = append(missing, "before_snapshot")
	}
	if in.After == nil {
		missing = append(missing, "after_snapshot")
	}

	riskAvailable := in.Before != nil || in.After != nil || beforeRisk > 0 || afterRisk > 0
	comparison.RiskScore = compareFloatMetric(beforeRisk, afterRisk, riskAvailable, true, 0.01, "lower risk is better")

	if in.Before != nil && in.After != nil {
		comparison.ServiceHealthy = compareBoolMetric(in.Before.ServiceHealth.Healthy, in.After.ServiceHealth.Healthy, true, true, "healthy service state is better")
		comparison.ServiceLatencyMS = compareFloatMetric(in.Before.ServiceHealth.LatencyMS, in.After.ServiceHealth.LatencyMS, true, true, 1.0, "lower latency is better")
		comparison.ServiceErrorRate = compareFloatMetric(in.Before.ServiceHealth.ErrorRate, in.After.ServiceHealth.ErrorRate, true, true, 0.001, "lower error rate is better")
		comparison.LogErrors = compareIntMetric(int64(in.Before.LogErrors), int64(in.After.LogErrors), true, true, "fewer log errors are better")
		comparison.LogWarnings = compareIntMetric(int64(in.Before.LogWarnings), int64(in.After.LogWarnings), true, true, "fewer log warnings are better")
		comparison.TriggeredSignals = compareIntMetric(int64(len(in.Before.TriggeredSignals)), int64(len(in.After.TriggeredSignals)), true, true, "fewer triggered signals are better")
		comparison.ValidationConfidence = compareFloatMetric(in.Before.ValidationConfidence, in.After.ValidationConfidence, true, false, 0.01, "higher validation confidence is better")
		comparison.RecommendationViability = compareIntMetric(int64(in.Before.RecommendationViability), int64(in.After.RecommendationViability), true, false, "higher recommendation viability is better")
		comparison.SecurityScore = compareFloatMetric(in.Before.Security.Score, in.After.Security.Score, true, true, 0.01, "lower security score is better")
	} else {
		missing = append(missing, "service_health", "logs", "triggered_signals", "validation_confidence", "recommendation_viability", "security_score")
	}

	comparison.MissingData = dedupeStrings(missing)
	comparison.Incomplete = len(comparison.MissingData) > 0
	comparison.Comparable = comparison.RiskScore.Available ||
		comparison.ServiceHealthy.Available ||
		comparison.ServiceLatencyMS.Available ||
		comparison.ServiceErrorRate.Available ||
		comparison.LogErrors.Available ||
		comparison.LogWarnings.Available ||
		comparison.TriggeredSignals.Available ||
		comparison.ValidationConfidence.Available ||
		comparison.RecommendationViability.Available ||
		comparison.SecurityScore.Available
	return comparison
}

func compareFloatMetric(before, after float64, available, lowerIsBetter bool, epsilon float64, note string) ValidationFloatComparison {
	out := ValidationFloatComparison{
		Available: available,
		Before:    before,
		After:     after,
		Delta:     after - before,
		Note:      note,
	}
	if !available {
		return out
	}
	if mathAbs(out.Delta) <= epsilon {
		return out
	}
	if lowerIsBetter {
		out.Improved = out.Delta < 0
		out.Regressed = out.Delta > 0
		return out
	}
	out.Improved = out.Delta > 0
	out.Regressed = out.Delta < 0
	return out
}

func compareIntMetric(before, after int64, available, lowerIsBetter bool, note string) ValidationIntComparison {
	out := ValidationIntComparison{
		Available: available,
		Before:    before,
		After:     after,
		Delta:     after - before,
		Note:      note,
	}
	if !available || out.Delta == 0 {
		return out
	}
	if lowerIsBetter {
		out.Improved = out.Delta < 0
		out.Regressed = out.Delta > 0
		return out
	}
	out.Improved = out.Delta > 0
	out.Regressed = out.Delta < 0
	return out
}

func compareBoolMetric(before, after bool, available, trueIsBetter bool, note string) ValidationBoolComparison {
	out := ValidationBoolComparison{
		Available: available,
		Before:    before,
		After:     after,
		Note:      note,
	}
	if !available || before == after {
		return out
	}
	if trueIsBetter {
		out.Improved = !before && after
		out.Regressed = before && !after
		return out
	}
	out.Improved = before && !after
	out.Regressed = !before && after
	return out
}

func legacyValidationDelta(comparison *ValidationEffectComparison) *ValidationStateDelta {
	if comparison == nil || !comparison.Comparable {
		return nil
	}
	return &ValidationStateDelta{
		RiskDelta:                    comparison.RiskScore.Delta,
		LatencyDeltaMS:               comparison.ServiceLatencyMS.Delta,
		ErrorRateDelta:               comparison.ServiceErrorRate.Delta,
		LogErrorDelta:                comparison.LogErrors.Delta,
		LogWarningDelta:              comparison.LogWarnings.Delta,
		TriggeredSignalDelta:         int(comparison.TriggeredSignals.Delta),
		HealthImproved:               comparison.ServiceHealthy.Improved || comparison.ServiceLatencyMS.Improved || comparison.ServiceErrorRate.Improved,
		SecurityImproved:             comparison.SecurityScore.Improved,
		ValidationConfidenceDelta:    comparison.ValidationConfidence.Delta,
		RecommendationViabilityDelta: int(comparison.RecommendationViability.Delta),
	}
}

func effectComparisonConfirmed(comparison *ValidationEffectComparison, resolved bool) bool {
	if comparison == nil || !comparison.Comparable {
		return false
	}
	improvements := countEffectImprovements(comparison)
	if improvements == 0 {
		return false
	}
	criticalImprovement := comparison.RiskScore.Improved && (comparison.ServiceHealthy.Improved || comparison.LogErrors.Improved || comparison.ServiceErrorRate.Improved || comparison.TriggeredSignals.Improved)
	if resolved && comparison.RiskScore.Improved && !effectComparisonContradicted(comparison) {
		return true
	}
	return criticalImprovement && countEffectRegressions(comparison) == 0
}

func effectComparisonContradicted(comparison *ValidationEffectComparison) bool {
	if comparison == nil || !comparison.Comparable {
		return false
	}
	if comparison.ServiceHealthy.Regressed || comparison.LogErrors.Regressed || comparison.ServiceErrorRate.Regressed || comparison.RecommendationViability.Regressed {
		return true
	}
	if comparison.RiskScore.Regressed && countEffectImprovements(comparison) == 0 {
		return true
	}
	return countEffectRegressions(comparison) > countEffectImprovements(comparison) && countEffectRegressions(comparison) > 0
}

func effectComparisonPartiallySupported(comparison *ValidationEffectComparison) bool {
	if comparison == nil || !comparison.Comparable {
		return false
	}
	return countEffectImprovements(comparison) > 0 && !effectComparisonConfirmed(comparison, false) && !effectComparisonContradicted(comparison)
}

func countEffectImprovements(comparison *ValidationEffectComparison) int {
	if comparison == nil {
		return 0
	}
	count := 0
	for _, improved := range []bool{
		comparison.RiskScore.Improved,
		comparison.ServiceHealthy.Improved,
		comparison.ServiceLatencyMS.Improved,
		comparison.ServiceErrorRate.Improved,
		comparison.LogErrors.Improved,
		comparison.LogWarnings.Improved,
		comparison.TriggeredSignals.Improved,
		comparison.ValidationConfidence.Improved,
		comparison.RecommendationViability.Improved,
		comparison.SecurityScore.Improved,
	} {
		if improved {
			count++
		}
	}
	return count
}

func countEffectRegressions(comparison *ValidationEffectComparison) int {
	if comparison == nil {
		return 0
	}
	count := 0
	for _, regressed := range []bool{
		comparison.RiskScore.Regressed,
		comparison.ServiceHealthy.Regressed,
		comparison.ServiceLatencyMS.Regressed,
		comparison.ServiceErrorRate.Regressed,
		comparison.LogErrors.Regressed,
		comparison.LogWarnings.Regressed,
		comparison.TriggeredSignals.Regressed,
		comparison.ValidationConfidence.Regressed,
		comparison.RecommendationViability.Regressed,
		comparison.SecurityScore.Regressed,
	} {
		if regressed {
			count++
		}
	}
	return count
}

func effectSummarySentence(comparison *ValidationEffectComparison, beforeRisk, afterRisk float64, prefix, fallback string) string {
	if comparison == nil {
		return fallback
	}
	parts := []string{prefix}
	if comparison.RiskScore.Available {
		parts = append(parts, fmt.Sprintf("risk %.2f -> %.2f", beforeRisk, afterRisk))
	}
	if comparison.ServiceLatencyMS.Available {
		parts = append(parts, fmt.Sprintf("latency %.1fms -> %.1fms", comparison.ServiceLatencyMS.Before, comparison.ServiceLatencyMS.After))
	}
	if comparison.LogErrors.Available {
		parts = append(parts, fmt.Sprintf("log errors %d -> %d", comparison.LogErrors.Before, comparison.LogErrors.After))
	}
	if comparison.ValidationConfidence.Available && comparison.ValidationConfidence.Improved {
		parts = append(parts, fmt.Sprintf("validation confidence %.2f -> %.2f", comparison.ValidationConfidence.Before, comparison.ValidationConfidence.After))
	}
	if comparison.Incomplete && len(comparison.MissingData) > 0 {
		parts = append(parts, fmt.Sprintf("missing=%s", strings.Join(comparison.MissingData, ",")))
	}
	return truncateString(strings.Join(parts, " | "), 260)
}

func validationEvidenceIDs(snapshot *ValidationEvidenceSnapshot) []string {
	if snapshot == nil {
		return nil
	}
	return append([]string(nil), snapshot.EvidenceIDs...)
}

func selectedGuardedActionCandidate(state *workflowState) *ValidationActionCandidate {
	if state == nil {
		return nil
	}
	candidates := state.analysisHandoff.BoundedActionCandidates
	if len(candidates) == 0 {
		return nil
	}
	if !state.engine.cfg.ValidationAgentEnabled || state.validationReport.Agent == "" {
		copy := candidates[0]
		return &copy
	}
	validated := make(map[string]struct{}, len(state.validationReport.ValidatedRecommendationIDs))
	for _, id := range state.validationReport.ValidatedRecommendationIDs {
		validated[id] = struct{}{}
	}
	for _, candidate := range candidates {
		if len(validated) == 0 {
			if candidate.Safe {
				copy := candidate
				return &copy
			}
			continue
		}
		if _, ok := validated[candidate.RecommendationID]; ok {
			copy := candidate
			return &copy
		}
	}
	return nil
}

func appendValidationActionNote(report *ValidationActionReport, note string) {
	if report == nil || strings.TrimSpace(note) == "" {
		return
	}
	report.ActionSummary = dedupeStrings(append(report.ActionSummary, note))
}
