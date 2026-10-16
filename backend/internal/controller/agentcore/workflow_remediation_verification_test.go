package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPostActionRiskNeedsNewAndRecentMetricSample(t *testing.T) {
	now := time.Now().UTC()
	actionCompleted := now.Add(-time.Minute)
	for _, tc := range []struct {
		name       string
		observedAt time.Time
		want       bool
	}{
		{name: "new recent sample", observedAt: now.Add(-30 * time.Second), want: true},
		{name: "sample at action completion", observedAt: actionCompleted},
		{name: "pre-action sample", observedAt: actionCompleted.Add(-time.Second)},
		{name: "sample too old", observedAt: now.Add(-3 * time.Minute)},
		{name: "future sample", observedAt: now.Add(time.Second)},
		{name: "missing sample"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, postActionRiskSampleFresh(tc.observedAt, actionCompleted, now, 2*time.Minute))
		})
	}
}

func TestPostActionRiskNeedsEveryScoredSeriesToHaveNewAndRecentSample(t *testing.T) {
	now := time.Now().UTC()
	actionCompleted := now.Add(-time.Minute)
	fresh := RiskSeries{Key: "cpu", Points: []RiskSeriesPoint{{Timestamp: now.Add(-20 * time.Second)}}}
	for _, tc := range []struct {
		name   string
		series []RiskSeries
		want   bool
	}{
		{name: "fresh metric series", series: []RiskSeries{fresh}, want: true},
		{name: "fresh metric and log series", series: []RiskSeries{fresh, {Key: "log_burst", Points: []RiskSeriesPoint{{Timestamp: now.Add(-10 * time.Second)}}}}, want: true},
		{name: "missing series"},
		{name: "empty series points", series: []RiskSeries{{Key: "cpu"}}},
		{name: "metric series predates action", series: []RiskSeries{{Key: "cpu", Points: []RiskSeriesPoint{{Timestamp: actionCompleted.Add(-time.Second)}}}}},
		{name: "log series predates action", series: []RiskSeries{fresh, {Key: "log_burst", Points: []RiskSeriesPoint{{Timestamp: actionCompleted.Add(-time.Second)}}}}},
		{name: "series exceeds maximum age", series: []RiskSeries{{Key: "cpu", Points: []RiskSeriesPoint{{Timestamp: now.Add(-3 * time.Minute)}}}}},
		{name: "series sample in future", series: []RiskSeries{{Key: "cpu", Points: []RiskSeriesPoint{{Timestamp: now.Add(time.Second)}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, postActionRiskSeriesFresh(tc.series, actionCompleted, now, 2*time.Minute))
		})
	}
}

func TestVerifyPlanStepNeverTreatsRemediationPlanAsVerifiedEffect(t *testing.T) {
	step := AgentPlanStep{ID: "plan-remediation", Tool: ToolRemediation}
	for _, mode := range []string{
		"planned_only",
		"proposal_only",
		"human_operator_required",
		"dry_run",
		"approved_execution_requested",
		"blocked_by_policy",
		"failed",
		"executed",
	} {
		t.Run(mode, func(t *testing.T) {
			result := workflowToolResult{
				Summary: "remediation tool returned " + mode,
				Data:    remediationToolData{Mode: mode, Summary: "remediation tool returned " + mode},
			}
			verified, note, _ := verifyPlanStep(step, result, &workflowState{})
			require.False(t, verified, "a remediation tool result alone cannot prove recovery")
			require.NotEmpty(t, note)
		})
	}
}

func TestRemediationModeOnlyExecutedLiveActionNeedsPostActionVerification(t *testing.T) {
	for _, tc := range []struct {
		name             string
		result           workflowToolResult
		dryRun           bool
		invocationStatus string
		wantMode         string
		wantCheck        bool
	}{
		{name: "executed live", result: workflowToolResult{Data: remediationToolData{Mode: "executed"}}, invocationStatus: "executed", wantMode: "executed", wantCheck: true},
		{name: "executed legacy result", result: workflowToolResult{Data: ActionResult{Status: "executed"}}, invocationStatus: "success", wantMode: "executed", wantCheck: true},
		{name: "cached executed", result: workflowToolResult{Data: remediationToolData{Mode: "executed"}}, invocationStatus: "skipped", wantMode: "executed"},
		{name: "missing execution receipt", result: workflowToolResult{Data: remediationToolData{Mode: "executed"}}, wantMode: "executed"},
		{name: "executed but dry run", result: workflowToolResult{Data: remediationToolData{Mode: "executed"}}, dryRun: true, wantMode: "executed"},
		{name: "dry run", result: workflowToolResult{Data: remediationToolData{Mode: "dry_run"}}, wantMode: "dry_run"},
		{name: "proposal only", result: workflowToolResult{Data: remediationToolData{Mode: "proposal_only"}}, wantMode: "proposal_only"},
		{name: "operator handoff", result: workflowToolResult{Data: remediationToolData{Mode: "human_operator_required"}}, wantMode: "human_operator_required"},
		{name: "planned only", result: workflowToolResult{Data: remediationToolData{Mode: "planned_only"}}, wantMode: "planned_only"},
		{name: "approval pending", result: workflowToolResult{Data: remediationToolData{Mode: "approved_execution_requested"}}, wantMode: "approved_execution_requested"},
		{name: "blocked", result: workflowToolResult{Data: remediationToolData{Mode: "blocked_by_policy"}}, wantMode: "blocked_by_policy"},
		{name: "failed", result: workflowToolResult{Data: remediationToolData{Mode: "failed"}}, wantMode: "failed"},
		{name: "unknown mode", result: workflowToolResult{Data: remediationToolData{Mode: "unknown"}}, wantMode: "unknown"},
		{name: "missing payload", result: workflowToolResult{}, wantMode: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := remediationModeFromResult(tc.result)
			require.Equal(t, tc.wantMode, mode)
			require.Equal(t, tc.wantCheck, requiresPostActionVerification(mode, tc.dryRun, tc.invocationStatus))
		})
	}
}

func TestGuardedRemediationCachedExecutionIsNotNewAction(t *testing.T) {
	data := remediationToolData{Mode: "executed"}
	require.Equal(t, "executed", remediationStepStatus(data, WorkflowToolCall{InvocationStatus: "executed"}))
	for _, status := range []string{"skipped", "failed", "blocked", ""} {
		require.Equal(t, "not_executed", remediationStepStatus(data, WorkflowToolCall{InvocationStatus: status}))
	}
	require.Equal(t, "not_executed", remediationStepStatus(data, WorkflowToolCall{InvocationStatus: "success", DryRun: true}))
}
