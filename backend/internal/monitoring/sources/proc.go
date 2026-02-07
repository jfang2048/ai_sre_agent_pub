package sources

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// IOStats holds I/O statistics for rate calculation
type IOStats struct {
	ReadBytes  uint64
	WriteBytes uint64
	ReadOps    uint64
	WriteOps   uint64
}

type ProcSource struct {
	BaseSource
	config ProcConfig

	// CPU usage tracking
	cpuLastTotal uint64
	cpuLastIdle  uint64

	// Network I/O tracking for rate calculation
	lastNetStats map[string]IOStats

	// Disk I/O tracking for rate calculation
	lastDiskStats map[string]IOStats

	// Last collection time for rate calculation
	lastCollectTime time.Time

	mu sync.Mutex
}

func NewProcSource(config ProcConfig) *ProcSource {
	return &ProcSource{
		BaseSource: BaseSource{
			name:    "proc",
			enabled: config.Enabled,
		},
		config:          config,
		lastNetStats:    make(map[string]IOStats),
		lastDiskStats:   make(map[string]IOStats),
		lastCollectTime: time.Now(),
	}
}

func (p *ProcSource) Start(ctx context.Context) error {
	if !p.enabled {
		p.setStatus(false, false, "disabled")
		return nil
	}
	p.setStatus(true, true, "")
	return nil
}

func (p *ProcSource) Stop() error {
	p.setStatus(false, false, "")
	return nil
}

func (p *ProcSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	if !p.enabled {
		return &proto.MetricBatch{
			Metrics:     []*proto.Metric{},
			Source:      "proc",
			CollectedAt: timestamppb.Now(),
		}, nil
	}
	metrics := []*proto.Metric{}

	// Collect local time as timestamp
	now := timestamppb.Now()

	// Read load average
	if f, err := os.Open("/proc/loadavg"); err == nil {
		scanner := bufio.NewScanner(f)
		if scanner.Scan() {
			parts := strings.Fields(scanner.Text())
			if len(parts) >= 3 {
				if v, err := strconv.ParseFloat(parts[0], 64); err == nil {
					metrics = append(metrics, createGauge("system.load.1m", v, now))
				}
				if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
					metrics = append(metrics, createGauge("system.load.5m", v, now))
				}
				if v, err := strconv.ParseFloat(parts[2], 64); err == nil {
					metrics = append(metrics, createGauge("system.load.15m", v, now))
				}
			}
		}
		f.Close()
	}

	// Read CPU usage from /proc/stat
	if cpuMetrics, err := p.collectCPUStats(now); err == nil {
		metrics = append(metrics, cpuMetrics...)
	}

	// Read advanced CPU stats (Context Switches, Procs)
	if advMetrics, err := p.collectAdvancedStat(now); err == nil {
		metrics = append(metrics, advMetrics...)
	}

	// Read Open File Descriptors
	if fdMetrics, err := p.collectOpenFiles(now); err == nil {
		metrics = append(metrics, fdMetrics...)
	}

	// Read memory info
	if f, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(f)
		var memTotal, memAvailable, memFree, swapTotal, swapFree float64
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			val, err := strconv.ParseFloat(parts[1], 64) // KB
			if err != nil {
				continue
			}
			valBytes := val * 1024

			switch {
			case strings.HasPrefix(line, "MemTotal:"):
				memTotal = valBytes
				metrics = append(metrics, createGauge("system.memory.total", valBytes, now))
			case strings.HasPrefix(line, "MemAvailable:"):
				memAvailable = valBytes
				metrics = append(metrics, createGauge("system.memory.available", valBytes, now))
			case strings.HasPrefix(line, "MemFree:"):
				memFree = valBytes
				metrics = append(metrics, createGauge("system.memory.free", valBytes, now))
			case strings.HasPrefix(line, "Buffers:"):
				metrics = append(metrics, createGauge("system.memory.buffers", valBytes, now))
			case strings.HasPrefix(line, "Cached:"):
				metrics = append(metrics, createGauge("system.memory.cached", valBytes, now))
			case strings.HasPrefix(line, "SwapTotal:"):
				swapTotal = valBytes
				metrics = append(metrics, createGauge("system.swap.total", valBytes, now))
			case strings.HasPrefix(line, "SwapFree:"):
				swapFree = valBytes
				metrics = append(metrics, createGauge("system.swap.free", valBytes, now))
			case strings.HasPrefix(line, "Slab:"):
				metrics = append(metrics, createGauge("system.memory.slab", valBytes, now))
			case strings.HasPrefix(line, "Dirty:"):
				metrics = append(metrics, createGauge("system.memory.dirty", valBytes, now))
			case strings.HasPrefix(line, "Writeback:"):
				metrics = append(metrics, createGauge("system.memory.writeback", valBytes, now))
			case strings.HasPrefix(line, "AnonPages:"):
				metrics = append(metrics, createGauge("system.memory.anon", valBytes, now))
			case strings.HasPrefix(line, "Mapped:"):
				metrics = append(metrics, createGauge("system.memory.mapped", valBytes, now))
			case strings.HasPrefix(line, "PageTables:"):
				metrics = append(metrics, createGauge("system.memory.pagetables", valBytes, now))
			}
		}
		f.Close()

		// Calculate used memory (total - available)
		if memTotal > 0 && memAvailable > 0 {
			memUsed := memTotal - memAvailable
			metrics = append(metrics, createGauge("system.memory.used", memUsed, now))
		} else if memTotal > 0 && memFree > 0 {
			// Fallback if available not present
			memUsed := memTotal - memFree
			metrics = append(metrics, createGauge("system.memory.used", memUsed, now))
		}

		// Calculate swap used
		if swapTotal > 0 {
			swapUsed := swapTotal - swapFree
			metrics = append(metrics, createGauge("system.swap.used", swapUsed, now))
		}
	}

	// Read Network IO
	if netMetrics, err := p.collectNetworkIO(now); err == nil {
		metrics = append(metrics, netMetrics...)
	}

	// Read Disk Stats
	if diskMetrics, err := p.collectDiskStats(now); err == nil {
		metrics = append(metrics, diskMetrics...)
	}

	// Read VM Stats (paging, faults)
	if vmMetrics, err := p.collectVMStat(now); err == nil {
		metrics = append(metrics, vmMetrics...)
	}

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "proc",
		CollectedAt: now,
	}, nil
}

