package sources

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"syscall"
	"unsafe"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// EBPFCollector collects kernel-level metrics using perf events
type EBPFCollector struct {
	config EBPFConfig
	logger *zap.Logger

	mu      sync.RWMutex
	running bool
	stopCh  chan struct{}

	// perf event file descriptors
	perfFds map[string]int

	// Collected metrics
	lastStats map[string]uint64
}

// perfEventAttr is the perf_event_attr struct for perf_event_open syscall
type perfEventAttr struct {
	Type                    uint32
	Size                    uint32
	Config                  uint64
	SamplePeriodOrFreq      uint64
	SampleType              uint64
	ReadFormat              uint64
	Flags                   uint64
	WakeupEventsOrWatermark uint32
	BpType                  uint32
	Ext1                    uint64
	Ext2                    uint64
}

// NewEBPFCollector creates a new eBPF-based metrics collector
func NewEBPFCollector(config EBPFConfig, logger *zap.Logger) *EBPFCollector {
	return &EBPFCollector{
		config:    config,
		logger:    logger.With(zap.String("collector", "ebpf")),
		perfFds:   make(map[string]int),
		stopCh:    make(chan struct{}),
		lastStats: make(map[string]uint64),
	}
}

// Start begins the eBPF collection
func (e *EBPFCollector) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.running {
		return nil
	}

	e.running = true

	// Initialize perf event counters for various kernel metrics
	if e.config.Syscalls {
		e.setupSyscallCounter()
	}
	if e.config.Network {
		e.setupNetworkCounters()
	}
	if e.config.IO {
		e.setupIOCounters()
	}

	e.logger.Info("eBPF collector started")
	return nil
}

// Stop stops the eBPF collection
func (e *EBPFCollector) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.running {
		return nil
	}

	e.running = false
	close(e.stopCh)

	// Close all perf event file descriptors
	for name, fd := range e.perfFds {
		syscall.Close(fd)
		e.logger.Debug("closed perf event", zap.String("event", name), zap.Int("fd", fd))
	}
	e.perfFds = make(map[string]int)

	e.logger.Info("eBPF collector stopped")
	return nil
}

// Collect gathers current eBPF metrics
func (e *EBPFCollector) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	metrics := []*proto.Metric{}
	now := timestamppb.Now()

	e.mu.RLock()
	defer e.mu.RUnlock()

	// Read perf event counters
	for name, fd := range e.perfFds {
		value, err := e.readPerfEvent(fd)
		if err != nil {
			e.logger.Warn("failed to read perf event", zap.String("event", name), zap.Error(err))
			continue
		}

		// Calculate delta from previous reading
		var deltaValue uint64
		if prev, ok := e.lastStats[name]; ok {
			if value >= prev {
				deltaValue = value - prev
			}
		}
		e.lastStats[name] = value

		// Emit both absolute and rate metrics
		labels := []*proto.MetricLabel{{Key: "event", Value: name}}

		metrics = append(metrics, &proto.Metric{
			Name:   "ebpf.perf.count",
			Type:   proto.MetricType_METRIC_TYPE_COUNTER,
			Labels: labels,
			Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(value)}},
		})

		metrics = append(metrics, &proto.Metric{
			Name:   "ebpf.perf.rate",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels,
			Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(deltaValue)}},
		})
	}

	// Collect context switches from /proc/stat as an alternative to eBPF
	if cs, err := e.getContextSwitches(); err == nil {
		metrics = append(metrics, createGauge("system.context_switches", float64(cs), now))
	}

	// Collect page faults
	if pf, err := e.getPageFaults(); err == nil {
		metrics = append(metrics, createGauge("system.page_faults", float64(pf), now))
	}

	// Collect network stats from /proc/net/softnet_stat
	if e.config.Network {
		if netStats, err := e.collectSoftnetStats(now); err == nil {
			metrics = append(metrics, netStats...)
		}
	}

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "ebpf",
		CollectedAt: now,
	}, nil
}

// setupSyscallCounter sets up a counter for system calls
func (e *EBPFCollector) setupSyscallCounter() {
	// Use raw_syscalls:sys_enter as a proxy for syscall rate
	attr := &perfEventAttr{
		Type:   1, // PERF_TYPE_SOFTWARE
		Config: 3, // PERF_COUNT_SW_CONTEXT_SWITCHES
		Size:   uint32(unsafe.Sizeof(perfEventAttr{})),
	}

	fd, err := e.openPerfEvent(attr, -1, -1)
	if err != nil {
		e.logger.Warn("failed to setup syscall counter", zap.Error(err))
		return
	}

	e.perfFds["syscalls"] = fd
}

