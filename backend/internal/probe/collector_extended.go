// Package probe implements extended metrics collection (Level 2).
// These metrics provide deeper visibility into system performance.
package probe

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// collectPressure reads Pressure Stall Information from /proc/pressure/*
// Available on Linux 4.20+ kernels
func (c *Collector) collectPressure(now time.Time) ([]Metric, error) {
	var metrics []Metric

	resources := []string{"cpu", "memory", "io"}

	for _, resource := range resources {
		path := fmt.Sprintf("/proc/pressure/%s", resource)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // PSI may not be available on older kernels
		}

		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}

			// Parse: some avg10=0.00 avg60=0.00 avg300=0.00 total=12345
			// or:    full avg10=0.00 avg60=0.00 avg300=0.00 total=12345
			parts := strings.Fields(line)
			if len(parts) < 5 {
				continue
			}

			psiType := parts[0] // "some" or "full"
			for _, part := range parts[1:] {
				kv := strings.Split(part, "=")
				if len(kv) != 2 {
					continue
				}

				key := kv[0]
				val, err := strconv.ParseFloat(kv[1], 64)
				if err != nil {
					continue
				}

				var metricName string
				var metricType string

				switch key {
				case "avg10":
					metricName = fmt.Sprintf("node_pressure_%s_%s_avg10", resource, psiType)
					metricType = "gauge"
				case "avg60":
					metricName = fmt.Sprintf("node_pressure_%s_%s_avg60", resource, psiType)
					metricType = "gauge"
				case "avg300":
					metricName = fmt.Sprintf("node_pressure_%s_%s_avg300", resource, psiType)
					metricType = "gauge"
				case "total":
					metricName = fmt.Sprintf("node_pressure_%s_%s_seconds_total", resource, psiType)
					metricType = "counter"
					val = val / 1000000.0 // Convert microseconds to seconds
				default:
					continue
				}

				metrics = append(metrics, Metric{
					Name:      metricName,
					Type:      metricType,
					Value:     val,
					Timestamp: now,
				})
			}
		}
	}

	return metrics, nil
}

// collectSchedstat reads scheduler statistics from /proc/schedstat
func (c *Collector) collectSchedstat(now time.Time) ([]Metric, error) {
	data, err := os.ReadFile("/proc/schedstat")
	if err != nil {
		return nil, err
	}

	var metrics []Metric

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		// Look for cpu lines: cpu0 0 0 0 0 0 0 0 0 0
		if !strings.HasPrefix(line, "cpu") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 10 {
			continue
		}

		cpu := parts[0]

		// Fields (from kernel docs):
		// 1: sched_yield count
		// 2: unused (legacy)
		// 3: sched_switch count
		// 4: unused (legacy)
		// 5: unused (legacy)
		// 6: cpu run time (ns)
		// 7: cpu wait time (ns)
		// 8: timeslices run

		if runNs, err := strconv.ParseFloat(parts[7], 64); err == nil {
			metrics = append(metrics, Metric{
				Name:      "node_schedstat_running_seconds_total",
				Type:      "counter",
				Value:     runNs / 1e9,
				Labels:    map[string]string{"cpu": cpu},
				Timestamp: now,
			})
		}

		if waitNs, err := strconv.ParseFloat(parts[8], 64); err == nil {
			metrics = append(metrics, Metric{
				Name:      "node_schedstat_waiting_seconds_total",
				Type:      "counter",
				Value:     waitNs / 1e9,
				Labels:    map[string]string{"cpu": cpu},
				Timestamp: now,
			})
		}

		if timeslices, err := strconv.ParseFloat(parts[9], 64); err == nil {
			metrics = append(metrics, Metric{
				Name:      "node_schedstat_timeslices_total",
				Type:      "counter",
				Value:     timeslices,
				Labels:    map[string]string{"cpu": cpu},
				Timestamp: now,
			})
		}
	}

	return metrics, nil
}