// collectCPUStats collects detailed CPU metrics including iowait, user, system, etc.
func (p *ProcSource) collectCPUStats(now *timestamppb.Timestamp) ([]*proto.Metric, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty /proc/stat")
	}

	fields := strings.Fields(scanner.Text())
	if len(fields) < 8 || fields[0] != "cpu" {
		return nil, fmt.Errorf("invalid /proc/stat format")
	}

	// CPU fields: user, nice, system, idle, iowait, irq, softirq, steal, guest, guest_nice
	user, _ := strconv.ParseUint(fields[1], 10, 64)
	nice, _ := strconv.ParseUint(fields[2], 10, 64)
	system, _ := strconv.ParseUint(fields[3], 10, 64)
	idle, _ := strconv.ParseUint(fields[4], 10, 64)
	iowait, _ := strconv.ParseUint(fields[5], 10, 64)
	irq, _ := strconv.ParseUint(fields[6], 10, 64)
	softirq, _ := strconv.ParseUint(fields[7], 10, 64)
	steal := uint64(0)
	if len(fields) > 8 {
		steal, _ = strconv.ParseUint(fields[8], 10, 64)
	}

	total := user + nice + system + idle + iowait + irq + softirq + steal

	metrics := []*proto.Metric{}

	if p.cpuLastTotal == 0 {
		p.cpuLastTotal = total
		p.cpuLastIdle = idle
		// For one-shot or first run, take a quick second sample to get a delta
		time.Sleep(100 * time.Millisecond)
		return p.collectCPUStats(now)
	}

	totalDelta := total - p.cpuLastTotal
	idleDelta := idle - p.cpuLastIdle

	// Store current values for next calculation
	p.cpuLastTotal = total
	p.cpuLastIdle = idle

	if totalDelta == 0 {
		totalDelta = 1 // Avoid division by zero
	}

	// Calculate percentages based on delta
	// Usage = (total - idle) / total * 100
	usagePct := float64(totalDelta-idleDelta) / float64(totalDelta) * 100.0

	// For individual components, we need to estimate based on current sample
	// These are less accurate but still useful
	totalF := float64(total)
	if totalF == 0 {
		totalF = 1
	}
	userPct := float64(user+nice) / totalF * 100.0
	systemPct := float64(system) / totalF * 100.0
	idlePct := float64(idle) / totalF * 100.0
	iowaitPct := float64(iowait) / totalF * 100.0
	irqPct := float64(irq) / totalF * 100.0
	softirqPct := float64(softirq) / totalF * 100.0
	stealPct := float64(steal) / totalF * 100.0

	// Clamp values to valid range
	if usagePct < 0 {
		usagePct = 0
	} else if usagePct > 100 {
		usagePct = 100
	}

	metrics = append(metrics,
		createGauge("system.cpu.usage", usagePct, now),
		createGauge("system.cpu.user", userPct, now),
		createGauge("system.cpu.system", systemPct, now),
		createGauge("system.cpu.idle", idlePct, now),
		createGauge("system.cpu.iowait", iowaitPct, now),
		createGauge("system.cpu.irq", irqPct, now),
		createGauge("system.cpu.softirq", softirqPct, now),
		createGauge("system.cpu.steal", stealPct, now),
	)

	return metrics, nil
}

