package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemediationValidationPlansReadOnlyPreflight(t *testing.T) {
	tools := suggestedToolsForTargetFamilies(ValidationTargetRemediation, []string{"gpu", "kubernetes", "change"})
	require.GreaterOrEqual(t, len(tools), 6)
	require.Equal(t, ToolServiceHealth, tools[0])
	require.Contains(t, tools, ToolActionOutcome)
	require.Contains(t, tools, ToolNetworkBlastRadius)
	require.Contains(t, tools, ToolGPU)
	require.Contains(t, tools, ToolKubernetesResource)
	require.Contains(t, tools, ToolContainerRevision)
	require.Contains(t, tools, ToolDeploymentHistory)
	for _, tool := range tools {
		require.NotEqual(t, ToolRemediation, tool)
	}
}
