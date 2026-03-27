package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

type ValidationActionAgent struct {
	cfg    WorkflowConfig
	logger *zap.Logger
}

func newValidationActionAgent(cfg WorkflowConfig, logger *zap.Logger) *ValidationActionAgent {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ValidationActionAgent{
		cfg:    cfg,
		logger: logger.With(zap.String("component", "validation_action_agent")),
	}
}

func classifyValidationFocus(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(text, "deploy"), strings.Contains(text, "rollout"), strings.Contains(text, "config"), strings.Contains(text, "revision"), strings.Contains(text, "feature flag"), strings.Contains(text, "driver"):
		return "change"
	case strings.Contains(text, "gpu"), strings.Contains(text, "cuda"), strings.Contains(text, "nvidia"):
		return "gpu"
	case strings.Contains(text, "security"), strings.Contains(text, "permission"), strings.Contains(text, "suspicious"), strings.Contains(text, "port"), strings.Contains(text, "process graph"), strings.Contains(text, "ebpf"):
		return "security"
	case strings.Contains(text, "dns"), strings.Contains(text, "network"), strings.Contains(text, "connect"), strings.Contains(text, "latency"), strings.Contains(text, "timeout"):
		return "network"
	case strings.Contains(text, "memory"), strings.Contains(text, "oom"), strings.Contains(text, "evict"), strings.Contains(text, "leak"):
		return "memory"
	case strings.Contains(text, "disk"), strings.Contains(text, "storage"), strings.Contains(text, "io"):
		return "storage"
	case strings.Contains(text, "kubernetes"), strings.Contains(text, "pod"), strings.Contains(text, "container"), strings.Contains(text, "workload"):
		return "kubernetes"
	default:
		return "resource"
	}
}

func suggestedToolsForFocus(focus string, targetType ValidationTargetType) []ToolName {
	switch targetType {
	case ValidationTargetRecommendation:
		return []ToolName{ToolRunbookRetrieval, ToolSimilarCase, ToolHistoricalIncident, ToolActionOutcome, ToolServiceHealth}
	case ValidationTargetChangeCorrelation:
		return []ToolName{ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolLogs}
	case ValidationTargetRemediation:
		return []ToolName{ToolMetrics, ToolServiceHealth, ToolLogs, ToolConnectivityCheck}
	case ValidationTargetContradiction:
		switch focus {
		case "change":
			return []ToolName{ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolLogs}
		case "security":
			return []ToolName{ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery}
		case "gpu":
			return []ToolName{ToolGPU, ToolMetrics, ToolLogs}
		case "network":
			return []ToolName{ToolConnectivityCheck, ToolDNSCheck, ToolServiceHealth, ToolTopology}
		case "memory":
			return []ToolName{ToolMemoryPressure, ToolMetrics, ToolLogs}
		case "storage":
			return []ToolName{ToolStorageHealth, ToolMetrics, ToolLogs}
		case "kubernetes":
			return []ToolName{ToolKubernetesResource, ToolContainerRevision, ToolDeploymentHistory}
		default:
			return []ToolName{ToolMetrics, ToolLogs, ToolServiceHealth}
		}
	default:
		switch focus {
		case "change":
			return []ToolName{ToolChangeQuery, ToolDeploymentHistory, ToolConfigState, ToolLogs}
		case "security":
			return []ToolName{ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery}
		case "gpu":
			return []ToolName{ToolGPU, ToolMetrics, ToolLogs, ToolServiceHealth}
		case "network":
			return []ToolName{ToolConnectivityCheck, ToolDNSCheck, ToolServiceHealth, ToolNetworkBlastRadius, ToolTopology}
		case "memory":
			return []ToolName{ToolMemoryPressure, ToolMetrics, ToolLogs, ToolServiceHealth}
		case "storage":
			return []ToolName{ToolStorageHealth, ToolMetrics, ToolLogs}
		case "kubernetes":
			return []ToolName{ToolKubernetesResource, ToolContainerRevision, ToolDeploymentHistory, ToolLogs}
		default:
			return []ToolName{ToolMetrics, ToolLogs, ToolServiceHealth, ToolProcessLineage}
		}
	}
}

