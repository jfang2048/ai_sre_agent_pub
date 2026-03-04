package sources

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ProcessSource collects metrics about running processes
type ProcessSource struct {
	BaseSource
	config ProcessConfig
	logger *zap.Logger

	// Cached process data
	mu             sync.RWMutex
	prevCPUTimes   map[int]*ProcessCPUTimes
	prevIOStats    map[int]*ProcessIOStats
	lastSampleTime int64
}

// ProcessCPUTimes holds CPU times for a process
type ProcessCPUTimes struct {
	Utime  uint64
	Stime  uint64
	Cutime uint64
	Cstime uint64
}

// ProcessIOStats holds I/O stats for a process
type ProcessIOStats struct {
	ReadBytes  uint64
	WriteBytes uint64
}

// ProcessInfo holds information about a process
type ProcessInfo struct {
	PID        int
	Name       string
	Cmdline    string
	CPUPercent float64
	MemoryKB   uint64
	OpenFiles  int
	ReadBytes  uint64
	WriteBytes uint64
	State      string
	Utime      uint64
	Stime      uint64
	Cutime     uint64
	Cstime     uint64
	Wchan      string
}

// NewProcessSource creates a new process metrics source
func NewProcessSource(config ProcessConfig, logger *zap.Logger) (*ProcessSource, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("process source is disabled")
	}

	return &ProcessSource{
		BaseSource: BaseSource{
			name:    "process",
			enabled: config.Enabled,
		},
		config:         config,
		logger:         logger.With(zap.String("source", "process")),
		prevCPUTimes:   make(map[int]*ProcessCPUTimes),
		prevIOStats:    make(map[int]*ProcessIOStats),
		lastSampleTime: 0,
	}, nil
}

func (p *ProcessSource) Start(ctx context.Context) error {
	p.setStatus(true, true, "")
	p.logger.Info("process source started")
	return nil
}

func (p *ProcessSource) Stop() error {
	p.setStatus(false, false, "")
	p.logger.Info("process source stopped")
	return nil
}

// Collect collects process metrics
func (p *ProcessSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	metrics := []*proto.Metric{}
	now := timestamppb.Now()

	// Get all PIDs
	pids, err := p.getAllPIDs()
	if err != nil {
		p.setStatus(false, false, err.Error())
		return nil, err
	}

	// Collect per-process metrics
	if p.config.EnablePerProcess {
		processes := p.collectProcessMetrics(pids)
		topN := p.config.TopNProcesses
		if topN <= 0 {
			topN = 10
		}

		// Sort by CPU and emit top N
		for i, proc := range processes {
			if i >= topN {
				break
			}
			labels := []*proto.MetricLabel{
				{Key: "pid", Value: strconv.Itoa(proc.PID)},
				{Key: "name", Value: proc.Name},
			}
			metrics = append(metrics, &proto.Metric{
				Name:   "process.cpu.percent",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: labels,
				Points: []*proto.MetricPoint{{Timestamp: now, Value: proc.CPUPercent}},
			})
			metrics = append(metrics, &proto.Metric{
				Name:   "process.memory.bytes",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: labels,
				Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(proc.MemoryKB * 1024)}},
			})
			if p.config.EnableOpenFiles {
				metrics = append(metrics, &proto.Metric{
					Name:   "process.open_files",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(proc.OpenFiles)}},
				})
			}
			if p.config.EnableIO {
				metrics = append(metrics, &proto.Metric{
					Name:   "process.io.read_bytes",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(proc.ReadBytes)}},
				})
				metrics = append(metrics, &proto.Metric{
					Name:   "process.io.write_bytes",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(proc.WriteBytes)}},
				})
			}
			// Emit process info with wchan
			infoLabels := []*proto.MetricLabel{
				{Key: "pid", Value: strconv.Itoa(proc.PID)},
				{Key: "name", Value: proc.Name},
				{Key: "wchan", Value: proc.Wchan},
				{Key: "state", Value: proc.State},
			}
			metrics = append(metrics, &proto.Metric{
				Name:   "process.info",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: infoLabels,
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
	}

	// Collect aggregate metrics
	aggregateMetrics := p.collectAggregateMetrics(pids, now)
	metrics = append(metrics, aggregateMetrics...)

	p.setStatus(true, true, "")

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "process",
		CollectedAt: now,
	}, nil
}

