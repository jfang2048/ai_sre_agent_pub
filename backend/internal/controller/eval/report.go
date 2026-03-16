package eval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JSON renders the full report as formatted JSON.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// Text renders a compact human-readable summary suitable for CI logs.
func (r Report) Text() string {
	lines := []string{
		fmt.Sprintf("scope=%s passed=%t generated_at=%s", r.Scope, r.Passed, r.GeneratedAt.Format("2006-01-02T15:04:05Z")),
		"",
		fmt.Sprintf(
			"retrieval: cases=%d passed=%d recall@k=%.2f precision@k=%.2f context_precision=%.2f context_recall=%.2f signal_coverage=%.2f intent_accuracy=%.2f noise_robustness=%.2f avg_latency_ms=%.1f",
			r.Retrieval.CasesRun,
			r.Retrieval.CasesPassed,
			r.Retrieval.RecallAtK,
			r.Retrieval.PrecisionAtK,
			r.Retrieval.ContextPrecision,
			r.Retrieval.ContextRecall,
			r.Retrieval.SignalCoverage,
			r.Retrieval.IntentAccuracy,
			r.Retrieval.NoiseRobustness,
			r.Retrieval.AverageLatencyMS,
		),
		fmt.Sprintf(
			"workflow: cases=%d passed=%d rca@1=%.2f rca@3=%.2f fault_domain=%.2f evidence=%.2f trajectory=%.2f query_path=%.2f rec_correct=%.2f rec_safe=%.2f grounded_cmd=%.2f rag_improvement=%.2f",
			r.Workflow.CasesRun,
			r.Workflow.CasesPassed,
			r.Workflow.RootCauseAccuracyAt1,
			r.Workflow.RootCauseAccuracyAt3,
			r.Workflow.FaultDomainAccuracy,
			r.Workflow.EvidenceCoverage,
			r.Workflow.TrajectoryAccuracy,
			r.Workflow.QueryPathAccuracy,
			r.Workflow.RecommendationCorrectness,
			r.Workflow.RecommendationSafety,
			r.Workflow.GroundedCommandRate,
			r.Workflow.RAGImprovementRate,
		),
	}

	if len(r.Retrieval.FailedCaseIDs) > 0 {
		lines = append(lines, "", "retrieval failures:")
		for _, item := range r.Retrieval.Cases {
			if !item.Passed {
				lines = append(lines, fmt.Sprintf("- %s: %s", item.ID, strings.Join(item.Failures, "; ")))
			}
		}
	}
	if len(r.Workflow.FailedCaseIDs) > 0 {
		lines = append(lines, "", "workflow failures:")
		for _, item := range r.Workflow.Cases {
			if !item.Passed {
				lines = append(lines, fmt.Sprintf("- %s: %s", item.ID, strings.Join(item.Failures, "; ")))
			}
		}
	}
	return strings.Join(lines, "\n")
}
