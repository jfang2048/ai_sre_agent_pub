// Package probe implements the controlled node (probe) that runs on monitored hosts.
// It is responsible for low-level data collection from Linux kernel interfaces.
//
// Terminology:
//   - Probe: This component - a lightweight data collector on monitored hosts
//   - Controller: The central aggregation server
//   - Agent: The overall AI SRE system (NOT this component)
package probe

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Metric represents a single collected metric
type Metric struct {
	Name      string            `json:"name"`
	Help      string            `json:"help,omitempty"`
	Type      string            `json:"type"` // gauge, counter
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// MetricBatch contains a collection of metrics from a single scrape
type MetricBatch struct {
	Metrics     []Metric  `json:"metrics"`
	CollectedAt time.Time `json:"collected_at"`
	Hostname    string    `json:"hostname"`
}

// Collector gathers system metrics from /proc and /sys
type Collector struct {
	hostname string

	// State for rate calculations
	mu              sync.Mutex
	lastCPUTotal    uint64
	lastCPUIdle     uint64
	lastNetStats    map[string]netStats
	lastDiskStats   map[string]diskStats
	lastCollectTime time.Time

	// Collection configuration
	level         int // 1-5: Basic, Extended, Deep, Logs, RCA
	topNProcesses int

	// Root Cause Analysis collector (Level 5)
	rcaCollector *RootCauseCollector

	// New Collectors
	virtCollector  *VirtCollector
	traceCollector *TraceCollector
	ebpfCollector  *EBPFCollector
	logCollector   *LogCollector
	gpuCollector   *GPUCollector

	ebpfConfig EBPFConfig
}

type netStats struct {
	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
	TxPackets uint64
}

type diskStats struct {
	ReadBytes  uint64
	WriteBytes uint64
	ReadOps    uint64
	WriteOps   uint64
}

// CollectorOption is a functional option for configuring the collector
type CollectorOption func(*Collector)

// WithLevel sets the collection level (1-5)
func WithLevel(level int) CollectorOption {
	return func(c *Collector) {
		if level < 1 {
			level = 1
		}
		if level > 5 {
			level = 5
		}
		c.level = level
	}
}

// WithTopNProcesses sets the number of top processes to track
func WithTopNProcesses(n int) CollectorOption {
	return func(c *Collector) {
		c.topNProcesses = n
	}
}

// WithRCA enables root cause analysis collection
func WithRCA(config RootCauseConfig) CollectorOption {
	return func(c *Collector) {
		c.rcaCollector = NewRootCauseCollector(config)
	}
}

// WithEBPF configures the eBPF collector explicitly (overrides level gating).
func WithEBPF(cfg EBPFConfig) CollectorOption {
	return func(c *Collector) {
		c.ebpfConfig = cfg
	}
}

// NewCollector creates a new system metrics collector
func NewCollector(opts ...CollectorOption) (*Collector, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	c := &Collector{
		hostname:        hostname,
		lastNetStats:    make(map[string]netStats),
		lastDiskStats:   make(map[string]diskStats),
		lastCollectTime: time.Now(),
		level:           2, // Default: Basic + Extended
		topNProcesses:   10,
	}

	for _, opt := range opts {
		opt(c)
	}

	// Initialize RCA collector for level 5
	if c.level >= 5 && c.rcaCollector == nil {
		c.rcaCollector = NewRootCauseCollector(DefaultRootCauseConfig())
	}

	// Initialize new collectors
	c.virtCollector = NewVirtCollector()

	// Tracing (disabled by default, enabled via option or env)
	var errTrace error
	c.traceCollector, errTrace = NewTraceCollector(hostname, os.Getenv("SRE_COLLECTOR_TRACE_ENDPOINT"))
	if errTrace != nil {
		// Log error but continue
	}

	// Logs (Leaf 4+)
	if c.level >= 4 {
		c.logCollector = NewLogCollector(hostname)
	}

	// eBPF (Level 5+)
	if cfg := c.ebpfConfig; cfg.Enabled || c.level >= 5 {
		if !cfg.Enabled && c.level >= 5 {
			cfg.Enabled = true
		}
		c.ebpfCollector = NewEBPFCollectorWithConfig(cfg)
	}

	// GPU metrics (nvidia-smi if available)
	c.gpuCollector = NewGPUCollector()

	return c, nil
}

// Collect gathers all system metrics based on configured level
func (c *Collector) Collect() (*MetricBatch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(c.lastCollectTime).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	var metrics []Metric

	// Level 1: Basic metrics (always collected)
	if m, err := c.collectLoadAvg(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectCPU(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectMemory(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectDisk(now, elapsed); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectNetwork(now, elapsed); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectVMStat(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectStat(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectFileDescriptors(now); err == nil {
		metrics = append(metrics, m...)
	}

	// Level 2: Extended metrics (PSI, scheduler, TCP, thermal)
	if c.level >= 2 {
		metrics = append(metrics, c.collectExtended(now)...)
	}

	// Level 3: Deep metrics (per-process, interrupts)
	if c.level >= 3 {
		metrics = append(metrics, c.collectDeep(now, c.topNProcesses)...)
	}

	// Level 4: Kernel events (log summaries)
	if c.level >= 4 {
		if m, err := c.collectKernelEvents(now); err == nil {
			metrics = append(metrics, m...)
		}
	}

	// Level 5: Root Cause Analysis (detailed attribution)
	if c.level >= 5 && c.rcaCollector != nil {
		if m, err := c.rcaCollector.Collect(now); err == nil {
			metrics = append(metrics, m...)
		}
	}

	// Virtualization metrics
	if m, err := c.virtCollector.Collect(now); err == nil {
		metrics = append(metrics, m...)
	}

	// Tracing metrics
	if c.traceCollector != nil {
		if m := c.traceCollector.GetMetrics(now); m != nil {
			metrics = append(metrics, m...)
		}
	}

	// eBPF metrics (Level 5+ or explicitly enabled via config)
	if c.ebpfCollector != nil && (c.level >= 5 || c.ebpfConfig.Enabled) {
		if m := c.ebpfCollector.GetMetrics(now); m != nil {
			metrics = append(metrics, m...)
		}
	}

	// GPU metrics
	if c.gpuCollector != nil {
		if m, err := c.gpuCollector.Collect(now); err == nil && len(m) > 0 {
			metrics = append(metrics, m...)
		}
	}

	// Log metrics (Level 4+)
	if c.logCollector != nil && c.level >= 4 {
		if m := c.logCollector.GetMetrics(now); m != nil {
			metrics = append(metrics, m...)
		}
	}

	c.lastCollectTime = now

	return &MetricBatch{
		Metrics:     metrics,
		CollectedAt: now,
		Hostname:    c.hostname,
	}, nil
}

// Start starts background collectors
func (c *Collector) Start() {
	if c.logCollector != nil {
		c.logCollector.Start()
	}
	if c.ebpfCollector != nil {
		c.ebpfCollector.Start()
	}
}

// Stop stops background collectors
func (c *Collector) Stop() {
	if c.logCollector != nil {
		c.logCollector.Stop()
	}
	if c.ebpfCollector != nil {
		c.ebpfCollector.Stop()
	}
	if c.traceCollector != nil {
		c.traceCollector.Shutdown(context.Background())
	}
}

// collectLoadAvg reads /proc/loadavg
func (c *Collector) collectLoadAvg(now time.Time) ([]Metric, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return nil, fmt.Errorf("invalid loadavg format")
	}

	var metrics []Metric

	if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
		metrics = append(metrics, Metric{
			Name:      "node_load1",
			Help:      "1 minute load average",
			Type:      "gauge",
			Value:     v,
			Timestamp: now,
		})
	}

	if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
		metrics = append(metrics, Metric{
			Name:      "node_load5",
			Help:      "5 minute load average",
			Type:      "gauge",
			Value:     v,
			Timestamp: now,
		})
	}

	if v, err := strconv.ParseFloat(fields[2], 64); err == nil {
		metrics = append(metrics, Metric{
			Name:      "node_load15",
			Help:      "15 minute load average",
			Type:      "gauge",
			Value:     v,
			Timestamp: now,
		})
	}

	return metrics, nil
}

// collectCPU reads /proc/stat for CPU metrics
func (c *Collector) collectCPU(now time.Time) ([]Metric, error) {
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

	var metrics []Metric

	// Calculate CPU usage from delta
	if c.lastCPUTotal > 0 {
		totalDelta := total - c.lastCPUTotal
		idleDelta := idle - c.lastCPUIdle

		if totalDelta == 0 {
			totalDelta = 1
		}

		usagePct := float64(totalDelta-idleDelta) / float64(totalDelta) * 100.0
		if usagePct < 0 {
			usagePct = 0
		} else if usagePct > 100 {
			usagePct = 100
		}

		metrics = append(metrics, Metric{
			Name:      "node_cpu_usage_percent",
			Help:      "CPU usage percentage",
			Type:      "gauge",
			Value:     usagePct,
			Timestamp: now,
		})
	}

	c.lastCPUTotal = total
	c.lastCPUIdle = idle

	// Report component percentages based on cumulative values
	totalF := float64(total)
	if totalF == 0 {
		totalF = 1
	}

	modes := []struct {
		name  string
		value uint64
	}{
		{"user", user + nice},
		{"system", system},
		{"idle", idle},
		{"iowait", iowait},
		{"irq", irq},
		{"softirq", softirq},
		{"steal", steal},
	}

	for _, m := range modes {
		metrics = append(metrics, Metric{
			Name:      "node_cpu_seconds_total",
			Help:      "CPU time in seconds",
			Type:      "counter",
			Value:     float64(m.value) / 100.0, // jiffies to seconds (assuming HZ=100)
			Labels:    map[string]string{"mode": m.name},
			Timestamp: now,
		})
	}

	return metrics, nil
}

// collectMemory reads /proc/meminfo
func (c *Collector) collectMemory(now time.Time) ([]Metric, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	memValues := make(map[string]float64)
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		key := strings.TrimSuffix(parts[0], ":")
		val, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}
		// Convert from KB to bytes
		memValues[key] = val * 1024
	}

	var metrics []Metric

	memMappings := map[string]string{
		"MemTotal":     "node_memory_MemTotal_bytes",
		"MemFree":      "node_memory_MemFree_bytes",
		"MemAvailable": "node_memory_MemAvailable_bytes",
		"Buffers":      "node_memory_Buffers_bytes",
		"Cached":       "node_memory_Cached_bytes",
		"SwapTotal":    "node_memory_SwapTotal_bytes",
		"SwapFree":     "node_memory_SwapFree_bytes",
		"Slab":         "node_memory_Slab_bytes",
		"Dirty":        "node_memory_Dirty_bytes",
		"Writeback":    "node_memory_Writeback_bytes",
		"AnonPages":    "node_memory_AnonPages_bytes",
		"Mapped":       "node_memory_Mapped_bytes",
		"PageTables":   "node_memory_PageTables_bytes",
	}

	for src, dst := range memMappings {
		if v, ok := memValues[src]; ok {
			metrics = append(metrics, Metric{
				Name:      dst,
				Type:      "gauge",
				Value:     v,
				Timestamp: now,
			})
		}
	}

	// Calculate used memory
	if total, ok := memValues["MemTotal"]; ok {
		if avail, ok := memValues["MemAvailable"]; ok {
			used := total - avail
			metrics = append(metrics, Metric{
				Name:      "node_memory_Used_bytes",
				Help:      "Memory used in bytes",
				Type:      "gauge",
				Value:     used,
				Timestamp: now,
			})
		}
	}

	return metrics, nil
}

// collectDisk reads /proc/diskstats
func (c *Collector) collectDisk(now time.Time, elapsed float64) ([]Metric, error) {
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	currentStats := make(map[string]diskStats)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue
		}

		device := fields[2]
		// Skip partitions and virtual devices
		if c.skipDiskDevice(device) {
			continue
		}

		readsCompleted, _ := strconv.ParseUint(fields[3], 10, 64)
		readSectors, _ := strconv.ParseUint(fields[5], 10, 64)
		readTimeMs, _ := strconv.ParseFloat(fields[6], 64)
		writesCompleted, _ := strconv.ParseUint(fields[7], 10, 64)
		writeSectors, _ := strconv.ParseUint(fields[9], 10, 64)
		writeTimeMs, _ := strconv.ParseFloat(fields[10], 64)
		ioInProgress, _ := strconv.ParseFloat(fields[11], 64)
		ioTimeMs, _ := strconv.ParseFloat(fields[12], 64)

		readBytes := readSectors * 512
		writeBytes := writeSectors * 512

		currentStats[device] = diskStats{
			ReadBytes:  readBytes,
			WriteBytes: writeBytes,
			ReadOps:    readsCompleted,
			WriteOps:   writesCompleted,
		}

		labels := map[string]string{"device": device}

		// Total counters
		metrics = append(metrics, Metric{
			Name:      "node_disk_read_bytes_total",
			Type:      "counter",
			Value:     float64(readBytes),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_written_bytes_total",
			Type:      "counter",
			Value:     float64(writeBytes),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_reads_completed_total",
			Type:      "counter",
			Value:     float64(readsCompleted),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_writes_completed_total",
			Type:      "counter",
			Value:     float64(writesCompleted),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_read_time_seconds_total",
			Type:      "counter",
			Value:     readTimeMs / 1000.0,
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_write_time_seconds_total",
			Type:      "counter",
			Value:     writeTimeMs / 1000.0,
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_io_time_seconds_total",
			Type:      "counter",
			Value:     ioTimeMs / 1000.0,
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_io_now",
			Type:      "gauge",
			Value:     ioInProgress,
			Labels:    labels,
			Timestamp: now,
		})

		// Calculate rates if we have previous stats
		if prev, ok := c.lastDiskStats[device]; ok && elapsed > 0 {
			readBytesRate := float64(readBytes-prev.ReadBytes) / elapsed
			writeBytesRate := float64(writeBytes-prev.WriteBytes) / elapsed
			if readBytesRate < 0 {
				readBytesRate = 0
			}
			if writeBytesRate < 0 {
				writeBytesRate = 0
			}

			metrics = append(metrics, Metric{
				Name:      "node_disk_read_bytes_per_second",
				Type:      "gauge",
				Value:     readBytesRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_written_bytes_per_second",
				Type:      "gauge",
				Value:     writeBytesRate,
				Labels:    labels,
				Timestamp: now,
			})
		}
	}

	c.lastDiskStats = currentStats
	return metrics, nil
}

