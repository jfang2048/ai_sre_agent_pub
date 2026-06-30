package eval

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAnomalyJudgeDecisionStructuredWithinFencedJSON(t *testing.T) {
	raw := "Thought: compare explanations against the contract.\n```json\n{\"results\":[{\"id\":\"burst-clean\",\"pass\":true,\"score\":0.88,\"rationale\":\"label, disposition, and explanation line up\"},{\"id\":\"oom\",\"pass\":false,\"score\":0.25,\"rationale\":\"explanation understates the fault\"}]}\n```"

	decisions, err := parseAnomalyJudgeDecision(raw)
	require.NoError(t, err)
	require.Len(t, decisions, 2)
	require.True(t, decisions["burst-clean"].Pass)
	require.InDelta(t, 0.88, decisions["burst-clean"].Score, 0.01)
	require.Equal(t, "label, disposition, and explanation line up", decisions["burst-clean"].Rationale)
	require.False(t, decisions["oom"].Pass)
	require.InDelta(t, 0.25, decisions["oom"].Score, 0.01)
}

func TestRunAnomalyJudgeSuiteWithJudgeAggregatesResults(t *testing.T) {
	cases := []AnomalyCaseResult{
		{ID: "burst-clean", ExpectedLabel: "expected_recurring_burst", PredictedLabel: "expected_recurring_burst", ExpectedDisposition: "suppressed", PredictedDisposition: "suppressed", Explanation: "matches recurring history"},
		{ID: "oom", ExpectedLabel: "confirmed_anomaly", PredictedLabel: "confirmed_anomaly", ExpectedDisposition: "escalated", PredictedDisposition: "escalated", Explanation: "oom kill evidence present"},
	}

	report, err := runAnomalyJudgeSuiteWithJudge(context.Background(), cases, 0, 2, fakeAnomalyJudge{
		decisions: map[string]AnomalyJudgeCaseResult{
			"burst-clean": {ID: "burst-clean", Passed: true, Score: 0.92, Rationale: "good suppression explanation"},
			"oom":         {ID: "oom", Passed: false, Score: 0.41, Rationale: "explanation underplays fault severity"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, report.CasesRun)
	require.Equal(t, 1, report.CasesPassed)
	require.InDelta(t, 0.50, report.AgreementRate, 0.01)
	require.InDelta(t, (0.92+0.41)/2.0, report.AverageScore, 0.01)
	require.Equal(t, []string{"oom"}, report.FailedCaseIDs)
}

func TestRunAnomalyJudgeSuiteWithJudgeMarksMissingBatchResultAsFailure(t *testing.T) {
	cases := []AnomalyCaseResult{
		{ID: "burst-clean", ExpectedLabel: "expected_recurring_burst", PredictedLabel: "expected_recurring_burst", ExpectedDisposition: "suppressed", PredictedDisposition: "suppressed", Explanation: "matches recurring history"},
	}

	report, err := runAnomalyJudgeSuiteWithJudge(context.Background(), cases, 0, 1, fakeAnomalyJudge{
		decisions: map[string]AnomalyJudgeCaseResult{},
	})
	require.NoError(t, err)
	require.Equal(t, 1, report.CasesRun)
	require.Equal(t, 0, report.CasesPassed)
	require.Equal(t, []string{"burst-clean"}, report.FailedCaseIDs)
	require.Contains(t, report.Cases[0].Error, "did not return a decision")
}

type fakeAnomalyJudge struct {
	decisions map[string]AnomalyJudgeCaseResult
}

func (f fakeAnomalyJudge) JudgeBatch(_ context.Context, items []AnomalyCaseResult) ([]AnomalyJudgeCaseResult, error) {
	results := make([]AnomalyJudgeCaseResult, 0, len(items))
	for _, item := range items {
		result, ok := f.decisions[item.ID]
		if !ok {
			continue
		}
		result.ExpectedLabel = item.ExpectedLabel
		result.PredictedLabel = item.PredictedLabel
		result.ExpectedDisposition = item.ExpectedDisposition
		result.PredictedDisposition = item.PredictedDisposition
		result.Explanation = item.Explanation
		results = append(results, result)
	}
	if len(items) > 0 && len(results) == 0 && len(f.decisions) == 0 {
		return results, nil
	}
	if len(items) > 0 && len(results) == 0 {
		return nil, fmt.Errorf("missing decisions for batch")
	}
	return results, nil
}
