package collector

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	probeipcv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/probeipc/v1"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

type diskAliasState struct {
	awaitSeconds float64
	iops         float64
	queueDepth   float64
	queueCap     float64
}

type networkAliasState struct {
	rxBps       float64
	txBps       float64
	speedBitsPS float64
}

type processQueueState struct {
	name        string
	queuedBytes float64
	connections float64
}

func convertProbeCoreBatch(batch *probeipcv1.ProbeBatch, fallbackNow time.Time) ([]*telemetryv1.Metric, []*telemetryv1.ProcessSample) {
	if batch == nil {
		return nil, nil
	}
	ts := batch.GetCollectedAtUnixNano()
	if ts <= 0 {
		ts = fallbackNow.UnixNano()
	}

	metrics := make([]*telemetryv1.Metric, 0, len(batch.GetMetrics())+96)
	processes := make([]*telemetryv1.ProcessSample, 0, len(batch.GetProcesses()))

	diskSeen := false
	diskTotalReadBPS := 0.0
	diskTotalWriteBPS := 0.0
	diskTotalReadsPS := 0.0
	diskTotalWritesPS := 0.0
	diskTotalQueueDepth := 0.0
	diskLatencyWeighted := 0.0
	diskLatencyWeight := 0.0
	diskLatencyFallbackSum := 0.0
	diskLatencyFallbackCount := 0.0
	diskUtilPeakPercent := 0.0
	diskByDevice := map[string]*diskAliasState{}

	netSeen := false
	netTotalRxBPS := 0.0
	netTotalTxBPS := 0.0
	netTotalRxPPS := 0.0
	netTotalTxPPS := 0.0
	netByIface := map[string]*networkAliasState{}

	procQueue := map[string]*processQueueState{}

	for _, metric := range batch.GetMetrics() {
		name := strings.TrimSpace(metric.GetName())
		value := metric.GetValue()
		if name == "" || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}

		labelsMap := probeCoreLabelsMap(metric.GetLabels())
		labels := buildLabels(labelsMap)
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              name,
			Value:             value,
			TimestampUnixNano: ts,
			Labels:            labels,
		})

		switch name {
		case "probe_core_loadavg_1m":
			metrics = append(metrics, newAliasedMetric("node_load1", value, ts, labelsMap))
		case "probe_core_loadavg_5m":
			metrics = append(metrics, newAliasedMetric("node_load5", value, ts, labelsMap))
		case "probe_core_loadavg_15m":
			metrics = append(metrics, newAliasedMetric("node_load15", value, ts, labelsMap))
		case "probe_core_cpu_usage_percent":
			metrics = append(metrics, newAliasedMetric("node_cpu_usage_percent", value, ts, labelsMap))
		case "probe_core_sched_context_switches_total":
			metrics = append(metrics, newAliasedMetric("node_context_switches_total", value, ts, labelsMap))
		case "probe_core_sched_running_tasks":
			metrics = append(metrics, newAliasedMetric("node_procs_running", value, ts, labelsMap))
		case "probe_core_sched_blocked_tasks":
			metrics = append(metrics, newAliasedMetric("node_procs_blocked", value, ts, labelsMap))
		case "probe_core_memory_total_bytes":
			metrics = append(metrics, newAliasedMetric("node_memory_MemTotal_bytes", value, ts, labelsMap))
		case "probe_core_memory_available_bytes":
			metrics = append(metrics, newAliasedMetric("node_memory_MemAvailable_bytes", value, ts, labelsMap))
		case "probe_core_memory_used_bytes":
			metrics = append(metrics,
				newAliasedMetric("node_memory_Used_bytes", value, ts, labelsMap),
			)
		case "probe_core_memory_used_percent":
			metrics = append(metrics, newAliasedMetric("node_memory_used_percent", value, ts, labelsMap))
		case "probe_core_memory_cached_bytes":
			metrics = append(metrics, newAliasedMetric("node_memory_Cached_bytes", value, ts, labelsMap))
		case "probe_core_memory_buffers_bytes":
			metrics = append(metrics, newAliasedMetric("node_memory_Buffers_bytes", value, ts, labelsMap))
		case "probe_core_swap_total_bytes":
			metrics = append(metrics, newAliasedMetric("node_memory_SwapTotal_bytes", value, ts, labelsMap))
		case "probe_core_swap_free_bytes":
			metrics = append(metrics, newAliasedMetric("node_memory_SwapFree_bytes", value, ts, labelsMap))
		case "probe_core_vm_pgfault_total":
			metrics = append(metrics, newAliasedMetric("node_vmstat_pgfault", value, ts, labelsMap))
		case "probe_core_vm_pgmajfault_total":
			metrics = append(metrics, newAliasedMetric("node_vmstat_pgmajfault", value, ts, labelsMap))
		case "probe_core_vm_pgscan_kswapd_total":
			metrics = append(metrics, newAliasedMetric("node_vmstat_pgscan_kswapd", value, ts, labelsMap))
		case "probe_core_vm_pgsteal_kswapd_total":
			metrics = append(metrics, newAliasedMetric("node_vmstat_pgsteal_kswapd", value, ts, labelsMap))
		case "probe_core_disk_read_bytes_per_sec":
			diskSeen = true
			diskTotalReadBPS += value
			metrics = append(metrics, newAliasedMetric("node_disk_read_bytes_per_second", value, ts, labelsMap))
		case "probe_core_disk_write_bytes_per_sec":
			diskSeen = true
			diskTotalWriteBPS += value
			metrics = append(metrics, newAliasedMetric("node_disk_written_bytes_per_second", value, ts, labelsMap))
		case "probe_core_disk_reads_per_sec":
			diskSeen = true
			diskTotalReadsPS += value
			if dev := labelsMap["device"]; dev != "" {
				state := ensureDiskAliasState(diskByDevice, dev)
				state.iops += value
			}
			metrics = append(metrics, newAliasedMetric("node_disk_reads_per_second", value, ts, labelsMap))
		case "probe_core_disk_writes_per_sec":
			diskSeen = true
			diskTotalWritesPS += value
			if dev := labelsMap["device"]; dev != "" {
				state := ensureDiskAliasState(diskByDevice, dev)
				state.iops += value
			}
			metrics = append(metrics, newAliasedMetric("node_disk_writes_per_second", value, ts, labelsMap))
		case "probe_core_disk_await_ms":
			diskSeen = true
			seconds := value / 1000.0
			if dev := labelsMap["device"]; dev != "" {
				state := ensureDiskAliasState(diskByDevice, dev)
				state.awaitSeconds = seconds
			}
			metrics = append(metrics, newAliasedMetric("node_disk_avg_request_latency_seconds", seconds, ts, labelsMap))
		case "probe_core_disk_queue_depth":
			diskSeen = true
			diskTotalQueueDepth += value
			if dev := labelsMap["device"]; dev != "" {
				state := ensureDiskAliasState(diskByDevice, dev)
				state.queueDepth = value
			}
			metrics = append(metrics, newAliasedMetric("node_disk_queue_depth", value, ts, labelsMap))
		case "probe_core_disk_queue_capacity":
			diskSeen = true
			if dev := labelsMap["device"]; dev != "" {
				state := ensureDiskAliasState(diskByDevice, dev)
				state.queueCap = value
			}
			metrics = append(metrics, newAliasedMetric("node_disk_queue_capacity_requests", value, ts, labelsMap))
		case "probe_core_network_rx_bytes_per_sec":
			netSeen = true
			netTotalRxBPS += value
			if iface := labelsMap["iface"]; iface != "" {
				state := ensureNetworkAliasState(netByIface, iface)
				state.rxBps = value
			}
			metrics = append(metrics, newAliasedMetric("node_network_receive_bytes_per_second", value, ts, labelsMap))
		case "probe_core_network_tx_bytes_per_sec":
			netSeen = true
			netTotalTxBPS += value
			if iface := labelsMap["iface"]; iface != "" {
				state := ensureNetworkAliasState(netByIface, iface)
				state.txBps = value
			}
			metrics = append(metrics, newAliasedMetric("node_network_transmit_bytes_per_second", value, ts, labelsMap))
		case "probe_core_network_rx_packets_per_sec":
			netSeen = true
			netTotalRxPPS += value
			metrics = append(metrics, newAliasedMetric("node_network_receive_packets_per_second", value, ts, labelsMap))
		case "probe_core_network_tx_packets_per_sec":
			netSeen = true
			netTotalTxPPS += value
			metrics = append(metrics, newAliasedMetric("node_network_transmit_packets_per_second", value, ts, labelsMap))
		case "probe_core_network_rx_drops_total":
			metrics = append(metrics, newAliasedMetric("node_network_receive_drop_total", value, ts, labelsMap))
		case "probe_core_network_tx_drops_total":
			metrics = append(metrics, newAliasedMetric("node_network_transmit_drop_total", value, ts, labelsMap))
		case "probe_core_network_rx_errors_total":
			metrics = append(metrics, newAliasedMetric("node_network_receive_errs_total", value, ts, labelsMap))
		case "probe_core_network_tx_errors_total":
			metrics = append(metrics, newAliasedMetric("node_network_transmit_errs_total", value, ts, labelsMap))
		case "probe_core_network_tcp_retransmissions_total":
			metrics = append(metrics, newAliasedMetric("node_tcp_retransmits_total", value, ts, labelsMap))
		case "probe_core_network_tcp_retransmissions_per_sec":
			metrics = append(metrics, newAliasedMetric("node_tcp_retransmits_per_second", value, ts, labelsMap))
		case "probe_core_network_softnet_dropped_total":
			metrics = append(metrics, newAliasedMetric("node_softnet_dropped_total_all", value, ts, labelsMap))
		case "probe_core_network_softnet_squeezed_total":
			metrics = append(metrics, newAliasedMetric("node_softnet_times_squeezed_total_all", value, ts, labelsMap))
		case "probe_core_netlink_tx_queue_len":
			metrics = append(metrics, newAliasedMetric("node_network_interface_tx_queue_len", value, ts, labelsMap))
		case "probe_core_network_link_up":
			metrics = append(metrics, newAliasedMetric("node_network_interface_carrier_up", value, ts, labelsMap))
		case "probe_core_network_speed_mbps":
			bitsPerSecond := value * 1_000_000.0
			if iface := labelsMap["iface"]; iface != "" {
				state := ensureNetworkAliasState(netByIface, iface)
				state.speedBitsPS = bitsPerSecond
			}
			metrics = append(metrics, newAliasedMetric("node_network_interface_speed_bits_per_second", bitsPerSecond, ts, labelsMap))
		case "probe_core_rdma_ports":
			metrics = append(metrics, newAliasedMetric("node_rdma_ports", value, ts, labelsMap))
		case "probe_core_rdma_port_state":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_state", value, ts, labelsMap))
		case "probe_core_rdma_port_phys_state":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_phys_state", value, ts, labelsMap))
		case "probe_core_rdma_port_link_rate_gbps":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_link_rate_gbps", value, ts, labelsMap))
		case "probe_core_rdma_port_transmit_words_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_transmit_words_total", value, ts, labelsMap))
		case "probe_core_rdma_port_receive_words_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_receive_words_total", value, ts, labelsMap))
		case "probe_core_rdma_port_transmit_packets_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_transmit_packets_total", value, ts, labelsMap))
		case "probe_core_rdma_port_receive_packets_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_receive_packets_total", value, ts, labelsMap))
		case "probe_core_rdma_port_transmit_discards_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_transmit_discards_total", value, ts, labelsMap))
		case "probe_core_rdma_port_receive_errors_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_receive_errors_total", value, ts, labelsMap))
		case "probe_core_rdma_port_symbol_errors_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_symbol_errors_total", value, ts, labelsMap))
		case "probe_core_rdma_port_link_downed_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_link_downed_total", value, ts, labelsMap))
		case "probe_core_rdma_port_link_recovery_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_link_recovery_total", value, ts, labelsMap))
		case "probe_core_rdma_port_remote_physical_errors_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_remote_physical_errors_total", value, ts, labelsMap))
		case "probe_core_rdma_port_receive_constraint_errors_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_receive_constraint_errors_total", value, ts, labelsMap))
		case "probe_core_rdma_port_transmit_constraint_errors_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_transmit_constraint_errors_total", value, ts, labelsMap))
		case "probe_core_rdma_port_congestion_counter_total":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_congestion_counter_total", value, ts, labelsMap))
		case "probe_core_rdma_port_transmit_bytes_per_second":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_transmit_bytes_per_second", value, ts, labelsMap))
		case "probe_core_rdma_port_receive_bytes_per_second":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_receive_bytes_per_second", value, ts, labelsMap))
		case "probe_core_rdma_port_errors_per_second":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_errors_per_second", value, ts, labelsMap))
		case "probe_core_rdma_port_congestion_events_per_second":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_congestion_events_per_second", value, ts, labelsMap))
		case "probe_core_rdma_port_pfc_pause_frames_per_second":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_pfc_pause_frames_per_second", value, ts, labelsMap))
		case "probe_core_rdma_port_ecn_marked_ratio":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_ecn_marked_ratio", value, ts, labelsMap))
		case "probe_core_rdma_port_utilization_percent":
			metrics = append(metrics, newAliasedMetric("node_rdma_port_utilization_percent", value, ts, labelsMap))
		case "probe_core_rdma_errors_per_second":
			metrics = append(metrics, newAliasedMetric("node_rdma_errors_per_second", value, ts, labelsMap))
		case "probe_core_rdma_congestion_events_per_second":
			metrics = append(metrics, newAliasedMetric("node_rdma_congestion_events_per_second", value, ts, labelsMap))
		case "probe_core_rdma_pfc_pause_frames_per_second":
			metrics = append(metrics, newAliasedMetric("node_rdma_pfc_pause_frames_per_second", value, ts, labelsMap))
		case "probe_core_rdma_ecn_marked_ratio":
			metrics = append(metrics, newAliasedMetric("node_rdma_ecn_marked_ratio", value, ts, labelsMap))
		case "probe_core_gpu_utilization_percent":
			metrics = append(metrics, newAliasedMetric("node_gpu_utilization_sm_percent", value, ts, labelsMap))
		case "probe_core_gpu_memory_used_mb":
			metrics = append(metrics, newAliasedMetric("node_gpu_memory_used_mib", value, ts, labelsMap))
		case "probe_core_gpu_memory_total_mb":
			metrics = append(metrics, newAliasedMetric("node_gpu_memory_total_mib", value, ts, labelsMap))
		case "probe_core_gpu_temperature_c":
			metrics = append(metrics, newAliasedMetric("node_gpu_temperature_celsius", value, ts, labelsMap))
		case "probe_core_gpu_power_watts":
			metrics = append(metrics, newAliasedMetric("node_gpu_power_draw_watts", value, ts, labelsMap))
		case "probe_core_gpu_process_memory_used_mb":
			metrics = append(metrics, newAliasedMetric("node_gpu_process_memory_mib", value, ts, labelsMap))
		case "probe_core_process_cpu_percent":
			metrics = append(metrics, newAliasedMetric("node_process_cpu_percent", value, ts, labelsMap))
		case "probe_core_process_rss_bytes":
			metrics = append(metrics, newAliasedMetric("node_process_memory_rss_bytes", value, ts, labelsMap))
		case "probe_core_process_io_read_bps":
			metrics = append(metrics, newAliasedMetric("node_process_io_read_bytes_per_second", value, ts, labelsMap))
		case "probe_core_process_io_write_bps":
			metrics = append(metrics, newAliasedMetric("node_process_io_write_bytes_per_second", value, ts, labelsMap))
		case "probe_core_process_pss_bytes":
			metrics = append(metrics, newAliasedMetric("rca_memory_process_pss_bytes", value, ts, labelsMap))
		case "probe_core_process_page_faults_total":
			if labelsMap["type"] == "major" {
				metrics = append(metrics, newAliasedMetric("rca_memory_process_majflt_total", value, ts, labelsMap))
			}
		case "probe_core_process_sched_run_seconds_total":
			metrics = append(metrics, newAliasedMetric("rca_cpu_process_sched_run_seconds_total", value, ts, labelsMap))
		case "probe_core_process_sched_wait_seconds_total":
			metrics = append(metrics, newAliasedMetric("rca_cpu_process_sched_wait_seconds_total", value, ts, labelsMap))
		case "probe_core_process_sched_wait_ratio":
			metrics = append(metrics, newAliasedMetric("rca_cpu_process_sched_wait_ratio", value, ts, labelsMap))
		case "probe_core_process_block_io_delay_seconds_total":
			metrics = append(metrics, newAliasedMetric("rca_io_process_block_delay_seconds_total", value, ts, labelsMap))
		case "probe_core_process_block_io_delay_seconds_per_second":
			metrics = append(metrics, newAliasedMetric("rca_io_process_block_delay_seconds_per_second", value, ts, labelsMap))
		case "probe_core_process_socket_connections":
			pid := labelsMap["pid"]
			if pid != "" {
				state := procQueue[pid]
				if state == nil {
					state = &processQueueState{name: labelsMap["process"]}
					procQueue[pid] = state
				}
				state.connections = value
			}
		case "probe_core_process_socket_tx_queue_bytes", "probe_core_process_socket_rx_queue_bytes":
			pid := labelsMap["pid"]
			if pid != "" {
				state := procQueue[pid]
				if state == nil {
					state = &processQueueState{name: labelsMap["process"]}
					procQueue[pid] = state
				}
				state.queuedBytes += value
			}
		}

		if aliasName, aliasValue, ok := aliasProbeCorePressureMetric(name, value); ok {
			metrics = append(metrics, newAliasedMetric(aliasName, aliasValue, ts, labelsMap))
		}
	}

	if diskSeen {
		diskTotalIOPS := diskTotalReadsPS + diskTotalWritesPS
		for _, state := range diskByDevice {
			if state.iops > 0 && state.awaitSeconds > 0 {
				diskLatencyWeighted += state.awaitSeconds * state.iops
				diskLatencyWeight += state.iops
			} else if state.awaitSeconds > 0 {
				diskLatencyFallbackSum += state.awaitSeconds
				diskLatencyFallbackCount++
			}
			if state.queueCap > 0 {
				diskUtilPeakPercent = math.Max(diskUtilPeakPercent, clampPercent((state.queueDepth/state.queueCap)*100.0))
			}
		}

		metrics = append(metrics,
			newAliasedMetric("node_disk_total_read_bytes_per_second", diskTotalReadBPS, ts, nil),
			newAliasedMetric("node_disk_total_written_bytes_per_second", diskTotalWriteBPS, ts, nil),
			newAliasedMetric("node_disk_total_reads_per_second", diskTotalReadsPS, ts, nil),
			newAliasedMetric("node_disk_total_writes_per_second", diskTotalWritesPS, ts, nil),
			newAliasedMetric("node_disk_total_iops_per_second", diskTotalIOPS, ts, nil),
			newAliasedMetric("node_disk_queue_depth_total", diskTotalQueueDepth, ts, nil),
		)

		latencySeconds := 0.0
		if diskLatencyWeight > 0 {
			latencySeconds = diskLatencyWeighted / diskLatencyWeight
		} else if diskLatencyFallbackCount > 0 {
			latencySeconds = diskLatencyFallbackSum / diskLatencyFallbackCount
		}
		if latencySeconds > 0 {
			metrics = append(metrics,
				newAliasedMetric("node_disk_avg_request_latency_seconds", latencySeconds, ts, nil),
				newAliasedMetric("node_disk_request_latency_p50_seconds", latencySeconds, ts, nil),
				newAliasedMetric("node_disk_request_latency_p90_seconds", latencySeconds, ts, nil),
				newAliasedMetric("node_disk_request_latency_p99_seconds", latencySeconds, ts, nil),
			)
		}
		if diskUtilPeakPercent > 0 {
			metrics = append(metrics, newAliasedMetric("node_disk_utilization_peak_percent", diskUtilPeakPercent, ts, nil))
		}
	}

	if netSeen {
		metrics = append(metrics,
			newAliasedMetric("node_network_total_receive_bytes_per_second", netTotalRxBPS, ts, nil),
			newAliasedMetric("node_network_total_transmit_bytes_per_second", netTotalTxBPS, ts, nil),
			newAliasedMetric("node_network_total_receive_packets_per_second", netTotalRxPPS, ts, nil),
			newAliasedMetric("node_network_total_transmit_packets_per_second", netTotalTxPPS, ts, nil),
		)

		totalCapacity := 0.0
		utilPeak := 0.0
		for _, state := range netByIface {
			if state.speedBitsPS <= 0 {
				continue
			}
			totalCapacity += state.speedBitsPS
			ifaceUtil := clampPercent(((state.rxBps + state.txBps) * 8.0 / state.speedBitsPS) * 100.0)
			utilPeak = math.Max(utilPeak, ifaceUtil)
		}
		if utilPeak > 0 {
			metrics = append(metrics, newAliasedMetric("node_network_utilization_peak_percent", utilPeak, ts, nil))
		}
		if totalCapacity > 0 {
			metrics = append(metrics,
				newAliasedMetric("node_network_capacity_bits_per_second", totalCapacity, ts, nil),
				newAliasedMetric("node_network_capacity_utilization_percent", clampPercent(((netTotalRxBPS+netTotalTxBPS)*8.0/totalCapacity)*100.0), ts, nil),
			)
		}
	}

	if len(procQueue) > 0 {
		pids := make([]string, 0, len(procQueue))
		for pid := range procQueue {
			pids = append(pids, pid)
		}
		sort.Strings(pids)
		for _, pid := range pids {
			state := procQueue[pid]
			labels := map[string]string{"pid": pid}
			if strings.TrimSpace(state.name) != "" {
				labels["name"] = state.name
			}
			if state.connections > 0 {
				metrics = append(metrics, newAliasedMetric("rca_net_process_connections", state.connections, ts, labels))
			}
			metrics = append(metrics, newAliasedMetric("rca_net_process_queued_bytes", state.queuedBytes, ts, labels))
		}
	}

	for _, proc := range batch.GetProcesses() {
		if proc.GetPid() <= 0 {
			continue
		}
		processes = append(processes, &telemetryv1.ProcessSample{
			Pid:        proc.GetPid(),
			Name:       proc.GetName(),
			CpuPercent: proc.GetCpuPercent(),
			RssBytes:   proc.GetRssBytes(),
			IoReadBps:  proc.GetIoReadBps(),
			IoWriteBps: proc.GetIoWriteBps(),
		})
	}

	return metrics, processes
}

