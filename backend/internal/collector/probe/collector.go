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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
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
	lastCPUIOWait   uint64
	lastNetStats    map[string]netStats
	lastDiskStats   map[string]diskStats
	lastVMStats     map[string]uint64
	lastNetSNMP     map[string]uint64
	lastSoftnet     map[int]softnetStats
	lastNICIRQs     map[string]uint64
	lastRDMAPorts   map[string]rdmaPortStats
	lastCollectTime time.Time
	compatSampling  compatibilitySamplingProfile
	extendedCache   cachedMetricTier
	hardwareCache   cachedMetricTier
	deepCache       cachedMetricTier
	kernelCache     cachedMetricTier
	rcaCache        cachedMetricTier
	gpuCache        cachedMetricTier

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

	ebpfConfig                     EBPFConfig
	suppressCachedHardwarePayloads bool
}

type netStats struct {
	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
	TxPackets uint64
	RxErrors  uint64
	TxErrors  uint64
	RxDrops   uint64
	TxDrops   uint64
}

type diskStats struct {
	ReadBytes           uint64
	WriteBytes          uint64
	ReadOps             uint64
	WriteOps            uint64
	ReadTimeSeconds     float64
	WriteTimeSeconds    float64
	IOTimeSeconds       float64
	WeightedIOTimeTotal float64
}

type latencySample struct {
	LatencySeconds float64
	Ops            uint64
}

type softnetStats struct {
	Processed uint64
	Dropped   uint64
	Squeezed  uint64
}

