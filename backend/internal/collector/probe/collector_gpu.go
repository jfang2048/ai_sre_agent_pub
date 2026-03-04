// Package probe implements GPU metrics collection using low-overhead tooling.
package probe

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	gpuDisabledEnv = "SRE_COLLECTOR_GPU_DISABLED"

	gpuAdvancedIntervalEnv      = "SRE_COLLECTOR_GPU_ADVANCED_INTERVAL_SAMPLES"
	gpuProcessDetailIntervalEnv = "SRE_COLLECTOR_GPU_PROCESS_DETAIL_INTERVAL_SAMPLES"
	gpuQueryTimeoutMsEnv        = "SRE_COLLECTOR_GPU_QUERY_TIMEOUT_MS"

	defaultGPUAdvancedInterval      = uint64(3)
	defaultGPUProcessDetailInterval = uint64(2)
	defaultGPUQueryTimeout          = 1500 * time.Millisecond
)

var (
	xidRegex           = regexp.MustCompile(`(?i)Xid\s*[^0-9]*([0-9]+)`)
	gpuIndexRegex      = regexp.MustCompile(`(?i)\bGPU(?:\s+|:|\[|\()([0-9]+)\b`)
	gpuDashIndexRegex  = regexp.MustCompile(`(?i)\bGPU[-_]([0-9]+)\b`)
	uvmFaultRegex      = regexp.MustCompile(`(?i)\buvm\b.*\b(fault|page fault|migration)\b`)
	gpuResetRegex      = regexp.MustCompile(`(?i)\b(gpu|nvrm)\b.*\b(reset|fallen off the bus|gpu reset)\b`)
	eccEventRegex      = regexp.MustCompile(`(?i)\becc\b.*\b(error|single|double)\b`)
	throttleEventRegex = regexp.MustCompile(`(?i)\b(throttle|thermal slowdown|power brake|power cap)\b`)
)

type gpuEventKey struct {
	eventType string
	severity  string
	gpuID     string
	code      string
}

// GPUCollector gathers NVIDIA GPU telemetry via nvidia-smi and kernel logs.
type GPUCollector struct {
	nvidiaSMIPath string
	lastOffsets   map[string]int64
	cudaVersion   string

	sampleCount           uint64
	advancedInterval      uint64
	processDetailInterval uint64
	queryTimeout          time.Duration
	queryDurationMs       map[string]float64
	queryErrorsTotal      map[string]uint64
	queryTimeoutsTotal    map[string]uint64
	kernelEventTotals     map[gpuEventKey]uint64
}

// NewGPUCollector creates a GPU collector when nvidia-smi is available.
func NewGPUCollector() *GPUCollector {
	if os.Getenv(gpuDisabledEnv) == "1" {
		return nil
	}

	path, err := exec.LookPath("nvidia-smi")
	if err != nil {
		path = findNvidiaSMI()
	}
	if path == "" {
		return nil
	}

	collector := &GPUCollector{
		nvidiaSMIPath:         path,
		lastOffsets:           make(map[string]int64),
		advancedInterval:      positiveUintEnv(gpuAdvancedIntervalEnv, defaultGPUAdvancedInterval),
		processDetailInterval: positiveUintEnv(gpuProcessDetailIntervalEnv, defaultGPUProcessDetailInterval),
		queryTimeout:          positiveDurationMsEnv(gpuQueryTimeoutMsEnv, defaultGPUQueryTimeout),
		queryDurationMs:       make(map[string]float64),
		queryErrorsTotal:      make(map[string]uint64),
		queryTimeoutsTotal:    make(map[string]uint64),
		kernelEventTotals:     make(map[gpuEventKey]uint64),
	}
	collector.cudaVersion = detectCudaVersion(path)
	return collector
}

func positiveUintEnv(key string, fallback uint64) uint64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		return fallback
	}
	return v
}

func positiveDurationMsEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return time.Duration(v) * time.Millisecond
}

func (g *GPUCollector) shouldRunAdvanced() bool {
	if g.advancedInterval <= 1 {
		return true
	}
	return g.sampleCount%g.advancedInterval == 0
}

func (g *GPUCollector) shouldRunProcessDetail() bool {
	if g.processDetailInterval <= 1 {
		return true
	}
	return g.sampleCount%g.processDetailInterval == 0
}

func findNvidiaSMI() string {
	candidates := []string{
		"/usr/bin/nvidia-smi",
		"/usr/local/bin/nvidia-smi",
		"/opt/nvidia/bin/nvidia-smi",
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func detectCudaVersion(nvidiaSMIPath string) string {
	if version, err := readCudaVersionFile(); err == nil && version != "" {
		return version
	}

	cmd := exec.Command(nvidiaSMIPath)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "CUDA Version") {
			parts := strings.Split(line, "CUDA Version")
			if len(parts) > 1 {
				value := strings.TrimSpace(strings.TrimLeft(parts[1], ":"))
				value = strings.Fields(value)[0]
				return value
			}
		}
	}
	return ""
}

func readCudaVersionFile() (string, error) {
	paths := []string{
		"/usr/local/cuda/version.txt",
		"/usr/local/cuda-12/version.txt",
		"/usr/local/cuda-11/version.txt",
	}
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		line := strings.TrimSpace(string(content))
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields[len(fields)-1], nil
		}
	}
	return "", fmt.Errorf("cuda version file not found")
}

// Collect collects GPU metrics for the current scrape window.
func (g *GPUCollector) Collect(now time.Time) ([]Metric, error) {
	if g == nil || g.nvidiaSMIPath == "" {
		return nil, nil
	}

	g.sampleCount++
	runAdvanced := g.shouldRunAdvanced()
	runProcessDetail := g.shouldRunProcessDetail()

	gpus, baseMetrics := g.collectGPUBase(now)
	metrics := append([]Metric{}, baseMetrics...)

	stats := g.collectGPUStats(now, gpus, runAdvanced)
	health := g.collectGPUHealth(now, gpus, runAdvanced)
	procs := g.collectGPUProcesses(now, gpus, runProcessDetail)
	events := g.collectXidEvents(now)

	metrics = append(metrics, stats...)
	metrics = append(metrics, health...)
	metrics = append(metrics, procs...)
	metrics = append(metrics, events...)
	metrics = append(metrics, g.summarizeGPU(now, metrics)...)
	metrics = append(metrics, g.collectSamplerMetrics(now, runAdvanced, runProcessDetail)...)

	return metrics, nil
}