func (a *ValidationActionAgent) Run(ctx context.Context, state *workflowState, handoff AnalysisHandoff) ValidationActionReport {
	report := ValidationActionReport{
		Agent:        "validation_action_agent",
		Mode:         "bounded_react",
		StartedAt:    time.Now().UTC(),
		TargetLimit:  a.cfg.ValidationTargetLimit,
		ReadOnlyOnly: a.cfg.ValidationReadOnlyOnly,
	}
	if !a.cfg.ValidationAgentEnabled {
		report.Mode = "disabled"
		report.StopReason = "validation agent disabled"
		report.DegradedFallbackReason = "config disabled"
		report.CompletedAt = time.Now().UTC()
		return report
	}
	if len(handoff.SuggestedValidationTargets) == 0 {
		report.Mode = "deterministic_report"
		report.StopReason = "no validation targets"
		report.DegradedFallbackReason = "analysis handoff did not produce validation targets"
		report.CompletedAt = time.Now().UTC()
		return report
	}

	timeout := a.cfg.ValidationTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	targets := append([]ValidationTarget(nil), handoff.SuggestedValidationTargets...)
	if limit := a.cfg.ValidationTargetLimit; limit > 0 && len(targets) > limit {
		targets = targets[:limit]
	}
	report.Targets = targets

	for _, target := range targets {
		if report.Iterations >= a.cfg.ValidationMaxIterations || report.ToolCalls >= a.cfg.ValidationMaxToolCalls {
			report.StopReason = "validation budget reached"
			break
		}
		result := a.validateTarget(runCtx, state, handoff, target, &report)
		report.Results = append(report.Results, result)
		if result.StopReason == "validation budget reached" {
			report.StopReason = "validation budget reached"
			break
		}
	}

	if report.StopReason == "" {
		report.StopReason = "targets exhausted"
	}
	report.Confidence = validationReportConfidence(report.Results)
	report.ValidatedRecommendationIDs, report.RejectedRecommendationIDs = recommendationVerdicts(report.Results)
	report.ContradictionSummary = contradictionSummary(report.Results)
	report.ActionSummary = validationActionSummary(report.Results)
	report.UnresolvedUncertainty = unresolvedValidationGaps(report)
	report.CompletedAt = time.Now().UTC()
	return report
}

func (a *ValidationActionAgent) validateTarget(ctx context.Context, state *workflowState, handoff AnalysisHandoff, target ValidationTarget, report *ValidationActionReport) ValidationTargetResult {
	result := ValidationTargetResult{
		TargetID:                 target.ID,
		TargetType:               target.Type,
		Title:                    target.Title,
		HypothesisID:             target.HypothesisID,
		RecommendationID:         target.RecommendationID,
		Verdict:                  ValidationVerdictInsufficientEvidence,
		Confidence:               0.35,
		SupportingEvidenceIDs:    append([]string(nil), target.SupportingEvidenceIDs...),
		ContradictingEvidenceIDs: append([]string(nil), target.ContradictingEvidenceIDs...),
	}
	tools := target.SuggestedTools
	if len(tools) == 0 {
		tools = suggestedToolsForFocus(firstNonEmpty(target.Focus, classifyValidationFocus(strings.Join([]string{target.Title, target.Summary}, " "))), target.Type)
	}
	used := make(map[ToolName]struct{}, len(tools))
	for _, tool := range tools {
		if report.Iterations >= a.cfg.ValidationMaxIterations || report.ToolCalls >= a.cfg.ValidationMaxToolCalls {
			result.StopReason = "validation budget reached"
			break
		}
		if _, ok := used[tool]; ok {
			continue
		}
		used[tool] = struct{}{}
		query := validationQueryForTarget(state, handoff, target, tool)
		toolReason := validationToolReason(target, tool)
		toolResult, err := state.callToolAs(ctx, "validation_action_react_loop", "validation_action_agent", tool, query, target.ID)
		report.Iterations++
		report.ToolCalls++
		record := ValidationLoopRecord{
			Iteration:  report.Iterations,
			TargetID:   target.ID,
			Tool:       tool,
			ToolReason: toolReason,
			Timestamp:  time.Now().UTC(),
			Verdict:    result.Verdict,
		}
		if err != nil {
			record.Observation = truncateString(err.Error(), 220)
			record.StopReason = "tool call failed"
			report.LoopRecords = append(report.LoopRecords, record)
			continue
		}
		if len(state.toolCalls) > 0 {
			record.ToolCallID = state.toolCalls[len(state.toolCalls)-1].ID
		}
		delta, verdict, observation, supporting, contradicting := assessValidationObservation(target, tool, toolResult)
		record.Observation = observation
		record.Verdict = verdict
		record.ConfidenceDelta = delta
		record.SupportingEvidenceIDs = supporting
		record.ContradictingEvidenceIDs = contradicting
		report.LoopRecords = append(report.LoopRecords, record)

		result.ToolSequence = append(result.ToolSequence, tool)
		result.SupportingEvidenceIDs = dedupeStrings(append(result.SupportingEvidenceIDs, supporting...))
		result.ContradictingEvidenceIDs = dedupeStrings(append(result.ContradictingEvidenceIDs, contradicting...))
		result.Confidence = clamp01(result.Confidence + delta)
		result.Verdict = strongerValidationVerdict(result.Verdict, verdict)
		if result.Summary == "" {
			result.Summary = observation
		} else if observation != "" {
			result.Summary = truncateString(result.Summary+"; "+observation, 260)
		}

		if validationShouldStop(result, a.cfg.ValidationConfidenceThreshold) {
			result.StopReason = stopReasonForVerdict(result.Verdict)
			break
		}
	}
	if result.Summary == "" {
		result.Summary = "validation loop finished without decisive evidence"
	}
	if result.StopReason == "" {
		result.StopReason = "target tool sequence exhausted"
	}
	return result
}

