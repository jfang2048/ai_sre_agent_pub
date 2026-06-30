package controller

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
)

const (
	telemetryStateFresh       = "fresh"
	telemetryStateDelayed     = "delayed"
	telemetryStateStale       = "stale"
	telemetryStateUnavailable = "unavailable"
	telemetryStateDegraded    = "degraded"
)

var defaultCriticalSignals = []string{
	"cpu_usage_percent",
	"memory_used_percent",
	"network_total_bytes_per_second",
	"disk_total_bytes_per_second",
	"probe_core_fresh",
}

type telemetryQuality struct {
	State               string    `json:"state"`
	Partial             bool      `json:"partial,omitempty"`
	FallbackActive      bool      `json:"fallback_active,omitempty"`
	CoveragePercent     float64   `json:"coverage_percent,omitempty"`
	Confidence          float64   `json:"confidence,omitempty"`
	SourceMode          string    `json:"source_mode,omitempty"`
	RuntimeMode         string    `json:"runtime_mode,omitempty"`
	LatestCollectionAt  time.Time `json:"latest_collection_at,omitempty"`
	LatestIngestAt      time.Time `json:"latest_ingest_at,omitempty"`
	QueryAt             time.Time `json:"query_at"`
	FreshnessAgeSeconds float64   `json:"freshness_age_seconds,omitempty"`
	IngestDelaySeconds  float64   `json:"ingest_delay_seconds,omitempty"`
	MissingSignals      []string  `json:"missing_signals,omitempty"`
	DegradedReasons     []string  `json:"degraded_reasons,omitempty"`
	QualityHint         string    `json:"quality_hint,omitempty"`
}

type fleetTelemetryCoverage struct {
	State              string    `json:"state"`
	TotalCollectors    int       `json:"total_collectors"`
	FreshCollectors    int       `json:"fresh_collectors"`
	DelayedCollectors  int       `json:"delayed_collectors"`
	StaleCollectors    int       `json:"stale_collectors"`
	DegradedCollectors int       `json:"degraded_collectors"`
	PartialCollectors  int       `json:"partial_collectors"`
	FallbackCollectors int       `json:"fallback_collectors"`
	BacklogCollectors  int       `json:"backlog_collectors"`
	CoveragePercent    float64   `json:"coverage_percent,omitempty"`
	LatestCollectionAt time.Time `json:"latest_collection_at,omitempty"`
	LatestIngestAt     time.Time `json:"latest_ingest_at,omitempty"`
	QueryAt            time.Time `json:"query_at"`
	QualityHint        string    `json:"quality_hint,omitempty"`
}

