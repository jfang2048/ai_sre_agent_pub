// Package probe implements sliding window aggregation for metrics.
// This provides min/max/avg/percentile statistics over configurable time windows.
package probe

import (
	"math"
	"sort"
	"sync"
	"time"
)

// WindowStats holds aggregated statistics for a metric over a time window
type WindowStats struct {
	Name       string            `json:"name"`
	Labels     map[string]string `json:"labels,omitempty"`
	Window     string            `json:"window"` // "1m", "5m", "15m"
	Count      int               `json:"count"`
	Min        float64           `json:"min"`
	Max        float64           `json:"max"`
	Sum        float64           `json:"sum"`
	Avg        float64           `json:"avg"`
	P50        float64           `json:"p50"`
	P95        float64           `json:"p95"`
	P99        float64           `json:"p99"`
	StdDev     float64           `json:"stddev"`
	LastUpdate time.Time         `json:"last_update"`
}

// Sample represents a single metric sample with timestamp
type Sample struct {
	Value     float64
	Timestamp time.Time
}

// MetricWindow maintains samples for a single metric over time
type MetricWindow struct {
	Key     string
	Name    string
	Labels  map[string]string
	Samples []Sample
	MaxAge  time.Duration
}

// Aggregator maintains sliding windows for all metrics
type Aggregator struct {
	mu      sync.RWMutex
	windows map[string]*MetricWindow

	// Window configurations
	windowDurations []time.Duration
	maxSamples      int
}

// NewAggregator creates a new metrics aggregator
func NewAggregator() *Aggregator {
	return &Aggregator{
		windows: make(map[string]*MetricWindow),
		windowDurations: []time.Duration{
			1 * time.Minute,
			5 * time.Minute,
			15 * time.Minute,
		},
		maxSamples: 1000, // Maximum samples per metric
	}
}

// Add adds a metric sample to the aggregator
func (a *Aggregator) Add(m Metric) {
	key := makeKey(m.Name, m.Labels)

	a.mu.Lock()
	defer a.mu.Unlock()

	window, ok := a.windows[key]
	if !ok {
		window = &MetricWindow{
			Key:     key,
			Name:    m.Name,
			Labels:  m.Labels,
			Samples: make([]Sample, 0, a.maxSamples),
			MaxAge:  15 * time.Minute, // Keep up to 15 minutes of data
		}
		a.windows[key] = window
	}

	// Add sample
	window.Samples = append(window.Samples, Sample{
		Value:     m.Value,
		Timestamp: m.Timestamp,
	})

	// Trim old samples
	window.trim()
}

// AddBatch adds multiple metrics at once
func (a *Aggregator) AddBatch(metrics []Metric) {
	for _, m := range metrics {
		a.Add(m)
	}
}

// GetStats returns aggregated statistics for all metrics
func (a *Aggregator) GetStats(windowDuration time.Duration) []WindowStats {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var stats []WindowStats
	now := time.Now()
	cutoff := now.Add(-windowDuration)

	windowStr := formatDuration(windowDuration)

	for _, window := range a.windows {
		// Filter samples within window
		var values []float64
		for _, s := range window.Samples {
			if s.Timestamp.After(cutoff) {
				values = append(values, s.Value)
			}
		}

		if len(values) == 0 {
			continue
		}

		ws := WindowStats{
			Name:       window.Name,
			Labels:     window.Labels,
			Window:     windowStr,
			Count:      len(values),
			LastUpdate: now,
		}

		// Calculate statistics
		ws.Min, ws.Max, ws.Sum = minMaxSum(values)
		ws.Avg = ws.Sum / float64(len(values))
		ws.StdDev = stdDev(values, ws.Avg)
		ws.P50, ws.P95, ws.P99 = percentiles(values)

		stats = append(stats, ws)
	}

	return stats
}

