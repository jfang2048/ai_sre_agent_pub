package linux

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

// PerfCollector collects hardware performance counters using perf events
type PerfCollector struct {
	config    *PerfConfig
	logger    *zap.Logger
	metricsCh chan<- Metric

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// perf_event_attr struct for syscall
	// struct perf_event_attr {
	//     Type      uint32
	//     Size      uint32
	//     Config    uint64
	//     Sample    uint64
	//     Sample_Or_ID uint64
	//     Read_Format uint64
	//     Flags     uint64
	// }

	// perf file descriptors
	cpuCyclesFd           int
	cpuInstructionsFd     int
	cacheRefFd            int
	cacheMissFd           int
	branchInstrsFd        int
	branchMissesFd        int
	busCyclesFd           int
	stalledCyclesFrontend int
	stalledCyclesBackend  int
	refCyclesFd           int

	// Buffer for perf event reads
	perfBufferSize int
	perfBuffer     []byte

	// Enable/disable for specific events
	enabledFeatures map[string]bool

	mu      sync.RWMutex
	running bool
}

// PerfConfig configures perf event collection
type PerfConfig struct {
	Enabled        bool          `yaml:"enabled"`
	SampleInterval time.Duration `yaml:"sample_interval"`
	EnableCPU      bool          `yaml:"enable_cpu"`
	EnableCache    bool          `yaml:"enable_cache"`
	EnableBranch   bool          `yaml:"enable_branch"`
	EnableBus      bool          `yaml:"enable_bus"`
	EnableStalled  bool          `yaml:"enable_stalled"`
	SamplePeriod   uint32        `yaml:"sample_period"`
}

// CPUMetrics represents CPU performance metrics
type CPUMetrics struct {
	Timestamp             time.Time `json:"timestamp"`
	CPU                   int       `json:"cpu"`
	Cycles                uint64    `json:"cycles"`
	Instructions          uint64    `json:"instructions"`
	CacheRefs             uint64    `json:"cache_references"`
	CacheMisses           uint64    `json:"cache_misses"`
	Branches              uint64    `json:"branches"`
	BranchMisses          uint64    `json:"branch_misses"`
	BusCycles             uint64    `json:"bus_cycles"`
	StalledCyclesFrontend uint64    `json:"stalled_cycles_frontend"`
	StalledCyclesBackend  uint64    `json:"stalled_cycles_backend"`
	RefCycles             uint64    `json:"ref_cycles"`

	// Calculated metrics
	IPC             float64 `json:"ipc"` // Instructions Per Cycle
	CacheMissRate   float64 `json:"cache_miss_rate"`
	BranchMissRate  float64 `json:"branch_miss_rate"`
	CPUCyclesPerSec float64 `json:"cpu_cycles_per_sec"`
}

const (
	perfTypeHardware   = 0
	perfTypeSoftware   = 1
	perfTracepoint     = 2
	perfTypeBreakpoint = 5

	perfSize64      = 1 << 16
	perfFormatID    = 1 << 3
	perfFormatGroup = 1 << 4

	perfCount = 0
	perfRead  = 1 << 2
	// Disabled (for userspace counting)
	perfDisabled = 1 << 1

	perfAttrEnable       = 1 << 0
	perfAttrInherit      = 1 << 3
	perfAttrExcludeGuest = 1 << 4
	perfAttrExcludeUser  = 1 << 15
	archX86_64           = 4

	perfHWCacheOps  = 0 << (0 * 8)
	perfHWCacheL1D  = 0 << (0*8 + 0)
	perfHWCacheL1I  = 0 << (0*8 + 1)
	perfHWCacheLL   = 0 << (0*8 + 2)
	perfHWCacheDTLB = 0 << (0*8 + 3)
	perfHWCacheBPS  = 0 << (0*8 + 4)
	perfHWCacheNode = 0 << (0*8 + 5)

	// Cache event masks
	perfCacheOpRead        = 1 << 0
	perfCacheOpPrefetch    = 1 << 2
	perfCacheOpSpeculative = 1 << 3

	// perf_event_open return values
	perfSuccess           = 0
	perfErrorNoAccess     = -1
	perfErrorInvalidParam = -2
	perfErrorNotSupported = -4
)

// perfAttr represents perf_event_attr struct
type perfAttr struct {
	Type   uint32
	Size   uint32
	Config uint64
	Sample uint64
	Flags  uint64
}

// perfFileID represents a perf event file identifier
type perfFileID int

