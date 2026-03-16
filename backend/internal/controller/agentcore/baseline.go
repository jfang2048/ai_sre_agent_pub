package agent

import (
	"encoding/json"
	"math"
	"os"
	"sort"
	"sync"
	"time"
)

// BaselineDrift represents a detected deviation from the behavioral baseline.
type BaselineDrift struct {
	CollectorID string    `json:"collector_id"`
	Dimension   string    `json:"dimension"`
	Metric      string    `json:"metric"`
	Current     float64   `json:"current"`
	Baseline    float64   `json:"baseline"`
	Deviation   float64   `json:"deviation"`
	Percentile  float64   `json:"percentile"`
	Severity    string    `json:"severity"`
	DetectedAt  time.Time `json:"detected_at"`
}

// BaselineConfig configures the baseline engine.
type BaselineConfig struct {
	WindowMinutes     int     `json:"baseline_window_minutes" yaml:"baseline_window_minutes"`
	P95Threshold      float64 `json:"p95_threshold" yaml:"p95_threshold"`
	P99Threshold      float64 `json:"p99_threshold" yaml:"p99_threshold"`
	DriftMinSeverity  float64 `json:"drift_min_severity" yaml:"drift_min_severity"`
	MaxSamplesPerHost int     `json:"max_samples_per_host" yaml:"max_samples_per_host"`
}

// DefaultBaselineConfig returns safe defaults.
func DefaultBaselineConfig() BaselineConfig {
	return BaselineConfig{
		WindowMinutes:     60,
		P95Threshold:      1.5,
		P99Threshold:      2.5,
		DriftMinSeverity:  0.1,
		MaxSamplesPerHost: 1000,
	}
}

type baselineSample struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type hostBaseline struct {
	ProcessFrequency     []baselineSample            `json:"process_frequency"`
	PortExposure         map[int]int                 `json:"port_exposure"`
	OutboundDestClusters map[string]int              `json:"outbound_dest_clusters"`
	SyscallRates         map[string][]baselineSample `json:"syscall_rates"`
	FileAccessPatterns   map[string]int              `json:"file_access_patterns"`
	MetricSeries         map[string][]baselineSample `json:"metric_series"`
}

// BaselineEngine maintains per-host rolling behavioral baselines.
type BaselineEngine struct {
	mu        sync.RWMutex
	cfg       BaselineConfig
	baselines map[string]*hostBaseline
}

// NewBaselineEngine creates a baseline engine with the given config.
func NewBaselineEngine(cfg BaselineConfig) *BaselineEngine {
	if cfg.WindowMinutes <= 0 {
		cfg.WindowMinutes = 60
	}
	if cfg.MaxSamplesPerHost <= 0 {
		cfg.MaxSamplesPerHost = 1000
	}
	if cfg.P95Threshold <= 0 {
		cfg.P95Threshold = 1.5
	}
	if cfg.P99Threshold <= 0 {
		cfg.P99Threshold = 2.5
	}
	return &BaselineEngine{
		cfg:       cfg,
		baselines: make(map[string]*hostBaseline),
	}
}

// RecordProcessFrequency records a process spawn frequency sample.
func (be *BaselineEngine) RecordProcessFrequency(collectorID string, value float64, ts time.Time) {
	be.mu.Lock()
	defer be.mu.Unlock()
	hb := be.ensureHost(collectorID)
	hb.ProcessFrequency = appendBoundedSample(hb.ProcessFrequency, baselineSample{ts, value}, be.cfg.MaxSamplesPerHost)
}

// RecordPortExposure records an observed listening port.
func (be *BaselineEngine) RecordPortExposure(collectorID string, port int) {
	be.mu.Lock()
	defer be.mu.Unlock()
	be.ensureHost(collectorID).PortExposure[port]++
}

// RecordOutboundDestination records an outbound connection destination.
func (be *BaselineEngine) RecordOutboundDestination(collectorID, dest string) {
	be.mu.Lock()
	defer be.mu.Unlock()
	be.ensureHost(collectorID).OutboundDestClusters[dest]++
}

// RecordSyscallRate records a syscall rate sample.
func (be *BaselineEngine) RecordSyscallRate(collectorID, syscall string, rate float64, ts time.Time) {
	be.mu.Lock()
	defer be.mu.Unlock()
	hb := be.ensureHost(collectorID)
	hb.SyscallRates[syscall] = appendBoundedSample(hb.SyscallRates[syscall], baselineSample{ts, rate}, be.cfg.MaxSamplesPerHost)
}

