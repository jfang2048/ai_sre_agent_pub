package agent

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/changeintel"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestFreshPostActionRiskRejectsOldHistoryDespiteFreshNodeSnapshot(t *testing.T) {
	now := time.Now().UTC()
	actionCompleted := now.Add(-time.Minute)
	metrics := &mockTool{
		name:          ToolMetrics,
		deterministic: true,
		result: workflowToolResult{Data: metricsToolData{
			CollectorID: "collector-a",
			Node: &ingest.NodeSnapshot{
				CollectorID:  "collector-a",
				LastMetricAt: now.Add(-10 * time.Second),
			},
			History: []ingest.MetricHistorySample{
				{Timestamp: now.Add(-3 * time.Minute), Metrics: map[string]float64{"node_cpu_usage_percent": 80}},
				{Timestamp: now.Add(-2 * time.Minute), Metrics: map[string]float64{"node_cpu_usage_percent": 75}},
				{Timestamp: now.Add(-90 * time.Second), Metrics: map[string]float64{"node_cpu_usage_percent": 50}},
			},
		}},
	}
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), nil, nil, zap.NewNop())
	engine.tools = newWorkflowToolManager(zap.NewNop(), metrics)
	parent := &workflowState{workflowID: "wf-stale-history", workflowType: "rca", collectorID: "collector-a"}

	_, _, err := engine.freshPostActionRisk(context.Background(), parent, actionCompleted)
	require.ErrorContains(t, err, "risk series predate the action")
	require.Equal(t, 1, metrics.runCount)

	data := metrics.result.Data.(metricsToolData)
	data.History = append(data.History, ingest.MetricHistorySample{
		Timestamp: now.Add(-5 * time.Second),
		Metrics:   map[string]float64{"node_cpu_usage_percent": 45},
	})
	metrics.result.Data = data
	_, _, err = engine.freshPostActionRisk(context.Background(), parent, actionCompleted)
	require.NoError(t, err)
	require.Equal(t, 2, metrics.runCount)
}

func TestPostActionRiskDoesNotScoreHistoricalChangeAsRecoveryEvidence(t *testing.T) {
	now := time.Now().UTC()
	actionCompleted := now.Add(-time.Minute)
	metrics := &mockTool{name: ToolMetrics, deterministic: true, result: workflowToolResult{Data: metricsToolData{
		CollectorID: "collector-a",
		Node:        &ingest.NodeSnapshot{CollectorID: "collector-a", LastMetricAt: now.Add(-5 * time.Second)},
		History: []ingest.MetricHistorySample{
			{Timestamp: now.Add(-3 * time.Minute), Metrics: map[string]float64{"node_cpu_usage_percent": 80}},
			{Timestamp: now.Add(-2 * time.Minute), Metrics: map[string]float64{"node_cpu_usage_percent": 75}},
			{Timestamp: now.Add(-90 * time.Second), Metrics: map[string]float64{"node_cpu_usage_percent": 50}},
			{Timestamp: now.Add(-5 * time.Second), Metrics: map[string]float64{"node_cpu_usage_percent": 45}},
		},
	}}}
	change := &mockTool{name: ToolChangeQuery, deterministic: true, result: workflowToolResult{Data: changeToolData{
		CollectorID: "collector-a", Events: []changeintel.CorrelatedChange{{
			Event:       changeintel.ChangeEvent{ChangeID: "old-deployment", CollectorID: "collector-a", StartedAt: now.Add(-10 * time.Minute)},
			ChangeScore: 1,
		}},
	}}}
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), nil, nil, zap.NewNop())
	engine.tools = newWorkflowToolManager(zap.NewNop(), metrics, change)
	parent := &workflowState{workflowID: "wf-old-change", workflowType: "rca", collectorID: "collector-a"}
	withOldChange, _, err := engine.freshPostActionRisk(context.Background(), parent, actionCompleted)
	require.NoError(t, err)
	change.result.Data = changeToolData{CollectorID: "collector-a"}
	withoutChange, _, err := engine.freshPostActionRisk(context.Background(), parent, actionCompleted)
	require.NoError(t, err)
	require.InDelta(t, withoutChange, withOldChange, 1e-9)
}
