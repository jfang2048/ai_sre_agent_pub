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

func (a *ValidationActionAgent) Run(ctx context.Context, state *workflowState) ValidationActionReport {
	if !a.cfg.AgentMessageProtocolEnabled {
		return a.RunDecoded(ctx, state, state.analysisHandoff)
	}
	report := ValidationActionReport{
		SchemaVersion: ValidationActionReportSchemaVersion,
		Agent:         "validation_action_agent",
		IncidentID:    firstNonEmpty(state.rca.IncidentID, state.analysisHandoff.IncidentID, state.workflowID),
		CorrelationID: firstNonEmpty(state.workflowID, state.rca.TraceID),
		Mode:          "bounded_react",
		StartedAt:     time.Now().UTC(),
		TargetLimit:   a.cfg.ValidationTargetLimit,
		ReadOnlyOnly:  a.cfg.ValidationReadOnlyOnly,
	}
	if !a.cfg.ValidationAgentEnabled {
		report.Mode = "disabled"
		report.StopReason = "validation agent disabled"
		report.DegradedFallbackReason = "config disabled"
		report.CompletedAt = time.Now().UTC()
		return report
	}
	loadStarted := time.Now().UTC()
	handoff, handoffRef, requestRef, err := a.loadAnalysisHandoffFromMessages(state)
	report.HandoffParseLatencyMS = time.Since(loadStarted).Milliseconds()
	if err != nil {
		report.Mode = "message_protocol_error"
		report.StopReason = "analysis handoff message unavailable"
		report.DegradedFallbackReason = err.Error()
		report.CompletedAt = time.Now().UTC()
		return report
	}
	report.SourceAnalysisMessage = cloneAgentMessageRef(handoffRef)
	report.SourceValidationRequest = cloneAgentMessageRef(requestRef)
	return a.runDecoded(ctx, state, handoff, report)
}

func (a *ValidationActionAgent) RunDecoded(ctx context.Context, state *workflowState, handoff AnalysisHandoff) ValidationActionReport {
	report := ValidationActionReport{
		SchemaVersion: ValidationActionReportSchemaVersion,
		Agent:         "validation_action_agent",
		IncidentID:    firstNonEmpty(state.rca.IncidentID, state.workflowID),
		CorrelationID: firstNonEmpty(state.workflowID, state.rca.TraceID),
		Mode:          "bounded_react",
		StartedAt:     time.Now().UTC(),
		TargetLimit:   a.cfg.ValidationTargetLimit,
		ReadOnlyOnly:  a.cfg.ValidationReadOnlyOnly,
	}
	return a.runDecoded(ctx, state, handoff, report)
}

