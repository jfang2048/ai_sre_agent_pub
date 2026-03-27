package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/causalgraph"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

func (s *workflowState) applyChangeResult(result workflowToolResult) {
	data, ok := result.Data.(changeToolData)
	if !ok {
		return
	}
	s.changes = data
	s.changeLinks = changeLinksFromToolData(data)
}

func changeLinksFromToolData(data changeToolData) []RCAChangeLink {
	if len(data.Events) == 0 {
		return nil
	}
	out := make([]RCAChangeLink, 0, len(data.Events))
	for _, event := range data.Events {
		out = append(out, RCAChangeLink{
			ChangeID:          event.Event.ChangeID,
			Category:          event.Event.Category,
			Summary:           event.Event.Summary,
			Source:            event.Event.Source,
			Entity:            event.Event.Entity,
			Scope:             event.Event.Scope,
			StartedAt:         event.Event.StartedAt,
			TemporalAdjacency: event.TemporalAdjacency,
			ScopeOverlap:      event.ScopeOverlap,
			CorrelationScore:  event.ChangeScore,
			ImpactSummary:     event.ImpactSummary,
			HypothesisHint:    firstNonEmpty(strings.Join(event.HypothesisHints, "; "), event.Event.HypothesisHint),
		})
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func changeSummaries(links []RCAChangeLink) []string {
	if len(links) == 0 {
		return nil
	}
	out := make([]string, 0, len(links))
	for _, link := range links {
		if summary := strings.TrimSpace(firstNonEmpty(link.Summary, link.HypothesisHint)); summary != "" {
			out = append(out, summary)
		}
	}
	return dedupeStrings(out)
}

func deriveAdaptiveBaselineInsights(state *workflowState) []AdaptiveBaselineInsight {
	if state == nil || state.metricsData.Node == nil {
		return nil
	}
	node := state.metricsData.Node
	profile := hardwareProfile(node, state.gpu)
	workload := primaryWorkloadContext(node)
	type metricSet struct {
		dimension string
		keys      []string
	}
	sets := []metricSet{
		{dimension: "cpu", keys: []string{"node_cpu_usage_percent", "node_cpu_utilization_percent", "cpu_usage_percent"}},
		{dimension: "memory", keys: []string{"node_memory_used_percent", "node_memory_pressure_percent", "memory_used_percent"}},
		{dimension: "disk_io", keys: []string{"node_disk_io_utilization_percent", "node_disk_queue_fill_percent", "node_disk_read_bytes_per_second"}},
		{dimension: "network", keys: []string{"node_network_retransmit_percent", "node_network_rx_bytes_per_second", "node_network_tx_bytes_per_second"}},
		{dimension: "gpu", keys: []string{"node_gpu_utilization_sm_avg_percent", "node_gpu_memory_used_percent", "node_gpu_pcie_rx_total_mb_s"}},
	}
	out := make([]AdaptiveBaselineInsight, 0, len(sets))
	for _, set := range sets {
		metric, current, baseline, ok := baselineForMetric(state.metricsData.Node, state.metricsData.History, set.keys)
		if !ok {
			continue
		}
		deltaPercent := percentDelta(current, baseline)
		classification := classifyBaselineShift(current, baseline, state.metricsData.History, metric)
		if classification == "stable" {
			continue
		}
		out = append(out, AdaptiveBaselineInsight{
			Dimension:       set.dimension,
			Metric:          metric,
			Entity:          firstNonEmpty(node.Hostname, node.CollectorID),
			WorkloadClass:   workload.WorkloadClass,
			Job:             workload.Job,
			PodUID:          workload.PodUID,
			HardwareProfile: profile,
			Current:         current,
			Baseline:        baseline,
			DeltaPercent:    deltaPercent,
			Classification:  classification,
			Explanation:     baselineExplanation(set.dimension, metric, deltaPercent, workload, profile, state.gpu),
		})
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

type workloadContext struct {
	WorkloadClass string
	Job           string
	PodUID        string
}

func primaryWorkloadContext(node *ingest.NodeSnapshot) workloadContext {
	if node == nil {
		return workloadContext{}
	}
	top := topProcessResources(node, 1)
	if len(top) == 0 || top[0] == nil {
		return workloadContext{}
	}
	return workloadContext{
		WorkloadClass: top[0].WorkloadClass,
		Job:           top[0].Job,
		PodUID:        top[0].PodUID,
	}
}

func hardwareProfile(node *ingest.NodeSnapshot, gpu gpuToolData) string {
	if node == nil {
		return ""
	}
	parts := []string{firstNonEmpty(node.OS, "linux"), firstNonEmpty(node.Arch, "unknown-arch")}
	if len(gpu.Metrics) > 0 {
		parts = append(parts, "gpu-enabled")
	}
	keys := make([]string, 0, len(node.Labels))
	for key := range node.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := node.Labels[key]
		low := strings.ToLower(key)
		if strings.Contains(low, "driver") || strings.Contains(low, "instance") || strings.Contains(low, "accelerator") {
			parts = append(parts, fmt.Sprintf("%s=%s", key, value))
		}
	}
	return truncateString(strings.Join(dedupeStrings(parts), " | "), 180)
}

func baselineForMetric(node *ingest.NodeSnapshot, history []ingest.MetricHistorySample, candidates []string) (string, float64, float64, bool) {
	if node == nil {
		return "", 0, 0, false
	}
	for _, candidate := range candidates {
		current, ok := node.Metrics[candidate]
		if !ok {
			continue
		}
		total := 0.0
		samples := 0
		for _, sample := range history {
			if value, ok := sample.Metrics[candidate]; ok {
				total += value
				samples++
			}
		}
		if samples == 0 {
			continue
		}
		return candidate, current, total / float64(samples), true
	}
	return "", 0, 0, false
}

func percentDelta(current, baseline float64) float64 {
	if baseline == 0 {
		if current == 0 {
			return 0
		}
		return 100
	}
	return ((current - baseline) / baseline) * 100
}

func classifyBaselineShift(current, baseline float64, history []ingest.MetricHistorySample, metric string) string {
	delta := absFloat(percentDelta(current, baseline))
	if delta < 15 {
		return "stable"
	}
	if len(history) < 6 {
		if delta >= 35 {
			return "short_term_spike"
		}
		return "long_term_drift"
	}
	mid := len(history) / 2
	firstHalf := averageMetric(history[:mid], metric)
	secondHalf := averageMetric(history[mid:], metric)
	if firstHalf == 0 || secondHalf == 0 {
		if delta >= 35 {
			return "short_term_spike"
		}
		return "long_term_drift"
	}
	if absFloat(percentDelta(secondHalf, firstHalf)) >= 20 && absFloat(percentDelta(current, secondHalf)) >= 10 {
		return "short_term_spike"
	}
	return "long_term_drift"
}

func averageMetric(history []ingest.MetricHistorySample, metric string) float64 {
	if len(history) == 0 || strings.TrimSpace(metric) == "" {
		return 0
	}
	total := 0.0
	count := 0
	for _, sample := range history {
		if value, ok := sample.Metrics[metric]; ok {
			total += value
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func baselineExplanation(dimension, metric string, deltaPercent float64, workload workloadContext, hardwareProfile string, gpu gpuToolData) string {
	parts := []string{
		fmt.Sprintf("%s deviated from its adaptive baseline by %.1f%%", metric, deltaPercent),
	}
	if workload.WorkloadClass != "" || workload.Job != "" {
		parts = append(parts, fmt.Sprintf("workload=%s job=%s", firstNonEmpty(workload.WorkloadClass, "unknown"), firstNonEmpty(workload.Job, "unknown")))
	}
	if strings.Contains(strings.ToLower(dimension), "gpu") && strings.TrimSpace(gpu.Bottleneck) != "" {
		parts = append(parts, "gpu attribution="+gpu.Bottleneck)
	}
	if hardwareProfile != "" {
		parts = append(parts, "hardware="+hardwareProfile)
	}
	return truncateString(strings.Join(parts, " | "), 260)
}

func buildCausalAnalysis(state *workflowState) causalgraph.Analysis {
	if state == nil {
		return causalgraph.Analysis{}
	}
	nodes := make([]causalgraph.Node, 0, 48)
	edges := make([]causalgraph.Edge, 0, 96)
	symptomNodes := make([]string, 0, 12)
	impactScope := make([]string, 0, 12)

	for _, link := range state.changeLinks {
		nodeID := "change:" + firstNonEmpty(link.ChangeID, sanitizeID(link.Summary))
		nodes = append(nodes, causalgraph.Node{
			ID:    nodeID,
			Kind:  "change",
			Label: firstNonEmpty(link.Summary, link.ChangeID),
			Role:  "cause",
			Score: clamp01(link.CorrelationScore),
			Metadata: map[string]string{
				"category": link.Category,
				"scope":    link.Scope,
				"entity":   link.Entity,
			},
		})
		target := firstNonEmpty(link.Entity, state.collectorID)
		if target != "" {
			targetID := "impact:" + sanitizeID(target)
			nodes = append(nodes, causalgraph.Node{ID: targetID, Kind: "topology", Label: target, Score: 0.25})
			edges = append(edges, causalgraph.Edge{Source: nodeID, Target: targetID, Kind: "changes", Weight: clamp01(link.CorrelationScore)})
		}
	}
	for _, signal := range topTriggeredSignals(state.riskSignals, 8) {
		nodeID := "signal:" + sanitizeID(signal.ID)
		nodes = append(nodes, causalgraph.Node{
			ID:    nodeID,
			Kind:  "signal",
			Label: fmt.Sprintf("%s on %s", signal.Name, signal.Entity),
			Role:  "symptom",
			Score: clamp01(signal.Score),
		})
		symptomNodes = append(symptomNodes, nodeID)
		targetID := "impact:" + sanitizeID(firstNonEmpty(signal.Entity, state.collectorID))
		nodes = append(nodes, causalgraph.Node{ID: targetID, Kind: "topology", Label: firstNonEmpty(signal.Entity, state.collectorID), Score: 0.20})
		edges = append(edges, causalgraph.Edge{Source: targetID, Target: nodeID, Kind: "emits", Weight: clamp01(signal.Score)})
	}
	for _, process := range topProcessResources(state.metricsData.Node, 6) {
		if process == nil {
			continue
		}
		nodeID := "process:" + sanitizeID(processDisplayName(process))
		nodes = append(nodes, causalgraph.Node{
			ID:    nodeID,
			Kind:  "process",
			Label: processDisplayName(process),
			Score: clamp01(processPressureScore(process) / 100.0),
		})
		collectorNode := "impact:" + sanitizeID(firstNonEmpty(state.collectorID, processDisplayName(process)))
		nodes = append(nodes, causalgraph.Node{ID: collectorNode, Kind: "topology", Label: firstNonEmpty(state.collectorID, processDisplayName(process)), Score: 0.18})
		edges = append(edges, causalgraph.Edge{Source: collectorNode, Target: nodeID, Kind: "runs", Weight: 0.5})
	}
	for _, edge := range state.topoData.Snapshot.Edges {
		sourceID := "impact:" + sanitizeID(firstNonEmpty(edge.Source, "unknown"))
		targetID := "impact:" + sanitizeID(firstNonEmpty(edge.Target, "unknown"))
		nodes = append(nodes,
			causalgraph.Node{ID: sourceID, Kind: "topology", Label: edge.Source, Score: 0.15},
			causalgraph.Node{ID: targetID, Kind: "topology", Label: edge.Target, Score: 0.15},
		)
		edges = append(edges, causalgraph.Edge{Source: sourceID, Target: targetID, Kind: edge.Kind, Weight: 0.35})
	}
	for _, edge := range state.lineage.Edges {
		sourceID := "process:" + sanitizeID(edge.Source)
		targetID := "process:" + sanitizeID(edge.Target)
		nodes = append(nodes,
			causalgraph.Node{ID: sourceID, Kind: "process", Label: edge.Source, Score: 0.15},
			causalgraph.Node{ID: targetID, Kind: "process", Label: edge.Target, Score: 0.15},
		)
		edges = append(edges, causalgraph.Edge{Source: sourceID, Target: targetID, Kind: edge.Kind, Weight: 0.30})
	}
	for _, edge := range state.secGraph.Edges {
		sourceID := "runtime:" + sanitizeID(edge.Source)
		targetID := "runtime:" + sanitizeID(edge.Target)
		nodes = append(nodes,
			causalgraph.Node{ID: sourceID, Kind: "runtime", Label: edge.Source, Score: 0.18},
			causalgraph.Node{ID: targetID, Kind: "runtime", Label: edge.Target, Score: 0.18},
		)
		edges = append(edges, causalgraph.Edge{Source: sourceID, Target: targetID, Kind: edge.Relation, Weight: 0.28})
	}
	for _, item := range state.incident.ImpactedScope {
		impactScope = append(impactScope, "impact:"+sanitizeID(item))
	}
	if len(impactScope) == 0 && state.collectorID != "" {
		impactScope = append(impactScope, "impact:"+sanitizeID(state.collectorID))
	}

	input := causalgraph.Input{
		Nodes:        dedupeCausalNodes(nodes),
		Edges:        dedupeCausalEdges(edges),
		SymptomNodes: dedupeStrings(symptomNodes),
		ImpactScope:  dedupeStrings(impactScope),
	}
	return causalgraph.Analyze(input)
}

func dedupeCausalNodes(nodes []causalgraph.Node) []causalgraph.Node {
	if len(nodes) == 0 {
		return nil
	}
	best := make(map[string]causalgraph.Node, len(nodes))
	for _, node := range nodes {
		if strings.TrimSpace(node.ID) == "" {
			continue
		}
		if current, ok := best[node.ID]; ok && current.Score >= node.Score {
			continue
		}
		best[node.ID] = node
	}
	out := make([]causalgraph.Node, 0, len(best))
	for _, node := range best {
		out = append(out, node)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func dedupeCausalEdges(edges []causalgraph.Edge) []causalgraph.Edge {
	if len(edges) == 0 {
		return nil
	}
	out := make([]causalgraph.Edge, 0, len(edges))
	seen := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		key := strings.ToLower(strings.TrimSpace(edge.Source + "|" + edge.Target + "|" + edge.Kind))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source == out[j].Source {
			return out[i].Target < out[j].Target
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func buildEvidenceProvenance(state *workflowState) []EvidenceProvenance {
	if state == nil {
		return nil
	}
	out := make([]EvidenceProvenance, 0, 12)
	for _, item := range state.changeLinks {
		out = append(out, EvidenceProvenance{
			EvidenceID: firstNonEmpty(item.ChangeID, sanitizeID(item.Summary)),
			SourceType: "change_intel",
			Source:     item.Source,
			Summary:    item.Summary,
			Weight:     item.CorrelationScore,
		})
	}
	for _, item := range state.evidence {
		out = append(out, EvidenceProvenance{
			EvidenceID: firstNonEmpty(item.ID, sanitizeID(item.Summary)),
			SourceType: item.Kind,
			Source:     item.Source,
			Summary:    item.Summary,
			Weight:     clamp01(maxFloat(item.Value, absFloat(item.Delta)) / 100.0),
		})
		if len(out) >= 10 {
			break
		}
	}
	for _, item := range state.incidentMemoryMatches {
		out = append(out, EvidenceProvenance{
			EvidenceID: item.EvidenceID,
			SourceType: item.SourceType,
			Source:     item.SourcePath,
			Summary:    firstNonEmpty(item.Title, item.Summary, item.Snippet),
			Weight:     item.Score,
		})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func rollbackHintForChangeCategory(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "deployment":
		return "roll back to the previous release or image revision"
	case "config":
		return "restore the previous configuration snapshot"
	case "driver":
		return "revert to the previously known-good driver/runtime version"
	case "feature_flag":
		return "disable the flag or return the canary to the prior state"
	case "infrastructure":
		return "revert the node/runtime change or drain the affected node"
	default:
		return "restore the last known-good operational state"
	}
}

func buildUncertaintyComponents(state *workflowState) []UncertaintyComponent {
	if state == nil {
		return nil
	}
	telemetryScore := state.telemetryQuality.Confidence
	if telemetryScore <= 0 {
		maxSignals := 1.0
		if state.engine != nil && state.engine.cfg.MaxSignals > 0 {
			maxSignals = float64(state.engine.cfg.MaxSignals)
		} else if len(state.riskSignals) > 0 {
			maxSignals = float64(maxInt(len(state.riskSignals), 1))
		}
		telemetryScore = clamp01(float64(len(state.riskSignals)) / maxFloatValue(maxSignals, 1))
	}
	components := []UncertaintyComponent{
		{
			Dimension: "telemetry_coverage",
			Score:     clamp01(telemetryScore),
			Reason:    firstNonEmpty(telemetryQualityReason(state.telemetryQuality), firstNonEmpty(firstString(workflowLimitations(state)), "weighted signals and context determine telemetry coverage confidence")),
		},
		{
			Dimension: "change_correlation",
			Score:     strongestChangeScore(state.changeLinks),
			Reason:    firstNonEmpty(strongestChangeSummary(state.changeLinks), "no strongly correlated operational change was found"),
		},
		{
			Dimension: "historical_match",
			Score:     strongestRetrievedScore(state.incidentMemoryMatches),
			Reason:    firstNonEmpty(strongestRetrievedSummary(state.incidentMemoryMatches), "no strong prior-incident memory match was found"),
		},
		{
			Dimension: "plan_verification",
			Score:     verificationCoverage(state.planSteps),
			Reason:    firstNonEmpty(state.planStopReason, "plan coverage derived from verified investigation steps"),
		},
	}
	return components
}

func incidentMemoryActionOutcomes(steps []AgentPlanStep) []WorkflowMemoryActionOutcome {
	if len(steps) == 0 {
		return nil
	}
	out := make([]WorkflowMemoryActionOutcome, 0, len(steps))
	for _, step := range steps {
		if strings.TrimSpace(step.SupersededBy) != "" {
			continue
		}
		out = append(out, WorkflowMemoryActionOutcome{
			ActionID:       step.ID,
			Action:         firstNonEmpty(step.Query["action"], step.Title),
			Status:         step.Status,
			Verification:   step.VerificationNote,
			Success:        step.Verified,
			Useful:         step.Verified,
			ExecutedAt:     step.StartedAt,
			CompletedAt:    step.CompletedAt,
			RollbackStatus: map[bool]string{true: "not_needed", false: ""}[step.Verified],
		})
	}
	return out
}

func strongestChangeScore(links []RCAChangeLink) float64 {
	best := 0.0
	for _, item := range links {
		if item.CorrelationScore > best {
			best = item.CorrelationScore
		}
	}
	return clamp01(best)
}

func strongestChangeSummary(links []RCAChangeLink) string {
	if len(links) == 0 {
		return ""
	}
	return links[0].Summary
}

func strongestRetrievedScore(items []RetrievedDocumentEvidence) float64 {
	best := 0.0
	for _, item := range items {
		if item.Score > best {
			best = item.Score
		}
	}
	return clamp01(best)
}

func strongestRetrievedSummary(items []RetrievedDocumentEvidence) string {
	if len(items) == 0 {
		return ""
	}
	return firstNonEmpty(items[0].Title, items[0].Summary, items[0].Snippet)
}

func verificationCoverage(steps []AgentPlanStep) float64 {
	if len(steps) == 0 {
		return 0
	}
	total := 0.0
	verified := 0.0
	for _, step := range steps {
		if strings.TrimSpace(step.SupersededBy) != "" {
			continue
		}
		total++
		if step.Verified {
			verified++
		}
	}
	if total == 0 {
		return 0
	}
	return clamp01(verified / total)
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