// collectTCPConnections reads TCP connection states from /proc/net/tcp
func (c *Collector) collectTCPConnections(now time.Time) ([]Metric, error) {
	states := map[string]float64{
		"established": 0,
		"syn_sent":    0,
		"syn_recv":    0,
		"fin_wait1":   0,
		"fin_wait2":   0,
		"time_wait":   0,
		"close":       0,
		"close_wait":  0,
		"last_ack":    0,
		"listen":      0,
		"closing":     0,
	}

	stateMap := map[string]string{
		"01": "established",
		"02": "syn_sent",
		"03": "syn_recv",
		"04": "fin_wait1",
		"05": "fin_wait2",
		"06": "time_wait",
		"07": "close",
		"08": "close_wait",
		"09": "last_ack",
		"0A": "listen",
		"0B": "closing",
	}

	// Read both IPv4 and IPv6
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if lineNum == 1 {
				continue // Skip header
			}

			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 {
				continue
			}

			stateHex := fields[3]
			if stateName, ok := stateMap[strings.ToUpper(stateHex)]; ok {
				states[stateName]++
			}
		}
		f.Close()
	}

	var metrics []Metric
	for state, count := range states {
		metrics = append(metrics, Metric{
			Name:      "node_tcp_connections",
			Type:      "gauge",
			Value:     count,
			Labels:    map[string]string{"state": state},
			Timestamp: now,
		})
	}

	return metrics, nil
}

// collectNetSNMP reads TCP/UDP statistics from /proc/net/snmp.
// It emits both cumulative counters and selected per-second derivatives.
func (c *Collector) collectNetSNMP(now time.Time, elapsed float64) ([]Metric, error) {
	f, err := os.Open("/proc/net/snmp")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	currentCounters := make(map[string]uint64, 16)
	scanner := bufio.NewScanner(f)

	var headers []string
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		protocol := strings.TrimSuffix(parts[0], ":")

		// First line is headers, second is values
		if len(headers) == 0 || headers[0] != protocol {
			headers = parts
			continue
		}

		// Parse values.
		for i := 1; i < len(parts) && i < len(headers); i++ {
			valFloat, err := strconv.ParseFloat(parts[i], 64)
			if err != nil {
				continue
			}
			valUint, _ := strconv.ParseUint(parts[i], 10, 64)

			name := headers[i]
			var metricName string

			switch protocol {
			case "Tcp":
				switch name {
				case "RetransSegs":
					metricName = "node_tcp_retransmits_total"
				case "InErrs":
					metricName = "node_tcp_in_errs_total"
				case "OutRsts":
					metricName = "node_tcp_out_rsts_total"
				case "InSegs":
					metricName = "node_tcp_in_segs_total"
				case "OutSegs":
					metricName = "node_tcp_out_segs_total"
				case "ActiveOpens":
					metricName = "node_tcp_active_opens_total"
				case "PassiveOpens":
					metricName = "node_tcp_passive_opens_total"
				case "CurrEstab":
					metricName = "node_tcp_curr_estab"
					// This is a gauge, not counter
					metrics = append(metrics, Metric{
						Name:      metricName,
						Type:      "gauge",
						Value:     valFloat,
						Timestamp: now,
					})
					continue
				}
			case "Udp":
				switch name {
				case "InDatagrams":
					metricName = "node_udp_in_datagrams_total"
				case "OutDatagrams":
					metricName = "node_udp_out_datagrams_total"
				case "InErrors":
					metricName = "node_udp_in_errors_total"
				case "RcvbufErrors":
					metricName = "node_udp_rcvbuf_errors_total"
				case "SndbufErrors":
					metricName = "node_udp_sndbuf_errors_total"
				}
			}

			if metricName != "" {
				metrics = append(metrics, Metric{
					Name:      metricName,
					Type:      "counter",
					Value:     valFloat,
					Timestamp: now,
				})
				currentCounters[protocol+"."+name] = valUint
			}
		}

		headers = nil
	}

	if elapsed > 0 && len(c.lastNetSNMP) > 0 {
		emitCounterRate := func(counterKey, metricName string) float64 {
			curr, ok := currentCounters[counterKey]
			if !ok {
				return 0
			}
			prev := c.lastNetSNMP[counterKey]
			rate := float64(counterDeltaUint(curr, prev)) / elapsed
			metrics = append(metrics, Metric{
				Name:      metricName,
				Type:      "gauge",
				Value:     rate,
				Timestamp: now,
			})
			return rate
		}

		retransRate := emitCounterRate("Tcp.RetransSegs", "node_tcp_retransmits_per_second")
		outSegRate := emitCounterRate("Tcp.OutSegs", "node_tcp_out_segs_per_second")
		emitCounterRate("Tcp.InSegs", "node_tcp_in_segs_per_second")
		emitCounterRate("Tcp.InErrs", "node_tcp_in_errs_per_second")
		emitCounterRate("Tcp.OutRsts", "node_tcp_out_rsts_per_second")
		emitCounterRate("Udp.InErrors", "node_udp_in_errors_per_second")
		emitCounterRate("Udp.RcvbufErrors", "node_udp_rcvbuf_errors_per_second")
		emitCounterRate("Udp.SndbufErrors", "node_udp_sndbuf_errors_per_second")

		if outSegRate > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_tcp_retransmit_ratio",
				Type:      "gauge",
				Value:     retransRate / outSegRate,
				Timestamp: now,
			})
		}
	}

	c.lastNetSNMP = currentCounters

	return metrics, nil
}

