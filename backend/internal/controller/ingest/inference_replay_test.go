package ingest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inferenceobs"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestInferenceRestoredFromAppliedReceiptsWithoutRepeatingObservers(t *testing.T) {
	ctx := context.Background()
	cfg := InboxConfig{Backend: InboxBackendBbolt, Path: filepath.Join(t.TempDir(), "inbox.db")}
	inbox, err := OpenInbox(ctx, cfg, zap.NewNop())
	require.NoError(t, err)
	original := inferenceobs.New(inferenceobs.DefaultConfig())
	observer := &recordingProcessor{}
	server := NewServerWithInbox(NewMemoryStore(), inbox, zap.NewNop(), original, observer)
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		at := now.Add(time.Duration(i-1) * 30 * time.Second)
		labels := []*telemetryv1.Label{{Key: "inference_endpoint", Value: "serving"}, {Key: "model", Value: "synthetic-model"}}
		batch := &telemetryv1.TelemetryBatch{BatchId: fmt.Sprintf("inference-%d", i), Collector: &telemetryv1.CollectorInfo{CollectorId: "synthetic-node"}, Metrics: []*telemetryv1.Metric{
			{Name: "node_inference_generation_tokens_total", Value: float64(300 * i), TimestampUnixNano: at.UnixNano(), Labels: labels},
			{Name: "node_inference_requests_waiting", Value: 12, TimestampUnixNano: at.UnixNano(), Labels: labels},
			{Name: "node_gpu_throttle_thermal_any", Value: 1, TimestampUnixNano: at.UnixNano(), Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
		}}
		receipt, err := receiptFromBatch(batch, at)
		require.NoError(t, err)
		receipt, _, err = inbox.Commit(ctx, receipt)
		require.NoError(t, err)
		require.NoError(t, server.applyReceipt(ctx, receipt))
	}
	want := original.Snapshot("", now)
	require.Equal(t, 10.0, want.Endpoints[0].Models[0].Metrics["generation_tokens_per_second"].Value)
	require.NoError(t, inbox.Close())
	inbox, err = OpenInbox(ctx, cfg, zap.NewNop())
	require.NoError(t, err)
	defer inbox.Close()
	restored := inferenceobs.New(inferenceobs.DefaultConfig())
	server = NewServerWithInbox(NewMemoryStore(), inbox, zap.NewNop(), restored, observer)
	for i := 0; i < 2; i++ {
		require.NoError(t, server.RestoreHotState(ctx))
		require.Equal(t, want, restored.Snapshot("", now), "restart and duplicate replay preserve observations and rates")
	}
	calls, _, _ := observer.snapshot()
	require.Equal(t, 2, calls, "ordinary observers must not repeat during recovery")
}
