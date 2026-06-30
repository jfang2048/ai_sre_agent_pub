package agent

import "strings"

func finalizeNormalizedToolResult(state *workflowState, result *NormalizedToolResult) *NormalizedToolResult {
	if result == nil {
		return nil
	}
	if result.ConfidenceDelta == 0 {
		result.ConfidenceDelta = result.ConfidenceContribution
	}
	if result.ContradictionDelta == 0 {
		result.ContradictionDelta = result.ContradictionContribution
	}
	if result.HypothesisSpaceNarrowed {
		result.NarrowsHypothesisSpace = true
	}
	if len(result.RecommendedScopeRefinement) == 0 {
		result.RecommendedScopeRefinement = append([]string(nil), result.AffectedScope...)
	}
	if result.RecommendedTimeWindowRefine == "" {
		result.RecommendedTimeWindowRefine = suggestedWindowRefinement(result)
	}
	if result.ResultQuality == "" {
		result.ResultQuality = inferNormalizedResultQuality(result)
	}
	if result.Cacheability == "" {
		result.Cacheability = inferCacheability(result)
	}
	if !result.LowYieldSignal {
		result.LowYieldSignal = inferLowYield(result)
	}
	result.EvidenceIDs = dedupeStrings(result.EvidenceIDs)
	result.StructuredFindings = dedupeStrings(result.StructuredFindings)
	result.AffectedScope = dedupeStrings(result.AffectedScope)
	result.RecommendedScopeRefinement = dedupeStrings(result.RecommendedScopeRefinement)
	result.LikelyNextToolFamilies = dedupeStrings(result.LikelyNextToolFamilies)
	result.LikelyNextChecks = dedupeStrings(result.LikelyNextChecks)
	if state != nil && len(result.RecommendedScopeRefinement) == 0 && strings.TrimSpace(state.collectorID) != "" {
		result.RecommendedScopeRefinement = []string{state.collectorID}
	}
	return result
}

func inferNormalizedResultQuality(result *NormalizedToolResult) string {
	switch {
	case result == nil:
		return "low"
	case strings.Contains(strings.ToLower(strings.TrimSpace(result.Summary)), "no findings"), strings.Contains(strings.ToLower(strings.TrimSpace(result.Summary)), "unavailable"):
		return "low"
	case len(result.StructuredFindings) >= 2 || len(result.EvidenceIDs) >= 2:
		return "high"
	case len(result.StructuredFindings) == 1 || strings.TrimSpace(result.Summary) != "":
		return "medium"
	default:
		return "low"
	}
}

func inferLowYield(result *NormalizedToolResult) bool {
	if result == nil {
		return true
	}
	summary := strings.ToLower(strings.TrimSpace(result.Summary))
	return len(result.StructuredFindings) == 0 &&
		len(result.EvidenceIDs) == 0 &&
		!result.NarrowsHypothesisSpace &&
		!result.HypothesisSpaceNarrowed &&
		(strings.TrimSpace(result.Summary) == "" || strings.Contains(summary, "no findings") || strings.Contains(summary, "unavailable"))
}

func inferCacheability(result *NormalizedToolResult) string {
	if result == nil {
		return "none"
	}
	switch strings.ToLower(strings.TrimSpace(result.Freshness)) {
	case "recent", "high":
		return "short_ttl"
	case "historical", "low":
		return "long_ttl"
	default:
		return "bounded_ttl"
	}
}

func suggestedWindowRefinement(result *NormalizedToolResult) string {
	if result == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(result.Freshness)) {
	case "recent":
		return "10m"
	case "historical":
		return "2h"
	default:
		return "30m"
	}
}
