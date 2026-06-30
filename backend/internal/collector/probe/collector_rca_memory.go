// Package probe implements memory root cause analysis collection.
// This provides process-level memory attribution for diagnosing memory pressure.
package probe

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemoryProcessInfo holds detailed memory information for a process
type MemoryProcessInfo struct {
	PID         int
	Name        string
	VmSize      uint64 // Virtual memory size
	VmRSS       uint64 // Resident set size
	VmData      uint64 // Data segment size
	VmStack     uint64 // Stack size
	VmExe       uint64 // Text (executable) size
	VmLib       uint64 // Shared library size
	VmSwap      uint64 // Swapped out memory
	VmPeak      uint64 // Peak virtual memory
	HWMPeak     uint64 // Peak RSS (high water mark)
	RssAnon     uint64 // Anonymous memory (heap)
	RssFile     uint64 // File-backed memory
	RssShmem    uint64 // Shared memory
	PSS         uint64 // Proportional set size
	MajorFaults uint64
	MinorFaults uint64
	OOMScore    int
	OOMScoreAdj int

	// Top memory regions
	TopRegions []MemoryRegion
}

// MemoryRegion represents a memory mapping region
type MemoryRegion struct {
	Type       string // heap, stack, lib, file, anonymous
	Path       string // file path if applicable
	Size       uint64 // in bytes
	RSS        uint64 // resident in bytes
	PSS        uint64 // proportional set size
	Private    uint64 // private memory
	Shared     uint64 // shared memory
	Referenced uint64 // recently accessed
}

// MemoryRootCauseCollector collects detailed memory attribution data
type MemoryRootCauseCollector struct {
	mu sync.Mutex

	// Configuration
	topN          int
	topRegions    int
	detailedSmaps bool
}

// NewMemoryRootCauseCollector creates a new memory RCA collector
func NewMemoryRootCauseCollector(topN, topRegions int, detailedSmaps bool) *MemoryRootCauseCollector {
	if topN <= 0 {
		topN = 20
	}
	if topRegions <= 0 {
		topRegions = 5
	}
	return &MemoryRootCauseCollector{
		topN:          topN,
		topRegions:    topRegions,
		detailedSmaps: detailedSmaps,
	}
}

