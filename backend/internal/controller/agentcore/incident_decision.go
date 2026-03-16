package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func SynthesizeIncident(state *workflowState) IncidentSynthesis {
	if state == nil {
		return IncidentSynthesis{}
	}
	signals := make([]IncidentGroupedSignal, 0, 16)
	for _, signal := range topTriggeredSignals(state.riskSignals, 8) {
		signals = append(signals, IncidentGroupedSignal{
			SignalID:     signal.ID,
			SignalType:   signal.Name,
			Source:       "metrics",
			Scope:        signal.Scope,
			Entity:       signal.Entity,
			Severity:     signal.Severity,
			Score:        signal.Score,
			Summary:      fmt.Sprintf("%s deviated %.1f%% from baseline on %s", signal.Name, signal.DeltaPercent, signal.Entity),
			EvidenceIDs:  []string{fmt.Sprintf("ev-%s", sanitizeID(signal.ID))},
			LastObserved: signal.LastObservedAt,
		})
	}
	for _, event := range state.investigationEvents {
		signals = append(signals, IncidentGroupedSignal{
			SignalID:     event.ID,
			SignalType:   event.Title,
			Source:       "eventization",
			Scope:        firstNonEmpty(event.Scope, "node"),
			Entity:       firstNonEmpty(event.Entity, state.collectorID, "fleet"),
			Severity:     firstNonEmpty(event.Severity, "medium"),
			Score:        clamp01(event.Confidence),
			Summary:      firstNonEmpty(event.Summary, event.Symptom),
			EvidenceIDs:  []string{event.ID},
			LastObserved: state.now,
		})
		if len(signals) >= 10 {
			break
		}
	}
	for _, finding := range state.security.StructuredFindings {
		signals = append(signals, IncidentGroupedSignal{
			SignalID:     firstNonEmpty(finding.FindingID, finding.EvidenceID, sanitizeID(finding.Category+"-"+finding.Summary)),
			SignalType:   firstNonEmpty(finding.Category, "security"),
			Source:       firstNonEmpty(finding.Source, "security_audit"),
			Scope:        firstNonEmpty(finding.Scope, "node"),
			Entity:       firstNonEmpty(state.collectorID, "fleet"),
			Severity:     firstNonEmpty(finding.Severity, "medium"),
			Score:        clamp01(finding.Confidence),
			Summary:      firstNonEmpty(finding.Summary, finding.Description),
			EvidenceIDs:  dedupeStrings([]string{finding.EvidenceID, finding.FindingID}),
			LastObserved: state.now,
		})
		if len(signals) >= 12 {
			break
		}
	}
	for _, event := range state.ebpf.RuntimeEvents {
		signals = append(signals, IncidentGroupedSignal{
			SignalID:     firstNonEmpty(event.EvidenceID, sanitizeID(event.Category+"-"+event.Type+"-"+event.PID)),
			SignalType:   firstNonEmpty(event.Type, event.Category, "runtime_event"),
			Source:       "trace_query",
			Scope:        firstNonEmpty(event.NodeScope, "node"),
			Entity:       firstNonEmpty(event.PID, state.collectorID, "fleet"),
			Severity:     firstNonEmpty(event.Severity, "medium"),
			Score:        clamp01(event.Confidence),
			Summary:      firstNonEmpty(event.Description, fmt.Sprintf("%s %s", event.Category, event.Type)),
			EvidenceIDs:  dedupeStrings([]string{event.EvidenceID}),
			LastObserved: event.Timestamp,
		})
		if len(signals) >= 16 {
			break
		}
	}

	impactedScope := make([]string, 0, 12)
	for _, row := range state.scopeRisks {
		if strings.TrimSpace(row.Entity) == "" {
			continue
		}
		impactedScope = append(impactedScope, fmt.Sprintf("%s/%s", row.Scope, row.Entity))
	}
	for _, signal := range signals {
		if strings.TrimSpace(signal.Entity) == "" {
			continue
		}
		impactedScope = append(impactedScope, fmt.Sprintf("%s/%s", signal.Scope, signal.Entity))
	}
	impactedScope = dedupeStrings(impactedScope)
	if len(impactedScope) > 10 {
		impactedScope = impactedScope[:10]
	}

	correlationReasons := make([]string, 0, 8)
	for _, co := range state.cooccurrences {
		correlationReasons = append(correlationReasons, truncateString(co.Explanation, 180))
		if len(correlationReasons) >= 5 {
			break
		}
	}
	if len(state.logsData.RecentDeploys) > 0 {
		correlationReasons = append(correlationReasons, "recent rollout overlaps with the incident window")
	}
	if len(state.security.StructuredFindings) > 0 && len(state.ebpf.RuntimeEvents) > 0 {
		correlationReasons = append(correlationReasons, "runtime behavior and security findings overlap in the same window")
	}
	correlationReasons = dedupeStrings(correlationReasons)

	topOffenders := make([]string, 0, 8)
	if state.metricsData.Node != nil {
		for _, item := range topProcessResources(state.metricsData.Node, 4) {
			topOffenders = append(topOffenders, processDisplayName(item))
		}
	}
	for _, edge := range state.lineage.Paths {
		if len(topOffenders) >= 8 {
			break
		}
		topOffenders = append(topOffenders, truncateString(edge, 140))
	}
	topOffenders = dedupeStrings(topOffenders)

	timelineTransitions := summarizeTimelineTransitions(state)
	neighborhood := summarizeTopologyNeighborhood(state)
	severity := incidentSeverity(signals, state.risk.RiskLevel)
	confidence := incidentConfidence(state, signals)
	candidate := detectIncidentRootCauseCluster(state, signals)
	summary := incidentSummary(state, signals, severity, candidate)
	incidentID := fmt.Sprintf("inc-%s", sanitizeID(state.workflowID))
	if strings.TrimSpace(state.rca.IncidentID) != "" {
		incidentID = state.rca.IncidentID
	}

	return IncidentSynthesis{
		IncidentID:                incidentID,
		Summary:                   summary,
		GroupedSignals:            signals,
		ImpactedScope:             impactedScope,
		TimeWindow:                TimeWindow{Start: state.now.Add(-state.window), End: state.now},
		Severity:                  severity,
		Confidence:                confidence,
		CandidateRootCauseCluster: candidate,
		CorrelationReasons:        correlationReasons,
		TopOffenders:              topOffenders,
		TimelineTransitions:       timelineTransitions,
		TopologyNeighborhood:      neighborhood,
	}
}

