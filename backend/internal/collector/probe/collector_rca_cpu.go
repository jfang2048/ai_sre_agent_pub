// Package probe implements CPU root cause analysis collection.
// This provides process-level CPU attribution for diagnosing high CPU usage.
package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CPUProcessInfo holds detailed CPU information for a process
type CPUProcessInfo struct {
	PID                 int
	Name                string
	State               string
	UserTime            float64 // seconds
	SystemTime          float64 // seconds
	ChildUserTime       float64 // seconds
	ChildSystemTime     float64 // seconds
	Threads             int
	Priority            int
	Nice                int
	StartTime           float64 // seconds since boot
	VoluntarySwitches   uint64
	InvoluntarySwitches uint64
	Wchan               string  // wait channel (kernel function)
	Syscall             string  // current syscall if blocked
	CPUPercent          float64 // calculated
	UserPercent         float64
	SystemPercent       float64
}

// CPURootCauseCollector collects detailed CPU attribution data
type CPURootCauseCollector struct {
	mu sync.Mutex

	// Previous state for rate calculations
	lastProcessCPU map[int]*CPUProcessInfo
	lastTotalCPU   uint64
	lastCollect    time.Time

	// Configuration
	topN int
}

// NewCPURootCauseCollector creates a new CPU RCA collector
func NewCPURootCauseCollector(topN int) *CPURootCauseCollector {
	if topN <= 0 {
		topN = 20
	}
	return &CPURootCauseCollector{
		lastProcessCPU: make(map[int]*CPUProcessInfo),
		topN:           topN,
	}
}

// Collect gathers CPU root cause metrics
func (c *CPURootCauseCollector) Collect(now time.Time) ([]Metric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := now.Sub(c.lastCollect).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	// Get total CPU time for percentage calculation
	totalCPU := c.getTotalCPU()
	totalCPUDelta := float64(totalCPU - c.lastTotalCPU)
	if totalCPUDelta <= 0 {
		totalCPUDelta = 1
	}

	// Scan all processes
	procs, err := c.scanProcesses(totalCPUDelta, elapsed)
	if err != nil {
		return nil, err
	}

	var metrics []Metric

	// Sort by CPU percent (descending) for top N
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].CPUPercent > procs[j].CPUPercent
	})

	// Emit metrics for top N CPU consumers
	count := c.topN
	if count > len(procs) {
		count = len(procs)
	}

	for _, p := range procs[:count] {
		if p.CPUPercent < 0.1 {
			continue // Skip near-zero processes
		}

		labels := map[string]string{
			"pid":  fmt.Sprintf("%d", p.PID),
			"name": sanitizeProcName(p.Name),
		}

		// CPU time totals
		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_user_seconds_total",
			Type:      "counter",
			Value:     p.UserTime,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_system_seconds_total",
			Type:      "counter",
			Value:     p.SystemTime,
			Labels:    labels,
			Timestamp: now,
		})

		// Current CPU percentage
		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_percent",
			Type:      "gauge",
			Value:     p.CPUPercent,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_user_percent",
			Type:      "gauge",
			Value:     p.UserPercent,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_system_percent",
			Type:      "gauge",
			Value:     p.SystemPercent,
			Labels:    labels,
			Timestamp: now,
		})

		// Thread count
		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_threads",
			Type:      "gauge",
			Value:     float64(p.Threads),
			Labels:    labels,
			Timestamp: now,
		})

		// Process state
		stateLabels := copyLabels(labels)
		stateLabels["state"] = p.State
		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_state",
			Type:      "gauge",
			Value:     1,
			Labels:    stateLabels,
			Timestamp: now,
		})

		// Context switches
		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_voluntary_switches_total",
			Type:      "counter",
			Value:     float64(p.VoluntarySwitches),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_involuntary_switches_total",
			Type:      "counter",
			Value:     float64(p.InvoluntarySwitches),
			Labels:    labels,
			Timestamp: now,
		})

		// Wait channel (what kernel function is blocking)
		if p.Wchan != "" && p.Wchan != "0" {
			wchanLabels := copyLabels(labels)
			wchanLabels["wchan"] = p.Wchan
			metrics = append(metrics, Metric{
				Name:      "rca_cpu_process_wchan",
				Type:      "gauge",
				Value:     1,
				Labels:    wchanLabels,
				Timestamp: now,
			})
		}

		// Current syscall
		if p.Syscall != "" && p.Syscall != "running" {
			syscallLabels := copyLabels(labels)
			syscallLabels["syscall"] = p.Syscall
			metrics = append(metrics, Metric{
				Name:      "rca_cpu_process_syscall",
				Type:      "gauge",
				Value:     1,
				Labels:    syscallLabels,
				Timestamp: now,
			})
		}

		// Priority/nice
		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_priority",
			Type:      "gauge",
			Value:     float64(p.Priority),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_cpu_process_nice",
			Type:      "gauge",
			Value:     float64(p.Nice),
			Labels:    labels,
			Timestamp: now,
		})
	}

	// Summary metrics
	var totalCPUPercent float64
	stateDistribution := make(map[string]int)
	for _, p := range procs {
		totalCPUPercent += p.CPUPercent
		stateDistribution[p.State]++
	}

	metrics = append(metrics, Metric{
		Name:      "rca_cpu_total_process_percent",
		Type:      "gauge",
		Value:     totalCPUPercent,
		Timestamp: now,
	})

	for state, count := range stateDistribution {
		metrics = append(metrics, Metric{
			Name:      "rca_cpu_processes_by_state",
			Type:      "gauge",
			Value:     float64(count),
			Labels:    map[string]string{"state": stateToName(state)},
			Timestamp: now,
		})
	}

	// Update state
	c.lastTotalCPU = totalCPU
	c.lastCollect = now
	c.updateLastProcessCPU(procs)

	return metrics, nil
}