// Collect gathers memory root cause metrics
func (c *MemoryRootCauseCollector) Collect(now time.Time) ([]Metric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Get total memory for percentage calculation
	totalMem := c.getTotalMemory()

	// Scan all processes
	procs, err := c.scanProcesses()
	if err != nil {
		return nil, err
	}

	var metrics []Metric

	// Sort by RSS (descending) for top N
	sort.Slice(procs, func(i, j int) bool {
		return procs[i].VmRSS > procs[j].VmRSS
	})

	// Emit metrics for top N memory consumers
	count := c.topN
	if count > len(procs) {
		count = len(procs)
	}

	for _, p := range procs[:count] {
		if p.VmRSS < 1024*1024 { // Skip < 1MB
			continue
		}

		labels := map[string]string{
			"pid":  fmt.Sprintf("%d", p.PID),
			"name": sanitizeProcName(p.Name),
		}

		// Memory sizes
		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_rss_bytes",
			Type:      "gauge",
			Value:     float64(p.VmRSS),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_vms_bytes",
			Type:      "gauge",
			Value:     float64(p.VmSize),
			Labels:    labels,
			Timestamp: now,
		})

		if totalMem > 0 {
			metrics = append(metrics, Metric{
				Name:      "rca_memory_process_percent",
				Type:      "gauge",
				Value:     float64(p.VmRSS) / float64(totalMem) * 100.0,
				Labels:    labels,
				Timestamp: now,
			})
		}

		// Memory breakdown
		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_anon_bytes",
			Type:      "gauge",
			Value:     float64(p.RssAnon),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_file_bytes",
			Type:      "gauge",
			Value:     float64(p.RssFile),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_shared_bytes",
			Type:      "gauge",
			Value:     float64(p.RssShmem),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_swap_bytes",
			Type:      "gauge",
			Value:     float64(p.VmSwap),
			Labels:    labels,
			Timestamp: now,
		})

		if p.PSS > 0 {
			metrics = append(metrics, Metric{
				Name:      "rca_memory_process_pss_bytes",
				Type:      "gauge",
				Value:     float64(p.PSS),
				Labels:    labels,
				Timestamp: now,
			})
		}

		// Memory segments
		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_data_bytes",
			Type:      "gauge",
			Value:     float64(p.VmData),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_stack_bytes",
			Type:      "gauge",
			Value:     float64(p.VmStack),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_lib_bytes",
			Type:      "gauge",
			Value:     float64(p.VmLib),
			Labels:    labels,
			Timestamp: now,
		})

		// Page faults
		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_majflt_total",
			Type:      "counter",
			Value:     float64(p.MajorFaults),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_minflt_total",
			Type:      "counter",
			Value:     float64(p.MinorFaults),
			Labels:    labels,
			Timestamp: now,
		})

		// OOM score
		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_oom_score",
			Type:      "gauge",
			Value:     float64(p.OOMScore),
			Labels:    labels,
			Timestamp: now,
		})

		// Peak memory (high water mark)
		metrics = append(metrics, Metric{
			Name:      "rca_memory_process_hwm_bytes",
			Type:      "gauge",
			Value:     float64(p.HWMPeak),
			Labels:    labels,
			Timestamp: now,
		})

		// Top memory regions
		for _, region := range p.TopRegions {
			regionLabels := copyLabels(labels)
			regionLabels["type"] = region.Type
			if region.Path != "" && len(region.Path) < 64 {
				regionLabels["path"] = region.Path
			}

			metrics = append(metrics, Metric{
				Name:      "rca_memory_region_rss_bytes",
				Type:      "gauge",
				Value:     float64(region.RSS),
				Labels:    regionLabels,
				Timestamp: now,
			})
		}
	}

	// Summary metrics
	var totalRSS, totalSwap uint64
	for _, p := range procs {
		totalRSS += p.VmRSS
		totalSwap += p.VmSwap
	}

	metrics = append(metrics, Metric{
		Name:      "rca_memory_total_process_rss_bytes",
		Type:      "gauge",
		Value:     float64(totalRSS),
		Timestamp: now,
	})

	metrics = append(metrics, Metric{
		Name:      "rca_memory_total_process_swap_bytes",
		Type:      "gauge",
		Value:     float64(totalSwap),
		Timestamp: now,
	})

	return metrics, nil
}

func (c *MemoryRootCauseCollector) scanProcesses() ([]MemoryProcessInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	var procs []MemoryProcessInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		proc, err := c.readProcessMemory(pid)
		if err != nil {
			continue
		}

		procs = append(procs, proc)
	}

	return procs, nil
}

