package core

import (
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collections/ring"
)

// MetricsHistory stores historical metrics for charts
type MetricsHistory struct {
	mu   sync.RWMutex
	ring *ring.Ring[MetricsSample]
}

// MetricsSample represents a snapshot of metrics at a point in time
type MetricsSample struct {
	Timestamp time.Time
	Metrics   map[string]float64
}

// NewMetricsHistory creates a new metrics history store
func NewMetricsHistory(maxSize int) *MetricsHistory {
	return &MetricsHistory{
		ring: ring.New[MetricsSample](maxSize),
	}
}

// Add adds a new metrics sample
func (h *MetricsHistory) Add(metrics map[string]float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	sample := MetricsSample{
		Timestamp: time.Now(),
		Metrics:   make(map[string]float64, len(metrics)),
	}
	for k, v := range metrics {
		sample.Metrics[k] = v
	}

	h.ring.Push(sample)
}

// GetSince returns samples since the given time
func (h *MetricsHistory) GetSince(since time.Time) []MetricsSample {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]MetricsSample, 0, h.ring.Len())
	h.ring.ForEachOldest(func(s MetricsSample) {
		if s.Timestamp.After(since) {
			out = append(out, s)
		}
	})
	return out
}

// GetLastN returns the last N samples
func (h *MetricsHistory) GetLastN(n int) []MetricsSample {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := h.ring.SliceLastN(n)
	if out == nil {
		return []MetricsSample{}
	}
	return out
}

// GetMetricHistory returns history for a specific metric
func (h *MetricsHistory) GetMetricHistory(metricName string, since time.Time) []MetricPoint {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]MetricPoint, 0)
	h.ring.ForEachOldest(func(sample MetricsSample) {
		if !sample.Timestamp.After(since) {
			return
		}
		if val, ok := sample.Metrics[metricName]; ok {
			result = append(result, MetricPoint{
				Timestamp: sample.Timestamp,
				Value:     val,
			})
		}
	})
	return result
}

// MetricPoint represents a single metric value at a point in time
type MetricPoint struct {
	Timestamp time.Time
	Value     float64
}
