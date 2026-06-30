package evaluation

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// JSON renders the system-performance report as formatted JSON.
func (r SystemPerformanceReport) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Text renders a compact operator-facing summary suitable for local comparison.
func (r SystemPerformanceReport) Text() string {
	lines := []string{
		fmt.Sprintf(
			"system_perf: scope=%s variant=%s passed=%t overall=%.3f generated_at=%s",
			r.Scope,
			firstNonEmpty(strings.TrimSpace(r.Variant), "default"),
			r.Passed,
			r.Scorecard.OverallScore,
			r.GeneratedAt.Format("2006-01-02T15:04:05Z"),
		),
		fmt.Sprintf(
			"dimensions: correctness=%.3f closure=%.3f governance=%.3f efficiency=%.3f stability=%.3f collaboration=%.3f",
			r.Scorecard.Correctness,
			r.Scorecard.Closure,
			r.Scorecard.Governance,
			r.Scorecard.Efficiency,
			r.Scorecard.Stability,
			r.Scorecard.Collaboration,
		),
		fmt.Sprintf(
			"metrics: root_cause_top1=%.2f governance=%.2f handoff=%.2f validation=%.2f post_action=%.2f evidence=%.2f memory=%.2f replay=%.2f message_integrity=%.2f avg_latency_ms=%.1f tool_calls=%.1f token_cost=%.1f",
			r.Metrics.RootCauseTop1Rate,
			r.Metrics.GovernanceCoverage,
			r.Metrics.AnalysisHandoffCoverage,
			r.Metrics.ValidationReportCoverage,
			r.Metrics.PostActionValidationCoverage,
			r.Metrics.EvidencePackageCoverage,
			r.Metrics.MemoryWritebackCoverage,
			r.Metrics.ReplayStabilityScore,
			r.Metrics.MessageHistoryIntegrityScore,
			r.Metrics.EndToEndLatencyMS,
			r.Metrics.ToolCallCount,
			r.Metrics.TokenCost,
		),
	}
	if r.Comparison != nil {
		lines = append(lines, fmt.Sprintf(
			"compare: baseline=%s overall_delta=%.3f latency_delta_ms=%.1f governance_delta=%.3f collaboration_delta=%.3f",
			r.Comparison.BaselinePath,
			r.Comparison.ScoreDeltas["overall_score"],
			r.Comparison.LatencyDeltas["end_to_end_latency_ms"],
			r.Comparison.GovernanceDeltas["governance_coverage"],
			r.Comparison.CollaborationDeltas["message_history_integrity_score"],
		))
	}
	if len(r.FailedCaseIDs) > 0 {
		lines = append(lines, "", "system performance failures:")
		for _, item := range r.Cases {
			if !item.Passed {
				lines = append(lines, fmt.Sprintf("- %s: %s", item.ID, strings.Join(item.Failures, "; ")))
			}
		}
	}
	return strings.Join(lines, "\n")
}

func loadSystemPerformanceReport(path string) (SystemPerformanceReport, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SystemPerformanceReport{}, err
	}
	var report SystemPerformanceReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return SystemPerformanceReport{}, err
	}
	return report, nil
}
