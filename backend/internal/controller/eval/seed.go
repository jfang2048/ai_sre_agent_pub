package eval

import (
	"fmt"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

func seedIncidentScenario(store *ingest.MemoryStore, index *logindex.Index, collectorID string, scenario TelemetryScenario, now time.Time) error {
	return seedTelemetryScenarioAt(store, index, collectorID, "", scenario, now)
}

func seedTelemetryScenarioAt(store *ingest.MemoryStore, index *logindex.Index, collectorID, service string, scenario TelemetryScenario, now time.Time) error {
	if store == nil {
		return fmt.Errorf("memory store is required")
	}
	collectorID = strings.TrimSpace(collectorID)
	if collectorID == "" {
		return fmt.Errorf("collector id is required")
	}
	stepMinutes := scenario.StepMinutes
	if stepMinutes <= 0 {
		stepMinutes = 1
	}
	durationMinutes := scenario.DurationMinutes
	if durationMinutes <= 0 {
		durationMinutes = 30
	}
	sampleCount := durationMinutes / stepMinutes
	if sampleCount < 6 {
		sampleCount = 6
	}
	base := now.Add(-time.Duration(sampleCount) * time.Duration(stepMinutes) * time.Minute).UTC()

	labels := make([]*telemetryv1.Label, 0, 1)
	if strings.TrimSpace(service) != "" {
		labels = append(labels, &telemetryv1.Label{Key: "service", Value: strings.TrimSpace(service)})
	}

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: collectorID,
		Hostname:    collectorID + "-host",
		Version:     "v0.9-eval",
		Os:          "linux",
		Arch:        "amd64",
		Labels:      labels,
	}, now.UTC())

	for i := 0; i < sampleCount; i++ {
		ts := base.Add(time.Duration(i*stepMinutes) * time.Minute)
		metrics := make([]*telemetryv1.Metric, 0, len(scenario.MetricSeries))
		for _, spec := range scenario.MetricSeries {
			value := seriesValue(spec, i)
			metrics = append(metrics, &telemetryv1.Metric{
				Name:              spec.Name,
				Value:             value,
				TimestampUnixNano: ts.UnixNano(),
			})
		}
		store.StoreMetrics(collectorID, metrics, ts)

		fingerprints := buildFingerprintBatch(scenario.Logs, i)
		if len(fingerprints) > 0 {
			store.StoreLogs(collectorID, fingerprints, ts)
		}
		if index != nil {
			events := buildLogEvents(collectorID, scenario.Logs, i, ts)
			if len(events) > 0 {
				index.AddBatch(events)
			}
		}
	}

	if len(scenario.Processes) > 0 {
		processes := make([]*telemetryv1.ProcessSample, 0, len(scenario.Processes))
		for _, proc := range scenario.Processes {
			processes = append(processes, &telemetryv1.ProcessSample{
				Pid:        proc.PID,
				Name:       proc.Name,
				CpuPercent: proc.CPU,
				RssBytes:   proc.RSSBytes,
				IoReadBps:  proc.IOReadBPS,
				IoWriteBps: proc.IOWrtBPS,
			})
		}
		store.StoreProcesses(collectorID, processes, now.Add(-30*time.Second))
	}

	return nil
}

func seriesValue(spec MetricSeriesSpec, index int) float64 {
	mode := strings.ToLower(strings.TrimSpace(spec.Mode))
	switch {
	case len(spec.Sequence) > 0:
		if index < len(spec.Sequence) {
			return clampSeriesValue(spec.Sequence[index], spec.Min, spec.Max)
		}
		return clampSeriesValue(spec.Sequence[len(spec.Sequence)-1], spec.Min, spec.Max)
	case mode == "constant":
		return clampSeriesValue(spec.Value, spec.Min, spec.Max)
	default:
		value := spec.Start + spec.Step*float64(index)
		if mode == "constant" && spec.Value != 0 {
			value = spec.Value
		}
		return clampSeriesValue(value, spec.Min, spec.Max)
	}
}

func clampSeriesValue(value, min, max float64) float64 {
	if max != 0 && value > max {
		value = max
	}
	if min != 0 && value < min {
		value = min
	}
	return value
}

func buildFingerprintBatch(specs []LogSeriesSpec, sampleIndex int) []*telemetryv1.LogFingerprint {
	out := make([]*telemetryv1.LogFingerprint, 0, len(specs))
	for _, spec := range specs {
		if !logSpecMatches(spec, sampleIndex) {
			continue
		}
		count := spec.CountStart + uint64(sampleIndex)*maxUint64(1, spec.CountStep)
		if count == 0 {
			count = 1
		}
		out = append(out, &telemetryv1.LogFingerprint{
			Fingerprint: spec.Fingerprint,
			Example:     spec.Example,
			Count:       count,
		})
	}
	return out
}

func buildLogEvents(collectorID string, specs []LogSeriesSpec, sampleIndex int, ts time.Time) []logindex.RawEvent {
	out := make([]logindex.RawEvent, 0, len(specs))
	for _, spec := range specs {
		if !logSpecMatches(spec, sampleIndex) {
			continue
		}
		count := spec.CountStart + uint64(sampleIndex)*maxUint64(1, spec.CountStep)
		if count == 0 {
			count = 1
		}
		level := strings.TrimSpace(spec.Level)
		if level == "" {
			level = "warn"
		}
		out = append(out, logindex.RawEvent{
			Timestamp:   ts,
			CollectorID: collectorID,
			Hostname:    collectorID + "-host",
			Service:     firstNonEmpty(spec.Service, "eval-service"),
			Process:     firstNonEmpty(spec.Process, "eval-process"),
			PID:         "1000",
			Level:       level,
			Source:      "app",
			Message:     spec.Example,
			Count:       count,
		})
	}
	return out
}

func logSpecMatches(spec LogSeriesSpec, sampleIndex int) bool {
	every := spec.Every
	if every <= 0 {
		every = 1
	}
	offset := spec.Offset
	if sampleIndex < offset {
		return false
	}
	return (sampleIndex-offset)%every == 0
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, item := range values {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