// RecordFileAccess records a file access event.
func (be *BaselineEngine) RecordFileAccess(collectorID, path string) {
	be.mu.Lock()
	defer be.mu.Unlock()
	be.ensureHost(collectorID).FileAccessPatterns[path]++
}

// RecordMetric records a generic metric sample for baseline comparison.
func (be *BaselineEngine) RecordMetric(collectorID, metric string, value float64, ts time.Time) {
	be.mu.Lock()
	defer be.mu.Unlock()
	hb := be.ensureHost(collectorID)
	hb.MetricSeries[metric] = appendBoundedSample(hb.MetricSeries[metric], baselineSample{ts, value}, be.cfg.MaxSamplesPerHost)
}

// DetectDrift computes baseline drift for a given collector.
func (be *BaselineEngine) DetectDrift(collectorID string) []BaselineDrift {
	be.mu.RLock()
	defer be.mu.RUnlock()

	hb, ok := be.baselines[collectorID]
	if !ok {
		return nil
	}

	now := time.Now()
	cutoff := now.Add(-time.Duration(be.cfg.WindowMinutes) * time.Minute)
	var drifts []BaselineDrift

	drifts = append(drifts, be.computeSeriesDrift(collectorID, "process_frequency", "spawn_rate", hb.ProcessFrequency, cutoff)...)
	for syscall, series := range hb.SyscallRates {
		drifts = append(drifts, be.computeSeriesDrift(collectorID, "syscall_rate", syscall, series, cutoff)...)
	}
	for metric, series := range hb.MetricSeries {
		drifts = append(drifts, be.computeSeriesDrift(collectorID, "metric", metric, series, cutoff)...)
	}
	return drifts
}

// DetectNewPorts returns ports not previously seen in baseline.
func (be *BaselineEngine) DetectNewPorts(collectorID string, currentPorts []int) []int {
	be.mu.RLock()
	defer be.mu.RUnlock()

	hb, ok := be.baselines[collectorID]
	if !ok || hb.PortExposure == nil {
		return currentPorts
	}
	var newPorts []int
	for _, port := range currentPorts {
		if _, known := hb.PortExposure[port]; !known {
			newPorts = append(newPorts, port)
		}
	}
	return newPorts
}

// DetectNewDestinations returns outbound destinations not seen in baseline.
func (be *BaselineEngine) DetectNewDestinations(collectorID string, currentDests []string) []string {
	be.mu.RLock()
	defer be.mu.RUnlock()

	hb, ok := be.baselines[collectorID]
	if !ok || hb.OutboundDestClusters == nil {
		return currentDests
	}
	var newDests []string
	for _, dest := range currentDests {
		if _, known := hb.OutboundDestClusters[dest]; !known {
			newDests = append(newDests, dest)
		}
	}
	return newDests
}

