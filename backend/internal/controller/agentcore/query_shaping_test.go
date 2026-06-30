package agent

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestShapeToolQueryRefinesScopeFromRuntimeState(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 40 * time.Minute})
	state.adaptiveScopeHints = []string{"service:checkout", "pod:checkout-v2"}
	contract, ok := engine.tools.contracts.Get(ToolConnectivityCheck)
	require.True(t, ok)

	shaped := shapeToolQuery(state, contract, buildToolSelectionContext(state, adaptiveRuntimeStage, nil))
	require.Equal(t, "service:checkout,pod:checkout-v2", shaped.Query["scope"])
	require.Equal(t, "20m0s", shaped.Query["window"])
}

func TestShapeToolQueryUsesRecommendedWindowRefinement(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 40 * time.Minute})
	state.adaptiveNormalizedResults = append(state.adaptiveNormalizedResults, NormalizedToolResult{
		RecommendedTimeWindowRefine: "12m",
	})
	contract, ok := engine.tools.contracts.Get(ToolLogs)
	require.True(t, ok)

	shaped := shapeToolQuery(state, contract, buildToolSelectionContext(state, adaptiveRuntimeStage, nil))
	require.Equal(t, "12m", shaped.Query["window"])
}

func TestShapeToolQueryInjectsSceneSpecificFilters(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 30 * time.Minute})
	state.sceneClassification = SceneClassification{SceneFamily: SceneFamilyDeploymentRollout}
	contract, ok := engine.tools.contracts.Get(ToolLogs)
	require.True(t, ok)

	context := buildToolSelectionContext(state, adaptiveRuntimeStage, nil)
	context.SceneFamily = SceneFamilyDeploymentRollout
	shaped := shapeToolQuery(state, contract, context)
	require.Contains(t, shaped.Query["event_filters"], "rollout")
}

func TestShapeToolQueryNarrowsValidationQueryAfterToolResults(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 30 * time.Minute})
	state.adaptiveNormalizedResults = append(state.adaptiveNormalizedResults, NormalizedToolResult{
		RecommendedScopeRefinement:  []string{"service:payments"},
		RecommendedTimeWindowRefine: "15m",
		LikelyNextChecks:            []string{"dns lookup", "timeout"},
	})
	target := ValidationTarget{
		Type:         ValidationTargetHypothesis,
		Title:        "payment dependency failures",
		Summary:      "validate service timeout evidence",
		ReadOnly:     true,
		EvidenceGaps: []string{"dns health"},
		ToolFamilies: []string{"network"},
	}
	contract, ok := engine.tools.contracts.Get(ToolDNSCheck)
	require.True(t, ok)

	shaped := shapeToolQuery(state, contract, buildToolSelectionContext(state, "validation_action_react_loop", &target))
	require.Equal(t, "service:payments", shaped.Query["scope"])
	require.Equal(t, "15m", shaped.Query["window"])
	require.Contains(t, shaped.Query["event_filters"], "timeout")
}
