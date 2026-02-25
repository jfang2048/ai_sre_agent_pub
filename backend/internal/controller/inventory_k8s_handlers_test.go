package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestInventoryHandlersHeartbeatAndLookup(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Nodes = []NodeConfig{
		{Name: "probe-static", Address: "10.0.0.1:9100"},
	}
	ctrl, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	body := []byte(`{"probe_id":"probe-runtime","hostname":"node-runtime","address":"10.0.0.2:9100"}`)
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/heartbeat", bytes.NewReader(body))
	postReq.Header.Set("Content-Type", "application/json")
	postW := httptest.NewRecorder()
	mux.ServeHTTP(postW, postReq)
	if postW.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/inventory/heartbeat status=%d body=%s", postW.Code, postW.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/probes", nil)
	listW := httptest.NewRecorder()
	mux.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/inventory/probes status=%d body=%s", listW.Code, listW.Body.String())
	}
	var listResp struct {
		Count  int `json:"count"`
		Probes []struct {
			ID string `json:"id"`
		} `json:"probes"`
	}
	if err := json.NewDecoder(listW.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listResp.Count < 2 {
		t.Fatalf("inventory count=%d, want >=2", listResp.Count)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/inventory/probes/probe-runtime", nil)
	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/inventory/probes/probe-runtime status=%d body=%s", getW.Code, getW.Body.String())
	}
	var probeResp struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(getW.Body).Decode(&probeResp); err != nil {
		t.Fatalf("decode probe response: %v", err)
	}
	if probeResp.ID != "probe-runtime" {
		t.Fatalf("probe id=%q, want probe-runtime", probeResp.ID)
	}
	if probeResp.Hostname != "node-runtime" {
		t.Fatalf("probe hostname=%q, want node-runtime", probeResp.Hostname)
	}
}

func TestK8sHandlersEnabledWithoutClusterTargets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Kubernetes.Enabled = true
	cfg.Kubernetes.Clusters = nil

	ctrl, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/status", nil)
	statusW := httptest.NewRecorder()
	mux.ServeHTTP(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/k8s/status status=%d body=%s", statusW.Code, statusW.Body.String())
	}

	clustersReq := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/clusters", nil)
	clustersW := httptest.NewRecorder()
	mux.ServeHTTP(clustersW, clustersReq)
	if clustersW.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/k8s/clusters status=%d body=%s", clustersW.Code, clustersW.Body.String())
	}
	var clustersResp struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(clustersW.Body).Decode(&clustersResp); err != nil {
		t.Fatalf("decode clusters response: %v", err)
	}
	if clustersResp.Count != 0 {
		t.Fatalf("clusters count=%d, want 0", clustersResp.Count)
	}

	workloadsReq := httptest.NewRequest(http.MethodGet, "/api/v1/k8s/workloads/top?metric=cpu&limit=5", nil)
	workloadsW := httptest.NewRecorder()
	mux.ServeHTTP(workloadsW, workloadsReq)
	if workloadsW.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/k8s/workloads/top status=%d body=%s", workloadsW.Code, workloadsW.Body.String())
	}
}

func TestIngestSchemaStatusEndpoints(t *testing.T) {
	cfg := DefaultConfig()
	ctrl, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/ingest/status", nil)
	statusW := httptest.NewRecorder()
	mux.ServeHTTP(statusW, statusReq)
	if statusW.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/ingest/status status=%d body=%s", statusW.Code, statusW.Body.String())
	}

	schemaReq := httptest.NewRequest(http.MethodGet, "/api/v1/ingest/schema", nil)
	schemaW := httptest.NewRecorder()
	mux.ServeHTTP(schemaW, schemaReq)
	if schemaW.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/ingest/schema status=%d body=%s", schemaW.Code, schemaW.Body.String())
	}
	var schemaResp struct {
		Version            string `json:"version"`
		MaxMetricsPerBatch int    `json:"max_metrics_per_batch"`
	}
	if err := json.NewDecoder(schemaW.Body).Decode(&schemaResp); err != nil {
		t.Fatalf("decode schema response: %v", err)
	}
	if schemaResp.Version == "" {
		t.Fatalf("schema version empty")
	}
	if schemaResp.MaxMetricsPerBatch <= 0 {
		t.Fatalf("schema max_metrics_per_batch=%d, want >0", schemaResp.MaxMetricsPerBatch)
	}
}
