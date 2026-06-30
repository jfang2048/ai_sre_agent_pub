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

func TestCloneNodeCopiesProcessesAndLogsWithoutAliasing(t *testing.T) {
	src := &NodeSnapshot{
		CollectorID: "c-1",
		Processes: []*telemetryv1.ProcessSample{
			{Pid: 42, Name: "trainer", CpuPercent: 73.5, RssBytes: 2048, IoReadBps: 11, IoWriteBps: 22},
			nil,
		},
		Logs: []*telemetryv1.LogFingerprint{
			{Fingerprint: "oom", Count: 3, Example: "OOM killer invoked", TimestampUnixNano: 123},
			nil,
		},
	}

	cloned := cloneNode(src)
	if assert.NotNil(t, cloned) && assert.Len(t, cloned.Processes, 2) && assert.Len(t, cloned.Logs, 2) {
		assert.Nil(t, cloned.Processes[1])
		assert.Nil(t, cloned.Logs[1])

		cloned.Processes[0].Name = "mutated"
		cloned.Processes[0].CpuPercent = 1
		cloned.Logs[0].Fingerprint = "changed"
		cloned.Logs[0].Count = 0

		assert.Equal(t, "trainer", src.Processes[0].Name)
		assert.Equal(t, 73.5, src.Processes[0].CpuPercent)
		assert.Equal(t, "oom", src.Logs[0].Fingerprint)
		assert.Equal(t, uint64(3), src.Logs[0].Count)
	}
}

func TestStoreMetricsCapturesStorageSnapshots(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "node_disk_read_bytes_per_second",
			Value: 1024,
			Labels: []*telemetryv1.Label{
				{Key: "device", Value: "sda"},
			},
		},
		{
			Name:  "node_disk_queue_depth",
			Value: 2.5,
			Labels: []*telemetryv1.Label{
				{Key: "device", Value: "sda"},
			},
		},
		{
			Name:  "node_disk_avg_request_latency_seconds",
			Value: 0.004,
			Labels: []*telemetryv1.Label{
				{Key: "device", Value: "sda"},
			},
		},
		{
			Name:  "node_disk_partition_read_bytes_per_second",
			Value: 512,
			Labels: []*telemetryv1.Label{
				{Key: "device", Value: "sda"},
				{Key: "partition", Value: "sda1"},
			},
		},
		{
			Name:  "node_filesystem_used_percent",
			Value: 82,
			Labels: []*telemetryv1.Label{
				{Key: "mountpoint", Value: "/data"},
				{Key: "device", Value: "/dev/sda1"},
				{Key: "fstype", Value: "ext4"},
			},
		},
		{
			Name:  "node_filesystem_files_used_percent",
			Value: 70,
			Labels: []*telemetryv1.Label{
				{Key: "mountpoint", Value: "/data"},
				{Key: "device", Value: "/dev/sda1"},
				{Key: "fstype", Value: "ext4"},
			},
		},
	}, now)

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) {
		if assert.Contains(t, snap.StorageDevices, "sda") {
			dev := snap.StorageDevices["sda"]
			assert.Equal(t, 1024.0, dev.ReadBytesPerSecond)
			assert.Equal(t, 2.5, dev.QueueDepth)
			assert.Equal(t, 4.0, dev.AvgRequestLatencyMS)
		}
		if assert.Contains(t, snap.StoragePartitions, "sda1") {
			part := snap.StoragePartitions["sda1"]
			assert.Equal(t, "sda", part.ParentDevice)
			assert.Equal(t, 512.0, part.ReadBytesPerSecond)
		}
		if assert.Contains(t, snap.Filesystems, "/data") {
			fs := snap.Filesystems["/data"]
			assert.Equal(t, "/dev/sda1", fs.Device)
			assert.Equal(t, "ext4", fs.FSType)
			assert.Equal(t, 82.0, fs.UsedPercent)
			assert.Equal(t, 70.0, fs.FilesUsedPercent)
		}
	}
}

