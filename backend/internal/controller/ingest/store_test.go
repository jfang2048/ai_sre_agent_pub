package ingest

import (
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
)

func TestStoreCapturesProcessNetworkMetrics(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	metrics := []*telemetryv1.Metric{
		{
			Name:  "rca_net_process_connections",
			Value: 5,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "123"},
				{Key: "name", Value: "nginx"},
			},
		},
		{
			Name:  "rca_net_process_queued_bytes",
			Value: 1024,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "123"},
			},
		},
	}

	store.StoreMetrics("c-1", metrics, now)
	snap := store.Node("c-1")
	if assert.NotNil(t, snap) && assert.Contains(t, snap.ProcessNetwork, "123") {
		p := snap.ProcessNetwork["123"]
		assert.Equal(t, "nginx", p.Name)
		assert.Equal(t, 5, p.Connections)
		assert.Equal(t, 1024.0, p.QueuedBytes)
	}
}

func TestCloneNodeCopiesProcessNetwork(t *testing.T) {
	s := &NodeSnapshot{
		CollectorID: "c-1",
		ProcessNetwork: map[string]*ProcessNetworkSample{
			"1": {PID: "1", Connections: 3},
		},
	}

	cloned := cloneNode(s)
	assert.NotSame(t, s, cloned)
	assert.Contains(t, cloned.ProcessNetwork, "1")
	cloned.ProcessNetwork["1"].Connections = 10

	// Original should not change
	assert.Equal(t, 3, s.ProcessNetwork["1"].Connections)
}

func TestStoreMetricsAggregatesNetworkAndDiskAcrossLabels(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	metrics := []*telemetryv1.Metric{
		{
			Name:  "node_network_receive_bytes_per_second",
			Value: 100,
			Labels: []*telemetryv1.Label{
				{Key: "device", Value: "eth0"},
			},
		},
		{
			Name:  "node_network_receive_bytes_per_second",
			Value: 50,
			Labels: []*telemetryv1.Label{
				{Key: "device", Value: "eth1"},
			},
		},
		{
			Name:  "node_disk_read_bytes_per_second",
			Value: 20,
			Labels: []*telemetryv1.Label{
				{Key: "device", Value: "sda"},
			},
		},
		{
			Name:  "node_disk_read_bytes_per_second",
			Value: 30,
			Labels: []*telemetryv1.Label{
				{Key: "device", Value: "nvme0n1"},
			},
		},
	}

	store.StoreMetrics("c-1", metrics, now)
	snap := store.Node("c-1")
	if assert.NotNil(t, snap) {
		assert.Equal(t, 150.0, snap.Metrics["node_network_receive_bytes_per_second"])
		assert.Equal(t, 50.0, snap.Metrics["node_disk_read_bytes_per_second"])
	}
}

func TestStoreMetricsResetsProcessNetworkPerBatch(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "rca_net_process_connections",
			Value: 5,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "111"},
				{Key: "name", Value: "first"},
			},
		},
	}, now)

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "rca_net_process_connections",
			Value: 3,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "222"},
				{Key: "name", Value: "second"},
			},
		},
	}, now.Add(time.Second))

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) {
		assert.NotContains(t, snap.ProcessNetwork, "111")
		if assert.Contains(t, snap.ProcessNetwork, "222") {
			assert.Equal(t, "second", snap.ProcessNetwork["222"].Name)
		}
	}
}

func TestStoreTracksProcessResourcesWithTotalsFrequencyAndLogs(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "rca_cpu_process_percent",
			Value: 72,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "321"},
				{Key: "name", Value: "worker"},
			},
		},
		{
			Name:  "rca_io_process_read_bytes_total",
			Value: 1024,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "321"},
				{Key: "name", Value: "worker"},
			},
		},
		{
			Name:  "node_gpu_process_memory_mib",
			Value: 512,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "321"},
				{Key: "process", Value: "worker"},
			},
		},
	}, now)

	// Counter should contribute delta on later sample.
	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "rca_cpu_process_percent",
			Value: 80,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "321"},
				{Key: "name", Value: "worker"},
			},
		},
		{
			Name:  "rca_io_process_read_bytes_total",
			Value: 1536,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "321"},
				{Key: "name", Value: "worker"},
			},
		},
	}, now.Add(2*time.Second))

	store.StoreLogs("c-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "e", Count: 3, Example: "worker[321]: ERROR timeout"},
		{Fingerprint: "w", Count: 2, Example: "worker[321]: warning retry"},
	}, now.Add(3*time.Second))

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) && assert.Contains(t, snap.ProcessResources, "pid|321") {
		p := snap.ProcessResources["pid|321"]
		assert.Equal(t, "worker", p.Name)
		assert.Greater(t, p.CategoryTotals["cpu"], 0.0)
		assert.Greater(t, p.CategoryTotals["gpu"], 0.0)
		assert.Equal(t, uint64(2), p.CategoryFrequency["cpu"])
		assert.Equal(t, uint64(5), p.CategoryFrequency["logs"])
		assert.Equal(t, uint64(3), p.LogErrors)
		assert.Equal(t, uint64(2), p.LogWarnings)
		assert.Equal(t, 1536.0, p.SignalTotals["rca_io_process_read_bytes_total"])
	}
}

func TestStoreMetricsProcessNetworkUsesProcessLabelFallback(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "rca_net_process_connections",
			Value: 8,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "404"},
				{Key: "process", Value: "edge-proxy"},
			},
		},
	}, now)

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) && assert.Contains(t, snap.ProcessNetwork, "404") {
		assert.Equal(t, "edge-proxy", snap.ProcessNetwork["404"].Name)
		assert.Equal(t, 8, snap.ProcessNetwork["404"].Connections)
	}
}

func TestStoreLogsGuessesProcessFromServiceKeyValue(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreLogs("c-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "l1", Count: 5, Example: `level=error service=payments-api msg="timeout"`},
	}, now)

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) && assert.Contains(t, snap.ProcessResources, "name|payments-api") {
		p := snap.ProcessResources["name|payments-api"]
		assert.Equal(t, uint64(5), p.LogErrors)
		assert.Equal(t, uint64(0), p.LogWarnings)
	}
}

func TestStoreLogsGuessesProcessFromJSONLogFields(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreLogs("c-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "l1", Count: 3, Example: `{"level":"error","service":"checkout","pid":1234,"msg":"timeout"}`},
	}, now)

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) && assert.Contains(t, snap.ProcessResources, "pid|1234") {
		p := snap.ProcessResources["pid|1234"]
		assert.Equal(t, "checkout", p.Name)
		assert.Equal(t, uint64(3), p.LogErrors)
	}
}

func TestMetricProcessIdentitySupportsExtendedLabelKeys(t *testing.T) {
	metric := &telemetryv1.Metric{
		Name: "node_ebpf_process_events_total",
		Labels: []*telemetryv1.Label{
			{Key: "tgid", Value: "888"},
			{Key: "command", Value: "python-worker"},
		},
	}

	pid, name := metricProcessIdentity(metric)
	assert.Equal(t, "888", pid)
	assert.Equal(t, "python-worker", name)
}