func buildFleetTelemetryQuality(
	node *ingest.NodeSnapshot,
	samples []ingest.MetricHistorySample,
	series []fleetTrendSeries,
	summary map[string]float64,
	filter map[string]struct{},
	queryAt time.Time,
) telemetryQuality {
	quality := telemetryQuality{
		State:   telemetryStateUnavailable,
		QueryAt: queryAt,
	}
	if node != nil {
		quality.SourceMode = strings.TrimSpace(node.ProbeSource)
		quality.RuntimeMode = strings.TrimSpace(node.RuntimeMode)
		quality.LatestCollectionAt = node.LastCollectionAt
		quality.LatestIngestAt = node.LastIngestAt
	}

	if len(samples) == 0 || len(series) == 0 {
		quality.MissingSignals = criticalSignals(filter, series, summary)
		quality.QualityHint = "no trend samples are available yet; wait for a collector batch or verify ingest health"
		return quality
	}

	if quality.LatestCollectionAt.IsZero() {
		quality.LatestCollectionAt = samples[len(samples)-1].Timestamp
	}
	if quality.LatestIngestAt.IsZero() {
		if samples[len(samples)-1].IngestedAt.IsZero() {
			quality.LatestIngestAt = samples[len(samples)-1].Timestamp
		} else {
			quality.LatestIngestAt = samples[len(samples)-1].IngestedAt
		}
	}

	referenceTime := quality.LatestCollectionAt
	if referenceTime.IsZero() {
		referenceTime = quality.LatestIngestAt
	}
	if !referenceTime.IsZero() {
		quality.FreshnessAgeSeconds = maxQualityFloat(0, queryAt.Sub(referenceTime).Seconds())
	}
	if !quality.LatestCollectionAt.IsZero() && !quality.LatestIngestAt.IsZero() {
		quality.IngestDelaySeconds = maxQualityFloat(0, quality.LatestIngestAt.Sub(quality.LatestCollectionAt).Seconds())
	}

	quality.MissingSignals = criticalSignals(filter, series, summary)
	quality.Partial = len(quality.MissingSignals) > 0
	quality.CoveragePercent = coveragePercent(quality.MissingSignals, filter)

	if node != nil {
		if node.RuntimeModeDegraded {
			quality.DegradedReasons = append(quality.DegradedReasons, "collector runtime mode is degraded")
		}
		for _, reason := range node.RuntimeReasons {
			if strings.TrimSpace(reason) != "" {
				quality.DegradedReasons = append(quality.DegradedReasons, strings.ReplaceAll(reason, "_", " "))
			}
		}
		if strings.TrimSpace(node.ProbeSource) != "" && !strings.EqualFold(strings.TrimSpace(node.ProbeSource), "probe_core") {
			quality.FallbackActive = true
			quality.DegradedReasons = append(quality.DegradedReasons, fmt.Sprintf("collector is using %s compatibility source", node.ProbeSource))
		}
		if backlog := metricValueOr(node.Metrics, "collector_spool_backlog_bytes"); backlog > 0 {
			quality.DegradedReasons = append(quality.DegradedReasons, "collector replay backlog is still draining")
		}
	}

	if summary["probe_core_active"] < 1 && summary["probe_core_client_available"] >= 1 {
		quality.DegradedReasons = append(quality.DegradedReasons, "probe-core client is available but not active")
	}
	if summary["probe_core_fresh"] > 0 && summary["probe_core_fresh"] < 1 {
		quality.DegradedReasons = append(quality.DegradedReasons, "probe-core freshness is degraded")
	}
	if quality.Partial {
		quality.DegradedReasons = append(quality.DegradedReasons, "critical telemetry coverage is incomplete")
	}
	if quality.IngestDelaySeconds >= 30 {
		quality.DegradedReasons = append(quality.DegradedReasons, "telemetry arrival delay is elevated")
	}
	quality.DegradedReasons = dedupeQualityStrings(quality.DegradedReasons)

	switch {
	case quality.FreshnessAgeSeconds >= 300:
		quality.State = telemetryStateStale
	case len(quality.DegradedReasons) > 0:
		quality.State = telemetryStateDegraded
	case quality.FreshnessAgeSeconds >= 90:
		quality.State = telemetryStateDelayed
	default:
		quality.State = telemetryStateFresh
	}

	quality.Confidence = qualityConfidence(quality)
	quality.QualityHint = telemetryQualityHint(quality)
	return quality
}

