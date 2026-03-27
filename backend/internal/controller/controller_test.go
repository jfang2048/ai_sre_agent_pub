package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentreport "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agent"
	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

func TestNewController(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()

	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if ctrl == nil {
		t.Fatal("New() returned nil")
	}
}

func TestNewControllerAuthEnabledWithoutAPIKeyFails(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.APIKeyEnv = "TEST_SRE_AGENT_CONTROLLER_API_KEY"
	t.Setenv("TEST_SRE_AGENT_CONTROLLER_API_KEY", "")

	ctrl, err := New(cfg, logger)
	if err == nil {
		t.Fatal("New() should fail when auth is enabled and API key env is empty")
	}
	if ctrl != nil {
		t.Fatal("New() returned non-nil controller on auth misconfiguration")
	}
	if !strings.Contains(err.Error(), "auth is enabled") {
		t.Fatalf("expected auth configuration error, got: %v", err)
	}
}

func TestNewControllerAuthEnabledWithAPIKeySucceeds(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.APIKeyEnv = "TEST_SRE_AGENT_CONTROLLER_API_KEY"
	t.Setenv("TEST_SRE_AGENT_CONTROLLER_API_KEY", "test-api-key")

	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if ctrl == nil {
		t.Fatal("New() returned nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ListenAddr != ":8080" {
		t.Errorf("DefaultConfig().ListenAddr = %v, want :8080", cfg.ListenAddr)
	}

	if cfg.ScrapeInterval != 15*time.Second {
		t.Errorf("DefaultConfig().ScrapeInterval = %v, want 15s", cfg.ScrapeInterval)
	}

	if cfg.ScrapeTimeout != 10*time.Second {
		t.Errorf("DefaultConfig().ScrapeTimeout = %v, want 10s", cfg.ScrapeTimeout)
	}

	if cfg.WebPath != "./web" {
		t.Errorf("DefaultConfig().WebPath = %v, want ./web", cfg.WebPath)
	}

	if cfg.LogLevel != "info" {
		t.Errorf("DefaultConfig().LogLevel = %v, want info", cfg.LogLevel)
	}
}

func TestNodeConfig(t *testing.T) {
	node := NodeConfig{
		Name:    "test-node",
		Address: "localhost:9100",
		Labels: map[string]string{
			"env":  "test",
			"role": "web",
		},
	}

	if node.Name != "test-node" {
		t.Errorf("NodeConfig.Name = %s, want test-node", node.Name)
	}

	if node.Address != "localhost:9100" {
		t.Errorf("NodeConfig.Address = %s, want localhost:9100", node.Address)
	}

	if len(node.Labels) != 2 {
		t.Errorf("NodeConfig.Labels count = %d, want 2", len(node.Labels))
	}
}

func TestControllerAddNode(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	node := NodeConfig{
		Name:    "test-node",
		Address: "localhost:9100",
	}

	err = ctrl.AddNode(node)
	if err != nil {
		t.Fatalf("AddNode() error = %v", err)
	}

	// Try adding same node again
	err = ctrl.AddNode(node)
	if err == nil {
		t.Error("AddNode() should error on duplicate node")
	}
}

func TestControllerRemoveNode(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.Nodes = []NodeConfig{
		{Name: "test-node", Address: "localhost:9100"},
	}
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = ctrl.RemoveNode("test-node")
	if err != nil {
		t.Fatalf("RemoveNode() error = %v", err)
	}

	// Try removing non-existent node
	err = ctrl.RemoveNode("nonexistent")
	if err == nil {
		t.Error("RemoveNode() should error on non-existent node")
	}
}

func TestControllerHandleHealth(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	tests := []struct {
		path string
	}{
		{"/health"},
		{"/healthz"},
		{"/readyz"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
			}

			if w.Body.String() != "OK" {
				t.Errorf("body = %s, want OK", w.Body.String())
			}
		})
	}
}