// collectThermal reads thermal zone temperatures from /sys/class/thermal
func (c *Collector) collectThermal(now time.Time) ([]Metric, error) {
	thermalPath := "/sys/class/thermal"
	entries, err := os.ReadDir(thermalPath)
	if err != nil {
		return nil, err
	}

	var metrics []Metric

	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "thermal_zone") {
			continue
		}

		zonePath := filepath.Join(thermalPath, entry.Name())

		// Read temperature
		tempData, err := os.ReadFile(filepath.Join(zonePath, "temp"))
		if err != nil {
			continue
		}

		temp, err := strconv.ParseFloat(strings.TrimSpace(string(tempData)), 64)
		if err != nil {
			continue
		}

		// Temperature is in millidegrees Celsius
		tempCelsius := temp / 1000.0

		// Read zone type
		typeData, _ := os.ReadFile(filepath.Join(zonePath, "type"))
		zoneType := strings.TrimSpace(string(typeData))
		if zoneType == "" {
			zoneType = "unknown"
		}

		zone := strings.TrimPrefix(entry.Name(), "thermal_zone")

		metrics = append(metrics, Metric{
			Name:      "node_thermal_zone_temp_celsius",
			Type:      "gauge",
			Value:     tempCelsius,
			Labels:    map[string]string{"zone": zone, "type": zoneType},
			Timestamp: now,
		})
	}

	return metrics, nil
}

// collectSoftnet reads softnet statistics from /proc/net/softnet_stat.
// It emits per-CPU counters and node-level rate/saturation signals.
func (c *Collector) collectSoftnet(now time.Time, elapsed float64) ([]Metric, error) {
	f, err := os.Open("/proc/net/softnet_stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	current := make(map[int]softnetStats)
	scanner := bufio.NewScanner(f)
	cpu := 0
	var totalProcessed uint64
	var totalDropped uint64
	var totalSqueezed uint64
	totalProcessedRate := 0.0
	totalDroppedRate := 0.0
	totalSqueezedRate := 0.0

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		cpuLabel := fmt.Sprintf("%d", cpu)
		stats := softnetStats{}

		// Field 0: packets processed
		if processed, err := strconv.ParseUint(fields[0], 16, 64); err == nil {
			stats.Processed = processed
			metrics = append(metrics, Metric{
				Name:      "node_softnet_processed_total",
				Type:      "counter",
				Value:     float64(processed),
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})
			totalProcessed += processed
		}

		// Field 1: packets dropped
		if dropped, err := strconv.ParseUint(fields[1], 16, 64); err == nil {
			stats.Dropped = dropped
			metrics = append(metrics, Metric{
				Name:      "node_softnet_dropped_total",
				Type:      "counter",
				Value:     float64(dropped),
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})
			totalDropped += dropped
		}

		// Field 2: time_squeeze (ran out of netdev budget)
		if squeeze, err := strconv.ParseUint(fields[2], 16, 64); err == nil {
			stats.Squeezed = squeeze
			metrics = append(metrics, Metric{
				Name:      "node_softnet_times_squeezed_total",
				Type:      "counter",
				Value:     float64(squeeze),
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})
			totalSqueezed += squeeze
		}

		current[cpu] = stats

		if prev, ok := c.lastSoftnet[cpu]; ok && elapsed > 0 {
			processedRate := float64(counterDeltaUint(stats.Processed, prev.Processed)) / elapsed
			droppedRate := float64(counterDeltaUint(stats.Dropped, prev.Dropped)) / elapsed
			squeezedRate := float64(counterDeltaUint(stats.Squeezed, prev.Squeezed)) / elapsed

			metrics = append(metrics, Metric{
				Name:      "node_softnet_processed_per_second",
				Type:      "gauge",
				Value:     processedRate,
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_softnet_dropped_per_second",
				Type:      "gauge",
				Value:     droppedRate,
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_softnet_times_squeezed_per_second",
				Type:      "gauge",
				Value:     squeezedRate,
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})

			totalProcessedRate += processedRate
			totalDroppedRate += droppedRate
			totalSqueezedRate += squeezedRate
		}

		cpu++
	}

	if elapsed > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_softnet_processed_per_second",
			Type:      "gauge",
			Value:     totalProcessedRate,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_softnet_dropped_per_second",
			Type:      "gauge",
			Value:     totalDroppedRate,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_softnet_times_squeezed_per_second",
			Type:      "gauge",
			Value:     totalSqueezedRate,
			Timestamp: now,
		})
		if totalProcessedRate > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_softnet_drop_ratio",
				Type:      "gauge",
				Value:     totalDroppedRate / totalProcessedRate,
				Timestamp: now,
			})
		}
	}

	metrics = append(metrics, Metric{
		Name:      "node_softnet_processed_total_all",
		Type:      "gauge",
		Value:     float64(totalProcessed),
		Timestamp: now,
	})
	metrics = append(metrics, Metric{
		Name:      "node_softnet_dropped_total_all",
		Type:      "gauge",
		Value:     float64(totalDropped),
		Timestamp: now,
	})
	metrics = append(metrics, Metric{
		Name:      "node_softnet_times_squeezed_total_all",
		Type:      "gauge",
		Value:     float64(totalSqueezed),
		Timestamp: now,
	})

	c.lastSoftnet = current

	return metrics, nil
}