func buildNodeTelemetryQuality(node *ingest.NodeSnapshot, queryAt time.Time) telemetryQuality {
	quality := telemetryQuality{
		State:   telemetryStateUnavailable,
		QueryAt: queryAt,
	}
	if node == nil {
		quality.QualityHint = "No collector snapshot is available yet."
		return quality
	}

	quality.SourceMode = strings.TrimSpace(node.ProbeSource)
	quality.RuntimeMode = strings.TrimSpace(node.RuntimeMode)
	quality.LatestCollectionAt = node.LastCollectionAt
	quality.LatestIngestAt = node.LastIngestAt

	if quality.LatestCollectionAt.IsZero() {
		quality.LatestCollectionAt = firstNonZeroTime(node.UpdatedAt, node.LastSeen)
	}
	if quality.LatestIngestAt.IsZero() {
		quality.LatestIngestAt = firstNonZeroTime(node.UpdatedAt, node.LastSeen)
	}

	referenceTime := firstNonZeroTime(quality.LatestCollectionAt, quality.LatestIngestAt)
	if referenceTime.IsZero() {
		quality.QualityHint = "Collector has no recorded telemetry timestamps yet."
		return quality
	}
	quality.FreshnessAgeSeconds = maxQualityFloat(0, queryAt.Sub(referenceTime).Seconds())
	if !quality.LatestCollectionAt.IsZero() && !quality.LatestIngestAt.IsZero() {
		quality.IngestDelaySeconds = maxQualityFloat(0, quality.LatestIngestAt.Sub(quality.LatestCollectionAt).Seconds())
	}

	quality.MissingSignals = snapshotCriticalSignals(node)
	quality.Partial = len(quality.MissingSignals) > 0
	quality.CoveragePercent = coveragePercent(quality.MissingSignals, nil)

	if node.RuntimeModeDegraded {
		quality.DegradedReasons = append(quality.DegradedReasons, "collector runtime mode is degraded")
	}
	for _, reason := range node.RuntimeReasons {
		if strings.TrimSpace(reason) != "" {
			quality.DegradedReasons = append(quality.DegradedReasons, strings.ReplaceAll(reason, "_", " "))
		}
	}
	if quality.SourceMode != "" && !strings.EqualFold(quality.SourceMode, "probe_core") {
		quality.FallbackActive = true
		quality.DegradedReasons = append(quality.DegradedReasons, fmt.Sprintf("collector is using %s compatibility source", quality.SourceMode))
	}
	if metricValueOr(node.Metrics, "collector_spool_backlog_bytes") > 0 {
		quality.DegradedReasons = append(quality.DegradedReasons, "collector replay backlog is still draining")
	}
	if probeFresh, ok := node.Metrics["collector_probe_core_fresh"]; ok && probeFresh < 1 {
		quality.DegradedReasons = append(quality.DegradedReasons, "probe-core freshness is degraded")
	}
	if quality.Partial {
		quality.DegradedReasons = append(quality.DegradedReasons, "critical telemetry coverage is incomplete")
	}
	if quality.IngestDelaySeconds >= 30 {
		quality.DegradedReasons = append(quality.DegradedReasons, "telemetry arrival delay is elevated")
	}
	quality.DegradedReasons = dedupeQualityStrings(quality.DegradedReasons)

	switch {
	case quality.FreshnessAgeSeconds >= 300:
		quality.State = telemetryStateStale
	case len(quality.DegradedReasons) > 0:
		quality.State = telemetryStateDegraded
	case quality.FreshnessAgeSeconds >= 90:
		quality.State = telemetryStateDelayed
	default:
		quality.State = telemetryStateFresh
	}

	quality.Confidence = qualityConfidence(quality)
	quality.QualityHint = telemetryQualityHint(quality)
	return quality
}

func buildFleetCoverageSummary(nodes []*ingest.NodeSnapshot, queryAt time.Time) fleetTelemetryCoverage {
	summary := fleetTelemetryCoverage{
		State:   telemetryStateUnavailable,
		QueryAt: queryAt,
	}
	if len(nodes) == 0 {
		summary.QualityHint = "No collector snapshots are available yet, so fleet coverage is currently blind."
		return summary
	}

	summary.TotalCollectors = len(nodes)
	coverageSum := 0.0
	for _, node := range nodes {
		quality := buildNodeTelemetryQuality(node, queryAt)
		coverageSum += quality.CoveragePercent
		switch quality.State {
		case telemetryStateFresh:
			summary.FreshCollectors++
		case telemetryStateDelayed:
			summary.DelayedCollectors++
		case telemetryStateStale:
			summary.StaleCollectors++
		default:
			summary.DegradedCollectors++
		}
		if quality.Partial {
			summary.PartialCollectors++
		}
		if quality.FallbackActive {
			summary.FallbackCollectors++
		}
		if node != nil && metricValueOr(node.Metrics, "collector_spool_backlog_bytes") > 0 {
			summary.BacklogCollectors++
		}
		summary.LatestCollectionAt = maxTime(summary.LatestCollectionAt, quality.LatestCollectionAt)
		summary.LatestIngestAt = maxTime(summary.LatestIngestAt, quality.LatestIngestAt)
	}
	summary.CoveragePercent = coverageSum / float64(len(nodes))

	switch {
	case summary.StaleCollectors > 0:
		summary.State = telemetryStateStale
	case summary.DegradedCollectors > 0 || summary.PartialCollectors > 0 || summary.FallbackCollectors > 0 || summary.BacklogCollectors > 0:
		summary.State = telemetryStateDegraded
	case summary.DelayedCollectors > 0:
		summary.State = telemetryStateDelayed
	default:
		summary.State = telemetryStateFresh
	}

	switch summary.State {
	case telemetryStateUnavailable:
		summary.QualityHint = "No collectors are reporting yet."
	case telemetryStateStale:
		summary.QualityHint = "At least one collector is stale enough to create fleet-wide blind spots."
	case telemetryStateDegraded:
		summary.QualityHint = "Fleet coverage is degraded: some collectors are partial, lagging, or running in fallback mode."
	case telemetryStateDelayed:
		summary.QualityHint = "Fleet telemetry is arriving, but some collectors are delayed."
	default:
		summary.QualityHint = "Fleet telemetry coverage is currently healthy."
	}
	return summary
}