func (c *Collector) skipDiskDevice(device string) bool {
	// Skip loop, ram, dm, zram devices
	for _, prefix := range []string{"loop", "ram", "dm-", "zram"} {
		if strings.HasPrefix(device, prefix) {
			return true
		}
	}

	// Skip NVMe partitions (nvme0n1p1)
	if strings.HasPrefix(device, "nvme") {
		if idx := strings.LastIndex(device, "p"); idx != -1 && idx < len(device)-1 {
			afterP := device[idx+1:]
			if len(afterP) > 0 && afterP[0] >= '0' && afterP[0] <= '9' {
				return true
			}
		}
		return false
	}

	// Skip SATA/SCSI partitions (sda1, sdb2)
	if len(device) > 2 {
		lastChar := device[len(device)-1]
		if lastChar >= '0' && lastChar <= '9' {
			prevChar := device[len(device)-2]
			if prevChar >= 'a' && prevChar <= 'z' && prevChar != 'n' {
				return true
			}
		}
	}

	return false
}

// collectNetwork reads /proc/net/dev
func (c *Collector) collectNetwork(now time.Time, elapsed float64) ([]Metric, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	currentStats := make(map[string]netStats)

	scanner := bufio.NewScanner(f)
	lineIdx := 0
	for scanner.Scan() {
		line := scanner.Text()
		if lineIdx < 2 {
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

		rxBytes, _ := strconv.ParseUint(fields[0], 10, 64)
		rxPackets, _ := strconv.ParseUint(fields[1], 10, 64)
		rxErrors, _ := strconv.ParseFloat(fields[2], 64)
		rxDrops, _ := strconv.ParseFloat(fields[3], 64)
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)
		txPackets, _ := strconv.ParseUint(fields[9], 10, 64)
		txErrors, _ := strconv.ParseFloat(fields[10], 64)
		txDrops, _ := strconv.ParseFloat(fields[11], 64)

		currentStats[iface] = netStats{
			RxBytes:   rxBytes,
			TxBytes:   txBytes,
			RxPackets: rxPackets,
			TxPackets: txPackets,
		}

		labels := map[string]string{"device": iface}

		// Total counters
		metrics = append(metrics, Metric{
			Name:      "node_network_receive_bytes_total",
			Type:      "counter",
			Value:     float64(rxBytes),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_transmit_bytes_total",
			Type:      "counter",
			Value:     float64(txBytes),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_receive_packets_total",
			Type:      "counter",
			Value:     float64(rxPackets),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_transmit_packets_total",
			Type:      "counter",
			Value:     float64(txPackets),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_receive_errs_total",
			Type:      "counter",
			Value:     rxErrors,
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_transmit_errs_total",
			Type:      "counter",
			Value:     txErrors,
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_receive_drop_total",
			Type:      "counter",
			Value:     rxDrops,
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_transmit_drop_total",
			Type:      "counter",
			Value:     txDrops,
			Labels:    labels,
			Timestamp: now,
		})

		// Calculate rates if we have previous stats
		if prev, ok := c.lastNetStats[iface]; ok && elapsed > 0 {
			rxBytesRate := float64(rxBytes-prev.RxBytes) / elapsed
			txBytesRate := float64(txBytes-prev.TxBytes) / elapsed
			if rxBytesRate < 0 {
				rxBytesRate = 0
			}
			if txBytesRate < 0 {
				txBytesRate = 0
			}

			metrics = append(metrics, Metric{
				Name:      "node_network_receive_bytes_per_second",
				Type:      "gauge",
				Value:     rxBytesRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_transmit_bytes_per_second",
				Type:      "gauge",
				Value:     txBytesRate,
				Labels:    labels,
				Timestamp: now,
			})
		}
	}

	c.lastNetStats = currentStats
	return metrics, nil
}

