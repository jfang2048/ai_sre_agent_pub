package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestPipelineIntegration validates a full probe-ingest-store batch flow.
func TestPipelineIntegration(t *testing.T) {
	store := ingest.NewMemoryStore()
	client, cleanup := startIngestBufConn(t, store)
	defer cleanup()

	now := time.Now().UnixNano()
	mockPayload := &telemetryv1.TelemetryBatch{
		BatchId: "test-batch-001",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "test-node-01",
			Hostname:    "test-node-01",
		},
		Metrics: []*telemetryv1.Metric{
			{
				Name:              "test.metric",
				Value:             42.0,
				TimestampUnixNano: now,
			},
		},
	}

	ack, err := pushBatch(t, client, mockPayload)
	require.NoError(t, err, "Failed to send payload")
	require.Equal(t, "test-batch-001", ack.GetBatchId())

	require.Eventually(t, func() bool {
		nodeData := store.Node("test-node-01")
		if nodeData == nil {
			return false
		}
		metrics := nodeData.Metrics
		if len(metrics) == 0 {
			return false
		}
		return metrics["test.metric"] == 42.0
	}, 2*time.Second, 50*time.Millisecond, "Expected test metric in controller store")
}

// TestPipelineIntegrationRecoversAfterInvalidPayload ensures an invalid stream
// does not poison subsequent valid telemetry ingestion.
func TestPipelineIntegrationRecoversAfterInvalidPayload(t *testing.T) {
	store := ingest.NewMemoryStore()
	client, cleanup := startIngestBufConn(t, store)
	defer cleanup()

	// First send is invalid and must fail with validation error.
	_, err := pushBatch(t, client, &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-invalid",
		},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "expected gRPC status error")
	require.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "batch_id is required")

	// A valid payload right after the invalid one should still be accepted.
	ack, err := pushBatch(t, client, &telemetryv1.TelemetryBatch{
		BatchId: "batch-valid-after-invalid",
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-valid",
			Hostname:    "node-valid",
		},
		Metrics: []*telemetryv1.Metric{
			{
				Name:              "pipeline.recovery.metric",
				Value:             99,
				TimestampUnixNano: time.Now().UnixNano(),
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "batch-valid-after-invalid", ack.GetBatchId())

	require.Eventually(t, func() bool {
		node := store.Node("collector-valid")
		if node == nil {
			return false
		}
		v, ok := node.Metrics["pipeline.recovery.metric"]
		return ok && v == 99
	}, 2*time.Second, 50*time.Millisecond, "Expected valid payload to be ingested after invalid payload")
}
