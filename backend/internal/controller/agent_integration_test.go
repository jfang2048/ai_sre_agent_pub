package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/agent"
	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidents"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestParseAgentActionUpdatePayloadNormalizesStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/actions/a1", strings.NewReader(`{"status":" COMPLETED ","note":" done "}`))

	status, note, err := parseAgentActionUpdatePayload(req)
	require.NoError(t, err)
	require.Equal(t, agent.ActionStatusCompleted, status)
	require.Equal(t, "done", note)
}

func TestParseAgentActionUpdatePayloadRejectsInvalidStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/actions/a1", strings.NewReader(`{"status":"queued"}`))

	_, _, err := parseAgentActionUpdatePayload(req)
	require.Error(t, err)
}

func TestParseAgentActionUpdatePayloadRejectsUnknownFields(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/actions/a1", strings.NewReader(`{"status":"completed","extra":1}`))

	_, _, err := parseAgentActionUpdatePayload(req)
	require.Error(t, err)
}

func TestParseAgentActionUpdatePayloadRequiresStatusOrNote(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/actions/a1", strings.NewReader(`{"note":"   "}`))

	_, _, err := parseAgentActionUpdatePayload(req)
	require.Error(t, err)
}

func TestParseNodePath(t *testing.T) {
	node, err := parseNodePath("/api/v1/agent/reports/node-a/latest")
	require.NoError(t, err)
	require.Equal(t, "node-a", node)

	_, err = parseNodePath("/api/v1/agent/reports/node-a/sub")
	require.Error(t, err)

	_, err = parseNodePath("/api/v1/agent/reports/")
	require.Error(t, err)
}

func TestParseIncidentActionExecutePath(t *testing.T) {
	alertID, actionID, err := parseIncidentActionPath("alert-1/actions/action-1/execute", "execute")
	require.NoError(t, err)
	require.Equal(t, "alert-1", alertID)
	require.Equal(t, "action-1", actionID)

	_, _, err = parseIncidentActionPath("alert-1/context", "execute")
	require.Error(t, err)
}

func TestParseIncidentActionRollbackPath(t *testing.T) {
	alertID, actionID, err := parseIncidentActionPath("alert-1/actions/action-1/rollback", "rollback")
	require.NoError(t, err)
	require.Equal(t, "alert-1", alertID)
	require.Equal(t, "action-1", actionID)

	_, _, err = parseIncidentActionPath("alert-1/actions/action-1/execute", "rollback")
	require.Error(t, err)
}

func TestParseIncidentActionExecutePayload(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/a/actions/b/execute", strings.NewReader(`{"dry_run":false,"approval_token":"abc"}`))
	payload, err := parseIncidentActionExecutePayload(req)
	require.NoError(t, err)
	require.NotNil(t, payload.DryRun)
	require.False(t, *payload.DryRun)
	require.Equal(t, "abc", payload.ApprovalToken)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/a/actions/b/execute", strings.NewReader(`{"unexpected":1}`))
	_, err = parseIncidentActionExecutePayload(req)
	require.Error(t, err)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/a/actions/b/execute", http.NoBody)
	payload, err = parseIncidentActionExecutePayload(req)
	require.NoError(t, err)
	require.Nil(t, payload.DryRun)
	require.Empty(t, payload.ApprovalToken)
}

func TestParseIncidentActionRollbackPayload(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/a/actions/b/rollback", strings.NewReader(`{"dry_run":true,"approval_token":"abc","rollback_id":"rb-1"}`))
	payload, err := parseIncidentActionRollbackPayload(req)
	require.NoError(t, err)
	require.NotNil(t, payload.DryRun)
	require.True(t, *payload.DryRun)
	require.Equal(t, "abc", payload.ApprovalToken)
	require.Equal(t, "rb-1", payload.RollbackID)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/a/actions/b/rollback", strings.NewReader(`{"unexpected":1}`))
	_, err = parseIncidentActionRollbackPayload(req)
	require.Error(t, err)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/a/actions/b/rollback", http.NoBody)
	payload, err = parseIncidentActionRollbackPayload(req)
	require.NoError(t, err)
	require.Nil(t, payload.DryRun)
	require.Empty(t, payload.ApprovalToken)
	require.Empty(t, payload.RollbackID)
}