// NewPerfCollector creates a new perf collector
func NewPerfCollector(config *PerfConfig, logger *zap.Logger, metricsCh chan<- Metric) (*PerfCollector, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("perf collector is disabled")
	}

	if runtime.GOOS != "linux" && runtime.GOARCH != "amd64" {
		return nil, fmt.Errorf("perf collector requires linux/amd64")
	}

	pc := &PerfCollector{
		config:          config,
		logger:          logger.With(zap.String("collector", "perf")),
		metricsCh:       metricsCh,
		perfBufferSize:  128, // Page size
		enabledFeatures: make(map[string]bool),
		running:         false,
	}

	// Check perf_event_open availability (access to /proc/sys/kernel/perf_event_paranoid)
	data, err := os.ReadFile("/proc/sys/kernel/perf_event_paranoid")
	if err == nil && len(data) > 0 {
		value := strings.TrimSpace(string(data))
		if value == "1" || value == "2" {
			pc.logger.Warn("perf_event_paranoid is enabled, may need elevated permissions")
		}
	}

	return pc, nil
}

// Open opens a perf event file for a specific counter
func (pc *PerfCollector) Open(eventType uint32, eventConfig uint64) (int, error) {
	attr := perfAttr{
		Type:   perfTypeHardware,
		Size:   uint32(unsafe.Sizeof(perfAttr{})),
		Config: eventConfig | perfSize64 | perfFormatGroup | perfFormatID,
		Flags:  0,
	}

	// Disable inherit for kernel events
	attr.Flags |= perfDisabled | perfAttrInherit | perfAttrExcludeGuest | perfAttrExcludeUser

	// Set up CPU to monitor all CPUs
	attr.Config |= (1 << 16) // Any CPU (or bit mask)

	return pc.perfEventOpen(attr)
}

// perfEventOpen calls perf_event_open syscall
func (pc *PerfCollector) perfEventOpen(attr perfAttr) (int, error) {
	fd, _, errno := syscall.Syscall6(
		298, // __NR_perf_event_open
		uintptr(unsafe.Pointer(&attr)),
		0,           // pid (0 = current task)
		^uintptr(0), // cpu (-1 = any CPU)
		uintptr(unsafe.Pointer(&attr.Size)),
		0, // flags
		0, // signal file descriptor
	)

	if errno != 0 {
		return -1, fmt.Errorf("perf_event_open failed: %d", errno)
	}

	return int(fd), nil
}

// ReadCounter reads a perf counter value
func (pc *PerfCollector) ReadCounter(fd int) (uint64, error) {
	var value uint64
	// Use raw syscall for read to get exact bytes
	_, err := unix.Read(fd, unsafe.Slice((*byte)(unsafe.Pointer(&value)), int(unsafe.Sizeof(value))))
	if err != nil {
		return 0, fmt.Errorf("failed to read perf counter: %w", err)
	}
	return value, nil
}

// ResetCounter resets a perf counter
func (pc *PerfCollector) ResetCounter(fd int) error {
	// ioctl PERF_EVENT_IOC_RESET
	_, _, errno := unix.Syscall6(
		294, // __NR_ioctl
		uintptr(fd),
		0x2400, // PERF_EVENT_IOC_RESET
		0,
		0,
		0,
		0,
	)
	if errno != 0 {
		return fmt.Errorf("failed to reset perf counter: %d", errno)
	}
	return nil
}