type gpuInfo struct {
	Index         string
	UUID          string
	Name          string
	DriverVersion string
	CudaVersion   string
	Persistence   string
}

func (g *GPUCollector) collectGPUBase(now time.Time) ([]gpuInfo, []Metric) {
	metrics := []Metric{}
	gpus := []gpuInfo{}

	query := "index,uuid,name,driver_version,persistence_mode"
	output := g.runNvidiaSMIQuery("gpu_base", "--query-gpu="+query, "--format=csv,noheader")
	if output == "" {
		return gpus, metrics
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		parts := splitCSV(line)
		if len(parts) < 5 {
			continue
		}
		info := gpuInfo{
			Index:         strings.TrimSpace(parts[0]),
			UUID:          strings.TrimSpace(parts[1]),
			Name:          strings.TrimSpace(parts[2]),
			DriverVersion: strings.TrimSpace(parts[3]),
			Persistence:   strings.TrimSpace(parts[4]),
			CudaVersion:   g.cudaVersion,
		}
		gpus = append(gpus, info)

		labels := map[string]string{
			"gpu_id": info.Index,
			"uuid":   info.UUID,
			"name":   info.Name,
		}
		if info.DriverVersion != "" {
			labels["driver_version"] = info.DriverVersion
		}
		if info.CudaVersion != "" {
			labels["cuda_version"] = info.CudaVersion
		}

		metrics = append(metrics, Metric{
			Name:      "node_gpu_info",
			Type:      "gauge",
			Value:     1,
			Labels:    labels,
			Timestamp: now,
		})

		persistenceValue := 0.0
		if strings.EqualFold(info.Persistence, "enabled") || info.Persistence == "1" {
			persistenceValue = 1
		}
		metrics = append(metrics, Metric{
			Name:      "node_gpu_persistence_mode",
			Type:      "gauge",
			Value:     persistenceValue,
			Labels:    map[string]string{"gpu_id": info.Index},
			Timestamp: now,
		})
	}

	metrics = append(metrics, Metric{
		Name:      "node_gpu_count",
		Type:      "gauge",
		Value:     float64(len(gpus)),
		Timestamp: now,
	})

	return gpus, metrics
}

func (g *GPUCollector) collectGPUStats(now time.Time, gpus []gpuInfo, advanced bool) []Metric {
	metrics := []Metric{}
	if len(gpus) == 0 {
		return metrics
	}

	fullQuery := strings.Join([]string{
		"index",
		"utilization.gpu",
		"utilization.memory",
		"memory.total",
		"memory.used",
		"memory.free",
		"temperature.gpu",
		"temperature.memory",
		"power.draw",
		"power.limit",
		"clocks.gr",
		"clocks.sm",
		"clocks.memory",
		"clocks.video",
		"fan.speed",
		"pcie.link.gen.current",
		"pcie.link.width.current",
		"pcie.throughput.rx",
		"pcie.throughput.tx",
		"memory.bus_width",
	}, ",")

	output := g.runNvidiaSMIQuery("gpu_stats_full", "--query-gpu="+fullQuery, "--format=csv,noheader,nounits")
	if output == "" {
		// Compatibility fallback: some nvidia-smi versions reject the full field set.
		minimalQuery := strings.Join([]string{
			"index",
			"utilization.gpu",
			"utilization.memory",
			"memory.total",
			"memory.used",
			"memory.free",
			"temperature.gpu",
			"power.draw",
			"power.limit",
		}, ",")
		output = g.runNvidiaSMIQuery("gpu_stats_min", "--query-gpu="+minimalQuery, "--format=csv,noheader,nounits")
	}
	if output == "" || strings.Contains(strings.ToLower(output), "no devices were found") {
		return metrics
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		parts := splitCSV(line)
		if len(parts) < 9 {
			continue
		}

		gpuID := strings.TrimSpace(parts[0])
		labels := map[string]string{"gpu_id": gpuID}
		appendGPUStatsFromParts(&metrics, parts, labels, now, gpuID)
	}

	if !advanced {
		return metrics
	}

	rows := g.queryGPUFieldRows("gpu_stats_extended_util", []string{"index", "utilization.encoder", "utilization.decoder", "utilization.jpeg", "utilization.ofa", "pstate"}, true)
	for _, row := range rows {
		gpuID := strings.TrimSpace(row["index"])
		labels := map[string]string{"gpu_id": gpuID}
		addMetricIfFloat(&metrics, "node_gpu_utilization_encoder_percent", row["utilization.encoder"], labels, now)
		addMetricIfFloat(&metrics, "node_gpu_utilization_decoder_percent", row["utilization.decoder"], labels, now)
		addMetricIfFloat(&metrics, "node_gpu_utilization_jpeg_percent", row["utilization.jpeg"], labels, now)
		addMetricIfFloat(&metrics, "node_gpu_utilization_ofa_percent", row["utilization.ofa"], labels, now)

		if pstate := parsePState(row["pstate"]); pstate >= 0 {
			metrics = append(metrics, Metric{
				Name:      "node_gpu_power_state",
				Type:      "gauge",
				Value:     pstate,
				Labels:    labels,
				Timestamp: now,
			})
		}
	}

	rows = g.queryGPUFieldRows("gpu_stats_memory_extended", []string{"index", "memory.reserved", "bar1.memory.total", "bar1.memory.used", "bar1.memory.free"}, true)
	for _, row := range rows {
		gpuID := strings.TrimSpace(row["index"])
		labels := map[string]string{"gpu_id": gpuID}
		addMetricIfFloat(&metrics, "node_gpu_memory_reserved_mib", row["memory.reserved"], labels, now)
		addMetricIfFloat(&metrics, "node_gpu_bar1_memory_total_mib", row["bar1.memory.total"], labels, now)
		addMetricIfFloat(&metrics, "node_gpu_bar1_memory_used_mib", row["bar1.memory.used"], labels, now)
		addMetricIfFloat(&metrics, "node_gpu_bar1_memory_free_mib", row["bar1.memory.free"], labels, now)
	}

	rows = g.queryGPUFieldRows("gpu_stats_pcie_max", []string{"index", "pcie.link.gen.max", "pcie.link.width.max"}, true)
	for _, row := range rows {
		gpuID := strings.TrimSpace(row["index"])
		labels := map[string]string{"gpu_id": gpuID}
		addMetricIfFloat(&metrics, "node_gpu_pcie_gen_max", row["pcie.link.gen.max"], labels, now)
		addMetricIfFloat(&metrics, "node_gpu_pcie_width_max", row["pcie.link.width.max"], labels, now)

		genMax := parseFloat(row["pcie.link.gen.max"])
		widthMax := parseFloat(row["pcie.link.width.max"])
		maxBW := pcieBandwidthMBps(genMax, widthMax)
		if maxBW <= 0 {
			continue
		}
		rx := findMetricValue(metrics, "node_gpu_pcie_rx_mb_s", gpuID)
		tx := findMetricValue(metrics, "node_gpu_pcie_tx_mb_s", gpuID)
		metrics = append(metrics, Metric{
			Name:      "node_gpu_pcie_bandwidth_max_mb_s",
			Type:      "gauge",
			Value:     maxBW,
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_gpu_pcie_rx_utilization_max_percent",
			Type:      "gauge",
			Value:     clampPercent((rx / maxBW) * 100.0),
			Labels:    labels,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_gpu_pcie_tx_utilization_max_percent",
			Type:      "gauge",
			Value:     clampPercent((tx / maxBW) * 100.0),
			Labels:    labels,
			Timestamp: now,
		})
	}

	rows = g.queryGPUFieldRows("gpu_stats_nvlink", []string{"index", "nvlink.link_count"}, true)
	for _, row := range rows {
		gpuID := strings.TrimSpace(row["index"])
		labels := map[string]string{"gpu_id": gpuID}
		addMetricIfFloat(&metrics, "node_gpu_nvlink_links", row["nvlink.link_count"], labels, now)
	}

	return metrics
}

