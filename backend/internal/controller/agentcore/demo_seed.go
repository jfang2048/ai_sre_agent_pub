package agent

import (
	"fmt"
	"math"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

// SeedDemoData populates the store and log index with deterministic synthetic
// telemetry so that the agent workflow endpoints (potential-risks, joint-risk,
// rca) produce meaningful, non-empty results without a real collector.
//
// Three scenarios are seeded:
//   - demo-web-1:   CPU ramp + IO spike + retransmit burst  → "cascading latency"
//   - demo-gpu-1:   memory leak + IO pressure               → "GPU training stall"
//   - demo-db-1:    retransmit ratio high + softnet drops    → "network saturation"
func SeedDemoData(store *ingest.MemoryStore, logIdx *logindex.Index) {
	now := time.Now().UTC()

	type scenario struct {
		id       string
		hostname string
		labels   map[string]string
		metrics  func(t time.Time, i int) map[string]float64
		procs    []*telemetryv1.ProcessSample
		logs     []*telemetryv1.LogFingerprint
	}

	scenarios := []scenario{
		{
			id:       "demo-web-1",
			hostname: "web-prod-01.us-east-1",
			labels:   map[string]string{"role": "web", "env": "production", "region": "us-east-1"},
			metrics: func(_ time.Time, i int) map[string]float64 {
				cpuBase := 35.0 + float64(i)*1.8
				if cpuBase > 92 {
					cpuBase = 92
				}
				ioLat := 2.0 + float64(i)*0.8 + math.Sin(float64(i)/3)*2
				retrans := 0.01 + float64(i)*0.003
				if retrans > 0.12 {
					retrans = 0.12
				}
				memUsed := 4.2e9 + float64(i)*0.05e9
				memTotal := 8.0e9
				netRxBps := 18e6 + float64(i)*8e5
				netTxBps := 12e6 + float64(i)*6e5
				diskReadBps := 22e6 + float64(i)*1.1e6
				diskWriteBps := 14e6 + float64(i)*9e5
				procsRunning := 180.0 + float64(i%24)
				procsBlocked := 1.0 + float64(i/10)
				return map[string]float64{
					"node_cpu_usage_percent":                               cpuBase,
					"node_memory_Used_bytes":                               memUsed,
					"node_memory_MemTotal_bytes":                           memTotal,
					"node_memory_MemAvailable_bytes":                       memTotal - memUsed,
					"node_memory_used_bytes":                               memUsed,
					"node_memory_total_bytes":                              memTotal,
					"node_memory_available_bytes":                          memTotal - memUsed,
					"node_disk_io_time_weighted_seconds":                   ioLat,
					"node_disk_total_read_bytes_per_second":                diskReadBps,
					"node_disk_total_written_bytes_per_second":             diskWriteBps,
					"node_disk_total_iops_per_second":                      1400 + float64(i)*30,
					"node_pressure_io_full_avg10":                          ioLat * 1.5,
					"node_tcp_retransmit_ratio":                            retrans,
					"node_softnet_dropped_per_second":                      float64(i) * 0.3,
					"node_load1":                                           cpuBase / 25,
					"node_load5":                                           cpuBase / 30,
					"node_load15":                                          cpuBase / 35,
					"node_network_receive_bytes_per_second":                netRxBps,
					"node_network_transmit_bytes_per_second":               netTxBps,
					"node_network_total_receive_bytes_per_second":          netRxBps,
					"node_network_total_transmit_bytes_per_second":         netTxBps,
					"node_network_receive_bytes_total":                     1e8 + float64(i)*5e6,
					"node_network_transmit_bytes_total":                    8e7 + float64(i)*4e6,
					"node_procs_running":                                   procsRunning,
					"node_procs_blocked":                                   procsBlocked,
					"node_security_world_writable_sensitive_paths":         float64((i / 8) + 1),
					"node_security_weak_permission_count":                  float64((i / 6) + 2),
					"node_security_unexpected_listening_ports_count":       float64((i / 10) + 1),
					"node_security_suspicious_outbound_destinations_count": float64((i / 9) + 1),
					"node_security_large_file_growth_bytes":                float64(i) * 4 * 1024 * 1024,
				}
			},
			procs: []*telemetryv1.ProcessSample{
				{Pid: 1234, Name: "nginx", CpuPercent: 45.2, RssBytes: 512 * 1024 * 1024, IoReadBps: 5e6, IoWriteBps: 2e6},
				{Pid: 2345, Name: "node", CpuPercent: 32.1, RssBytes: 1024 * 1024 * 1024, IoReadBps: 1e6, IoWriteBps: 8e5},
				{Pid: 3456, Name: "postgres", CpuPercent: 18.5, RssBytes: 2048 * 1024 * 1024, IoReadBps: 15e6, IoWriteBps: 10e6},
			},
			logs: []*telemetryv1.LogFingerprint{
				{Fingerprint: "connection_timeout", Example: "2024-01-15T10:30:00Z ERROR [nginx] upstream connection timeout after 30s to backend:8080", Count: 142, TimestampUnixNano: now.Add(-2 * time.Minute).UnixNano()},
				{Fingerprint: "retry_exhausted", Example: "2024-01-15T10:31:00Z ERROR [node] retry exhausted for POST /api/v1/orders: 3/3 attempts failed", Count: 87, TimestampUnixNano: now.Add(-1 * time.Minute).UnixNano()},
				{Fingerprint: "disk_io_latency", Example: "2024-01-15T10:32:00Z WARN [postgres] checkpoint taking 12.3s, expected < 5s, IO latency elevated", Count: 23, TimestampUnixNano: now.Add(-5 * time.Minute).UnixNano()},
				{Fingerprint: "connection_pool", Example: "2024-01-15T10:33:00Z WARN [node] connection pool exhausted, 0 available of 50", Count: 56, TimestampUnixNano: now.Add(-3 * time.Minute).UnixNano()},
			},
		},
		{
			id:       "demo-gpu-1",
			hostname: "gpu-train-03.us-west-2",
			labels:   map[string]string{"role": "gpu-training", "env": "production", "region": "us-west-2"},
			metrics: func(_ time.Time, i int) map[string]float64 {
				memUsed := 12.0e9 + float64(i)*0.25e9
				memTotal := 32.0e9
				if memUsed > 30e9 {
					memUsed = 30e9
				}
				cpuBase := 55.0 + math.Sin(float64(i)/4)*8
				ioPressure := 5.0 + float64(i)*0.6
				if ioPressure > 45 {
					ioPressure = 45
				}
				gpuUtil := 85 + math.Sin(float64(i)/2)*10
				netRxBps := 6e6 + float64(i)*4e5
				netTxBps := 5e6 + float64(i)*3e5
				diskReadBps := 45e6 + float64(i)*2.5e6
				diskWriteBps := 32e6 + float64(i)*1.8e6
				procsRunning := 640.0 + float64(i%32)
				procsBlocked := 2.0 + float64(i/12)
				return map[string]float64{
					"node_cpu_usage_percent":                       cpuBase,
					"node_memory_Used_bytes":                       memUsed,
					"node_memory_MemTotal_bytes":                   memTotal,
					"node_memory_MemAvailable_bytes":               memTotal - memUsed,
					"node_memory_used_bytes":                       memUsed,
					"node_memory_total_bytes":                      memTotal,
					"node_memory_available_bytes":                  memTotal - memUsed,
					"node_disk_io_time_weighted_seconds":           1.5 + float64(i)*0.3,
					"node_disk_total_read_bytes_per_second":        diskReadBps,
					"node_disk_total_written_bytes_per_second":     diskWriteBps,
					"node_disk_total_iops_per_second":              2200 + float64(i)*40,
					"node_pressure_io_full_avg10":                  ioPressure,
					"node_tcp_retransmit_ratio":                    0.005,
					"node_softnet_dropped_per_second":              0.1,
					"node_network_receive_bytes_per_second":        netRxBps,
					"node_network_transmit_bytes_per_second":       netTxBps,
					"node_network_total_receive_bytes_per_second":  netRxBps,
					"node_network_total_transmit_bytes_per_second": netTxBps,
					"node_procs_running":                           procsRunning,
					"node_procs_blocked":                           procsBlocked,
					"node_gpu_utilization_percent":                 gpuUtil,
					"node_gpu_utilization_sm_avg_percent":          gpuUtil,
					"node_gpu_memory_used_bytes":                   10e9 + float64(i)*0.15e9,
					"node_gpu_memory_total_bytes":                  16e9,
					"node_gpu_memory_used_total_mib":               (10e9 + float64(i)*0.15e9) / (1024 * 1024),
					"node_gpu_memory_total_all_mib":                16e9 / (1024 * 1024),
					"node_security_sysctl_risky_count":             float64(i % 3),
				}
			},
			procs: []*telemetryv1.ProcessSample{
				{Pid: 5001, Name: "python3", CpuPercent: 380.0, RssBytes: 12 * 1024 * 1024 * 1024, IoReadBps: 50e6, IoWriteBps: 30e6},
				{Pid: 5002, Name: "nccl-proxy", CpuPercent: 15.3, RssBytes: 256 * 1024 * 1024, IoReadBps: 1e6, IoWriteBps: 1e6},
			},
			logs: []*telemetryv1.LogFingerprint{
				{Fingerprint: "gpu_memory_pressure", Example: "2024-01-15T10:30:00Z WARN [training] GPU memory fragmentation at 78%, allocation slow path triggered", Count: 34, TimestampUnixNano: now.Add(-2 * time.Minute).UnixNano()},
				{Fingerprint: "checkpoint_slow", Example: "2024-01-15T10:31:00Z WARN [training] checkpoint write took 45s (expected <10s), IO bottleneck suspected", Count: 8, TimestampUnixNano: now.Add(-10 * time.Minute).UnixNano()},
				{Fingerprint: "oom_risk", Example: "2024-01-15T10:32:00Z ERROR [training] RSS growth rate 250MB/min, projected OOM in ~32 minutes", Count: 5, TimestampUnixNano: now.Add(-4 * time.Minute).UnixNano()},
			},
		},
		{
			id:       "demo-db-1",
			hostname: "db-primary-01.eu-west-1",
			labels:   map[string]string{"role": "database", "env": "production", "region": "eu-west-1"},
			metrics: func(_ time.Time, i int) map[string]float64 {
				retrans := 0.02 + float64(i)*0.005
				if retrans > 0.18 {
					retrans = 0.18
				}
				softnet := 5.0 + float64(i)*2.5
				netRxBps := 22e6 + float64(i)*1.5e6
				netTxBps := 16e6 + float64(i)*1.1e6
				diskReadBps := 80e6 + float64(i)*3.2e6
				diskWriteBps := 60e6 + float64(i)*2.6e6
				procsRunning := 320.0 + float64(i%28)
				procsBlocked := 2.0 + float64(i/9)
				return map[string]float64{
					"node_cpu_usage_percent":                       42.0 + float64(i)*0.5,
					"node_memory_Used_bytes":                       28e9,
					"node_memory_MemTotal_bytes":                   64e9,
					"node_memory_MemAvailable_bytes":               36e9,
					"node_memory_used_bytes":                       28e9,
					"node_memory_total_bytes":                      64e9,
					"node_memory_available_bytes":                  36e9,
					"node_disk_io_time_weighted_seconds":           0.8 + float64(i)*0.1,
					"node_disk_total_read_bytes_per_second":        diskReadBps,
					"node_disk_total_written_bytes_per_second":     diskWriteBps,
					"node_disk_total_iops_per_second":              2600 + float64(i)*45,
					"node_pressure_io_full_avg10":                  2.0 + float64(i)*0.3,
					"node_tcp_retransmit_ratio":                    retrans,
					"node_softnet_dropped_per_second":              softnet,
					"node_network_receive_bytes_per_second":        netRxBps,
					"node_network_transmit_bytes_per_second":       netTxBps,
					"node_network_total_receive_bytes_per_second":  netRxBps,
					"node_network_total_transmit_bytes_per_second": netTxBps,
					"node_network_receive_bytes_total":             5e8 + float64(i)*2e7,
					"node_network_transmit_bytes_total":            3e8 + float64(i)*1.5e7,
					"node_procs_running":                           procsRunning,
					"node_procs_blocked":                           procsBlocked,
					"node_security_selinux_disabled":               1,
					"node_security_apparmor_disabled":              1,
				}
			},
			procs: []*telemetryv1.ProcessSample{
				{Pid: 9001, Name: "postgres", CpuPercent: 35.4, RssBytes: 16 * 1024 * 1024 * 1024, IoReadBps: 80e6, IoWriteBps: 60e6},
				{Pid: 9002, Name: "pgbouncer", CpuPercent: 8.2, RssBytes: 128 * 1024 * 1024, IoReadBps: 1e5, IoWriteBps: 1e5},
			},
			logs: []*telemetryv1.LogFingerprint{
				{Fingerprint: "replication_lag", Example: "2024-01-15T10:30:00Z WARN [postgres] replication lag to replica-02 reached 8.2s, threshold 5s", Count: 45, TimestampUnixNano: now.Add(-1 * time.Minute).UnixNano()},
				{Fingerprint: "connection_refused", Example: "2024-01-15T10:31:00Z ERROR [pgbouncer] server connection refused: too many connections (max 500)", Count: 112, TimestampUnixNano: now.Add(-2 * time.Minute).UnixNano()},
				{Fingerprint: "slow_query", Example: "2024-01-15T10:32:00Z WARN [postgres] slow query: SELECT * FROM orders WHERE ... took 12.4s", Count: 67, TimestampUnixNano: now.Add(-3 * time.Minute).UnixNano()},
			},
		},
	}

	const historySamples = 30
	const sampleInterval = 2 * time.Minute

	for _, sc := range scenarios {
		store.UpsertCollector(&telemetryv1.CollectorInfo{
			CollectorId: sc.id,
			Hostname:    sc.hostname,
			Version:     "v0.7-demo",
			Os:          "linux",
			Arch:        "amd64",
			Labels:      mapToLabels(sc.labels),
		}, now)

		for i := 0; i < historySamples; i++ {
			ts := now.Add(-time.Duration(historySamples-i) * sampleInterval)
			metrics := sc.metrics(ts, i)
			protoMetrics := make([]*telemetryv1.Metric, 0, len(metrics))
			for name, value := range metrics {
				protoMetrics = append(protoMetrics, &telemetryv1.Metric{Name: name, Value: value})
			}
			protoMetrics = append(protoMetrics, demoEBPFMetrics(sc.id, ts, i)...)
			store.StoreMetrics(sc.id, protoMetrics, ts)
		}

		store.StoreProcesses(sc.id, sc.procs, now.Add(-30*time.Second))
		store.StoreLogs(sc.id, sc.logs, now.Add(-1*time.Minute))

		if logIdx != nil {
			events := make([]logindex.RawEvent, 0, len(sc.logs))
			for _, lf := range sc.logs {
				if lf == nil || lf.Example == "" {
					continue
				}
				events = append(events, logindex.RawEvent{
					CollectorID: sc.id,
					Message:     lf.Example,
					Fingerprint: lf.Fingerprint,
					Count:       lf.Count,
					Timestamp:   time.Unix(0, lf.TimestampUnixNano),
				})
			}
			logIdx.AddBatch(events)
		}

		store.StoreBatchMeta(sc.id, &telemetryv1.TelemetryBatch{
			BatchId:          fmt.Sprintf("demo-batch-%s", sc.id),
			WallTimeUnixNano: now.UnixNano(),
		}, now)
	}
}

func mapToLabels(m map[string]string) []*telemetryv1.Label {
	labels := make([]*telemetryv1.Label, 0, len(m))
	for k, v := range m {
		labels = append(labels, &telemetryv1.Label{Key: k, Value: v})
	}
	return labels
}

func demoEBPFMetrics(scenarioID string, ts time.Time, i int) []*telemetryv1.Metric {
	metrics := make([]*telemetryv1.Metric, 0, 24)
	addEvent := func(evidenceID, category, eventType, severity, pid, description string, labels map[string]string) {
		evLabels := []*telemetryv1.Label{
			{Key: "evidence_id", Value: evidenceID},
			{Key: "category", Value: category},
			{Key: "type", Value: eventType},
			{Key: "severity", Value: severity},
			{Key: "confidence", Value: "0.90"},
			{Key: "pid", Value: pid},
			{Key: "scope", Value: "node"},
			{Key: "description", Value: description},
			{Key: "ts_unix_nano", Value: fmt.Sprintf("%d", ts.UnixNano())},
		}
		for k, v := range labels {
			evLabels = append(evLabels, &telemetryv1.Label{Key: k, Value: v})
		}
		metrics = append(metrics, &telemetryv1.Metric{
			Name:   "node_ebpf_runtime_event",
			Value:  1,
			Labels: evLabels,
		})
	}

	metrics = append(metrics, &telemetryv1.Metric{
		Name:  "node_ebpf_runtime_mode",
		Value: 1,
		Labels: []*telemetryv1.Label{
			{Key: "mode", Value: "libbpf_ringbuf"},
		},
	})

	baseExec := float64(100 + i*3)
	baseFork := float64(80 + i*2)
	baseOpen := float64(140 + i*4)
	baseConnect := float64(60 + i*3)
	baseBind := float64(6 + i/4)
	syscalls := []struct {
		name  string
		value float64
	}{
		{"execve", baseExec},
		{"fork", baseFork},
		{"open", baseOpen},
		{"connect", baseConnect},
		{"bind", baseBind},
	}
	for _, syscall := range syscalls {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:  "node_ebpf_syscall_statistics_total",
			Value: syscall.value,
			Labels: []*telemetryv1.Label{
				{Key: "syscall", Value: syscall.name},
			},
		})
	}

	metrics = append(metrics,
		&telemetryv1.Metric{Name: "node_ebpf_abnormal_bind_ports_count", Value: float64((i / 9) + 1)},
		&telemetryv1.Metric{Name: "node_ebpf_long_lived_tcp_connections", Value: float64((i / 10) + 1)},
		&telemetryv1.Metric{Name: "node_ebpf_privilege_escalation_attempts_total", Value: float64(i / 11)},
		&telemetryv1.Metric{
			Name:  "node_ebpf_process_events_total",
			Value: float64(40 + i*2),
			Labels: []*telemetryv1.Label{
				{Key: "pid", Value: "4210"},
				{Key: "process", Value: "python3"},
				{Key: "type", Value: "execve"},
			},
		},
	)

	// Deterministic demo anomaly set required by acceptance criteria.
	if scenarioID == "demo-web-1" && i >= 22 {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:  "node_security_finding",
			Value: 0.93,
			Labels: []*telemetryv1.Label{
				{Key: "finding_id", Value: fmt.Sprintf("sf-demo-outbound-%02d", i)},
				{Key: "category", Value: "unexpected_outbound"},
				{Key: "severity", Value: "high"},
				{Key: "scope", Value: "runtime"},
				{Key: "summary", Value: "Unexpected outbound connections escaped the local baseline"},
				{Key: "evidence", Value: "remote=203.0.113.77:4444 || process=python3 || path=/tmp/bootstrap"},
				{Key: "next_step", Value: "Validate egress policy and inspect the exec chain"},
				{Key: "source", Value: "collector_security_audit"},
				{Key: "pid", Value: "4210"},
				{Key: "process", Value: "python3"},
				{Key: "remote_ip", Value: "203.0.113.77"},
				{Key: "path", Value: "/tmp/bootstrap"},
			},
		})
		addEvent(
			fmt.Sprintf("ev-demo-exec-chain-%02d", i),
			"process",
			"execve",
			"high",
			"4210",
			"abnormal exec chain: /tmp/bootstrap -> /usr/bin/curl",
			map[string]string{
				"path": "/tmp/bootstrap",
				"comm": "python3",
			},
		)
		addEvent(
			fmt.Sprintf("ev-demo-outbound-%02d", i),
			"network",
			"connect",
			"high",
			"4210",
			"suspicious outbound connection to non-baseline remote IP",
			map[string]string{
				"remote_ip": "203.0.113.77",
				"port":      "4444",
				"comm":      "python3",
			},
		)
		addEvent(
			fmt.Sprintf("ev-demo-bind-%02d", i),
			"security",
			"abnormal_bind_port",
			"high",
			"4210",
			"unexpected listening port observed",
			map[string]string{
				"port": "31337",
				"comm": "python3",
			},
		)
		addEvent(
			fmt.Sprintf("ev-demo-perm-%02d", i),
			"file",
			"open",
			"high",
			"4210",
			"sensitive file access and permission anomaly",
			map[string]string{
				"path": "/etc/shadow",
				"comm": "python3",
			},
		)
		addEvent(
			fmt.Sprintf("ev-demo-longtcp-%02d", i),
			"network",
			"long_lived_tcp",
			"medium",
			"4210",
			"long-lived tcp connection exceeded baseline threshold",
			map[string]string{
				"remote_ip": "198.51.100.22",
				"port":      "443",
			},
		)
	}

	return metrics
}
