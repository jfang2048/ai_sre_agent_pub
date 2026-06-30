package agent

import (
	"fmt"
	"sort"
	"strings"
)

type SceneFamily string

const (
	SceneFamilyResourceContention       SceneFamily = "resource_contention"
	SceneFamilyChangeInduced            SceneFamily = "change_induced"
	SceneFamilyNetworkConnectivity      SceneFamily = "network_connectivity"
	SceneFamilyStorageIO                SceneFamily = "storage_io"
	SceneFamilyGPUInference             SceneFamily = "gpu_inference"
	SceneFamilyGPUTrainingOrCollective  SceneFamily = "gpu_training_or_collective"
	SceneFamilyDeploymentRollout        SceneFamily = "deployment_rollout"
	SceneFamilyKubernetesWorkload       SceneFamily = "kubernetes_workload"
	SceneFamilyBareMetalKernelOrIRQ     SceneFamily = "bare_metal_kernel_or_irq"
	SceneFamilyDatabaseLikeLatencyPath  SceneFamily = "database_like_latency_path"
	SceneFamilySecurityOrProcessAnomaly SceneFamily = "security_or_process_anomaly"
)

type SceneClassification struct {
	SceneFamily        SceneFamily `json:"scene_family"`
	Confidence         float64     `json:"confidence"`
	CandidateSubscenes []string    `json:"candidate_subscenes,omitempty"`
	MissingEvidence    []string    `json:"missing_evidence,omitempty"`
	CollectionHints    []string    `json:"collection_hints,omitempty"`
}

type sceneScore struct {
	Family    SceneFamily
	Score     float64
	Subscenes []string
	Hints     []string
	Missing   []string
}

func classifyScene(state *workflowState) SceneClassification {
	if state == nil {
		return SceneClassification{
			SceneFamily:     SceneFamilyResourceContention,
			Confidence:      0.35,
			MissingEvidence: []string{"broad_observation_unavailable"},
			CollectionHints: sceneCollectionHints(SceneFamilyResourceContention),
		}
	}

	scores := sceneScoresFromState(state)
	if len(scores) == 0 {
		return SceneClassification{
			SceneFamily:     SceneFamilyResourceContention,
			Confidence:      0.40,
			MissingEvidence: sceneMissingEvidence(SceneFamilyResourceContention, state),
			CollectionHints: sceneCollectionHints(SceneFamilyResourceContention),
		}
	}

	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Score == scores[j].Score {
			return scores[i].Family < scores[j].Family
		}
		return scores[i].Score > scores[j].Score
	})

	total := 0.0
	for _, score := range scores {
		total += score.Score
	}
	top := scores[0]
	confidence := clamp01(top.Score / maxFloat(total, 0.25))
	if len(state.changeLinks) > 0 && top.Family == SceneFamilyChangeInduced {
		confidence = clamp01(confidence + 0.08)
	}
	if len(state.security.StructuredFindings) > 0 && top.Family == SceneFamilySecurityOrProcessAnomaly {
		confidence = clamp01(confidence + 0.08)
	}
	if len(state.gpu.Metrics) > 0 && (top.Family == SceneFamilyGPUInference || top.Family == SceneFamilyGPUTrainingOrCollective) {
		confidence = clamp01(confidence + 0.08)
	}

	candidates := make([]string, 0, minInt(len(scores), 3))
	for _, score := range scores {
		if score.Score <= 0 {
			continue
		}
		if len(score.Subscenes) == 0 {
			candidates = append(candidates, string(score.Family))
		} else {
			candidates = append(candidates, score.Subscenes...)
		}
		if len(candidates) >= 4 {
			break
		}
	}

	return SceneClassification{
		SceneFamily:        top.Family,
		Confidence:         applyTelemetryConfidenceLimit(maxFloat(confidence, 0.35), state.telemetryQuality),
		CandidateSubscenes: dedupeStrings(candidates),
		MissingEvidence:    dedupeStrings(top.Missing),
		CollectionHints:    dedupeStrings(top.Hints),
	}
}