type rdmaPortStats struct {
	XmitWords        uint64
	RcvWords         uint64
	ErrorEvents      uint64
	CongestionEvents uint64
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

// WithSuppressCachedHardwarePayloads avoids re-emitting cached slow hardware-tier
// compatibility metrics on cache-hit cycles.
func WithSuppressCachedHardwarePayloads(enabled bool) CollectorOption {
	return func(c *Collector) {
		c.suppressCachedHardwarePayloads = enabled
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
		lastVMStats:     make(map[string]uint64),
		lastNetSNMP:     make(map[string]uint64),
		lastSoftnet:     make(map[int]softnetStats),
		lastNICIRQs:     make(map[string]uint64),
		lastRDMAPorts:   make(map[string]rdmaPortStats),
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

	// eBPF is the primary kernel-event path in v0.7, but constrained operators
	// can still disable it explicitly and accept degraded visibility.
	if c.ebpfConfig.Enabled {
		cfg := c.ebpfConfig
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

	if m, err := c.collectFilesystems(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectNetwork(now, elapsed); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectVMStat(now, elapsed); err == nil {
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
		if c.compatSampling.Enabled {
			metrics = append(metrics, c.collectExtendedTier(now, elapsed, compatibilityAnomalyTriggered(metrics))...)
			metrics = append(metrics, c.collectHardwareTier(now, elapsed, compatibilityAnomalyTriggered(metrics))...)
		} else {
			metrics = append(metrics, c.collectExtended(now, elapsed)...)
		}
	}

	// Level 3: Deep metrics (per-process, interrupts)
	if c.level >= 3 {
		if c.compatSampling.Enabled {
			metrics = append(metrics, c.collectDeepTier(now, compatibilityAnomalyTriggered(metrics))...)
		} else {
			metrics = append(metrics, c.collectDeep(now, c.topNProcesses)...)
		}
	}

	// Level 4: Kernel events (log summaries)
	if c.level >= 4 {
		if c.compatSampling.Enabled {
			metrics = append(metrics, c.collectKernelEventsTier(now, compatibilityAnomalyTriggered(metrics))...)
		} else if m, err := c.collectKernelEvents(now); err == nil {
			metrics = append(metrics, m...)
		}
	}

	// Level 5: Root Cause Analysis (detailed attribution)
	if c.level >= 5 && c.rcaCollector != nil {
		if c.compatSampling.Enabled {
			metrics = append(metrics, c.collectRCATier(now, compatibilityAnomalyTriggered(metrics))...)
		} else if m, err := c.rcaCollector.Collect(now); err == nil {
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

	// eBPF metrics are always part of the runtime observability core.
	if c.ebpfCollector != nil {
		if m := c.ebpfCollector.GetMetrics(now); m != nil {
			metrics = append(metrics, m...)
		}
	}

	// GPU metrics
	if c.gpuCollector != nil {
		if c.compatSampling.Enabled {
			metrics = append(metrics, c.collectGPUTier(now, compatibilityAnomalyTriggered(metrics))...)
		} else if m, err := c.gpuCollector.Collect(now); err == nil && len(m) > 0 {
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
		totalDelta := uint64(0)
		if total >= c.lastCPUTotal {
			totalDelta = total - c.lastCPUTotal
		}
		idleDelta := uint64(0)
		if idle >= c.lastCPUIdle {
			idleDelta = idle - c.lastCPUIdle
		}
		iowaitDelta := uint64(0)
		if iowait >= c.lastCPUIOWait {
			iowaitDelta = iowait - c.lastCPUIOWait
		}

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

		iowaitPct := float64(iowaitDelta) / float64(totalDelta) * 100.0
		if iowaitPct < 0 {
			iowaitPct = 0
		} else if iowaitPct > 100 {
			iowaitPct = 100
		}
		metrics = append(metrics, Metric{
			Name:      "node_cpu_iowait_percent",
			Help:      "CPU iowait percentage",
			Type:      "gauge",
			Value:     iowaitPct,
			Timestamp: now,
		})
	}

	c.lastCPUTotal = total
	c.lastCPUIdle = idle
	c.lastCPUIOWait = iowait

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

// collectDisk reads /proc/diskstats and emits both per-device and per-partition views.
func (c *Collector) collectDisk(now time.Time, elapsed float64) ([]Metric, error) {
	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	currentStats := make(map[string]diskStats)

	var deviceCount int
	var totalReadBPS float64
	var totalWriteBPS float64
	var totalReadIOPS float64
	var totalWriteIOPS float64
	var totalQueueDepth float64
	var peakUtilization float64
	var totalQueueCapacity float64
	var readTimeDeltaTotal float64
	var writeTimeDeltaTotal float64
	var readOpsDeltaTotal uint64
	var writeOpsDeltaTotal uint64
	var latencySamples []latencySample
	var nvmeCount int
	var nvmeReadBPS float64
	var nvmeWriteBPS float64
	var nvmeIOPS float64
	var nvmeQueueDepth float64
	var nvmePeakUtilization float64
	var nvmeRequestLatencySecondsTotal float64
	var nvmeOpsDeltaTotal uint64

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue
		}

		devName := fields[2]
		if shouldSkipDiskDevice(devName) {
			continue
		}

		scope, parent := diskMetricScope(devName)

		readsCompleted := parseUintField(fields[3])
		readSectors := parseUintField(fields[5])
		readTimeSeconds := parseFloatField(fields[6]) / 1000.0
		writesCompleted := parseUintField(fields[7])
		writeSectors := parseUintField(fields[9])
		writeTimeSeconds := parseFloatField(fields[10]) / 1000.0
		ioInProgress := parseFloatField(fields[11])
		ioTimeSeconds := parseFloatField(fields[12]) / 1000.0
		weightedIOTimeSeconds := 0.0
		if len(fields) > 13 {
			weightedIOTimeSeconds = parseFloatField(fields[13]) / 1000.0
		}

		readBytes := readSectors * 512
		writeBytes := writeSectors * 512
		key := scope + ":" + devName
		currentStats[key] = diskStats{
			ReadBytes:           readBytes,
			WriteBytes:          writeBytes,
			ReadOps:             readsCompleted,
			WriteOps:            writesCompleted,
			ReadTimeSeconds:     readTimeSeconds,
			WriteTimeSeconds:    writeTimeSeconds,
			IOTimeSeconds:       ioTimeSeconds,
			WeightedIOTimeTotal: weightedIOTimeSeconds,
		}

		if scope == "device" {
			deviceCount++
		}

		labels := map[string]string{"device": devName}
		if scope == "partition" {
			labels = map[string]string{
				"device":    parent,
				"partition": devName,
			}
		}

		queueCapacity := 0.0
		if scope == "device" {
			queueCapacity = readBlockQueueCapacity(devName)
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
				Value:     readTimeSeconds,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_write_time_seconds_total",
				Type:      "counter",
				Value:     writeTimeSeconds,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_io_time_seconds_total",
				Type:      "counter",
				Value:     ioTimeSeconds,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_weighted_io_time_seconds_total",
				Type:      "counter",
				Value:     weightedIOTimeSeconds,
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
			if queueCapacity > 0 {
				metrics = append(metrics, Metric{
					Name:      "node_disk_queue_capacity_requests",
					Type:      "gauge",
					Value:     queueCapacity,
					Labels:    labels,
					Timestamp: now,
				})
				metrics = append(metrics, Metric{
					Name:      "node_disk_io_inflight_fill_percent",
					Type:      "gauge",
					Value:     clampPercent((ioInProgress / queueCapacity) * 100.0),
					Labels:    labels,
					Timestamp: now,
				})
				totalQueueCapacity += queueCapacity
			}
		} else {
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_read_bytes_total",
				Type:      "counter",
				Value:     float64(readBytes),
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_written_bytes_total",
				Type:      "counter",
				Value:     float64(writeBytes),
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_reads_completed_total",
				Type:      "counter",
				Value:     float64(readsCompleted),
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_writes_completed_total",
				Type:      "counter",
				Value:     float64(writesCompleted),
				Labels:    labels,
				Timestamp: now,
			})
		}

		prev, ok := c.lastDiskStats[key]
		if !ok || elapsed <= 0 {
			continue
		}

		readBytesDelta := counterDeltaUint(readBytes, prev.ReadBytes)
		writeBytesDelta := counterDeltaUint(writeBytes, prev.WriteBytes)
		readOpsDelta := counterDeltaUint(readsCompleted, prev.ReadOps)
		writeOpsDelta := counterDeltaUint(writesCompleted, prev.WriteOps)
		readTimeDelta := counterDeltaFloat(readTimeSeconds, prev.ReadTimeSeconds)
		writeTimeDelta := counterDeltaFloat(writeTimeSeconds, prev.WriteTimeSeconds)
		ioTimeDelta := counterDeltaFloat(ioTimeSeconds, prev.IOTimeSeconds)
		weightedIOTimeDelta := counterDeltaFloat(weightedIOTimeSeconds, prev.WeightedIOTimeTotal)

		readBytesRate := float64(readBytesDelta) / elapsed
		writeBytesRate := float64(writeBytesDelta) / elapsed
		readOpsRate := float64(readOpsDelta) / elapsed
		writeOpsRate := float64(writeOpsDelta) / elapsed

		if scope == "device" {
			utilization := clampPercent((ioTimeDelta / elapsed) * 100.0)
			queueDepth := 0.0
			if weightedIOTimeDelta > 0 {
				queueDepth = weightedIOTimeDelta / elapsed
			}
			avgReadLatency := 0.0
			if readOpsDelta > 0 {
				avgReadLatency = readTimeDelta / float64(readOpsDelta)
			}
			avgWriteLatency := 0.0
			if writeOpsDelta > 0 {
				avgWriteLatency = writeTimeDelta / float64(writeOpsDelta)
			}
			avgRequestLatency := 0.0
			totalOpsDelta := readOpsDelta + writeOpsDelta
			if totalOpsDelta > 0 {
				avgRequestLatency = (readTimeDelta + writeTimeDelta) / float64(totalOpsDelta)
				if avgRequestLatency > 0 {
					latencySamples = append(latencySamples, latencySample{
						LatencySeconds: avgRequestLatency,
						Ops:            totalOpsDelta,
					})
				}
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
			metrics = append(metrics, Metric{
				Name:      "node_disk_reads_per_second",
				Type:      "gauge",
				Value:     readOpsRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_writes_per_second",
				Type:      "gauge",
				Value:     writeOpsRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_iops_per_second",
				Type:      "gauge",
				Value:     readOpsRate + writeOpsRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_queue_depth",
				Type:      "gauge",
				Value:     queueDepth,
				Labels:    labels,
				Timestamp: now,
			})
			if queueCapacity > 0 {
				metrics = append(metrics, Metric{
					Name:      "node_disk_queue_depth_fill_percent",
					Type:      "gauge",
					Value:     clampPercent((queueDepth / queueCapacity) * 100.0),
					Labels:    labels,
					Timestamp: now,
				})
			}
			metrics = append(metrics, Metric{
				Name:      "node_disk_utilization_percent",
				Type:      "gauge",
				Value:     utilization,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_avg_read_latency_seconds",
				Type:      "gauge",
				Value:     avgReadLatency,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_avg_write_latency_seconds",
				Type:      "gauge",
				Value:     avgWriteLatency,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_avg_request_latency_seconds",
				Type:      "gauge",
				Value:     avgRequestLatency,
				Labels:    labels,
				Timestamp: now,
			})

			totalReadBPS += readBytesRate
			totalWriteBPS += writeBytesRate
			totalReadIOPS += readOpsRate
			totalWriteIOPS += writeOpsRate
			totalQueueDepth += queueDepth
			if utilization > peakUtilization {
				peakUtilization = utilization
			}
			readTimeDeltaTotal += readTimeDelta
			writeTimeDeltaTotal += writeTimeDelta
			readOpsDeltaTotal += readOpsDelta
			writeOpsDeltaTotal += writeOpsDelta

			if strings.HasPrefix(devName, "nvme") {
				nvmeCount++
				nvmeReadBPS += readBytesRate
				nvmeWriteBPS += writeBytesRate
				nvmeIOPS += readOpsRate + writeOpsRate
				nvmeQueueDepth += queueDepth
				if utilization > nvmePeakUtilization {
					nvmePeakUtilization = utilization
				}
				nvmeRequestLatencySecondsTotal += readTimeDelta + writeTimeDelta
				nvmeOpsDeltaTotal += totalOpsDelta
			}
		} else {
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_read_bytes_per_second",
				Type:      "gauge",
				Value:     readBytesRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_written_bytes_per_second",
				Type:      "gauge",
				Value:     writeBytesRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_reads_per_second",
				Type:      "gauge",
				Value:     readOpsRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_writes_per_second",
				Type:      "gauge",
				Value:     writeOpsRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_partition_iops_per_second",
				Type:      "gauge",
				Value:     readOpsRate + writeOpsRate,
				Labels:    labels,
				Timestamp: now,
			})
		}
	}

	c.lastDiskStats = currentStats

	if deviceCount > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_disk_devices",
			Type:      "gauge",
			Value:     float64(deviceCount),
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_total_read_bytes_per_second",
			Type:      "gauge",
			Value:     totalReadBPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_total_written_bytes_per_second",
			Type:      "gauge",
			Value:     totalWriteBPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_total_reads_per_second",
			Type:      "gauge",
			Value:     totalReadIOPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_total_writes_per_second",
			Type:      "gauge",
			Value:     totalWriteIOPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_total_iops_per_second",
			Type:      "gauge",
			Value:     totalReadIOPS + totalWriteIOPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_queue_depth_total",
			Type:      "gauge",
			Value:     totalQueueDepth,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_disk_queue_depth_avg",
			Type:      "gauge",
			Value:     totalQueueDepth / float64(deviceCount),
			Timestamp: now,
		})
		if totalQueueCapacity > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_disk_queue_capacity_requests_total",
				Type:      "gauge",
				Value:     totalQueueCapacity,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_disk_queue_depth_fill_percent",
				Type:      "gauge",
				Value:     clampPercent((totalQueueDepth / totalQueueCapacity) * 100.0),
				Timestamp: now,
			})
		}
		metrics = append(metrics, Metric{
			Name:      "node_disk_utilization_peak_percent",
			Type:      "gauge",
			Value:     peakUtilization,
			Timestamp: now,
		})

		if readOpsDeltaTotal > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_disk_avg_read_latency_seconds",
				Type:      "gauge",
				Value:     readTimeDeltaTotal / float64(readOpsDeltaTotal),
				Timestamp: now,
			})
		}
		if writeOpsDeltaTotal > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_disk_avg_write_latency_seconds",
				Type:      "gauge",
				Value:     writeTimeDeltaTotal / float64(writeOpsDeltaTotal),
				Timestamp: now,
			})
		}
		if readOpsDeltaTotal+writeOpsDeltaTotal > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_disk_avg_request_latency_seconds",
				Type:      "gauge",
				Value:     (readTimeDeltaTotal + writeTimeDeltaTotal) / float64(readOpsDeltaTotal+writeOpsDeltaTotal),
				Timestamp: now,
			})
		}

		if len(latencySamples) > 0 {
			p50 := weightedLatencyQuantile(latencySamples, 0.50)
			p90 := weightedLatencyQuantile(latencySamples, 0.90)
			p99 := weightedLatencyQuantile(latencySamples, 0.99)

			metrics = append(metrics, Metric{Name: "node_disk_request_latency_p50_seconds", Type: "gauge", Value: p50, Timestamp: now})
			metrics = append(metrics, Metric{Name: "node_disk_request_latency_p90_seconds", Type: "gauge", Value: p90, Timestamp: now})
			metrics = append(metrics, Metric{Name: "node_disk_request_latency_p99_seconds", Type: "gauge", Value: p99, Timestamp: now})

			bucketBounds := []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}
			totalOps := uint64(0)
			for _, sample := range latencySamples {
				totalOps += sample.Ops
			}
			metrics = append(metrics, Metric{Name: "node_disk_request_latency_ops_total", Type: "gauge", Value: float64(totalOps), Timestamp: now})
			for _, bound := range bucketBounds {
				cumulativeOps := uint64(0)
				for _, sample := range latencySamples {
					if sample.LatencySeconds <= bound {
						cumulativeOps += sample.Ops
					}
				}
				metrics = append(metrics, Metric{
					Name:      "node_disk_request_latency_ops_bucket",
					Type:      "gauge",
					Value:     float64(cumulativeOps),
					Labels:    map[string]string{"le": strconv.FormatFloat(bound, 'g', -1, 64)},
					Timestamp: now,
				})
			}
		}

		if nvmeCount > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_nvme_devices",
				Type:      "gauge",
				Value:     float64(nvmeCount),
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_nvme_total_read_bytes_per_second",
				Type:      "gauge",
				Value:     nvmeReadBPS,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_nvme_total_written_bytes_per_second",
				Type:      "gauge",
				Value:     nvmeWriteBPS,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_nvme_total_iops_per_second",
				Type:      "gauge",
				Value:     nvmeIOPS,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_nvme_queue_depth_total",
				Type:      "gauge",
				Value:     nvmeQueueDepth,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_nvme_queue_depth_avg",
				Type:      "gauge",
				Value:     nvmeQueueDepth / float64(nvmeCount),
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_nvme_utilization_peak_percent",
				Type:      "gauge",
				Value:     nvmePeakUtilization,
				Timestamp: now,
			})
			if nvmeOpsDeltaTotal > 0 {
				metrics = append(metrics, Metric{
					Name:      "node_nvme_avg_request_latency_seconds",
					Type:      "gauge",
					Value:     nvmeRequestLatencySecondsTotal / float64(nvmeOpsDeltaTotal),
					Timestamp: now,
				})
			}
		}
	}

	return metrics, nil
}

