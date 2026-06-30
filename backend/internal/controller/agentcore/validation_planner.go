package agent

import (
	"fmt"
	"sort"
	"strings"
)

func buildHypothesisHandoffs(state *workflowState) []AnalysisHypothesisHandoff {
	if state == nil || len(state.hypotheses) == 0 {
		return nil
	}
	index := evidenceIndex(state.evidence)
	out := make([]AnalysisHypothesisHandoff, 0, len(state.hypotheses))
	for _, hypothesis := range state.hypotheses {
		families := deriveToolFamilies(state, ValidationTargetHypothesis, hypothesis.Title, hypothesis.Description, hypothesis.EvidenceIDs, nil, nil)
		out = append(out, AnalysisHypothesisHandoff{
			HypothesisID:             hypothesis.ID,
			Rank:                     hypothesis.Rank,
			Title:                    hypothesis.Title,
			Summary:                  hypothesis.Description,
			Confidence:               hypothesis.Confidence,
			SupportingEvidenceIDs:    append([]string(nil), hypothesis.EvidenceIDs...),
			ContradictingEvidenceIDs: append([]string(nil), hypothesis.ContradictingEvidenceIDs...),
			ExpectedSignals:          expectedSignalsFromEvidence(index, hypothesis.EvidenceIDs),
			ToolFamilies:             families,
			Uncertainty:              hypothesisUncertainty(state, hypothesis),
		})
	}
	return out
}

