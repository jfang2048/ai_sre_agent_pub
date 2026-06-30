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
			"anomaly: cases=%d passed=%d accuracy=%.2f precision=%.2f recall=%.2f f1=%.2f fpr=%.2f fnr=%.2f disposition_accuracy=%.2f",
			r.Anomaly.CasesRun,
			r.Anomaly.CasesPassed,
			r.Anomaly.Accuracy,
			r.Anomaly.Precision,
			r.Anomaly.Recall,
			r.Anomaly.F1,
			r.Anomaly.FalsePositiveRate,
			r.Anomaly.FalseNegativeRate,
			r.Anomaly.DispositionAccuracy,
		),
		fmt.Sprintf(
			"workflow: cases=%d passed=%d rca@1=%.2f rca@3=%.2f fault_domain=%.2f evidence=%.2f trajectory=%.2f query_path=%.2f rec_correct=%.2f rec_safe=%.2f grounded_cmd=%.2f rag_improvement=%.2f governance=%.2f verify=%.2f handoff=%.2f validation=%.2f validation_loops=%.2f durable=%.2f evidence_pkg=%.2f memory=%.2f",
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
			r.Workflow.GovernanceCoverage,
			r.Workflow.VerificationCoverage,
			r.Workflow.AnalysisHandoffCoverage,
			r.Workflow.ValidationReportCoverage,
			r.Workflow.ValidationLoopCoverage,
			r.Workflow.DurableRunCoverage,
			r.Workflow.EvidencePackageCoverage,
			r.Workflow.MemoryWritebackCoverage,
		),
	}
	if r.Anomaly.ExplanationJudge != nil {
		lines = append(lines, fmt.Sprintf(
			"anomaly_judge: cases=%d passed=%d agreement=%.2f avg_score=%.2f",
			r.Anomaly.ExplanationJudge.CasesRun,
			r.Anomaly.ExplanationJudge.CasesPassed,
			r.Anomaly.ExplanationJudge.AgreementRate,
			r.Anomaly.ExplanationJudge.AverageScore,
		))
	}

	if len(r.Retrieval.FailedCaseIDs) > 0 {
		lines = append(lines, "", "retrieval failures:")
		for _, item := range r.Retrieval.Cases {
			if !item.Passed {
				lines = append(lines, fmt.Sprintf("- %s: %s", item.ID, strings.Join(item.Failures, "; ")))
			}
		}
	}
	if len(r.Anomaly.FailedCaseIDs) > 0 {
		lines = append(lines, "", "anomaly failures:")
		for _, item := range r.Anomaly.Cases {
			if !item.Passed {
				lines = append(lines, fmt.Sprintf("- %s: %s", item.ID, strings.Join(item.Failures, "; ")))
			}
		}
	}
	if r.Anomaly.ExplanationJudge != nil && len(r.Anomaly.ExplanationJudge.FailedCaseIDs) > 0 {
		lines = append(lines, "", "anomaly judge failures:")
		for _, item := range r.Anomaly.ExplanationJudge.Cases {
			if !item.Passed {
				msg := item.Rationale
				if strings.TrimSpace(item.Error) != "" {
					msg = item.Error
				}
				lines = append(lines, fmt.Sprintf("- %s: %s", item.ID, msg))
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