func criticalSignals(filter map[string]struct{}, series []fleetTrendSeries, summary map[string]float64) []string {
	required := defaultCriticalSignals
	if len(filter) > 0 {
		required = make([]string, 0, len(filter))
		for key := range filter {
			required = append(required, key)
		}
	}
	available := make(map[string]struct{}, len(series))
	for _, item := range series {
		available[item.Key] = struct{}{}
	}
	missing := make([]string, 0, len(required))
	for _, key := range required {
		if _, ok := available[key]; ok {
			continue
		}
		if _, ok := summary[key]; ok {
			continue
		}
		missing = append(missing, key)
	}
	return missing
}

func coveragePercent(missing []string, filter map[string]struct{}) float64 {
	total := len(defaultCriticalSignals)
	if len(filter) > 0 {
		total = len(filter)
	}
	if total == 0 {
		return 100
	}
	covered := total - len(missing)
	if covered < 0 {
		covered = 0
	}
	return float64(covered) / float64(total) * 100
}

func qualityConfidence(q telemetryQuality) float64 {
	confidence := 1.0
	confidence -= (100 - clampRange(q.CoveragePercent, 0, 100)) / 100 * 0.45
	switch q.State {
	case telemetryStateDelayed:
		confidence -= 0.1
	case telemetryStateDegraded:
		confidence -= 0.2
	case telemetryStateStale:
		confidence -= 0.35
	case telemetryStateUnavailable:
		confidence -= 0.6
	}
	if q.IngestDelaySeconds >= 30 {
		confidence -= 0.1
	}
	return math.Max(0.1, math.Min(1.0, confidence))
}

func telemetryQualityHint(q telemetryQuality) string {
	switch q.State {
	case telemetryStateUnavailable:
		return "No recent telemetry is available, so UI values and RCA are currently blind."
	case telemetryStateStale:
		return "Telemetry is stale enough to increase MTTR and false-RCA risk; refresh collector health before acting."
	case telemetryStateDelayed:
		return "Telemetry is arriving late, so fast-moving incidents may already have shifted."
	case telemetryStateDegraded:
		return "Telemetry coverage is degraded; treat flat or missing values as an observability warning, not a healthy zero."
	default:
		return "Telemetry freshness and coverage are currently healthy."
	}
}

func maxQualityFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func dedupeQualityStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func snapshotCriticalSignals(node *ingest.NodeSnapshot) []string {
	if node == nil {
		return append([]string(nil), defaultCriticalSignals...)
	}
	metrics := node.Metrics
	missing := make([]string, 0, len(defaultCriticalSignals))
	if !hasMetricAny(metrics, "node_cpu_usage_percent", "probe_core_cpu_usage_percent") {
		missing = append(missing, "cpu_usage_percent")
	}
	if !hasMetricAny(metrics, "node_memory_used_percent", "memory_used_percent", "probe_core_memory_used_percent") {
		missing = append(missing, "memory_used_percent")
	}
	if !hasMetricAny(metrics,
		"node_network_total_receive_bytes_per_second",
		"node_network_receive_bytes_per_second",
		"node_network_total_transmit_bytes_per_second",
		"node_network_transmit_bytes_per_second",
	) {
		missing = append(missing, "network_total_bytes_per_second")
	}
	if !hasMetricAny(metrics,
		"node_disk_read_bytes_per_second",
		"node_disk_write_bytes_per_second",
		"node_disk_total_bytes_per_second",
		"probe_core_disk_read_bytes_per_second",
		"probe_core_disk_write_bytes_per_second",
	) {
		missing = append(missing, "disk_total_bytes_per_second")
	}
	if !hasMetricAny(metrics, "collector_probe_core_fresh") {
		missing = append(missing, "probe_core_fresh")
	}
	return missing
}

func hasMetricAny(metrics map[string]float64, names ...string) bool {
	if len(metrics) == 0 {
		return false
	}
	for _, name := range names {
		if _, ok := metrics[name]; ok {
			return true
		}
	}
	return false
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func maxTime(left, right time.Time) time.Time {
	switch {
	case left.IsZero():
		return right
	case right.IsZero():
		return left
	case right.After(left):
		return right
	default:
		return left
	}
}