func shouldSkipDiskDevice(device string) bool {
	for _, prefix := range []string{"loop", "ram", "dm-", "zram", "fd"} {
		if strings.HasPrefix(device, prefix) {
			return true
		}
	}
	return false
}

// skipDiskDevice preserves the legacy behavior used in tests (skip partitions).
func (c *Collector) skipDiskDevice(device string) bool {
	if shouldSkipDiskDevice(device) {
		return true
	}
	scope, _ := diskMetricScope(device)
	return scope == "partition"
}

func diskMetricScope(device string) (scope string, parent string) {
	if strings.HasPrefix(device, "nvme") || strings.HasPrefix(device, "mmcblk") {
		if idx := strings.LastIndex(device, "p"); idx > 0 && isDigits(device[idx+1:]) {
			return "partition", device[:idx]
		}
		return "device", ""
	}
	if strings.HasPrefix(device, "md") || strings.HasPrefix(device, "sr") {
		return "device", ""
	}

	cut := trailingDigitsStart(device)
	if cut > 0 {
		return "partition", device[:cut]
	}
	return "device", ""
}

func trailingDigitsStart(s string) int {
	if s == "" {
		return -1
	}
	i := len(s) - 1
	for i >= 0 && s[i] >= '0' && s[i] <= '9' {
		i--
	}
	if i == len(s)-1 {
		return -1
	}
	if i < 0 {
		return -1
	}
	if s[i] == '-' {
		return -1
	}
	return i + 1
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

type mountInfo struct {
	Mountpoint string
	Device     string
	FSType     string
}

// collectFilesystems emits per-mount capacity/inode visibility from statfs.
func (c *Collector) collectFilesystems(now time.Time) ([]Metric, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}

	var metrics []Metric
	lines := strings.Split(string(data), "\n")
	var mounts int
	var totalSize float64
	var totalUsed float64
	var totalAvail float64
	var maxUsedPercent float64
	var maxInodePercent float64

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		m, ok := parseMountInfoLine(line)
		if !ok || shouldSkipFilesystem(m) {
			continue
		}

		var stat unix.Statfs_t
		if err := unix.Statfs(m.Mountpoint, &stat); err != nil {
			continue
		}

		blockSize := float64(stat.Bsize)
		sizeBytes := float64(stat.Blocks) * blockSize
		freeBytes := float64(stat.Bfree) * blockSize
		availBytes := float64(stat.Bavail) * blockSize
		usedBytes := sizeBytes - freeBytes
		if usedBytes < 0 {
			usedBytes = 0
		}
		usedPercent := percentRatio(usedBytes, sizeBytes)

		filesTotal := float64(stat.Files)
		filesFree := float64(stat.Ffree)
		filesUsed := filesTotal - filesFree
		if filesUsed < 0 {
			filesUsed = 0
		}
		filesUsedPercent := percentRatio(filesUsed, filesTotal)

		readOnly := 0.0
		if stat.Flags&unix.ST_RDONLY != 0 {
			readOnly = 1.0
		}

		labels := map[string]string{
			"device":     m.Device,
			"mountpoint": m.Mountpoint,
			"fstype":     m.FSType,
		}

		metrics = append(metrics, Metric{Name: "node_filesystem_size_bytes", Type: "gauge", Value: sizeBytes, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_free_bytes", Type: "gauge", Value: freeBytes, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_avail_bytes", Type: "gauge", Value: availBytes, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_used_bytes", Type: "gauge", Value: usedBytes, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_used_percent", Type: "gauge", Value: usedPercent, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_files", Type: "gauge", Value: filesTotal, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_files_free", Type: "gauge", Value: filesFree, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_files_used", Type: "gauge", Value: filesUsed, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_files_used_percent", Type: "gauge", Value: filesUsedPercent, Labels: labels, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_readonly", Type: "gauge", Value: readOnly, Labels: labels, Timestamp: now})

		mounts++
		totalSize += sizeBytes
		totalUsed += usedBytes
		totalAvail += availBytes
		if usedPercent > maxUsedPercent {
			maxUsedPercent = usedPercent
		}
		if filesUsedPercent > maxInodePercent {
			maxInodePercent = filesUsedPercent
		}
	}

	if mounts > 0 {
		metrics = append(metrics, Metric{Name: "node_filesystem_mounts_total", Type: "gauge", Value: float64(mounts), Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_total_size_bytes", Type: "gauge", Value: totalSize, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_total_used_bytes", Type: "gauge", Value: totalUsed, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_total_avail_bytes", Type: "gauge", Value: totalAvail, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_total_used_percent", Type: "gauge", Value: percentRatio(totalUsed, totalSize), Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_space_pressure_percent", Type: "gauge", Value: maxUsedPercent, Timestamp: now})
		metrics = append(metrics, Metric{Name: "node_filesystem_inode_pressure_percent", Type: "gauge", Value: maxInodePercent, Timestamp: now})
	}

	return metrics, nil
}

func parseMountInfoLine(line string) (mountInfo, bool) {
	parts := strings.Split(line, " - ")
	if len(parts) != 2 {
		return mountInfo{}, false
	}
	left := strings.Fields(parts[0])
	right := strings.Fields(parts[1])
	if len(left) < 5 || len(right) < 2 {
		return mountInfo{}, false
	}
	return mountInfo{
		Mountpoint: decodeMountField(left[4]),
		FSType:     right[0],
		Device:     decodeMountField(right[1]),
	}, true
}

func decodeMountField(in string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(in)
}

func shouldSkipFilesystem(m mountInfo) bool {
	if m.Mountpoint == "" || m.FSType == "" {
		return true
	}
	for _, prefix := range []string{"/proc", "/sys", "/dev"} {
		if strings.HasPrefix(m.Mountpoint, prefix) {
			return true
		}
	}

	switch m.FSType {
	case "proc", "sysfs", "devtmpfs", "devpts", "tmpfs", "cgroup", "cgroup2", "securityfs",
		"debugfs", "tracefs", "configfs", "mqueue", "hugetlbfs", "pstore", "fusectl",
		"rpc_pipefs", "autofs", "nfsd", "binfmt_misc":
		return true
	default:
		return false
	}
}

func parseUintField(in string) uint64 {
	v, err := strconv.ParseUint(in, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseFloatField(in string) float64 {
	v, err := strconv.ParseFloat(in, 64)
	if err != nil {
		return 0
	}
	return v
}

func readBlockQueueCapacity(device string) float64 {
	if device == "" {
		return 0
	}
	data, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/queue/nr_requests", device))
	if err != nil {
		return 0
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || val <= 0 {
		return 0
	}
	return val
}

func counterDeltaUint(curr, prev uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	return curr
}

func counterDeltaFloat(curr, prev float64) float64 {
	if curr >= prev {
		return curr - prev
	}
	return curr
}

func percentRatio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return clampPercent((numerator / denominator) * 100.0)
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func weightedLatencyQuantile(samples []latencySample, quantile float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	if quantile <= 0 {
		quantile = 0
	}
	if quantile > 1 {
		quantile = 1
	}

	sorted := append([]latencySample(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].LatencySeconds == sorted[j].LatencySeconds {
			return sorted[i].Ops < sorted[j].Ops
		}
		return sorted[i].LatencySeconds < sorted[j].LatencySeconds
	})

	totalOps := uint64(0)
	for _, sample := range sorted {
		totalOps += sample.Ops
	}
	if totalOps == 0 {
		return 0
	}

	target := uint64(float64(totalOps)*quantile + 0.5)
	if target == 0 {
		target = 1
	}
	running := uint64(0)
	for _, sample := range sorted {
		running += sample.Ops
		if running >= target {
			return sample.LatencySeconds
		}
	}
	return sorted[len(sorted)-1].LatencySeconds
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
	totalRxBPS := 0.0
	totalTxBPS := 0.0
	totalRxPPS := 0.0
	totalTxPPS := 0.0
	totalRxErrPS := 0.0
	totalTxErrPS := 0.0
	totalRxDropPS := 0.0
	totalTxDropPS := 0.0
	totalCapacityBPS := 0.0
	peakUtilPercent := 0.0
	utilSum := 0.0
	utilCount := 0

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
		rxErrors, _ := strconv.ParseUint(fields[2], 10, 64)
		rxDrops, _ := strconv.ParseUint(fields[3], 10, 64)
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)
		txPackets, _ := strconv.ParseUint(fields[9], 10, 64)
		txErrors, _ := strconv.ParseUint(fields[10], 10, 64)
		txDrops, _ := strconv.ParseUint(fields[11], 10, 64)

		currentStats[iface] = netStats{
			RxBytes:   rxBytes,
			TxBytes:   txBytes,
			RxPackets: rxPackets,
			TxPackets: txPackets,
			RxErrors:  rxErrors,
			TxErrors:  txErrors,
			RxDrops:   rxDrops,
			TxDrops:   txDrops,
		}

		labels := map[string]string{"device": iface}
		speedBPS := readInterfaceSpeedBPS(iface)

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
			Value:     float64(rxErrors),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_transmit_errs_total",
			Type:      "counter",
			Value:     float64(txErrors),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_receive_drop_total",
			Type:      "counter",
			Value:     float64(rxDrops),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_transmit_drop_total",
			Type:      "counter",
			Value:     float64(txDrops),
			Labels:    labels,
			Timestamp: now,
		})

		if speedBPS > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_network_interface_speed_bits_per_second",
				Type:      "gauge",
				Value:     speedBPS,
				Labels:    labels,
				Timestamp: now,
			})
		}

		// Calculate rates if we have previous stats
		if prev, ok := c.lastNetStats[iface]; ok && elapsed > 0 {
			rxBytesRate := float64(counterDeltaUint(rxBytes, prev.RxBytes)) / elapsed
			txBytesRate := float64(counterDeltaUint(txBytes, prev.TxBytes)) / elapsed
			rxPacketsRate := float64(counterDeltaUint(rxPackets, prev.RxPackets)) / elapsed
			txPacketsRate := float64(counterDeltaUint(txPackets, prev.TxPackets)) / elapsed
			rxErrRate := float64(counterDeltaUint(rxErrors, prev.RxErrors)) / elapsed
			txErrRate := float64(counterDeltaUint(txErrors, prev.TxErrors)) / elapsed
			rxDropRate := float64(counterDeltaUint(rxDrops, prev.RxDrops)) / elapsed
			txDropRate := float64(counterDeltaUint(txDrops, prev.TxDrops)) / elapsed

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
			metrics = append(metrics, Metric{
				Name:      "node_network_receive_packets_per_second",
				Type:      "gauge",
				Value:     rxPacketsRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_transmit_packets_per_second",
				Type:      "gauge",
				Value:     txPacketsRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_receive_errs_per_second",
				Type:      "gauge",
				Value:     rxErrRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_transmit_errs_per_second",
				Type:      "gauge",
				Value:     txErrRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_receive_drop_per_second",
				Type:      "gauge",
				Value:     rxDropRate,
				Labels:    labels,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_transmit_drop_per_second",
				Type:      "gauge",
				Value:     txDropRate,
				Labels:    labels,
				Timestamp: now,
			})

			if speedBPS > 0 {
				utilization := clampPercent(((rxBytesRate + txBytesRate) * 8.0 / speedBPS) * 100.0)
				metrics = append(metrics, Metric{
					Name:      "node_network_interface_utilization_percent",
					Type:      "gauge",
					Value:     utilization,
					Labels:    labels,
					Timestamp: now,
				})
				if utilization > peakUtilPercent {
					peakUtilPercent = utilization
				}
				utilSum += utilization
				utilCount++
				totalCapacityBPS += speedBPS
			}

			totalRxBPS += rxBytesRate
			totalTxBPS += txBytesRate
			totalRxPPS += rxPacketsRate
			totalTxPPS += txPacketsRate
			totalRxErrPS += rxErrRate
			totalTxErrPS += txErrRate
			totalRxDropPS += rxDropRate
			totalTxDropPS += txDropRate
		}
	}

	c.lastNetStats = currentStats

	if elapsed > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_network_total_receive_bytes_per_second",
			Type:      "gauge",
			Value:     totalRxBPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_transmit_bytes_per_second",
			Type:      "gauge",
			Value:     totalTxBPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_receive_packets_per_second",
			Type:      "gauge",
			Value:     totalRxPPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_transmit_packets_per_second",
			Type:      "gauge",
			Value:     totalTxPPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_receive_errs_per_second",
			Type:      "gauge",
			Value:     totalRxErrPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_transmit_errs_per_second",
			Type:      "gauge",
			Value:     totalTxErrPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_receive_drop_per_second",
			Type:      "gauge",
			Value:     totalRxDropPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_transmit_drop_per_second",
			Type:      "gauge",
			Value:     totalTxDropPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_errs_per_second",
			Type:      "gauge",
			Value:     totalRxErrPS + totalTxErrPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_total_drop_per_second",
			Type:      "gauge",
			Value:     totalRxDropPS + totalTxDropPS,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_network_utilization_peak_percent",
			Type:      "gauge",
			Value:     peakUtilPercent,
			Timestamp: now,
		})
		if utilCount > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_network_utilization_avg_percent",
				Type:      "gauge",
				Value:     utilSum / float64(utilCount),
				Timestamp: now,
			})
		}
		if totalCapacityBPS > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_network_capacity_bits_per_second",
				Type:      "gauge",
				Value:     totalCapacityBPS,
				Timestamp: now,
			})
			metrics = append(metrics, Metric{
				Name:      "node_network_capacity_utilization_percent",
				Type:      "gauge",
				Value:     clampPercent(((totalRxBPS + totalTxBPS) * 8.0 / totalCapacityBPS) * 100.0),
				Timestamp: now,
			})
		}
	}

	return metrics, nil
}