func appendGPUStatsFromParts(metrics *[]Metric, parts []string, labels map[string]string, now time.Time, gpuID string) {
	// Extended fields (when full query succeeds).
	if len(parts) >= 19 {
		addMetricIfFloat(metrics, "node_gpu_utilization_sm_percent", parts[1], labels, now)
		addMetricIfFloat(metrics, "node_gpu_utilization_memory_percent", parts[2], labels, now)
		addMetricIfFloat(metrics, "node_gpu_memory_total_mib", parts[3], labels, now)
		addMetricIfFloat(metrics, "node_gpu_memory_used_mib", parts[4], labels, now)
		addMetricIfFloat(metrics, "node_gpu_memory_free_mib", parts[5], labels, now)
		addMetricIfFloat(metrics, "node_gpu_temperature_celsius", parts[6], labels, now)
		addMetricIfFloat(metrics, "node_gpu_temperature_memory_celsius", parts[7], labels, now)
		addMetricIfFloat(metrics, "node_gpu_power_draw_watts", parts[8], labels, now)
		addMetricIfFloat(metrics, "node_gpu_power_limit_watts", parts[9], labels, now)
		addMetricIfFloat(metrics, "node_gpu_clock_graphics_mhz", parts[10], labels, now)
		addMetricIfFloat(metrics, "node_gpu_clock_sm_mhz", parts[11], labels, now)
		addMetricIfFloat(metrics, "node_gpu_clock_memory_mhz", parts[12], labels, now)
		addMetricIfFloat(metrics, "node_gpu_clock_video_mhz", parts[13], labels, now)
		addMetricIfFloat(metrics, "node_gpu_fan_speed_percent", parts[14], labels, now)
		addMetricIfFloat(metrics, "node_gpu_pcie_gen", parts[15], labels, now)
		addMetricIfFloat(metrics, "node_gpu_pcie_width", parts[16], labels, now)
		addMetricIfFloat(metrics, "node_gpu_pcie_rx_mb_s", parts[17], labels, now)
		addMetricIfFloat(metrics, "node_gpu_pcie_tx_mb_s", parts[18], labels, now)

		gen := parseFloat(parts[15])
		width := parseFloat(parts[16])
		rx := parseFloat(parts[17])
		tx := parseFloat(parts[18])
		if theoretical := pcieBandwidthMBps(gen, width); theoretical > 0 {
			*metrics = append(*metrics,
				Metric{
					Name:      "node_gpu_pcie_bandwidth_theoretical_mb_s",
					Type:      "gauge",
					Value:     theoretical,
					Labels:    labels,
					Timestamp: now,
				},
				Metric{
					Name:      "node_gpu_pcie_rx_utilization_percent",
					Type:      "gauge",
					Value:     clampPercent((rx / theoretical) * 100.0),
					Labels:    labels,
					Timestamp: now,
				},
				Metric{
					Name:      "node_gpu_pcie_tx_utilization_percent",
					Type:      "gauge",
					Value:     clampPercent((tx / theoretical) * 100.0),
					Labels:    labels,
					Timestamp: now,
				},
				Metric{
					Name:      "node_gpu_pcie_link_utilization_percent",
					Type:      "gauge",
					Value:     clampPercent(((rx + tx) / (2 * theoretical)) * 100.0),
					Labels:    labels,
					Timestamp: now,
				},
			)
		}

		if len(parts) > 19 {
			busWidth := parseFloat(parts[19])
			memClock := findMetricValue(*metrics, "node_gpu_clock_memory_mhz", gpuID)
			if busWidth > 0 && memClock > 0 {
				bandwidth := (2.0 * memClock * busWidth / 8.0) / 1000.0
				*metrics = append(*metrics, Metric{
					Name:      "node_gpu_memory_bandwidth_theoretical_gbs",
					Type:      "gauge",
					Value:     bandwidth,
					Labels:    labels,
					Timestamp: now,
				})
			}
		}
		return
	}

	// Minimal compatible fields.
	addMetricIfFloat(metrics, "node_gpu_utilization_sm_percent", parts[1], labels, now)
	addMetricIfFloat(metrics, "node_gpu_utilization_memory_percent", parts[2], labels, now)
	addMetricIfFloat(metrics, "node_gpu_memory_total_mib", parts[3], labels, now)
	addMetricIfFloat(metrics, "node_gpu_memory_used_mib", parts[4], labels, now)
	addMetricIfFloat(metrics, "node_gpu_memory_free_mib", parts[5], labels, now)
	addMetricIfFloat(metrics, "node_gpu_temperature_celsius", parts[6], labels, now)
	addMetricIfFloat(metrics, "node_gpu_power_draw_watts", parts[7], labels, now)
	addMetricIfFloat(metrics, "node_gpu_power_limit_watts", parts[8], labels, now)
}

