package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestHandleEBPFSummaryReturnsStructuredData(t *testing.T) {
	store := ingest.NewMemoryStore()
	collectorID := "collector-ebpf-summary"
	now := time.Now().UTC()

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: collectorID,
		Hostname:    "node-ebpf",
	}, now)

	store.StoreMetrics(collectorID, []*telemetryv1.Metric{
		{
			Name:  "node_ebpf_runtime_event",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "evidence_id", Value: "ev-ebpf-summary-1"},
				{Key: "category", Value: "network"},
				{Key: "type", Value: "abnormal_bind_port"},
				{Key: "severity", Value: "high"},
				{Key: "confidence", Value: "0.90"},
				{Key: "pid", Value: "4210"},
				{Key: "scope", Value: "node"},
				{Key: "description", Value: "unexpected listening port observed"},
				{Key: "port", Value: "31337"},
				{Key: "ts_unix_nano", Value: "1700000000000000000"},
			},
		},
		{
			Name:  "node_ebpf_syscall_statistics_total",
			Value: 144,
			Labels: []*telemetryv1.Label{
				{Key: "syscall", Value: "execve"},
			},
		},
		{Name: "node_ebpf_abnormal_bind_ports_count", Value: 1},
		{Name: "node_ebpf_long_lived_tcp_connections", Value: 2},
	}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ebpf/summary?collector_id="+collectorID, nil)
	resp := httptest.NewRecorder()
	ctrl.handleEBPFSummary(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	var payload struct {
		CollectorID string                 `json:"collector_id"`
		Summary     map[string]interface{} `json:"summary"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.Equal(t, collectorID, payload.CollectorID)
	require.NotEmpty(t, payload.Summary)
	require.Contains(t, payload.Summary, "syscall_statistics")
	require.Contains(t, payload.Summary, "process_graph_snapshot")
	require.Contains(t, payload.Summary, "network_behavior_summary")
	require.Contains(t, payload.Summary, "recent_events")
}

func TestHandleEBPFEventsReturnsEvents(t *testing.T) {
	store := ingest.NewMemoryStore()
	collectorID := "collector-ebpf-events"
	now := time.Now().UTC()

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: collectorID,
		Hostname:    "node-ebpf-events",
	}, now)

	store.StoreMetrics(collectorID, []*telemetryv1.Metric{
		{
			Name:  "node_ebpf_runtime_event",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "evidence_id", Value: "ev-ebpf-events-1"},
				{Key: "category", Value: "process"},
				{Key: "type", Value: "execve"},
				{Key: "severity", Value: "high"},
				{Key: "confidence", Value: "0.88"},
				{Key: "pid", Value: "7001"},
				{Key: "scope", Value: "node"},
				{Key: "description", Value: "execution from /tmp detected"},
				{Key: "path", Value: "/tmp/bootstrap"},
				{Key: "ts_unix_nano", Value: "1700000000000000100"},
			},
		},
	}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ebpf/events?collector_id="+collectorID+"&limit=10", nil)
	resp := httptest.NewRecorder()
	ctrl.handleEBPFEvents(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)

	var payload struct {
		Events []ingest.RuntimeSecurityEvent `json:"events"`
		Count  int                           `json:"count"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	require.NotEmpty(t, payload.Events)
	require.Equal(t, 1, payload.Count)
	require.Equal(t, "ev-ebpf-events-1", payload.Events[0].EvidenceID)
}
