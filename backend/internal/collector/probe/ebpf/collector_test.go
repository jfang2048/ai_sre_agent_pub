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

func TestMetricSamplesExposeBoundedCorrelationSignals(t *testing.T) {
	c := NewCollector(DefaultConfig())
	now := time.Now().UTC()

	c.ingest(wireEvent{
		Timestamp: now.UnixNano(),
		Category:  "network",
		Type:      "connect",
		PID:       4242,
		Comm:      "trainer",
		Bytes:     4096,
		LatencyNs: 2500000,
		Details:   "remote_ip=8.8.8.8 port=443",
	})
	c.ingest(wireEvent{
		Timestamp: now.UnixNano(),
		Category:  "file",
		Type:      "open",
		PID:       4242,
		Comm:      "trainer",
		Details:   "path=/etc/shadow",
	})

	metrics := c.MetricSamples(now.Add(10 * time.Second))
	assertMetricWithLabel(t, metrics, "node_ebpf_category_events_total", "category", "network")
	assertMetricWithLabel(t, metrics, "node_ebpf_category_bytes_total", "category", "network")
	assertMetricWithLabel(t, metrics, "node_ebpf_category_latency_seconds_avg", "category", "network")
	assertMetricWithLabel(t, metrics, "node_ebpf_remote_scope_events_total", "scope", "public")
	assertMetricWithLabel(t, metrics, "node_ebpf_sensitive_path_events_total", "scope", "auth_db")
	assertMetricWithLabel(t, metrics, "node_ebpf_process_category_events_total", "category", "network")
	assertMetricWithLabel(t, metrics, "node_ebpf_process_category_events_total", "category", "file")
	assertMetricWithLabel(t, metrics, "node_ebpf_runtime_event", "remote_scope", "public")
	assertMetricWithLabel(t, metrics, "node_ebpf_runtime_event", "path_scope", "auth_db")

	summary := c.Summary()
	if got := summary.CategoryCounts["network"]; got != 1 {
		t.Fatalf("expected network category count 1, got %d", got)
	}
	if got := summary.RemoteScopeCounts["public"]; got != 1 {
		t.Fatalf("expected public remote scope count 1, got %d", got)
	}
	if got := summary.SensitivePathCounts["auth_db"]; got != 1 {
		t.Fatalf("expected auth_db path scope count 1, got %d", got)
	}
}

func assertMetricWithLabel(t *testing.T, metrics []MetricSample, name, labelKey, labelValue string) {
	t.Helper()
	for _, metric := range metrics {
		if metric.Name != name {
			continue
		}
		if metric.Labels[labelKey] == labelValue {
			return
		}
	}
	t.Fatalf("expected metric %q with %s=%q", name, labelKey, labelValue)
}
