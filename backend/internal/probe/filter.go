package probe

import (
	"sort"
	"strings"
	"sync"
)

// MetricsFilter safeguards against noisy data and outliers
type MetricsFilter struct {
	mu           sync.Mutex
	lastValues   map[string]float64
	emaValues    map[string]float64
	alpha        float64 // Smoothing factor (0.1 - 1.0)
	outlierLimit float64 // Multiplier for jump detection
}

// NewMetricsFilter creates a filter with EMA smoothing
// alpha: 1.0 = no smoothing, 0.1 = heavy smoothing
func NewMetricsFilter(alpha float64) *MetricsFilter {
	if alpha <= 0 {
		alpha = 0.5
	}
	if alpha > 1 {
		alpha = 1.0
	}
	return &MetricsFilter{
		lastValues:   make(map[string]float64),
		emaValues:    make(map[string]float64),
		alpha:        alpha,
		outlierLimit: 5.0,
	}
}

// Apply runs the filter on a batch of metrics
func (f *MetricsFilter) Apply(batch *MetricBatch) *MetricBatch {
	if batch == nil {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	filtered := make([]Metric, 0, len(batch.Metrics))

	for _, m := range batch.Metrics {
		// Only filter Gauges. Counters must remain monotonic and exact.
		if m.Type != "gauge" {
			filtered = append(filtered, m)
			continue
		}

		key := m.Name + "|" + labelsToKey(m.Labels)

		// 1. Outlier Check (Basic)
		// If value leaps > 5x instantly, reject/dampen it
		if last, ok := f.lastValues[key]; ok && last > 0 {
			if m.Value > last*f.outlierLimit || m.Value < last/f.outlierLimit {
				// Use previous EMA instead of this spike if available
				if ema, ok := f.emaValues[key]; ok {
					m.Value = ema
				} else {
					m.Value = last
				}
			}
		}

		f.lastValues[key] = m.Value

		// 2. EMA Smoothing
		// EMA_t = alpha * x_t + (1-alpha) * EMA_t-1
		if prevEMA, ok := f.emaValues[key]; ok {
			newEMA := f.alpha*m.Value + (1-f.alpha)*prevEMA
			f.emaValues[key] = newEMA
			m.Value = newEMA
		} else {
			f.emaValues[key] = m.Value
		}

		filtered = append(filtered, m)
	}

	return &MetricBatch{
		Metrics:     filtered,
		CollectedAt: batch.CollectedAt,
		Hostname:    batch.Hostname,
	}
}

func labelsToKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(labels[k])
		sb.WriteString(";")
	}
	return sb.String()
}
