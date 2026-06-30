package incidents

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

func TestBuildContextWithIngestFallback(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "node-1",
		Hostname:    "node-1",
		Labels: []*telemetryv1.Label{
			{Key: "service", Value: "payments"},
		},
	}, now)
	store.StoreMetrics("node-1", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 92},
		{Name: "node_memory_used_percent", Value: 70},
	}, now)
	store.StoreLogs("node-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "abc", Count: 3, Example: "timeout while calling checkout"},
	}, now)

	cfg := DefaultConfig()
	cfg.Kubernetes.Enabled = false
	logger := zap.NewNop()

	orchestrator, err := NewOrchestrator(cfg, store, logger)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	alert := InputAlert{
		ID:       "a1",
		Title:    "High CPU",
		Service:  "payments",
		Severity: "P1",
		StartsAt: now.Add(-2 * time.Minute),
		Labels:   map[string]string{"service": "payments"},
	}

	ctxBundle, err := orchestrator.BuildContext(context.Background(), alert, "inc-1")
	if err != nil {
		t.Fatalf("build context failed: %v", err)
	}
	if len(ctxBundle.Metrics) == 0 {
		t.Fatalf("expected metrics in context")
	}
	if len(ctxBundle.Logs) == 0 {
		t.Fatalf("expected logs in context")
	}
	if len(ctxBundle.Services) == 0 {
		t.Fatalf("expected service impact")
	}
}
