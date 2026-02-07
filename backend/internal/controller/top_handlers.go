package controller

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
)

const (
	defaultTopProgramsLimit = 20
	maxTopProgramsLimit     = 200
	defaultCategoryTopN     = 5
	maxCategoryTopN         = 20
)

var topProgramCategories = []string{"cpu", "memory", "disk", "disk_io", "network", "gpu", "logs"}

var resourceCategoryTitles = map[string]string{
	"cpu":     "CPU",
	"memory":  "Memory",
	"disk":    "Disk (Storage Footprint)",
	"disk_io": "Disk I/O (Rate & Syscalls)",
	"network": "Network / NIC",
	"gpu":     "GPU",
	"logs":    "Logs",
}

var resourceCategorySignals = map[string][]KernelSignal{
	"cpu": {
		{Name: "rca_cpu_process_percent", Unit: "%", Source: "/proc/[pid]/stat", Description: "Per-process CPU utilization percentage."},
		{Name: "rca_cpu_process_user_percent", Unit: "%", Source: "/proc/[pid]/stat", Description: "User-mode CPU percentage."},
		{Name: "rca_cpu_process_system_percent", Unit: "%", Source: "/proc/[pid]/stat", Description: "Kernel-mode CPU percentage."},
		{Name: "node_process_cpu_percent", Unit: "%", Source: "process sampler", Description: "Collector per-process CPU percentage fallback."},
		{Name: "rca_cpu_process_wchan", Unit: "state", Source: "/proc/[pid]/wchan", Description: "Kernel wait channel for blocked tasks."},
		{Name: "rca_cpu_process_syscall", Unit: "syscall", Source: "/proc/[pid]/syscall", Description: "Current syscall (if blocked)."},
		{Name: "node_ebpf_process_events_total", Unit: "events", Source: "eBPF sched tracepoints", Description: "Kernel scheduler event counts by process."},
	},
	"memory": {
		{Name: "rca_memory_process_rss_bytes", Unit: "bytes", Source: "/proc/[pid]/status", Description: "Resident memory bytes."},
		{Name: "rca_memory_process_pss_bytes", Unit: "bytes", Source: "/proc/[pid]/smaps_rollup", Description: "Proportional set size bytes."},
		{Name: "rca_memory_process_swap_bytes", Unit: "bytes", Source: "/proc/[pid]/status", Description: "Swapped bytes by process."},
		{Name: "node_process_memory_rss_bytes", Unit: "bytes", Source: "process sampler", Description: "Collector per-process RSS fallback."},
		{Name: "rca_memory_process_majflt_total", Unit: "faults", Source: "/proc/[pid]/stat", Description: "Major page faults (counter)."},
		{Name: "rca_memory_process_oom_score", Unit: "score", Source: "/proc/[pid]/oom_score", Description: "OOM kill likelihood score."},
		{Name: "node_ebpf_process_events_total", Unit: "events", Source: "eBPF memory hooks", Description: "Kernel memory pressure events by process."},
	},
	"disk": {
		{Name: "rca_io_process_read_bytes_total", Unit: "bytes", Source: "/proc/[pid]/io", Description: "Total bytes read from storage."},
		{Name: "rca_io_process_write_bytes_total", Unit: "bytes", Source: "/proc/[pid]/io", Description: "Total bytes written to storage."},
		{Name: "rca_io_process_file_fd", Unit: "fds", Source: "/proc/[pid]/fd", Description: "Open storage file descriptors."},
	},
	"disk_io": {
		{Name: "rca_io_process_read_bytes_per_second", Unit: "bytes/s", Source: "/proc/[pid]/io deltas", Description: "Read throughput per process."},
		{Name: "rca_io_process_write_bytes_per_second", Unit: "bytes/s", Source: "/proc/[pid]/io deltas", Description: "Write throughput per process."},
		{Name: "node_process_io_read_bytes_per_second", Unit: "bytes/s", Source: "process sampler", Description: "Collector per-process read throughput fallback."},
		{Name: "node_process_io_write_bytes_per_second", Unit: "bytes/s", Source: "process sampler", Description: "Collector per-process write throughput fallback."},
		{Name: "rca_io_process_read_syscalls_total", Unit: "syscalls", Source: "/proc/[pid]/io", Description: "Read syscall count (counter)."},
		{Name: "rca_io_process_write_syscalls_total", Unit: "syscalls", Source: "/proc/[pid]/io", Description: "Write syscall count (counter)."},
		{Name: "node_ebpf_process_events_total", Unit: "events", Source: "eBPF block tracepoints", Description: "Kernel block I/O events by process."},
	},
	"network": {
		{Name: "rca_net_process_connections", Unit: "connections", Source: "/proc/net/tcp,/proc/*/fd", Description: "Open socket connections by process."},
		{Name: "rca_net_process_queued_bytes", Unit: "bytes", Source: "/proc/net/tcp queues", Description: "Socket queue backlog bytes."},
		{Name: "rca_net_connection_queue_bytes", Unit: "bytes", Source: "/proc/net/tcp queues", Description: "Top queued connection bytes."},
		{Name: "node_ebpf_process_events_total", Unit: "events", Source: "eBPF net tracepoints", Description: "Kernel network events by process."},
	},
	"gpu": {
		{Name: "node_gpu_process_memory_mib", Unit: "MiB", Source: "nvidia-smi", Description: "GPU memory used per process."},
		{Name: "node_gpu_process_sm_util_percent", Unit: "%", Source: "nvidia-smi", Description: "SM utilization by process."},
		{Name: "node_gpu_process_mem_util_percent", Unit: "%", Source: "nvidia-smi", Description: "GPU memory utilization by process."},
		{Name: "node_ebpf_gpu_events_total", Unit: "events", Source: "eBPF GPU hooks", Description: "Kernel-level GPU events (node-level aggregate)."},
	},
	"logs": {
		{Name: "log_errors", Unit: "count", Source: "collector log fingerprints", Description: "Per-process/service error log count."},
		{Name: "log_warnings", Unit: "count", Source: "collector log fingerprints", Description: "Per-process/service warning log count."},
		{Name: "node_kmsg_messages_total", Unit: "count", Source: "/dev/kmsg", Description: "Kernel message counts by severity (node-level)."},
	},
}

