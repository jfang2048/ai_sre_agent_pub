package monitoring

import (
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/monitoring/sources"
	"go.uber.org/zap"
)

// SLIConfig configures SLI tracking
type SLIConfig struct {
	// Time window for SLI calculations
	RollingWindow time.Duration `yaml:"rolling_window"`

	// How long to keep historical data
	RetentionPeriod time.Duration `yaml:"retention_period"`
}

// SLIType represents different SLI types
type SLIType string

const (
	SLITypeAvailability SLIType = "availability"
	SLITypeLatency      SLIType = "latency"
	SLITypeThroughput   SLIType = "throughput"
	SLITypeErrorRate    SLIType = "error_rate"
	SLITypeSaturation   SLIType = "saturation"
)

// SLIDefinition defines an SLI
type SLIDefinition struct {
	ID          string  `yaml:"id"`
	Name        string  `yaml:"name"`
	Type        SLIType `yaml:"type"`
	Description string  `yaml:"description"`

	// Metric to track
	MetricName  string `yaml:"metric_name"`
	MetricQuery string `yaml:"metric_query"`

	// SLI-specific configuration
	// For latency: which percentile to track (e.g., p95, p99)
	LatencyPercentile float64 `yaml:"latency_percentile"`

	// For error rate: what constitutes an error
	ErrorThreshold float64 `yaml:"error_threshold"`

	// For availability: success criteria
	SuccessCriteria string `yaml:"success_criteria"`

	Labels map[string]string `yaml:"labels"`
}

// SLIValue represents a calculated SLI value
type SLIValue struct {
	SLIID     string    `json:"sli_id"`
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"` // 0-1 for rate-based SLIs
	Valid     bool      `json:"valid"` // Whether enough data was available

	// Additional data
	EventCount int           `json:"event_count"`
	Window     time.Duration `json:"window"`
}

// SLIResult represents the result of an SLI evaluation
type SLIResult struct {
	SLIID       string            `json:"sli_id"`
	SLIName     string            `json:"sli_name"`
	Timestamp   time.Time         `json:"timestamp"`
	Value       float64           `json:"value"`
	Valid       bool              `json:"valid"`
	Labels      map[string]string `json:"labels"`
	Window      time.Duration     `json:"window"`
	GoodEvents  int               `json:"good_events"`
	TotalEvents int               `json:"total_events"`
}

// SLITracker tracks Service Level Indicators
type SLITracker struct {
	config SLIConfig
	logger *zap.Logger

	// SLI definitions
	slis map[string]*SLIDefinition

	// Time-series data for each SLI
	data map[string]*TimeSeriesBuffer

	mu sync.RWMutex
}

// TimeSeriesBuffer stores time-series data for SLI calculations
type TimeSeriesBuffer struct {
	points  []DataPoint
	maxSize int
	window  time.Duration
	mu      sync.Mutex
}

// DataPoint represents a single metric data point
type DataPoint struct {
	Timestamp time.Time
	Value     float64
	Labels    map[string]string
}

// NewSLITracker creates a new SLI tracker
func NewSLITracker(config SLIConfig, logger *zap.Logger) *SLITracker {
	if config.RollingWindow == 0 {
		config.RollingWindow = 1 * time.Hour
	}
	if config.RetentionPeriod == 0 {
		config.RetentionPeriod = 7 * 24 * time.Hour
	}

	return &SLITracker{
		config: config,
		logger: logger.With(zap.String("component", "sli_tracker")),
		slis:   make(map[string]*SLIDefinition),
		data:   make(map[string]*TimeSeriesBuffer),
	}
}

// RegisterSLI registers a new SLI definition
func (t *SLITracker) RegisterSLI(sli *SLIDefinition) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.slis[sli.ID] = sli
	t.data[sli.ID] = NewTimeSeriesBuffer(int(t.config.RetentionPeriod.Seconds()), t.config.RollingWindow)

	t.logger.Info("registered SLI",
		zap.String("id", sli.ID),
		zap.String("name", sli.Name),
		zap.String("type", string(sli.Type)))
}

// RecordMetric records a metric value for SLI tracking
func (t *SLITracker) RecordMetric(metric sources.Metric) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Find matching SLIs
	for sliID, sli := range t.slis {
		if t.matchesSLI(metric, sli) {
			if buffer, ok := t.data[sliID]; ok {
				buffer.Add(DataPoint{
					Timestamp: metric.Timestamp,
					Value:     metric.Value,
					Labels:    metric.Labels,
				})
			}
		}
	}
}

// matchesSLI checks if a metric matches an SLI definition
func (t *SLITracker) matchesSLI(metric sources.Metric, sli *SLIDefinition) bool {
	// Check metric name
	if sli.MetricName != "" && metric.Name != sli.MetricName {
		return false
	}

	// Check labels
	for k, v := range sli.Labels {
		if metricVal, ok := metric.Labels[k]; !ok || metricVal != v {
			return false
		}
	}

	return true
}