func readInterfaceSpeedBPS(iface string) float64 {
	data, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/speed", iface))
	if err != nil {
		return 0
	}
	speedMbps, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || speedMbps <= 0 || speedMbps > 1_000_000 {
		return 0
	}
	return speedMbps * 1_000_000
}

var vmstatCounterMetricMap = map[string]string{
	"pgfault":                "node_vmstat_pgfault",
	"pgmajfault":             "node_vmstat_pgmajfault",
	"pswpin":                 "node_vmstat_pswpin",
	"pswpout":                "node_vmstat_pswpout",
	"oom_kill":               "node_vmstat_oom_kill",
	"pgpgin":                 "node_vmstat_pgpgin",
	"pgpgout":                "node_vmstat_pgpgout",
	"pgscan_kswapd":          "node_vmstat_pgscan_kswapd",
	"pgscan_direct":          "node_vmstat_pgscan_direct",
	"pgsteal_kswapd":         "node_vmstat_pgsteal_kswapd",
	"pgsteal_direct":         "node_vmstat_pgsteal_direct",
	"pgactivate":             "node_vmstat_pgactivate",
	"pgdeactivate":           "node_vmstat_pgdeactivate",
	"workingset_refault":     "node_vmstat_workingset_refault",
	"workingset_activate":    "node_vmstat_workingset_activate",
	"workingset_restore":     "node_vmstat_workingset_restore",
	"workingset_nodereclaim": "node_vmstat_workingset_nodereclaim",
	"nr_dirtied":             "node_vmstat_nr_dirtied",
	"nr_written":             "node_vmstat_nr_written",
	"numa_hit":               "node_vmstat_numa_hit",
	"numa_miss":              "node_vmstat_numa_miss",
	"numa_foreign":           "node_vmstat_numa_foreign",
	"numa_interleave":        "node_vmstat_numa_interleave",
	"numa_local":             "node_vmstat_numa_local",
	"numa_other":             "node_vmstat_numa_other",
}