// collectSockstat reads socket statistics from /proc/net/sockstat
func (c *Collector) collectSockstat(now time.Time) ([]Metric, error) {
	f, err := os.Open("/proc/net/sockstat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 3 {
			continue
		}

		protocol := strings.TrimSuffix(parts[0], ":")

		// Parse key-value pairs
		for i := 1; i < len(parts)-1; i += 2 {
			key := parts[i]
			val, err := strconv.ParseFloat(parts[i+1], 64)
			if err != nil {
				continue
			}

			var metricName string
			switch {
			case protocol == "sockets" && key == "used":
				metricName = "node_sockets_used"
			case protocol == "TCP" && key == "inuse":
				metricName = "node_tcp_sockets_inuse"
			case protocol == "TCP" && key == "orphan":
				metricName = "node_tcp_sockets_orphan"
			case protocol == "TCP" && key == "tw":
				metricName = "node_tcp_sockets_timewait"
			case protocol == "TCP" && key == "alloc":
				metricName = "node_tcp_sockets_alloc"
			case protocol == "TCP" && key == "mem":
				metricName = "node_tcp_sockets_mem_pages"
			case protocol == "UDP" && key == "inuse":
				metricName = "node_udp_sockets_inuse"
			case protocol == "UDP" && key == "mem":
				metricName = "node_udp_sockets_mem_pages"
			}

			if metricName != "" {
				metrics = append(metrics, Metric{
					Name:      metricName,
					Type:      "gauge",
					Value:     val,
					Timestamp: now,
				})
			}
		}
	}

	return metrics, nil
}

