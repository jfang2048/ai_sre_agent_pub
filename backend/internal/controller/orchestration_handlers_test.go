package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

func TestOrchestrationHandlersSubmitReconcileAndRoute(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultConfig()
	cfg.Orchestration.Enabled = true
	cfg.Orchestration.SafetyMarginRatio = 0
	cfg.Orchestration.PeakPressureThreshold = 0.95

	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	seedIngestForOrchestration(ctrl)

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	submitBody := map[string]interface{}{
		"service":  "chat-gateway",
		"model":    "model-a",
		"class":    "realtime",
		"priority": "P1",
		"requested": map[string]interface{}{
			"cpu_cores":    2,
			"memory_bytes": float64(2 * 1024 * 1024 * 1024),
		},
		"latency_slo_ms":        120,
		"target_concurrency":    64,
		"max_partitions":        2,
		"cache_reuse_preferred": true,
	}
	submitJSON, _ := json.Marshal(submitBody)
	submitReq := httptest.NewRequest(http.MethodPost, "/api/v1/orchestration/workloads", bytes.NewReader(submitJSON))
	submitReq.Header.Set("Content-Type", "application/json")
	submitW := httptest.NewRecorder()
	mux.ServeHTTP(submitW, submitReq)
	if submitW.Code != http.StatusCreated {
		t.Fatalf("submit status = %d, want 201, body=%s", submitW.Code, submitW.Body.String())
	}

	var submitResp struct {
		Workload struct {
			Spec struct {
				ID string `json:"id"`
			} `json:"spec"`
		} `json:"workload"`
	}
	if err := json.NewDecoder(submitW.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if submitResp.Workload.Spec.ID == "" {
		t.Fatalf("missing workload id in submit response")
	}
	workloadID := submitResp.Workload.Spec.ID

	reconcileReq := httptest.NewRequest(http.MethodPost, "/api/v1/orchestration/reconcile", nil)
	reconcileW := httptest.NewRecorder()
	mux.ServeHTTP(reconcileW, reconcileReq)
	if reconcileW.Code != http.StatusOK {
		t.Fatalf("reconcile status = %d, want 200", reconcileW.Code)
	}

	resourcesReq := httptest.NewRequest(http.MethodGet, "/api/v1/orchestration/resources", nil)
	resourcesW := httptest.NewRecorder()
	mux.ServeHTTP(resourcesW, resourcesReq)
	if resourcesW.Code != http.StatusOK {
		t.Fatalf("resources status = %d, want 200", resourcesW.Code)
	}
	var resourcesResp map[string]interface{}
	if err := json.NewDecoder(resourcesW.Body).Decode(&resourcesResp); err != nil {
		t.Fatalf("decode resources response: %v", err)
	}
	if count, ok := resourcesResp["count"].(float64); !ok || count < 1 {
		t.Fatalf("resources count = %v, want >= 1", resourcesResp["count"])
	}

	policyReq := httptest.NewRequest(http.MethodGet, "/api/v1/orchestration/policy", nil)
	policyW := httptest.NewRecorder()
	mux.ServeHTTP(policyW, policyReq)
	if policyW.Code != http.StatusOK {
		t.Fatalf("policy status = %d, want 200", policyW.Code)
	}
	var policyResp struct {
		Policy struct {
			SLOBreachConsecutive int  `json:"slo_breach_consecutive"`
			AutoRemediation      bool `json:"auto_remediation_enabled"`
		} `json:"policy"`
	}
	if err := json.NewDecoder(policyW.Body).Decode(&policyResp); err != nil {
		t.Fatalf("decode policy response: %v", err)
	}
	if policyResp.Policy.SLOBreachConsecutive <= 0 {
		t.Fatalf("policy.slo_breach_consecutive = %d, want > 0", policyResp.Policy.SLOBreachConsecutive)
	}
	if !policyResp.Policy.AutoRemediation {
		t.Fatalf("policy.auto_remediation_enabled = false, want true")
	}

	diagReq := httptest.NewRequest(http.MethodGet, "/api/v1/orchestration/diagnostics", nil)
	diagW := httptest.NewRecorder()
	mux.ServeHTTP(diagW, diagReq)
	if diagW.Code != http.StatusOK {
		t.Fatalf("diagnostics status = %d, want 200", diagW.Code)
	}
	var diagResp struct {
		Diagnostics struct {
			Metrics struct {
				ReconcilesTotal float64 `json:"reconciles_total"`
			} `json:"metrics"`
		} `json:"diagnostics"`
	}
	if err := json.NewDecoder(diagW.Body).Decode(&diagResp); err != nil {
		t.Fatalf("decode diagnostics response: %v", err)
	}
	if diagResp.Diagnostics.Metrics.ReconcilesTotal < 1 {
		t.Fatalf("diagnostics.metrics.reconciles_total = %v, want >= 1", diagResp.Diagnostics.Metrics.ReconcilesTotal)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/orchestration/workloads/"+workloadID, nil)
	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("get workload status = %d, want 200", getW.Code)
	}
	var getResp struct {
		Workload struct {
			State string `json:"state"`
		} `json:"workload"`
	}
	if err := json.NewDecoder(getW.Body).Decode(&getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	if getResp.Workload.State != "running" {
		t.Fatalf("workload state = %s, want running", getResp.Workload.State)
	}

	routesReq := httptest.NewRequest(http.MethodGet, "/api/v1/orchestration/routes?service=chat-gateway&model=model-a", nil)
	routesW := httptest.NewRecorder()
	mux.ServeHTTP(routesW, routesReq)
	if routesW.Code != http.StatusOK {
		t.Fatalf("routes status = %d, want 200", routesW.Code)
	}
	var routesResp map[string]interface{}
	if err := json.NewDecoder(routesW.Body).Decode(&routesResp); err != nil {
		t.Fatalf("decode routes response: %v", err)
	}
	if count, ok := routesResp["count"].(float64); !ok || count < 1 {
		t.Fatalf("routes count = %v, want >= 1", routesResp["count"])
	}

	completeReq := httptest.NewRequest(http.MethodPost, "/api/v1/orchestration/workloads/"+workloadID+"/complete", nil)
	completeW := httptest.NewRecorder()
	mux.ServeHTTP(completeW, completeReq)
	if completeW.Code != http.StatusOK {
		t.Fatalf("complete status = %d, want 200", completeW.Code)
	}
}

func seedIngestForOrchestration(ctrl *Controller) {
	now := time.Now()
	ctrl.ingestStore.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "collector-a",
		Hostname:    "node-a",
		Labels: []*telemetryv1.Label{
			{Key: "resource.cpu.capacity", Value: "8"},
			{Key: "resource.network.mbps", Value: "10000"},
			{Key: "resource.storage.iops", Value: "50000"},
			{Key: "zone", Value: "z1"},
			{Key: "cluster", Value: "c1"},
		},
	}, now)

	ctrl.ingestStore.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 20, TimestampUnixNano: now.UnixNano()},
		{Name: "node_memory_MemTotal_bytes", Value: 64 * 1024 * 1024 * 1024, TimestampUnixNano: now.UnixNano()},
		{Name: "node_memory_MemAvailable_bytes", Value: 56 * 1024 * 1024 * 1024, TimestampUnixNano: now.UnixNano()},
		{Name: "node_load1", Value: 1, TimestampUnixNano: now.UnixNano()},
		{Name: "node_network_receive_bytes_per_second", Value: 1500000, TimestampUnixNano: now.UnixNano()},
		{Name: "node_network_transmit_bytes_per_second", Value: 1200000, TimestampUnixNano: now.UnixNano()},
		{Name: "node_disk_total_iops_per_second", Value: 200, TimestampUnixNano: now.UnixNano()},
	}, now)
}
