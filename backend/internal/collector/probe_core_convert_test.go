package collector

import (
	"testing"
	"time"

	probeipcv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/probeipc/v1"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestConvertProbeCoreBatchAddsCompatibilityAliases(t *testing.T) {
	batch := &probeipcv1.ProbeBatch{
		CollectedAtUnixNano: time.Now().UnixNano(),
		Metrics: []*probeipcv1.Metric{
			{Name: "probe_core_cpu_usage_percent", Value: 88.5},
			{Name: "probe_core_memory_total_bytes", Value: 200},
			{Name: "probe_core_memory_used_bytes", Value: 50},
			{Name: "probe_core_pressure_io_full_avg10", Value: 3.1},
			{Name: "probe_core_pressure_io_full_total", Value: 5_000_000},
			{Name: "probe_core_disk_read_bytes_per_sec", Value: 1024, Labels: []*probeipcv1.Label{{Key: "device", Value: "nvme0n1"}}},
			{Name: "probe_core_disk_write_bytes_per_sec", Value: 2048, Labels: []*probeipcv1.Label{{Key: "device", Value: "nvme0n1"}}},
			{Name: "probe_core_disk_reads_per_sec", Value: 10, Labels: []*probeipcv1.Label{{Key: "device", Value: "nvme0n1"}}},
			{Name: "probe_core_disk_writes_per_sec", Value: 20, Labels: []*probeipcv1.Label{{Key: "device", Value: "nvme0n1"}}},
			{Name: "probe_core_disk_await_ms", Value: 4, Labels: []*probeipcv1.Label{{Key: "device", Value: "nvme0n1"}}},
			{Name: "probe_core_disk_queue_depth", Value: 2, Labels: []*probeipcv1.Label{{Key: "device", Value: "nvme0n1"}}},
			{Name: "probe_core_disk_queue_capacity", Value: 8, Labels: []*probeipcv1.Label{{Key: "device", Value: "nvme0n1"}}},
			{Name: "probe_core_network_rx_bytes_per_sec", Value: 100, Labels: []*probeipcv1.Label{{Key: "iface", Value: "eth0"}}},
			{Name: "probe_core_network_tx_bytes_per_sec", Value: 50, Labels: []*probeipcv1.Label{{Key: "iface", Value: "eth0"}}},
			{Name: "probe_core_network_rx_packets_per_sec", Value: 20, Labels: []*probeipcv1.Label{{Key: "iface", Value: "eth0"}}},
			{Name: "probe_core_network_tx_packets_per_sec", Value: 10, Labels: []*probeipcv1.Label{{Key: "iface", Value: "eth0"}}},
			{Name: "probe_core_network_speed_mbps", Value: 1000, Labels: []*probeipcv1.Label{{Key: "iface", Value: "eth0"}}},
			{Name: "probe_core_network_tcp_retransmissions_per_sec", Value: 1.5},
			{Name: "probe_core_rdma_ports", Value: 2},
			{Name: "probe_core_rdma_port_state", Value: 4, Labels: []*probeipcv1.Label{{Key: "device", Value: "mlx5_0"}, {Key: "port", Value: "1"}}},
			{Name: "probe_core_rdma_port_transmit_bytes_per_second", Value: 1_000_000, Labels: []*probeipcv1.Label{{Key: "device", Value: "mlx5_0"}, {Key: "port", Value: "1"}}},
			{Name: "probe_core_rdma_port_receive_bytes_per_second", Value: 1_200_000, Labels: []*probeipcv1.Label{{Key: "device", Value: "mlx5_0"}, {Key: "port", Value: "1"}}},
			{Name: "probe_core_rdma_port_congestion_events_per_second", Value: 2.5, Labels: []*probeipcv1.Label{{Key: "device", Value: "mlx5_0"}, {Key: "port", Value: "1"}}},
			{Name: "probe_core_rdma_port_pfc_pause_frames_per_second", Value: 1.1, Labels: []*probeipcv1.Label{{Key: "device", Value: "mlx5_0"}, {Key: "port", Value: "1"}}},
			{Name: "probe_core_rdma_port_ecn_marked_ratio", Value: 0.02, Labels: []*probeipcv1.Label{{Key: "device", Value: "mlx5_0"}, {Key: "port", Value: "1"}}},
			{Name: "probe_core_rdma_congestion_events_per_second", Value: 3.4},
			{Name: "probe_core_rdma_pfc_pause_frames_per_second", Value: 1.1},
			{Name: "probe_core_rdma_ecn_marked_ratio", Value: 0.015},
			{Name: "probe_core_process_sched_run_seconds_total", Value: 10.5, Labels: []*probeipcv1.Label{{Key: "pid", Value: "42"}, {Key: "process", Value: "trainer"}}},
			{Name: "probe_core_process_sched_wait_seconds_total", Value: 2.1, Labels: []*probeipcv1.Label{{Key: "pid", Value: "42"}, {Key: "process", Value: "trainer"}}},
			{Name: "probe_core_process_sched_wait_ratio", Value: 0.2, Labels: []*probeipcv1.Label{{Key: "pid", Value: "42"}, {Key: "process", Value: "trainer"}}},
			{Name: "probe_core_process_block_io_delay_seconds_total", Value: 1.7, Labels: []*probeipcv1.Label{{Key: "pid", Value: "42"}, {Key: "process", Value: "trainer"}}},
			{Name: "probe_core_process_block_io_delay_seconds_per_second", Value: 0.09, Labels: []*probeipcv1.Label{{Key: "pid", Value: "42"}, {Key: "process", Value: "trainer"}}},
			{Name: "probe_core_process_socket_connections", Value: 6, Labels: []*probeipcv1.Label{{Key: "pid", Value: "42"}, {Key: "process", Value: "trainer"}}},
			{Name: "probe_core_process_socket_tx_queue_bytes", Value: 64, Labels: []*probeipcv1.Label{{Key: "pid", Value: "42"}, {Key: "process", Value: "trainer"}}},
			{Name: "probe_core_process_socket_rx_queue_bytes", Value: 32, Labels: []*probeipcv1.Label{{Key: "pid", Value: "42"}, {Key: "process", Value: "trainer"}}},
		},
		Processes: []*probeipcv1.ProcessSample{
			{Pid: 42, Name: "trainer", CpuPercent: 70, RssBytes: 1024, IoReadBps: 11, IoWriteBps: 22},
		},
	}

	metrics, processes := convertProbeCoreBatch(batch, time.Now())
	require.NotEmpty(t, metrics)
	require.Len(t, processes, 1)
	require.Equal(t, int32(42), processes[0].Pid)

	metricMap := flattenMetrics(metrics)
	require.Contains(t, metricMap, "node_cpu_usage_percent")
	require.Contains(t, metricMap, "node_memory_MemTotal_bytes")
	require.Contains(t, metricMap, "node_memory_Used_bytes")
	require.Contains(t, metricMap, "node_pressure_io_full_avg10")
	require.Contains(t, metricMap, "node_pressure_io_full_seconds_total")
	require.Contains(t, metricMap, "node_disk_total_read_bytes_per_second")
	require.Contains(t, metricMap, "node_disk_total_written_bytes_per_second")
	require.Contains(t, metricMap, "node_disk_total_iops_per_second")
	require.Contains(t, metricMap, "node_network_total_receive_bytes_per_second")
	require.Contains(t, metricMap, "node_network_total_transmit_bytes_per_second")
	require.Contains(t, metricMap, "node_network_utilization_peak_percent")
	require.Contains(t, metricMap, "node_tcp_retransmits_per_second")
	require.Contains(t, metricMap, "node_rdma_ports")
	require.Contains(t, metricMap, "node_rdma_port_state")
	require.Contains(t, metricMap, "node_rdma_port_transmit_bytes_per_second")
	require.Contains(t, metricMap, "node_rdma_port_congestion_events_per_second")
	require.Contains(t, metricMap, "node_rdma_pfc_pause_frames_per_second")
	require.Contains(t, metricMap, "node_rdma_ecn_marked_ratio")
	require.Contains(t, metricMap, "rca_cpu_process_sched_run_seconds_total")
	require.Contains(t, metricMap, "rca_cpu_process_sched_wait_seconds_total")
	require.Contains(t, metricMap, "rca_cpu_process_sched_wait_ratio")
	require.Contains(t, metricMap, "rca_io_process_block_delay_seconds_total")
	require.Contains(t, metricMap, "rca_io_process_block_delay_seconds_per_second")
	require.Contains(t, metricMap, "rca_net_process_connections")
	require.Contains(t, metricMap, "rca_net_process_queued_bytes")
}

func flattenMetrics(metrics []*telemetryv1.Metric) map[string]float64 {
	out := make(map[string]float64, len(metrics))
	for _, metric := range metrics {
		out[metric.Name] = metric.Value
	}
	return out
}
