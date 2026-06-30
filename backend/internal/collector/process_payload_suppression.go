package collector

import (
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

const (
	collectorProcessPayloadRefreshedMetric  = "collector_process_payload_refreshed"
	collectorProcessPayloadSuppressedMetric = "collector_process_payload_suppressed"
	defaultProcessPayloadRefreshInterval    = 1 * time.Minute
	processRSSBucketBytes                   = 64 * 1024 * 1024
	processIOBucketBPS                      = 1 * 1024 * 1024
)

type processPayloadSuppressionState struct {
	lastFingerprint uint64
	lastEmittedAt   time.Time
	initialized     bool
}

func (c *Collector) suppressUnchangedProcessPayload(now time.Time, processes []*telemetryv1.ProcessSample) ([]*telemetryv1.ProcessSample, []*telemetryv1.Metric) {
	if c == nil || len(processes) == 0 {
		return processes, nil
	}
	cfg := c.configSnapshot()
	fingerprint := processPayloadFingerprint(processes)
	if !cfg.SuppressUnchangedProcessPayloads {
		c.processState = processPayloadSuppressionState{
			lastFingerprint: fingerprint,
			lastEmittedAt:   now,
			initialized:     true,
		}
		return processes, processPayloadStateMetrics(now, true, false)
	}

	refreshInterval := cfg.ProcessPayloadRefreshInterval
	if refreshInterval <= 0 {
		refreshInterval = defaultProcessPayloadRefreshInterval
	}
	unchanged := c.processState.initialized && c.processState.lastFingerprint == fingerprint
	if unchanged && !c.processState.lastEmittedAt.IsZero() && now.Sub(c.processState.lastEmittedAt) < refreshInterval {
		return nil, processPayloadStateMetrics(now, false, true)
	}

	c.processState = processPayloadSuppressionState{
		lastFingerprint: fingerprint,
		lastEmittedAt:   now,
		initialized:     true,
	}
	return processes, processPayloadStateMetrics(now, true, false)
}

func processPayloadStateMetrics(now time.Time, refreshed, suppressed bool) []*telemetryv1.Metric {
	ts := now.UnixNano()
	return []*telemetryv1.Metric{
		{
			Name:              collectorProcessPayloadRefreshedMetric,
			Value:             boolToFloat(refreshed),
			TimestampUnixNano: ts,
		},
		{
			Name:              collectorProcessPayloadSuppressedMetric,
			Value:             boolToFloat(suppressed),
			TimestampUnixNano: ts,
		},
	}
}

func processPayloadFingerprint(processes []*telemetryv1.ProcessSample) uint64 {
	if len(processes) == 0 {
		return 0
	}
	hasher := fnv.New64a()
	writeString := func(value string) {
		_, _ = hasher.Write([]byte(value))
		_, _ = hasher.Write([]byte{0})
	}
	for _, proc := range processes {
		if proc == nil {
			continue
		}
		writeString(strconv.FormatInt(int64(proc.GetPid()), 10))
		writeString(strings.ToLower(strings.TrimSpace(proc.GetName())))
		writeString(strconv.FormatFloat(quantizeProcessCPU(proc.GetCpuPercent()), 'f', 1, 64))
		writeString(strconv.FormatUint(bucketUint64Value(proc.GetRssBytes(), processRSSBucketBytes), 10))
		writeString(strconv.FormatUint(bucketProcessIO(proc.GetIoReadBps()), 10))
		writeString(strconv.FormatUint(bucketProcessIO(proc.GetIoWriteBps()), 10))
	}
	return hasher.Sum64()
}

func quantizeProcessCPU(value float64) float64 {
	return math.Round(value*2) / 2
}

func bucketUint64Value(value, bucket uint64) uint64 {
	if bucket == 0 {
		return value
	}
	return value / bucket
}

func bucketProcessIO(value float64) uint64 {
	if processIOBucketBPS <= 0 {
		return uint64(math.Max(0, math.Floor(value)))
	}
	return uint64(math.Max(0, math.Floor(value/float64(processIOBucketBPS))))
}
