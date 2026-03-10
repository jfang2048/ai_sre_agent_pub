package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/securityaudit"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDemoSecurityFindingsNonEmpty(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	SeedDemoData(store, index)

	evaluator := securityaudit.NewEvaluator(store, index)
	findings := evaluator.Findings(securityaudit.Options{
		CollectorID: "demo-web-1",
		Window:      90 * time.Minute,
		Limit:       200,
	})
	require.NotEmpty(t, findings)
	require.NotEmpty(t, findings[0].EvidenceID)
	require.NotZero(t, findings[0].Timestamp.UnixNano())
	require.NotEmpty(t, findings[0].NodeScope)
	require.Greater(t, findings[0].Confidence, 0.0)
}

func TestDemoJointRiskScorePositive(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	SeedDemoData(store, index)

	engine := NewWorkflowEngine(DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "demo-web-1",
		Window:      90 * time.Minute,
		Trigger:     "demo",
	})
	require.NoError(t, err)
	require.Greater(t, report.RiskScore, 0.0)
	require.NotEmpty(t, report.Signals)
	require.NotEmpty(t, report.ToolCalls)
}

func TestDemoRCAReportJSONSchema(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	SeedDemoData(store, index)

	engine := NewWorkflowEngine(DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	report, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "demo-web-1",
		Window:      90 * time.Minute,
		Trigger:     "demo",
	})
	require.NoError(t, err)

	raw, err := json.Marshal(report)
	require.NoError(t, err)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Contains(t, payload, "workflow_id")
	require.Contains(t, payload, "pipeline_version")
	require.Contains(t, payload, "incident_id")
	require.Contains(t, payload, "context")
	require.Contains(t, payload, "evidence")
	require.Contains(t, payload, "hypotheses")
	require.Contains(t, payload, "structured_report")
	require.Contains(t, payload, "agent_loop")

	hypotheses, ok := payload["hypotheses"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, hypotheses)
}