// ProgramStats represents aggregated resource signals for a program (process).
type ProgramStats struct {
	CollectorID string `json:"collector_id"`
	Hostname    string `json:"hostname"`
	PID         string `json:"pid,omitempty"`
	Name        string `json:"name"`

	CPUPercent   float64 `json:"cpu_percent,omitempty"`
	MemoryBytes  uint64  `json:"memory_bytes,omitempty"`
	DiskReadBps  float64 `json:"disk_read_bps,omitempty"`
	DiskWriteBps float64 `json:"disk_write_bps,omitempty"`

	DiskReadBytesTotal      float64 `json:"disk_read_bytes_total,omitempty"`
	DiskWriteBytesTotal     float64 `json:"disk_write_bytes_total,omitempty"`
	DiskReadSyscallsTotal   float64 `json:"disk_read_syscalls_total,omitempty"`
	DiskWriteSyscallsTotal  float64 `json:"disk_write_syscalls_total,omitempty"`
	DiskQueuedBytesEstimate float64 `json:"disk_queued_bytes_estimate,omitempty"`

	NetBytesPerSecond float64 `json:"net_bytes_per_second,omitempty"`
	NetQueuedBytes    float64 `json:"net_queued_bytes,omitempty"`
	NetConnections    int     `json:"net_connections,omitempty"`

	GPUMemMiB     float64 `json:"gpu_mem_mib,omitempty"`
	GPUUtilSMPct  float64 `json:"gpu_util_sm_percent,omitempty"`
	GPUUtilMemPct float64 `json:"gpu_util_mem_percent,omitempty"`

	LogErrors   int `json:"log_errors,omitempty"`
	LogWarnings int `json:"log_warnings,omitempty"`

	CategoryTotals    map[string]float64 `json:"category_totals,omitempty"`
	CategoryFrequency map[string]uint64  `json:"category_frequency,omitempty"`
	SignalValues      map[string]float64 `json:"signal_values,omitempty"`
	SignalTotals      map[string]float64 `json:"signal_totals,omitempty"`
	SignalFrequency   map[string]uint64  `json:"signal_frequency,omitempty"`

	Categories []string `json:"categories,omitempty"`
	Score      float64  `json:"score"`
}

// KernelSignal documents kernel-level signal provenance used in ranking.
type KernelSignal struct {
	Name        string `json:"name"`
	Unit        string `json:"unit,omitempty"`
	Source      string `json:"source,omitempty"`
	Description string `json:"description,omitempty"`
}

// ResourceCategoryPage describes one UI page/tab worth of ranked process data.
type ResourceCategoryPage struct {
	Category      string         `json:"category"`
	Title         string         `json:"title"`
	PrimaryMetric string         `json:"primary_metric"`
	KernelSignals []KernelSignal `json:"kernel_signals,omitempty"`
	Ranked        []ProgramStats `json:"ranked"`
}

// TopProgramsReport contains unified, cross-category highlights.
type TopProgramsReport struct {
	TopOverall      *ProgramStats  `json:"top_overall,omitempty"`
	MostProblematic *ProgramStats  `json:"most_problematic,omitempty"`
	Hotspots        []ProgramStats `json:"hotspots,omitempty"`
	CategoryCounts  map[string]int `json:"category_counts,omitempty"`
	CategoryTopN    map[string]int `json:"category_top_n,omitempty"`
	GeneratedAt     time.Time      `json:"generated_at"`
}

// TopProgramsResponse is returned by the /api/v1/top/programs endpoint.
type TopProgramsResponse struct {
	GeneratedAt   time.Time                       `json:"generated_at"`
	Limit         int                             `json:"limit"`
	Count         int                             `json:"count"`
	Programs      []ProgramStats                  `json:"programs"`
	Summary       map[string]ProgramStats         `json:"summary"` // highest per category
	ByCategory    map[string][]ProgramStats       `json:"by_category"`
	Report        TopProgramsReport               `json:"report"`
	ResourcePages map[string]ResourceCategoryPage `json:"resource_pages,omitempty"`
}