func (g *GPUCollector) collectGPUHealth(now time.Time, gpus []gpuInfo, advanced bool) []Metric {
	metrics := []Metric{}
	if len(gpus) == 0 {
		return metrics
	}

	query := strings.Join([]string{
		"index",
		"ecc.errors.aggregate.single_bit",
		"ecc.errors.aggregate.double_bit",
		"clocks_throttle_reasons.active",
		"clocks_throttle_reasons.hw_thermal_slowdown",
		"clocks_throttle_reasons.sw_thermal_slowdown",
		"clocks_throttle_reasons.hw_power_brake_slowdown",
		"clocks_throttle_reasons.sw_power_cap",
		"clocks_throttle_reasons.gpu_idle",
		"mig.mode.current",
		"mig.mode.pending",
		"compute_mode",
	}, ",")

	output := g.runNvidiaSMIQuery("gpu_health_core", "--query-gpu="+query, "--format=csv,noheader")
	if output != "" {
		scanner := bufio.NewScanner(strings.NewReader(output))
		for scanner.Scan() {
			line := scanner.Text()
			parts := splitCSV(line)
			if len(parts) < 12 {
				continue
			}
			gpuID := strings.TrimSpace(parts[0])
			labels := map[string]string{"gpu_id": gpuID}

			addMetricIfFloatWithType(&metrics, "node_gpu_ecc_single_bit_errors_total", parts[1], labels, now, "counter")
			addMetricIfFloatWithType(&metrics, "node_gpu_ecc_double_bit_errors_total", parts[2], labels, now, "counter")

			throttleActive := parseBoolStatus(parts[3])
			metrics = append(metrics, Metric{
				Name:      "node_gpu_throttle_active",
				Type:      "gauge",
				Value:     throttleActive,
				Labels:    labels,
				Timestamp: now,
			})

			metrics = append(metrics, Metric{
				Name:      "node_gpu_throttle_thermal_active",
				Type:      "gauge",
				Value:     maxFloat(parseBoolStatus(parts[4]), parseBoolStatus(parts[5])),
				Labels:    labels,
				Timestamp: now,
			})

			metrics = append(metrics, Metric{
				Name:      "node_gpu_throttle_power_active",
				Type:      "gauge",
				Value:     maxFloat(parseBoolStatus(parts[6]), parseBoolStatus(parts[7])),
				Labels:    labels,
				Timestamp: now,
			})

			metrics = append(metrics, Metric{
				Name:      "node_gpu_throttle_idle_active",
				Type:      "gauge",
				Value:     parseBoolStatus(parts[8]),
				Labels:    labels,
				Timestamp: now,
			})

			metrics = append(metrics, Metric{
				Name:      "node_gpu_mig_enabled",
				Type:      "gauge",
				Value:     parseEnabled(parts[9]),
				Labels:    labels,
				Timestamp: now,
			})

			metrics = append(metrics, Metric{
				Name:      "node_gpu_mig_pending",
				Type:      "gauge",
				Value:     parseEnabled(parts[10]),
				Labels:    labels,
				Timestamp: now,
			})

			metrics = append(metrics, Metric{
				Name:      "node_gpu_compute_mode",
				Type:      "gauge",
				Value:     1,
				Labels:    map[string]string{"gpu_id": gpuID, "mode": strings.TrimSpace(parts[11])},
				Timestamp: now,
			})
		}
	}

	if !advanced {
		return metrics
	}

	rows := g.queryGPUFieldRows("gpu_health_reliability", []string{
		"index",
		"ecc.errors.volatile.single_bit",
		"ecc.errors.volatile.double_bit",
		"retired_pages.single_bit_retirement",
		"retired_pages.double_bit_retirement",
		"retired_pages.pending",
		"remapped_rows.correctable",
		"remapped_rows.uncorrectable",
		"remapped_rows.pending",
	}, false)
	for _, row := range rows {
		gpuID := strings.TrimSpace(row["index"])
		labels := map[string]string{"gpu_id": gpuID}
		addMetricIfFloatWithType(&metrics, "node_gpu_ecc_volatile_single_bit_errors_total", row["ecc.errors.volatile.single_bit"], labels, now, "counter")
		addMetricIfFloatWithType(&metrics, "node_gpu_ecc_volatile_double_bit_errors_total", row["ecc.errors.volatile.double_bit"], labels, now, "counter")
		addMetricIfFloatWithType(&metrics, "node_gpu_retired_pages_single_bit_total", row["retired_pages.single_bit_retirement"], labels, now, "counter")
		addMetricIfFloatWithType(&metrics, "node_gpu_retired_pages_double_bit_total", row["retired_pages.double_bit_retirement"], labels, now, "counter")
		addMetricIfFloat(&metrics, "node_gpu_retired_pages_pending", row["retired_pages.pending"], labels, now)
		addMetricIfFloatWithType(&metrics, "node_gpu_remapped_rows_correctable_total", row["remapped_rows.correctable"], labels, now, "counter")
		addMetricIfFloatWithType(&metrics, "node_gpu_remapped_rows_uncorrectable_total", row["remapped_rows.uncorrectable"], labels, now, "counter")
		addMetricIfFloat(&metrics, "node_gpu_remapped_rows_pending", row["remapped_rows.pending"], labels, now)
	}

	rows = g.queryGPUFieldRows("gpu_health_reset", []string{"index", "reset_status.reset_required", "reset_status.drain_and_reset_recommended"}, false)
	for _, row := range rows {
		gpuID := strings.TrimSpace(row["index"])
		labels := map[string]string{"gpu_id": gpuID}
		metrics = append(metrics,
			Metric{
				Name:      "node_gpu_reset_required",
				Type:      "gauge",
				Value:     parseBoolStatus(row["reset_status.reset_required"]),
				Labels:    labels,
				Timestamp: now,
			},
			Metric{
				Name:      "node_gpu_reset_recommended",
				Type:      "gauge",
				Value:     parseBoolStatus(row["reset_status.drain_and_reset_recommended"]),
				Labels:    labels,
				Timestamp: now,
			},
		)
	}

	return metrics
}

type gpuProcessSample struct {
	gpuID         string
	pid           string
	name          string
	contextType   string
	memMiB        float64
	utilSMPct     float64
	utilMemPct    float64
	utilEncPct    float64
	utilDecPct    float64
	contextActive float64

	hasMem     bool
	hasUtilSM  bool
	hasUtilMem bool
	hasUtilEnc bool
	hasUtilDec bool
}