func (c *Collector) collectNetworkInterfaceSysfs(now time.Time) ([]Metric, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil, err
	}

	var metrics []Metric
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		iface := strings.TrimSpace(entry.Name())
		if iface == "" || iface == "lo" {
			continue
		}

		labels := map[string]string{"device": iface}
		base := filepath.Join("/sys/class/net", iface)

		if carrier, ok := readUintFile(filepath.Join(base, "carrier")); ok {
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_carrier_up",
				Type:      "gauge",
				Value:     float64(carrier),
				Labels:    labels,
				Timestamp: now,
			})
		}
		if mtu, ok := readUintFile(filepath.Join(base, "mtu")); ok {
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_mtu_bytes",
				Type:      "gauge",
				Value:     float64(mtu),
				Labels:    labels,
				Timestamp: now,
			})
		}
		if txQLen, ok := readUintFile(filepath.Join(base, "tx_queue_len")); ok {
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_tx_queue_len",
				Type:      "gauge",
				Value:     float64(txQLen),
				Labels:    labels,
				Timestamp: now,
			})
		}

		rxQueues, txQueues := countNetworkQueues(filepath.Join(base, "queues"))
		if rxQueues > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_rx_queues",
				Type:      "gauge",
				Value:     float64(rxQueues),
				Labels:    labels,
				Timestamp: now,
			})
		}
		if txQueues > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_tx_queues",
				Type:      "gauge",
				Value:     float64(txQueues),
				Labels:    labels,
				Timestamp: now,
			})
		}

		txLimitBytes, txInflightBytes := readTXBQL(filepath.Join(base, "queues"))
		if txLimitBytes > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_tx_queue_limit_bytes",
				Type:      "gauge",
				Value:     float64(txLimitBytes),
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_tx_queue_inflight_bytes",
				Type:      "gauge",
				Value:     float64(txInflightBytes),
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_tx_queue_fill_percent",
				Type:      "gauge",
				Value:     clampPercent((float64(txInflightBytes) / float64(txLimitBytes)) * 100.0),
				Labels:    labels,
				Timestamp: now,
			})
		}

		statMetrics := map[string]string{
			"rx_fifo_errors":   "node_network_interface_rx_fifo_errors_total",
			"tx_fifo_errors":   "node_network_interface_tx_fifo_errors_total",
			"rx_missed_errors": "node_network_interface_rx_missed_errors_total",
			"rx_over_errors":   "node_network_interface_rx_over_errors_total",
			"rx_crc_errors":    "node_network_interface_rx_crc_errors_total",
			"collisions":       "node_network_interface_collisions_total",
			"rx_nohandler":     "node_network_interface_rx_nohandler_total",
		}
		for source, metricName := range statMetrics {
			if value, ok := readUintFile(filepath.Join(base, "statistics", source)); ok {
				metrics = append(metrics, Metric{
					Name:      metricName,
					Type:      "counter",
					Value:     float64(value),
					Labels:    labels,
					Timestamp: now,
				})
			}
		}
	}

	return metrics, nil
}

func (c *Collector) collectNetworkInterrupts(now time.Time, elapsed float64) ([]Metric, error) {
	interfaces, err := listNetworkInterfaces()
	if err != nil {
		return nil, err
	}
	if len(interfaces) == 0 {
		return nil, nil
	}

	data, err := os.ReadFile("/proc/interrupts")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	current := make(map[string]uint64, len(interfaces))

	for _, line := range lines {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 2 {
			continue
		}

		sum := uint64(0)
		idx := 0
		for idx < len(fields) {
			v, err := strconv.ParseUint(fields[idx], 10, 64)
			if err != nil {
				break
			}
			sum += v
			idx++
		}
		if sum == 0 || idx >= len(fields) {
			continue
		}
		desc := strings.ToLower(strings.Join(fields[idx:], " "))
		for iface := range interfaces {
			name := strings.ToLower(iface)
			if strings.Contains(desc, name) {
				current[iface] += sum
			}
		}
	}

	var metrics []Metric
	totalRate := 0.0
	for iface := range interfaces {
		currentCount := current[iface]
		labels := map[string]string{"device": iface}
		metrics = append(metrics, Metric{
			Name:      "node_network_interface_interrupts_total",
			Type:      "counter",
			Value:     float64(currentCount),
			Labels:    labels,
			Timestamp: now,
		})
		if prev, ok := c.lastNICIRQs[iface]; ok && elapsed > 0 {
			rate := float64(counterDeltaUint(currentCount, prev)) / elapsed
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_interrupts_per_second",
				Type:      "gauge",
				Value:     rate,
				Labels:    labels,
				Timestamp: now,
			})
			totalRate += rate
		}
	}

	if elapsed > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_network_interrupts_per_second",
			Type:      "gauge",
			Value:     totalRate,
			Timestamp: now,
		})
	}

	c.lastNICIRQs = current
	return metrics, nil
}

