package utils

import (
	"sync"
	"time"
)

// MetricType represents the type of a metric
type MetricType int

const (
	MetricTypeGauge MetricType = iota
	MetricTypeCounter
	MetricTypeHistogram
	MetricTypeSummary
)

// MetricValue represents a single metric value
type MetricValue struct {
	Name      string
	Type      MetricType
	Value     float64
	Timestamp time.Time
	Labels    map[string]string
}

// MetricCollector collects and stores metrics
type MetricCollector struct {
	mu      sync.RWMutex
	metrics map[string]*MetricFamily
}

// MetricFamily represents a family of metrics with the same name
type MetricFamily struct {
	Name    string
	Type    MetricType
	Help    string
	metrics []*MetricValue
}

// NewMetricCollector creates a new metric collector
func NewMetricCollector() *MetricCollector {
	return &MetricCollector{
		metrics: make(map[string]*MetricFamily),
	}
}

// Register registers a new metric family
func (mc *MetricCollector) Register(name string, typ MetricType, help string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if _, exists := mc.metrics[name]; !exists {
		mc.metrics[name] = &MetricFamily{
			Name:    name,
			Type:    typ,
			Help:    help,
			metrics: make([]*MetricValue, 0),
		}
	}
}

// Set sets a gauge metric value
func (mc *MetricCollector) Set(name string, value float64, labels map[string]string) {
	mc.record(name, MetricTypeGauge, value, labels)
}

// Inc increments a counter metric
func (mc *MetricCollector) Inc(name string, value float64, labels map[string]string) {
	mc.record(name, MetricTypeCounter, value, labels)
}

// Observe records an observation for a histogram/summary
func (mc *MetricCollector) Observe(name string, value float64, labels map[string]string) {
	mc.record(name, MetricTypeHistogram, value, labels)
}

func (mc *MetricCollector) record(name string, typ MetricType, value float64, labels map[string]string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	family, exists := mc.metrics[name]
	if !exists {
		family = &MetricFamily{
			Name:    name,
			Type:    typ,
			metrics: make([]*MetricValue, 0),
		}
		mc.metrics[name] = family
	}

	metric := &MetricValue{
		Name:      name,
		Type:      typ,
		Value:     value,
		Timestamp: time.Now(),
		Labels:    labels,
	}

	// For gauges, replace existing metric with same labels
	if typ == MetricTypeGauge {
		for i, m := range family.metrics {
			if labelsEqual(m.Labels, labels) {
				family.metrics[i] = metric
				return
			}
		}
	}

	family.metrics = append(family.metrics, metric)
}

// Get retrieves metric values for a given name
func (mc *MetricCollector) Get(name string) []*MetricValue {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if family, exists := mc.metrics[name]; exists {
		result := make([]*MetricValue, len(family.metrics))
		copy(result, family.metrics)
		return result
	}
	return nil
}

// GetAll returns all metrics
func (mc *MetricCollector) GetAll() map[string]*MetricFamily {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make(map[string]*MetricFamily, len(mc.metrics))
	for k, v := range mc.metrics {
		familyCopy := &MetricFamily{
			Name:    v.Name,
			Type:    v.Type,
			Help:    v.Help,
			metrics: make([]*MetricValue, len(v.metrics)),
		}
		copy(familyCopy.metrics, v.metrics)
		result[k] = familyCopy
	}
	return result
}

// Clear clears all metrics
func (mc *MetricCollector) Clear() {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.metrics = make(map[string]*MetricFamily)
}

// labelsEqual compares two label maps
func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// TimeSeries represents a time series of metric values
type TimeSeries struct {
	Points   []DataPoint
	Labels   map[string]string
	Metadata map[string]string
}

// DataPoint represents a single data point in a time series
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// TimeSeriesBuffer stores time series data
type TimeSeriesBuffer struct {
	mu        sync.RWMutex
	series    map[string]*TimeSeries
	maxPoints int
	retention time.Duration
}

// NewTimeSeriesBuffer creates a new time series buffer
func NewTimeSeriesBuffer(maxPoints int, retention time.Duration) *TimeSeriesBuffer {
	return &TimeSeriesBuffer{
		series:    make(map[string]*TimeSeries),
		maxPoints: maxPoints,
		retention: retention,
	}
}

// Add adds a data point to a time series
func (tsb *TimeSeriesBuffer) Add(name string, value float64, labels map[string]string) {
	tsb.mu.Lock()
	defer tsb.mu.Unlock()

	key := makeKey(name, labels)
	series, exists := tsb.series[key]
	if !exists {
		series = &TimeSeries{
			Points:   make([]DataPoint, 0, tsb.maxPoints),
			Labels:   labels,
			Metadata: make(map[string]string),
		}
		tsb.series[key] = series
	}

	point := DataPoint{
		Timestamp: time.Now(),
		Value:     value,
	}

	series.Points = append(series.Points, point)

	// Trim old points
	if tsb.maxPoints > 0 && len(series.Points) > tsb.maxPoints {
		series.Points = series.Points[len(series.Points)-tsb.maxPoints:]
	}

	// Remove points outside retention window
	if tsb.retention > 0 {
		cutoff := time.Now().Add(-tsb.retention)
		for i, p := range series.Points {
			if p.Timestamp.After(cutoff) {
				series.Points = series.Points[i:]
				break
			}
		}
	}
}

// Get retrieves a time series
func (tsb *TimeSeriesBuffer) Get(name string, labels map[string]string) *TimeSeries {
	tsb.mu.RLock()
	defer tsb.mu.RUnlock()

	key := makeKey(name, labels)
	if series, exists := tsb.series[key]; exists {
		// Return a copy
		copy := &TimeSeries{
			Points:   make([]DataPoint, len(series.Points)),
			Labels:   make(map[string]string),
			Metadata: make(map[string]string),
		}
		copy.Points[0] = series.Points[0]
		for k, v := range series.Labels {
			copy.Labels[k] = v
		}
		for k, v := range series.Metadata {
			copy.Metadata[k] = v
		}
		return copy
	}
	return nil
}

// Query retrieves time series data within a time range
func (tsb *TimeSeriesBuffer) Query(name string, labels map[string]string, start, end time.Time) []DataPoint {
	tsb.mu.RLock()
	defer tsb.mu.RUnlock()

	key := makeKey(name, labels)
	series, exists := tsb.series[key]
	if !exists {
		return nil
	}

	var result []DataPoint
	for _, p := range series.Points {
		if (p.Timestamp.Equal(start) || p.Timestamp.After(start)) &&
			(p.Timestamp.Equal(end) || p.Timestamp.Before(end)) {
			result = append(result, p)
		}
	}
	return result
}

// makeKey creates a unique key from name and labels
func makeKey(name string, labels map[string]string) string {
	key := name
	for k, v := range labels {
		key += ";" + k + "=" + v
	}
	return key
}