func buildActionCandidates(state *workflowState) []ValidationActionCandidate {
	if state == nil || len(state.recommendation) == 0 {
		return nil
	}
	out := make([]ValidationActionCandidate, 0, 4)
	for _, rec := range state.recommendation {
		category := normalizeValidationCategory(rec.Category)
		if category == "" {
			category = normalizeValidationCategory(rec.ExecutionLevel)
		}
		if category == "" {
			category = "read_only_validation"
		}
		if rec.Safe && !rec.RequiresApproval && rec.DryRunDefault && category == "read_only_validation" {
			continue
		}
		tool := ToolActionOutcome
		if !rec.Safe || rec.RequiresApproval {
			tool = ToolRemediation
		}
		blastRadiusScope := dedupeStrings(append([]string(nil), state.incident.ImpactedScope...))
		candidate := ValidationActionCandidate{
			ID:                  firstNonEmpty(rec.ID, fmt.Sprintf("action-%d", len(out)+1)),
			RecommendationID:    rec.ID,
			Category:            category,
			ActionIntent:        actionIntentFromText(rec.Summary, rec.Details, rec.Rationale, rec.RollbackHint),
			ActionCategory:      actionCategoryFromIntent(actionIntentFromText(rec.Summary, rec.Details, rec.Rationale, rec.RollbackHint)),
			Summary:             rec.Summary,
			Scope:               rec.Scope,
			ExecutionLevel:      rec.ExecutionLevel,
			Safe:                rec.Safe,
			DryRunDefault:       rec.DryRunDefault,
			RequiresApproval:    rec.RequiresApproval,
			Reversible:          rec.Reversible,
			Preconditions:       append([]string(nil), rec.Preconditions...),
			RollbackHint:        rec.RollbackHint,
			ExpectedImpact:      rec.ExpectedImpact,
			BlastRadiusEstimate: len(blastRadiusScope),
			BlastRadiusScope:    blastRadiusScope,
			RollbackContract: RollbackContract{
				Summary:    strings.TrimSpace(rec.RollbackHint),
				Required:   rec.Reversible || strings.TrimSpace(rec.RollbackHint) != "",
				Reversible: rec.Reversible,
			},
			Metadata: map[string]string{
				"recommendation_priority": strings.TrimSpace(rec.Priority),
			},
			PrimaryTool: tool,
		}
		diagnostic := buildDiagnosticActionContract(candidate, state)
		candidate.DiagnosticContract = &diagnostic
		contract := buildValidationActionContract(candidate, state.collectorID, blastRadiusScope)
		candidate.ActuatorSafetyTier = contract.ActuatorSafetyTier
		candidate.ActionContract = &contract
		out = append(out, candidate)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func buildValidationTargetPlan(state *workflowState, targetType ValidationTargetType, title, summary, hypothesisID, recommendationID, actionCandidateID, priority string, supporting, contradicting, changeCategories []string) ValidationTarget {
	families := deriveToolFamilies(state, targetType, title, summary, supporting, changeCategories, nil)
	target := ValidationTarget{
		Type:                     targetType,
		Title:                    title,
		Summary:                  summary,
		HypothesisID:             hypothesisID,
		RecommendationID:         recommendationID,
		ActionCandidateID:        actionCandidateID,
		Priority:                 priority,
		ImpactedScope:            impactedScopeFromState(state),
		ToolFamilies:             families,
		EvidenceGaps:             deriveEvidenceGaps(state, targetType, supporting, contradicting, changeCategories, families),
		ContradictionCandidates:  contradictionCandidates(targetType, families),
		ChangeCategories:         dedupeStrings(changeCategories),
		SupportingEvidenceIDs:    append([]string(nil), supporting...),
		ContradictingEvidenceIDs: append([]string(nil), contradicting...),
		ExpectedSignals:          expectedSignalsForFamilies(families),
		ReadOnly:                 targetType != ValidationTargetRemediation,
		ExecutionCategory:        executionCategoryForTarget(targetType, actionCandidateID, recommendationID, state),
		Focus:                    deriveValidationFocus(families, title, summary),
	}
	target.SuggestedTools = prioritizeValidationTools(suggestedToolsForTargetFamilies(targetType, families), target.EvidenceGaps)
	if targetType == ValidationTargetRecommendation {
		target.ReadOnly = true
	}
	if targetType == ValidationTargetRemediation {
		target.PostAction = true
	}
	return target
}

func deriveToolFamilies(state *workflowState, targetType ValidationTargetType, title, summary string, evidenceIDs, changeCategories, extra []string) []string {
	families := make([]string, 0, 8)
	add := func(items ...string) {
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" || slicesContainsString(families, item) {
				continue
			}
			families = append(families, item)
		}
	}

	if targetType == ValidationTargetRecommendation {
		add("service_health", "metrics", "logs")
	}
	if targetType == ValidationTargetSceneClassification {
		add(collectionPlanFamilies(state.collectionPlan)...)
	}
	if targetType == ValidationTargetChangeCorrelation {
		add("change", "config")
	}
	if targetType == ValidationTargetContradiction {
		add("contradiction")
	}

	index := evidenceIndex(state.evidence)
	for _, id := range evidenceIDs {
		evidence, ok := index[id]
		if !ok {
			continue
		}
		switch evidence.Kind {
		case "change_event":
			add("change", "config")
		case "runtime_security_event":
			add("security", "runtime")
		case "gpu_metric":
			add("gpu", "metrics", "service_health")
		case "adaptive_baseline":
			add("metrics")
		case "knowledge_retrieval":
			add("knowledge")
		default:
			add("metrics")
		}
	}

	for _, category := range changeCategories {
		switch strings.ToLower(strings.TrimSpace(category)) {
		case "deployment", "release", "runtime", "feature_flag":
			add("change", "config", "kubernetes")
		case "driver":
			add("change", "gpu")
		case "network":
			add("network", "topology")
		}
	}

	if state != nil {
		if len(state.security.StructuredFindings) > 0 || len(state.security.Findings) > 0 {
			add("security")
		}
		if len(state.changeLinks) > 0 && (targetType == ValidationTargetChangeCorrelation || targetType == ValidationTargetRecommendation) {
			add("change")
		}
		if targetType == ValidationTargetRecommendation {
			if len(state.metricsData.History) < 3 {
				add("metrics")
			}
			if len(state.logsData.Snippets) == 0 && state.logsData.Errors+state.logsData.Warnings == 0 {
				add("logs")
			}
			for _, rec := range state.recommendation {
				lower := strings.ToLower(strings.TrimSpace(rec.Scope + " " + rec.Summary + " " + rec.Details))
				switch {
				case strings.Contains(lower, "deploy"), strings.Contains(lower, "rollout"), strings.Contains(lower, "revision"):
					add("change", "config", "kubernetes")
				case strings.Contains(lower, "memory"), strings.Contains(lower, "oom"):
					add("memory")
				case strings.Contains(lower, "network"), strings.Contains(lower, "dns"), strings.Contains(lower, "connect"):
					add("network")
				}
			}
			for _, blindSpot := range state.telemetryQuality.BlindSpots {
				spot := strings.ToLower(strings.TrimSpace(blindSpot))
				switch {
				case strings.Contains(spot, "metric"), strings.Contains(spot, "history"), strings.Contains(spot, "baseline"):
					add("metrics")
				case strings.Contains(spot, "log"):
					add("logs")
				case strings.Contains(spot, "service"), strings.Contains(spot, "latency"), strings.Contains(spot, "health"):
					add("service_health")
				case strings.Contains(spot, "runtime"), strings.Contains(spot, "ebpf"):
					add("runtime")
				}
			}
		}
		for _, missing := range state.telemetryQuality.MissingSignals {
			switch strings.ToLower(strings.TrimSpace(missing)) {
			case "logs":
				add("logs")
			case "metrics":
				add("metrics")
			case "topology":
				add("topology")
			case "runtime", "ebpf":
				add("runtime")
			}
		}
	}

	add(extra...)
	if len(families) == 0 {
		switch classifyValidationFocus(strings.Join([]string{title, summary}, " ")) {
		case "change":
			add("change", "config")
		case "gpu":
			add("gpu", "metrics")
		case "security":
			add("security", "runtime")
		case "network":
			add("network", "service_health", "topology")
		case "memory":
			add("memory", "metrics", "logs")
		case "storage":
			add("storage", "metrics")
		case "kubernetes":
			add("kubernetes", "change")
		default:
			add("metrics", "logs", "service_health")
		}
	}
	return families
}