func (c *Collector) collectRDMAMetrics(now time.Time, elapsed float64) ([]Metric, error) {
	root := "/sys/class/infiniband"
	devices, err := os.ReadDir(root)
	if err != nil {
		// RDMA is optional; absence is expected on non-RDMA hosts.
		return nil, nil
	}

	var metrics []Metric
	current := make(map[string]rdmaPortStats)
	portCount := 0
	totalErrorRate := 0.0
	totalCongestionRate := 0.0

	for _, dev := range devices {
		if !dev.IsDir() {
			continue
		}
		devName := dev.Name()
		ports, err := os.ReadDir(filepath.Join(root, devName, "ports"))
		if err != nil {
			continue
		}

		for _, portEntry := range ports {
			if !portEntry.IsDir() {
				continue
			}
			port := portEntry.Name()
			portCount++
			labels := map[string]string{"device": devName, "port": port}
			portBase := filepath.Join(root, devName, "ports", port)
			counterBase := filepath.Join(portBase, "counters")

			if state, ok := readRDMAState(filepath.Join(portBase, "state")); ok {
				metrics = append(metrics, Metric{
					Name:      "node_rdma_port_state",
					Type:      "gauge",
					Value:     float64(state),
					Labels:    labels,
					Timestamp: now,
				})
			}
			if state, ok := readRDMAState(filepath.Join(portBase, "phys_state")); ok {
				metrics = append(metrics, Metric{
					Name:      "node_rdma_port_phys_state",
					Type:      "gauge",
					Value:     float64(state),
					Labels:    labels,
					Timestamp: now,
				})
			}

			linkRateGbps := 0.0
			if rate, ok := readRDMALinkRateGbps(filepath.Join(portBase, "rate")); ok {
				linkRateGbps = rate
				metrics = append(metrics, Metric{
					Name:      "node_rdma_port_link_rate_gbps",
					Type:      "gauge",
					Value:     rate,
					Labels:    labels,
					Timestamp: now,
				})
			}

			var xmitWords uint64
			var rcvWords uint64
			var errorEvents uint64

			counterMetrics := map[string]string{
				"port_xmit_data":                  "node_rdma_port_transmit_words_total",
				"port_rcv_data":                   "node_rdma_port_receive_words_total",
				"port_xmit_packets":               "node_rdma_port_transmit_packets_total",
				"port_rcv_packets":                "node_rdma_port_receive_packets_total",
				"port_xmit_discards":              "node_rdma_port_transmit_discards_total",
				"port_rcv_errors":                 "node_rdma_port_receive_errors_total",
				"symbol_error":                    "node_rdma_port_symbol_errors_total",
				"link_downed":                     "node_rdma_port_link_downed_total",
				"link_error_recovery":             "node_rdma_port_link_recovery_total",
				"port_rcv_remote_physical_errors": "node_rdma_port_remote_physical_errors_total",
				"port_rcv_constraint_errors":      "node_rdma_port_receive_constraint_errors_total",
				"port_xmit_constraint_errors":     "node_rdma_port_transmit_constraint_errors_total",
			}
			for source, metricName := range counterMetrics {
				v, ok := readUintFile(filepath.Join(counterBase, source))
				if !ok {
					continue
				}
				metrics = append(metrics, Metric{
					Name:      metricName,
					Type:      "counter",
					Value:     float64(v),
					Labels:    labels,
					Timestamp: now,
				})
				switch source {
				case "port_xmit_data":
					xmitWords = v
				case "port_rcv_data":
					rcvWords = v
				case "port_xmit_discards", "port_rcv_errors", "symbol_error", "link_downed", "link_error_recovery", "port_rcv_remote_physical_errors", "port_rcv_constraint_errors", "port_xmit_constraint_errors":
					errorEvents += v
				}
			}

			congestionEvents := uint64(0)
			hwCounters := filepath.Join(portBase, "hw_counters")
			if entries, err := os.ReadDir(hwCounters); err == nil {
				for _, hw := range entries {
					if hw.IsDir() {
						continue
					}
					name := strings.ToLower(hw.Name())
					if !isRDMACongestionCounter(name) {
						continue
					}
					v, ok := readUintFile(filepath.Join(hwCounters, hw.Name()))
					if !ok {
						continue
					}
					congestionEvents += v
					metrics = append(metrics, Metric{
						Name:  "node_rdma_port_congestion_counter_total",
						Type:  "counter",
						Value: float64(v),
						Labels: map[string]string{
							"device":  devName,
							"port":    port,
							"counter": hw.Name(),
						},
						Timestamp: now,
					})
				}
			}

			key := devName + ":" + port
			current[key] = rdmaPortStats{
				XmitWords:        xmitWords,
				RcvWords:         rcvWords,
				ErrorEvents:      errorEvents,
				CongestionEvents: congestionEvents,
			}

			if prev, ok := c.lastRDMAPorts[key]; ok && elapsed > 0 {
				txBPS := (float64(counterDeltaUint(xmitWords, prev.XmitWords)) * 4.0) / elapsed
				rxBPS := (float64(counterDeltaUint(rcvWords, prev.RcvWords)) * 4.0) / elapsed
				errorRate := float64(counterDeltaUint(errorEvents, prev.ErrorEvents)) / elapsed
				congRate := float64(counterDeltaUint(congestionEvents, prev.CongestionEvents)) / elapsed

				metrics = append(metrics, Metric{
					Name:      "node_rdma_port_transmit_bytes_per_second",
					Type:      "gauge",
					Value:     txBPS,
					Labels:    labels,
					Timestamp: now,
				})
				metrics = append(metrics, Metric{
					Name:      "node_rdma_port_receive_bytes_per_second",
					Type:      "gauge",
					Value:     rxBPS,
					Labels:    labels,
					Timestamp: now,
				})
				metrics = append(metrics, Metric{
					Name:      "node_rdma_port_errors_per_second",
					Type:      "gauge",
					Value:     errorRate,
					Labels:    labels,
					Timestamp: now,
				})
				metrics = append(metrics, Metric{
					Name:      "node_rdma_port_congestion_events_per_second",
					Type:      "gauge",
					Value:     congRate,
					Labels:    labels,
					Timestamp: now,
				})

				if linkRateGbps > 0 {
					linkBPS := linkRateGbps * 1_000_000_000.0
					utilization := clampPercent(((txBPS + rxBPS) * 8.0 / linkBPS) * 100.0)
					metrics = append(metrics, Metric{
						Name:      "node_rdma_port_utilization_percent",
						Type:      "gauge",
						Value:     utilization,
						Labels:    labels,
						Timestamp: now,
					})
				}

				totalErrorRate += errorRate
				totalCongestionRate += congRate
			}
		}
	}

	if portCount > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_rdma_ports",
			Type:      "gauge",
			Value:     float64(portCount),
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_rdma_errors_per_second",
			Type:      "gauge",
			Value:     totalErrorRate,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_rdma_congestion_events_per_second",
			Type:      "gauge",
			Value:     totalCongestionRate,
			Timestamp: now,
		})
	}

	c.lastRDMAPorts = current
	return metrics, nil
}

