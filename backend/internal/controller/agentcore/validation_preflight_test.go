package agent

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type preflightTestTopology struct{}

func (preflightTestTopology) Snapshot(context.Context) TopologySnapshot {
	return TopologySnapshot{Edges: []TopologyEdge{{Source: "checkout", Target: "payments", Kind: "calls"}}}
}

func TestRemediationValidationPlansReadOnlyPreflight(t *testing.T) {
	tools := suggestedToolsForTargetFamilies(ValidationTargetRemediation, []string{"gpu", "kubernetes", "change"})
	require.GreaterOrEqual(t, len(tools), 6)
	require.Equal(t, ToolOperatorPreflight, tools[0])
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

func TestOperatorPreflightDistinguishesFreshEvidenceFromMissingChecks(t *testing.T) {
	now := time.Now().UTC()
	store := ingest.NewMemoryStore()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "host-a", Labels: []*telemetryv1.Label{
		{Key: "service", Value: "checkout"}, {Key: "namespace", Value: "shop"},
		{Key: "workload", Value: "checkout"}, {Key: "revision", Value: "42"},
	}}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{{Name: "service_error_rate", Value: 0.04, TimestampUnixNano: now.UnixNano()}}, now)
	tool := &operatorPreflightTool{store: store, topology: preflightTestTopology{}, maxAge: 2 * time.Minute}
	result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
	require.NoError(t, err)
	data, ok := result.Data.(operatorPreflightData)
	require.True(t, ok)
	require.True(t, data.Fresh)
	require.True(t, data.HealthAvailable)
	require.Equal(t, "checkout", data.Identity.Service)
	require.Equal(t, "42", data.Revision.Revision)
	require.Contains(t, data.Impact.Downstream, "payments")
	require.Contains(t, data.EvidenceGaps, "live replicas and disruption budget require separate operator verification")
	require.NotEmpty(t, data.OperatorChecks)
	require.True(t, describeWorkflowTool(tool).ReadOnly)
	replayed, ok := decodeWorkflowToolPayload(ToolOperatorPreflight, marshalToolResultPayload(data))
	require.True(t, ok)
	require.Equal(t, data.Identity.Service, replayed.(operatorPreflightData).Identity.Service)
}

func TestOperatorPreflightDoesNotCallMissingTelemetryHealthy(t *testing.T) {
	store := ingest.NewMemoryStore()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-stale"}, time.Now().Add(-10*time.Minute))
	tool := &operatorPreflightTool{store: store, maxAge: time.Minute}
	result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: "collector-stale"})
	require.NoError(t, err)
	data := result.Data.(operatorPreflightData)
	require.False(t, data.Fresh)
	require.False(t, data.HealthAvailable)
	require.False(t, data.Health.Healthy)
	require.Contains(t, data.EvidenceGaps, "collector snapshot is missing a timestamp or is stale")
	require.Contains(t, data.EvidenceGaps, "service health metrics unavailable")
}

func TestOperatorPreflightCandidateOnlyForRemediation(t *testing.T) {
	engine := NewWorkflowEngineFromConfig(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	base := ToolSelectionContext{Stage: "validation_action_react_loop", ReadOnlyOnly: true}
	for _, candidate := range generateToolCandidates(state, base) {
		require.NotEqual(t, ToolOperatorPreflight, candidate.Tool)
	}
	base.Target = &ValidationTarget{Type: ValidationTargetRemediation, SuggestedTools: []ToolName{ToolOperatorPreflight}}
	base.PreferredTools = []ToolName{ToolOperatorPreflight}
	found := false
	for _, candidate := range generateToolCandidates(state, base) {
		found = found || candidate.Tool == ToolOperatorPreflight
	}
	require.True(t, found)
}