func TestCloneNodeCopiesStorageMaps(t *testing.T) {
	src := &NodeSnapshot{
		CollectorID: "c-1",
		StorageDevices: map[string]*StorageDeviceSample{
			"sda": {Device: "sda", ReadBytesPerSecond: 100},
		},
		StoragePartitions: map[string]*StorageDeviceSample{
			"sda1": {Device: "sda", Partition: "sda1", ReadBytesPerSecond: 80, Scope: "partition"},
		},
		Filesystems: map[string]*FilesystemSample{
			"/data": {Mountpoint: "/data", UsedPercent: 90},
		},
	}

	cloned := cloneNode(src)
	if assert.NotNil(t, cloned) {
		cloned.StorageDevices["sda"].ReadBytesPerSecond = 1
		cloned.StoragePartitions["sda1"].ReadBytesPerSecond = 2
		cloned.Filesystems["/data"].UsedPercent = 3

		assert.Equal(t, 100.0, src.StorageDevices["sda"].ReadBytesPerSecond)
		assert.Equal(t, 80.0, src.StoragePartitions["sda1"].ReadBytesPerSecond)
		assert.Equal(t, 90.0, src.Filesystems["/data"].UsedPercent)
	}
}

func TestStoreMetricsCapturesProbeCoreSourceAndModules(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "collector_probe_source",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "source", Value: "probe_core"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_requested",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "network"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_active",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "network"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_requested",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "rdma"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_active",
			Value: 0,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "rdma"},
			},
		},
	}, now)

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) {
		assert.Equal(t, "probe_core", snap.ProbeSource)
		if assert.Contains(t, snap.ProbeCoreModules, "network") {
			network := snap.ProbeCoreModules["network"]
			assert.Equal(t, 1.0, network.Requested)
			assert.Equal(t, 1.0, network.Active)
		}
		if assert.Contains(t, snap.ProbeCoreModules, "rdma") {
			rdma := snap.ProbeCoreModules["rdma"]
			assert.Equal(t, 1.0, rdma.Requested)
			assert.Equal(t, 0.0, rdma.Active)
		}
	}
}

func TestStoreMetricsResetsProbeCoreModuleStatePerBatch(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "collector_probe_source",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "source", Value: "probe_core"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_requested",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "network"},
			},
		},
	}, now)

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "collector_probe_source",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "source", Value: "go"},
			},
		},
	}, now.Add(time.Second))

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) {
		assert.Equal(t, "go", snap.ProbeSource)
		assert.Empty(t, snap.ProbeCoreModules)
	}
}

func TestStoreMetricsCarriesForwardLowChurnCollectorStateOnPartialUpdates(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "collector_probe_source",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "source", Value: "probe_core"},
			},
		},
		{
			Name:  "collector_runtime_mode",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "mode", Value: "host"},
			},
		},
		{
			Name:  "collector_runtime_capability_available",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "capability", Value: "bpf_capability"},
			},
		},
		{
			Name:  "collector_probe_core_collector_module_active",
			Value: 1,
			Labels: []*telemetryv1.Label{
				{Key: "module", Value: "network"},
			},
		},
		{
			Name:  "collector_hardware_cpu_sockets",
			Value: 2,
		},
	}, now)

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "collector_metrics_partial_update",
			Value: 1,
		},
		{
			Name:  "collector_metrics_suppressed_count",
			Value: 5,
		},
		{
			Name:  "collector_self_cpu_percent",
			Value: 2.5,
		},
	}, now.Add(time.Second))

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) {
		assert.Equal(t, "probe_core", snap.ProbeSource)
		assert.Equal(t, "host", snap.RuntimeMode)
		if assert.NotNil(t, snap.RuntimeCapabilities) {
			assert.Equal(t, 1.0, snap.RuntimeCapabilities["bpf_capability"])
		}
		if assert.Contains(t, snap.ProbeCoreModules, "network") {
			assert.Equal(t, 1.0, snap.ProbeCoreModules["network"].Active)
		}
		assert.Equal(t, 2.0, snap.Metrics["collector_hardware_cpu_sockets"])
		assert.Equal(t, 1.0, snap.Metrics["collector_metrics_partial_update"])
		assert.Equal(t, 2.5, snap.Metrics["collector_self_cpu_percent"])
	}
}

