package ebpf

import (
	"strings"
	"testing"
	"time"
)

func TestMetricSamplesTruncateRuntimeEventLabels(t *testing.T) {
	c := NewCollector(DefaultConfig())
	now := time.Now().UTC()

	tooLong := strings.Repeat("x", defaultLabelValueMaxLen+200)
	c.mu.Lock()
	c.pendingEvents = append(c.pendingEvents, Event{
		EvidenceID:  "ev-ebpf-test-long-label",
		Timestamp:   now,
		Category:    "process",
		Type:        "execve",
		Scope:       "node",
		PID:         1234,
		Comm:        tooLong,
		Path:        "/" + tooLong,
		RemoteIP:    tooLong,
		Severity:    "high",
		Confidence:  0.9,
		Description: tooLong,
	})
	c.mu.Unlock()

	metrics := c.MetricSamples(now)
	found := false
	for _, metric := range metrics {
		if metric.Name != "node_ebpf_runtime_event" {
			continue
		}
		found = true
		for key, value := range metric.Labels {
			if len(key) > defaultLabelKeyMaxLen {
				t.Fatalf("label key %q too long: %d", key, len(key))
			}
			if len(value) > defaultLabelValueMaxLen {
				t.Fatalf("label value for %q too long: %d", key, len(value))
			}
		}
	}
	if !found {
		t.Fatalf("expected at least one node_ebpf_runtime_event metric")
	}
}