// collectVMStat reads /proc/vmstat
func (c *Collector) collectVMStat(now time.Time) ([]Metric, error) {
	f, err := os.Open("/proc/vmstat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
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
			metrics = append(metrics, Metric{
				Name:      "node_vmstat_pgfault",
				Type:      "counter",
				Value:     val,
				Timestamp: now,
			})
		case "pgmajfault":
			metrics = append(metrics, Metric{
				Name:      "node_vmstat_pgmajfault",
				Type:      "counter",
				Value:     val,
				Timestamp: now,
			})
		case "pswpin":
			metrics = append(metrics, Metric{
				Name:      "node_vmstat_pswpin",
				Type:      "counter",
				Value:     val,
				Timestamp: now,
			})
		case "pswpout":
			metrics = append(metrics, Metric{
				Name:      "node_vmstat_pswpout",
				Type:      "counter",
				Value:     val,
				Timestamp: now,
			})
		case "oom_kill":
			metrics = append(metrics, Metric{
				Name:      "node_vmstat_oom_kill",
				Type:      "counter",
				Value:     val,
				Timestamp: now,
			})
		}
	}

	return metrics, nil
}

// collectStat reads additional metrics from /proc/stat
func (c *Collector) collectStat(now time.Time) ([]Metric, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 {
			continue
		}

		switch parts[0] {
		case "ctxt":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, Metric{
					Name:      "node_context_switches_total",
					Type:      "counter",
					Value:     v,
					Timestamp: now,
				})
			}
		case "processes":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, Metric{
					Name:      "node_forks_total",
					Type:      "counter",
					Value:     v,
					Timestamp: now,
				})
			}
		case "procs_running":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, Metric{
					Name:      "node_procs_running",
					Type:      "gauge",
					Value:     v,
					Timestamp: now,
				})
			}
		case "procs_blocked":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, Metric{
					Name:      "node_procs_blocked",
					Type:      "gauge",
					Value:     v,
					Timestamp: now,
				})
			}
		case "intr":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, Metric{
					Name:      "node_intr_total",
					Type:      "counter",
					Value:     v,
					Timestamp: now,
				})
			}
		case "btime":
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				metrics = append(metrics, Metric{
					Name:      "node_boot_time_seconds",
					Type:      "gauge",
					Value:     v,
					Timestamp: now,
				})
			}
		}
	}

	return metrics, nil
}

// collectFileDescriptors reads /proc/sys/fs/file-nr
func (c *Collector) collectFileDescriptors(now time.Time) ([]Metric, error) {
	data, err := os.ReadFile("/proc/sys/fs/file-nr")
	if err != nil {
		return nil, err
	}

	parts := strings.Fields(string(data))
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid file-nr format")
	}

	var metrics []Metric

	if allocated, err := strconv.ParseFloat(parts[0], 64); err == nil {
		metrics = append(metrics, Metric{
			Name:      "node_filefd_allocated",
			Type:      "gauge",
			Value:     allocated,
			Timestamp: now,
		})
	}

	if max, err := strconv.ParseFloat(parts[2], 64); err == nil {
		metrics = append(metrics, Metric{
			Name:      "node_filefd_maximum",
			Type:      "gauge",
			Value:     max,
			Timestamp: now,
		})
	}

	return metrics, nil
}