func (p *ProcSource) collectNetworkIO(now *timestamppb.Timestamp) ([]*proto.Metric, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p.mu.Lock()
	defer p.mu.Unlock()

	currentTime := time.Now()
	elapsed := currentTime.Sub(p.lastCollectTime).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	metrics := []*proto.Metric{}
	currentStats := make(map[string]IOStats)

	scanner := bufio.NewScanner(f)
	lineIdx := 0
	for scanner.Scan() {
		line := scanner.Text()
		if lineIdx < 2 || strings.TrimSpace(line) == "" {
			lineIdx++
			continue
		}
		lineIdx++

		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 12 {
			continue
		}

		// Fields: rx_bytes rx_packets rx_errs rx_drop rx_fifo rx_frame rx_compressed rx_multicast
		//         tx_bytes tx_packets tx_errs tx_drop tx_fifo tx_colls tx_carrier tx_compressed
		rxBytes, _ := strconv.ParseUint(fields[0], 10, 64)
		rxPackets, _ := strconv.ParseUint(fields[1], 10, 64)
		rxErrors, _ := strconv.ParseFloat(fields[2], 64)
		rxDrops, _ := strconv.ParseFloat(fields[3], 64)
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)
		txPackets, _ := strconv.ParseUint(fields[9], 10, 64)
		txErrors, _ := strconv.ParseFloat(fields[10], 64)
		txDrops, _ := strconv.ParseFloat(fields[11], 64)

		currentStats[iface] = IOStats{
			ReadBytes:  rxBytes,
			WriteBytes: txBytes,
			ReadOps:    rxPackets,
			WriteOps:   txPackets,
		}

		labels := []*proto.MetricLabel{{Key: "interface", Value: iface}}

		// Calculate rates if we have previous stats
		var rxBytesRate, txBytesRate, rxPacketsRate, txPacketsRate float64
		if prev, ok := p.lastNetStats[iface]; ok {
			rxBytesRate = float64(rxBytes-prev.ReadBytes) / elapsed
			txBytesRate = float64(txBytes-prev.WriteBytes) / elapsed
			rxPacketsRate = float64(rxPackets-prev.ReadOps) / elapsed
			txPacketsRate = float64(txPackets-prev.WriteOps) / elapsed
			// Clamp negative rates (can happen on counter reset)
			if rxBytesRate < 0 {
				rxBytesRate = 0
			}
			if txBytesRate < 0 {
				txBytesRate = 0
			}
		}

		// Cumulative totals (counters)
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.rx_bytes_total", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(rxBytes)}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.tx_bytes_total", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(txBytes)}},
		})

		// Rate metrics (gauges - bytes per second)
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.rx_bytes", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: rxBytesRate}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.network.bytes_recv", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: rxBytesRate}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.tx_bytes", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: txBytesRate}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.network.bytes_sent", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: txBytesRate}},
		})

		// Packet rates
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.rx_packets", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: rxPacketsRate}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.tx_packets", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: txPacketsRate}},
		})

		// Error and drop totals (these are still cumulative)
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.rx_errors", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: rxErrors}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.tx_errors", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: txErrors}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.rx_drops", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: rxDrops}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.net.tx_drops", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: txDrops}},
		})
	}

	// Update stored stats
	p.lastNetStats = currentStats
	p.lastCollectTime = currentTime

	return metrics, nil
}

