package eval

import (
	"context"
	"os"
	"testing"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/stretchr/testify/require"
)

func TestGoldenEvaluationFast(t *testing.T) {
	report, err := Run(context.Background(), RunOptions{Scope: ScopeFast})
	require.NoError(t, err)
	require.True(t, report.Passed, report.Text())
	require.GreaterOrEqual(t, report.Retrieval.RecallAtK, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Retrieval.PrecisionAtK, 0.25, report.Text())
	require.GreaterOrEqual(t, report.Anomaly.CasesRun, 12, report.Text())
	require.GreaterOrEqual(t, report.Anomaly.Accuracy, 0.85, report.Text())
	require.GreaterOrEqual(t, report.Anomaly.Precision, 0.80, report.Text())
	require.GreaterOrEqual(t, report.Anomaly.Recall, 0.80, report.Text())
	require.GreaterOrEqual(t, report.Anomaly.F1, 0.80, report.Text())
	require.GreaterOrEqual(t, report.Workflow.RootCauseAccuracyAt3, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.RecommendationSafety, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.GovernanceCoverage, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.DurableRunCoverage, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.AnalysisHandoffCoverage, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.ValidationReportCoverage, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.ValidationLoopCoverage, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.EvidencePackageCoverage, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.MemoryWritebackCoverage, 0.99, report.Text())
}

func TestGoldenEvaluationFastAnomalyConfusionMatrix(t *testing.T) {
	report, err := Run(context.Background(), RunOptions{Scope: ScopeFast})
	require.NoError(t, err)
	require.Equal(t, report.Anomaly.CasesRun, len(report.Anomaly.Cases))
	require.Len(t, report.Anomaly.ConfusionMatrix, 4)
	require.Len(t, report.Anomaly.PerClass, 4)

	total := 0
	for _, row := range report.Anomaly.ConfusionMatrix {
		rowTotal := 0
		for _, count := range row.Predictions {
			rowTotal += count
		}
		require.Greater(t, rowTotal, 0)
		total += rowTotal
	}
	require.Equal(t, report.Anomaly.CasesRun, total)

	for _, item := range report.Anomaly.Cases {
		require.NotEmpty(t, item.ExpectedLabel)
		require.NotEmpty(t, item.PredictedLabel)
		require.NotEmpty(t, item.ExpectedDisposition)
		require.NotEmpty(t, item.PredictedDisposition)
		require.NotEmpty(t, item.Explanation)
	}
}

func TestWorkflowEvaluationTracksTwoAgentArtifacts(t *testing.T) {
	report := agentcore.RCAWorkflowReport{
		AnalysisHandoff: agentcore.AnalysisHandoff{
			Agent:                      "analysis_agent",
			Hypotheses:                 []agentcore.RCAHypothesis{{ID: "hyp-1", Title: "recent rollout regression"}},
			SuggestedValidationTargets: []agentcore.ValidationTarget{{ID: "target-1", Type: agentcore.ValidationTargetHypothesis}},
		},
		Validation: agentcore.ValidationActionReport{
			Agent:       "validation_action_agent",
			Results:     []agentcore.ValidationTargetResult{{TargetID: "target-1", Verdict: agentcore.ValidationVerdictConfirmed}},
			LoopRecords: []agentcore.ValidationLoopRecord{{TargetID: "target-1", Iteration: 1, Tool: agentcore.ToolDeploymentHistory}},
		},
	}
	run := &agentcore.DurableRun{
		AnalysisHandoff: &agentcore.AnalysisHandoff{Agent: "analysis_agent"},
		Validation:      &agentcore.ValidationActionReport{Agent: "validation_action_agent"},
		ValidationLoops: []agentcore.ValidationLoopRecord{{TargetID: "target-1", Iteration: 1}},
	}

	require.True(t, analysisHandoffRecorded(report, run))
	require.True(t, validationReportRecorded(report, run))
	require.True(t, validationLoopsRecorded(report, run))
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
	require.GreaterOrEqual(t, report.Workflow.GovernanceCoverage, 0.99, report.Text())
	require.GreaterOrEqual(t, report.Workflow.VerificationCoverage, 0.99, report.Text())
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
	require.GreaterOrEqual(t, report.Workflow.DurableRunCoverage, 0.99, report.Text())
}
