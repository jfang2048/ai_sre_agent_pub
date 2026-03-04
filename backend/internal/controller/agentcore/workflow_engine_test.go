package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestEvaluateJointRiskDetectsCooccurrence(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-joint-risk")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-joint-risk",
		Window:      50 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.WorkflowID)
	require.Equal(t, "collector-joint-risk", report.CollectorID)
	require.NotEmpty(t, report.Signals)
	require.NotEmpty(t, report.ScopeRisks)
	require.NotEmpty(t, report.Series)
	require.NotEmpty(t, report.Recommendations)
	require.NotEmpty(t, report.Stages)
	require.NotEmpty(t, report.ToolCalls)
	require.True(t, strings.Contains(strings.ToLower(report.ActionableWhy), "co-occurred") || len(report.Cooccurrences) > 0)
	require.NotEqual(t, "", report.Insights.Mode)
}

func TestBuildRCAWorkflowProducesStructuredHypothesesAndEvidence(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-rca")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	report, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-rca",
		Window:      50 * time.Minute,
		Trigger:     "incident_alert",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.WorkflowID)
	require.Equal(t, "collector-rca", report.CollectorID)
	require.NotEmpty(t, report.Context.TopMetrics)
	require.NotEmpty(t, report.Hypotheses)
	require.NotEmpty(t, report.Evidence)
	require.NotEmpty(t, report.Recommendations)
	require.NotEmpty(t, report.ToolCalls)
	require.NotEmpty(t, report.Stages)
	require.NotEmpty(t, report.Reproducibility)
	require.NotEmpty(t, report.AgentLoop.PlanSteps)
	require.NotEmpty(t, report.StructuredReport.Timeline)
	require.NotEmpty(t, report.StructuredReport.SupportingSignals)

	hasEvidenceLinked := false
	for _, hypothesis := range report.Hypotheses {
		if len(hypothesis.EvidenceIDs) > 0 {
			hasEvidenceLinked = true
			break
		}
	}
	require.True(t, hasEvidenceLinked)

	audits := engine.AuditRecords(200, report.WorkflowID)
	require.NotEmpty(t, audits)
}

func TestPotentialRiskFindingsIncludesStructuredFields(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-potential-risk")

	cfg := DefaultWorkflowConfig()
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	err := engine.RefreshPotentialRiskFindings(context.Background(), WorkflowRequest{
		Window: 45 * time.Minute,
		Limit:  4,
	})
	require.NoError(t, err)

	findings := engine.PotentialRiskFindings(10, "collector-potential-risk")
	require.NotEmpty(t, findings)
	require.NotEmpty(t, findings[0].RiskSummary)
	require.NotEmpty(t, findings[0].ContributingSignals)
	require.NotEmpty(t, findings[0].Scope)
	require.NotEmpty(t, findings[0].SuggestedInvestigationSteps)
	require.Contains(t, strings.ToLower(findings[0].SuggestedInvestigationSteps[0]), "current")
}

func seedAgentWorkflowData(t *testing.T, store *ingest.MemoryStore, index *logindex.Index, collectorID string) {
	t.Helper()

	base := time.Now().Add(-55 * time.Minute).UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: collectorID + "-host"}, base)

	for i := 0; i < 44; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		cpu := 40.0 + float64(i)*1.15
		if cpu > 95 {
			cpu = 95
		}
		memPct := 60.0 + float64(i)*0.65
		ioLatencyMs := 5.0 + float64(i)*1.7
		retrans := 0.001 + float64(i)*0.0004
		if retrans > 0.03 {
			retrans = 0.03
		}
		softnet := float64(i) * 0.75
		ioPressure := 1.2 + float64(i)*0.32
		if ioPressure > 26 {
			ioPressure = 26
		}
		memTotal := float64(16 * 1024 * 1024 * 1024)
		memUsed := memTotal * (memPct / 100)

		store.StoreMetrics(collectorID, []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: cpu, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_MemTotal_bytes", Value: memTotal, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_Used_bytes", Value: memUsed, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_disk_request_latency_p99_seconds", Value: ioLatencyMs / 1000.0, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_tcp_retransmit_ratio", Value: retrans, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_softnet_dropped_per_second", Value: softnet, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_pressure_io_full_avg10", Value: ioPressure, TimestampUnixNano: ts.UnixNano()},
			{Name: "rca_net_process_connections", Value: 380 + float64(i), TimestampUnixNano: ts.UnixNano(), Labels: []*telemetryv1.Label{{Key: "pid", Value: "2100"}, {Key: "name", Value: "checkout-api"}}},
			{Name: "rca_memory_process_rss_bytes", Value: 350*1024*1024 + float64(i)*1024*1024, TimestampUnixNano: ts.UnixNano(), Labels: []*telemetryv1.Label{{Key: "pid", Value: "2100"}, {Key: "name", Value: "checkout-api"}, {Key: "pod_uid", Value: "pod-checkout-a"}, {Key: "job", Value: "checkout"}}},
		}, ts)

		if i%4 == 0 {
			store.StoreLogs(collectorID, []*telemetryv1.LogFingerprint{
				{Fingerprint: "timeout", Count: 2 + uint64(i/4), Example: "error timeout contacting payment service after deploy"},
				{Fingerprint: "permissions", Count: 1, Example: "warning weak permission chmod 777 in cache directory"},
			}, ts)
		}

		if index != nil {
			level := "info"
			message := "healthy request"
			count := uint64(1)
			if i%3 == 0 {
				level = "error"
				count = 2
				message = "timeout contacting payment dependency after rollout checkout-v2"
			}
			if i%6 == 0 {
				level = "warn"
				message = "weak permission warning world-writable cache directory"
			}
			index.AddBatch([]logindex.RawEvent{{
				Timestamp:   ts,
				CollectorID: collectorID,
				Hostname:    collectorID + "-host",
				Service:     "checkout",
				Process:     "checkout-api",
				PID:         "2100",
				Level:       level,
				Source:      "app",
				Message:     message,
				Count:       count,
			}})
		}
	}
}
