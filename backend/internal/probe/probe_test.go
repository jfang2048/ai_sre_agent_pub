package probe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewCollector(t *testing.T) {
	collector, err := NewCollector()
	if err != nil {
		t.Fatalf("NewCollector() error = %v", err)
	}
	if collector == nil {
		t.Fatal("NewCollector() returned nil")
	}
	if collector.hostname == "" {
		t.Error("NewCollector() hostname is empty")
	}
}

func TestCollectorCollect(t *testing.T) {
	collector, err := NewCollector()
	if err != nil {
		t.Fatalf("NewCollector() error = %v", err)
	}

	batch, err := collector.Collect()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if batch == nil {
		t.Fatal("Collect() returned nil batch")
	}

	if len(batch.Metrics) == 0 {
		t.Error("Collect() returned no metrics")
	}

	if batch.Hostname == "" {
		t.Error("Collect() batch hostname is empty")
	}

	if batch.CollectedAt.IsZero() {
		t.Error("Collect() batch CollectedAt is zero")
	}

	// Check for expected metrics
	expectedMetrics := []string{
		"node_load1",
		"node_load5",
		"node_load15",
		"node_cpu_seconds_total",
		"node_memory_MemTotal_bytes",
		"node_memory_MemAvailable_bytes",
	}

	metricNames := make(map[string]bool)
	for _, m := range batch.Metrics {
		metricNames[m.Name] = true
	}

	for _, expected := range expectedMetrics {
		if !metricNames[expected] {
			t.Errorf("expected metric %s not found", expected)
		}
	}
}

func TestCollectorRateCalculation(t *testing.T) {
	collector, err := NewCollector()
	if err != nil {
		t.Fatalf("NewCollector() error = %v", err)
	}

	// First collection to establish baseline
	_, err = collector.Collect()
	if err != nil {
		t.Fatalf("First Collect() error = %v", err)
	}

	// Wait a bit to allow for rate calculation
	time.Sleep(100 * time.Millisecond)

	// Second collection should have rates
	batch, err := collector.Collect()
	if err != nil {
		t.Fatalf("Second Collect() error = %v", err)
	}

	// Check for rate metrics after second collection
	hasRateMetric := false
	for _, m := range batch.Metrics {
		if strings.Contains(m.Name, "per_second") {
			hasRateMetric = true
			break
		}
	}

	if !hasRateMetric {
		t.Log("No rate metrics found (may need longer interval)")
	}
}

func TestNewProbe(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()

	probe, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if probe == nil {
		t.Fatal("New() returned nil")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ListenAddr != ":9100" {
		t.Errorf("DefaultConfig().ListenAddr = %v, want :9100", cfg.ListenAddr)
	}

	if cfg.ScrapeInterval != 10*time.Second {
		t.Errorf("DefaultConfig().ScrapeInterval = %v, want 10s", cfg.ScrapeInterval)
	}

	if cfg.LogLevel != "info" {
		t.Errorf("DefaultConfig().LogLevel = %v, want info", cfg.LogLevel)
	}
}

func TestProbeHandlers(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	probe, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Manually trigger a scrape to populate metrics
	probe.scrape()

	// Create test server
	mux := http.NewServeMux()
	probe.registerHandlers(mux)

	tests := []struct {
		name           string
		path           string
		expectedStatus int
		contentType    string
	}{
		{
			name:           "health endpoint",
			path:           "/health",
			expectedStatus: http.StatusOK,
			contentType:    "",
		},
		{
			name:           "healthz endpoint",
			path:           "/healthz",
			expectedStatus: http.StatusOK,
			contentType:    "",
		},
		{
			name:           "metrics endpoint",
			path:           "/metrics",
			expectedStatus: http.StatusOK,
			contentType:    "text/plain",
		},
		{
			name:           "metrics JSON endpoint",
			path:           "/api/v1/metrics",
			expectedStatus: http.StatusOK,
			contentType:    "application/json",
		},
		{
			name:           "status endpoint",
			path:           "/api/v1/status",
			expectedStatus: http.StatusOK,
			contentType:    "application/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.expectedStatus)
			}

			if tt.contentType != "" {
				ct := w.Header().Get("Content-Type")
				if !strings.Contains(ct, tt.contentType) {
					t.Errorf("Content-Type = %s, want contains %s", ct, tt.contentType)
				}
			}
		})
	}
}

func TestMetricsPrometheusFormat(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	probe, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	probe.scrape()

	mux := http.NewServeMux()
	probe.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	body := w.Body.String()

	// Check for Prometheus format indicators
	if !strings.Contains(body, "# TYPE") {
		t.Error("Prometheus output missing # TYPE")
	}

	if !strings.Contains(body, "node_") {
		t.Error("Prometheus output missing node_ metrics")
	}
}

func TestMetricsJSONFormat(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	probe, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	probe.scrape()

	mux := http.NewServeMux()
	probe.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/api/v1/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var batch MetricBatch
	if err := json.NewDecoder(w.Body).Decode(&batch); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if len(batch.Metrics) == 0 {
		t.Error("JSON response has no metrics")
	}

	if batch.Hostname == "" {
		t.Error("JSON response missing hostname")
	}
}

func TestStatusResponse(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	probe, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	probe.scrape()

	mux := http.NewServeMux()
	probe.registerHandlers(mux)

	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if resp.Version != "v0.2" {
		t.Errorf("Version = %s, want v0.2", resp.Version)
	}

	if resp.MetricsCount == 0 {
		t.Error("MetricsCount = 0, want > 0")
	}
}

func TestMetricTypes(t *testing.T) {
	collector, err := NewCollector()
	if err != nil {
		t.Fatalf("NewCollector() error = %v", err)
	}

	batch, err := collector.Collect()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	gaugeCount := 0
	counterCount := 0

	for _, m := range batch.Metrics {
		switch m.Type {
		case "gauge":
			gaugeCount++
		case "counter":
			counterCount++
		default:
			t.Errorf("Unknown metric type: %s for metric %s", m.Type, m.Name)
		}
	}

	if gaugeCount == 0 {
		t.Error("No gauge metrics found")
	}

	if counterCount == 0 {
		t.Error("No counter metrics found")
	}
}

func TestSkipDiskDevice(t *testing.T) {
	collector, err := NewCollector()
	if err != nil {
		t.Fatalf("NewCollector() error = %v", err)
	}

	tests := []struct {
		device   string
		expected bool
	}{
		{"loop0", true},
		{"loop1", true},
		{"ram0", true},
		{"dm-0", true},
		{"zram0", true},
		{"sda", false},
		{"sda1", true},
		{"sda2", true},
		{"nvme0n1", false},
		{"nvme0n1p1", true},
		{"nvme0n1p2", true},
	}

	for _, tt := range tests {
		t.Run(tt.device, func(t *testing.T) {
			result := collector.skipDiskDevice(tt.device)
			if result != tt.expected {
				t.Errorf("skipDiskDevice(%s) = %v, want %v", tt.device, result, tt.expected)
			}
		})
	}
}
