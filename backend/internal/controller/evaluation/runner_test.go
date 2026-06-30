package evaluation

import (
	"context"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/eval"
	"github.com/stretchr/testify/require"
)

func TestRunReplayFastIsStable(t *testing.T) {
	report, err := RunReplay(context.Background(), ReplayOptions{Scope: eval.ScopeFast})
	require.NoError(t, err)
	require.Equal(t, report.First.Passed, report.Second.Passed)
	require.True(t, report.Stable)
	require.GreaterOrEqual(t, report.StabilityScore, 0.999)
	require.GreaterOrEqual(t, report.First.Workflow.GovernanceCoverage, 0.99)
	require.GreaterOrEqual(t, report.First.Workflow.VerificationCoverage, 0.99)
	require.GreaterOrEqual(t, report.First.Workflow.DurableRunCoverage, 0.99)
}