func collectionPlanFamilies(plan CollectionPlan) []string {
	families := make([]string, 0, len(plan.TargetCollectorsOrModules))
	for _, module := range plan.TargetCollectorsOrModules {
		switch strings.ToLower(strings.TrimSpace(module)) {
		case "metrics", "process":
			families = append(families, "metrics")
		case "logs":
			families = append(families, "logs")
		case "change", "config", "container_revision":
			families = append(families, "change", "config")
		case "kubernetes":
			families = append(families, "kubernetes")
		case "connectivity", "dns":
			families = append(families, "network", "service_health")
		case "storage":
			families = append(families, "storage", "metrics")
		case "gpu":
			families = append(families, "gpu", "metrics")
		case "runtime":
			families = append(families, "runtime")
		case "security", "security_graph":
			families = append(families, "security")
		case "topology":
			families = append(families, "topology")
		case "service_health":
			families = append(families, "service_health")
		}
	}
	return dedupeStrings(families)
}

func deriveEvidenceGaps(state *workflowState, targetType ValidationTargetType, supporting, contradicting, changeCategories, families []string) []string {
	gaps := make([]string, 0, 8)
	add := func(items ...string) {
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" || slicesContainsString(gaps, item) {
				continue
			}
			gaps = append(gaps, item)
		}
	}
	if len(supporting) == 0 {
		add("supporting_evidence")
	}
	if targetType == ValidationTargetContradiction || len(contradicting) == 0 {
		add("contradiction_search")
	}
	if targetType == ValidationTargetRecommendation {
		add("validated_hypothesis")
	}
	if len(changeCategories) > 0 {
		add("change_scope_overlap", "revision_relevance")
	}
	for _, family := range families {
		switch family {
		case "logs":
			add("log_context")
		case "metrics":
			add("metric_baseline")
		case "change":
			add("temporal_overlap")
		case "config":
			add("config_state")
		case "security":
			add("security_counterevidence")
		case "network":
			add("service_health")
		case "knowledge":
			add("runbook_alignment", "prior_outcome_match")
		case "contradiction":
			add("healthy_counterevidence")
		}
	}
	if state != nil {
		add(state.telemetryQuality.BlindSpots...)
	}
	return gaps
}

