package eval

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoldenEvaluationFast(t *testing.T) {
	report, err := Run(context.Background(), RunOptions{Scope: ScopeFast})
	require.NoError(t, err)
	require.True(t, report.Passed, report.Text())
	require.GreaterOrEqual(t, report.Retrieval.RecallAtK, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Retrieval.PrecisionAtK, 0.25, report.Text())
	require.GreaterOrEqual(t, report.Workflow.RootCauseAccuracyAt3, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.RecommendationSafety, 0.99, report.Text())
}

func TestGoldenEvaluationRegression(t *testing.T) {
	if os.Getenv("SRE_AGENT_EVAL_SCOPE") != string(ScopeRegression) && os.Getenv("SRE_AGENT_EVAL_SCOPE") != string(ScopeBenchmark) {
		t.Skip("set SRE_AGENT_EVAL_SCOPE=regression or benchmark to run the regression evaluation suite")
	}
	report, err := Run(context.Background(), RunOptions{Scope: ScopeRegression})
	require.NoError(t, err)
	require.True(t, report.Passed, report.Text())
	require.GreaterOrEqual(t, report.Retrieval.NoiseRobustness, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.RootCauseAccuracyAt1, 0.49, report.Text())
	require.GreaterOrEqual(t, report.Workflow.RecommendationCorrectness, 0.24, report.Text())
	require.GreaterOrEqual(t, report.Workflow.GroundedCommandRate, 0.99, report.Text())
}

func TestGoldenEvaluationBenchmark(t *testing.T) {
	if os.Getenv("SRE_AGENT_EVAL_SCOPE") != string(ScopeBenchmark) {
		t.Skip("set SRE_AGENT_EVAL_SCOPE=benchmark to run the benchmark evaluation suite")
	}
	report, err := Run(context.Background(), RunOptions{Scope: ScopeBenchmark})
	require.NoError(t, err)
	require.True(t, report.Passed, report.Text())
	require.GreaterOrEqual(t, report.Workflow.TrajectoryAccuracy, 0.80, report.Text())
	require.GreaterOrEqual(t, report.Workflow.GroundedCommandRate, 0.99, report.Text())
}