// Start initializes all perf counters
func (pc *PerfCollector) Start() error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.running {
		return nil
	}

	// Open CPU cycles counter
	if pc.config.EnableCPU {
		fd, err := pc.Open(0, 0) // PERF_TYPE_HARDWARE, PERF_COUNT_HW_CPU_CYCLES
		if err != nil {
			return fmt.Errorf("failed to open CPU cycles counter: %w", err)
		}
		pc.cpuCyclesFd = fd

		// Open instructions counter
		fd, err = pc.Open(0, 0) // PERF_TYPE_HARDWARE, PERF_COUNT_HW_INSTRUCTIONS
		if err != nil {
			pc.logger.Warn("failed to open instructions counter", zap.Error(err))
		}
		pc.cpuInstructionsFd = fd

		// Open reference cycles counter
		fd, err = pc.Open(0, 0) // PERF_TYPE_HARDWARE, PERF_COUNT_HW_REF_CPU_CYCLES
		if err != nil {
			pc.logger.Warn("failed to open ref cycles counter", zap.Error(err))
		}
		pc.refCyclesFd = fd
	}

	// Open cache counters
	if pc.config.EnableCache {
		// Cache references
		fd, err := pc.Open(0, perfHWCacheOps)
		if err != nil {
			return fmt.Errorf("failed to open cache ref counter: %w", err)
		}
		pc.cacheRefFd = fd

		// Cache misses
		fd, err = pc.Open(0, perfHWCacheOps|0x1) // CACHE_OP_READ + READ
		if err != nil {
			pc.logger.Warn("failed to open cache miss counter", zap.Error(err))
		}
		pc.cacheMissFd = fd
	}

	// Open branch counters
	if pc.config.EnableBranch {
		// Branch instructions
		fd, err := pc.Open(0, 4) // PERF_COUNT_HW_BRANCH_INSTRUCTIONS
		if err != nil {
			return fmt.Errorf("failed to open branch counter: %w", err)
		}
		pc.branchInstrsFd = fd

		// Branch misses
		fd, err = pc.Open(0, 5) // PERF_COUNT_HW_BRANCH_MISSES
		if err != nil {
			pc.logger.Warn("failed to open branch miss counter", zap.Error(err))
		}
		pc.branchMissesFd = fd
	}

	// Open bus cycle counter
	if pc.config.EnableBus {
		fd, err := pc.Open(0, 6) // PERF_COUNT_HW_BUS_CYCLES
		if err != nil {
			pc.logger.Warn("failed to open bus cycle counter", zap.Error(err))
		}
		pc.busCyclesFd = fd

		// Stalled cycles frontend
		fd, err = pc.Open(0, 7) // PERF_COUNT_HW_STALLED_CYCLES_FRONTEND
		if err != nil {
			pc.logger.Warn("failed to open stalled frontend counter", zap.Error(err))
		}
		pc.stalledCyclesFrontend = fd

		// Stalled cycles backend
		fd, err = pc.Open(0, 8) // PERF_COUNT_HW_STALLED_CYCLES_BACKEND
		if err != nil {
			pc.logger.Warn("failed to open stalled backend counter", zap.Error(err))
		}
		pc.stalledCyclesBackend = fd
	}

	// Reset all counters
	perfFds := []int{
		pc.cpuCyclesFd, pc.cpuInstructionsFd, pc.cacheRefFd, pc.cacheMissFd,
		pc.branchInstrsFd, pc.branchMissesFd, pc.busCyclesFd,
		pc.stalledCyclesFrontend, pc.stalledCyclesBackend, pc.refCyclesFd,
	}

	for _, fd := range perfFds {
		if fd > 0 {
			pc.ResetCounter(fd)
		}
	}

	pc.running = true
	pc.logger.Info("perf collector started")

	return nil
}

// Stop closes all perf file descriptors
func (pc *PerfCollector) Stop() error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if !pc.running {
		return nil
	}

	if pc.cancel != nil {
		pc.cancel()
	}

	pc.wg.Wait()

	perfFds := []int{
		pc.cpuCyclesFd, pc.cpuInstructionsFd, pc.cacheRefFd, pc.cacheMissFd,
		pc.branchInstrsFd, pc.branchMissesFd, pc.busCyclesFd,
		pc.stalledCyclesFrontend, pc.stalledCyclesBackend, pc.refCyclesFd,
	}

	for _, fd := range perfFds {
		if fd > 0 {
			unix.Close(fd)
		}
	}

	// Reset all to 0
	pc.cpuCyclesFd = 0
	pc.cpuInstructionsFd = 0
	pc.cacheRefFd = 0
	pc.cacheMissFd = 0
	pc.branchInstrsFd = 0
	pc.branchMissesFd = 0
	pc.busCyclesFd = 0
	pc.stalledCyclesFrontend = 0
	pc.stalledCyclesBackend = 0
	pc.refCyclesFd = 0

	pc.running = false
	return nil
}