// getAllPIDs returns all running process IDs
func (p *ProcessSource) getAllPIDs() ([]int, error) {
	var pids []int

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}

	return pids, nil
}

// collectProcessMetrics collects detailed metrics for each process
func (p *ProcessSource) collectProcessMetrics(pids []int) []*ProcessInfo {
	processes := make([]*ProcessInfo, 0, len(pids))
	now := timestamppb.Now()
	currentTime := now.GetSeconds()

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, pid := range pids {
		proc, err := p.getProcessInfo(pid)
		if err != nil {
			continue
		}

		// Calculate CPU percentage
		if prev, ok := p.prevCPUTimes[pid]; ok {
			timeDelta := uint64(currentTime - p.lastSampleTime)
			if timeDelta > 0 {
				totalDelta := (proc.Utime + proc.Stime) - (prev.Utime + prev.Stime)
				proc.CPUPercent = float64(totalDelta) / float64(timeDelta) * 100.0
			}
		}

		p.prevCPUTimes[pid] = &ProcessCPUTimes{
			Utime:  proc.Utime,
			Stime:  proc.Stime,
			Cutime: proc.Cutime,
			Cstime: proc.Cstime,
		}

		// Get I/O stats if enabled
		if p.config.EnableIO {
			ioStats, err := p.getProcessIO(pid)
			if err == nil {
				proc.ReadBytes = ioStats.ReadBytes
				proc.WriteBytes = ioStats.WriteBytes

				// Calculate I/O rate
				if prev, ok := p.prevIOStats[pid]; ok {
					timeDelta := currentTime - p.lastSampleTime
					if timeDelta > 0 {
						readRate := (ioStats.ReadBytes - prev.ReadBytes) / uint64(timeDelta)
						writeRate := (ioStats.WriteBytes - prev.WriteBytes) / uint64(timeDelta)
						proc.ReadBytes = readRate
						proc.WriteBytes = writeRate
					}
				}

				p.prevIOStats[pid] = ioStats
			}
		}

		// Get open file count if enabled
		if p.config.EnableOpenFiles {
			proc.OpenFiles, _ = p.getProcessOpenFiles(pid)
		}

		processes = append(processes, proc)
	}

	p.lastSampleTime = currentTime

	// Sort by CPU percentage (descending)
	for i := 0; i < len(processes); i++ {
		for j := i + 1; j < len(processes); j++ {
			if processes[j].CPUPercent > processes[i].CPUPercent {
				processes[i], processes[j] = processes[j], processes[i]
			}
		}
	}

	return processes
}

// getProcessInfo reads process information from /proc/[pid]/stat
func (p *ProcessSource) getProcessInfo(pid int) (*ProcessInfo, error) {
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statData, err := os.ReadFile(statPath)
	if err != nil {
		return nil, err
	}

	// Parse stat (format is complex, simplified parsing)
	fields := strings.Fields(string(statData))
	if len(fields) < 24 {
		return nil, fmt.Errorf("invalid stat format")
	}

	proc := &ProcessInfo{PID: pid}

	// PID is fields[0]
	// Comm is fields[1] with parentheses
	if len(fields[1]) > 2 {
		proc.Name = fields[1][1 : len(fields[1])-1]
	} else {
		proc.Name = fields[1]
	}

	// State is fields[2]
	proc.State = fields[2]

	// Parse CPU times (fields 13-16 in 0-indexed: utime, stime, cutime, cstime)
	proc.Utime, _ = strconv.ParseUint(fields[13], 10, 64)
	proc.Stime, _ = strconv.ParseUint(fields[14], 10, 64)
	proc.Cutime, _ = strconv.ParseUint(fields[15], 10, 64)
	proc.Cstime, _ = strconv.ParseUint(fields[16], 10, 64)

	// Get memory from /proc/[pid]/statm
	statmPath := fmt.Sprintf("/proc/%d/statm", pid)
	statmData, err := os.ReadFile(statmPath)
	if err == nil {
		statmFields := strings.Fields(string(statmData))
		if len(statmFields) > 0 {
			if rss, err := strconv.ParseUint(statmFields[1], 10, 64); err == nil {
				proc.MemoryKB = rss * 4 // RSS is in pages
			}
		}
	}

	// Get cmdline
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	cmdlineData, err := os.ReadFile(cmdlinePath)
	if err == nil {
		proc.Cmdline = strings.ReplaceAll(string(cmdlineData), "\x00", " ")
	}

	// Get wchan (kernel function the process is sleeping in)
	wchanPath := fmt.Sprintf("/proc/%d/wchan", pid)
	wchanData, err := os.ReadFile(wchanPath)
	if err == nil {
		proc.Wchan = string(wchanData)
	} else {
		proc.Wchan = "-"
	}

	return proc, nil
}