func sceneScoresFromState(state *workflowState) []sceneScore {
	scores := make(map[SceneFamily]*sceneScore, 10)
	add := func(family SceneFamily, delta float64, subscene string) {
		entry, ok := scores[family]
		if !ok {
			entry = &sceneScore{
				Family: family,
				Hints:  sceneCollectionHints(family),
			}
			scores[family] = entry
		}
		entry.Score += delta
		if strings.TrimSpace(subscene) != "" {
			entry.Subscenes = append(entry.Subscenes, subscene)
		}
	}

	for _, signal := range state.riskSignals {
		text := strings.ToLower(strings.TrimSpace(signal.Name + " " + signal.ID + " " + signal.Entity))
		score := maxFloat(signal.Score, 0.05)
		switch {
		case strings.Contains(text, "oom"), strings.Contains(text, "memory"), strings.Contains(text, "cpu"), strings.Contains(text, "pressure"):
			add(SceneFamilyResourceContention, score, signal.Name)
		case strings.Contains(text, "retrans"), strings.Contains(text, "timeout"), strings.Contains(text, "dns"), strings.Contains(text, "connect"), strings.Contains(text, "socket"):
			add(SceneFamilyNetworkConnectivity, score, signal.Name)
		case strings.Contains(text, "io"), strings.Contains(text, "disk"), strings.Contains(text, "storage"), strings.Contains(text, "fsync"):
			add(SceneFamilyStorageIO, score, signal.Name)
		case strings.Contains(text, "gpu"), strings.Contains(text, "cuda"), strings.Contains(text, "nvidia"):
			add(SceneFamilyGPUInference, score, signal.Name)
		case strings.Contains(text, "irq"), strings.Contains(text, "softirq"), strings.Contains(text, "kernel"):
			add(SceneFamilyBareMetalKernelOrIRQ, score, signal.Name)
		case strings.Contains(text, "latency"), strings.Contains(text, "query"), strings.Contains(text, "transaction"), strings.Contains(text, "db"):
			add(SceneFamilyDatabaseLikeLatencyPath, score, signal.Name)
		}
	}

	for _, link := range state.changeLinks {
		score := maxFloat(link.CorrelationScore, 0.12)
		category := strings.ToLower(strings.TrimSpace(link.Category + " " + link.Summary + " " + link.HypothesisHint))
		add(SceneFamilyChangeInduced, score, firstNonEmpty(link.HypothesisHint, link.Summary))
		switch {
		case strings.Contains(category, "deploy"), strings.Contains(category, "rollout"), strings.Contains(category, "revision"):
			add(SceneFamilyDeploymentRollout, score+0.08, firstNonEmpty(link.Summary, link.Category))
		case strings.Contains(category, "config"), strings.Contains(category, "feature"):
			add(SceneFamilyChangeInduced, score+0.04, firstNonEmpty(link.Summary, link.Category))
		case strings.Contains(category, "driver"):
			add(SceneFamilyBareMetalKernelOrIRQ, score, firstNonEmpty(link.Summary, link.Category))
		}
	}

	for _, finding := range state.security.StructuredFindings {
		score := maxFloat(finding.Confidence, 0.14)
		add(SceneFamilySecurityOrProcessAnomaly, score, firstNonEmpty(finding.Category, finding.Summary))
	}
	if len(state.security.Findings) > 0 {
		add(SceneFamilySecurityOrProcessAnomaly, 0.18, "security_findings")
	}

	for _, event := range state.ebpf.RuntimeEvents {
		text := strings.ToLower(strings.TrimSpace(event.Category + " " + event.Type + " " + event.Description))
		score := maxFloat(event.Confidence, 0.08)
		switch {
		case strings.Contains(text, "connect"), strings.Contains(text, "bind"), strings.Contains(text, "dns"), strings.Contains(text, "tcp"):
			add(SceneFamilyNetworkConnectivity, score, firstNonEmpty(event.Type, event.Category))
		case strings.Contains(text, "exec"), strings.Contains(text, "fork"), strings.Contains(text, "security"):
			add(SceneFamilySecurityOrProcessAnomaly, score, firstNonEmpty(event.Type, event.Category))
		case strings.Contains(text, "irq"), strings.Contains(text, "softirq"), strings.Contains(text, "kernel"), strings.Contains(text, "sched"):
			add(SceneFamilyBareMetalKernelOrIRQ, score, firstNonEmpty(event.Type, event.Category))
		}
	}

	if len(state.gpu.Metrics) > 0 {
		score := 0.20
		hints := []string{}
		for name, value := range state.gpu.Metrics {
			lower := strings.ToLower(strings.TrimSpace(name))
			switch {
			case strings.Contains(lower, "util"), value >= 70:
				score += 0.06
				hints = append(hints, fmt.Sprintf("%s=%.1f", name, value))
			case strings.Contains(lower, "mem"), value >= 80:
				score += 0.05
				hints = append(hints, fmt.Sprintf("%s=%.1f", name, value))
			case strings.Contains(lower, "collective"), strings.Contains(lower, "nccl"), strings.Contains(lower, "allreduce"):
				add(SceneFamilyGPUTrainingOrCollective, 0.26, name)
			}
		}
		add(SceneFamilyGPUInference, score, strings.Join(hints, ", "))
		if len(state.gpu.TopProcesses) > 1 {
			add(SceneFamilyGPUTrainingOrCollective, 0.18, "multi_gpu_process_set")
		}
	}

	if strings.Contains(strings.ToLower(state.topoData.Snapshot.Summary), "kubernetes") {
		add(SceneFamilyKubernetesWorkload, 0.16, "topology_kubernetes")
	}
	for _, deploy := range state.logsData.RecentDeploys {
		lower := strings.ToLower(strings.TrimSpace(deploy))
		switch {
		case strings.Contains(lower, "rollout"), strings.Contains(lower, "deployment"):
			add(SceneFamilyDeploymentRollout, 0.18, deploy)
		case strings.Contains(lower, "pod"), strings.Contains(lower, "container"):
			add(SceneFamilyKubernetesWorkload, 0.12, deploy)
		}
	}
	for _, snippet := range state.logsData.Snippets {
		lower := strings.ToLower(strings.TrimSpace(snippet))
		switch {
		case strings.Contains(lower, "crashloop"), strings.Contains(lower, "pod"), strings.Contains(lower, "container"):
			add(SceneFamilyKubernetesWorkload, 0.10, truncateString(snippet, 80))
		case strings.Contains(lower, "database"), strings.Contains(lower, "query"), strings.Contains(lower, "transaction"), strings.Contains(lower, "replica lag"):
			add(SceneFamilyDatabaseLikeLatencyPath, 0.12, truncateString(snippet, 80))
		case strings.Contains(lower, "timeout"), strings.Contains(lower, "connection reset"), strings.Contains(lower, "no route"):
			add(SceneFamilyNetworkConnectivity, 0.10, truncateString(snippet, 80))
		}
	}

	if len(scores) == 0 {
		add(SceneFamilyResourceContention, 0.20, "fallback_resource_pressure")
	}

	out := make([]sceneScore, 0, len(scores))
	for family, score := range scores {
		score.Hints = dedupeStrings(score.Hints)
		score.Subscenes = dedupeStrings(score.Subscenes)
		score.Missing = sceneMissingEvidence(family, state)
		out = append(out, *score)
	}
	return out
}