// Collect collects current perf metrics
func (pc *PerfCollector) Collect() (*CPUMetrics, error) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	if !pc.running {
		return nil, fmt.Errorf("perf collector not running")
	}

	metrics := &CPUMetrics{
		Timestamp: time.Now(),
	}

	// Read all counters
	if pc.cpuCyclesFd > 0 {
		metrics.Cycles, _ = pc.ReadCounter(pc.cpuCyclesFd)
	}
	if pc.cpuInstructionsFd > 0 {
		metrics.Instructions, _ = pc.ReadCounter(pc.cpuInstructionsFd)
	}
	if pc.cacheRefFd > 0 {
		metrics.CacheRefs, _ = pc.ReadCounter(pc.cacheRefFd)
	}
	if pc.cacheMissFd > 0 {
		metrics.CacheMisses, _ = pc.ReadCounter(pc.cacheMissFd)
	}
	if pc.branchInstrsFd > 0 {
		metrics.Branches, _ = pc.ReadCounter(pc.branchInstrsFd)
	}
	if pc.branchMissesFd > 0 {
		metrics.BranchMisses, _ = pc.ReadCounter(pc.branchMissesFd)
	}
	if pc.busCyclesFd > 0 {
		metrics.BusCycles, _ = pc.ReadCounter(pc.busCyclesFd)
	}
	if pc.stalledCyclesFrontend > 0 {
		metrics.StalledCyclesFrontend, _ = pc.ReadCounter(pc.stalledCyclesFrontend)
	}
	if pc.stalledCyclesBackend > 0 {
		metrics.StalledCyclesBackend, _ = pc.ReadCounter(pc.stalledCyclesBackend)
	}
	if pc.refCyclesFd > 0 {
		metrics.RefCycles, _ = pc.ReadCounter(pc.refCyclesFd)
	}

	// Calculate derived metrics
	if metrics.Instructions > 0 && metrics.Cycles > 0 {
		metrics.IPC = float64(metrics.Instructions) / float64(metrics.Cycles)
	}
	if metrics.CacheRefs > 0 {
		metrics.CacheMissRate = float64(metrics.CacheMisses) / float64(metrics.CacheRefs)
	}
	if metrics.Branches > 0 {
		metrics.BranchMissRate = float64(metrics.BranchMisses) / float64(metrics.Branches)
	}

	return metrics, nil
}

// Name returns the collector name
func (pc *PerfCollector) Name() string {
	return "perf"
}

// StartWithCtx starts the perf collector with context
func (pc *PerfCollector) StartWithCtx(ctx context.Context) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.running {
		return nil
	}

	ctx, pc.cancel = context.WithCancel(ctx)

	// Initialize perf counters
	if err := pc.Start(); err != nil {
		return err
	}

	// Start periodic collection
	pc.wg.Add(1)
	go pc.collectLoop(ctx)

	return nil
}

// collectLoop runs the periodic collection loop
func (pc *PerfCollector) collectLoop(ctx context.Context) {
	defer pc.wg.Done()

	ticker := time.NewTicker(pc.config.SampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if metrics, err := pc.Collect(); err == nil {
				pc.emitMetrics(metrics)
			}
		}
	}
}

// emitMetrics emits perf metrics to the metrics channel
func (pc *PerfCollector) emitMetrics(metrics *CPUMetrics) {
	labels := map[string]string{"cpu": "all"}

	// Raw counter metrics
	pc.emitMetric("perf_cpu_cycles", float64(metrics.Cycles), "counter", labels)
	pc.emitMetric("perf_instructions", float64(metrics.Instructions), "counter", labels)
	pc.emitMetric("perf_cache_references", float64(metrics.CacheRefs), "counter", labels)
	pc.emitMetric("perf_cache_misses", float64(metrics.CacheMisses), "counter", labels)
	pc.emitMetric("perf_branch_instructions", float64(metrics.Branches), "counter", labels)
	pc.emitMetric("perf_branch_misses", float64(metrics.BranchMisses), "counter", labels)
	pc.emitMetric("perf_bus_cycles", float64(metrics.BusCycles), "counter", labels)
	pc.emitMetric("perf_stalled_cycles_frontend", float64(metrics.StalledCyclesFrontend), "counter", labels)
	pc.emitMetric("perf_stalled_cycles_backend", float64(metrics.StalledCyclesBackend), "counter", labels)
	pc.emitMetric("perf_ref_cycles", float64(metrics.RefCycles), "counter", labels)

	// Derived metrics
	pc.emitMetric("perf_ipc", metrics.IPC, "gauge", labels)
	pc.emitMetric("perf_cache_miss_rate", metrics.CacheMissRate, "gauge", labels)
	pc.emitMetric("perf_branch_miss_rate", metrics.BranchMissRate, "gauge", labels)
	pc.emitMetric("perf_cpu_cycles_per_sec", metrics.CPUCyclesPerSec, "gauge", labels)
}

// emitMetric emits a single metric
func (pc *PerfCollector) emitMetric(name string, value float64, metricType string, labels map[string]string) {
	if pc.metricsCh == nil {
		return
	}

	select {
	case pc.metricsCh <- Metric{
		Name:      name,
		Type:      metricType,
		Value:     value,
		Timestamp: time.Now(),
		Labels:    labels,
		Source:    "perf",
	}:
	default:
		pc.logger.Warn("perf metrics channel full, dropping metric",
			zap.String("name", name))
	}
}

// Metric represents a perf metric (local type for linux package)
type Metric struct {
	Name      string
	Type      string
	Value     float64
	Timestamp time.Time
	Labels    map[string]string
	Source    string
}