func (c *CPURootCauseCollector) scanProcesses(totalCPUDelta, elapsed float64) ([]CPUProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	var procs []CPUProcessInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		proc, err := c.readProcessCPU(pid)
		if err != nil {
			continue
		}

		// Calculate CPU percentage from delta
		if prev, ok := c.lastProcessCPU[pid]; ok {
			userDelta := proc.UserTime - prev.UserTime
			systemDelta := proc.SystemTime - prev.SystemTime
			totalDelta := userDelta + systemDelta

			if totalCPUDelta > 0 {
				// Normalize to percentage of total CPU
				proc.CPUPercent = (totalDelta / (totalCPUDelta / 100.0))
				proc.UserPercent = (userDelta / (totalCPUDelta / 100.0))
				proc.SystemPercent = (systemDelta / (totalCPUDelta / 100.0))
			}
		}

		procs = append(procs, proc)
	}

	return procs, nil
}

func (c *CPURootCauseCollector) readProcessCPU(pid int) (CPUProcessInfo, error) {
	procPath := fmt.Sprintf("/proc/%d", pid)
	proc := CPUProcessInfo{PID: pid}

	// Read /proc/[pid]/stat
	statData, err := os.ReadFile(filepath.Join(procPath, "stat"))
	if err != nil {
		return proc, err
	}

	// Parse stat - format: pid (comm) state ...
	statStr := string(statData)
	commStart := strings.Index(statStr, "(")
	commEnd := strings.LastIndex(statStr, ")")
	if commStart == -1 || commEnd == -1 {
		return proc, fmt.Errorf("invalid stat format")
	}

	proc.Name = statStr[commStart+1 : commEnd]

	// Parse remaining fields
	remaining := strings.Fields(statStr[commEnd+2:])
	if len(remaining) < 22 {
		return proc, fmt.Errorf("insufficient stat fields")
	}

	proc.State = remaining[0]

	// utime (13), stime (14), cutime (15), cstime (16), priority (17), nice (18)
	// num_threads (19), starttime (21)
	utime, _ := strconv.ParseFloat(remaining[11], 64)
	stime, _ := strconv.ParseFloat(remaining[12], 64)
	cutime, _ := strconv.ParseFloat(remaining[13], 64)
	cstime, _ := strconv.ParseFloat(remaining[14], 64)

	proc.UserTime = utime / 100.0 // Convert jiffies to seconds
	proc.SystemTime = stime / 100.0
	proc.ChildUserTime = cutime / 100.0
	proc.ChildSystemTime = cstime / 100.0

	proc.Priority, _ = strconv.Atoi(remaining[15])
	proc.Nice, _ = strconv.Atoi(remaining[16])
	proc.Threads, _ = strconv.Atoi(remaining[17])

	starttime, _ := strconv.ParseFloat(remaining[19], 64)
	proc.StartTime = starttime / 100.0

	// Read context switches from /proc/[pid]/status
	if statusData, err := os.ReadFile(filepath.Join(procPath, "status")); err == nil {
		for _, line := range strings.Split(string(statusData), "\n") {
			if strings.HasPrefix(line, "voluntary_ctxt_switches:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					proc.VoluntarySwitches, _ = strconv.ParseUint(parts[1], 10, 64)
				}
			} else if strings.HasPrefix(line, "nonvoluntary_ctxt_switches:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					proc.InvoluntarySwitches, _ = strconv.ParseUint(parts[1], 10, 64)
				}
			}
		}
	}

	// Read wait channel
	if wchanData, err := os.ReadFile(filepath.Join(procPath, "wchan")); err == nil {
		proc.Wchan = strings.TrimSpace(string(wchanData))
	}

	// Read current syscall
	if syscallData, err := os.ReadFile(filepath.Join(procPath, "syscall")); err == nil {
		parts := strings.Fields(string(syscallData))
		if len(parts) >= 1 {
			proc.Syscall = syscallNumberToName(parts[0])
		}
	}

	return proc, nil
}

