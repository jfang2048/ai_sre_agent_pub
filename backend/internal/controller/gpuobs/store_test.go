package gpuobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

func TestStore_ProcessBatch_BuildsSnapshotAndPersists(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.PersistDir = dir
	cfg.FlushInterval = 10 * time.Millisecond
	cfg.Retention = 1 * time.Hour

	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start() err=%v", err)
	}

	batch := &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "c-1",
			Hostname:    "node-a",
			Labels:      []*telemetryv1.Label{{Key: "env", Value: "test"}},
		},
		Metrics: []*telemetryv1.Metric{
			{
				Name:  "node_gpu_info",
				Value: 1,
				Labels: []*telemetryv1.Label{
					{Key: "gpu_id", Value: "0"},
					{Key: "uuid", Value: "GPU-uuid"},
					{Key: "name", Value: "NVIDIA-Test"},
					{Key: "driver_version", Value: "999.0"},
					{Key: "cuda_version", Value: "12.0"},
				},
			},
			{
				Name:  "node_gpu_utilization_sm_percent",
				Value: 42,
				Labels: []*telemetryv1.Label{
					{Key: "gpu_id", Value: "0"},
				},
			},
			{
				Name:  "node_gpu_memory_used_mib",
				Value: 1234,
				Labels: []*telemetryv1.Label{
					{Key: "gpu_id", Value: "0"},
				},
			},
			{
				Name:  "node_gpu_memory_total_mib",
				Value: 8192,
				Labels: []*telemetryv1.Label{
					{Key: "gpu_id", Value: "0"},
				},
			},
		},
	}

	s.ProcessBatch("c-1", batch, time.Now())

	node := s.Node("c-1")
	if node == nil {
		t.Fatalf("Node() = nil")
	}
	dev, ok := node.GPUs["0"]
	if !ok {
		t.Fatalf("missing gpu 0")
	}
	if dev.UUID != "GPU-uuid" {
		t.Fatalf("UUID=%q", dev.UUID)
	}
	if dev.UtilSMPercent != 42 {
		t.Fatalf("UtilSMPercent=%v", dev.UtilSMPercent)
	}
	if dev.MemUsedMiB != 1234 {
		t.Fatalf("MemUsedMiB=%v", dev.MemUsedMiB)
	}

	s.Stop()

	snap := filepath.Join(dir, "snapshots", "c-1.json")
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
}

func TestStore_TimelinesAndEvents(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.PersistDir = dir
	cfg.FlushInterval = 10 * time.Millisecond
	cfg.TimelineSamplesPerGPU = 32
	cfg.TimelineSamplesPerProcess = 32
	cfg.EventBufferPerNode = 64

	s := New(cfg)
	if err := s.Start(); err != nil {
		t.Fatalf("Start() err=%v", err)
	}
	defer s.Stop()

	ts0 := time.Now().Add(-30 * time.Second)
	batch0 := &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-a",
			Hostname:    "node-a",
		},
		Metrics: []*telemetryv1.Metric{
			{
				Name:  "node_gpu_info",
				Value: 1,
				Labels: []*telemetryv1.Label{
					{Key: "gpu_id", Value: "0"},
					{Key: "name", Value: "NVIDIA-Test"},
				},
			},
			{Name: "node_gpu_utilization_sm_percent", Value: 70, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_memory_used_mib", Value: 4096, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_process_sm_util_percent", Value: 66, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "123"}, {Key: "process", Value: "trainer"}}},
			{Name: "node_gpu_process_memory_mib", Value: 2048, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "123"}, {Key: "process", Value: "trainer"}}},
			{Name: "node_gpu_process_context_active", Value: 1, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "123"}, {Key: "process", Value: "trainer"}}},
			{Name: "node_gpu_event_total", Value: 2, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "event_type", Value: "xid"}, {Key: "severity", Value: "critical"}, {Key: "code", Value: "43"}}},
		},
	}
	s.ProcessBatch("collector-a", batch0, ts0)

	ts1 := ts0.Add(10 * time.Second)
	batch1 := &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-a",
			Hostname:    "node-a",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_gpu_utilization_sm_percent", Value: 82, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_process_sm_util_percent", Value: 91, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "pid", Value: "123"}, {Key: "process", Value: "trainer"}}},
			{Name: "node_gpu_event_total", Value: 3, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}, {Key: "event_type", Value: "xid"}, {Key: "severity", Value: "critical"}, {Key: "code", Value: "43"}}},
		},
	}
	s.ProcessBatch("collector-a", batch1, ts1)

	deviceTimeline := s.DeviceMetricTimeline("collector-a", "0", "node_gpu_utilization_sm_percent", ts0.Add(-time.Second), 100)
	if len(deviceTimeline) < 2 {
		t.Fatalf("device timeline len=%d, want >=2", len(deviceTimeline))
	}
	if deviceTimeline[len(deviceTimeline)-1].Value != 82 {
		t.Fatalf("device latest util=%v, want 82", deviceTimeline[len(deviceTimeline)-1].Value)
	}

	processTimeline := s.ProcessMetricTimeline("collector-a", "0", "123", "node_gpu_process_sm_util_percent", ts0.Add(-time.Second), 100)
	if len(processTimeline) < 2 {
		t.Fatalf("process timeline len=%d, want >=2", len(processTimeline))
	}
	if processTimeline[len(processTimeline)-1].Value != 91 {
		t.Fatalf("process latest util=%v, want 91", processTimeline[len(processTimeline)-1].Value)
	}

	events := s.Events("collector-a", "0", ts0.Add(-time.Second), 100, "")
	if len(events) < 2 {
		t.Fatalf("events len=%d, want >=2", len(events))
	}
	last := events[len(events)-1]
	if last.EventType != "xid" {
		t.Fatalf("last event type=%q, want xid", last.EventType)
	}

	ranked := s.RankedProcesses("collector-a", "0", "sm_util", 10)
	if len(ranked) == 0 {
		t.Fatalf("ranked processes empty")
	}
	if ranked[0].PID != "123" {
		t.Fatalf("ranked[0].PID=%q, want 123", ranked[0].PID)
	}
}