func sceneCollectionHints(scene SceneFamily) []string {
	switch scene {
	case SceneFamilyChangeInduced:
		return []string{"check rollout adjacency", "compare revision and config drift", "prefer narrow rollback evidence"}
	case SceneFamilyNetworkConnectivity:
		return []string{"sample connectivity at a shorter interval", "filter timeout and retransmit logs", "correlate with topology neighbors"}
	case SceneFamilyStorageIO:
		return []string{"increase IO sampling cadence briefly", "collect storage pressure signals", "keep process attribution enabled"}
	case SceneFamilyGPUInference:
		return []string{"capture GPU bottleneck metrics", "keep top process attribution", "prefer read-only diagnostic depth"}
	case SceneFamilyGPUTrainingOrCollective:
		return []string{"collect collective or interconnect indicators", "keep GPU process sets and log filters", "bound overhead with a short TTL"}
	case SceneFamilyDeploymentRollout:
		return []string{"restrict recollection to rollout window", "collect pod and revision context", "prefer proposal-only containment"}
	case SceneFamilyKubernetesWorkload:
		return []string{"collect pod restart and revision context", "scope queries to impacted workload", "keep runtime filters narrow"}
	case SceneFamilyBareMetalKernelOrIRQ:
		return []string{"increase runtime-event detail briefly", "collect scheduler or IRQ evidence", "avoid destructive actions"}
	case SceneFamilyDatabaseLikeLatencyPath:
		return []string{"collect latency, queueing, and storage hints", "filter database-like error patterns", "correlate with recent changes"}
	case SceneFamilySecurityOrProcessAnomaly:
		return []string{"collect process lineage and runtime graph evidence", "tighten security event filters", "default to escalation on weak rollback"}
	default:
		return []string{"collect process attribution", "recheck resource pressure with a bounded interval", "keep collection read-only and replayable"}
	}
}

func sceneMissingEvidence(scene SceneFamily, state *workflowState) []string {
	if state == nil {
		return []string{"scene_context_unavailable"}
	}
	gaps := make([]string, 0, 6)
	if len(state.metricsData.History) < 3 {
		gaps = append(gaps, "metric_baseline")
	}
	if len(state.logsData.Snippets) == 0 && state.logsData.Errors+state.logsData.Warnings == 0 {
		gaps = append(gaps, "recent_logs")
	}
	switch scene {
	case SceneFamilyChangeInduced, SceneFamilyDeploymentRollout:
		if len(state.changeLinks) == 0 {
			gaps = append(gaps, "change_correlation")
		}
	case SceneFamilyNetworkConnectivity:
		if len(state.ebpf.RuntimeEvents) == 0 {
			gaps = append(gaps, "runtime_network_events")
		}
	case SceneFamilyStorageIO:
		if !hasMetricLike(state, "io") && !hasMetricLike(state, "disk") {
			gaps = append(gaps, "io_pressure")
		}
	case SceneFamilyGPUInference, SceneFamilyGPUTrainingOrCollective:
		if len(state.gpu.Metrics) == 0 {
			gaps = append(gaps, "gpu_metrics")
		}
	case SceneFamilyKubernetesWorkload:
		if len(state.topoData.Snapshot.Nodes) == 0 {
			gaps = append(gaps, "workload_topology")
		}
	case SceneFamilyBareMetalKernelOrIRQ:
		if len(state.ebpf.RuntimeEvents) == 0 {
			gaps = append(gaps, "kernel_runtime_events")
		}
	case SceneFamilyDatabaseLikeLatencyPath:
		if !hasMetricLike(state, "latency") {
			gaps = append(gaps, "latency_path_metrics")
		}
	case SceneFamilySecurityOrProcessAnomaly:
		if len(state.lineage.Paths) == 0 {
			gaps = append(gaps, "process_lineage")
		}
	}
	gaps = append(gaps, state.telemetryQuality.BlindSpots...)
	return dedupeStrings(gaps)
}
