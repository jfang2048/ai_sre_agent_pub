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