func validationQueryForTarget(state *workflowState, handoff AnalysisHandoff, target ValidationTarget, tool ToolName) map[string]string {
	query := map[string]string{
		"query":            firstNonEmpty(target.Title, target.Summary, handoff.IncidentSummary),
		"incident_summary": handoff.IncidentSummary,
		"scope":            firstNonEmpty(state.collectorID, handoff.CollectorID, "fleet"),
		"target_type":      string(target.Type),
		"focus":            firstNonEmpty(target.Focus, classifyValidationFocus(strings.Join([]string{target.Title, target.Summary}, " "))),
	}
	switch tool {
	case ToolLogs:
		query["query"] = strings.ToLower(strings.Join([]string{target.Title, target.Summary, "error warn timeout restart oom deploy"}, " "))
	case ToolDeploymentHistory:
		query["category"] = "deploy"
	case ToolConfigState:
		query["category"] = "config"
	case ToolActionOutcome:
		query["query"] = strings.Join([]string{target.Title, target.Summary, handoff.IncidentSummary}, " ")
	case ToolMemoryPressure:
		query["signals"] = "memory,oom,eviction"
	case ToolConnectivityCheck, ToolDNSCheck, ToolNetworkBlastRadius:
		query["signals"] = "network,dns,latency"
	case ToolKubernetesResource, ToolContainerRevision:
		query["signals"] = "pod,container,workload,revision"
	}
	return query
}

func validationToolReason(target ValidationTarget, tool ToolName) string {
	switch tool {
	case ToolChangeQuery, ToolDeploymentHistory, ToolConfigState:
		return "target mentions rollout, config, or revision context"
	case ToolMetrics, ToolMemoryPressure, ToolStorageHealth:
		return "target needs current resource evidence before verdict changes"
	case ToolConnectivityCheck, ToolDNSCheck, ToolNetworkBlastRadius, ToolServiceHealth:
		return "target needs service or network health evidence"
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery:
		return "target needs runtime or security contradiction search"
	case ToolRunbookRetrieval, ToolSimilarCase, ToolHistoricalIncident, ToolActionOutcome:
		return "target is validating whether a recommendation matches prior verified practice"
	case ToolGPU:
		return "target needs GPU pressure or attribution evidence"
	default:
		return "target needs one more deterministic check"
	}
}

