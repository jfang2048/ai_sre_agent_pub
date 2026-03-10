package ingest

import (
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

func TestServerStatsAndSchema(t *testing.T) {
	store := NewMemoryStore()
	server := NewServer(store, zap.NewNop())

	now := time.Now()
	server.recordAccepted(&telemetryv1.TelemetryBatch{
		BatchId: "batch-1",
		Metrics: []*telemetryv1.Metric{{Name: "node_cpu_usage_percent", Value: 42}},
		Processes: []*telemetryv1.ProcessSample{
			{Pid: 1234, Name: "worker", CpuPercent: 80},
		},
		Logs: []*telemetryv1.LogFingerprint{
			{Fingerprint: "abc", Example: "error: test", Count: 1},
		},
	}, "collector-a", now)
	server.recordRejected(assertErr("invalid payload"))

	stats := server.Stats()
	if stats.BatchesTotal != 1 {
		t.Fatalf("Stats().BatchesTotal = %d, want 1", stats.BatchesTotal)
	}
	if stats.RejectedTotal != 1 {
		t.Fatalf("Stats().RejectedTotal = %d, want 1", stats.RejectedTotal)
	}
	if stats.MetricsTotal != 1 {
		t.Fatalf("Stats().MetricsTotal = %d, want 1", stats.MetricsTotal)
	}
	if stats.ProcessesTotal != 1 {
		t.Fatalf("Stats().ProcessesTotal = %d, want 1", stats.ProcessesTotal)
	}
	if stats.LogsTotal != 1 {
		t.Fatalf("Stats().LogsTotal = %d, want 1", stats.LogsTotal)
	}
	if stats.LastCollector != "collector-a" {
		t.Fatalf("Stats().LastCollector = %q, want collector-a", stats.LastCollector)
	}
	if stats.LastBatchID != "batch-1" {
		t.Fatalf("Stats().LastBatchID = %q, want batch-1", stats.LastBatchID)
	}
	if stats.LastError == "" {
		t.Fatalf("Stats().LastError empty, want non-empty")
	}
	if stats.LastBatchAt.IsZero() {
		t.Fatalf("Stats().LastBatchAt is zero")
	}
	if stats.LastRejectAt.IsZero() {
		t.Fatalf("Stats().LastRejectAt is zero")
	}

	schema := server.Schema()
	if schema.Version != "v1" {
		t.Fatalf("Schema().Version = %q, want v1", schema.Version)
	}
	if schema.MaxMetricsPerBatch != maxMetricsPerBatch {
		t.Fatalf("Schema().MaxMetricsPerBatch = %d, want %d", schema.MaxMetricsPerBatch, maxMetricsPerBatch)
	}
	if schema.MaxProcessesPerBatch != maxProcessesPerBatch {
		t.Fatalf("Schema().MaxProcessesPerBatch = %d, want %d", schema.MaxProcessesPerBatch, maxProcessesPerBatch)
	}
	if schema.MaxLogsPerBatch != maxLogsPerBatch {
		t.Fatalf("Schema().MaxLogsPerBatch = %d, want %d", schema.MaxLogsPerBatch, maxLogsPerBatch)
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}