func (g *GPUCollector) collectGPUProcesses(now time.Time, gpus []gpuInfo, detail bool) []Metric {
	metrics := []Metric{}
	if len(gpus) == 0 {
		return metrics
	}

	processes := make(map[string]*gpuProcessSample)
	ensure := func(gpuID, pid string) *gpuProcessSample {
		key := gpuID + "|" + pid
		if s, ok := processes[key]; ok {
			return s
		}
		s := &gpuProcessSample{gpuID: gpuID, pid: pid}
		processes[key] = s
		return s
	}

	query := "gpu_uuid,pid,process_name,used_memory,sm_util,mem_util"
	output := g.runNvidiaSMIQuery("gpu_process_compute_apps_ext", "--query-compute-apps="+query, "--format=csv,noheader,nounits")
	if output == "" {
		query = "gpu_uuid,pid,process_name,used_memory"
		output = g.runNvidiaSMIQuery("gpu_process_compute_apps_min", "--query-compute-apps="+query, "--format=csv,noheader,nounits")
	}

	if output != "" {
		scanner := bufio.NewScanner(strings.NewReader(output))
		for scanner.Scan() {
			line := scanner.Text()
			parts := splitCSV(line)
			if len(parts) < 4 {
				continue
			}
			uuid := strings.TrimSpace(parts[0])
			pid := strings.TrimSpace(parts[1])
			if pid == "" || pid == "-" {
				continue
			}
			gpuID := lookupGPUIndex(uuid, gpus)
			p := ensure(gpuID, pid)
			p.name = strings.TrimSpace(parts[2])
			p.contextType = "compute"

			if !isNA(parts[3]) {
				p.memMiB = parseFloat(parts[3])
				p.hasMem = true
				if p.memMiB > 0 {
					p.contextActive = 1
				}
			}
			if len(parts) > 4 && !isNA(parts[4]) {
				p.utilSMPct = parseFloat(parts[4])
				p.hasUtilSM = true
				if p.utilSMPct > 0 {
					p.contextActive = 1
				}
			}
			if len(parts) > 5 && !isNA(parts[5]) {
				p.utilMemPct = parseFloat(parts[5])
				p.hasUtilMem = true
				if p.utilMemPct > 0 {
					p.contextActive = 1
				}
			}
		}
	}

	if detail {
		for key, pm := range g.collectGPUProcessPMON() {
			p, ok := processes[key]
			if !ok {
				p = &gpuProcessSample{gpuID: pm.gpuID, pid: pm.pid}
				processes[key] = p
			}
			if p.name == "" && pm.command != "" {
				p.name = pm.command
			}
			if pm.contextType != "" {
				p.contextType = pm.contextType
			}
			if pm.hasSM {
				p.utilSMPct = pm.sm
				p.hasUtilSM = true
			}
			if pm.hasMem {
				p.utilMemPct = pm.mem
				p.hasUtilMem = true
			}
			if pm.hasEnc {
				p.utilEncPct = pm.enc
				p.hasUtilEnc = true
			}
			if pm.hasDec {
				p.utilDecPct = pm.dec
				p.hasUtilDec = true
			}
			if pm.contextActive {
				p.contextActive = 1
			}
		}
	}

	if len(processes) == 0 {
		return metrics
	}

	keys := make([]string, 0, len(processes))
	for key := range processes {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	processCount := make(map[string]int)
	contextCount := make(map[string]int)
	hotspotByGPU := make(map[string]*gpuProcessSample)

	for _, key := range keys {
		p := processes[key]
		if p == nil {
			continue
		}
		labels := map[string]string{
			"gpu_id": p.gpuID,
			"pid":    p.pid,
		}
		if p.name != "" {
			labels["process"] = p.name
		}
		if p.contextType != "" {
			labels["context_type"] = p.contextType
		}

		if p.hasMem {
			metrics = append(metrics, Metric{
				Name:      "node_gpu_process_memory_mib",
				Type:      "gauge",
				Value:     p.memMiB,
				Labels:    labels,
				Timestamp: now,
			})
		}
		if p.hasUtilSM {
			metrics = append(metrics, Metric{
				Name:      "node_gpu_process_sm_util_percent",
				Type:      "gauge",
				Value:     p.utilSMPct,
				Labels:    labels,
				Timestamp: now,
			})
		}
		if p.hasUtilMem {
			metrics = append(metrics, Metric{
				Name:      "node_gpu_process_mem_util_percent",
				Type:      "gauge",
				Value:     p.utilMemPct,
				Labels:    labels,
				Timestamp: now,
			})
		}
		if p.hasUtilEnc {
			metrics = append(metrics, Metric{
				Name:      "node_gpu_process_encoder_util_percent",
				Type:      "gauge",
				Value:     p.utilEncPct,
				Labels:    labels,
				Timestamp: now,
			})
		}
		if p.hasUtilDec {
			metrics = append(metrics, Metric{
				Name:      "node_gpu_process_decoder_util_percent",
				Type:      "gauge",
				Value:     p.utilDecPct,
				Labels:    labels,
				Timestamp: now,
			})
		}
		metrics = append(metrics, Metric{
			Name:      "node_gpu_process_context_active",
			Type:      "gauge",
			Value:     p.contextActive,
			Labels:    labels,
			Timestamp: now,
		})

		processCount[p.gpuID]++
		if p.contextActive > 0 {
			contextCount[p.gpuID]++
		}
		if cur := hotspotByGPU[p.gpuID]; cur == nil || p.utilSMPct > cur.utilSMPct {
			hotspotByGPU[p.gpuID] = p
		}
	}

	for gpuID, count := range processCount {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_process_count",
			Type:      "gauge",
			Value:     float64(count),
			Labels:    map[string]string{"gpu_id": gpuID},
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_gpu_context_count",
			Type:      "gauge",
			Value:     float64(contextCount[gpuID]),
			Labels:    map[string]string{"gpu_id": gpuID},
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_gpu_kernel_active_contexts",
			Type:      "gauge",
			Value:     float64(contextCount[gpuID]),
			Labels:    map[string]string{"gpu_id": gpuID},
			Timestamp: now,
		})
	}

	for gpuID, hotspot := range hotspotByGPU {
		if hotspot == nil || hotspot.utilSMPct <= 0 {
			continue
		}
		labels := map[string]string{"gpu_id": gpuID, "pid": hotspot.pid}
		if hotspot.name != "" {
			labels["process"] = hotspot.name
		}
		metrics = append(metrics, Metric{
			Name:      "node_gpu_kernel_hotspot_sm_util_percent",
			Type:      "gauge",
			Value:     hotspot.utilSMPct,
			Labels:    labels,
			Timestamp: now,
		})
	}

	return metrics
}