// SaveBaseline persists all baselines to a JSON file.
func (be *BaselineEngine) SaveBaseline(path string) error {
	be.mu.RLock()
	defer be.mu.RUnlock()
	data, err := json.Marshal(be.baselines)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadBaseline loads baselines from a JSON file.
func (be *BaselineEngine) LoadBaseline(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	be.mu.Lock()
	defer be.mu.Unlock()

	var loaded map[string]*hostBaseline
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	be.baselines = loaded
	return nil
}

// CollectorIDs returns all collector IDs with baselines.
func (be *BaselineEngine) CollectorIDs() []string {
	be.mu.RLock()
	defer be.mu.RUnlock()
	ids := make([]string, 0, len(be.baselines))
	for id := range be.baselines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Ready returns true if at least one baseline has sufficient data.
func (be *BaselineEngine) Ready() bool {
	be.mu.RLock()
	defer be.mu.RUnlock()
	for _, hb := range be.baselines {
		if len(hb.ProcessFrequency) >= 5 || len(hb.MetricSeries) > 0 {
			return true
		}
	}
	return false
}

// ensureHost returns the hostBaseline for a collector, creating it if absent.
// Caller must hold be.mu write lock.
func (be *BaselineEngine) ensureHost(collectorID string) *hostBaseline {
	hb, ok := be.baselines[collectorID]
	if ok {
		return hb
	}
	hb = &hostBaseline{
		PortExposure:         make(map[int]int),
		OutboundDestClusters: make(map[string]int),
		SyscallRates:         make(map[string][]baselineSample),
		FileAccessPatterns:   make(map[string]int),
		MetricSeries:         make(map[string][]baselineSample),
	}
	be.baselines[collectorID] = hb
	return hb
}

// computeSeriesDrift evaluates a single time-series for baseline deviation.
func (be *BaselineEngine) computeSeriesDrift(collectorID, dimension, metric string, series []baselineSample, cutoff time.Time) []BaselineDrift {
	if len(series) < 5 {
		return nil
	}

	splitIdx := len(series) * 2 / 3
	baselineSlice := series[:splitIdx]
	recentSlice := series[splitIdx:]

	// Extract baseline values within window, fall back to all baseline values.
	baselineVals := extractValuesAfter(baselineSlice, cutoff)
	if len(baselineVals) == 0 {
		baselineVals = extractAllValues(baselineSlice)
	}
	if len(baselineVals) < 3 {
		return nil
	}

	baselineMean := meanFloat64(baselineVals)
	baselineStd := stddevFloat64(baselineVals, baselineMean)
	p95 := percentileFloat64(baselineVals, 0.95)
	p99 := percentileFloat64(baselineVals, 0.99)

	var drifts []BaselineDrift
	for _, s := range recentSlice {
		if s.Timestamp.Before(cutoff) {
			continue
		}

		// Zero-variance special case: only flag if value materially exceeds mean.
		if baselineStd == 0 {
			if s.Value != baselineMean && s.Value > baselineMean*1.5 {
				drifts = append(drifts, BaselineDrift{
					CollectorID: collectorID, Dimension: dimension, Metric: metric,
					Current: s.Value, Baseline: baselineMean, Deviation: s.Value - baselineMean,
					Percentile: 99, Severity: "high", DetectedAt: s.Timestamp,
				})
			}
			continue
		}

		deviation := (s.Value - baselineMean) / baselineStd
		if math.Abs(deviation) < be.cfg.DriftMinSeverity {
			continue
		}

		severity, pct := classifyDrift(s.Value, deviation, p95, p99, be.cfg.P95Threshold, be.cfg.P99Threshold)
		if severity == "" {
			continue
		}

		drifts = append(drifts, BaselineDrift{
			CollectorID: collectorID, Dimension: dimension, Metric: metric,
			Current: s.Value, Baseline: baselineMean, Deviation: deviation,
			Percentile: pct, Severity: severity, DetectedAt: s.Timestamp,
		})
	}
	return drifts
}

// classifyDrift returns severity and percentile. Empty severity means no drift.
func classifyDrift(value, deviation, p95, p99, p95Thresh, p99Thresh float64) (string, float64) {
	if value > p99*p99Thresh {
		return "high", 99
	}
	if value > p95*p95Thresh {
		return "medium", 95
	}
	if deviation > 2.0 {
		return "low", 90
	}
	return "", 0
}

func extractValuesAfter(samples []baselineSample, cutoff time.Time) []float64 {
	var vals []float64
	for _, s := range samples {
		if !s.Timestamp.Before(cutoff) {
			vals = append(vals, s.Value)
		}
	}
	return vals
}

func extractAllValues(samples []baselineSample) []float64 {
	vals := make([]float64, len(samples))
	for i, s := range samples {
		vals[i] = s.Value
	}
	return vals
}

func appendBoundedSample(series []baselineSample, s baselineSample, max int) []baselineSample {
	if len(series) > 0 {
		lastIdx := len(series) - 1
		last := series[lastIdx]
		if s.Timestamp.Equal(last.Timestamp) {
			series[lastIdx] = s
			return series
		}
		if s.Timestamp.Before(last.Timestamp) {
			insertAt := sort.Search(len(series), func(i int) bool {
				return !series[i].Timestamp.Before(s.Timestamp)
			})
			if insertAt < len(series) && series[insertAt].Timestamp.Equal(s.Timestamp) {
				series[insertAt] = s
				return series
			}
			series = append(series, baselineSample{})
			copy(series[insertAt+1:], series[insertAt:])
			series[insertAt] = s
			if len(series) <= max {
				return series
			}
			trim := max / 10
			if trim < 1 {
				trim = 1
			}
			return series[trim:]
		}
	}
	series = append(series, s)
	if len(series) <= max {
		return series
	}
	trim := max / 10
	if trim < 1 {
		trim = 1
	}
	return series[trim:]
}

func meanFloat64(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func stddevFloat64(vals []float64, m float64) float64 {
	if len(vals) < 2 {
		return 0
	}
	sumSq := 0.0
	for _, v := range vals {
		d := v - m
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(vals)-1))
}

func percentileFloat64(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi || hi >= len(sorted) {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