// Calculate calculates the current SLI value
func (t *SLITracker) Calculate(sliID string) *SLIValue {
	t.mu.RLock()
	defer t.mu.RUnlock()

	sli, ok := t.slis[sliID]
	if !ok {
		t.logger.Warn("SLI not found", zap.String("id", sliID))
		return &SLIValue{SLIID: sliID, Valid: false}
	}

	buffer, ok := t.data[sliID]
	if !ok {
		return &SLIValue{SLIID: sliID, Valid: false}
	}

	// Get data points in the rolling window
	points := buffer.GetInWindow(t.config.RollingWindow)
	if len(points) == 0 {
		return &SLIValue{
			SLIID:     sliID,
			Timestamp: time.Now(),
			Valid:     false,
		}
	}

	// Calculate SLI based on type
	var value float64
	switch sli.Type {
	case SLITypeAvailability:
		value = t.calculateAvailability(points)
	case SLITypeLatency:
		value = t.calculateLatency(points, sli.LatencyPercentile)
	case SLITypeErrorRate:
		value = t.calculateErrorRate(points)
	case SLITypeThroughput:
		value = t.calculateThroughput(points)
	default:
		value = 0
	}

	return &SLIValue{
		SLIID:      sliID,
		Timestamp:  time.Now(),
		Value:      value,
		Valid:      true,
		EventCount: len(points),
		Window:     t.config.RollingWindow,
	}
}

// calculateAvailability calculates availability SLI
func (t *SLITracker) calculateAvailability(points []DataPoint) float64 {
	if len(points) == 0 {
		return 0
	}

	successful := 0.0
	total := float64(len(points))

	for _, p := range points {
		// Assume success is any value > 0
		if p.Value > 0 {
			successful++
		}
	}

	return successful / total
}

// calculateLatency calculates latency SLI at a given percentile
func (t *SLITracker) calculateLatency(points []DataPoint, percentile float64) float64 {
	if len(points) == 0 {
		return 0
	}

	// Sort values
	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = p.Value
	}

	// Simple percentile calculation (not efficient for large datasets)
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[i] > values[j] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}

	index := int(float64(len(values)-1) * percentile / 100.0)
	return values[index]
}

// calculateErrorRate calculates error rate SLI
func (t *SLITracker) calculateErrorRate(points []DataPoint) float64 {
	if len(points) == 0 {
		return 0
	}

	errors := 0.0
	total := float64(len(points))

	for _, p := range points {
		// Assume errors are values >= 500 (HTTP status codes)
		// This is a simplification - real implementation would be configurable
		if p.Value >= 500 {
			errors++
		}
	}

	return errors / total
}

// calculateThroughput calculates throughput SLI
func (t *SLITracker) calculateThroughput(points []DataPoint) float64 {
	if len(points) == 0 {
		return 0
	}

	sum := 0.0
	for _, p := range points {
		sum += p.Value
	}

	return sum / float64(len(points))
}

// GetAllSLIs returns all registered SLIs
func (t *SLITracker) GetAllSLIs() []*SLIDefinition {
	t.mu.RLock()
	defer t.mu.RUnlock()

	slis := make([]*SLIDefinition, 0, len(t.slis))
	for _, sli := range t.slis {
		slis = append(slis, sli)
	}
	return slis
}

// GetSLI returns a specific SLI definition
func (t *SLITracker) GetSLI(id string) (*SLIDefinition, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	sli, ok := t.slis[id]
	return sli, ok
}

// NewTimeSeriesBuffer creates a new time-series buffer
func NewTimeSeriesBuffer(maxSize int, window time.Duration) *TimeSeriesBuffer {
	return &TimeSeriesBuffer{
		points:  make([]DataPoint, 0, maxSize),
		maxSize: maxSize,
		window:  window,
	}
}

// Add adds a data point to the buffer
func (b *TimeSeriesBuffer) Add(point DataPoint) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.points = append(b.points, point)

	// Trim old points
	if len(b.points) > b.maxSize {
		b.points = b.points[len(b.points)-b.maxSize:]
	}

	// Also trim by time window
	if b.window > 0 {
		cutoff := time.Now().Add(-b.window)
		for i, p := range b.points {
			if p.Timestamp.After(cutoff) {
				b.points = b.points[i:]
				break
			}
		}
	}
}

// GetInWindow returns points within the time window
func (b *TimeSeriesBuffer) GetInWindow(window time.Duration) []DataPoint {
	b.mu.Lock()
	defer b.mu.Unlock()

	if window <= 0 {
		return b.points
	}

	cutoff := time.Now().Add(-window)
	result := make([]DataPoint, 0)

	for _, p := range b.points {
		if p.Timestamp.After(cutoff) {
			result = append(result, p)
		}
	}

	return result
}

// Latest returns the latest data point
func (b *TimeSeriesBuffer) Latest() *DataPoint {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.points) == 0 {
		return nil
	}
	return &b.points[len(b.points)-1]
}