// handleTopPrograms returns the most resource-intensive/problematic programs.
func (c *Controller) handleTopPrograms(w http.ResponseWriter, r *http.Request) {
	limit := parseTopProgramsLimit(r.URL.Query().Get("limit"))
	categoryTopN := normalizedCategoryTopN(limit)

	allPrograms := c.aggregateTopPrograms(maxTopProgramsLimit)
	programs := allPrograms
	if len(programs) > limit {
		programs = programs[:limit]
	}

	resp := TopProgramsResponse{
		GeneratedAt:   time.Now(),
		Limit:         limit,
		Count:         len(programs),
		Programs:      programs,
		Summary:       summarizeTopPrograms(allPrograms),
		ByCategory:    categorizeTopPrograms(allPrograms, categoryTopN),
		Report:        buildTopProgramsReport(allPrograms, categoryTopN),
		ResourcePages: buildResourceCategoryPages(allPrograms, maxInt(limit, categoryTopN)),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func parseTopProgramsLimit(raw string) int {
	if raw == "" {
		return defaultTopProgramsLimit
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return defaultTopProgramsLimit
	}
	if limit > maxTopProgramsLimit {
		return maxTopProgramsLimit
	}
	return limit
}

func normalizedCategoryTopN(limit int) int {
	topN := defaultCategoryTopN
	if limit > 0 && limit < topN {
		topN = limit
	}
	if topN > maxCategoryTopN {
		topN = maxCategoryTopN
	}
	if topN <= 0 {
		topN = 1
	}
	return topN
}

// aggregateTopPrograms builds a ranked list of programs across nodes/resources.
func (c *Controller) aggregateTopPrograms(limit int) []ProgramStats {
	if c.ingestStore == nil {
		return nil
	}

	nodes := c.ingestStore.Snapshot()
	gpuNodes := c.gpuStoreSnapshot()

	index := make(map[string]*ProgramStats)
	nameIndex := make(map[string]*ProgramStats)

	collectorNameKey := func(collectorID, name string) string {
		return collectorID + "|name|" + strings.ToLower(strings.TrimSpace(name))
	}

	ensure := func(collectorID, hostname, pid, name string) *ProgramStats {
		pid = strings.TrimSpace(pid)
		name = strings.TrimSpace(name)
		if name == "" {
			name = "unknown"
		}

		// If PID is absent, attempt best-effort merge by (collector, name) so
		// log-only samples can still attach to known process rows.
		if pid == "" {
			if ps, ok := nameIndex[collectorNameKey(collectorID, name)]; ok {
				return ps
			}
		}

		key := collectorID + "|pid|" + pid
		if pid == "" {
			key = collectorNameKey(collectorID, name)
		}
		if ps, ok := index[key]; ok {
			if ps.Name == "unknown" && name != "" {
				ps.Name = name
				nameIndex[collectorNameKey(collectorID, ps.Name)] = ps
			}
			if name != "" && name != "unknown" {
				nameIndex[collectorNameKey(collectorID, name)] = ps
			}
			return ps
		}

		ps := &ProgramStats{
			CollectorID:       collectorID,
			Hostname:          hostname,
			PID:               pid,
			Name:              name,
			CategoryTotals:    make(map[string]float64, len(topProgramCategories)),
			CategoryFrequency: make(map[string]uint64, len(topProgramCategories)),
			SignalValues:      make(map[string]float64, 16),
			SignalTotals:      make(map[string]float64, 16),
			SignalFrequency:   make(map[string]uint64, 16),
		}
		index[key] = ps
		nameIndex[collectorNameKey(collectorID, name)] = ps
		return ps
	}

	for _, n := range nodes {
		host := n.Hostname
		if host == "" {
			host = n.CollectorID
		}

		for _, p := range n.Processes {
			if p == nil {
				continue
			}
			pid := strconv.Itoa(int(p.Pid))
			ps := ensure(n.CollectorID, host, pid, p.Name)
			if p.CpuPercent > ps.CPUPercent {
				ps.CPUPercent = p.CpuPercent
			}
			if p.RssBytes > ps.MemoryBytes {
				ps.MemoryBytes = p.RssBytes
			}
			if p.IoReadBps > ps.DiskReadBps {
				ps.DiskReadBps = p.IoReadBps
			}
			if p.IoWriteBps > ps.DiskWriteBps {
				ps.DiskWriteBps = p.IoWriteBps
			}
		}

		for _, pr := range n.ProcessResources {
			if pr == nil {
				continue
			}
			ps := ensure(n.CollectorID, host, pr.PID, pr.Name)
			if ps.PID == "" && pr.PID != "" {
				ps.PID = pr.PID
			}
			if ps.Name == "unknown" && pr.Name != "" {
				ps.Name = pr.Name
			}

			mergeFloatMaps(ps.SignalTotals, pr.SignalTotals)
			mergeFloatMapsMax(ps.SignalValues, pr.SignalValues)
			mergeUint64Maps(ps.SignalFrequency, pr.SignalFrequency)
			mergeFloatMaps(ps.CategoryTotals, pr.CategoryTotals)
			mergeUint64Maps(ps.CategoryFrequency, pr.CategoryFrequency)

			ps.LogErrors += int(pr.LogErrors)
			ps.LogWarnings += int(pr.LogWarnings)
		}

		for pid, pn := range n.ProcessNetwork {
			if pn == nil {
				continue
			}
			ps := ensure(n.CollectorID, host, pid, pn.Name)
			if pn.Connections > ps.NetConnections {
				ps.NetConnections = pn.Connections
			}
			if pn.QueuedBytes > ps.NetQueuedBytes {
				ps.NetQueuedBytes = pn.QueuedBytes
			}
			if pn.BytesPerSecond > ps.NetBytesPerSecond {
				ps.NetBytesPerSecond = pn.BytesPerSecond
			}
		}

		hasLogAttribution := false
		for _, pr := range n.ProcessResources {
			if pr == nil {
				continue
			}
			if pr.LogErrors > 0 || pr.LogWarnings > 0 {
				hasLogAttribution = true
				break
			}
			if pr.SignalTotals["log_errors"] > 0 || pr.SignalTotals["log_warnings"] > 0 {
				hasLogAttribution = true
				break
			}
			if pr.SignalFrequency["log_errors"] > 0 || pr.SignalFrequency["log_warnings"] > 0 {
				hasLogAttribution = true
				break
			}
		}
		if !hasLogAttribution {
			for _, lf := range n.Logs {
				if lf == nil || lf.Example == "" {
					continue
				}
				severity := ""
				lower := strings.ToLower(lf.Example)
				switch {
				case strings.Contains(lower, "error"), strings.Contains(lower, "err "), strings.Contains(lower, "fatal"), strings.Contains(lower, "panic"), strings.Contains(lower, "critical"):
					severity = "error"
				case strings.Contains(lower, "warn"), strings.Contains(lower, "deprecated"):
					severity = "warn"
				default:
					continue
				}
				prog := guessProgramName(lf.Example)
				if prog == "" {
					prog = "unknown"
				}
				ps := ensure(n.CollectorID, host, "", prog)
				if severity == "error" {
					ps.LogErrors += int(lf.Count)
				} else {
					ps.LogWarnings += int(lf.Count)
				}
			}
		}
	}

	// GPU process attribution fallback.
	for _, gn := range gpuNodes {
		host := gn.Hostname
		if host == "" {
			host = gn.CollectorID
		}
		for _, dev := range gn.GPUs {
			for _, gp := range dev.Processes {
				ps := ensure(gn.CollectorID, host, gp.PID, gp.Name)
				if gp.MemMiB > ps.GPUMemMiB {
					ps.GPUMemMiB = gp.MemMiB
				}
				if gp.UtilSMPct > ps.GPUUtilSMPct {
					ps.GPUUtilSMPct = gp.UtilSMPct
				}
				if gp.UtilMemPct > ps.GPUUtilMemPct {
					ps.GPUUtilMemPct = gp.UtilMemPct
				}

				if gp.MemMiB > 0 && ps.SignalFrequency["node_gpu_process_memory_mib"] == 0 {
					ps.SignalValues["node_gpu_process_memory_mib"] = gp.MemMiB
					ps.SignalTotals["node_gpu_process_memory_mib"] += gp.MemMiB
					ps.SignalFrequency["node_gpu_process_memory_mib"]++
					ps.CategoryTotals["gpu"] += gp.MemMiB
					ps.CategoryFrequency["gpu"]++
				} else if gp.MemMiB > ps.SignalValues["node_gpu_process_memory_mib"] {
					ps.SignalValues["node_gpu_process_memory_mib"] = gp.MemMiB
				}
				if gp.UtilSMPct > 0 && ps.SignalFrequency["node_gpu_process_sm_util_percent"] == 0 {
					ps.SignalValues["node_gpu_process_sm_util_percent"] = gp.UtilSMPct
					ps.SignalTotals["node_gpu_process_sm_util_percent"] += gp.UtilSMPct
					ps.SignalFrequency["node_gpu_process_sm_util_percent"]++
					ps.CategoryTotals["gpu"] += gp.UtilSMPct
					ps.CategoryFrequency["gpu"]++
				} else if gp.UtilSMPct > ps.SignalValues["node_gpu_process_sm_util_percent"] {
					ps.SignalValues["node_gpu_process_sm_util_percent"] = gp.UtilSMPct
				}
				if gp.UtilMemPct > 0 && ps.SignalFrequency["node_gpu_process_mem_util_percent"] == 0 {
					ps.SignalValues["node_gpu_process_mem_util_percent"] = gp.UtilMemPct
					ps.SignalTotals["node_gpu_process_mem_util_percent"] += gp.UtilMemPct
					ps.SignalFrequency["node_gpu_process_mem_util_percent"]++
					ps.CategoryTotals["gpu"] += gp.UtilMemPct
					ps.CategoryFrequency["gpu"]++
				} else if gp.UtilMemPct > ps.SignalValues["node_gpu_process_mem_util_percent"] {
					ps.SignalValues["node_gpu_process_mem_util_percent"] = gp.UtilMemPct
				}
			}
		}
	}

	var out []ProgramStats
	for _, ps := range index {
		applySignalDerivedFields(ps)
		hydrateCategoryStats(ps)
		ps.Score = programScore(*ps)
		ps.Categories = deriveCategories(*ps)
		out = append(out, *ps)
	}

	sortProgramsByScore(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func mergeFloatMaps(dst, src map[string]float64) {
	if dst == nil || len(src) == 0 {
		return
	}
	for k, v := range src {
		dst[k] += v
	}
}

func mergeFloatMapsMax(dst, src map[string]float64) {
	if dst == nil || len(src) == 0 {
		return
	}
	for k, v := range src {
		if v > dst[k] {
			dst[k] = v
		}
	}
}

func mergeUint64Maps(dst, src map[string]uint64) {
	if dst == nil || len(src) == 0 {
		return
	}
	for k, v := range src {
		dst[k] += v
	}
}

func applySignalDerivedFields(ps *ProgramStats) {
	if ps == nil {
		return
	}

	ps.CPUPercent = maxFloat(ps.CPUPercent,
		signalMax(ps.SignalValues, "rca_cpu_process_percent", "node_process_cpu_percent"))

	if mem := signalMax(ps.SignalValues, "rca_memory_process_rss_bytes", "node_process_memory_rss_bytes"); mem > 0 {
		if uint64(mem) > ps.MemoryBytes {
			ps.MemoryBytes = uint64(mem)
		}
	}

	ps.DiskReadBps = maxFloat(ps.DiskReadBps,
		signalMax(ps.SignalValues, "rca_io_process_read_bytes_per_second", "node_process_io_read_bytes_per_second"))
	ps.DiskWriteBps = maxFloat(ps.DiskWriteBps,
		signalMax(ps.SignalValues, "rca_io_process_write_bytes_per_second", "node_process_io_write_bytes_per_second"))

	if v := signalSum(ps.SignalTotals, "rca_io_process_read_bytes_total"); v > 0 {
		ps.DiskReadBytesTotal = v
	}
	if v := signalSum(ps.SignalTotals, "rca_io_process_write_bytes_total"); v > 0 {
		ps.DiskWriteBytesTotal = v
	}
	if v := signalSum(ps.SignalTotals, "rca_io_process_read_syscalls_total"); v > 0 {
		ps.DiskReadSyscallsTotal = v
	}
	if v := signalSum(ps.SignalTotals, "rca_io_process_write_syscalls_total"); v > 0 {
		ps.DiskWriteSyscallsTotal = v
	}
	if v := signalSum(ps.SignalTotals, "rca_io_process_file_fd"); v > 0 {
		ps.DiskQueuedBytesEstimate = v
	}

	if v := signalMax(ps.SignalValues, "rca_net_process_connections"); v > 0 {
		if int(v) > ps.NetConnections {
			ps.NetConnections = int(v)
		}
	}
	ps.NetQueuedBytes = maxFloat(ps.NetQueuedBytes, signalMax(ps.SignalValues, "rca_net_process_queued_bytes", "rca_net_connection_queue_bytes"))

	ps.GPUMemMiB = maxFloat(ps.GPUMemMiB, signalMax(ps.SignalValues, "node_gpu_process_memory_mib"))
	ps.GPUUtilSMPct = maxFloat(ps.GPUUtilSMPct, signalMax(ps.SignalValues, "node_gpu_process_sm_util_percent"))
	ps.GPUUtilMemPct = maxFloat(ps.GPUUtilMemPct, signalMax(ps.SignalValues, "node_gpu_process_mem_util_percent"))

	if ps.LogErrors == 0 {
		ps.LogErrors = int(signalSum(ps.SignalTotals, "log_errors"))
	}
	if ps.LogWarnings == 0 {
		ps.LogWarnings = int(signalSum(ps.SignalTotals, "log_warnings"))
	}
}

func hydrateCategoryStats(ps *ProgramStats) {
	if ps == nil {
		return
	}
	if ps.CategoryTotals == nil {
		ps.CategoryTotals = make(map[string]float64, len(topProgramCategories))
	}
	if ps.CategoryFrequency == nil {
		ps.CategoryFrequency = make(map[string]uint64, len(topProgramCategories))
	}

	cpuTotal := signalSum(ps.SignalTotals,
		"rca_cpu_process_percent", "node_process_cpu_percent",
		"rca_cpu_process_user_percent", "rca_cpu_process_system_percent")
	if ps.CategoryTotals["cpu"] == 0 && cpuTotal > 0 {
		ps.CategoryTotals["cpu"] = cpuTotal
	}

	memoryTotal := signalSum(ps.SignalTotals,
		"rca_memory_process_rss_bytes", "rca_memory_process_pss_bytes", "rca_memory_process_swap_bytes",
		"node_process_memory_rss_bytes")
	if ps.CategoryTotals["memory"] == 0 && memoryTotal > 0 {
		ps.CategoryTotals["memory"] = memoryTotal
	}

	diskTotal := ps.DiskReadBytesTotal + ps.DiskWriteBytesTotal
	if diskTotal == 0 {
		diskTotal = signalSum(ps.SignalTotals, "rca_io_process_read_bytes_total", "rca_io_process_write_bytes_total")
	}
	if ps.CategoryTotals["disk"] == 0 && diskTotal > 0 {
		ps.CategoryTotals["disk"] = diskTotal
	}

	diskIOTotal := signalSum(ps.SignalTotals,
		"rca_io_process_read_bytes_per_second", "rca_io_process_write_bytes_per_second",
		"node_process_io_read_bytes_per_second", "node_process_io_write_bytes_per_second",
		"rca_io_process_read_syscalls_total", "rca_io_process_write_syscalls_total", "rca_io_process_file_fd")
	if ps.CategoryTotals["disk_io"] == 0 && diskIOTotal > 0 {
		ps.CategoryTotals["disk_io"] = diskIOTotal
	}

	networkTotal := signalSum(ps.SignalTotals,
		"rca_net_process_connections", "rca_net_process_queued_bytes", "rca_net_connection_queue_bytes")
	if networkTotal == 0 {
		networkTotal = ps.NetQueuedBytes + ps.NetBytesPerSecond + float64(ps.NetConnections)
	}
	if ps.CategoryTotals["network"] == 0 && networkTotal > 0 {
		ps.CategoryTotals["network"] = networkTotal
	}

	gpuTotal := signalSum(ps.SignalTotals,
		"node_gpu_process_memory_mib", "node_gpu_process_sm_util_percent", "node_gpu_process_mem_util_percent")
	if ps.CategoryTotals["gpu"] == 0 && gpuTotal > 0 {
		ps.CategoryTotals["gpu"] = gpuTotal
	}

	logsTotal := (signalSum(ps.SignalTotals, "log_errors") * 2) + signalSum(ps.SignalTotals, "log_warnings")
	if logsTotal == 0 {
		logsTotal = float64(ps.LogErrors*2 + ps.LogWarnings)
	}
	if ps.CategoryTotals["logs"] == 0 && logsTotal > 0 {
		ps.CategoryTotals["logs"] = logsTotal
	}

	if ps.CategoryFrequency["cpu"] == 0 {
		ps.CategoryFrequency["cpu"] = signalFreqSum(ps.SignalFrequency,
			"rca_cpu_process_percent", "node_process_cpu_percent")
	}
	if ps.CategoryFrequency["memory"] == 0 {
		ps.CategoryFrequency["memory"] = signalFreqSum(ps.SignalFrequency,
			"rca_memory_process_rss_bytes", "node_process_memory_rss_bytes")
	}
	if ps.CategoryFrequency["disk"] == 0 {
		ps.CategoryFrequency["disk"] = signalFreqSum(ps.SignalFrequency,
			"rca_io_process_read_bytes_total", "rca_io_process_write_bytes_total",
			"node_process_io_read_bytes_per_second", "node_process_io_write_bytes_per_second")
	}
	if ps.CategoryFrequency["disk_io"] == 0 {
		ps.CategoryFrequency["disk_io"] = signalFreqSum(ps.SignalFrequency,
			"rca_io_process_read_bytes_per_second", "rca_io_process_write_bytes_per_second",
			"rca_io_process_read_syscalls_total", "rca_io_process_write_syscalls_total", "rca_io_process_file_fd",
			"node_process_io_read_bytes_per_second", "node_process_io_write_bytes_per_second")
	}
	if ps.CategoryFrequency["network"] == 0 {
		ps.CategoryFrequency["network"] = signalFreqSum(ps.SignalFrequency,
			"rca_net_process_connections", "rca_net_process_queued_bytes", "rca_net_connection_queue_bytes")
	}
	if ps.CategoryFrequency["gpu"] == 0 {
		ps.CategoryFrequency["gpu"] = signalFreqSum(ps.SignalFrequency,
			"node_gpu_process_memory_mib", "node_gpu_process_sm_util_percent", "node_gpu_process_mem_util_percent")
	}
	if ps.CategoryFrequency["logs"] == 0 {
		ps.CategoryFrequency["logs"] = signalFreqSum(ps.SignalFrequency, "log_errors", "log_warnings")
	}

	for _, category := range topProgramCategories {
		if ps.CategoryFrequency[category] == 0 && metricCurrentValue(*ps, category) > 0 {
			ps.CategoryFrequency[category] = 1
		}
	}
}

func signalMax(values map[string]float64, keys ...string) float64 {
	maxValue := 0.0
	for _, k := range keys {
		if v := values[k]; v > maxValue {
			maxValue = v
		}
	}
	return maxValue
}

func signalSum(values map[string]float64, keys ...string) float64 {
	total := 0.0
	for _, k := range keys {
		total += values[k]
	}
	return total
}

func signalFreqSum(values map[string]uint64, keys ...string) uint64 {
	var total uint64
	for _, k := range keys {
		total += values[k]
	}
	return total
}

func (c *Controller) gpuStoreSnapshot() []*gpuobs.Node {
	if c.gpuStore == nil {
		return nil
	}
	return c.gpuStore.Snapshot()
}

func programScore(p ProgramStats) float64 {
	clamp := func(v, lo, hi float64) float64 {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}

	cpu := clamp(metricCurrentValue(p, "cpu")/100.0, 0, 1) * 3.0
	mem := clamp(metricCurrentValue(p, "memory")/(4.0*1024*1024*1024), 0, 1) * 2.0
	storage := clamp(metricCurrentValue(p, "disk")/(2.0*1024*1024*1024), 0, 1) * 1.0
	io := clamp(metricCurrentValue(p, "disk_io")/(50.0*1024*1024), 0, 1) * 1.0
	net := clamp(metricCurrentValue(p, "network")/(50.0*1024*1024), 0, 1) * 1.5
	gpu := clamp(metricCurrentValue(p, "gpu")/100.0, 0, 1) * 2.0
	logs := clamp(metricCurrentValue(p, "logs")/20.0, 0, 1) * 1.5

	return cpu + mem + storage + io + net + gpu + logs
}

func deriveCategories(p ProgramStats) []string {
	var cats []string
	if metricCurrentValue(p, "cpu") >= 80 || categoryFrequencyValue(p, "cpu") >= 3 {
		cats = append(cats, "cpu")
	}
	if metricCurrentValue(p, "memory") >= 2*1024*1024*1024 || categoryFrequencyValue(p, "memory") >= 3 {
		cats = append(cats, "memory")
	}
	if (p.DiskReadBps+p.DiskWriteBps) >= 20*1024*1024 || metricCurrentValue(p, "disk") >= 1*1024*1024*1024 {
		cats = append(cats, "disk")
	}
	if metricCurrentValue(p, "disk_io") >= 10*1024*1024 || categoryFrequencyValue(p, "disk_io") >= 3 {
		cats = append(cats, "disk_io")
	}
	if p.NetBytesPerSecond >= 10*1024*1024 || p.NetQueuedBytes >= 1*1024*1024 || p.NetConnections >= 50 || categoryFrequencyValue(p, "network") >= 3 {
		cats = append(cats, "network")
	}
	if p.GPUUtilSMPct >= 60 || p.GPUMemMiB >= 2000 || categoryFrequencyValue(p, "gpu") >= 3 {
		cats = append(cats, "gpu")
	}
	if p.LogErrors > 0 || p.LogWarnings > 0 || categoryFrequencyValue(p, "logs") > 0 {
		cats = append(cats, "logs")
	}
	return cats
}

func sortProgramsByScore(programs []ProgramStats) {
	sort.Slice(programs, func(i, j int) bool {
		left := programs[i]
		right := programs[j]

		switch {
		case left.Score != right.Score:
			return left.Score > right.Score
		case metricValue(left, "logs") != metricValue(right, "logs"):
			return metricValue(left, "logs") > metricValue(right, "logs")
		case metricCurrentValue(left, "cpu") != metricCurrentValue(right, "cpu"):
			return metricCurrentValue(left, "cpu") > metricCurrentValue(right, "cpu")
		case metricCurrentValue(left, "memory") != metricCurrentValue(right, "memory"):
			return metricCurrentValue(left, "memory") > metricCurrentValue(right, "memory")
		case left.Name != right.Name:
			return left.Name < right.Name
		default:
			return left.PID < right.PID
		}
	})
}

func summarizeTopPrograms(programs []ProgramStats) map[string]ProgramStats {
	summary := make(map[string]ProgramStats)
	pickMax := func(key string, candidate ProgramStats) {
		if current, ok := summary[key]; !ok || metricValue(candidate, key) > metricValue(current, key) {
			summary[key] = candidate
		}
	}

	for _, p := range programs {
		pickMax("cpu", p)
		pickMax("memory", p)
		pickMax("disk", p)
		pickMax("disk_io", p)
		pickMax("network", p)
		pickMax("gpu", p)
		pickMax("logs", p)
		pickMax("score", p)
	}

	return summary
}

func categorizeTopPrograms(programs []ProgramStats, topN int) map[string][]ProgramStats {
	if topN <= 0 {
		topN = defaultCategoryTopN
	}
	if topN > maxCategoryTopN {
		topN = maxCategoryTopN
	}

	out := make(map[string][]ProgramStats, len(topProgramCategories))
	for _, category := range topProgramCategories {
		ranked := make([]ProgramStats, 0, len(programs))
		for _, p := range programs {
			if metricValue(p, category) > 0 {
				ranked = append(ranked, p)
			}
		}

		sort.Slice(ranked, func(i, j int) bool {
			left := ranked[i]
			right := ranked[j]
			switch {
			case metricValue(left, category) != metricValue(right, category):
				return metricValue(left, category) > metricValue(right, category)
			case categoryFrequencyValue(left, category) != categoryFrequencyValue(right, category):
				return categoryFrequencyValue(left, category) > categoryFrequencyValue(right, category)
			case left.Score != right.Score:
				return left.Score > right.Score
			case left.Name != right.Name:
				return left.Name < right.Name
			default:
				return left.PID < right.PID
			}
		})

		if len(ranked) > topN {
			ranked = ranked[:topN]
		}
		out[category] = ranked
	}
	return out
}

func buildTopProgramsReport(programs []ProgramStats, categoryTopN int) TopProgramsReport {
	report := TopProgramsReport{
		CategoryCounts: make(map[string]int, len(topProgramCategories)),
		CategoryTopN:   make(map[string]int, len(topProgramCategories)),
		GeneratedAt:    time.Now(),
	}
	for _, category := range topProgramCategories {
		report.CategoryCounts[category] = 0
		report.CategoryTopN[category] = 0
	}

	if len(programs) == 0 {
		return report
	}

	top := programs[0]
	report.TopOverall = &top
	report.Hotspots = append(report.Hotspots, programs...)
	if len(report.Hotspots) > categoryTopN {
		report.Hotspots = report.Hotspots[:categoryTopN]
	}

	var problematic ProgramStats
	var problematicSet bool
	for _, p := range programs {
		for _, category := range p.Categories {
			report.CategoryCounts[category]++
		}
		for _, category := range topProgramCategories {
			if metricValue(p, category) > 0 {
				report.CategoryTopN[category]++
			}
		}

		if !problematicSet || metricValue(p, "logs") > metricValue(problematic, "logs") || (metricValue(p, "logs") == metricValue(problematic, "logs") && p.Score > problematic.Score) {
			problematic = p
			problematicSet = true
		}
	}
	if problematicSet && metricValue(problematic, "logs") > 0 {
		p := problematic
		report.MostProblematic = &p
	}

	return report
}

func buildResourceCategoryPages(programs []ProgramStats, topN int) map[string]ResourceCategoryPage {
	if topN <= 0 {
		topN = defaultTopProgramsLimit
	}
	pages := make(map[string]ResourceCategoryPage, len(topProgramCategories))

	for _, category := range topProgramCategories {
		ranked := make([]ProgramStats, 0, len(programs))
		for _, p := range programs {
			if metricValue(p, category) > 0 || categoryFrequencyValue(p, category) > 0 {
				ranked = append(ranked, p)
			}
		}

		sort.Slice(ranked, func(i, j int) bool {
			left := ranked[i]
			right := ranked[j]

			leftOverall := metricValue(left, category)
			rightOverall := metricValue(right, category)
			if leftOverall != rightOverall {
				return leftOverall > rightOverall
			}

			leftFreq := categoryFrequencyValue(left, category)
			rightFreq := categoryFrequencyValue(right, category)
			if leftFreq != rightFreq {
				return leftFreq > rightFreq
			}

			leftCurrent := metricCurrentValue(left, category)
			rightCurrent := metricCurrentValue(right, category)
			if leftCurrent != rightCurrent {
				return leftCurrent > rightCurrent
			}

			if left.Score != right.Score {
				return left.Score > right.Score
			}
			if left.Name != right.Name {
				return left.Name < right.Name
			}
			return left.PID < right.PID
		})

		if len(ranked) > topN {
			ranked = ranked[:topN]
		}

		pages[category] = ResourceCategoryPage{
			Category:      category,
			Title:         resourceCategoryTitles[category],
			PrimaryMetric: primaryMetricLabel(category),
			KernelSignals: resourceCategorySignals[category],
			Ranked:        ranked,
		}
	}

	return pages
}

func primaryMetricLabel(category string) string {
	switch category {
	case "cpu":
		return "CPU utilization / scheduler pressure"
	case "memory":
		return "Resident memory + memory pressure"
	case "disk":
		return "Cumulative storage bytes + open file footprint"
	case "disk_io":
		return "Read/write throughput + syscall pressure"
	case "network":
		return "NIC/socket pressure"
	case "gpu":
		return "GPU memory + SM utilization"
	case "logs":
		return "Error/warning event volume"
	default:
		return "resource pressure"
	}
}

func metricValue(p ProgramStats, key string) float64 {
	switch key {
	case "score":
		return p.Score
	default:
		return categoryOverall(p, key)
	}
}

func categoryOverall(p ProgramStats, key string) float64 {
	if p.CategoryTotals != nil {
		if v := p.CategoryTotals[key]; v > 0 {
			return v
		}
	}
	return metricCurrentValue(p, key)
}

func categoryFrequencyValue(p ProgramStats, key string) uint64 {
	if p.CategoryFrequency != nil {
		if v := p.CategoryFrequency[key]; v > 0 {
			return v
		}
	}
	if metricCurrentValue(p, key) > 0 {
		return 1
	}
	return 0
}

func metricCurrentValue(p ProgramStats, key string) float64 {
	switch key {
	case "cpu":
		return p.CPUPercent
	case "memory":
		return float64(p.MemoryBytes)
	case "disk":
		if p.DiskReadBps+p.DiskWriteBps > 0 {
			return p.DiskReadBps + p.DiskWriteBps
		}
		return p.DiskReadBytesTotal + p.DiskWriteBytesTotal
	case "disk_io":
		return (p.DiskReadBps + p.DiskWriteBps) + p.DiskReadSyscallsTotal + p.DiskWriteSyscallsTotal + p.DiskQueuedBytesEstimate
	case "network":
		return p.NetBytesPerSecond + p.NetQueuedBytes + float64(p.NetConnections*1024)
	case "gpu":
		return p.GPUUtilSMPct + p.GPUMemMiB/100.0 + p.GPUUtilMemPct/100.0
	case "logs":
		return float64(p.LogErrors*2 + p.LogWarnings)
	default:
		return 0
	}
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// guessProgramName attempts to extract a program token from a log line.
func guessProgramName(line string) string {
	token := strings.TrimSpace(line)
	if token == "" {
		return ""
	}

	if idx := strings.Index(token, ": "); idx > 0 {
		token = strings.TrimSpace(token[:idx])
	} else if parts := strings.SplitN(token, ":", 2); len(parts) > 0 {
		token = strings.TrimSpace(parts[0])
	}
	fields := strings.Fields(token)
	if len(fields) > 0 {
		token = fields[len(fields)-1]
	}
	if slash := strings.LastIndex(token, "/"); slash >= 0 && slash+1 < len(token) {
		token = token[slash+1:]
	}
	if idx := strings.Index(token, "["); idx > 0 {
		token = token[:idx]
	}
	token = strings.Trim(token, "[](){}<>:;,. \t\r\n")
	switch strings.ToLower(token) {
	case "error", "warn", "warning", "info", "debug", "fatal", "panic", "critical":
		return ""
	}
	return token
}