func readUintFile(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func countNetworkQueues(queuesPath string) (rxCount int, txCount int) {
	entries, err := os.ReadDir(queuesPath)
	if err != nil {
		return 0, 0
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "rx-") {
			rxCount++
		}
		if strings.HasPrefix(name, "tx-") {
			txCount++
		}
	}
	return rxCount, txCount
}

func readTXBQL(queuesPath string) (limit uint64, inflight uint64) {
	entries, err := os.ReadDir(queuesPath)
	if err != nil {
		return 0, 0
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "tx-") {
			continue
		}
		base := filepath.Join(queuesPath, name, "byte_queue_limits")
		if v, ok := readUintFile(filepath.Join(base, "limit_max")); ok {
			limit += v
		}
		if v, ok := readUintFile(filepath.Join(base, "inflight")); ok {
			inflight += v
		}
	}
	return limit, inflight
}

func listNetworkInterfaces() (map[string]struct{}, error) {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" || name == "lo" {
			continue
		}
		out[name] = struct{}{}
	}
	return out, nil
}

func readRDMAState(path string) (uint64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 0, false
	}
	parts := strings.SplitN(text, ":", 2)
	val, err := strconv.ParseUint(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

func readRDMALinkRateGbps(path string) (float64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) == 0 {
		return 0, false
	}
	rate, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || rate <= 0 {
		return 0, false
	}
	return rate, true
}

func isRDMACongestionCounter(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	keywords := []string{"cnp", "ecn", "cong", "pfc", "buffer"}
	for _, kw := range keywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	return false
}

// collectExtended collects all Level 2 extended metrics
func (c *Collector) collectExtended(now time.Time, elapsed float64) []Metric {
	var metrics []Metric

	if m, err := c.collectPressure(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectSchedstat(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectTCPConnections(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectNetSNMP(now, elapsed); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectThermal(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectSoftnet(now, elapsed); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectSockstat(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectNetworkInterfaceSysfs(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectNetworkInterrupts(now, elapsed); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectRDMAMetrics(now, elapsed); err == nil {
		metrics = append(metrics, m...)
	}

	return metrics
}