func (a *ValidationActionAgent) runDecoded(ctx context.Context, state *workflowState, handoff AnalysisHandoff, report ValidationActionReport) ValidationActionReport {
	report.ActionCandidates = append([]ValidationActionCandidate(nil), handoff.BoundedActionCandidates...)
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
		if report.Iterations >= a.cfg.ValidationMaxIterations || report.ToolCalls >= a.cfg.ValidationMaxToolCalls {
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

func (a *ValidationActionAgent) loadAnalysisHandoffFromMessages(state *workflowState) (AnalysisHandoff, *AgentMessageRef, *AgentMessageRef, error) {
	if state == nil || state.engine == nil || state.engine.messageStore == nil {
		return AnalysisHandoff{}, nil, nil, fmt.Errorf("agent message store unavailable")
	}
	var requestRef *AgentMessageRef
	var handoffRef *AgentMessageRef
	if ref, err := state.engine.messageStore.Latest(state.workflowID, AgentMessageTypeValidationRequest); err == nil && ref != nil {
		requestRef = ref
		envelope, err := state.engine.messageStore.LoadEnvelope(*ref)
		if err != nil {
			return AnalysisHandoff{}, nil, nil, err
		}
		var request ValidationRequestMessage
		if err := decodeAgentMessagePayload(envelope, AgentMessageTypeValidationRequest, a.cfg.AgentMessageSchemaVersion, &request); err != nil {
			return AnalysisHandoff{}, nil, nil, err
		}
		handoffRef = cloneAgentMessageRef(&request.AnalysisMessage)
	} else if err != nil {
		return AnalysisHandoff{}, nil, nil, err
	}
	if handoffRef == nil {
		ref, err := state.engine.messageStore.Latest(state.workflowID, AgentMessageTypeAnalysisHandoff)
		if err != nil {
			return AnalysisHandoff{}, nil, nil, err
		}
		if ref == nil {
			return AnalysisHandoff{}, nil, requestRef, fmt.Errorf("analysis handoff message not found")
		}
		handoffRef = ref
	}
	envelope, err := state.engine.messageStore.LoadEnvelope(*handoffRef)
	if err != nil {
		return AnalysisHandoff{}, nil, requestRef, err
	}
	var message AnalysisHandoffMessage
	if err := decodeAgentMessagePayload(envelope, AgentMessageTypeAnalysisHandoff, a.cfg.AgentMessageSchemaVersion, &message); err != nil {
		return AnalysisHandoff{}, nil, requestRef, err
	}
	return message.Handoff, handoffRef, requestRef, nil
}

func (a *ValidationActionAgent) validateTarget(ctx context.Context, state *workflowState, handoff AnalysisHandoff, target ValidationTarget, report *ValidationActionReport) ValidationTargetResult {
	result := ValidationTargetResult{
		TargetID:                 target.ID,
		TargetType:               target.Type,
		Title:                    target.Title,
		HypothesisID:             target.HypothesisID,
		RecommendationID:         target.RecommendationID,
		ActionCandidateID:        target.ActionCandidateID,
		Verdict:                  ValidationVerdictInsufficientEvidence,
		Confidence:               0.35,
		EvidenceGaps:             append([]string(nil), target.EvidenceGaps...),
		SupportingEvidenceIDs:    append([]string(nil), target.SupportingEvidenceIDs...),
		ContradictingEvidenceIDs: append([]string(nil), target.ContradictingEvidenceIDs...),
	}
	target = normalizeValidationTargetPlan(state, handoff, target)
	selectionContext := buildToolSelectionContext(state, "validation_action_react_loop", &target)
	candidates := generateToolCandidates(state, selectionContext)
	if len(candidates) == 0 {
		for _, tool := range target.SuggestedTools {
			candidates = append(candidates, ToolCandidate{
				Tool:   tool,
				Query:  validationQueryForTarget(state, handoff, target, tool),
				Reason: validationToolReason(target, tool),
			})
		}
	}
	selectionDecision := buildToolSelectionDecision(state, selectionContext, candidates)
	if selectionDecision.Selected == nil && len(candidates) == 0 {
		result.StopReason = firstNonEmpty(selectionDecision.StopReason, "no validation tool candidates")
		return result
	}
	candidates = selectionDecision.Alternatives
	if len(candidates) == 0 && selectionDecision.Selected != nil {
		candidates = append(candidates, *selectionDecision.Selected)
	}
	used := make(map[ToolName]struct{}, len(candidates))
	for _, candidate := range candidates {
		if report.Iterations >= a.cfg.ValidationMaxIterations || report.ToolCalls >= a.cfg.ValidationMaxToolCalls {
			result.StopReason = "validation budget reached"
			break
		}
		tool := candidate.Tool
		if _, ok := used[tool]; ok {
			continue
		}
		used[tool] = struct{}{}
		query := candidate.Query
		if len(query) == 0 {
			query = validationQueryForTarget(state, handoff, target, tool)
		}
		toolReason := firstNonEmpty(candidate.Reason, validationToolReason(target, tool), selectionDecision.Reason)
		toolResult, err := state.callToolAs(ctx, "validation_action_react_loop", "validation_action_agent", tool, query, target.ID)
		report.Iterations++
		report.ToolCalls++
		record := ValidationLoopRecord{
			Iteration:   report.Iterations,
			TargetID:    target.ID,
			Tool:        tool,
			ToolReason:  toolReason,
			PlannerNote: plannerNoteForTarget(target),
			Timestamp:   time.Now().UTC(),
			Verdict:     result.Verdict,
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
		delta, verdict, observation, supporting, contradicting := assessValidationObservation(handoff, target, tool, toolResult)
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
		if tool == ToolMetrics && verdict == ValidationVerdictInsufficientEvidence && strings.Contains(strings.ToLower(observation), "insufficient metric history") {
			result.StopReason = "insufficient metric history"
			break
		}

		if validationShouldStop(result, a.cfg.ValidationConfidenceThreshold) {
			result.StopReason = stopReasonForVerdict(result.Verdict)
			break
		}
	}
	if result.Summary == "" {
		result.Summary = "validation loop finished without decisive evidence"
	}
	if result.StopReason == "" && report != nil && (report.Iterations >= a.cfg.ValidationMaxIterations || report.ToolCalls >= a.cfg.ValidationMaxToolCalls) {
		result.StopReason = "validation budget reached"
	}
	if result.StopReason == "" {
		result.StopReason = "target tool sequence exhausted"
	}
	return result
}

func validationQueryForTarget(state *workflowState, handoff AnalysisHandoff, target ValidationTarget, tool ToolName) map[string]string {
	if state != nil && state.engine != nil && state.engine.tools != nil && state.engine.tools.contracts != nil {
		if contract, ok := state.engine.tools.contracts.Get(tool); ok {
			shaped := shapeToolQuery(state, contract, buildToolSelectionContext(state, "validation_action_react_loop", &target))
			query := cloneStringMap(shaped.Query)
			query["incident_summary"] = firstNonEmpty(query["incident_summary"], handoff.IncidentSummary)
			query["scope"] = firstNonEmpty(query["scope"], state.collectorID, handoff.CollectorID, "fleet")
			query["target_type"] = string(target.Type)
			query["focus"] = firstNonEmpty(query["focus"], target.Focus, deriveValidationFocus(target.ToolFamilies, target.Title, target.Summary))
			query["impacted_scope"] = strings.Join(target.ImpactedScope, ",")
			query["change_categories"] = strings.Join(target.ChangeCategories, ",")
			query["validation_category"] = firstNonEmpty(target.ExecutionCategory, "read_only_validation")
			query["read_only"] = fmt.Sprintf("%t", target.ReadOnly)
			return query
		}
	}
	query := map[string]string{
		"query":               firstNonEmpty(target.Title, target.Summary, handoff.IncidentSummary),
		"incident_summary":    handoff.IncidentSummary,
		"scope":               firstNonEmpty(state.collectorID, handoff.CollectorID, "fleet"),
		"target_type":         string(target.Type),
		"focus":               firstNonEmpty(target.Focus, deriveValidationFocus(target.ToolFamilies, target.Title, target.Summary)),
		"impacted_scope":      strings.Join(target.ImpactedScope, ","),
		"evidence_gaps":       strings.Join(target.EvidenceGaps, ","),
		"change_categories":   strings.Join(target.ChangeCategories, ","),
		"validation_category": firstNonEmpty(target.ExecutionCategory, "read_only_validation"),
		"read_only":           fmt.Sprintf("%t", target.ReadOnly),
	}
	if handoff.SceneFamily != "" {
		query["scene_family"] = string(handoff.SceneFamily)
		query["scene_confidence"] = fmt.Sprintf("%.2f", handoff.SceneConfidence)
		query["candidate_subscenes"] = strings.Join(handoff.CandidateSubscenes, ",")
		query["remaining_budget"] = fmt.Sprintf("bytes=%d events=%d rounds=%d", handoff.RemainingBudget.RemainingBytes, handoff.RemainingBudget.RemainingEvents, handoff.RemainingBudget.RemainingRounds)
	}
	if target.HypothesisID != "" {
		query["hypothesis_id"] = target.HypothesisID
	}
	if target.RecommendationID != "" {
		query["recommendation_id"] = target.RecommendationID
	}
	if target.ActionCandidateID != "" {
		query["action_candidate_id"] = target.ActionCandidateID
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
	if target.Type == ValidationTargetSceneClassification && handoff.CollectionPlanSummary.PlanID != "" {
		query["query"] = strings.Join(append(append([]string(nil), handoff.CollectionPlanSummary.TargetCollectorsOrModules...), handoff.MissingEvidence...), " ")
		query["event_filters"] = strings.Join(handoff.MissingEvidence, ",")
	}
	return query
}

func validationToolReason(target ValidationTarget, tool ToolName) string {
	if len(target.EvidenceGaps) > 0 {
		return fmt.Sprintf("close evidence gap %q using %s", target.EvidenceGaps[0], tool)
	}
	if len(target.ToolFamilies) > 0 {
		return fmt.Sprintf("target requires %s evidence", target.ToolFamilies[0])
	}
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

func assessValidationObservation(handoff AnalysisHandoff, target ValidationTarget, tool ToolName, result workflowToolResult) (float64, ValidationVerdict, string, []string, []string) {
	supporting := []string{fmt.Sprintf("ev-%s", sanitizeID(target.ID))}
	contradicting := []string{}
	if target.Type == ValidationTargetSceneClassification {
		return assessSceneValidationObservation(handoff, target, tool, result, supporting)
	}
	switch tool {
	case ToolChangeQuery, ToolDeploymentHistory:
		data, ok := result.Data.(changeToolData)
		if !ok {
			return -0.08, ValidationVerdictInsufficientEvidence, "change evidence payload unavailable", nil, nil
		}
		if len(data.Events) == 0 {
			if target.Type == ValidationTargetChangeCorrelation || slicesContainsString(target.ToolFamilies, "change") {
				return -0.18, ValidationVerdictContradicted, "no correlated deployment or config changes matched the incident window", nil, supporting
			}
			return -0.04, ValidationVerdictInsufficientEvidence, "change query returned no strong matches", nil, nil
		}
		matched := 0
		for _, event := range data.Events {
			supporting = append(supporting, event.Event.ChangeID)
			if len(target.ChangeCategories) == 0 || slicesContainsString(target.ChangeCategories, strings.TrimSpace(event.Event.Category)) {
				matched++
			}
		}
		if matched == 0 && len(target.ChangeCategories) > 0 {
			return -0.12, ValidationVerdictContradicted, "change activity exists, but not for the expected category or scope", nil, dedupeStrings(supporting)
		}
		verdict := ValidationVerdictPartiallySupported
		delta := 0.16
		if matched > 0 && (data.Strongest == nil || data.Strongest.ChangeScore >= 0.65) {
			verdict = ValidationVerdictConfirmed
			delta = 0.24
		}
		return delta, verdict, truncateString(firstNonEmpty(data.Summary, fmt.Sprintf("correlated changes=%d", len(data.Events))), 220), dedupeStrings(supporting), nil
	case ToolConfigState:
		data, ok := result.Data.(configStateToolData)
		if !ok {
			return -0.05, ValidationVerdictInsufficientEvidence, "config state payload unavailable", nil, nil
		}
		if len(data.Changes) == 0 && len(data.Labels) == 0 {
			return -0.16, ValidationVerdictContradicted, "no config or revision state supports the change hypothesis", nil, supporting
		}
		if data.RuntimeMode == "" && len(data.Changes) == 0 {
			return -0.08, ValidationVerdictInsufficientEvidence, "config state is present but does not explain the current scope", nil, nil
		}
		return 0.16, ValidationVerdictPartiallySupported, data.Summary, dedupeStrings(supporting), nil
	case ToolMetrics, ToolServiceHealth, ToolMemoryPressure, ToolStorageHealth, ToolConnectivityCheck, ToolDNSCheck, ToolNetworkBlastRadius, ToolGPU:
		return assessOperationalObservation(target, tool, result, supporting)
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
		if target.Type == ValidationTargetRecommendation && data.Errors > 0 {
			return -0.06, ValidationVerdictContradicted, fmt.Sprintf("current scope still shows logs errors=%d warnings=%d", data.Errors, data.Warnings), nil, dedupeStrings(supporting)
		}
		return 0.14, ValidationVerdictPartiallySupported, fmt.Sprintf("logs errors=%d warnings=%d", data.Errors, data.Warnings), dedupeStrings(supporting), nil
	case ToolRunbookRetrieval, ToolHistoricalIncident, ToolSimilarCase, ToolActionOutcome:
		if data, ok := result.Data.(knowledgeToolData); ok {
			if len(data.Hits) == 0 {
				return -0.08, ValidationVerdictInsufficientEvidence, "knowledge retrieval found no validated analogues", nil, nil
			}
			supporting = append(supporting, data.EvidenceIDs...)
			matched := 0
			for _, hit := range data.Hits {
				if knowledgeHitMatchesTarget(hit, handoff, target) {
					matched++
				}
			}
			if matched == 0 {
				return -0.06, ValidationVerdictInsufficientEvidence, "knowledge exists but does not match the validated hypothesis or current scope", nil, nil
			}
			if baselineEvidenceTarget(target) {
				return 0.04, ValidationVerdictInsufficientEvidence, "prior cases exist, but they do not replace the missing baseline evidence", dedupeStrings(supporting), nil
			}
			verdict := ValidationVerdictPartiallySupported
			delta := 0.18
			if target.Type == ValidationTargetRecommendation && (tool == ToolRunbookRetrieval || tool == ToolActionOutcome) {
				verdict = ValidationVerdictConfirmed
				delta = 0.24
			}
			return delta, verdict, truncateString(firstNonEmpty(data.Summary, fmt.Sprintf("knowledge hits=%d matched=%d", len(data.Hits), matched)), 220), dedupeStrings(supporting), nil
		}
	case ToolSecurity, ToolSecurityGraph, ToolProcessLineage, ToolEBPFQuery, ToolKubernetesResource, ToolContainerRevision:
		if strings.TrimSpace(result.Summary) != "" {
			if target.Type == ValidationTargetContradiction && looksHealthyValidationSummary(result.Summary) {
				return -0.12, ValidationVerdictContradicted, truncateString(result.Summary, 220), nil, dedupeStrings(supporting)
			}
			return 0.14, ValidationVerdictPartiallySupported, truncateString(result.Summary, 220), dedupeStrings(supporting), nil
		}
	}
	return 0.0, ValidationVerdictInsufficientEvidence, truncateString(firstNonEmpty(result.Summary, "validation check completed"), 220), dedupeStrings(supporting), nil
}

func assessSceneValidationObservation(handoff AnalysisHandoff, target ValidationTarget, tool ToolName, result workflowToolResult, supporting []string) (float64, ValidationVerdict, string, []string, []string) {
	summary := strings.ToLower(strings.TrimSpace(result.Summary))
	scene := strings.ToLower(strings.TrimSpace(string(target.SceneFamily)))
	switch {
	case scene == "":
		return -0.04, ValidationVerdictInsufficientEvidence, "scene family was not set on the validation target", nil, nil
	case strings.Contains(summary, sceneKeyword(scene)) || slicesContainsString(target.ToolFamilies, sceneFamilyPrimaryFamily(target.SceneFamily)):
		return 0.18, ValidationVerdictPartiallySupported, truncateString(firstNonEmpty(result.Summary, "scene-oriented evidence collected"), 220), dedupeStrings(supporting), nil
	case looksHealthyValidationSummary(summary):
		return -0.12, ValidationVerdictContradicted, truncateString(firstNonEmpty(result.Summary, "scene evidence contradicted by the current checks"), 220), nil, dedupeStrings(supporting)
	default:
		return 0.06, ValidationVerdictInsufficientEvidence, truncateString(firstNonEmpty(result.Summary, "scene evidence did not materially change confidence"), 220), dedupeStrings(supporting), nil
	}
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
			out = append(out, result.EvidenceGaps...)
		}
	}
	if report.DegradedFallbackReason != "" {
		out = append(out, report.DegradedFallbackReason)
	}
	return dedupeStrings(out)
}

func sceneKeyword(scene string) string {
	switch scene {
	case string(SceneFamilyChangeInduced), string(SceneFamilyDeploymentRollout):
		return "deploy"
	case string(SceneFamilyNetworkConnectivity):
		return "network"
	case string(SceneFamilyStorageIO):
		return "storage"
	case string(SceneFamilyGPUInference), string(SceneFamilyGPUTrainingOrCollective):
		return "gpu"
	case string(SceneFamilyKubernetesWorkload):
		return "pod"
	case string(SceneFamilyBareMetalKernelOrIRQ):
		return "kernel"
	case string(SceneFamilyDatabaseLikeLatencyPath):
		return "latency"
	case string(SceneFamilySecurityOrProcessAnomaly):
		return "security"
	default:
		return "pressure"
	}
}

func sceneFamilyPrimaryFamily(scene SceneFamily) string {
	switch scene {
	case SceneFamilyChangeInduced, SceneFamilyDeploymentRollout:
		return "change"
	case SceneFamilyNetworkConnectivity:
		return "network"
	case SceneFamilyStorageIO:
		return "storage"
	case SceneFamilyGPUInference, SceneFamilyGPUTrainingOrCollective:
		return "gpu"
	case SceneFamilyKubernetesWorkload:
		return "kubernetes"
	case SceneFamilyBareMetalKernelOrIRQ:
		return "runtime"
	case SceneFamilyDatabaseLikeLatencyPath:
		return "service_health"
	case SceneFamilySecurityOrProcessAnomaly:
		return "security"
	default:
		return "metrics"
	}
}

func normalizeValidationTargetPlan(state *workflowState, handoff AnalysisHandoff, target ValidationTarget) ValidationTarget {
	if len(target.ToolFamilies) == 0 {
		target.ToolFamilies = deriveToolFamilies(state, target.Type, target.Title, target.Summary, target.SupportingEvidenceIDs, target.ChangeCategories, nil)
	}
	if len(target.EvidenceGaps) == 0 {
		target.EvidenceGaps = deriveEvidenceGaps(state, target.Type, target.SupportingEvidenceIDs, target.ContradictingEvidenceIDs, target.ChangeCategories, target.ToolFamilies)
	}
	if len(target.ImpactedScope) == 0 {
		target.ImpactedScope = append([]string(nil), handoff.ImpactedScope...)
	}
	if len(target.SuggestedTools) == 0 {
		target.SuggestedTools = suggestedToolsForTargetFamilies(target.Type, target.ToolFamilies)
	}
	target.SuggestedTools = prioritizeValidationTools(target.SuggestedTools, target.EvidenceGaps)
	if target.Focus == "" {
		target.Focus = deriveValidationFocus(target.ToolFamilies, target.Title, target.Summary)
	}
	if len(target.ExpectedSignals) == 0 {
		target.ExpectedSignals = expectedSignalsForFamilies(target.ToolFamilies)
	}
	if target.ExecutionCategory == "" {
		target.ExecutionCategory = executionCategoryForTarget(target.Type, target.ActionCandidateID, target.RecommendationID, state)
	}
	if target.SceneFamily == "" {
		target.SceneFamily = handoff.SceneFamily
	}
	return target
}

func plannerNoteForTarget(target ValidationTarget) string {
	if len(target.ToolFamilies) == 0 && len(target.EvidenceGaps) == 0 {
		return ""
	}
	return fmt.Sprintf("families=%s gaps=%s", strings.Join(target.ToolFamilies, ","), strings.Join(target.EvidenceGaps, ","))
}

func assessOperationalObservation(target ValidationTarget, tool ToolName, result workflowToolResult, supporting []string) (float64, ValidationVerdict, string, []string, []string) {
	if strings.TrimSpace(result.Summary) == "" {
		return -0.04, ValidationVerdictInsufficientEvidence, "observability query returned no summary", nil, nil
	}
	switch tool {
	case ToolMetrics:
		if data, ok := result.Data.(metricsToolData); ok {
			if len(data.History) < 3 {
				return -0.16, ValidationVerdictInsufficientEvidence, "insufficient metric history to validate the current target", nil, nil
			}
			if data.Node == nil || len(data.Node.Metrics) == 0 {
				return -0.10, ValidationVerdictInsufficientEvidence, "current metric snapshot is unavailable", nil, nil
			}
			if baselineEvidenceTarget(target) {
				return 0.08, ValidationVerdictPartiallySupported, truncateString(result.Summary, 220), dedupeStrings(supporting), nil
			}
			return 0.14, ValidationVerdictPartiallySupported, truncateString(result.Summary, 220), dedupeStrings(supporting), nil
		}
	case ToolServiceHealth:
		if data, ok := result.Data.(serviceHealthToolData); ok {
			if baselineEvidenceTarget(target) && data.Healthy {
				return -0.06, ValidationVerdictInsufficientEvidence, "service health is currently green, but the baseline evidence is still too thin", nil, nil
			}
			if target.Type == ValidationTargetContradiction && data.Healthy {
				return -0.16, ValidationVerdictContradicted, truncateString(data.Summary, 220), nil, dedupeStrings(supporting)
			}
			if !data.Healthy && (data.ErrorRate >= 0.05 || data.RestartLike || data.LatencyMS >= 1000) {
				return 0.20, ValidationVerdictConfirmed, truncateString(data.Summary, 220), dedupeStrings(supporting), nil
			}
			return 0.12, ValidationVerdictPartiallySupported, truncateString(data.Summary, 220), dedupeStrings(supporting), nil
		}
	case ToolMemoryPressure:
		if data, ok := result.Data.(memoryPressureToolData); ok {
			if len(data.OOMHints) > 0 {
				return 0.24, ValidationVerdictConfirmed, truncateString(data.Summary, 220), dedupeStrings(supporting), nil
			}
			if target.Type == ValidationTargetContradiction && len(data.PressureSignals) == 0 && data.WorkingSetPct < 80 {
				return -0.14, ValidationVerdictContradicted, "memory pressure is not visible in the current window", nil, dedupeStrings(supporting)
			}
			if len(data.PressureSignals) > 0 || data.WorkingSetPct >= 85 {
				return 0.14, ValidationVerdictPartiallySupported, truncateString(data.Summary, 220), dedupeStrings(supporting), nil
			}
		}
	case ToolConnectivityCheck:
		if data, ok := result.Data.(connectivityCheckToolData); ok {
			if target.Type == ValidationTargetContradiction && data.Healthy {
				return -0.14, ValidationVerdictContradicted, truncateString(data.Summary, 220), nil, dedupeStrings(supporting)
			}
			if !data.Healthy {
				return 0.18, ValidationVerdictPartiallySupported, truncateString(data.Summary, 220), dedupeStrings(supporting), nil
			}
		}
	case ToolDNSCheck:
		if data, ok := result.Data.(dnsCheckToolData); ok {
			if target.Type == ValidationTargetContradiction && data.Healthy {
				return -0.12, ValidationVerdictContradicted, truncateString(data.Summary, 220), nil, dedupeStrings(supporting)
			}
			if !data.Healthy && len(data.Hints) > 0 {
				return 0.16, ValidationVerdictPartiallySupported, truncateString(data.Summary, 220), dedupeStrings(supporting), nil
			}
		}
	case ToolStorageHealth:
		if data, ok := result.Data.(storageHealthToolData); ok {
			if target.Type == ValidationTargetContradiction && !data.Pressure {
				return -0.12, ValidationVerdictContradicted, "storage pressure is absent in the current window", nil, dedupeStrings(supporting)
			}
			if data.Pressure {
				return 0.16, ValidationVerdictPartiallySupported, truncateString(data.Summary, 220), dedupeStrings(supporting), nil
			}
		}
	case ToolGPU:
		if data, ok := result.Data.(gpuToolData); ok {
			if target.Type == ValidationTargetContradiction && data.Bottleneck == "" && len(data.TopProcesses) == 0 {
				return -0.10, ValidationVerdictContradicted, "gpu telemetry does not support the current hypothesis", nil, dedupeStrings(supporting)
			}
			if data.Bottleneck != "" || len(data.TopProcesses) > 0 {
				return 0.18, ValidationVerdictPartiallySupported, truncateString(data.Summary, 220), dedupeStrings(supporting), nil
			}
		}
	}
	if target.Type == ValidationTargetContradiction && looksHealthyValidationSummary(result.Summary) {
		return -0.16, ValidationVerdictContradicted, truncateString(result.Summary, 220), nil, dedupeStrings(supporting)
	}
	if baselineEvidenceTarget(target) {
		return -0.06, ValidationVerdictInsufficientEvidence, truncateString(result.Summary, 220), nil, nil
	}
	return 0.12, ValidationVerdictPartiallySupported, truncateString(result.Summary, 220), dedupeStrings(supporting), nil
}

func baselineEvidenceTarget(target ValidationTarget) bool {
	if strings.EqualFold(strings.TrimSpace(target.ID), "validate-evidence-gap") {
		return true
	}
	return strings.Contains(strings.ToLower(target.Title), "baseline evidence")
}

func knowledgeHitMatchesTarget(hit RetrievedDocumentEvidence, handoff AnalysisHandoff, target ValidationTarget) bool {
	texts := []string{
		target.Title,
		target.Summary,
		handoff.IncidentSummary,
		strings.Join(target.ImpactedScope, " "),
	}
	for _, hypothesis := range handoff.HypothesisPackets {
		if hypothesis.HypothesisID != "" && hypothesis.HypothesisID == target.HypothesisID {
			texts = append(texts, hypothesis.Title, hypothesis.Summary)
			break
		}
	}
	joined := strings.ToLower(strings.Join(texts, " "))
	for _, candidate := range append([]string{hit.Title, hit.Summary, hit.Snippet}, hit.LikelyCauses...) {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if strings.Contains(joined, candidate) || strings.Contains(candidate, strings.ToLower(strings.TrimSpace(target.Title))) {
			return true
		}
	}
	for _, step := range hit.RemediationSteps {
		step = strings.ToLower(strings.TrimSpace(step))
		if step != "" && strings.Contains(step, strings.ToLower(strings.TrimSpace(target.Title))) {
			return true
		}
	}

	corpus := make([]string, 0, 4+len(hit.LikelyCauses)+len(hit.RemediationSteps))
	corpus = append(corpus, hit.Title, hit.Summary, hit.Snippet)
	corpus = append(corpus, hit.LikelyCauses...)
	corpus = append(corpus, hit.RemediationSteps...)
	keywords := validationMatchKeywords(texts...)
	if len(keywords) == 0 {
		return false
	}
	candidateText := strings.ToLower(strings.Join(corpus, " "))
	matches := 0
	for _, keyword := range keywords {
		if strings.Contains(candidateText, keyword) {
			matches++
		}
	}
	return matches > 0
}

func validationMatchKeywords(texts ...string) []string {
	stop := map[string]struct{}{
		"validate": {}, "validation": {}, "recommendation": {}, "recommendations": {},
		"hypothesis": {}, "incident": {}, "current": {}, "scope": {}, "service": {},
		"system": {}, "check": {}, "checks": {}, "evidence": {}, "target": {},
		"suggested": {}, "action": {}, "actions": {}, "change": {}, "changes": {},
		"issue": {}, "issues": {}, "problem": {}, "problems": {}, "candidate": {},
	}
	seen := make(map[string]struct{})
	keywords := make([]string, 0, 8)
	for _, text := range texts {
		for _, token := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		}) {
			if len(token) < 4 {
				continue
			}
			if _, blocked := stop[token]; blocked {
				continue
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			keywords = append(keywords, token)
		}
	}
	return keywords
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