var vmstatGaugeMetricMap = map[string]string{
	"nr_dirty":              "node_vmstat_nr_dirty_pages",
	"nr_writeback":          "node_vmstat_nr_writeback_pages",
	"nr_writeback_temp":     "node_vmstat_nr_writeback_temp_pages",
	"nr_file_pages":         "node_vmstat_nr_file_pages",
	"nr_mapped":             "node_vmstat_nr_mapped_pages",
	"nr_shmem":              "node_vmstat_nr_shmem_pages",
	"nr_slab_reclaimable":   "node_vmstat_nr_slab_reclaimable_pages",
	"nr_slab_unreclaimable": "node_vmstat_nr_slab_unreclaimable_pages",
}

var vmstatRateMetricMap = map[string]string{
	"pgfault":         "node_vmstat_pgfault_per_second",
	"pgmajfault":      "node_vmstat_pgmajfault_per_second",
	"pswpin":          "node_vmstat_pswpin_per_second",
	"pswpout":         "node_vmstat_pswpout_per_second",
	"pgpgin":          "node_vmstat_pgpgin_per_second",
	"pgpgout":         "node_vmstat_pgpgout_per_second",
	"pgscan_kswapd":   "node_vmstat_pgscan_kswapd_per_second",
	"pgscan_direct":   "node_vmstat_pgscan_direct_per_second",
	"pgsteal_kswapd":  "node_vmstat_pgsteal_kswapd_per_second",
	"pgsteal_direct":  "node_vmstat_pgsteal_direct_per_second",
	"nr_dirtied":      "node_vmstat_nr_dirtied_per_second",
	"nr_written":      "node_vmstat_nr_written_per_second",
	"numa_hit":        "node_vmstat_numa_hit_per_second",
	"numa_miss":       "node_vmstat_numa_miss_per_second",
	"numa_foreign":    "node_vmstat_numa_foreign_per_second",
	"numa_interleave": "node_vmstat_numa_interleave_per_second",
	"numa_local":      "node_vmstat_numa_local_per_second",
	"numa_other":      "node_vmstat_numa_other_per_second",
}