func assessValidationObservation(target ValidationTarget, tool ToolName, result workflowToolResult) (float64, ValidationVerdict, string, []string, []string) {
	supporting := []string{fmt.Sprintf("ev-%s", sanitizeID(target.ID))}
	contradicting := []string{}
	switch tool {
	case ToolChangeQuery, ToolDeploymentHistory:
		data, ok := result.Data.(changeToolData)
		if !ok {
			return -0.08, ValidationVerdictInsufficientEvidence, "change evidence payload unavailable", nil, nil
		}
		if len(data.Events) == 0 {
			if target.Type == ValidationTargetChangeCorrelation || target.Focus == "change" {
				return -0.18, ValidationVerdictContradicted, "no correlated deployment or config changes matched the incident window", nil, supporting
			}
			return -0.04, ValidationVerdictInsufficientEvidence, "change query returned no strong matches", nil, nil
		}
		for _, event := range data.Events {
			supporting = append(supporting, event.Event.ChangeID)
		}
		return 0.22, ValidationVerdictConfirmed, truncateString(firstNonEmpty(data.Summary, fmt.Sprintf("correlated changes=%d", len(data.Events))), 220), dedupeStrings(supporting), nil
	case ToolConfigState:
		data, ok := result.Data.(configStateToolData)
		if !ok {
			return -0.05, ValidationVerdictInsufficientEvidence, "config state payload unavailable", nil, nil
		}
		if len(data.Changes) == 0 && len(data.Labels) == 0 {
			return -0.16, ValidationVerdictContradicted, "no config or revision state supports the change hypothesis", nil, supporting
		}
		return 0.16, ValidationVerdictPartiallySupported, data.Summary, dedupeStrings(supporting), nil
	case ToolMetrics, ToolServiceHealth, ToolMemoryPressure, ToolStorageHealth, ToolConnectivityCheck, ToolDNSCheck, ToolNetworkBlastRadius, ToolGPU:
		if strings.TrimSpace(result.Summary) == "" {
			return -0.04, ValidationVerdictInsufficientEvidence, "observability query returned no summary", nil, nil
		}
		if target.Type == ValidationTargetContradiction && looksHealthyValidationSummary(result.Summary) {
			contradicting = append(contradicting, supporting...)
			return -0.16, ValidationVerdictContradicted, truncateString(result.Summary, 220), nil, contradicting
		}
		return 0.12, ValidationVerdictPartiallySupported, truncateString(result.Summary, 220), dedupeStrings(supporting), nil
	case ToolLogs:
		data, ok := result.Data.(logsToolData)
		if !ok {
			return -0.05, ValidationVerdictInsufficientEvidence, "log payload unavailable", nil, nil
		}
		if data.Errors+data.Warnings == 0 && len(data.Snippets) == 0 {
			if target.Type == ValidationTargetContradiction {
				contradicting = append(contradicting, supporting...)
				return -0.14, ValidationVerdictContradicted, "selected log window did not reinforce the current hypothesis", nil, contradicting
			}
			return -0.08, ValidationVerdictInsufficientEvidence, "logs did not add supporting evidence", nil, nil
		}
		return 0.14, ValidationVerdictPartiallySupported, fmt.Sprintf("logs errors=%d warnings=%d", data.Errors, data.Warnings), dedupeStrings(supporting), nil
	case ToolRunbookRetrieval, ToolHistoricalIncident, ToolSimilarCase, ToolActionOutcome:
		if data, ok := result.Data.(knowledgeToolData); ok {
			if len(data.Hits) == 0 {
				return -0.08, ValidationVerdictInsufficientEvidence, "knowledge retrieval found no validated analogues", nil, nil
			}
			supporting = append(supporting, data.EvidenceIDs...)
			return 0.18, ValidationVerdictPartiallySupported, truncateString(firstNonEmpty(data.Summary, fmt.Sprintf("knowledge hits=%d", len(data.Hits))), 220), dedupeStrings(supporting), nil
		}
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery, ToolKubernetesResource, ToolContainerRevision:
		if strings.TrimSpace(result.Summary) != "" {
			return 0.14, ValidationVerdictPartiallySupported, truncateString(result.Summary, 220), dedupeStrings(supporting), nil
		}
	}
	return 0.0, ValidationVerdictInsufficientEvidence, truncateString(firstNonEmpty(result.Summary, "validation check completed"), 220), dedupeStrings(supporting), nil
}

func looksHealthyValidationSummary(summary string) bool {
	summary = strings.ToLower(strings.TrimSpace(summary))
	return strings.Contains(summary, "healthy") || strings.Contains(summary, "no major findings") || strings.Contains(summary, "returned no strong matches")
}

func strongerValidationVerdict(current, next ValidationVerdict) ValidationVerdict {
	score := func(verdict ValidationVerdict) int {
		switch verdict {
		case ValidationVerdictConfirmed, ValidationVerdictContradicted:
			return 3
		case ValidationVerdictPartiallySupported:
			return 2
		default:
			return 1
		}
	}
	if score(next) >= score(current) {
		return next
	}
	return current
}