func (c *MemoryRootCauseCollector) readProcessMemory(pid int) (MemoryProcessInfo, error) {
	procPath := fmt.Sprintf("/proc/%d", pid)
	proc := MemoryProcessInfo{PID: pid}

	// Read name from comm
	if data, err := os.ReadFile(filepath.Join(procPath, "comm")); err == nil {
		proc.Name = strings.TrimSpace(string(data))
	}

	// Read /proc/[pid]/status for memory info
	statusFile, err := os.Open(filepath.Join(procPath, "status"))
	if err != nil {
		return proc, err
	}
	defer statusFile.Close()

	scanner := bufio.NewScanner(statusFile)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		key := strings.TrimSuffix(parts[0], ":")
		val, _ := strconv.ParseUint(parts[1], 10, 64)

		switch key {
		case "VmSize":
			proc.VmSize = val * 1024
		case "VmRSS":
			proc.VmRSS = val * 1024
		case "VmData":
			proc.VmData = val * 1024
		case "VmStk":
			proc.VmStack = val * 1024
		case "VmExe":
			proc.VmExe = val * 1024
		case "VmLib":
			proc.VmLib = val * 1024
		case "VmSwap":
			proc.VmSwap = val * 1024
		case "VmPeak":
			proc.VmPeak = val * 1024
		case "VmHWM":
			proc.HWMPeak = val * 1024
		case "RssAnon":
			proc.RssAnon = val * 1024
		case "RssFile":
			proc.RssFile = val * 1024
		case "RssShmem":
			proc.RssShmem = val * 1024
		}
	}

	// Read page faults from /proc/[pid]/stat
	if statData, err := os.ReadFile(filepath.Join(procPath, "stat")); err == nil {
		statStr := string(statData)
		commEnd := strings.LastIndex(statStr, ")")
		if commEnd != -1 {
			remaining := strings.Fields(statStr[commEnd+2:])
			if len(remaining) >= 12 {
				proc.MinorFaults, _ = strconv.ParseUint(remaining[7], 10, 64)
				proc.MajorFaults, _ = strconv.ParseUint(remaining[9], 10, 64)
			}
		}
	}

	// Read OOM score
	if data, err := os.ReadFile(filepath.Join(procPath, "oom_score")); err == nil {
		proc.OOMScore, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}

	if data, err := os.ReadFile(filepath.Join(procPath, "oom_score_adj")); err == nil {
		proc.OOMScoreAdj, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}

	// Read smaps_rollup for PSS (less expensive than full smaps)
	if data, err := os.ReadFile(filepath.Join(procPath, "smaps_rollup")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "Pss:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					pss, _ := strconv.ParseUint(parts[1], 10, 64)
					proc.PSS = pss * 1024
				}
				break
			}
		}
	}

	// Read top memory regions from smaps (only for top processes - expensive)
	if c.detailedSmaps && proc.VmRSS > 100*1024*1024 { // Only for > 100MB processes
		proc.TopRegions = c.readTopRegions(procPath)
	}

	return proc, nil
}

func (c *MemoryRootCauseCollector) readTopRegions(procPath string) []MemoryRegion {
	f, err := os.Open(filepath.Join(procPath, "smaps"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var regions []MemoryRegion
	var current *MemoryRegion

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// New region header: address perms offset dev inode pathname
		if len(line) > 0 && (line[0] >= '0' && line[0] <= '9' || line[0] >= 'a' && line[0] <= 'f') {
			if current != nil && current.RSS > 0 {
				regions = append(regions, *current)
			}

			current = &MemoryRegion{}
			parts := strings.Fields(line)
			if len(parts) >= 6 {
				path := parts[5]
				current.Path = path
				current.Type = classifyRegion(path)
			} else if len(parts) >= 1 {
				current.Type = "anonymous"
			}
			continue
		}

		if current == nil {
			continue
		}

		// Parse region details
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		key := strings.TrimSuffix(parts[0], ":")
		val, _ := strconv.ParseUint(parts[1], 10, 64)
		val *= 1024 // Convert kB to bytes

		switch key {
		case "Size":
			current.Size = val
		case "Rss":
			current.RSS = val
		case "Pss":
			current.PSS = val
		case "Private_Clean", "Private_Dirty":
			current.Private += val
		case "Shared_Clean", "Shared_Dirty":
			current.Shared += val
		case "Referenced":
			current.Referenced = val
		}
	}

	if current != nil && current.RSS > 0 {
		regions = append(regions, *current)
	}

	// Sort by RSS and take top N
	sort.Slice(regions, func(i, j int) bool {
		return regions[i].RSS > regions[j].RSS
	})

	if len(regions) > c.topRegions {
		regions = regions[:c.topRegions]
	}

	return regions
}

func classifyRegion(path string) string {
	if path == "" || path == "[anon]" {
		return "anonymous"
	}
	if path == "[heap]" {
		return "heap"
	}
	if path == "[stack]" {
		return "stack"
	}
	if path == "[vdso]" || path == "[vvar]" || path == "[vsyscall]" {
		return "vdso"
	}
	if strings.HasSuffix(path, ".so") || strings.Contains(path, ".so.") {
		return "library"
	}
	if strings.HasPrefix(path, "/") {
		return "file"
	}
	return "other"
}

func (c *MemoryRootCauseCollector) getTotalMemory() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				val, _ := strconv.ParseUint(parts[1], 10, 64)
				return val * 1024

			}
		}
	}

	return 0
}
