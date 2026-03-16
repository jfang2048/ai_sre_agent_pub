package controller

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

func TestAnalysisIngestProcessorFeedsEngine(t *testing.T) {
	cfg := analysis.DefaultConfig()
	cfg.AnalysisInterval = time.Hour
	engine, err := analysis.New(cfg, nil)
	if err != nil {
		t.Fatalf("failed to create analysis engine: %v", err)
	}

	processor := newAnalysisIngestProcessor(engine)
	if processor == nil {
		t.Fatalf("expected non-nil processor")
	}

	batch := &telemetryv1.TelemetryBatch{
		Metrics: []*telemetryv1.Metric{
			{
				Name:              "system.cpu.usage",
				Value:             72.5,
				TimestampUnixNano: time.Now().Add(-time.Second).UnixNano(),
				Labels: []*telemetryv1.Label{
					{Key: "source", Value: "probe-core"},
				},
			},
		},
	}

	processor.ProcessBatch("collector-a", batch, time.Now())
	snapshot := engine.GetNodeMetricsSnapshot("collector-a")
	if snapshot == nil {
		t.Fatalf("expected snapshot")
	}
	if value, ok := snapshot["system.cpu.usage"]; !ok || value != 72.5 {
		t.Fatalf("unexpected metric value: %v", snapshot)
	}
}