func validationShouldStop(result ValidationTargetResult, threshold float64) bool {
	switch result.Verdict {
	case ValidationVerdictConfirmed, ValidationVerdictContradicted:
		return result.Confidence >= maxFloat(0.5, threshold-0.1)
	case ValidationVerdictPartiallySupported:
		return result.Confidence >= threshold
	default:
		return false
	}
}

func stopReasonForVerdict(verdict ValidationVerdict) string {
	switch verdict {
	case ValidationVerdictConfirmed:
		return "support threshold reached"
	case ValidationVerdictContradicted:
		return "contradiction threshold reached"
	case ValidationVerdictPartiallySupported:
		return "partial support threshold reached"
	default:
		return "insufficient evidence"
	}
}

func validationReportConfidence(results []ValidationTargetResult) float64 {
	if len(results) == 0 {
		return 0
	}
	total := 0.0
	for _, result := range results {
		total += clamp01(result.Confidence)
	}
	return clamp01(total / float64(len(results)))
}

func recommendationVerdicts(results []ValidationTargetResult) ([]string, []string) {
	validated := []string{}
	rejected := []string{}
	for _, result := range results {
		if result.TargetType != ValidationTargetRecommendation {
			continue
		}
		switch result.Verdict {
		case ValidationVerdictConfirmed, ValidationVerdictPartiallySupported:
			validated = append(validated, firstNonEmpty(result.RecommendationID, result.TargetID))
		case ValidationVerdictContradicted:
			rejected = append(rejected, firstNonEmpty(result.RecommendationID, result.TargetID))
		}
	}
	return dedupeStrings(validated), dedupeStrings(rejected)
}

func contradictionSummary(results []ValidationTargetResult) []string {
	out := []string{}
	for _, result := range results {
		if result.Verdict != ValidationVerdictContradicted {
			continue
		}
		out = append(out, firstNonEmpty(result.Summary, result.Title))
	}
	return dedupeStrings(out)
}

func validationActionSummary(results []ValidationTargetResult) []string {
	out := []string{}
	for _, result := range results {
		if result.TargetType != ValidationTargetRecommendation {
			continue
		}
		if result.Verdict == ValidationVerdictConfirmed || result.Verdict == ValidationVerdictPartiallySupported {
			out = append(out, result.Title)
		}
	}
	return dedupeStrings(out)
}

func unresolvedValidationGaps(report ValidationActionReport) []string {
	out := []string{}
	for _, result := range report.Results {
		if result.Verdict == ValidationVerdictInsufficientEvidence {
			out = append(out, firstNonEmpty(result.Summary, result.Title))
		}
	}
	if report.DegradedFallbackReason != "" {
		out = append(out, report.DegradedFallbackReason)
	}
	return dedupeStrings(out)
}

func applyValidationReport(state *workflowState, report ValidationActionReport) {
	if state == nil {
		return
	}
	for _, result := range report.Results {
		if result.TargetType != ValidationTargetHypothesis || result.HypothesisID == "" {
			continue
		}
		for idx := range state.hypotheses {
			if state.hypotheses[idx].ID == "" || state.hypotheses[idx].ID != result.HypothesisID {
				continue
			}
			switch result.Verdict {
			case ValidationVerdictConfirmed:
				state.hypotheses[idx].Confidence = clamp01(state.hypotheses[idx].Confidence + 0.10)
			case ValidationVerdictPartiallySupported:
				state.hypotheses[idx].Confidence = clamp01(state.hypotheses[idx].Confidence + 0.04)
			case ValidationVerdictContradicted:
				state.hypotheses[idx].Confidence = clamp01(state.hypotheses[idx].Confidence - 0.18)
			default:
				state.hypotheses[idx].Confidence = clamp01(state.hypotheses[idx].Confidence - 0.03)
			}
			state.hypotheses[idx].EvidenceIDs = dedupeStrings(append(state.hypotheses[idx].EvidenceIDs, result.SupportingEvidenceIDs...))
			state.hypotheses[idx].ContradictingEvidenceIDs = dedupeStrings(append(state.hypotheses[idx].ContradictingEvidenceIDs, result.ContradictingEvidenceIDs...))
		}
	}
}
