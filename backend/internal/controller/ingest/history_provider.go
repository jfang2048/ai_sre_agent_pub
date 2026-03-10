package ingest

import "time"

// MetricHistoryProvider supplies trend-relevant metric history to APIs and agent workflows.
// The in-memory ingest store remains the default implementation; controller-side TSDB readers
// can implement the same contract without changing workflow call sites.
type MetricHistoryProvider interface {
	MetricHistory(collectorID string, since time.Time, limit int) []MetricHistorySample
}

// IsTrendMetric reports whether a metric participates in trend-oriented retention and analysis.
func IsTrendMetric(name string) bool {
	return shouldStoreTrendMetric(name)
}

// IsAggregatedMetric reports whether same-named metric samples are summed within a batch.
func IsAggregatedMetric(name string) bool {
	return shouldAggregateMetric(name)
}