func TestCloneNodeCopiesProbeCoreModules(t *testing.T) {
	src := &NodeSnapshot{
		CollectorID: "c-1",
		ProbeSource: "probe_core",
		ProbeCoreModules: map[string]*ProbeCoreModuleSample{
			"network": {Module: "network", Requested: 1, Active: 1},
		},
	}

	cloned := cloneNode(src)
	if assert.NotNil(t, cloned) {
		cloned.ProbeSource = "go"
		cloned.ProbeCoreModules["network"].Active = 0
		assert.Equal(t, "probe_core", src.ProbeSource)
		assert.Equal(t, 1.0, src.ProbeCoreModules["network"].Active)
	}
}

func TestUpsertCollectorIgnoresNilAndBlankLabels(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	assert.NotPanics(t, func() {
		store.UpsertCollector(&telemetryv1.CollectorInfo{
			CollectorId: "collector-labels",
			Hostname:    "node-labels",
			Labels: []*telemetryv1.Label{
				nil,
				{Key: "", Value: "invalid-empty"},
				{Key: "   ", Value: "invalid-whitespace"},
				{Key: " role ", Value: "worker"},
			},
		}, now)
	})

	snap := store.Node("collector-labels")
	if assert.NotNil(t, snap) {
		assert.Equal(t, "worker", snap.Labels["role"])
		assert.NotContains(t, snap.Labels, "")
		assert.NotContains(t, snap.Labels, "   ")
	}
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

func TestStoreMetricsCapturesProcessContextLabels(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{
			Name:  "rca_net_process_connections",
			Value: 12,
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "909"},
				{Key: "name", Value: "torchrun"},
				{Key: "workload_class", Value: "training"},
				{Key: "job", Value: "llm-pretrain"},
				{Key: "comm_pattern", Value: "nccl"},
				{Key: "pod_uid", Value: "abc123def456"},
			},
		},
	}, now)

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) && assert.Contains(t, snap.ProcessResources, "pid|909") {
		p := snap.ProcessResources["pid|909"]
		assert.Equal(t, "training", p.WorkloadClass)
		assert.Equal(t, "llm-pretrain", p.Job)
		assert.Equal(t, "nccl", p.CommPattern)
		assert.Equal(t, "abc123def456", p.PodUID)
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

func TestMetricHistoryStoresTrendMetricsOnly(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 42.5},
		{Name: "collector_probe_core_active", Value: 1},
		{Name: "collector_protection_mode_severity", Value: 2},
		{Name: "rca_cpu_process_percent", Value: 91, Labels: []*telemetryv1.Label{{Key: "pid", Value: "12"}}},
	}, now)

	history := store.MetricHistory("c-1", time.Time{}, 10)
	if assert.Len(t, history, 1) {
		assert.Equal(t, 42.5, history[0].Metrics["node_cpu_usage_percent"])
		assert.Equal(t, 1.0, history[0].Metrics["collector_probe_core_active"])
		assert.Equal(t, 2.0, history[0].Metrics["collector_protection_mode_severity"])
		_, exists := history[0].Metrics["rca_cpu_process_percent"]
		assert.False(t, exists)
	}
}

func TestMetricHistoryRespectsSinceAndLimit(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().Truncate(time.Second)

	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 10},
	}, now)
	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 20},
	}, now.Add(time.Second))
	store.StoreMetrics("c-1", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 30},
	}, now.Add(2*time.Second))

	history := store.MetricHistory("c-1", now.Add(500*time.Millisecond), 1)
	if assert.Len(t, history, 1) {
		assert.Equal(t, 30.0, history[0].Metrics["node_cpu_usage_percent"])
	}
}

func TestNodeReturnsDefensiveCopiesForProcessesAndLogs(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now()

	store.StoreProcesses("c-1", []*telemetryv1.ProcessSample{
		{Pid: 42, Name: "trainer", CpuPercent: 80},
	}, now)
	store.StoreLogs("c-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "oom", Example: "OOM killer invoked", Count: 1},
	}, now)

	snap := store.Node("c-1")
	if assert.NotNil(t, snap) && assert.Len(t, snap.Processes, 1) && assert.Len(t, snap.Logs, 1) {
		snap.Processes[0].Name = "mutated"
		snap.Logs[0].Fingerprint = "changed"
	}

	again := store.Node("c-1")
	if assert.NotNil(t, again) && assert.Len(t, again.Processes, 1) && assert.Len(t, again.Logs, 1) {
		assert.Equal(t, "trainer", again.Processes[0].Name)
		assert.Equal(t, "oom", again.Logs[0].Fingerprint)
	}
}
