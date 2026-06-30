package agent

import (
	"fmt"
	"strings"
	"time"
)

type QueryShape struct {
	Query                    map[string]string `json:"query"`
	RecommendedScope         []string          `json:"recommended_scope,omitempty"`
	RecommendedTimeWindow    string            `json:"recommended_time_window,omitempty"`
	InjectedFilters          []string          `json:"injected_filters,omitempty"`
	ResultQualityExpectation string            `json:"result_quality_expectation,omitempty"`
}

func shapeToolQuery(state *workflowState, contract ToolContract, context ToolSelectionContext) QueryShape {
	if state == nil {
		state = &workflowState{}
	}
	scopeHints := make([]string, 0, 6)
	scopeHints = append(scopeHints, context.ScopeHints...)
	if context.Target != nil {
		scopeHints = append(scopeHints, context.Target.ImpactedScope...)
		if context.Target.HypothesisID != "" {
			scopeHints = append(scopeHints, "hypothesis:"+context.Target.HypothesisID)
		}
		if context.Target.RecommendationID != "" {
			scopeHints = append(scopeHints, "recommendation:"+context.Target.RecommendationID)
		}
		if context.Target.ActionCandidateID != "" {
			scopeHints = append(scopeHints, "action_candidate:"+context.Target.ActionCandidateID)
		}
	}
	for idx := len(state.adaptiveNormalizedResults) - 1; idx >= 0; idx-- {
		scopeHints = append(scopeHints, state.adaptiveNormalizedResults[idx].RecommendedScopeRefinement...)
		if len(scopeHints) >= 6 {
			break
		}
	}
	scopeHints = dedupeStrings(compactStrings(scopeHints...))

	window := shapedTimeWindow(state, contract, context)
	filters := sceneEventFilters(context.SceneFamily, state)
	filters = append(filters, contract.PreferredQueryHints.DefaultTerms...)
	for idx := len(state.adaptiveNormalizedResults) - 1; idx >= 0; idx-- {
		filters = append(filters, state.adaptiveNormalizedResults[idx].LikelyNextChecks...)
		if len(filters) >= 8 {
			break
		}
	}
	if context.Target != nil && len(context.Target.ExpectedSignals) > 0 {
		filters = append(filters, context.Target.ExpectedSignals...)
	}
	filters = dedupeStrings(compactStrings(filters...))

	queryText := firstNonEmpty(context.Objective, context.IncidentSummary, adaptiveObjective(state))
	if len(filters) > 0 && (contract.Name == ToolLogs || contract.Name == ToolRunbookRetrieval || contract.Name == ToolHistoricalIncident || contract.Name == ToolSimilarCase || contract.Name == ToolActionOutcome) {
		queryText = strings.Join(compactStrings(queryText, strings.Join(filters, " ")), " ")
	}

	query := map[string]string{
		"query":            truncateString(queryText, 420),
		"incident_summary": firstNonEmpty(context.IncidentSummary, adaptiveObjective(state)),
		"scope":            firstNonEmpty(strings.Join(scopeHints, ","), firstNonEmpty(context.CollectorID, state.collectorID, "fleet")),
		"window":           window,
		"scene_family":     string(context.SceneFamily),
		"evidence_gaps":    strings.Join(context.EvidenceGaps, ","),
		"contradictions":   strings.Join(context.Contradictions, ","),
		"query_hints":      strings.Join(contract.PreferredQueryHints.DefaultTerms, "; "),
	}
	if len(filters) > 0 {
		query["event_filters"] = strings.Join(filters, ",")
	}
	if context.Target != nil {
		query["target_type"] = string(context.Target.Type)
		query["focus"] = firstNonEmpty(context.Target.Focus, deriveValidationFocus(context.Target.ToolFamilies, context.Target.Title, context.Target.Summary))
		query["validation_category"] = firstNonEmpty(context.Target.ExecutionCategory, "read_only_validation")
		query["read_only"] = fmt.Sprintf("%t", context.Target.ReadOnly)
		query["impacted_scope"] = strings.Join(context.Target.ImpactedScope, ",")
		query["change_categories"] = strings.Join(context.Target.ChangeCategories, ",")
	}

	switch contract.Name {
	case ToolLogs:
		query["query"] = strings.ToLower(strings.Join(compactStrings(queryText, strings.Join(filters, " "), "error warn timeout restart"), " "))
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase, ToolActionOutcome:
		query["top_k"] = "4"
		query["intent"] = firstNonEmpty(string(contract.CapabilityFamily), "general")
	case ToolDeploymentHistory:
		query["category"] = "deploy"
	case ToolConfigState:
		query["category"] = "config"
	case ToolMemoryPressure:
		query["signals"] = "memory,oom,eviction"
	case ToolConnectivityCheck, ToolDNSCheck, ToolNetworkBlastRadius:
		query["signals"] = "network,dns,latency"
	case ToolKubernetesResource, ToolContainerRevision:
		query["signals"] = "pod,container,workload,revision"
	case ToolStorageHealth:
		query["signals"] = "disk,storage,io"
	case ToolGPU:
		query["signals"] = "gpu,cuda,collective"
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery:
		query["signals"] = "runtime,process,security"
	}

	return QueryShape{
		Query:                    query,
		RecommendedScope:         scopeHints,
		RecommendedTimeWindow:    window,
		InjectedFilters:          filters,
		ResultQualityExpectation: expectedQueryResultQuality(contract, context, scopeHints, filters),
	}
}

func shapedTimeWindow(state *workflowState, contract ToolContract, context ToolSelectionContext) string {
	for idx := len(state.adaptiveNormalizedResults) - 1; idx >= 0; idx-- {
		if candidate := strings.TrimSpace(state.adaptiveNormalizedResults[idx].RecommendedTimeWindowRefine); candidate != "" {
			return candidate
		}
	}
	base := context.Window
	if base <= 0 {
		base = state.window
	}
	if base <= 0 {
		base = 30 * time.Minute
	}
	switch contract.FreshnessSensitivity {
	case ToolFreshnessSensitivityHigh:
		base = maxDuration(5*time.Minute, base/2)
	case ToolFreshnessSensitivityMedium:
		base = maxDuration(10*time.Minute, (base*2)/3)
	default:
		base = minDuration(90*time.Minute, base)
	}
	if context.Target != nil && context.Target.Type == ValidationTargetSceneClassification {
		base = minDuration(base, 15*time.Minute)
	}
	return base.String()
}

func expectedQueryResultQuality(contract ToolContract, context ToolSelectionContext, scopeHints, filters []string) string {
	switch {
	case len(scopeHints) > 0 && len(filters) > 0:
		return "high"
	case len(context.EvidenceGaps) > 0 && contract.ReadOnly:
		return "medium"
	default:
		return "low"
	}
}
