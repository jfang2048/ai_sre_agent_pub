// Package queue provides data ingestion and buffering for AI analysis.
//
// v0.5 intentionally keeps a single queue backend (in-memory bounded queue)
// to reduce runtime complexity and avoid half-implemented transport variants.
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// DataPoint represents a unified data point for AI analysis
type DataPoint struct {
	NodeName   string            `json:"node_name"`
	Timestamp  time.Time         `json:"timestamp"`
	Metrics    []MetricData      `json:"metrics"`
	Logs       []LogEntry        `json:"logs"`
	Context    map[string]string `json:"context"`
	K8sContext *K8sMetadata      `json:"k8s,omitempty"`
}

// MetricData represents a single metric measurement
type MetricData struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Timestamp time.Time         `json:"timestamp"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// LogEntry represents a log line
type LogEntry struct {
	Message   string            `json:"message"`
	Level     string            `json:"level"`
	Timestamp time.Time         `json:"timestamp"`
	Source    string            `json:"source"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// K8sMetadata provides Kubernetes context
type K8sMetadata struct {
	Namespace      string            `json:"namespace"`
	PodName        string            `json:"pod_name"`
	ContainerName  string            `json:"container_name"`
	NodeName       string            `json:"node_name"`
	DeploymentName string            `json:"deployment_name"`
	ServiceName    string            `json:"service_name"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
}

// Queue defines the interface for data queues
type Queue interface {
	// Enqueue adds a data point to the queue
	Enqueue(ctx context.Context, data *DataPoint) error

	// Dequeue retrieves data points from the queue (blocking)
	Dequeue(ctx context.Context) (*DataPoint, error)

	// DequeueBatch retrieves multiple data points
	DequeueBatch(ctx context.Context, maxItems int) ([]*DataPoint, error)

	// Len returns the current queue length
	Len() int

	// Close closes the queue
	Close() error
}

// Config holds queue configuration
type Config struct {
	// Queue type. v0.5 supports only "memory" (or empty for default).
	Type string `yaml:"type"`

	// In-memory queue settings
	MaxSize int `yaml:"max_size"`

	// Processing settings
	BatchSize    int           `yaml:"batch_size"`
	FlushTimeout time.Duration `yaml:"flush_timeout"`
}

// DefaultConfig returns default queue configuration
func DefaultConfig() Config {
	return Config{
		Type:         "memory",
		MaxSize:      10000,
		BatchSize:    100,
		FlushTimeout: 5 * time.Second,
	}
}

// ============================================================================
// In-Memory Queue Implementation
// ============================================================================

// MemoryQueue is a thread-safe in-memory bounded queue
type MemoryQueue struct {
	data     []*DataPoint
	maxSize  int
	mu       sync.Mutex
	closed   bool
	logger   *zap.Logger
	wakeCh   chan struct{}
	closedCh chan struct{}

	// Stats
	enqueued int64
	dequeued int64
	dropped  int64
}

// NewMemoryQueue creates a new in-memory queue
func NewMemoryQueue(maxSize int, logger *zap.Logger) *MemoryQueue {
	if logger == nil {
		logger, _ = zap.NewDevelopment()
	}
	if maxSize <= 0 {
		maxSize = DefaultConfig().MaxSize
	}

	q := &MemoryQueue{
		data:     make([]*DataPoint, 0, maxSize),
		maxSize:  maxSize,
		logger:   logger.With(zap.String("component", "memory_queue")),
		wakeCh:   make(chan struct{}, 1),
		closedCh: make(chan struct{}),
	}
	return q
}

func (q *MemoryQueue) notifyWaiters() {
	select {
	case q.wakeCh <- struct{}{}:
	default:
	}
}

// Enqueue adds a data point to the queue
func (q *MemoryQueue) Enqueue(ctx context.Context, data *DataPoint) error {
	q.mu.Lock()

	if q.closed {
		q.mu.Unlock()
		return ErrQueueClosed
	}

	// Drop oldest if at capacity (bounded queue behavior)
	if len(q.data) >= q.maxSize {
		q.data = q.data[1:]
		q.dropped++
		q.logger.Warn("queue full, dropping oldest data point",
			zap.Int64("dropped_total", q.dropped))
	}

	q.data = append(q.data, data)
	q.enqueued++
	q.mu.Unlock()
	q.notifyWaiters()
	return nil
}

// Dequeue retrieves a data point (blocking)
func (q *MemoryQueue) Dequeue(ctx context.Context) (*DataPoint, error) {
	for {
		q.mu.Lock()
		if len(q.data) > 0 {
			data := q.data[0]
			q.data = q.data[1:]
			q.dequeued++
			q.mu.Unlock()
			return data, nil
		}
		if q.closed {
			q.mu.Unlock()
			return nil, ErrQueueClosed
		}
		wakeCh := q.wakeCh
		closedCh := q.closedCh
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-closedCh:
		case <-wakeCh:
		}
	}
}

// DequeueBatch retrieves multiple data points
func (q *MemoryQueue) DequeueBatch(ctx context.Context, maxItems int) ([]*DataPoint, error) {
	if maxItems <= 0 {
		maxItems = 1
	}
	for {
		q.mu.Lock()
		if len(q.data) > 0 {
			count := len(q.data)
			if count > maxItems {
				count = maxItems
			}
			batch := make([]*DataPoint, count)
			copy(batch, q.data[:count])
			q.data = q.data[count:]
			q.dequeued += int64(count)
			q.mu.Unlock()
			return batch, nil
		}
		if q.closed {
			q.mu.Unlock()
			return nil, ErrQueueClosed
		}
		wakeCh := q.wakeCh
		closedCh := q.closedCh
		q.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-closedCh:
		case <-wakeCh:
		}
	}
}

// Len returns the current queue length
func (q *MemoryQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.data)
}

// Close closes the queue
func (q *MemoryQueue) Close() error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	close(q.closedCh)
	q.mu.Unlock()
	q.notifyWaiters()
	return nil
}

// Stats returns queue statistics
func (q *MemoryQueue) Stats() QueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()

	return QueueStats{
		Current:  len(q.data),
		Capacity: q.maxSize,
		Enqueued: q.enqueued,
		Dequeued: q.dequeued,
		Dropped:  q.dropped,
	}
}

// QueueStats contains queue statistics
type QueueStats struct {
	Current  int   `json:"current"`
	Capacity int   `json:"capacity"`
	Enqueued int64 `json:"enqueued"`
	Dequeued int64 `json:"dequeued"`
	Dropped  int64 `json:"dropped"`
}

// ============================================================================
// Queue Factory
// ============================================================================

// NewQueue creates a queue based on configuration
func NewQueue(cfg Config, logger *zap.Logger) (Queue, error) {
	switch cfg.Type {
	case "memory", "":
		return NewMemoryQueue(cfg.MaxSize, logger), nil

	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedQueueType, cfg.Type)
	}
}

// ============================================================================
// Serialization Helpers
// ============================================================================

// MarshalJSON serializes a DataPoint to JSON
func (d *DataPoint) MarshalJSON() ([]byte, error) {
	type Alias DataPoint
	return json.Marshal(&struct {
		*Alias
		Timestamp string `json:"timestamp"`
	}{
		Alias:     (*Alias)(d),
		Timestamp: d.Timestamp.Format(time.RFC3339Nano),
	})
}

// ToBytes serializes a data point for transport or disk persistence.
func (d *DataPoint) ToBytes() ([]byte, error) {
	return json.Marshal(d)
}

// FromBytes deserializes a DataPoint
func FromBytes(data []byte) (*DataPoint, error) {
	var dp DataPoint
	if err := json.Unmarshal(data, &dp); err != nil {
		return nil, err
	}
	return &dp, nil
}

// ============================================================================
// Errors
// ============================================================================

// Common queue errors
type QueueError string

func (e QueueError) Error() string { return string(e) }

const (
	ErrQueueClosed          = QueueError("queue is closed")
	ErrQueueFull            = QueueError("queue is full")
	ErrUnsupportedQueueType = QueueError("unsupported queue type")
)
