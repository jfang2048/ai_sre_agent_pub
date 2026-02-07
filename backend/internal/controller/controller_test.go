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

	if resp["version"] != "v0.1" {
		t.Errorf("version = %v, want v0.1", resp["version"])
	}

	if resp["total_nodes"].(float64) != 1 {
		t.Errorf("total_nodes = %v, want 1", resp["total_nodes"])
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
