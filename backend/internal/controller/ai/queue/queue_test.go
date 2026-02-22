package queue

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Enqueue / Dequeue basic tests ──────────────────────────────────────

func TestEnqueueDequeue(t *testing.T) {
	q := NewMemoryQueue(10, zap.NewNop())
	ctx := context.Background()

	dp := &DataPoint{NodeName: "web-1", Timestamp: time.Now()}
	require.NoError(t, q.Enqueue(ctx, dp))
	assert.Equal(t, 1, q.Len())

	got, err := q.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "web-1", got.NodeName)
	assert.Equal(t, 0, q.Len())
}

func TestEnqueueMultipleDequeueFIFO(t *testing.T) {
	q := NewMemoryQueue(10, zap.NewNop())
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		q.Enqueue(ctx, &DataPoint{NodeName: "node", Timestamp: time.Now().Add(time.Duration(i) * time.Second)})
	}
	assert.Equal(t, 5, q.Len())

	got, _ := q.Dequeue(ctx)
	assert.True(t, got.Timestamp.Before(time.Now()), "FIFO: first enqueued should be first dequeued")
}

// ── Bounded queue (drop oldest) tests ─────────────────────────────────

func TestEnqueueDropsOldestAtCapacity(t *testing.T) {
	q := NewMemoryQueue(3, zap.NewNop())
	ctx := context.Background()

	q.Enqueue(ctx, &DataPoint{NodeName: "a"})
	q.Enqueue(ctx, &DataPoint{NodeName: "b"})
	q.Enqueue(ctx, &DataPoint{NodeName: "c"})
	q.Enqueue(ctx, &DataPoint{NodeName: "d"}) // should drop "a"

	assert.Equal(t, 3, q.Len())

	got, _ := q.Dequeue(ctx)
	assert.Equal(t, "b", got.NodeName, "Oldest item 'a' should have been dropped")

	stats := q.Stats()
	assert.Equal(t, int64(1), stats.Dropped)
	assert.Equal(t, int64(4), stats.Enqueued)
}

// ── Close semantics tests ──────────────────────────────────────────────

func TestEnqueueAfterCloseReturnsError(t *testing.T) {
	q := NewMemoryQueue(10, zap.NewNop())
	q.Close()

	err := q.Enqueue(context.Background(), &DataPoint{NodeName: "x"})
	require.Error(t, err)
	assert.Equal(t, ErrQueueClosed, err)
}

func TestDequeueAfterCloseReturnsError(t *testing.T) {
	q := NewMemoryQueue(10, zap.NewNop())
	q.Close()

	_, err := q.Dequeue(context.Background())
	require.Error(t, err)
	assert.Equal(t, ErrQueueClosed, err)
}

func TestDequeueDrainsBeforeClose(t *testing.T) {
	q := NewMemoryQueue(10, zap.NewNop())
	ctx := context.Background()

	q.Enqueue(ctx, &DataPoint{NodeName: "pre-close"})
	q.Close()

	// Should still get the item that was enqueued before close
	got, err := q.Dequeue(ctx)
	require.NoError(t, err)
	assert.Equal(t, "pre-close", got.NodeName)

	// Now it's empty and closed
	_, err = q.Dequeue(ctx)
	assert.Equal(t, ErrQueueClosed, err)
}

// ── DequeueBatch tests ─────────────────────────────────────────────────

func TestDequeueBatch(t *testing.T) {
	q := NewMemoryQueue(10, zap.NewNop())
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		q.Enqueue(ctx, &DataPoint{NodeName: "node"})
	}

	batch, err := q.DequeueBatch(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, batch, 3)
	assert.Equal(t, 2, q.Len(), "Should have 2 remaining")
}

func TestDequeueBatchLessThanMax(t *testing.T) {
	q := NewMemoryQueue(10, zap.NewNop())
	ctx := context.Background()

	q.Enqueue(ctx, &DataPoint{NodeName: "only"})

	batch, err := q.DequeueBatch(ctx, 10)
	require.NoError(t, err)
	assert.Len(t, batch, 1, "Should return available items even if less than max")
}

// ── Context cancellation tests ─────────────────────────────────────────

func TestDequeueContextCancelled(t *testing.T) {
	q := NewMemoryQueue(10, zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := q.Dequeue(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// ── Stats tests ────────────────────────────────────────────────────────

func TestStatsTracking(t *testing.T) {
	q := NewMemoryQueue(2, zap.NewNop())
	ctx := context.Background()

	q.Enqueue(ctx, &DataPoint{NodeName: "a"})
	q.Enqueue(ctx, &DataPoint{NodeName: "b"})
	q.Enqueue(ctx, &DataPoint{NodeName: "c"}) // drops a
	q.Dequeue(ctx)                            // dequeue b

	stats := q.Stats()
	assert.Equal(t, 1, stats.Current)
	assert.Equal(t, 2, stats.Capacity)
	assert.Equal(t, int64(3), stats.Enqueued)
	assert.Equal(t, int64(1), stats.Dequeued)
	assert.Equal(t, int64(1), stats.Dropped)
}

// ── Serialization tests ───────────────────────────────────────────────

func TestToBytesRoundTrip(t *testing.T) {
	dp := &DataPoint{
		NodeName:  "web-1",
		Timestamp: time.Now().Truncate(time.Millisecond),
		Metrics:   []MetricData{{Name: "cpu", Value: 95.5}},
		Logs:      []LogEntry{{Message: "error", Level: "error"}},
	}

	data, err := dp.ToBytes()
	require.NoError(t, err)

	got, err := FromBytes(data)
	require.NoError(t, err)
	assert.Equal(t, dp.NodeName, got.NodeName)
	assert.Len(t, got.Metrics, 1)
	assert.Equal(t, 95.5, got.Metrics[0].Value)
}

func TestFromBytesInvalidJSON(t *testing.T) {
	_, err := FromBytes([]byte("not-json"))
	require.Error(t, err)
}

// ── Factory tests ──────────────────────────────────────────────────────

func TestNewQueueDefault(t *testing.T) {
	q, err := NewQueue(DefaultConfig(), zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, q)
}

func TestNewQueueUnknownType(t *testing.T) {
	cfg := Config{Type: "unknown", MaxSize: 100}
	q, err := NewQueue(cfg, zap.NewNop())
	require.NoError(t, err, "Unknown type should fallback to memory queue")
	require.NotNil(t, q)
}

// ── Nil logger safety ─────────────────────────────────────────────────

func TestNewMemoryQueueNilLogger(t *testing.T) {
	q := NewMemoryQueue(10, nil)
	require.NotNil(t, q, "Should create dev logger if nil")
	require.NoError(t, q.Enqueue(context.Background(), &DataPoint{NodeName: "x"}))
}