func contradictionCandidates(targetType ValidationTargetType, families []string) []string {
	if targetType != ValidationTargetContradiction {
		return nil
	}
	out := make([]string, 0, len(families))
	for _, family := range families {
		switch family {
		case "change":
			out = append(out, "no relevant rollout or config delta")
		case "service_health":
			out = append(out, "service health stayed healthy")
		case "network":
			out = append(out, "connectivity stayed healthy")
		case "security":
			out = append(out, "security findings do not overlap the scope")
		case "memory":
			out = append(out, "memory pressure is absent")
		}
	}
	return dedupeStrings(out)
}

func suggestedToolsForTargetFamilies(targetType ValidationTargetType, families []string) []ToolName {
	tools := make([]ToolName, 0, 10)
	add := func(items ...ToolName) {
		for _, item := range items {
			if item == "" || slicesContainsTool(tools, item) {
				continue
			}
			tools = append(tools, item)
		}
	}
	for _, family := range families {
		switch family {
		case "metrics":
			add(ToolMetrics)
		case "logs":
			add(ToolLogs)
		case "service_health":
			add(ToolServiceHealth)
		case "change":
			add(ToolChangeQuery, ToolDeploymentHistory)
		case "config":
			add(ToolConfigState, ToolContainerRevision)
		case "kubernetes":
			add(ToolKubernetesResource, ToolContainerRevision)
		case "runtime":
			add(ToolEBPFQuery, ToolProcessLineage)
		case "security":
			add(ToolSecurity, ToolSecurityGraph, ToolProcessLineage)
		case "network":
			add(ToolConnectivityCheck, ToolDNSCheck, ToolNetworkBlastRadius, ToolTopology)
		case "memory":
			add(ToolMemoryPressure, ToolMetrics, ToolLogs)
		case "storage":
			add(ToolStorageHealth, ToolMetrics, ToolLogs)
		case "gpu":
			add(ToolGPU, ToolMetrics, ToolServiceHealth)
		case "knowledge":
			add(ToolRunbookRetrieval, ToolSimilarCase, ToolHistoricalIncident, ToolActionOutcome)
		}
	}
	switch targetType {
	case ValidationTargetRecommendation:
		add(ToolRunbookRetrieval, ToolActionOutcome, ToolServiceHealth)
	case ValidationTargetRemediation:
		add(ToolServiceHealth, ToolMetrics, ToolLogs)
	case ValidationTargetContradiction:
		add(ToolServiceHealth)
	}
	if len(tools) == 0 {
		add(ToolMetrics, ToolLogs, ToolServiceHealth)
	}
	return tools
}

func prioritizeValidationTools(tools []ToolName, gaps []string) []ToolName {
	if len(tools) < 2 || len(gaps) == 0 {
		return tools
	}
	prioritized := make([]ToolName, 0, len(tools))
	addFront := func(candidates ...ToolName) {
		for _, candidate := range candidates {
			for _, tool := range tools {
				if tool != candidate || slicesContainsTool(prioritized, tool) {
					continue
				}
				prioritized = append(prioritized, tool)
			}
		}
	}
	for _, gap := range gaps {
		gap = strings.ToLower(strings.TrimSpace(gap))
		switch {
		case strings.Contains(gap, "metric"), strings.Contains(gap, "history"), strings.Contains(gap, "baseline"):
			addFront(ToolMetrics)
		case strings.Contains(gap, "log"):
			addFront(ToolLogs)
		case strings.Contains(gap, "service_health"), strings.Contains(gap, "latency"):
			addFront(ToolServiceHealth)
		case strings.Contains(gap, "config"), strings.Contains(gap, "revision"):
			addFront(ToolConfigState, ToolDeploymentHistory)
		case strings.Contains(gap, "security"):
			addFront(ToolSecurity, ToolSecurityGraph)
		case strings.Contains(gap, "runbook"), strings.Contains(gap, "outcome"):
			addFront(ToolRunbookRetrieval, ToolActionOutcome, ToolHistoricalIncident)
		}
	}
	for _, tool := range tools {
		if slicesContainsTool(prioritized, tool) {
			continue
		}
		prioritized = append(prioritized, tool)
	}
	return prioritized
}

