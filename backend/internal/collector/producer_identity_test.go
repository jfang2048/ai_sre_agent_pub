package collector

import (
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestProducerIdentityIsInjectableAndMonotonic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SpoolDir = t.TempDir()
	cfg.ProbeCore.Enabled = false
	cfg.EBPF.Enabled = false
	cfg.CollectorID = "collector-a"

	collector, err := NewWithOptions(cfg, nil, WithProducerEpochGenerator(func() (string, error) {
		return "fixed-epoch", nil
	}))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, collector.spool.Close()) })

	collector.mu.Lock()
	firstID, firstEpoch, firstSequence := collector.nextBatchIdentityLocked()
	secondID, secondEpoch, secondSequence := collector.nextBatchIdentityLocked()
	collector.mu.Unlock()

	require.Equal(t, "fixed-epoch", firstEpoch)
	require.Equal(t, firstEpoch, secondEpoch)
	require.Equal(t, uint64(1), firstSequence)
	require.Equal(t, uint64(2), secondSequence)
	require.Equal(t, "collector-a-fixed-epoch-1", firstID)
	require.Equal(t, "collector-a-fixed-epoch-2", secondID)
}

func TestQueuedBatchRetainsProducerIdentityAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	queue, err := spool.New(dir, 1024*1024)
	require.NoError(t, err)
	want := &telemetryv1.TelemetryBatch{
		Collector:        &telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "host-a"},
		BatchId:          "collector-a-epoch-a-41",
		ProducerEpoch:    "epoch-a",
		ProducerSequence: 41,
	}
	payload, err := proto.Marshal(want)
	require.NoError(t, err)
	require.NoError(t, queue.Enqueue(payload))
	require.NoError(t, queue.Close())

	reopened, err := spool.New(dir, 1024*1024)
	require.NoError(t, err)
	payload, _, err = reopened.Next()
	require.NoError(t, err)
	var got telemetryv1.TelemetryBatch
	require.NoError(t, proto.Unmarshal(payload, &got))
	require.Equal(t, want.BatchId, got.BatchId)
	require.Equal(t, want.ProducerEpoch, got.ProducerEpoch)
	require.Equal(t, want.ProducerSequence, got.ProducerSequence)
}
