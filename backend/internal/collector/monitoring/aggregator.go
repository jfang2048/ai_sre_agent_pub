package monitoring

import (
	"sync"
	"time"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
)

// Aggregator aggregates metrics over time.
type Aggregator struct {
	mu      sync.Mutex
	metrics map[string]*proto.Metric
	window  time.Duration
}

// NewAggregator creates a new Aggregator.
func NewAggregator(window time.Duration) *Aggregator {
	return &Aggregator{
		metrics: make(map[string]*proto.Metric),
		window:  window,
	}
}

// Add adds a metric to the aggregation.
func (a *Aggregator) Add(metric *proto.Metric) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Keep the latest sample per metric name within the current flush window.
	a.metrics[metric.Name] = metric
}

// Flush returns the aggregated metrics and clears the state.
func (a *Aggregator) Flush() []*proto.Metric {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]*proto.Metric, 0, len(a.metrics))
	for _, m := range a.metrics {
		result = append(result, m)
	}
	// Clear map
	a.metrics = make(map[string]*proto.Metric)
	return result
}