// getProcessIO reads I/O statistics from /proc/[pid]/io
func (p *ProcessSource) getProcessIO(pid int) (*ProcessIOStats, error) {
	ioPath := fmt.Sprintf("/proc/%d/io", pid)
	f, err := os.Open(ioPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stats := &ProcessIOStats{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		value, _ := strconv.ParseUint(fields[1], 10, 64)

		switch key {
		case "read_bytes:":
			stats.ReadBytes = value
		case "write_bytes:":
			stats.WriteBytes = value
		}
	}

	return stats, nil
}

// getProcessOpenFiles counts open file descriptors for a process
func (p *ProcessSource) getProcessOpenFiles(pid int) (int, error) {
	fdPath := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdPath)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// collectAggregateMetrics collects aggregate process metrics
func (p *ProcessSource) collectAggregateMetrics(pids []int, now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Count total processes
	totalProcesses := len(pids)
	metrics = append(metrics, createGauge("process.count", float64(totalProcesses), now))

	// Count processes by state
	stateCounts := make(map[string]int)
	for _, pid := range pids {
		statPath := fmt.Sprintf("/proc/%d/stat", pid)
		data, err := os.ReadFile(statPath)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data))
		if len(fields) > 2 {
			state := fields[2]
			stateCounts[state]++
		}
	}

	for state, count := range stateCounts {
		labels := []*proto.MetricLabel{{Key: "state", Value: state}}
		metrics = append(metrics, &proto.Metric{
			Name:   "process.count_by_state",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels,
			Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(count)}},
		})
	}

	// Count threads
	var totalThreads int
	for _, pid := range pids {
		statPath := fmt.Sprintf("/proc/%d/stat", pid)
		data, err := os.ReadFile(statPath)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data))
		if len(fields) > 19 {
			if threads, err := strconv.Atoi(fields[19]); err == nil {
				totalThreads += threads
			}
		}
	}
	metrics = append(metrics, createGauge("process.threads", float64(totalThreads), now))

	// Get context switches from /proc/stat
	if data, err := os.ReadFile("/proc/stat"); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "ctxt") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if val, err := strconv.ParseFloat(fields[1], 64); err == nil {
						metrics = append(metrics, createGauge("system.context_switches", val, now))
					}
				}
				break
			}
		}
	}

	// Get fork count from /proc/stat
	if data, err := os.ReadFile("/proc/stat"); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "processes") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if val, err := strconv.ParseFloat(fields[1], 64); err == nil {
						metrics = append(metrics, createGauge("process.forks_total", val, now))
					}
				}
				break
			}
		}
	}

	return metrics
}

// GetTopProcesses returns the top N processes by CPU usage
func (p *ProcessSource) GetTopProcesses(n int) []*ProcessInfo {
	pids, err := p.getAllPIDs()
	if err != nil {
		return nil
	}
	processes := p.collectProcessMetrics(pids)
	if n > 0 && n < len(processes) {
		return processes[:n]
	}
	return processes
}