func (p *ProcSource) collectDiskStats(now *timestamppb.Timestamp) ([]*proto.Metric, error) {
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p.mu.Lock()
	defer p.mu.Unlock()

	currentTime := time.Now()
	elapsed := currentTime.Sub(p.lastCollectTime).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	metrics := []*proto.Metric{}
	currentStats := make(map[string]IOStats)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}

		device := fields[2]
		// Skip loop and ram devices
		if strings.HasPrefix(device, "loop") || strings.HasPrefix(device, "ram") || strings.HasPrefix(device, "dm-") {
			continue
		}
		// Skip zram devices
		if strings.HasPrefix(device, "zram") {
			continue
		}

		// Partition detection:
		// - NVMe partitions: nvme0n1p1, nvme0n1p2 (contains "p" followed by digit after "n")
		// - SATA/SCSI partitions: sda1, sda2 (letter followed by digit at end)
		isPartition := false

		// Check for NVMe partition pattern (nvme0n1p1)
		if strings.HasPrefix(device, "nvme") {
			if idx := strings.LastIndex(device, "p"); idx != -1 && idx < len(device)-1 {
				// Check if there's a digit after the last 'p'
				afterP := device[idx+1:]
				if len(afterP) > 0 && afterP[0] >= '0' && afterP[0] <= '9' {
					isPartition = true
				}
			}
		} else {
			// Check for SATA/SCSI partition pattern (sda1, sdb2)
			// Device names like sda, sdb, vda, xvda followed by digits are partitions
			if len(device) > 2 {
				lastChar := device[len(device)-1]
				if lastChar >= '0' && lastChar <= '9' {
					prevChar := device[len(device)-2]
					// If previous char is a letter (not 'n' for nvme), it's a partition
					if prevChar >= 'a' && prevChar <= 'z' && prevChar != 'n' {
						isPartition = true
					}
				}
			}
		}

		if isPartition {
			continue
		}

		// 0-indexed in split array:
		// 0: major, 1: minor, 2: device
		// 3: reads completed, 4: reads merged, 5: sectors read, 6: time reading ms
		// 7: writes completed, 8: writes merged, 9: sectors written, 10: time writing ms
		// 11: io in progress, 12: time doing io ms, 13: weighted time io ms

		readsCompleted, _ := strconv.ParseUint(fields[3], 10, 64)
		readSectors, _ := strconv.ParseUint(fields[5], 10, 64)
		readTimeMs, _ := strconv.ParseFloat(fields[6], 64)
		writesCompleted, _ := strconv.ParseUint(fields[7], 10, 64)
		writeSectors, _ := strconv.ParseUint(fields[9], 10, 64)
		writeTimeMs, _ := strconv.ParseFloat(fields[10], 64)
		ioInProgress, _ := strconv.ParseFloat(fields[11], 64)
		ioTimeMs, _ := strconv.ParseFloat(fields[12], 64)

		// Convert sectors to bytes (1 sector = 512 bytes)
		readBytes := readSectors * 512
		writeBytes := writeSectors * 512

		currentStats[device] = IOStats{
			ReadBytes:  readBytes,
			WriteBytes: writeBytes,
			ReadOps:    readsCompleted,
			WriteOps:   writesCompleted,
		}

		labels := []*proto.MetricLabel{{Key: "device", Value: device}}

		// Calculate rates if we have previous stats
		var readBytesRate, writeBytesRate, readOpsRate, writeOpsRate float64
		if prev, ok := p.lastDiskStats[device]; ok {
			readBytesRate = float64(readBytes-prev.ReadBytes) / elapsed
			writeBytesRate = float64(writeBytes-prev.WriteBytes) / elapsed
			readOpsRate = float64(readsCompleted-prev.ReadOps) / elapsed
			writeOpsRate = float64(writesCompleted-prev.WriteOps) / elapsed
			// Clamp negative rates
			if readBytesRate < 0 {
				readBytesRate = 0
			}
			if writeBytesRate < 0 {
				writeBytesRate = 0
			}
		}

		// Cumulative totals (counters)
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.read_bytes_total", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(readBytes)}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.write_bytes_total", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(writeBytes)}},
		})

		// Rate metrics (gauges - bytes per second)
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.read_bytes", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: readBytesRate}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.write_bytes", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: writeBytesRate}},
		})

		// Operation rates
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.read_ops", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: readOpsRate}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.write_ops", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: writeOpsRate}},
		})

		// Time metrics (cumulative)
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.read_time_ms", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: readTimeMs}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.write_time_ms", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: writeTimeMs}},
		})
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.io_time_ms", Type: proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: ioTimeMs}},
		})

		// Current I/O in progress (gauge)
		metrics = append(metrics, &proto.Metric{
			Name: "system.disk.io_in_progress", Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels, Points: []*proto.MetricPoint{{Timestamp: now, Value: ioInProgress}},
		})
	}

	// Update stored stats
	p.lastDiskStats = currentStats

	return metrics, nil
}

