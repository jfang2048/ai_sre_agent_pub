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

// collectNetSNMP reads TCP/UDP statistics from /proc/net/snmp
func (c *Collector) collectNetSNMP(now time.Time) ([]Metric, error) {
	f, err := os.Open("/proc/net/snmp")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
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

		// Parse values
		for i := 1; i < len(parts) && i < len(headers); i++ {
			val, err := strconv.ParseFloat(parts[i], 64)
			if err != nil {
				continue
			}

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
						Value:     val,
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
					Value:     val,
					Timestamp: now,
				})
			}
		}

		headers = nil
	}

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

// collectSoftnet reads softnet statistics from /proc/net/softnet_stat
func (c *Collector) collectSoftnet(now time.Time) ([]Metric, error) {
	f, err := os.Open("/proc/net/softnet_stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	scanner := bufio.NewScanner(f)
	cpu := 0

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		cpuLabel := fmt.Sprintf("%d", cpu)

		// Field 0: packets processed
		if processed, err := strconv.ParseUint(fields[0], 16, 64); err == nil {
			metrics = append(metrics, Metric{
				Name:      "node_softnet_processed_total",
				Type:      "counter",
				Value:     float64(processed),
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})
		}

		// Field 1: packets dropped
		if dropped, err := strconv.ParseUint(fields[1], 16, 64); err == nil {
			metrics = append(metrics, Metric{
				Name:      "node_softnet_dropped_total",
				Type:      "counter",
				Value:     float64(dropped),
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})
		}

		// Field 2: time_squeeze (ran out of netdev budget)
		if squeeze, err := strconv.ParseUint(fields[2], 16, 64); err == nil {
			metrics = append(metrics, Metric{
				Name:      "node_softnet_times_squeezed_total",
				Type:      "counter",
				Value:     float64(squeeze),
				Labels:    map[string]string{"cpu": cpuLabel},
				Timestamp: now,
			})
		}

		cpu++
	}

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

// collectExtended collects all Level 2 extended metrics
func (c *Collector) collectExtended(now time.Time) []Metric {
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

	if m, err := c.collectNetSNMP(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectThermal(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectSoftnet(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectSockstat(now); err == nil {
		metrics = append(metrics, m...)
	}

	return metrics
}