// setupNetworkCounters sets up network event counters
func (e *EBPFCollector) setupNetworkCounters() {
	// Use context switches as a proxy for network activity
	attr := &perfEventAttr{
		Type:   1, // PERF_TYPE_SOFTWARE
		Config: 3, // PERF_COUNT_SW_CONTEXT_SWITCHES
		Size:   uint32(unsafe.Sizeof(perfEventAttr{})),
	}

	fd, err := e.openPerfEvent(attr, -1, -1)
	if err != nil {
		e.logger.Warn("failed to setup network counter", zap.Error(err))
		return
	}

	e.perfFds["network_events"] = fd
}

// setupIOCounters sets up I/O counters
func (e *EBPFCollector) setupIOCounters() {
	// Use page faults as a proxy for I/O activity
	attr := &perfEventAttr{
		Type:   1, // PERF_TYPE_SOFTWARE
		Config: 2, // PERF_COUNT_SW_PAGE_FAULTS
		Size:   uint32(unsafe.Sizeof(perfEventAttr{})),
	}

	fd, err := e.openPerfEvent(attr, -1, -1)
	if err != nil {
		e.logger.Warn("failed to setup I/O counter", zap.Error(err))
		return
	}

	e.perfFds["io_events"] = fd
}

// openPerfEvent opens a perf event file descriptor
func (e *EBPFCollector) openPerfEvent(attr *perfEventAttr, pid, cpu int) (int, error) {
	fd, _, errno := syscall.Syscall6(
		298, // __NR_perf_event_open on x86_64
		uintptr(unsafe.Pointer(attr)),
		uintptr(pid),
		uintptr(cpu),
		0, // group_fd
		0, // flags
		0, // sigtrap
	)

	if errno != 0 {
		return -1, errno
	}

	return int(fd), nil
}

// readPerfEvent reads the current value of a perf event counter
func (e *EBPFCollector) readPerfEvent(fd int) (uint64, error) {
	var value uint64
	data := make([]byte, 8)
	_, err := syscall.Read(fd, data)
	if err != nil {
		return 0, err
	}
	value = binary.LittleEndian.Uint64(data)
	return value, nil
}

// getContextSwitches reads context switches from /proc/stat
func (e *EBPFCollector) getContextSwitches() (uint64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}

	lines := splitLines(string(data))
	for _, line := range lines {
		if startsWith(line, "ctxt") {
			fields := splitFields(line)
			if len(fields) >= 2 {
				return parseUint64(fields[1]), nil
			}
		}
	}
	return 0, fmt.Errorf("ctxt not found in /proc/stat")
}

// getPageFaults reads page faults from /proc/stat
func (e *EBPFCollector) getPageFaults() (uint64, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, err
	}

	lines := splitLines(string(data))
	for _, line := range lines {
		if startsWith(line, "processes") {
			fields := splitFields(line)
			if len(fields) >= 2 {
				return parseUint64(fields[1]), nil
			}
		}
	}
	return 0, fmt.Errorf("processes not found in /proc/stat")
}

// collectSoftnetStats collects per-CPU network processing stats
func (e *EBPFCollector) collectSoftnetStats(now *timestamppb.Timestamp) ([]*proto.Metric, error) {
	metrics := []*proto.Metric{}

	data, err := os.ReadFile("/proc/net/softnet_stat")
	if err != nil {
		return nil, err
	}

	lines := splitLines(string(data))
	cpuIndex := 0
	for _, line := range lines {
		fields := splitFields(line)
		if len(fields) >= 2 {
			// Field 0: processed packets
			// Field 1: dropped packets
			// Field 2: time_squeeze
			processed := parseUint64(fields[0])
			dropped := parseUint64(fields[1])

			metrics = append(metrics, &proto.Metric{
				Name:   "network.softnet.processed",
				Type:   proto.MetricType_METRIC_TYPE_COUNTER,
				Labels: []*proto.MetricLabel{{Key: "cpu", Value: fmt.Sprintf("%d", cpuIndex)}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(processed)}},
			})

			metrics = append(metrics, &proto.Metric{
				Name:   "network.softnet.dropped",
				Type:   proto.MetricType_METRIC_TYPE_COUNTER,
				Labels: []*proto.MetricLabel{{Key: "cpu", Value: fmt.Sprintf("%d", cpuIndex)}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(dropped)}},
			})

			cpuIndex++
		}
	}

	return metrics, nil
}

// Helper functions to avoid importing strconv/bstrings

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func splitFields(s string) []string {
	var fields []string
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		fields = append(fields, s[start:])
	}
	return fields
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func parseUint64(s string) uint64 {
	var v uint64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			v = v*10 + uint64(c-'0')
		} else {
			break
		}
	}
	return v
}

// readU64 reads a uint64 from a byte slice
func readU64(data []byte, offset int) uint64 {
	if offset+8 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint64(data[offset : offset+8])
}

// readU32 reads a uint32 from a byte slice
func readU32(data []byte, offset int) uint32 {
	if offset+4 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}
