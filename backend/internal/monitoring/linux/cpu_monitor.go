package linux

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// CPUMonitor monitors CPU performance with Linux-specific metrics
type CPUMonitor struct {
	config    *CPUConfig
	logger    *zap.Logger
	metricsCh chan<- CPUMetric

	mu      sync.RWMutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// Previous values for calculating deltas
	prevStats    map[string]*CPUStat
	prevCpuStats map[int]*PerCpuStat
	lastUpdate   time.Time
}

// CPUConfig configures CPU monitoring
type CPUConfig struct {
	Enabled        bool          `yaml:"enabled"`
	SampleInterval time.Duration `yaml:"sample_interval"`

	// What to collect
	EnablePerCPU     bool `yaml:"enable_per_cpu"`
	EnableLoadAvg    bool `yaml:"enable_load_avg"`
	EnableInterrupts bool `yaml:"enable_interrupts"`
	EnableContext    bool `yaml:"enable_context"`
	EnableSoftIRQ    bool `yaml:"enable_softirq"`
	EnableFrequency  bool `yaml:"enable_frequency"`
}

// CPUMetric represents a CPU performance metric
type CPUMetric struct {
	Timestamp time.Time         `json:"timestamp"`
	Type      string            `json:"type"`
	CPU       int               `json:"cpu,omitempty"` // -1 for aggregate
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Unit      string            `json:"unit,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// CPUStat holds aggregate CPU statistics from /proc/stat
type CPUStat struct {
	User      float64 // Normal processes executing in user mode
	Nice      float64 // Niced processes executing in user mode
	System    float64 // Processes executing in kernel mode
	Idle      float64 // Idle time
	Iowait    float64 // Waiting for I/O to complete
	Irq       float64 // Servicing interrupts
	Softirq   float64 // Servicing softirqs
	Steal     float64 // Stolen time (running in VM)
	Guest     float64 // Time spent running a virtual CPU
	GuestNice float64 // Time spent running a niced virtual CPU

	// Calculated metrics
	Total float64
	Usage float64
}

// PerCpuStat holds per-CPU statistics
type PerCpuStat struct {
	CPU       int
	User      float64
	Nice      float64
	System    float64
	Idle      float64
	Iowait    float64
	Irq       float64
	Softirq   float64
	Steal     float64
	Guest     float64
	GuestNice float64

	Total float64
	Usage float64
}

// LoadAvg holds load average values
type LoadAvg struct {
	Load1   float64
	Load5   float64
	Load15  float64
	Running int // Number of currently running processes
	Total   int // Total number of processes
	LastPID int // Last PID created
}

// InterruptStats holds interrupt statistics
type InterruptStats struct {
	IRQs  map[string]uint64 // per-IRQ counts
	Total uint64
}

// SoftIRQStats holds softirq statistics
type SoftIRQStats struct {
	Hi      uint64 // High priority tasklets
	Timer   uint64 // Timer softirqs
	NetTx   uint64 // Network transmit
	NetRx   uint64 // Network receive
	Block   uint64 // Block device I/O
	BlockIO uint64
	IRQPoll uint64
	Tasklet uint64
	Sched   uint64
	HRTimer uint64
	RCU     uint64
	Total   uint64
}

// CPUFrequency holds CPU frequency information
type CPUFrequency struct {
	CPU             int
	Current         uint64 // kHz
	Min             uint64
	Max             uint64
	ScalingGovernor string
}

// NewCPUMonitor creates a new CPU monitor
func NewCPUMonitor(config *CPUConfig, logger *zap.Logger, metricsCh chan<- CPUMetric) (*CPUMonitor, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("CPU monitor is disabled")
	}

	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("CPU monitor requires Linux")
	}

	return &CPUMonitor{
		config:       config,
		logger:       logger.With(zap.String("collector", "cpu_monitor")),
		metricsCh:    metricsCh,
		prevStats:    make(map[string]*CPUStat),
		prevCpuStats: make(map[int]*PerCpuStat),
		lastUpdate:   time.Now(),
		running:      false,
	}, nil
}

// Start starts the CPU monitor
func (cm *CPUMonitor) Start(ctx context.Context) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.running {
		return nil
	}

	ctx, cm.cancel = context.WithCancel(ctx)

	// Initial collection
	cm.collectAll()

	cm.wg.Add(1)
	go cm.collectLoop(ctx)

	cm.running = true
	cm.logger.Info("CPU monitor started")

	return nil
}

// Stop stops the CPU monitor
func (cm *CPUMonitor) Stop() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if !cm.running {
		return nil
	}

	if cm.cancel != nil {
		cm.cancel()
	}

	cm.wg.Wait()
	cm.running = false
	cm.logger.Info("CPU monitor stopped")

	return nil
}

// collectLoop runs the periodic collection loop
func (cm *CPUMonitor) collectLoop(ctx context.Context) {
	defer cm.wg.Done()

	ticker := time.NewTicker(cm.config.SampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cm.collectAll()
		}
	}
}

// collectAll collects all enabled CPU metrics
func (cm *CPUMonitor) collectAll() {
	// Always collect aggregate CPU stats
	if stat := cm.collectCPUStat(); stat != nil {
		cm.emitCPUMetrics(stat)
	}

	if cm.config.EnablePerCPU {
		cm.collectPerCPUStats()
	}

	if cm.config.EnableLoadAvg {
		cm.collectLoadAvg()
	}

	if cm.config.EnableInterrupts {
		cm.collectInterrupts()
	}

	if cm.config.EnableSoftIRQ {
		cm.collectSoftIRQs()
	}

	if cm.config.EnableFrequency {
		cm.collectFrequency()
	}

	// Collect context switches from /proc/stat
	if cm.config.EnableContext {
		cm.collectContextSwitches()
	}

	cm.lastUpdate = time.Now()
}

// collectCPUStat reads aggregate CPU stats from /proc/stat
func (cm *CPUMonitor) collectCPUStat() *CPUStat {
	file, err := os.Open("/proc/stat")
	if err != nil {
		cm.logger.Error("failed to open /proc/stat", zap.Error(err))
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				continue
			}

			stat := &CPUStat{}
			stat.User = parseFloat(fields[1])
			stat.Nice = parseFloat(fields[2])
			stat.System = parseFloat(fields[3])
			stat.Idle = parseFloat(fields[4])
			stat.Iowait = parseFloat(fields[5])
			stat.Irq = parseFloat(fields[6])
			stat.Softirq = parseFloat(fields[7])

			if len(fields) > 8 {
				stat.Steal = parseFloat(fields[8])
			}
			if len(fields) > 9 {
				stat.Guest = parseFloat(fields[9])
			}
			if len(fields) > 10 {
				stat.GuestNice = parseFloat(fields[10])
			}

			// Calculate total and usage
			stat.Total = stat.User + stat.Nice + stat.System + stat.Idle +
				stat.Iowait + stat.Irq + stat.Softirq + stat.Steal +
				stat.Guest + stat.GuestNice

			// Calculate usage delta from previous
			if prev, ok := cm.prevStats["aggregate"]; ok {
				totalDelta := stat.Total - prev.Total
				if totalDelta > 0 {
					idleDelta := stat.Idle - prev.Idle
					stat.Usage = (totalDelta - idleDelta) / totalDelta * 100
				}
			}

			cm.prevStats["aggregate"] = stat
			return stat
		}
	}

	return nil
}

// collectPerCPUStats reads per-CPU stats from /proc/stat
func (cm *CPUMonitor) collectPerCPUStats() {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu") && !strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 8 {
				continue
			}

			// Parse CPU number
			cpuNum := parseInt(fields[0][3:]) // strip "cpu" prefix

			stat := &PerCpuStat{
				CPU:     cpuNum,
				User:    parseFloat(fields[1]),
				Nice:    parseFloat(fields[2]),
				System:  parseFloat(fields[3]),
				Idle:    parseFloat(fields[4]),
				Iowait:  parseFloat(fields[5]),
				Irq:     parseFloat(fields[6]),
				Softirq: parseFloat(fields[7]),
			}

			if len(fields) > 8 {
				stat.Steal = parseFloat(fields[8])
			}
			if len(fields) > 9 {
				stat.Guest = parseFloat(fields[9])
			}

			stat.Total = stat.User + stat.Nice + stat.System + stat.Idle +
				stat.Iowait + stat.Irq + stat.Softirq + stat.Steal + stat.Guest

			// Calculate usage delta
			if prev, ok := cm.prevCpuStats[cpuNum]; ok {
				totalDelta := stat.Total - prev.Total
				if totalDelta > 0 {
					idleDelta := stat.Idle - prev.Idle
					stat.Usage = (totalDelta - idleDelta) / totalDelta * 100
				}
			}

			cm.prevCpuStats[cpuNum] = stat
			cm.emitPerCPUMetrics(stat)
		}
	}
}

// collectLoadAvg reads load average from /proc/loadavg
func (cm *CPUMonitor) collectLoadAvg() {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}

	fields := strings.Fields(string(data))
	if len(fields) < 5 {
		return
	}

	load1 := parseFloat(fields[0])
	load5 := parseFloat(fields[1])
	load15 := parseFloat(fields[2])

	// Parse "running/total" format
	runTotal := strings.Split(fields[3], "/")
	running := parseInt(runTotal[0])
	total := parseInt(runTotal[1])

	lastPID := parseInt(fields[4])

	cm.emitMetric("load_average_1m", load1, "", map[string]string{"type": "1m"})
	cm.emitMetric("load_average_5m", load5, "", map[string]string{"type": "5m"})
	cm.emitMetric("load_average_15m", load15, "", map[string]string{"type": "15m"})
	cm.emitMetric("processes_running", float64(running), "", nil)
	cm.emitMetric("processes_total", float64(total), "", nil)
	cm.emitMetric("last_pid", float64(lastPID), "", nil)

	// Calculate per-CPU normalized load
	numCPU := runtime.NumCPU()
	if numCPU > 0 {
		cm.emitMetric("load_per_cpu", load1/float64(numCPU), "", nil)
	}
}

// collectInterrupts reads interrupt stats from /proc/interrupts
func (cm *CPUMonitor) collectInterrupts() {
	file, err := os.Open("/proc/interrupts")
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	irqCounts := make(map[string]uint64)
	var totalIRQs uint64

	// Skip header line
	if scanner.Scan() {
		// Header has CPU names
	}

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		// First field is IRQ number or name
		irqName := fields[0]
		var sum uint64

		// Sum counts across all CPUs
		for i := 1; i < len(fields); i++ {
			if fields[i] == "" || strings.Contains(fields[i], ":") {
				continue
			}
			val := parseUint64(fields[i])
			sum += val
		}

		irqCounts[irqName] = sum
		totalIRQs += sum

		// Emit per-IRQ metrics for common IRQs
		if strings.Contains(irqName, "IO-APIC") ||
			strings.Contains(irqName, "PCI") ||
			strings.Contains(irqName, "eth") ||
			strings.Contains(irqName, "xhci") ||
			strings.Contains(irqName, "ahci") {
			labels := map[string]string{"irq": irqName}
			cm.emitMetric("interrupts_total", float64(sum), "", labels)
		}
	}

	cm.emitMetric("interrupts_all", float64(totalIRQs), "", nil)
}

// collectSoftIRQs reads softirq stats from /proc/softirqs
func (cm *CPUMonitor) collectSoftIRQs() {
	file, err := os.Open("/proc/softirqs")
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// First line has CPU count
	if !scanner.Scan() {
		return
	}

	// Known softirq types: HI, TIMER, NET_TX, NET_RX, BLOCK, IRQ_POLL, TASKLET, SCHED, HRTIMER, RCU
	stats := make(map[string]uint64)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		irqType := strings.TrimSuffix(fields[0], ":")
		var sum uint64

		// Sum across all CPUs
		for i := 1; i < len(fields); i++ {
			sum += parseUint64(fields[i])
		}

		stats[irqType] = sum

		labels := map[string]string{"type": strings.ToLower(irqType)}
		cm.emitMetric("softirq_total", float64(sum), "", labels)
	}
}

// collectContextSwitches reads context switch count from /proc/stat
func (cm *CPUMonitor) collectContextSwitches() {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ctxt") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				count := parseUint64(fields[1])
				cm.emitMetric("context_switches", float64(count), "counter", nil)

				// Calculate rate if we have previous data
				if prev, ok := cm.prevStats["ctxt"]; ok {
					delta := count - uint64(prev.Total)
					elapsed := time.Since(cm.lastUpdate).Seconds()
					if elapsed > 0 {
						rate := float64(delta) / elapsed
						cm.emitMetric("context_switches_rate", rate, "switches/sec", nil)
					}
					prev.Total = float64(count)
				} else {
					cm.prevStats["ctxt"] = &CPUStat{Total: float64(count)}
				}
			}
			break
		}
	}
}

// collectFrequency reads CPU frequency info from /sys/devices/system/cpu
func (cm *CPUMonitor) collectFrequency() {
	// Try to read from cpufreq subsystem
	cpuDirs, err := os.ReadDir("/sys/devices/system/cpu")
	if err != nil {
		return
	}

	for _, entry := range cpuDirs {
		if !strings.HasPrefix(entry.Name(), "cpu") {
			continue
		}

		cpuNumStr := strings.TrimPrefix(entry.Name(), "cpu")
		if cpuNumStr == "" {
			continue // Skip "cpu" directory (not a CPU number)
		}

		cpuNum := parseInt(cpuNumStr)
		if cpuNum < 0 {
			continue
		}

		cpuPath := fmt.Sprintf("/sys/devices/system/cpu/%s", entry.Name())

		// Read current frequency
		if curFreq, err := readFileUint64(cpuPath + "/cpufreq/scaling_cur_freq"); err == nil {
			labels := map[string]string{"cpu": cpuNumStr}
			cm.emitMetric("cpu_frequency_khz", float64(curFreq), "kHz", labels)
		}

		// Read min/max
		if minFreq, err := readFileUint64(cpuPath + "/cpufreq/scaling_min_freq"); err == nil {
			labels := map[string]string{"cpu": cpuNumStr, "limit": "min"}
			cm.emitMetric("cpu_frequency_limit_khz", float64(minFreq), "kHz", labels)
		}
		if maxFreq, err := readFileUint64(cpuPath + "/cpufreq/scaling_max_freq"); err == nil {
			labels := map[string]string{"cpu": cpuNumStr, "limit": "max"}
			cm.emitMetric("cpu_frequency_limit_khz", float64(maxFreq), "kHz", labels)
		}

		// Read governor
		if gov, err := os.ReadFile(cpuPath + "/cpufreq/scaling_governor"); err == nil {
			labels := map[string]string{
				"cpu":      cpuNumStr,
				"governor": strings.TrimSpace(string(gov)),
			}
			cm.emitMetric("cpu_governor", 1, "", labels)
		}
	}
}

// emitMetric emits a single CPU metric
func (cm *CPUMonitor) emitMetric(name string, value float64, unit string, labels map[string]string) {
	if cm.metricsCh == nil {
		return
	}

	metric := CPUMetric{
		Timestamp: time.Now(),
		Name:      name,
		Value:     value,
		Unit:      unit,
		Labels:    labels,
	}

	select {
	case cm.metricsCh <- metric:
	default:
		cm.logger.Warn("CPU metrics channel full, dropping metric",
			zap.String("name", name))
	}
}

// emitCPUMetrics emits aggregate CPU metrics
func (cm *CPUMonitor) emitCPUMetrics(stat *CPUStat) {
	labels := map[string]string{"cpu": "all"}

	cm.emitMetric("cpu_user", stat.User, "jiffies", labels)
	cm.emitMetric("cpu_nice", stat.Nice, "jiffies", labels)
	cm.emitMetric("cpu_system", stat.System, "jiffies", labels)
	cm.emitMetric("cpu_idle", stat.Idle, "jiffies", labels)
	cm.emitMetric("cpu_iowait", stat.Iowait, "jiffies", labels)
	cm.emitMetric("cpu_irq", stat.Irq, "jiffies", labels)
	cm.emitMetric("cpu_softirq", stat.Softirq, "jiffies", labels)
	cm.emitMetric("cpu_stolen", stat.Steal, "jiffies", labels)
	cm.emitMetric("cpu_usage_percent", stat.Usage, "%", labels)
}

// emitPerCPUMetrics emits per-CPU metrics
func (cm *CPUMonitor) emitPerCPUMetrics(stat *PerCpuStat) {
	labels := map[string]string{"cpu": strconv.Itoa(stat.CPU)}

	cm.emitMetric("cpu_user", stat.User, "jiffies", labels)
	cm.emitMetric("cpu_system", stat.System, "jiffies", labels)
	cm.emitMetric("cpu_idle", stat.Idle, "jiffies", labels)
	cm.emitMetric("cpu_usage_percent", stat.Usage, "%", labels)
}

// GetStats returns current CPU statistics
func (cm *CPUMonitor) GetStats() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	stats := make(map[string]interface{})
	stats["running"] = cm.running
	stats["num_cpu"] = runtime.NumCPU()
	stats["last_update"] = cm.lastUpdate

	if stat, ok := cm.prevStats["aggregate"]; ok {
		stats["aggregate_usage"] = stat.Usage
	}

	return stats
}

// Helper functions
func parseFloat(s string) float64 {
	val, _ := strconv.ParseFloat(s, 64)
	return val
}

func parseInt(s string) int {
	val, _ := strconv.Atoi(s)
	return val
}

func parseUint64(s string) uint64 {
	val, _ := strconv.ParseUint(s, 10, 64)
	return val
}

func readFileUint64(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}