func TestControllerHandleReadyFollowerWithoutReadAccessReturnsUnavailable(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.HA.Enabled = true
	cfg.HA.Mode = "standby"
	cfg.HA.AllowFollowerRead = false

	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestControllerHandleStatus(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.Nodes = []NodeConfig{
		{Name: "node-1", Address: "localhost:9100"},
	}
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if resp["version"] != cfg.Version {
		t.Errorf("version = %v, want %s", resp["version"], cfg.Version)
	}

	if resp["total_nodes"].(float64) != 1 {
		t.Errorf("total_nodes = %v, want 1", resp["total_nodes"])
	}
	deployment, ok := resp["deployment"].(map[string]interface{})
	if !ok {
		t.Fatalf("deployment block missing or wrong type: %#v", resp["deployment"])
	}
	if deployment["mode"] != cfg.Deployment.Mode {
		t.Errorf("deployment.mode = %v, want %s", deployment["mode"], cfg.Deployment.Mode)
	}
	apiBlock, ok := resp["api"].(map[string]interface{})
	if !ok {
		t.Fatalf("api block missing or wrong type: %#v", resp["api"])
	}
	if apiBlock["rate_limit_enabled"] != cfg.API.RateLimitEnabled {
		t.Errorf("api.rate_limit_enabled = %v, want %v", apiBlock["rate_limit_enabled"], cfg.API.RateLimitEnabled)
	}
}

func TestControllerHandleStatusIncludesCollectorCoverage(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-fresh", Hostname: "node-fresh"}, now)
	store.StoreMetrics("collector-fresh", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 35},
		{Name: "node_memory_used_percent", Value: 48},
		{Name: "node_network_receive_bytes_per_second", Value: 1024},
		{Name: "node_disk_read_bytes_per_second", Value: 2048},
		{Name: "collector_probe_core_fresh", Value: 1},
	}, now)
	store.StoreBatchMeta("collector-fresh", &telemetryv1.TelemetryBatch{
		Collector:        &telemetryv1.CollectorInfo{CollectorId: "collector-fresh", Hostname: "node-fresh"},
		BatchId:          "batch-fresh",
		WallTimeUnixNano: now.UnixNano(),
	}, now)

	staleAt := now.Add(-10 * time.Minute)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-stale", Hostname: "node-stale"}, staleAt)
	store.StoreMetrics("collector-stale", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 22},
		{Name: "collector_probe_core_fresh", Value: 0},
		{Name: "collector_spool_backlog_bytes", Value: 2048},
	}, staleAt)
	store.StoreBatchMeta("collector-stale", &telemetryv1.TelemetryBatch{
		Collector:        &telemetryv1.CollectorInfo{CollectorId: "collector-stale", Hostname: "node-stale"},
		BatchId:          "batch-stale",
		WallTimeUnixNano: staleAt.UnixNano(),
	}, staleAt)

	ctrl := &Controller{
		config:      DefaultConfig(),
		ingestStore: store,
	}

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	w := httptest.NewRecorder()
	ctrl.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		CollectorCoverage struct {
			State              string  `json:"state"`
			TotalCollectors    int     `json:"total_collectors"`
			FreshCollectors    int     `json:"fresh_collectors"`
			StaleCollectors    int     `json:"stale_collectors"`
			DegradedCollectors int     `json:"degraded_collectors"`
			BacklogCollectors  int     `json:"backlog_collectors"`
			CoveragePercent    float64 `json:"coverage_percent"`
		} `json:"collector_coverage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if resp.CollectorCoverage.State != telemetryStateStale {
		t.Fatalf("state = %q, want %q", resp.CollectorCoverage.State, telemetryStateStale)
	}
	if resp.CollectorCoverage.TotalCollectors != 2 {
		t.Fatalf("total_collectors = %d, want 2", resp.CollectorCoverage.TotalCollectors)
	}
	if resp.CollectorCoverage.FreshCollectors != 1 {
		t.Fatalf("fresh_collectors = %d, want 1", resp.CollectorCoverage.FreshCollectors)
	}
	if resp.CollectorCoverage.StaleCollectors != 1 {
		t.Fatalf("stale_collectors = %d, want 1", resp.CollectorCoverage.StaleCollectors)
	}
	if resp.CollectorCoverage.DegradedCollectors != 0 {
		t.Fatalf("degraded_collectors = %d, want 0", resp.CollectorCoverage.DegradedCollectors)
	}
	if resp.CollectorCoverage.BacklogCollectors != 1 {
		t.Fatalf("backlog_collectors = %d, want 1", resp.CollectorCoverage.BacklogCollectors)
	}
	if resp.CollectorCoverage.CoveragePercent >= 100 {
		t.Fatalf("coverage_percent = %f, want < 100", resp.CollectorCoverage.CoveragePercent)
	}
}

func TestControllerHandleHAStatus(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.HA.Enabled = true
	cfg.HA.Mode = "standby"
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/api/v1/ha/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp struct {
		Enabled  bool   `json:"enabled"`
		Mode     string `json:"mode"`
		Active   bool   `json:"active"`
		ReadOnly bool   `json:"read_only"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Enabled {
		t.Fatalf("enabled = false, want true")
	}
	if resp.Mode != "standby" {
		t.Fatalf("mode = %q, want standby", resp.Mode)
	}
	if resp.Active {
		t.Fatalf("active = true, want false")
	}
	if !resp.ReadOnly {
		t.Fatalf("read_only = false, want true")
	}
}

