package analysis

import (
	"strings"
	"testing"
	"time"
)

func TestIncidentReportsClassifyCommunicationCongestion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AnalysisInterval = time.Hour

	engine, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	now := time.Now().UTC()
	var samples []MetricSample
	for i := 0; i < 24; i++ {
		ts := now.Add(-time.Duration(24-i) * time.Second)
		rx := 8_000_000.0
		tx := 6_000_000.0
		retrans := 0.05
		if i >= 22 {
			rx = 180_000_000.0
			tx = 120_000_000.0
			retrans = 1.2
		}
		samples = append(samples,
			MetricSample{Name: "node_network_receive_bytes_per_second", Value: rx, Timestamp: ts},
			MetricSample{Name: "node_network_transmit_bytes_per_second", Value: tx, Timestamp: ts},
			MetricSample{Name: "probe_core_network_tcp_retransmissions_per_sec", Value: retrans, Timestamp: ts},
			MetricSample{Name: "system.cpu.usage", Value: 35.0, Timestamp: ts},
			MetricSample{Name: "node_memory_Used_bytes", Value: 20 * 1024 * 1024 * 1024, Timestamp: ts},
			MetricSample{Name: "node_memory_MemTotal_bytes", Value: 64 * 1024 * 1024 * 1024, Timestamp: ts},
		)
	}

	engine.IngestMetrics("node-net", samples)
	engine.runAnalysis()

	reports := engine.GetIncidentReports("node-net", 10)
	if len(reports) == 0 {
		t.Fatalf("expected incident reports")
	}
	report := reports[0]
	if report.Classification != IncidentClassCommunicationCongest {
		t.Fatalf("expected %s classification, got %s", IncidentClassCommunicationCongest, report.Classification)
	}
	if !strings.Contains(strings.ToLower(report.ProbableCause), "network") {
		t.Fatalf("expected network probable cause, got %q", report.ProbableCause)
	}
	if len(report.SupportingSignals) == 0 {
		t.Fatalf("expected supporting signals")
	}
}

func TestIncidentReportsClassifyGPUStarvation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AnalysisInterval = time.Hour

	engine, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	now := time.Now().UTC()
	var samples []MetricSample
	for i := 0; i < 24; i++ {
		ts := now.Add(-time.Duration(24-i) * time.Second)
		gpuUtil := 62.0
		cpuUsage := 48.0
		if i >= 22 {
			gpuUtil = 18.0
			cpuUsage = 93.0
		}
		samples = append(samples,
			MetricSample{Name: "node_gpu_utilization_sm_avg_percent", Value: gpuUtil, Timestamp: ts},
			MetricSample{Name: "node_gpu_process_total", Value: 3.0, Timestamp: ts},
			MetricSample{Name: "system.cpu.usage", Value: cpuUsage, Timestamp: ts},
			MetricSample{Name: "node_load1", Value: 5.5, Timestamp: ts},
			MetricSample{Name: "node_memory_Used_bytes", Value: 28 * 1024 * 1024 * 1024, Timestamp: ts},
			MetricSample{Name: "node_memory_MemTotal_bytes", Value: 64 * 1024 * 1024 * 1024, Timestamp: ts},
		)
	}

	engine.IngestMetrics("node-gpu", samples)
	engine.runAnalysis()

	reports := engine.GetIncidentReports("node-gpu", 10)
	if len(reports) == 0 {
		t.Fatalf("expected incident reports")
	}
	report := reports[0]
	if report.Classification != IncidentClassGPUStarvation {
		t.Fatalf("expected %s classification, got %s", IncidentClassGPUStarvation, report.Classification)
	}
	if !containsString(report.ImpactedComponents, "gpu") {
		t.Fatalf("expected impacted components to include gpu, got %v", report.ImpactedComponents)
	}
	if report.PrimaryMetric == "" {
		t.Fatalf("expected primary metric")
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
