// Package probe implements GPU metrics collection using low-overhead tooling.
package probe

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	gpuDisabledEnv = "SRE_COLLECTOR_GPU_DISABLED"
)

var xidRegex = regexp.MustCompile(`Xid\s+(\d+)`)

// GPUCollector gathers NVIDIA GPU telemetry via nvidia-smi and kernel logs.
type GPUCollector struct {
	nvidiaSMIPath string
	lastOffsets   map[string]int64
	cudaVersion   string
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
		nvidiaSMIPath: path,
		lastOffsets:   make(map[string]int64),
	}
	collector.cudaVersion = detectCudaVersion(path)
	return collector
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

	gpus, baseMetrics := g.collectGPUBase(now)
	metrics := append([]Metric{}, baseMetrics...)

	stats := g.collectGPUStats(now, gpus)
	health := g.collectGPUHealth(now, gpus)
	procs := g.collectGPUProcesses(now, gpus)
	xids := g.collectXidEvents(now)

	metrics = append(metrics, stats...)
	metrics = append(metrics, health...)
	metrics = append(metrics, procs...)
	metrics = append(metrics, xids...)
	metrics = append(metrics, g.summarizeGPU(now, metrics)...)

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
	output := g.runNvidiaSMI("--query-gpu="+query, "--format=csv,noheader")
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

func (g *GPUCollector) collectGPUStats(now time.Time, gpus []gpuInfo) []Metric {
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

	output := g.runNvidiaSMI("--query-gpu="+fullQuery, "--format=csv,noheader,nounits")
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
		output = g.runNvidiaSMI("--query-gpu="+minimalQuery, "--format=csv,noheader,nounits")
	}
	if output == "" || strings.Contains(output, "No running compute processes") {
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

func (g *GPUCollector) collectGPUHealth(now time.Time, gpus []gpuInfo) []Metric {
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

	output := g.runNvidiaSMI("--query-gpu="+query, "--format=csv,noheader")
	if output == "" {
		return metrics
	}

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

	return metrics
}

func (g *GPUCollector) collectGPUProcesses(now time.Time, gpus []gpuInfo) []Metric {
	metrics := []Metric{}
	if len(gpus) == 0 {
		return metrics
	}

	query := "gpu_uuid,pid,process_name,used_memory,sm_util,mem_util"
	output := g.runNvidiaSMI("--query-compute-apps="+query, "--format=csv,noheader,nounits")
	if output == "" {
		return metrics
	}

	processCount := make(map[string]int)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		parts := splitCSV(line)
		if len(parts) < 4 {
			continue
		}
		uuid := strings.TrimSpace(parts[0])
		pid := strings.TrimSpace(parts[1])
		procName := strings.TrimSpace(parts[2])
		usedMem := strings.TrimSpace(parts[3])

		gpuID := lookupGPUIndex(uuid, gpus)
		labels := map[string]string{
			"gpu_id": gpuID,
			"pid":    pid,
		}
		if procName != "" {
			labels["process"] = procName
		}

		addMetricIfFloat(&metrics, "node_gpu_process_memory_mib", usedMem, labels, now)

		if len(parts) > 4 {
			addMetricIfFloat(&metrics, "node_gpu_process_sm_util_percent", parts[4], labels, now)
		}
		if len(parts) > 5 {
			addMetricIfFloat(&metrics, "node_gpu_process_mem_util_percent", parts[5], labels, now)
		}

		processCount[gpuID]++
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
			Value:     float64(count),
			Labels:    map[string]string{"gpu_id": gpuID},
			Timestamp: now,
		})
	}

	return metrics
}

func (g *GPUCollector) collectXidEvents(now time.Time) []Metric {
	metrics := []Metric{}
	files := []string{
		"/var/log/syslog",
		"/var/log/messages",
		"/var/log/kern.log",
	}
	counts := make(map[string]int)

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
			if !strings.Contains(line, "Xid") {
				continue
			}
			match := xidRegex.FindStringSubmatch(line)
			if len(match) < 2 {
				continue
			}
			code := match[1]
			counts[code]++
		}
	}

	for code, count := range counts {
		metrics = append(metrics, Metric{
			Name:      "node_gpu_xid_errors_total",
			Type:      "counter",
			Value:     float64(count),
			Labels:    map[string]string{"code": code},
			Timestamp: now,
		})
	}

	return metrics
}

func (g *GPUCollector) summarizeGPU(now time.Time, metricsSnapshot []Metric) []Metric {
	metrics := []Metric{}

	var (
		utilSum     float64
		utilCount   float64
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
		procSum     float64
	)

	for _, metric := range metricsSnapshot {
		switch metric.Name {
		case "node_gpu_utilization_sm_percent":
			utilSum += metric.Value
			utilCount++
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
		case "node_gpu_process_count":
			procSum += metric.Value
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
	)

	return metrics
}

func (g *GPUCollector) runNvidiaSMI(args ...string) string {
	cmdArgs := append([]string{}, args...)
	cmd := exec.Command(g.nvidiaSMIPath, cmdArgs...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
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
	if value == "" || value == "N/A" {
		return 0
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func isNA(raw string) bool {
	value := strings.TrimSpace(strings.ToUpper(raw))
	return value == "" || value == "N/A"
}
func parseBoolStatus(raw string) float64 {
	value := strings.TrimSpace(strings.ToLower(raw))
	switch value {
	case "active", "1", "yes", "true":
		return 1
	case "not active", "0", "no", "false":
		return 0
	default:
		return 0
	}
}

func parseEnabled(raw string) float64 {
	value := strings.TrimSpace(strings.ToLower(raw))
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