type gpuPMONSample struct {
	gpuID         string
	pid           string
	contextType   string
	command       string
	sm            float64
	mem           float64
	enc           float64
	dec           float64
	hasSM         bool
	hasMem        bool
	hasEnc        bool
	hasDec        bool
	contextActive bool
}

func (g *GPUCollector) collectGPUProcessPMON() map[string]*gpuPMONSample {
	out := make(map[string]*gpuPMONSample)
	output := g.runNvidiaSMIQuery("gpu_process_pmon", "pmon", "-c", "1", "-s", "um")
	if output == "" {
		return out
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}
		if _, err := strconv.Atoi(parts[0]); err != nil {
			continue
		}
		pid := strings.TrimSpace(parts[1])
		if pid == "" || pid == "-" {
			continue
		}

		sample := &gpuPMONSample{
			gpuID:       strings.TrimSpace(parts[0]),
			pid:         pid,
			contextType: strings.TrimSpace(parts[2]),
			command:     strings.Join(parts[7:], " "),
		}
		if !isNA(parts[3]) {
			sample.sm = parseFloat(parts[3])
			sample.hasSM = true
		}
		if !isNA(parts[4]) {
			sample.mem = parseFloat(parts[4])
			sample.hasMem = true
		}
		if !isNA(parts[5]) {
			sample.enc = parseFloat(parts[5])
			sample.hasEnc = true
		}
		if !isNA(parts[6]) {
			sample.dec = parseFloat(parts[6])
			sample.hasDec = true
		}
		if strings.Contains(strings.ToUpper(sample.contextType), "C") || sample.sm > 0 || sample.mem > 0 {
			sample.contextActive = true
		}
		out[sample.gpuID+"|"+sample.pid] = sample
	}
	return out
}

func (g *GPUCollector) collectXidEvents(now time.Time) []Metric {
	files := []string{
		"/var/log/syslog",
		"/var/log/messages",
		"/var/log/kern.log",
	}

	deltas := make(map[gpuEventKey]uint64)

	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		stat, err := file.Stat()
		if err != nil {
			file.Close()
			continue
		}
		offset := g.lastOffsets[path]
		if offset > stat.Size() {
			offset = 0
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			file.Close()
			continue
		}

		buffer := bytes.NewBuffer(nil)
		if _, err := io.Copy(buffer, file); err != nil {
			file.Close()
			continue
		}
		g.lastOffsets[path] = stat.Size()
		file.Close()

		scanner := bufio.NewScanner(buffer)
		for scanner.Scan() {
			line := scanner.Text()
			for _, evt := range parseGPUKernelEvents(line) {
				deltas[evt]++
			}
		}
	}

	for key, delta := range deltas {
		g.kernelEventTotals[key] += delta
	}

	if len(g.kernelEventTotals) == 0 {
		return nil
	}

	type xidRollupKey struct {
		gpuID string
		code  string
	}
	xidRollup := make(map[xidRollupKey]uint64)
	uvmRollup := make(map[string]uint64)
	resetRollup := make(map[string]uint64)
	reliabilityRollup := make(map[string]uint64)

	eventKeys := make([]gpuEventKey, 0, len(g.kernelEventTotals))
	for key := range g.kernelEventTotals {
		eventKeys = append(eventKeys, key)
	}
	sort.Slice(eventKeys, func(i, j int) bool {
		li := eventKeys[i]
		lj := eventKeys[j]
		if li.gpuID != lj.gpuID {
			return li.gpuID < lj.gpuID
		}
		if li.eventType != lj.eventType {
			return li.eventType < lj.eventType
		}
		if li.code != lj.code {
			return li.code < lj.code
		}
		return li.severity < lj.severity
	})

	for _, key := range eventKeys {
		total := g.kernelEventTotals[key]
		switch key.eventType {
		case "xid":
			xidRollup[xidRollupKey{gpuID: key.gpuID, code: key.code}] += total
			reliabilityRollup[key.gpuID] += total
		case "uvm_fault":
			uvmRollup[key.gpuID] += total
		case "reset":
			resetRollup[key.gpuID] += total
			reliabilityRollup[key.gpuID] += total
		case "ecc":
			reliabilityRollup[key.gpuID] += total
		}
	}

	metrics := make([]Metric, 0, len(eventKeys)+len(xidRollup)+len(uvmRollup)+len(resetRollup)+len(reliabilityRollup))
	for _, key := range eventKeys {
		total := g.kernelEventTotals[key]
		labels := map[string]string{
			"event_type": key.eventType,
			"severity":   key.severity,
		}
		if key.gpuID != "" {
			labels["gpu_id"] = key.gpuID
		}
		if key.code != "" {
			labels["code"] = key.code
		}
		metrics = append(metrics, Metric{
			Name:      "node_gpu_event_total",
			Type:      "counter",
			Value:     float64(total),
			Labels:    labels,
			Timestamp: now,
		})
	}

	for key, total := range xidRollup {
		labels := map[string]string{"code": key.code}
		if key.gpuID != "" {
			labels["gpu_id"] = key.gpuID
		}
		metrics = append(metrics, Metric{
			Name:      "node_gpu_xid_errors_total",
			Type:      "counter",
			Value:     float64(total),
			Labels:    labels,
			Timestamp: now,
		})
	}
	for gpuID, total := range uvmRollup {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_uvm_faults_total",
			Type:      "counter",
			Value:     float64(total),
			Labels:    map[string]string{"gpu_id": gpuID},
			Timestamp: now,
		})
	}
	for gpuID, total := range resetRollup {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_reset_events_total",
			Type:      "counter",
			Value:     float64(total),
			Labels:    map[string]string{"gpu_id": gpuID},
			Timestamp: now,
		})
	}
	for gpuID, total := range reliabilityRollup {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_reliability_events_total",
			Type:      "counter",
			Value:     float64(total),
			Labels:    map[string]string{"gpu_id": gpuID},
			Timestamp: now,
		})
	}

	return metrics
}