func TestControllerStandbyRejectsNodeMutations(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.HA.Enabled = true
	cfg.HA.Mode = "standby"
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	body := strings.NewReader(`{"name":"node-new","address":"127.0.0.1:9100"}`)
	req := httptest.NewRequest("POST", "/api/v1/nodes", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestControllerHandleNodes(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.Nodes = []NodeConfig{
		{Name: "node-1", Address: "localhost:9100"},
		{Name: "node-2", Address: "localhost:9101"},
	}
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if resp["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}
}

func TestControllerHandleNodesPost(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	body := `{"name":"new-node","address":"localhost:9102"}`
	req := httptest.NewRequest("POST", "/api/v1/nodes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	// Verify node was added
	req = httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1", resp["count"])
	}
}

func TestControllerHandleNodeByID(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.Nodes = []NodeConfig{
		{Name: "test-node", Address: "localhost:9100"},
	}
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	// Get existing node
	req := httptest.NewRequest("GET", "/api/v1/nodes/test-node", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET status = %d, want %d", w.Code, http.StatusOK)
	}

	// Get non-existent node
	req = httptest.NewRequest("GET", "/api/v1/nodes/nonexistent", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("GET nonexistent status = %d, want %d", w.Code, http.StatusNotFound)
	}

	// Delete node
	req = httptest.NewRequest("DELETE", "/api/v1/nodes/test-node", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("DELETE status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestControllerHandlePrometheusMetrics(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.Nodes = []NodeConfig{
		{Name: "test-node", Address: "localhost:9100"},
	}
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()

	// Check for controller metrics
	if !strings.Contains(body, "sre_controller_nodes_total") {
		t.Error("Missing sre_controller_nodes_total metric")
	}

	if !strings.Contains(body, "sre_controller_nodes_healthy") {
		t.Error("Missing sre_controller_nodes_healthy metric")
	}

	if !strings.Contains(body, "sre_node_up") {
		t.Error("Missing sre_node_up metric")
	}
}

func TestControllerHTTPMutationAudit(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.API.AuditMutations = true
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)
	handler := ctrl.wrapHTTPHandler(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/nodes", strings.NewReader(`{"name":"audit-node","address":"127.0.0.1:9100"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}

	ctrl.apiMu.RLock()
	defer ctrl.apiMu.RUnlock()
	if len(ctrl.controllerAuditLog) == 0 {
		t.Fatal("expected mutation audit record")
	}
	last := ctrl.controllerAuditLog[len(ctrl.controllerAuditLog)-1]
	if last.Action != "http_mutation_request" {
		t.Fatalf("action = %q, want http_mutation_request", last.Action)
	}
	if last.Status != "success" {
		t.Fatalf("status = %q, want success", last.Status)
	}
	if last.Input["method"] != http.MethodPost {
		t.Fatalf("method = %q, want %q", last.Input["method"], http.MethodPost)
	}
}

func TestControllerHTTPRateLimit(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.API.RateLimitEnabled = true
	cfg.API.RateLimitRPS = 1
	cfg.API.RateLimitBurst = 1
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)
	handler := ctrl.wrapHTTPHandler(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
}

func TestControllerHandlePrometheusMetricsIncludesWorkflowObservability(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	workflow := agentcore.NewWorkflowEngine(agentcore.DefaultWorkflowConfig(), store, index, nil, zap.NewNop())

	ctrl := &Controller{
		agentWorkflow: workflow,
		agentEngine:   &agentreport.Engine{},
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	ctrl.handlePrometheusMetrics(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "agent_reasoning_steps_total") {
		t.Fatal("missing agent_reasoning_steps_total metric")
	}
	if !strings.Contains(body, "agent_hallucination_proxy_total") {
		t.Fatal("missing agent_hallucination_proxy_total metric")
	}
	if !strings.Contains(body, "agent_action_dry_run_total") {
		t.Fatal("missing agent_action_dry_run_total metric")
	}
	if !strings.Contains(body, "agent_workflow_verifications_total") {
		t.Fatal("missing agent_workflow_verifications_total metric")
	}
	if !strings.Contains(body, "agent_workflow_evidence_packages_total") {
		t.Fatal("missing agent_workflow_evidence_packages_total metric")
	}
	if !strings.Contains(body, "agent_workflow_memory_writebacks_total") {
		t.Fatal("missing agent_workflow_memory_writebacks_total metric")
	}
}

func TestControllerCORS(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	// Test OPTIONS preflight
	req := httptest.NewRequest("OPTIONS", "/api/v1/nodes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("OPTIONS status = %d, want %d", w.Code, http.StatusOK)
	}

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("Missing CORS header")
	}

	// Test CORS on regular request
	req = httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("Missing CORS header on GET")
	}
}

func TestControllerStartStop(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.ListenAddr = ":0" // Use random available port
	cfg.Nodes = []NodeConfig{}
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = ctrl.Start(ctx)
	if err != nil {
		if isNetPermissionError(err) {
			t.Skipf("Skipping due to listen permission error: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}

	// Wait a bit for server to start
	time.Sleep(100 * time.Millisecond)

	err = ctrl.Stop()
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestControllerDoubleStart(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.Nodes = []NodeConfig{}
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = ctrl.Start(ctx)
	if err != nil {
		if isNetPermissionError(err) {
			t.Skipf("Skipping due to listen permission error: %v", err)
		}
		t.Fatalf("First Start() error = %v", err)
	}
	defer ctrl.Stop()

	// Second start should error
	err = ctrl.Start(ctx)
	if err == nil {
		t.Error("Second Start() should error")
	}
}

// TestMockAgentScrape tests scraping with a mock agent
func TestMockAgentScrape(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if isNetPermissionError(err) {
			t.Skipf("Skipping due to listen permission error: %v", err)
		}
		t.Fatalf("failed to listen: %v", err)
	}

	// Create a mock agent server
	mockAgent := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/metrics" {
			response := map[string]interface{}{
				"metrics": []map[string]interface{}{
					{
						"name":  "node_cpu_usage_percent",
						"type":  "gauge",
						"value": 25.5,
					},
					{
						"name":  "node_memory_MemTotal_bytes",
						"type":  "gauge",
						"value": 16777216000,
					},
				},
				"collected_at": time.Now(),
				"hostname":     "mock-host",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}
		http.NotFound(w, r)
	}))
	mockAgent.Listener = listener
	mockAgent.Start()
	defer mockAgent.Close()

	// Extract host:port from mock server URL
	addr := strings.TrimPrefix(mockAgent.URL, "http://")

	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.ScrapeInterval = 100 * time.Millisecond
	cfg.ScrapeTimeout = 1 * time.Second
	cfg.Nodes = []NodeConfig{
		{Name: "mock-node", Address: addr},
	}

	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = ctrl.Start(ctx)
	if err != nil {
		if isNetPermissionError(err) {
			t.Skipf("Skipping due to listen permission error: %v", err)
		}
		t.Fatalf("Start() error = %v", err)
	}
	defer ctrl.Stop()

	// Wait for at least one scrape
	time.Sleep(200 * time.Millisecond)

	// Check node status
	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	nodes := resp["nodes"].([]interface{})
	if len(nodes) != 1 {
		t.Fatalf("Expected 1 node, got %d", len(nodes))
	}

	node := nodes[0].(map[string]interface{})
	if !node["healthy"].(bool) {
		t.Error("Node should be healthy after successful scrape")
	}

	if node["metric_count"].(float64) != 2 {
		t.Errorf("metric_count = %v, want 2", node["metric_count"])
	}
}

func isNetPermissionError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "operation not permitted")
}

func TestNodeStatusStruct(t *testing.T) {
	status := NodeStatus{
		Name:        "test",
		Address:     "localhost:9100",
		Healthy:     true,
		LastScrape:  time.Now(),
		MetricCount: 50,
	}

	if status.Name != "test" {
		t.Errorf("Name = %s, want test", status.Name)
	}

	if status.Healthy != true {
		t.Error("Healthy should be true")
	}
}

func TestAgentMetricStruct(t *testing.T) {
	metric := AgentMetric{
		Name:  "test_metric",
		Type:  "gauge",
		Value: 123.45,
		Labels: map[string]string{
			"label1": "value1",
		},
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(metric)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded AgentMetric
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.Name != metric.Name {
		t.Errorf("Name = %s, want %s", decoded.Name, metric.Name)
	}

	if decoded.Value != metric.Value {
		t.Errorf("Value = %f, want %f", decoded.Value, metric.Value)
	}
}

func BenchmarkControllerHandleNodes(b *testing.B) {
	logger, _ := zap.NewProduction()
	cfg := DefaultConfig()
	for i := 0; i < 100; i++ {
		cfg.Nodes = append(cfg.Nodes, NodeConfig{
			Name:    fmt.Sprintf("node-%d", i),
			Address: fmt.Sprintf("localhost:%d", 9100+i),
		})
	}

	ctrl, _ := New(cfg, logger)
	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/api/v1/nodes", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}

func BenchmarkControllerHandleStatus(b *testing.B) {
	logger, _ := zap.NewProduction()
	cfg := DefaultConfig()
	ctrl, _ := New(cfg, logger)
	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/api/v1/status", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}