func summarizeTimelineTransitions(state *workflowState) []string {
	if state == nil {
		return nil
	}
	out := make([]string, 0, 6)
	for _, bucket := range state.logsData.Timeline {
		if bucket.Total == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("logs=%d around %s", bucket.Total, bucket.Start.UTC().Format(time.RFC3339)))
		if len(out) >= 3 {
			break
		}
	}
	for _, stage := range state.stages {
		if strings.TrimSpace(stage.Name) == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s=%s", stage.Name, stage.Status))
		if len(out) >= 6 {
			break
		}
	}
	return dedupeStrings(out)
}

func summarizeTopologyNeighborhood(state *workflowState) []string {
	if state == nil {
		return nil
	}
	out := make([]string, 0, 8)
	for _, node := range state.topoData.Snapshot.Nodes {
		out = append(out, fmt.Sprintf("%s/%s", node.Type, firstNonEmpty(node.Name, node.ID)))
		if len(out) >= 5 {
			break
		}
	}
	for _, edge := range state.topoData.Snapshot.Edges {
		out = append(out, fmt.Sprintf("%s -> %s (%s)", edge.Source, edge.Target, edge.Kind))
		if len(out) >= 8 {
			break
		}
	}
	return dedupeStrings(out)
}

func incidentSeverity(signals []IncidentGroupedSignal, riskLevel string) string {
	if strings.EqualFold(strings.TrimSpace(riskLevel), "critical") {
		return "critical"
	}
	maxRank := severityRank(strings.TrimSpace(riskLevel))
	for _, signal := range signals {
		if rank := severityRank(signal.Severity); rank > maxRank {
			maxRank = rank
		}
	}
	return severityFromRank(maxRank)
}

func incidentConfidence(state *workflowState, signals []IncidentGroupedSignal) float64 {
	if state == nil {
		return 0
	}
	confidence := clamp01(state.risk.RiskScore * 0.7)
	if len(signals) >= 3 {
		confidence = clamp01(confidence + 0.12)
	}
	if len(state.cooccurrences) > 0 {
		confidence = clamp01(confidence + 0.08)
	}
	if len(state.security.StructuredFindings) > 0 && len(state.ebpf.RuntimeEvents) > 0 {
		confidence = clamp01(confidence + 0.08)
	}
	if len(state.retrievedDocs) > 0 {
		confidence = clamp01(confidence + 0.05)
	}
	return confidence
}

