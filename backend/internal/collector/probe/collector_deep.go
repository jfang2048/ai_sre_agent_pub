// Package probe implements deep metrics collection (Level 3).
// These metrics provide per-process visibility for the top resource consumers.
package probe

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProcessInfo holds information about a single process
type ProcessInfo struct {
	PID        int
	Name       string
	State      string
	CPUTime    float64 // Total CPU time in seconds
	MemoryRSS  uint64  // Resident set size in bytes
	MemoryVMS  uint64  // Virtual memory size in bytes
	Threads    int
	FDs        int
	ReadBytes  uint64
	WriteBytes uint64
	Uptime     float64 // Process uptime in seconds
}

// collectProcesses collects per-process metrics for top N processes
func (c *Collector) collectProcesses(now time.Time, topN int) ([]Metric, error) {
	if topN <= 0 {
		topN = 10
	}

	procs, err := c.scanProcesses()
	if err != nil {
		return nil, err
	}

	var metrics []Metric

	// Sort by CPU time (descending) and take top N
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].CPUTime > procs[j].CPUTime
	})

	topCPU := procs
	if len(topCPU) > topN {
		topCPU = topCPU[:topN]
	}

	for _, p := range topCPU {
		labels := map[string]string{
			"pid":  fmt.Sprintf("%d", p.PID),
			"name": sanitizeLabel(p.Name),
		}

		metrics = append(metrics, Metric{
			Name:      "node_process_cpu_seconds_total",
			Type:      "counter",
			Value:     p.CPUTime,
			Labels:    labels,
			Timestamp: now,
		})
	}

	// Sort by memory (descending) and take top N
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].MemoryRSS > procs[j].MemoryRSS
	})

	topMem := procs
	if len(topMem) > topN {
		topMem = topMem[:topN]
	}

	for _, p := range topMem {
		labels := map[string]string{
			"pid":  fmt.Sprintf("%d", p.PID),
			"name": sanitizeLabel(p.Name),
		}

		metrics = append(metrics, Metric{
			Name:      "node_process_memory_rss_bytes",
			Type:      "gauge",
			Value:     float64(p.MemoryRSS),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "node_process_memory_vms_bytes",
			Type:      "gauge",
			Value:     float64(p.MemoryVMS),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "node_process_threads",
			Type:      "gauge",
			Value:     float64(p.Threads),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "node_process_fds",
			Type:      "gauge",
			Value:     float64(p.FDs),
			Labels:    labels,
			Timestamp: now,
		})
	}

	// Aggregate statistics
	var totalProcs int
	var totalThreads int
	var totalFDs int
	stateCount := make(map[string]int)

	for _, p := range procs {
		totalProcs++
		totalThreads += p.Threads
		totalFDs += p.FDs
		stateCount[p.State]++
	}

	metrics = append(metrics, Metric{
		Name:      "node_processes_total",
		Type:      "gauge",
		Value:     float64(totalProcs),
		Timestamp: now,
	})

	metrics = append(metrics, Metric{
		Name:      "node_threads_total",
		Type:      "gauge",
		Value:     float64(totalThreads),
		Timestamp: now,
	})

	metrics = append(metrics, Metric{
		Name:      "node_fds_total",
		Type:      "gauge",
		Value:     float64(totalFDs),
		Timestamp: now,
	})

	for state, count := range stateCount {
		metrics = append(metrics, Metric{
			Name:      "node_processes_state",
			Type:      "gauge",
			Value:     float64(count),
			Labels:    map[string]string{"state": state},
			Timestamp: now,
		})
	}

	return metrics, nil
}

// scanProcesses reads all process information from /proc
func (c *Collector) scanProcesses() ([]ProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	// Get system boot time for uptime calculation
	bootTime := c.getBootTime()

	var procs []ProcessInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // Not a process directory
		}

		proc, err := c.readProcess(pid, bootTime)
		if err != nil {
			continue // Process may have exited
		}

		procs = append(procs, proc)
	}

	return procs, nil
}