// collectVMStat reads /proc/vmstat.
func (c *Collector) collectVMStat(now time.Time, elapsed float64) ([]Metric, error) {
	f, err := os.Open("/proc/vmstat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	current := make(map[string]uint64, len(c.lastVMStats))
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		val := parseUintField(parts[1])
		current[key] = val

		if metricName, ok := vmstatCounterMetricMap[key]; ok {
			metrics = append(metrics, Metric{
				Name:      metricName,
				Type:      "counter",
				Value:     float64(val),
				Timestamp: now,
			})
		}
		if metricName, ok := vmstatGaugeMetricMap[key]; ok {
			metrics = append(metrics, Metric{
				Name:      metricName,
				Type:      "gauge",
				Value:     float64(val),
				Timestamp: now,
			})
		}
	}

	if elapsed > 0 {
		for key, metricName := range vmstatRateMetricMap {
			curr, ok := current[key]
			if !ok {
				continue
			}
			prev, hadPrev := c.lastVMStats[key]
			if !hadPrev {
				continue
			}
			metrics = append(metrics, Metric{
				Name:      metricName,
				Type:      "gauge",
				Value:     float64(counterDeltaUint(curr, prev)) / elapsed,
				Timestamp: now,
			})
		}
	}

	local := current["numa_local"]
	other := current["numa_other"]
	miss := current["numa_miss"]
	hit := current["numa_hit"]
	localityDenom := local + other + miss
	if localityDenom > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_numa_locality_ratio_percent",
			Type:      "gauge",
			Value:     clampPercent((float64(local) / float64(localityDenom)) * 100.0),
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_numa_miss_ratio_percent",
			Type:      "gauge",
			Value:     clampPercent((float64(miss) / float64(localityDenom)) * 100.0),
			Timestamp: now,
		})
	}
	hitDenom := hit + miss
	if hitDenom > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_numa_hit_ratio_percent",
			Type:      "gauge",
			Value:     clampPercent((float64(hit) / float64(hitDenom)) * 100.0),
			Timestamp: now,
		})
	}

	c.lastVMStats = current
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