func detectIncidentRootCauseCluster(state *workflowState, signals []IncidentGroupedSignal) string {
	if state == nil {
		return "insufficient evidence"
	}
	if len(state.hypotheses) > 0 && strings.TrimSpace(state.hypotheses[0].Title) != "" {
		return state.hypotheses[0].Title
	}
	type scoreRow struct {
		category string
		score    float64
	}
	buckets := map[string]float64{}
	for _, signal := range signals {
		low := strings.ToLower(strings.TrimSpace(signal.SignalType + " " + signal.Summary))
		switch {
		case strings.Contains(low, "gpu"), strings.Contains(low, "cuda"), strings.Contains(low, "pcie"):
			buckets["gpu contention or device pressure"] += signal.Score
		case strings.Contains(low, "security"), strings.Contains(low, "permission"), strings.Contains(low, "listener"), strings.Contains(low, "outbound"):
			buckets["security exposure or policy drift"] += signal.Score
		case strings.Contains(low, "network"), strings.Contains(low, "retransmit"), strings.Contains(low, "connect"), strings.Contains(low, "socket"):
			buckets["network congestion or connection churn"] += signal.Score
		case strings.Contains(low, "io"), strings.Contains(low, "disk"), strings.Contains(low, "storage"):
			buckets["storage or IO bottleneck"] += signal.Score
		case strings.Contains(low, "memory"), strings.Contains(low, "oom"), strings.Contains(low, "rss"):
			buckets["memory pressure and reclaim"] += signal.Score
		default:
			buckets["cpu scheduling contention or multi-resource saturation"] += signal.Score
		}
	}
	rows := make([]scoreRow, 0, len(buckets))
	for category, score := range buckets {
		rows = append(rows, scoreRow{category: category, score: score})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].score > rows[j].score })
	if len(rows) == 0 {
		return "insufficient evidence"
	}
	return rows[0].category
}

func incidentSummary(state *workflowState, signals []IncidentGroupedSignal, severity, cluster string) string {
	if state == nil {
		return "incident synthesis unavailable"
	}
	parts := []string{strings.ToUpper(firstNonEmpty(severity, "medium"))}
	if len(signals) > 0 {
		names := make([]string, 0, minInt(3, len(signals)))
		for _, signal := range signals {
			names = append(names, firstNonEmpty(signal.SignalType, signal.SignalID))
			if len(names) >= 3 {
				break
			}
		}
		parts = append(parts, fmt.Sprintf("incident grouped from %s", strings.Join(dedupeStrings(names), ", ")))
	}
	if strings.TrimSpace(cluster) != "" {
		parts = append(parts, fmt.Sprintf("candidate cluster: %s", cluster))
	}
	if len(state.cooccurrences) > 0 {
		parts = append(parts, "multi-signal co-occurrence confirmed")
	}
	return strings.Join(parts, "; ")
}

func EvaluateActionPolicy(rec WorkflowRecommendation, incidentConfidence float64) ActionPolicyDecision {
	policy := ActionPolicyDecision{
		Status:           "allowed",
		Reason:           "read-only or low-impact action",
		ExecutionLevel:   recommendationExecutionLevel(rec),
		RequiresApproval: rec.RequiresApproval,
		DryRunRequired:   rec.DryRunDefault || !rec.Safe,
		RollbackRequired: !rec.Safe,
	}
	missing := make([]string, 0, 2)
	if !rec.Safe && strings.TrimSpace(firstNonEmpty(rec.RollbackHint, rec.RollbackConsideration)) == "" {
		missing = append(missing, "rollback_plan")
	}
	if rec.Confidence > 0 && rec.Confidence < 0.40 {
		missing = append(missing, "confidence")
	}
	if incidentConfidence > 0 && incidentConfidence < 0.40 && !rec.Safe {
		missing = append(missing, "workflow_confidence")
	}
	if len(missing) > 0 {
		policy.MissingConditions = dedupeStrings(missing)
		policy.ExecutionLevel = "suggest_only"
		if containsString(policy.MissingConditions, "rollback_plan") {
			policy.Status = "missing_rollback"
			policy.Reason = "impactful action is missing a rollback plan"
		} else {
			policy.Status = "insufficient_confidence"
			policy.Reason = "impactful action is not justified by enough evidence"
		}
		return policy
	}
	if rec.RequiresApproval || !rec.Safe {
		policy.Status = "allowed_with_approval"
		policy.Reason = firstNonEmpty(rec.ApprovalReason, "impactful action requires human approval")
		policy.ExecutionLevel = "approval_required"
		return policy
	}
	return policy
}