func TestHandleAgentIncidentActionExecute(t *testing.T) {
	engine := &agent.Engine{}
	ctx := incidents.AggregatedContext{
		IncidentID: "incident-1",
		AlertID:    "alert-1",
		Alert: incidents.InputAlert{
			ID:       "alert-1",
			Title:    "API timeout",
			Service:  "checkout",
			Severity: "critical",
			Labels:   map[string]string{"commit": "abc123"},
		},
		ResourceScope: []incidents.ResourceRef{
			{ID: "pod-a", Type: "pod", Name: "checkout-a"},
		},
		Metrics: []incidents.MetricFinding{
			{Scope: "node-a", Symptoms: []string{"CPU saturation"}},
		},
		Logs: []incidents.LogFinding{
			{Scope: "checkout", Matches: []incidents.LogMatch{{Example: "timeout on payment dependency"}}},
		},
	}
	assessment := engine.IngestIncidentContext(ctx)
	require.NotEmpty(t, assessment.AutomationPlan.Actions)

	controller := &Controller{agentEngine: engine}

	safeActionID := assessment.AutomationPlan.Actions[0].ID
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/alert-1/actions/"+safeActionID+"/execute", strings.NewReader(`{}`))
	resp := httptest.NewRecorder()
	controller.handleAgentIncidentByID(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	unsafeActionID := ""
	for _, action := range assessment.AutomationPlan.Actions {
		if action.RequiresApproval {
			unsafeActionID = action.ID
			break
		}
	}
	require.NotEmpty(t, unsafeActionID)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/alert-1/actions/"+unsafeActionID+"/execute", strings.NewReader(`{"dry_run":false}`))
	resp = httptest.NewRecorder()
	controller.handleAgentIncidentByID(resp, req)
	require.Equal(t, http.StatusPreconditionRequired, resp.Code)

	// audit list should be queryable
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agent/incidents/alert-1/actions/audit", nil)
	resp = httptest.NewRecorder()
	controller.handleAgentIncidentByID(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	// rollback on latest reversible action in dry-run mode
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/incidents/alert-1/actions/"+safeActionID+"/rollback", strings.NewReader(`{"dry_run":true}`))
	resp = httptest.NewRecorder()
	controller.handleAgentIncidentByID(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
}

func TestHandleAgentJointRiskAndRCAWorkflow(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedWorkflowTelemetry(t, store, index, "collector-risk-a")

	workflow := agentcore.NewWorkflowEngine(agentcore.DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	controller := &Controller{agentWorkflow: workflow}

	riskReq := httptest.NewRequest(http.MethodGet, "/api/v1/agent/joint-risk?collector_id=collector-risk-a&window=45m&limit=5", nil)
	riskResp := httptest.NewRecorder()
	controller.handleAgentJointRisk(riskResp, riskReq)
	require.Equal(t, http.StatusOK, riskResp.Code)

	var riskPayload struct {
		Reports []agentcore.JointRiskAssessment `json:"reports"`
		Count   int                             `json:"count"`
	}
	require.NoError(t, json.NewDecoder(riskResp.Body).Decode(&riskPayload))
	require.GreaterOrEqual(t, riskPayload.Count, 1)
	require.NotEmpty(t, riskPayload.Reports[0].Signals)
	require.NotEmpty(t, riskPayload.Reports[0].Recommendations)
	require.NotEmpty(t, riskPayload.Reports[0].Stages)

	rcaReq := httptest.NewRequest(http.MethodGet, "/api/v1/agent/rca?collector_id=collector-risk-a&window=45m&limit=5", nil)
	rcaResp := httptest.NewRecorder()
	controller.handleAgentRCAWorkflow(rcaResp, rcaReq)
	require.Equal(t, http.StatusOK, rcaResp.Code)

	var rcaPayload struct {
		Reports []agentcore.RCAWorkflowReport `json:"reports"`
		Count   int                           `json:"count"`
	}
	require.NoError(t, json.NewDecoder(rcaResp.Body).Decode(&rcaPayload))
	require.GreaterOrEqual(t, rcaPayload.Count, 1)
	require.NotEmpty(t, rcaPayload.Reports[0].Hypotheses)
	require.NotEmpty(t, rcaPayload.Reports[0].Evidence)
	require.NotEmpty(t, rcaPayload.Reports[0].Recommendations)
	require.NotEmpty(t, rcaPayload.Reports[0].ToolCalls)
	require.NotEmpty(t, rcaPayload.Reports[0].AgentLoop.PlanSteps)
	require.NotEmpty(t, rcaPayload.Reports[0].StructuredReport.Timeline)
	require.NotEmpty(t, rcaPayload.Reports[0].StructuredReport.SupportingSignals)
}

func TestHandleAgentPotentialRisks(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedWorkflowTelemetry(t, store, index, "collector-risk-insights")

	workflow := agentcore.NewWorkflowEngine(agentcore.DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	controller := &Controller{agentWorkflow: workflow}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/potential-risks?collector_id=collector-risk-insights&window=45m&limit=10&refresh=true", nil)
	resp := httptest.NewRecorder()
	controller.handleAgentPotentialRisks(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	var payload agentcore.PotentialRiskResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.GreaterOrEqual(t, payload.Count, 1)
	first := payload.Findings[0]
	require.NotEmpty(t, first.RiskSummary)
	require.NotEmpty(t, first.ContributingSignals)
	require.NotEmpty(t, first.TimeWindow)
	require.NotEmpty(t, first.Scope)
	require.GreaterOrEqual(t, first.ConfidenceScore, 0.0)
	require.NotEmpty(t, first.SuggestedInvestigationSteps)
}

func TestHandleAgentWorkflowAudit(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedWorkflowTelemetry(t, store, index, "collector-risk-audit")

	workflow := agentcore.NewWorkflowEngine(agentcore.DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	controller := &Controller{agentWorkflow: workflow}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/joint-risk?collector_id=collector-risk-audit&window=30m", nil)
	resp := httptest.NewRecorder()
	controller.handleAgentJointRisk(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	auditReq := httptest.NewRequest(http.MethodGet, "/api/v1/agent/workflow/audit?limit=20", nil)
	auditResp := httptest.NewRecorder()
	controller.handleAgentWorkflowAudit(auditResp, auditReq)
	require.Equal(t, http.StatusOK, auditResp.Code)

	var payload struct {
		Records []agentcore.WorkflowAuditRecord `json:"records"`
		Count   int                             `json:"count"`
	}
	require.NoError(t, json.NewDecoder(auditResp.Body).Decode(&payload))
	require.GreaterOrEqual(t, payload.Count, 1)
	require.NotEmpty(t, payload.Records[0].WorkflowID)
	require.NotEmpty(t, payload.Records[0].Stage)
	require.NotEmpty(t, payload.Records[0].Action)
}

func seedWorkflowTelemetry(t *testing.T, store *ingest.MemoryStore, index *logindex.Index, collectorID string) {
	t.Helper()

	base := time.Now().Add(-50 * time.Minute).UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: collectorID + "-host"}, base)

	for i := 0; i < 40; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		cpu := 45.0 + float64(i)*1.2
		if cpu > 96 {
			cpu = 96
		}
		memPercent := 58.0 + float64(i)*0.6
		memTotal := float64(16 * 1024 * 1024 * 1024)
		memUsed := memTotal * (memPercent / 100.0)
		ioLatencyMS := 6.0 + float64(i)*1.8
		retrans := 0.001 + float64(i)*0.00045
		if retrans > 0.04 {
			retrans = 0.04
		}
		softnet := float64(i) * 0.7
		ioPressure := 1.0 + float64(i)*0.35
		if ioPressure > 30 {
			ioPressure = 30
		}

		store.StoreMetrics(collectorID, []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: cpu, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_MemTotal_bytes", Value: memTotal, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_Used_bytes", Value: memUsed, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_disk_request_latency_p99_seconds", Value: ioLatencyMS / 1000.0, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_tcp_retransmit_ratio", Value: retrans, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_softnet_dropped_per_second", Value: softnet, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_pressure_io_full_avg10", Value: ioPressure, TimestampUnixNano: ts.UnixNano()},
			{Name: "rca_net_process_connections", Value: 420 + float64(i), TimestampUnixNano: ts.UnixNano(), Labels: []*telemetryv1.Label{{Key: "pid", Value: "1201"}, {Key: "name", Value: "api-server"}}},
			{Name: "rca_io_process_read_bytes_total", Value: 1024 + float64(i)*128, TimestampUnixNano: ts.UnixNano(), Labels: []*telemetryv1.Label{{Key: "pid", Value: "1201"}, {Key: "name", Value: "api-server"}}},
			{Name: "rca_memory_process_rss_bytes", Value: 300*1024*1024 + float64(i)*1024*1024, TimestampUnixNano: ts.UnixNano(), Labels: []*telemetryv1.Label{{Key: "pid", Value: "1201"}, {Key: "name", Value: "api-server"}, {Key: "pod_uid", Value: "pod-a"}, {Key: "job", Value: "checkout"}}},
		}, ts)

		if i%5 == 0 {
			store.StoreLogs(collectorID, []*telemetryv1.LogFingerprint{
				{Fingerprint: "timeout", Count: 3 + uint64(i/5), Example: "error timeout contacting payments after deploy checkout-v1"},
				{Fingerprint: "permission", Count: 1, Example: "warning weak permission chmod 777 on temp path"},
			}, ts)
		}

		if index != nil {
			level := "info"
			message := "normal request flow"
			count := uint64(1)
			if i%4 == 0 {
				level = "error"
				count = 3
				message = "timeout contacting payments dependency after rollout checkout-v1"
			}
			if i%6 == 0 {
				level = "warn"
				message = "weak permission warning: world-writable cache directory"
			}
			index.AddBatch([]logindex.RawEvent{{
				Timestamp:   ts,
				CollectorID: collectorID,
				Hostname:    collectorID + "-host",
				Service:     "checkout",
				Process:     "api-server",
				PID:         "1201",
				Level:       level,
				Source:      "app",
				Message:     message,
				Count:       count,
			}})
		}
	}
}