func probeCoreLabelsMap(labels []*probeipcv1.Label) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for _, label := range labels {
		key := strings.TrimSpace(label.GetKey())
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(label.GetValue())
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ensureDiskAliasState(states map[string]*diskAliasState, key string) *diskAliasState {
	state := states[key]
	if state != nil {
		return state
	}
	state = &diskAliasState{}
	states[key] = state
	return state
}

func ensureNetworkAliasState(states map[string]*networkAliasState, key string) *networkAliasState {
	state := states[key]
	if state != nil {
		return state
	}
	state = &networkAliasState{}
	states[key] = state
	return state
}

func aliasProbeCorePressureMetric(name string, value float64) (string, float64, bool) {
	if !strings.HasPrefix(name, "probe_core_pressure_") {
		return "", 0, false
	}
	parts := strings.Split(strings.TrimPrefix(name, "probe_core_pressure_"), "_")
	if len(parts) != 3 {
		return "", 0, false
	}
	resource := parts[0]
	scope := parts[1]
	kind := parts[2]

	switch kind {
	case "avg10", "avg60", "avg300":
		return fmt.Sprintf("node_pressure_%s_%s_%s", resource, scope, kind), value, true
	case "total":
		return fmt.Sprintf("node_pressure_%s_%s_seconds_total", resource, scope), value / 1_000_000.0, true
	default:
		return "", 0, false
	}
}

func newAliasedMetric(name string, value float64, ts int64, labels map[string]string) *telemetryv1.Metric {
	if ts <= 0 {
		ts = time.Now().UnixNano()
	}
	return &telemetryv1.Metric{
		Name:              name,
		Value:             value,
		TimestampUnixNano: ts,
		Labels:            buildLabels(labels),
	}
}

func clampPercent(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func metricValueAny(metrics []*telemetryv1.Metric, names ...string) float64 {
	for _, name := range names {
		for _, metric := range metrics {
			if metric.Name == name {
				return metric.Value
			}
		}
	}
	return 0
}