func parseGPUKernelEvents(line string) []gpuEventKey {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	gpuID := extractGPUIndexFromLine(line)
	if gpuID == "" {
		gpuID = "unknown"
	}

	out := make([]gpuEventKey, 0, 2)
	if match := xidRegex.FindStringSubmatch(line); len(match) > 1 {
		code := match[1]
		out = append(out, gpuEventKey{
			eventType: "xid",
			severity:  xidSeverity(code),
			gpuID:     gpuID,
			code:      code,
		})
	}
	if uvmFaultRegex.MatchString(line) {
		out = append(out, gpuEventKey{
			eventType: "uvm_fault",
			severity:  "warning",
			gpuID:     gpuID,
		})
	}
	if gpuResetRegex.MatchString(line) {
		out = append(out, gpuEventKey{
			eventType: "reset",
			severity:  "critical",
			gpuID:     gpuID,
		})
	}
	if eccEventRegex.MatchString(line) {
		out = append(out, gpuEventKey{
			eventType: "ecc",
			severity:  "warning",
			gpuID:     gpuID,
		})
	}
	if throttleEventRegex.MatchString(line) {
		out = append(out, gpuEventKey{
			eventType: "throttle",
			severity:  "warning",
			gpuID:     gpuID,
		})
	}
	return out
}

func extractGPUIndexFromLine(line string) string {
	if match := gpuIndexRegex.FindStringSubmatch(line); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	if match := gpuDashIndexRegex.FindStringSubmatch(line); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func xidSeverity(code string) string {
	switch code {
	case "13", "31", "43", "48", "63", "79", "94":
		return "critical"
	case "8", "14", "32", "45", "74":
		return "warning"
	default:
		return "info"
	}
}

func (g *GPUCollector) collectSamplerMetrics(now time.Time, advanced, processDetail bool) []Metric {
	metrics := make([]Metric, 0, len(g.queryDurationMs)*3+6)

	metrics = append(metrics,
		Metric{
			Name:      "node_gpu_sampler_advanced_interval_samples",
			Type:      "gauge",
			Value:     float64(g.advancedInterval),
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_sampler_process_detail_interval_samples",
			Type:      "gauge",
			Value:     float64(g.processDetailInterval),
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_sampler_advanced_cycle_active",
			Type:      "gauge",
			Value:     boolToFloat(advanced),
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_sampler_process_detail_cycle_active",
			Type:      "gauge",
			Value:     boolToFloat(processDetail),
			Timestamp: now,
		},
	)

	keys := make([]string, 0, len(g.queryDurationMs))
	for key := range g.queryDurationMs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		labels := map[string]string{"query": key}
		metrics = append(metrics,
			Metric{
				Name:      "node_gpu_sampler_query_duration_ms",
				Type:      "gauge",
				Value:     g.queryDurationMs[key],
				Labels:    labels,
				Timestamp: now,
			},
			Metric{
				Name:      "node_gpu_sampler_query_errors_total",
				Type:      "counter",
				Value:     float64(g.queryErrorsTotal[key]),
				Labels:    labels,
				Timestamp: now,
			},
			Metric{
				Name:      "node_gpu_sampler_query_timeouts_total",
				Type:      "counter",
				Value:     float64(g.queryTimeoutsTotal[key]),
				Labels:    labels,
				Timestamp: now,
			},
		)
	}

	return metrics
}

func boolToFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func (g *GPUCollector) summarizeGPU(now time.Time, metricsSnapshot []Metric) []Metric {
	metrics := []Metric{}

	var (
		utilSum     float64
		utilCount   float64
		utilEncSum  float64
		utilEncCnt  float64
		utilDecSum  float64
		utilDecCnt  float64
		memUsedSum  float64
		memTotalSum float64
		tempMax     float64
		powerSum    float64
		powerLimit  float64
		throttle    float64
		thermal     float64
		powerThrot  float64
		pcieRxSum   float64
		pcieTxSum   float64
		pcieUtilSum float64
		pcieUtilCnt float64
		procSum     float64
		contextSum  float64
		hotspotMax  float64
	)

	for _, metric := range metricsSnapshot {
		switch metric.Name {
		case "node_gpu_utilization_sm_percent":
			utilSum += metric.Value
			utilCount++
		case "node_gpu_utilization_encoder_percent":
			utilEncSum += metric.Value
			utilEncCnt++
		case "node_gpu_utilization_decoder_percent":
			utilDecSum += metric.Value
			utilDecCnt++
		case "node_gpu_memory_used_mib":
			memUsedSum += metric.Value
		case "node_gpu_memory_total_mib":
			memTotalSum += metric.Value
		case "node_gpu_temperature_celsius":
			if metric.Value > tempMax {
				tempMax = metric.Value
			}
		case "node_gpu_power_draw_watts":
			powerSum += metric.Value
		case "node_gpu_power_limit_watts":
			powerLimit += metric.Value
		case "node_gpu_throttle_active":
			if metric.Value > 0 {
				throttle = 1
			}
		case "node_gpu_throttle_thermal_active":
			if metric.Value > 0 {
				thermal = 1
			}
		case "node_gpu_throttle_power_active":
			if metric.Value > 0 {
				powerThrot = 1
			}
		case "node_gpu_pcie_rx_mb_s":
			pcieRxSum += metric.Value
		case "node_gpu_pcie_tx_mb_s":
			pcieTxSum += metric.Value
		case "node_gpu_pcie_link_utilization_percent":
			pcieUtilSum += metric.Value
			pcieUtilCnt++
		case "node_gpu_process_count":
			procSum += metric.Value
		case "node_gpu_context_count":
			contextSum += metric.Value
		case "node_gpu_kernel_hotspot_sm_util_percent":
			if metric.Value > hotspotMax {
				hotspotMax = metric.Value
			}
		}
	}

	if utilCount > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_utilization_sm_avg_percent",
			Type:      "gauge",
			Value:     utilSum / utilCount,
			Timestamp: now,
		})
	}
	if utilEncCnt > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_utilization_encoder_avg_percent",
			Type:      "gauge",
			Value:     utilEncSum / utilEncCnt,
			Timestamp: now,
		})
	}
	if utilDecCnt > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_utilization_decoder_avg_percent",
			Type:      "gauge",
			Value:     utilDecSum / utilDecCnt,
			Timestamp: now,
		})
	}
	if memTotalSum > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_memory_used_percent",
			Type:      "gauge",
			Value:     (memUsedSum / memTotalSum) * 100.0,
			Timestamp: now,
		})
	}
	if memUsedSum > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_memory_used_total_mib",
			Type:      "gauge",
			Value:     memUsedSum,
			Timestamp: now,
		})
	}
	if memTotalSum > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_memory_total_all_mib",
			Type:      "gauge",
			Value:     memTotalSum,
			Timestamp: now,
		})
	}
	if tempMax > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_temperature_max_celsius",
			Type:      "gauge",
			Value:     tempMax,
			Timestamp: now,
		})
	}
	if powerSum > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_power_draw_total_watts",
			Type:      "gauge",
			Value:     powerSum,
			Timestamp: now,
		})
	}
	if powerLimit > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_power_limit_total_watts",
			Type:      "gauge",
			Value:     powerLimit,
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_gpu_power_draw_percent",
			Type:      "gauge",
			Value:     (powerSum / powerLimit) * 100.0,
			Timestamp: now,
		})
	}
	if pcieUtilCnt > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_pcie_link_utilization_avg_percent",
			Type:      "gauge",
			Value:     pcieUtilSum / pcieUtilCnt,
			Timestamp: now,
		})
	}
	if hotspotMax > 0 {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_kernel_hotspot_peak_sm_util_percent",
			Type:      "gauge",
			Value:     hotspotMax,
			Timestamp: now,
		})
	}

	metrics = append(metrics,
		Metric{
			Name:      "node_gpu_throttle_active_any",
			Type:      "gauge",
			Value:     throttle,
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_throttle_thermal_any",
			Type:      "gauge",
			Value:     thermal,
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_throttle_power_any",
			Type:      "gauge",
			Value:     powerThrot,
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_pcie_rx_total_mb_s",
			Type:      "gauge",
			Value:     pcieRxSum,
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_pcie_tx_total_mb_s",
			Type:      "gauge",
			Value:     pcieTxSum,
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_process_total",
			Type:      "gauge",
			Value:     procSum,
			Timestamp: now,
		},
		Metric{
			Name:      "node_gpu_context_total",
			Type:      "gauge",
			Value:     contextSum,
			Timestamp: now,
		},
	)

	return metrics
}

