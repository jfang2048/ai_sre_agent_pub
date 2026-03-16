package agent

import (
	"fmt"
	"sort"
	"strings"
)

func topTriggeredTrend(items []TrendAssessment) (TrendAssessment, bool) {
	if len(items) == 0 {
		return TrendAssessment{}, false
	}
	ordered := append([]TrendAssessment(nil), items...)
	sort.Slice(ordered, func(i, j int) bool {
		if severityRank(ordered[i].Severity) != severityRank(ordered[j].Severity) {
			return severityRank(ordered[i].Severity) > severityRank(ordered[j].Severity)
		}
		if ordered[i].Triggered != ordered[j].Triggered {
			return ordered[i].Triggered
		}
		if ordered[i].Confidence != ordered[j].Confidence {
			return ordered[i].Confidence > ordered[j].Confidence
		}
		return ordered[i].LastObservedAt.After(ordered[j].LastObservedAt)
	})
	for _, item := range ordered {
		if item.Triggered || item.Confidence >= 0.55 {
			return item, true
		}
	}
	return TrendAssessment{}, false
}

func topWeakSignalCluster(items []JointRiskCooccurrence) (JointRiskCooccurrence, bool) {
	if len(items) == 0 {
		return JointRiskCooccurrence{}, false
	}
	ordered := append([]JointRiskCooccurrence(nil), items...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CombinedScore != ordered[j].CombinedScore {
			return ordered[i].CombinedScore > ordered[j].CombinedScore
		}
		if ordered[i].Correlation != ordered[j].Correlation {
			return ordered[i].Correlation > ordered[j].Correlation
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered[0], true
}

func checksForTrendAssessment(trend TrendAssessment) []string {
	probableCause := probableCauseForEvidence(trend.Display, trend.SeriesKey)
	out := append([]string{}, checksForHypothesis(probableCause)...)
	switch strings.ToLower(strings.TrimSpace(trend.SeriesKey)) {
	case "memory_pressure":
		out = append(out,
			"confirm whether growth is steady across consecutive samples instead of a single burst",
			"compare reclaim and swap activity before increasing limits or scaling",
		)
	case "io_latency", "io_pressure":
		out = append(out,
			"verify the trend against queue depth and the busiest disk or filesystem",
			"separate device saturation from a temporary flush or backup job",
		)
	case "retransmit_ratio", "softnet_drop":
		out = append(out,
			"confirm the trend on the same interface and time window before blaming the service",
			"check whether drops align with softnet squeeze or link oversubscription",
		)
	case "gpu_temperature", "gpu_memory_pressure", "gpu_utilization":
		out = append(out,
			"confirm whether the GPU trend is thermal, memory, or placement related",
			"validate the busiest device and workload before moving jobs",
		)
	default:
		out = append(out, "verify the trend against the most recent baseline window before escalating")
	}
	if hint := strings.TrimSpace(trend.OperatorHint); hint != "" {
		out = append(out, truncateString(hint, 160))
	}
	if forecast := strings.TrimSpace(trend.Forecast); forecast != "" {
		out = append(out, truncateString("forecast: "+forecast, 160))
	}
	return dedupeStrings(out)
}

func checksForSignalCluster(co JointRiskCooccurrence) []string {
	probableCause := probableCauseForEvidence(strings.Join(co.Signals, " "))
	out := append([]string{}, checksForHypothesis(probableCause)...)
	out = append(out,
		"verify that the same signals overlap in the same collector and time window",
		"confirm the shared entity or service is also the top offender before escalating",
	)
	explanation := strings.ToLower(strings.TrimSpace(co.Explanation + " " + co.ActionableCause))
	if strings.Contains(explanation, "deploy") || strings.Contains(explanation, "rollout") {
		out = append(out, "compare the weak-signal window against the latest rollout or config change")
	}
	if strings.Contains(explanation, "latency") || strings.Contains(explanation, "queue") {
		out = append(out, "check whether the same queue or latency signal appears in logs, topology, or process pressure")
	}
	return dedupeStrings(out)
}

func appendKnowledgeChecks(base []string, hit RetrievedDocumentEvidence, commandLimit, stepLimit int) []string {
	out := append([]string{}, base...)
	for i, command := range hit.Commands {
		if i >= commandLimit {
			break
		}
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		out = append(out, truncateString(fmt.Sprintf("run: %s", command), 180))
	}
	for i, step := range hit.RemediationSteps {
		if i >= stepLimit {
			break
		}
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		out = append(out, truncateString(step, 180))
	}
	return dedupeStrings(out)
}

func countInvestigationEventsByCategory(items []InvestigationEvent, category string) int {
	total := 0
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Category), strings.TrimSpace(category)) {
			total++
		}
	}
	return total
}

func topRecommendationSummary(items []WorkflowRecommendation) string {
	for _, item := range items {
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			return summary
		}
	}
	return ""
}