// GetAggregatedMetrics returns metrics with window suffixes for Prometheus
func (a *Aggregator) GetAggregatedMetrics(now time.Time) []Metric {
	var metrics []Metric

	for _, duration := range a.windowDurations {
		windowStr := formatDuration(duration)
		stats := a.GetStats(duration)

		for _, s := range stats {
			baseName := s.Name
			labels := copyLabels(s.Labels)
			labels["window"] = windowStr

			// Min
			metrics = append(metrics, Metric{
				Name:      baseName + "_min",
				Type:      "gauge",
				Value:     s.Min,
				Labels:    copyLabels(labels),
				Timestamp: now,
			})

			// Max
			metrics = append(metrics, Metric{
				Name:      baseName + "_max",
				Type:      "gauge",
				Value:     s.Max,
				Labels:    copyLabels(labels),
				Timestamp: now,
			})

			// Avg
			metrics = append(metrics, Metric{
				Name:      baseName + "_avg",
				Type:      "gauge",
				Value:     s.Avg,
				Labels:    copyLabels(labels),
				Timestamp: now,
			})

			// P95
			metrics = append(metrics, Metric{
				Name:      baseName + "_p95",
				Type:      "gauge",
				Value:     s.P95,
				Labels:    copyLabels(labels),
				Timestamp: now,
			})

			// P99
			metrics = append(metrics, Metric{
				Name:      baseName + "_p99",
				Type:      "gauge",
				Value:     s.P99,
				Labels:    copyLabels(labels),
				Timestamp: now,
			})
		}
	}

	return metrics
}

// Clear removes all samples
func (a *Aggregator) Clear() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.windows = make(map[string]*MetricWindow)
}

// trim removes samples older than MaxAge
func (w *MetricWindow) trim() {
	cutoff := time.Now().Add(-w.MaxAge)

	// Find first valid sample
	validIdx := 0
	for i, s := range w.Samples {
		if s.Timestamp.After(cutoff) {
			validIdx = i
			break
		}
	}

	if validIdx > 0 {
		w.Samples = w.Samples[validIdx:]
	}

	// Also limit total samples
	if len(w.Samples) > 1000 {
		w.Samples = w.Samples[len(w.Samples)-1000:]
	}
}

// makeKey creates a unique key for a metric with labels
func makeKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}

	// Sort label keys for consistent ordering
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	key := name
	for _, k := range keys {
		key += "|" + k + "=" + labels[k]
	}
	return key
}

// formatDuration formats a duration as a short string
func formatDuration(d time.Duration) string {
	switch d {
	case 1 * time.Minute:
		return "1m"
	case 5 * time.Minute:
		return "5m"
	case 15 * time.Minute:
		return "15m"
	case 1 * time.Hour:
		return "1h"
	default:
		return d.String()
	}
}

// minMaxSum calculates min, max, and sum of values
func minMaxSum(values []float64) (min, max, sum float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}

	min = values[0]
	max = values[0]
	sum = 0

	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
	}

	return min, max, sum
}

// stdDev calculates standard deviation
func stdDev(values []float64, mean float64) float64 {
	if len(values) < 2 {
		return 0
	}

	var sumSq float64
	for _, v := range values {
		diff := v - mean
		sumSq += diff * diff
	}

	return math.Sqrt(sumSq / float64(len(values)))
}

// percentiles calculates p50, p95, p99 percentiles
func percentiles(values []float64) (p50, p95, p99 float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}

	// Sort a copy
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	p50 = percentile(sorted, 0.50)
	p95 = percentile(sorted, 0.95)
	p99 = percentile(sorted, 0.99)

	return p50, p95, p99
}

// percentile calculates a single percentile from sorted values
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}

	rank := p * float64(len(sorted)-1)
	lower := int(rank)
	upper := lower + 1

	if upper >= len(sorted) {
		return sorted[len(sorted)-1]
	}

	frac := rank - float64(lower)
	return sorted[lower] + frac*(sorted[upper]-sorted[lower])
}

// copyLabels creates a copy of a labels map
func copyLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	copy := make(map[string]string, len(labels))
	for k, v := range labels {
		copy[k] = v
	}
	return copy
}