func recommendationExecutionLevel(rec WorkflowRecommendation) string {
	switch {
	case rec.RequiresApproval || !rec.Safe:
		return "approval_required"
	case rec.DryRunDefault:
		return "dry_run"
	default:
		return "auto_execute"
	}
}

func severityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium", "warning":
		return 2
	default:
		return 1
	}
}

func severityFromRank(rank int) string {
	switch {
	case rank >= 4:
		return "critical"
	case rank == 3:
		return "high"
	case rank == 2:
		return "medium"
	default:
		return "low"
	}
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func recommendationFromFields(
	id, category, priority, summary, details, scope string,
	checks []string,
	safe, dryRunDefault, requiresApproval, reversible bool,
	rollbackHint, approvalReason, rationale, expectedImpact, riskLevel string,
	confidence float64,
	evidenceIDs []string,
) WorkflowRecommendation {
	rec := WorkflowRecommendation{
		ID:                    id,
		Category:              category,
		Priority:              priority,
		Summary:               summary,
		Details:               details,
		Scope:                 scope,
		Checks:                dedupeStrings(checks),
		Safe:                  safe,
		DryRunDefault:         dryRunDefault,
		RequiresApproval:      requiresApproval,
		ApprovalReason:        approvalReason,
		Reversible:            reversible,
		RollbackHint:          rollbackHint,
		Rationale:             rationale,
		ExpectedImpact:        expectedImpact,
		RiskLevel:             firstNonEmpty(riskLevel, priority),
		Confidence:            clamp01(confidence),
		EvidenceIDs:           dedupeStrings(evidenceIDs),
		RollbackConsideration: rollbackHint,
	}
	rec.ExecutionLevel = recommendationExecutionLevel(rec)
	rec.Preconditions = recommendationPreconditions(rec)
	rec.BlastRadius = recommendationBlastRadius(rec)
	rec.IdempotencyNote = recommendationIdempotency(rec)
	rec.Timeout = recommendationTimeout(rec)
	rec.OperatorJustification = recommendationJustification(rec)
	return rec
}

func recommendationPreconditions(rec WorkflowRecommendation) []string {
	conditions := make([]string, 0, 4)
	if rec.Scope != "" {
		conditions = append(conditions, "confirm incident scope still matches "+rec.Scope)
	}
	if rec.DryRunDefault {
		conditions = append(conditions, "run dry-run first and review outcome")
	}
	if rec.RequiresApproval {
		conditions = append(conditions, "capture explicit operator approval before execution")
	}
	if !rec.Safe {
		conditions = append(conditions, "verify rollback plan and current blast radius before execution")
	}
	return dedupeStrings(conditions)
}

func recommendationBlastRadius(rec WorkflowRecommendation) string {
	if strings.TrimSpace(rec.Scope) != "" {
		return "scoped to " + strings.TrimSpace(rec.Scope)
	}
	if rec.Safe {
		return "read-only diagnostic scope"
	}
	return "production service scope may change"
}

func recommendationIdempotency(rec WorkflowRecommendation) string {
	if rec.Safe {
		return "read-only repeatable check"
	}
	if rec.Reversible {
		return "repeat only after validating the previous action outcome and rollback state"
	}
	return "not guaranteed idempotent; repeat only with manual operator review"
}

func recommendationTimeout(rec WorkflowRecommendation) string {
	if rec.Safe {
		return "30s"
	}
	return "2m"
}

func recommendationJustification(rec WorkflowRecommendation) string {
	if strings.TrimSpace(rec.Rationale) != "" {
		return strings.TrimSpace(rec.Rationale)
	}
	if rec.Safe {
		return "low-risk diagnostic step to gather missing evidence"
	}
	return "impactful action is only justified after scoped evidence review"
}

func ensureInitialHypotheses(state *workflowState) {
	if state == nil || len(state.hypotheses) > 0 {
		return
	}
	hypotheses := generateDeterministicHypotheses(state)
	for _, hypothesis := range hypotheses {
		state.hypotheses = append(state.hypotheses, hypothesis)
		state.hypothesisUpdates = append(state.hypothesisUpdates, HypothesisUpdate{
			Timestamp:     time.Now().UTC(),
			HypothesisID:  hypothesis.ID,
			Action:        "created",
			OldConfidence: 0,
			NewConfidence: hypothesis.Confidence,
			Reason:        "seeded from grouped incident evidence",
		})
	}
	rerankHypotheses(state)
}

func generateDeterministicHypotheses(state *workflowState) []RCAHypothesis {
	if state == nil {
		return nil
	}
	hypotheses := make([]RCAHypothesis, 0, 8)
	appendHypothesis := func(id, title string, confidence float64, description string) {
		id = strings.TrimSpace(id)
		if id == "" || title == "" {
			return
		}
		hypotheses = append(hypotheses, RCAHypothesis{
			ID:          id,
			Title:       title,
			Confidence:  clamp01(confidence),
			Description: description,
		})
	}
	for _, event := range state.investigationEvents {
		if strings.TrimSpace(event.ProbableCause) == "" {
			continue
		}
		appendHypothesis(
			fmt.Sprintf("h-%s", sanitizeID(event.ID)),
			event.ProbableCause,
			clamp01(maxFloat(event.Confidence, 0.42)),
			firstNonEmpty(event.Summary, event.Symptom),
		)
	}
	for _, signal := range topTriggeredSignals(state.riskSignals, state.engine.cfg.MaxHypotheses) {
		title := hypothesisTitleFromSignal(signal.Name)
		confidence := clamp01(signal.Score * (1.0 / maxFloat(signal.Weight, 0.01)))
		appendHypothesis(
			fmt.Sprintf("h-%s", sanitizeID(signal.ID)),
			title,
			confidence,
			fmt.Sprintf("%s inferred from %s trend acceleration %.3f and baseline delta %.1f%%", title, signal.Name, signal.Acceleration, signal.DeltaPercent),
		)
	}
	if strings.Contains(strings.ToLower(state.incident.CandidateRootCauseCluster), "gpu") {
		appendHypothesis("h-gpu", state.incident.CandidateRootCauseCluster, clamp01(state.incident.Confidence), "incident synthesis grouped GPU pressure, process attribution, and throughput anomalies")
	}
	if len(state.logsData.RecentDeploys) > 0 {
		appendHypothesis("h-recent-deploy", "recent deployment/regression", 0.62, "recent rollout/deployment signals co-occur with incident window")
	}
	if len(state.security.Findings) > 0 {
		appendHypothesis("h-security", "security or permission misconfiguration", clamp01(0.45+state.security.Score*0.4), "security findings align with the incident window and impacted scope")
	}
	return dedupeHypotheses(hypotheses)
}

func updateHypothesesFromToolResult(state *workflowState, step AgentPlanStep, result workflowToolResult) {
	if state == nil {
		return
	}
	ensureInitialHypotheses(state)
	switch step.Tool {
	case ToolMetrics:
		for i := range state.hypotheses {
			bump := 0.0
			title := strings.ToLower(state.hypotheses[i].Title)
			switch {
			case strings.Contains(title, "cpu") && hasMetricLike(state, "cpu"):
				bump = 0.08
			case strings.Contains(title, "memory") && hasMetricLike(state, "memory"):
				bump = 0.08
			case strings.Contains(title, "storage"), strings.Contains(title, "io"):
				if hasMetricLike(state, "io") || hasMetricLike(state, "disk") {
					bump = 0.08
				}
			case strings.Contains(title, "network") && (hasMetricLike(state, "net") || hasMetricLike(state, "retrans")):
				bump = 0.08
			case strings.Contains(title, "gpu") && len(state.gpu.Metrics) > 0:
				bump = 0.10
			}
			if bump > 0 {
				recordHypothesisConfidence(state, i, bump, "supported by metrics evidence")
			}
		}
	case ToolLogs:
		data, _ := result.Data.(logsToolData)
		if len(data.RecentDeploys) > 0 {
			upsertHypothesis(state, "h-recent-deploy", "recent deployment/regression", 0.08, "log bursts overlap with a recent rollout")
		}
		if data.Errors+data.Warnings == 0 && expectsLogBurst(state) {
			decayHypothesisByKeyword(state, "deploy", 0.06, "log burst hypothesis weakened by missing logs")
		}
	case ToolSecurity:
		data, _ := result.Data.(securityToolData)
		if len(data.StructuredFindings) > 0 {
			upsertHypothesis(state, "h-security", "security or permission misconfiguration", 0.10, "collector-side security findings support a policy or exposure issue")
		} else {
			decayHypothesisByKeyword(state, "security", 0.08, "security findings did not confirm the suspicion")
		}
	case ToolEBPFQuery:
		data, _ := result.Data.(ebpfToolData)
		if len(data.RuntimeEvents) > 0 {
			if data.NetworkBehaviorSummary.UnexpectedOutbound > 0 || data.NetworkBehaviorSummary.AbnormalBindPorts > 0 {
				upsertHypothesis(state, "h-runtime-network", "network congestion or connection churn", 0.08, "kernel runtime events show connection churn or abnormal bind behavior")
			}
			if len(data.ProcessGraph.Edges) > 0 {
				upsertHypothesis(state, "h-runtime-process", "runtime process behavior anomaly", 0.06, "kernel runtime events and process lineage show abnormal execution behavior")
			}
		}
	case ToolGPU:
		data, _ := result.Data.(gpuToolData)
		if strings.TrimSpace(data.Bottleneck) != "" && !strings.Contains(strings.ToLower(data.Bottleneck), "no dominant") {
			upsertHypothesis(state, "h-gpu", data.Bottleneck, 0.10, "GPU telemetry points to a concrete device-side bottleneck")
		}
	case ToolKnowledge, ToolRAGQuery, ToolHistoricalIncident, ToolRunbookRetrieval, ToolSimilarCase:
		data, _ := result.Data.(knowledgeToolData)
		if len(data.Hits) > 0 && len(state.hypotheses) > 0 {
			recordHypothesisConfidence(state, 0, 0.04, "retrieved runbook or prior case supports the current leading hypothesis")
		}
	}
	rerankHypotheses(state)
}

func rerankHypotheses(state *workflowState) {
	if state == nil {
		return
	}
	sort.Slice(state.hypotheses, func(i, j int) bool {
		if state.hypotheses[i].Confidence == state.hypotheses[j].Confidence {
			return state.hypotheses[i].Title < state.hypotheses[j].Title
		}
		return state.hypotheses[i].Confidence > state.hypotheses[j].Confidence
	})
	if len(state.hypotheses) > state.engine.cfg.MaxHypotheses {
		state.hypotheses = state.hypotheses[:state.engine.cfg.MaxHypotheses]
	}
	for i := range state.hypotheses {
		state.hypotheses[i].Rank = i + 1
	}
}

func recordHypothesisConfidence(state *workflowState, index int, delta float64, reason string) {
	if state == nil || index < 0 || index >= len(state.hypotheses) {
		return
	}
	old := state.hypotheses[index].Confidence
	next := clamp01(old + delta)
	if next == old {
		return
	}
	state.hypotheses[index].Confidence = next
	action := "confidence_increased"
	if delta < 0 {
		action = "confidence_decreased"
	}
	state.hypothesisUpdates = append(state.hypothesisUpdates, HypothesisUpdate{
		Timestamp:     time.Now().UTC(),
		HypothesisID:  state.hypotheses[index].ID,
		Action:        action,
		OldConfidence: old,
		NewConfidence: next,
		Reason:        reason,
	})
}

func upsertHypothesis(state *workflowState, id, title string, delta float64, description string) {
	if state == nil || title == "" {
		return
	}
	for i := range state.hypotheses {
		if strings.EqualFold(strings.TrimSpace(state.hypotheses[i].Title), strings.TrimSpace(title)) || strings.EqualFold(strings.TrimSpace(state.hypotheses[i].ID), strings.TrimSpace(id)) {
			if description != "" {
				state.hypotheses[i].Description = description
			}
			recordHypothesisConfidence(state, i, delta, description)
			return
		}
	}
	confidence := clamp01(maxFloat(delta, 0.45))
	state.hypotheses = append(state.hypotheses, RCAHypothesis{
		ID:          firstNonEmpty(id, fmt.Sprintf("h-%s", sanitizeID(title))),
		Title:       title,
		Confidence:  confidence,
		Description: description,
	})
	state.hypothesisUpdates = append(state.hypothesisUpdates, HypothesisUpdate{
		Timestamp:     time.Now().UTC(),
		HypothesisID:  firstNonEmpty(id, fmt.Sprintf("h-%s", sanitizeID(title))),
		Action:        "created",
		OldConfidence: 0,
		NewConfidence: confidence,
		Reason:        description,
	})
}

func decayHypothesisByKeyword(state *workflowState, keyword string, delta float64, reason string) {
	if state == nil {
		return
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	for i := range state.hypotheses {
		if strings.Contains(strings.ToLower(state.hypotheses[i].Title), keyword) {
			recordHypothesisConfidence(state, i, -mathAbs(delta), reason)
		}
	}
}

func dedupeHypotheses(in []RCAHypothesis) []RCAHypothesis {
	if len(in) == 0 {
		return nil
	}
	out := make([]RCAHypothesis, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		key := firstNonEmpty(strings.TrimSpace(item.ID), strings.ToLower(strings.TrimSpace(item.Title)))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func hasMetricLike(state *workflowState, needle string) bool {
	if state == nil || state.metricsData.Node == nil {
		return false
	}
	needle = strings.ToLower(strings.TrimSpace(needle))
	for name := range state.metricsData.Node.Metrics {
		if strings.Contains(strings.ToLower(name), needle) {
			return true
		}
	}
	return false
}

func topHypothesisConfidence(hypotheses []RCAHypothesis) float64 {
	if len(hypotheses) == 0 {
		return 0
	}
	return hypotheses[0].Confidence
}

func evidenceIDsFromTopHypotheses(hypotheses []RCAHypothesis, limit int) []string {
	out := make([]string, 0, limit*3)
	for _, hypothesis := range hypotheses {
		out = append(out, hypothesis.EvidenceIDs...)
		if limit > 0 && len(out) >= limit*3 {
			break
		}
	}
	out = dedupeStrings(out)
	if limit > 0 && len(out) > limit*3 {
		out = out[:limit*3]
	}
	return out
}

func unresolvedGapsFromState(state *workflowState) []string {
	if state == nil {
		return nil
	}
	gaps := make([]string, 0, 8)
	if len(state.hypotheses) == 0 {
		gaps = append(gaps, "no ranked hypothesis reached the evidence threshold")
	} else if state.hypotheses[0].Confidence < 0.55 {
		gaps = append(gaps, "top hypothesis confidence remains below 0.55")
	}
	requiredPlanned, requiredVerified := summarizeRequiredPlanSteps(state.planSteps)
	if requiredPlanned > requiredVerified {
		gaps = append(gaps, fmt.Sprintf("%d required plan steps were not verified", requiredPlanned-requiredVerified))
	}
	if len(state.retrievedDocs) == 0 {
		gaps = append(gaps, "RAG retrieval did not return corroborating evidence")
	}
	return dedupeStrings(append(gaps, state.limitations...))
}

func derefProposedActions(in []*ProposedAction) []ProposedAction {
	if len(in) == 0 {
		return nil
	}
	out := make([]ProposedAction, 0, len(in))
	for _, item := range in {
		if item == nil {
			continue
		}
		out = append(out, *item)
	}
	return out
}

func recommendationSummariesByCategory(recs []WorkflowRecommendation, category string, limit int) []string {
	out := make([]string, 0, limit)
	for _, rec := range recs {
		if category != "" && !strings.EqualFold(strings.TrimSpace(rec.Category), strings.TrimSpace(category)) {
			continue
		}
		out = append(out, rec.Summary)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return dedupeStrings(out)
}

func shouldStopPlanLoop(state *workflowState) (bool, string) {
	if state == nil {
		return false, ""
	}
	if len(state.hypotheses) > 0 && state.hypotheses[0].Confidence >= 0.82 && state.stepsVerified >= 2 {
		return true, "enough evidence collected"
	}
	if state.stepsExecuted >= 2 && (len(state.hypotheses) == 0 || state.hypotheses[0].Confidence < 0.28) {
		return true, "confidence remained too low"
	}
	return false, ""
}

func mathAbs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