// readProcess reads information for a single process
func (c *Collector) readProcess(pid int, bootTime float64) (ProcessInfo, error) {
	procPath := fmt.Sprintf("/proc/%d", pid)

	proc := ProcessInfo{PID: pid}

	// Read /proc/[pid]/stat
	statData, err := os.ReadFile(filepath.Join(procPath, "stat"))
	if err != nil {
		return proc, err
	}

	// Parse stat - format: pid (comm) state ...
	// Find the closing paren of comm to handle names with spaces/parens
	statStr := string(statData)
	commStart := strings.Index(statStr, "(")
	commEnd := strings.LastIndex(statStr, ")")
	if commStart == -1 || commEnd == -1 {
		return proc, fmt.Errorf("invalid stat format")
	}

	proc.Name = statStr[commStart+1 : commEnd]

	// Parse remaining fields after (comm)
	remaining := strings.Fields(statStr[commEnd+2:])
	if len(remaining) < 22 {
		return proc, fmt.Errorf("insufficient stat fields")
	}

	proc.State = remaining[0]

	// Fields (0-indexed after comm):
	// 0: state
	// 11: utime (user time in clock ticks)
	// 12: stime (system time in clock ticks)
	// 13: cutime (children user time)
	// 14: cstime (children system time)
	// 17: num_threads
	// 19: starttime (time started after boot in clock ticks)
	// 20: vsize (virtual memory size in bytes)
	// 21: rss (resident set size in pages)

	utime, _ := strconv.ParseFloat(remaining[11], 64)
	stime, _ := strconv.ParseFloat(remaining[12], 64)
	proc.CPUTime = (utime + stime) / 100.0 // Convert clock ticks to seconds (assume HZ=100)

	proc.Threads, _ = strconv.Atoi(remaining[17])

	starttime, _ := strconv.ParseFloat(remaining[19], 64)
	proc.Uptime = bootTime - (starttime / 100.0)

	vsize, _ := strconv.ParseUint(remaining[20], 10, 64)
	proc.MemoryVMS = vsize

	rss, _ := strconv.ParseUint(remaining[21], 10, 64)
	proc.MemoryRSS = rss * 4096 // Convert pages to bytes (assume 4KB pages)

	// Read file descriptors count
	fdPath := filepath.Join(procPath, "fd")
	if fdEntries, err := os.ReadDir(fdPath); err == nil {
		proc.FDs = len(fdEntries)
	}

	// Read I/O statistics (optional, may require root)
	if ioData, err := os.ReadFile(filepath.Join(procPath, "io")); err == nil {
		for _, line := range strings.Split(string(ioData), "\n") {
			parts := strings.Split(line, ": ")
			if len(parts) != 2 {
				continue
			}
			val, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
			switch parts[0] {
			case "read_bytes":
				proc.ReadBytes = val
			case "write_bytes":
				proc.WriteBytes = val
			}
		}
	}

	return proc, nil
}

// getBootTime returns the system boot time as Unix timestamp
func (c *Collector) getBootTime() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				btime, _ := strconv.ParseFloat(parts[1], 64)
				return float64(time.Now().Unix()) - btime
			}
		}
	}

	return 0
}

// collectInterrupts reads interrupt distribution from /proc/interrupts
func (c *Collector) collectInterrupts(now time.Time) ([]Metric, error) {
	f, err := os.Open("/proc/interrupts")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	scanner := bufio.NewScanner(f)

	// First line is CPU headers
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty interrupts file")
	}

	header := strings.Fields(scanner.Text())
	numCPUs := len(header)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < numCPUs+1 {
			continue
		}

		irq := strings.TrimSuffix(fields[0], ":")

		// Skip non-numeric IRQs for now (like NMI, LOC, etc.)
		// but include total counts
		var total uint64
		for i := 1; i <= numCPUs && i < len(fields); i++ {
			count, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				break
			}
			total += count
		}

		if total > 0 {
			// Get device name if available (last field)
			device := irq
			if len(fields) > numCPUs+1 {
				device = fields[len(fields)-1]
			}

			metrics = append(metrics, Metric{
				Name:      "node_interrupts_total",
				Type:      "counter",
				Value:     float64(total),
				Labels:    map[string]string{"irq": irq, "device": sanitizeLabel(device)},
				Timestamp: now,
			})
		}
	}

	return metrics, nil
}

// collectSoftIRQs reads softirq distribution from /proc/softirqs
func (c *Collector) collectSoftIRQs(now time.Time) ([]Metric, error) {
	f, err := os.Open("/proc/softirqs")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var metrics []Metric
	scanner := bufio.NewScanner(f)

	// First line is CPU headers
	if !scanner.Scan() {
		return nil, fmt.Errorf("empty softirqs file")
	}

	header := strings.Fields(scanner.Text())
	numCPUs := len(header)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < numCPUs+1 {
			continue
		}

		irqType := strings.TrimSuffix(fields[0], ":")

		var total uint64
		for i := 1; i <= numCPUs && i < len(fields); i++ {
			count, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				break
			}
			total += count
		}

		metrics = append(metrics, Metric{
			Name:      "node_softirqs_total",
			Type:      "counter",
			Value:     float64(total),
			Labels:    map[string]string{"type": strings.ToLower(irqType)},
			Timestamp: now,
		})
	}

	return metrics, nil
}

// collectDeep collects all Level 3 deep metrics
func (c *Collector) collectDeep(now time.Time, topN int) []Metric {
	var metrics []Metric

	if m, err := c.collectProcesses(now, topN); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectInterrupts(now); err == nil {
		metrics = append(metrics, m...)
	}

	if m, err := c.collectSoftIRQs(now); err == nil {
		metrics = append(metrics, m...)
	}

	return metrics
}

// sanitizeLabel sanitizes a label value for Prometheus
func sanitizeLabel(s string) string {
	// Limit length
	if len(s) > 64 {
		s = s[:64]
	}

	// Replace problematic characters
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "")

	return s
}
