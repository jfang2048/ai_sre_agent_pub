package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

func buildGPUControllerForTest(t *testing.T) *Controller {
	t.Helper()

	gcfg := gpuobs.DefaultConfig()
	gcfg.PersistDir = t.TempDir()
	store := gpuobs.New(gcfg)
	if err := store.Start(); err != nil {
		t.Fatalf("gpu store start: %v", err)
	}
	t.Cleanup(store.Stop)

	now := time.Now().Add(-10 * time.Second)
	batch := &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_gpu_info", Value: 1, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "name", Value: "H100"}}},
			{Name: "node_gpu_utilization_sm_percent", Value: 84, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_memory_used_mib", Value: 24000, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_memory_total_mib", Value: 81920, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_pcie_link_utilization_percent", Value: 45, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_process_sm_util_percent", Value: 90, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "222"}, {Key: "process", Value: "trainer"}}},
			{Name: "node_gpu_process_memory_mib", Value: 20000, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "222"}, {Key: "process", Value: "trainer"}}},
			{Name: "node_gpu_process_context_active", Value: 1, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "222"}, {Key: "process", Value: "trainer"}}},
			{Name: "node_gpu_event_total", Value: 3, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "event_type", Value: "xid"}, {Key: "severity", Value: "critical"}, {Key: "code", Value: "43"}}},
		},
	}
	store.ProcessBatch("collector-a", batch, now)

	ing := ingest.NewMemoryStore()
	ing.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	ing.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_iowait_percent", Value: 12},
		{Name: "node_disk_utilization_peak_percent", Value: 67},
		{Name: "node_network_utilization_peak_percent", Value: 58},
		{Name: "node_tcp_retransmit_ratio", Value: 0.02},
	}, now)

	return &Controller{gpuStore: store, ingestStore: ing}
}

func TestGPUHandlers_TimelineEventsProcessesCorrelation(t *testing.T) {
	ctrl := buildGPUControllerForTest(t)

	// timeline
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gpu/timeline?collector_id=collector-a&gpu_id=0&metric=node_gpu_utilization_sm_percent&window=1h", nil)
	rec := httptest.NewRecorder()
	ctrl.handleGPUTimeline(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline status=%d body=%s", rec.Code, rec.Body.String())
	}
	var timelineResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &timelineResp); err != nil {
		t.Fatalf("timeline decode: %v", err)
	}
	if timelineResp["count"] == nil {
		t.Fatalf("timeline missing count")
	}

	// events
	req = httptest.NewRequest(http.MethodGet, "/api/v1/gpu/events?collector_id=collector-a&gpu_id=0&window=1h", nil)
	rec = httptest.NewRecorder()
	ctrl.handleGPUEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", rec.Code, rec.Body.String())
	}

	// processes
	req = httptest.NewRequest(http.MethodGet, "/api/v1/gpu/processes?collector_id=collector-a&gpu_id=0&sort_by=sm_util", nil)
	rec = httptest.NewRecorder()
	ctrl.handleGPUProcesses(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("processes status=%d body=%s", rec.Code, rec.Body.String())
	}
	var processesResp struct {
		Count     int `json:"count"`
		Processes []struct {
			PID string `json:"pid"`
		} `json:"processes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &processesResp); err != nil {
		t.Fatalf("processes decode: %v", err)
	}
	if processesResp.Count == 0 || len(processesResp.Processes) == 0 {
		t.Fatalf("expected processes in response")
	}

	// correlation
	req = httptest.NewRequest(http.MethodGet, "/api/v1/gpu/correlation?collector_id=collector-a", nil)
	rec = httptest.NewRecorder()
	ctrl.handleGPUCorrelation(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("correlation status=%d body=%s", rec.Code, rec.Body.String())
	}
	var corrResp struct {
		Scores map[string]float64 `json:"scores"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &corrResp); err != nil {
		t.Fatalf("correlation decode: %v", err)
	}
	if _, ok := corrResp.Scores["overall_risk_percent"]; !ok {
		t.Fatalf("correlation missing overall_risk_percent")
	}
}