func expectedSignalsFromEvidence(index map[string]RCAEvidence, evidenceIDs []string) []string {
	signals := make([]string, 0, len(evidenceIDs))
	for _, id := range evidenceIDs {
		evidence, ok := index[id]
		if !ok {
			continue
		}
		if evidence.MetricName != "" {
			signals = append(signals, evidence.MetricName)
			continue
		}
		signals = append(signals, evidence.Kind)
	}
	return dedupeStrings(signals)
}

func expectedSignalsForFamilies(families []string) []string {
	out := make([]string, 0, len(families)*2)
	for _, family := range families {
		switch family {
		case "service_health":
			out = append(out, "service_latency_p95_ms", "service_error_rate")
		case "network":
			out = append(out, "network_throughput", "packet_retransmits")
		case "memory":
			out = append(out, "memory_usage_percent", "oom_kill")
		case "gpu":
			out = append(out, "gpu_utilization", "gpu_memory_used")
		case "storage":
			out = append(out, "storage_latency", "disk_utilization")
		case "change":
			out = append(out, "deployment_history", "config_state")
		}
	}
	return dedupeStrings(out)
}

func deriveValidationFocus(families []string, title, summary string) string {
	for _, family := range families {
		switch family {
		case "change", "gpu", "security", "network", "memory", "storage", "kubernetes":
			return family
		}
	}
	return classifyValidationFocus(strings.Join([]string{title, summary}, " "))
}

func impactedScopeFromState(state *workflowState) []string {
	if state == nil {
		return nil
	}
	scope := append([]string(nil), state.incident.ImpactedScope...)
	scope = append(scope, state.incident.TopologyNeighborhood...)
	return dedupeStrings(scope)
}

func hypothesisUncertainty(state *workflowState, hypothesis RCAHypothesis) []string {
	uncertainty := make([]string, 0, 4)
	if len(hypothesis.ContradictingEvidenceIDs) == 0 {
		uncertainty = append(uncertainty, "contradicting_evidence_missing")
	}
	if hypothesis.Confidence < 0.65 {
		uncertainty = append(uncertainty, "confidence_below_strong_threshold")
	}
	if state != nil {
		uncertainty = append(uncertainty, state.telemetryQuality.BlindSpots...)
	}
	return dedupeStrings(uncertainty)
}

func evidenceIndex(items []RCAEvidence) map[string]RCAEvidence {
	index := make(map[string]RCAEvidence, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		index[item.ID] = item
	}
	return index
}

func slicesContainsTool(items []ToolName, target ToolName) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func slicesContainsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func normalizeValidationCategory(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "", "read_only", "read_only_validation":
		return "read_only_validation"
	case "immediate_investigation", "knowledge_review":
		return "read_only_validation"
	case "probable_containment", "containment":
		return "probable_containment"
	case "medium_term_remediation", "remediation":
		return "medium_term_remediation"
	case "profiling", "diagnostic_execution":
		return "profiling"
	default:
		return value
	}
}

func executionCategoryForTarget(targetType ValidationTargetType, actionCandidateID, recommendationID string, state *workflowState) string {
	if targetType == ValidationTargetRemediation && state != nil {
		for _, candidate := range state.analysisHandoff.BoundedActionCandidates {
			if candidate.ID == actionCandidateID || candidate.RecommendationID == recommendationID {
				return normalizeValidationCategory(candidate.Category)
			}
		}
	}
	if targetType == ValidationTargetRecommendation && state != nil {
		for _, rec := range state.recommendation {
			if rec.ID == recommendationID {
				return normalizeValidationCategory(rec.Category)
			}
		}
	}
	return "read_only_validation"
}

func validationExecutionAllowed(cfg WorkflowConfig, category string) bool {
	category = normalizeValidationCategory(category)
	if category == "" || category == "read_only_validation" {
		return true
	}
	for _, allowed := range cfg.ValidationAllowExecCategories {
		allowed = normalizeValidationCategory(allowed)
		if allowed == "*" || allowed == category {
			return true
		}
	}
	return false
}

func sortedCopyStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := append([]string(nil), items...)
	sort.Strings(out)
	return out
}