func parseMemLine(name, line string, ts *timestamppb.Timestamp) *proto.Metric {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil
	}
	val, err := strconv.ParseFloat(fields[1], 64) // KB
	if err != nil {
		return nil
	}
	return createGauge(name, val*1024, ts) // Convert to bytes
}

func createGauge(name string, value float64, ts *timestamppb.Timestamp) *proto.Metric {
	return &proto.Metric{
		Name: name,
		Type: proto.MetricType_METRIC_TYPE_GAUGE,
		Points: []*proto.MetricPoint{
			{
				Timestamp: ts,
				Value:     value,
			},
		},
	}
}

func createCounter(name string, value float64, ts *timestamppb.Timestamp) *proto.Metric {
	return &proto.Metric{
		Name: name,
		Type: proto.MetricType_METRIC_TYPE_COUNTER,
		Points: []*proto.MetricPoint{
			{
				Timestamp: ts,
				Value:     value,
			},
		},
	}
}

func (p *ProcSource) collectAdvancedStat(now *timestamppb.Timestamp) ([]*proto.Metric, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	metrics := []*proto.Metric{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		switch parts[0] {
		case "ctxt":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, createCounter("system.ctxt", v, now))
			}
		case "processes":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, createCounter("system.processes", v, now))
			}
		case "procs_running":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, createGauge("system.procs_running", v, now))
			}
		case "procs_blocked":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, createGauge("system.procs_blocked", v, now))
			}
		case "intr":
			// First value after "intr" is total interrupts
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, createCounter("system.intr", v, now))
			}
		case "softirq":
			// First value after "softirq" is total soft interrupts
			if len(parts) >= 2 {
				if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
					metrics = append(metrics, createCounter("system.softirq_total", v, now))
				}
			}
		case "btime":
			// Boot time as Unix epoch
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, createGauge("system.boot_time", v, now))
			}
		}
	}
	return metrics, nil
}

func (p *ProcSource) collectOpenFiles(now *timestamppb.Timestamp) ([]*proto.Metric, error) {
	f, err := os.Open("/proc/sys/fs/file-nr")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	metrics := []*proto.Metric{}
	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		// file-nr format: allocated  free  maximum
		if len(parts) >= 3 {
			if allocated, err := strconv.ParseFloat(parts[0], 64); err == nil {
				metrics = append(metrics, createGauge("system.fd.allocated", allocated, now))
				metrics = append(metrics, createGauge("system.fd.open", allocated, now)) // Alias for compatibility
			}
			if free, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, createGauge("system.fd.free", free, now))
			}
			if max, err := strconv.ParseFloat(parts[2], 64); err == nil {
				metrics = append(metrics, createGauge("system.fd.maximum", max, now))
			}
		}
	}
	if len(metrics) == 0 {
		return nil, fmt.Errorf("failed to read file-nr")
	}
	return metrics, nil
}

// collectVMStat collects virtual memory statistics from /proc/vmstat
func (p *ProcSource) collectVMStat(now *timestamppb.Timestamp) ([]*proto.Metric, error) {
	f, err := os.Open("/proc/vmstat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	metrics := []*proto.Metric{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		val, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}

		switch key {
		case "pgfault":
			metrics = append(metrics, createCounter("system.vm.pgfault", val, now))
		case "pgmajfault":
			metrics = append(metrics, createCounter("system.vm.pgmajfault", val, now))
		case "pswpin":
			metrics = append(metrics, createCounter("system.vm.pswpin", val, now))
		case "pswpout":
			metrics = append(metrics, createCounter("system.vm.pswpout", val, now))
		case "pgpgin":
			metrics = append(metrics, createCounter("system.vm.pgpgin", val, now))
		case "pgpgout":
			metrics = append(metrics, createCounter("system.vm.pgpgout", val, now))
		case "oom_kill":
			metrics = append(metrics, createCounter("system.vm.oom_kill", val, now))
		case "nr_dirty":
			metrics = append(metrics, createGauge("system.vm.nr_dirty", val, now))
		case "nr_writeback":
			metrics = append(metrics, createGauge("system.vm.nr_writeback", val, now))
		case "nr_free_pages":
			metrics = append(metrics, createGauge("system.vm.nr_free_pages", val, now))
		}
	}
	return metrics, nil
}
