package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/securityaudit"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestPostActionRiskSourcesRequireActualEventTimes(t *testing.T) {
	now := time.Now().UTC()
	actionCompleted := now.Add(-time.Minute)
	fresh := now.Add(-10 * time.Second)
	old := actionCompleted.Add(-time.Second)
	for _, tc := range []struct {
		name     string
		security securityToolData
		ebpf     ebpfToolData
		wantErr  string
	}{
		{name: "no contributing events"},
		{name: "fresh security finding", security: securityToolData{Score: 0.5, SourceTimes: []time.Time{fresh}}},
		{name: "security source unknown", security: securityToolData{Score: 0.5}, wantErr: "security finding source time is unknown"},
		{name: "security source before action", security: securityToolData{Score: 0.5, SourceTimes: []time.Time{old}}, wantErr: "security finding predates the action"},
		{name: "one security source unknown", security: securityToolData{Score: 0.5, SourceTimes: []time.Time{fresh, {}}}, wantErr: "security finding source time is unknown"},
		{name: "fresh eBPF event", ebpf: ebpfToolData{BehaviorScore: 0.4, RuntimeEvents: []ingest.RuntimeSecurityEvent{{Timestamp: fresh, SourceTimestampKnown: true}}}},
		{name: "eBPF receipt time only", ebpf: ebpfToolData{BehaviorScore: 0.4, RuntimeEvents: []ingest.RuntimeSecurityEvent{{Timestamp: fresh}}}, wantErr: "eBPF event source time is unknown"},
		{name: "eBPF event before action", ebpf: ebpfToolData{BehaviorScore: 0.4, RuntimeEvents: []ingest.RuntimeSecurityEvent{{Timestamp: old, SourceTimestampKnown: true}}}, wantErr: "eBPF event predates the action"},
		{name: "eBPF score without events", ebpf: ebpfToolData{BehaviorScore: 0.4}, wantErr: "eBPF event source time is unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := postActionRiskSourcesFresh(tc.security, tc.ebpf, actionCompleted, now, 2*time.Minute)
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

func TestRiskSignalDoesNotInventSecurityEventTime(t *testing.T) {
	now := time.Now().UTC()
	security := securityToolData{Score: 0.8, SourceTimes: []time.Time{{}, now}, FindingIDs: []string{"finding"}}
	signals := buildRiskSignals("collector-a", nil, security, ebpfToolData{}, nil)
	require.NotEmpty(t, signals)
	var securitySignal *JointRiskSignal
	for i := range signals {
		if signals[i].ID == "security_exposure" {
			securitySignal = &signals[i]
			break
		}
	}
	require.NotNil(t, securitySignal)
	require.True(t, securitySignal.LastObservedAt.IsZero(), "unknown source time must remain unknown")
}

func TestSecurityFindingSourceTimeIgnoresReceiptTime(t *testing.T) {
	now := time.Now().UTC()
	sourceAt := now.Add(-time.Minute)
	store := ingest.NewMemoryStore()
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_security_finding", Value: 0.8, TimestampUnixNano: now.UnixNano(), Labels: []*telemetryv1.Label{{Key: "finding_id", Value: "known"}, {Key: "ts_unix_nano", Value: fmt.Sprint(sourceAt.UnixNano())}}},
		{Name: "node_security_finding", Value: 0.8, TimestampUnixNano: now.UnixNano(), Labels: []*telemetryv1.Label{{Key: "finding_id", Value: "receipt-only"}}},
	}, now)
	known := securityaudit.Finding{CollectorID: "collector-a", FindingID: "known", Source: "collector_security_audit", Timestamp: now}
	require.Equal(t, sourceAt, securityFindingSourceTimestamp(store, known))
	unknown := securityaudit.Finding{CollectorID: "collector-a", FindingID: "receipt-only", Source: "collector_security_audit", Timestamp: now}
	require.True(t, securityFindingSourceTimestamp(store, unknown).IsZero(), "finding timestamp may be a receive-time fallback")
}

func TestFreshPostActionRiskRejectsStaleSecuritySource(t *testing.T) {
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
	security := &mockTool{name: ToolSecurity, deterministic: true, result: workflowToolResult{Data: securityToolData{
		Score: 0.6, SourceTimes: []time.Time{actionCompleted.Add(-time.Second)},
	}}}
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), nil, nil, zap.NewNop())
	engine.tools = newWorkflowToolManager(zap.NewNop(), metrics, security)
	parent := &workflowState{workflowID: "wf-stale-security", workflowType: "rca", collectorID: "collector-a"}
	_, _, err := engine.freshPostActionRisk(context.Background(), parent, actionCompleted)
	require.ErrorContains(t, err, "security finding predates the action")
	security.result.Data = securityToolData{Score: 0.6, SourceTimes: []time.Time{now.Add(-5 * time.Second)}}
	_, _, err = engine.freshPostActionRisk(context.Background(), parent, actionCompleted)
	require.NoError(t, err)
	require.Equal(t, 2, metrics.runCount)
	require.Equal(t, 2, security.runCount)
}
