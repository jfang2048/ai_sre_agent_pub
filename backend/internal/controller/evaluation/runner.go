package evaluation

import (
	"context"
	"math"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
)

// RunReplay executes the golden evaluation twice and scores deterministic replay stability.
func RunReplay(ctx context.Context, opts ReplayOptions) (ReplayReport, error) {
	scope := opts.Scope
	if scope == "" {
		scope = eval.ScopeFast
	}
	first, err := eval.Run(ctx, eval.RunOptions{
		Scope:    scope,
		RepoRoot: opts.RepoRoot,
	})
	if err != nil {
		return ReplayReport{}, err
	}
	second, err := eval.Run(ctx, eval.RunOptions{
		Scope:    scope,
		RepoRoot: opts.RepoRoot,
	})
	if err != nil {
		return ReplayReport{}, err
	}

	workflowDrift := map[string]float64{
		"root_cause_accuracy_at_1":  absDiff(first.Workflow.RootCauseAccuracyAt1, second.Workflow.RootCauseAccuracyAt1),
		"root_cause_accuracy_at_3":  absDiff(first.Workflow.RootCauseAccuracyAt3, second.Workflow.RootCauseAccuracyAt3),
		"recommendation_safety":     absDiff(first.Workflow.RecommendationSafety, second.Workflow.RecommendationSafety),
		"governance_coverage":       absDiff(first.Workflow.GovernanceCoverage, second.Workflow.GovernanceCoverage),
		"verification_coverage":     absDiff(first.Workflow.VerificationCoverage, second.Workflow.VerificationCoverage),
		"durable_run_coverage":      absDiff(first.Workflow.DurableRunCoverage, second.Workflow.DurableRunCoverage),
		"evidence_package_coverage": absDiff(first.Workflow.EvidencePackageCoverage, second.Workflow.EvidencePackageCoverage),
		"memory_writeback_coverage": absDiff(first.Workflow.MemoryWritebackCoverage, second.Workflow.MemoryWritebackCoverage),
	}
	anomalyDrift := map[string]float64{
		"accuracy":            absDiff(first.Anomaly.Accuracy, second.Anomaly.Accuracy),
		"precision":           absDiff(first.Anomaly.Precision, second.Anomaly.Precision),
		"recall":              absDiff(first.Anomaly.Recall, second.Anomaly.Recall),
		"f1":                  absDiff(first.Anomaly.F1, second.Anomaly.F1),
		"false_positive_rate": absDiff(first.Anomaly.FalsePositiveRate, second.Anomaly.FalsePositiveRate),
		"false_negative_rate": absDiff(first.Anomaly.FalseNegativeRate, second.Anomaly.FalseNegativeRate),
	}
	retrievalDrift := map[string]float64{
		"recall_at_k":      absDiff(first.Retrieval.RecallAtK, second.Retrieval.RecallAtK),
		"precision_at_k":   absDiff(first.Retrieval.PrecisionAtK, second.Retrieval.PrecisionAtK),
		"noise_robustness": absDiff(first.Retrieval.NoiseRobustness, second.Retrieval.NoiseRobustness),
		"intent_accuracy":  absDiff(first.Retrieval.IntentAccuracy, second.Retrieval.IntentAccuracy),
	}

	maxDrift := 0.0
	for _, drift := range workflowDrift {
		if drift > maxDrift {
			maxDrift = drift
		}
	}
	for _, drift := range anomalyDrift {
		if drift > maxDrift {
			maxDrift = drift
		}
	}
	for _, drift := range retrievalDrift {
		if drift > maxDrift {
			maxDrift = drift
		}
	}
	stability := 1.0 - clamp01(maxDrift)
	return ReplayReport{
		GeneratedAt:    time.Now().UTC(),
		Scope:          scope,
		Stable:         stability >= 0.999 && first.Passed == second.Passed,
		StabilityScore: stability,
		First:          first,
		Second:         second,
		AnomalyDrift:   anomalyDrift,
		WorkflowDrift:  workflowDrift,
		RetrievalDrift: retrievalDrift,
	}, nil
}

func absDiff(a, b float64) float64 {
	return math.Abs(a - b)
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}