func (c *CPURootCauseCollector) getTotalCPU() uint64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0
	}

	fields := strings.Fields(lines[0])
	if len(fields) < 8 || fields[0] != "cpu" {
		return 0
	}

	var total uint64
	for _, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		total += v
	}

	return total
}

func (c *CPURootCauseCollector) updateLastProcessCPU(procs []CPUProcessInfo) {
	newMap := make(map[int]*CPUProcessInfo)
	for i := range procs {
		p := procs[i]
		newMap[p.PID] = &p
	}
	c.lastProcessCPU = newMap
}

func sanitizeProcName(name string) string {
	// Limit length and sanitize
	if len(name) > 32 {
		name = name[:32]
	}
	// Remove special characters
	name = strings.ReplaceAll(name, "\"", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.ReplaceAll(name, "\n", "")
	return name
}

func stateToName(state string) string {
	switch state {
	case "R":
		return "running"
	case "S":
		return "sleeping"
	case "D":
		return "disk_wait"
	case "Z":
		return "zombie"
	case "T":
		return "stopped"
	case "t":
		return "tracing"
	case "X":
		return "dead"
	case "I":
		return "idle"
	default:
		return state
	}
}

func syscallNumberToName(num string) string {
	// Common syscall numbers (x86_64)
	syscalls := map[string]string{
		"0":   "read",
		"1":   "write",
		"2":   "open",
		"3":   "close",
		"7":   "poll",
		"8":   "lseek",
		"9":   "mmap",
		"11":  "munmap",
		"16":  "ioctl",
		"17":  "pread64",
		"18":  "pwrite64",
		"19":  "readv",
		"20":  "writev",
		"21":  "access",
		"22":  "pipe",
		"23":  "select",
		"35":  "nanosleep",
		"56":  "clone",
		"57":  "fork",
		"59":  "execve",
		"60":  "exit",
		"61":  "wait4",
		"202": "futex",
		"232": "epoll_wait",
		"270": "pselect6",
		"271": "ppoll",
		"-1":  "running",
	}

	if name, ok := syscalls[num]; ok {
		return name
	}
	return "syscall_" + num
}