func (g *GPUCollector) runNvidiaSMIQuery(queryID string, args ...string) string {
	if g == nil || g.nvidiaSMIPath == "" {
		return ""
	}
	if queryID == "" {
		queryID = "unknown"
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), g.queryTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, g.nvidiaSMIPath, args...)
	output, err := cmd.Output()
	g.queryDurationMs[queryID] = time.Since(start).Seconds() * 1000.0
	if err != nil {
		g.queryErrorsTotal[queryID]++
		if ctx.Err() == context.DeadlineExceeded {
			g.queryTimeoutsTotal[queryID]++
		}
		return ""
	}
	return strings.TrimSpace(string(output))
}

func (g *GPUCollector) queryGPUFieldRows(queryID string, fields []string, noUnits bool) []map[string]string {
	if len(fields) < 2 {
		return nil
	}
	format := "csv,noheader"
	if noUnits {
		format += ",nounits"
	}
	query := "--query-gpu=" + strings.Join(fields, ",")
	output := g.runNvidiaSMIQuery(queryID, query, "--format="+format)
	if output == "" {
		return nil
	}

	rows := make([]map[string]string, 0, 8)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		parts := splitCSV(scanner.Text())
		if len(parts) != len(fields) {
			continue
		}
		row := make(map[string]string, len(fields))
		for i, field := range fields {
			row[field] = strings.TrimSpace(parts[i])
		}
		rows = append(rows, row)
	}
	return rows
}

func (g *GPUCollector) runNvidiaSMI(args ...string) string {
	return g.runNvidiaSMIQuery("legacy", args...)
}

func pcieBandwidthMBps(gen, width float64) float64 {
	if gen <= 0 || width <= 0 {
		return 0
	}
	laneMBps := map[int]float64{
		1: 250.0,
		2: 500.0,
		3: 984.6,
		4: 1969.2,
		5: 3938.5,
		6: 7877.0,
	}
	lane, ok := laneMBps[int(gen)]
	if !ok {
		return 0
	}
	return lane * width
}

func parsePState(raw string) float64 {
	v := strings.TrimSpace(strings.ToUpper(raw))
	if v == "" || v == "N/A" || strings.Contains(v, "NOT SUPPORTED") {
		return -1
	}
	v = strings.TrimPrefix(v, "P")
	if v == "" {
		return -1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return -1
	}
	return float64(n)
}

func splitCSV(line string) []string {
	parts := strings.Split(line, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func addMetricIfFloat(metrics *[]Metric, name, raw string, labels map[string]string, ts time.Time) {
	addMetricIfFloatWithType(metrics, name, raw, labels, ts, "gauge")
}

func addMetricIfFloatWithType(metrics *[]Metric, name, raw string, labels map[string]string, ts time.Time, metricType string) {
	if isNA(raw) {
		return
	}
	value := parseFloat(raw)
	*metrics = append(*metrics, Metric{
		Name:      name,
		Type:      metricType,
		Value:     value,
		Labels:    labels,
		Timestamp: ts,
	})
}

func parseFloat(raw string) float64 {
	value := strings.TrimSpace(strings.Trim(raw, "%"))
	value = strings.Trim(value, "[]")
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func isNA(raw string) bool {
	value := strings.TrimSpace(strings.ToUpper(strings.Trim(raw, "[]")))
	if value == "" {
		return true
	}
	switch value {
	case "N/A", "-", "UNKNOWN":
		return true
	}
	if strings.Contains(value, "NOT SUPPORTED") {
		return true
	}
	return false
}

func parseBoolStatus(raw string) float64 {
	value := strings.TrimSpace(strings.ToLower(raw))
	value = strings.Trim(value, "[]")
	switch value {
	case "active", "1", "yes", "true", "enabled", "required", "recommended":
		return 1
	case "not active", "0", "no", "false", "disabled", "not required", "not recommended":
		return 0
	default:
		return 0
	}
}

func parseEnabled(raw string) float64 {
	value := strings.TrimSpace(strings.ToLower(strings.Trim(raw, "[]")))
	if value == "enabled" || value == "1" || value == "true" {
		return 1
	}
	return 0
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func lookupGPUIndex(uuid string, gpus []gpuInfo) string {
	for _, gpu := range gpus {
		if gpu.UUID == uuid {
			return gpu.Index
		}
	}
	return uuid
}

func findMetricValue(metrics []Metric, name, gpuID string) float64 {
	for _, metric := range metrics {
		if metric.Name == name && metric.Labels != nil && metric.Labels["gpu_id"] == gpuID {
			return metric.Value
		}
	}
	return 0
}
