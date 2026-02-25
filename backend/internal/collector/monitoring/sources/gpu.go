package sources

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GPUSource collects GPU/AI server metrics
type GPUSource struct {
	BaseSource
	config GPUConfig
	logger *zap.Logger

	nvidiaSMIPath string
	gpuCount      int
	lastQueryTime int64
}

// NewGPUSource creates a new GPU metrics source
func NewGPUSource(config GPUConfig, logger *zap.Logger) (*GPUSource, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("gpu source is disabled")
	}

	source := &GPUSource{
		BaseSource: BaseSource{
			name:    "gpu",
			enabled: config.Enabled,
		},
		config:        config,
		logger:        logger.With(zap.String("source", "gpu")),
		nvidiaSMIPath: "",
		gpuCount:      0,
	}

	// Detect available GPU tools
	source.detectGPUTools()

	if source.nvidiaSMIPath == "" && !config.IncludeAMD && !config.IncludeIntel {
		return nil, fmt.Errorf("no GPU detection tools found (nvidia-smi not available)")
	}

	return source, nil
}

func (g *GPUSource) Name() string {
	return "gpu"
}

func (g *GPUSource) Start(ctx context.Context) error {
	g.setStatus(true, true, "")
	g.logger.Info("GPU source started", zap.String("nvidia_smi", g.nvidiaSMIPath))
	return nil
}

func (g *GPUSource) Stop() error {
	g.setStatus(false, false, "")
	g.logger.Info("GPU source stopped")
	return nil
}

// detectGPUTools detects available GPU monitoring tools
func (g *GPUSource) detectGPUTools() {
	// Check for nvidia-smi
	path, err := exec.LookPath("nvidia-smi")
	if err == nil {
		g.nvidiaSMIPath = path
		g.logger.Debug("Found nvidia-smi", zap.String("path", path))
		return
	}

	// Check common paths
	commonPaths := []string{
		"/usr/bin/nvidia-smi",
		"/usr/local/bin/nvidia-smi",
		"/opt/nvidia/bin/nvidia-smi",
	}

	for _, p := range commonPaths {
		if _, err := os.Stat(p); err == nil {
			g.nvidiaSMIPath = p
			g.logger.Debug("Found nvidia-smi", zap.String("path", p))
			return
		}
	}
}

// Collect collects GPU metrics
func (g *GPUSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	metrics := []*proto.Metric{}
	now := timestamppb.Now()

	if g.nvidiaSMIPath != "" {
		nvidiaMetrics := g.collectNVIDIA(now)
		metrics = append(metrics, nvidiaMetrics...)
	}

	// Check for AMD GPUs
	if g.config.IncludeAMD {
		amdMetrics := g.collectAMD(now)
		metrics = append(metrics, amdMetrics...)
	}

	// Check for Intel GPUs
	if g.config.IncludeIntel {
		intelMetrics := g.collectIntel(now)
		metrics = append(metrics, intelMetrics...)
	}

	g.setStatus(true, true, "")

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "gpu",
		CollectedAt: now,
	}, nil
}

// collectNVIDIA collects NVIDIA GPU metrics using nvidia-smi
func (g *GPUSource) collectNVIDIA(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// First get GPU count
	if g.gpuCount == 0 {
		count, err := g.getNVIDIA_gpuCount()
		if err != nil {
			return metrics
		}
		g.gpuCount = count
	}

	// Collect base GPU info
	gpuInfo := g.runNvidiaSMI("--query-gpu=index,name,uuid,persistence_mode,driver_version", "--format=csv,noheader")
	if gpuInfo == "" {
		return metrics
	}

	scanner := bufio.NewScanner(strings.NewReader(gpuInfo))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			continue
		}

		index := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		uuid := strings.TrimSpace(parts[2])
		persistence := strings.TrimSpace(parts[3])

		labels := []*proto.MetricLabel{
			{Key: "gpu_id", Value: index},
			{Key: "name", Value: name},
			{Key: "uuid", Value: uuid},
		}

		// GPU persistence mode
		persistenceEnabled := 0.0
		if strings.ToLower(persistence) == "enabled" || persistence == "1" {
			persistenceEnabled = 1
		}
		metrics = append(metrics, &proto.Metric{
			Name:   "gpu.nvidia.persistence_mode",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels,
			Points: []*proto.MetricPoint{{Timestamp: now, Value: persistenceEnabled}},
		})

		// Collect detailed metrics for each GPU
		detailedMetrics := g.collectNVIDIADetailed(index, name, now)
		metrics = append(metrics, detailedMetrics...)
	}

	return metrics
}

// collectNVIDIADetailed collects detailed metrics for a specific GPU
func (g *GPUSource) collectNVIDIADetailed(gpuID, gpuName string, now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}
	labels := []*proto.MetricLabel{{Key: "gpu_id", Value: gpuID}, {Key: "name", Value: gpuName}}

	// Get utilization metrics
	utilization := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=utilization.gpu,utilization.memory", "--format=csv,noheader,nounits")
	if utilization != "" {
		parts := strings.Split(strings.TrimSpace(utilization), ",")
		if len(parts) >= 2 {
			if gpuUtil, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
				metrics = append(metrics, &proto.Metric{
					Name:   "gpu.nvidia.utilization.gpu",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: gpuUtil}},
				})
			}
			if memUtil, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
				metrics = append(metrics, &proto.Metric{
					Name:   "gpu.nvidia.utilization.memory",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: memUtil}},
				})
			}
		}
	}

	// Get memory info
	memoryInfo := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=memory.total,memory.used,memory.free", "--format=csv,noheader,nounits")
	if memoryInfo != "" {
		parts := strings.Split(strings.TrimSpace(memoryInfo), ",")
		if len(parts) >= 3 {
			if totalMem, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
				metrics = append(metrics, &proto.Metric{
					Name:   "gpu.nvidia.memory.total",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: totalMem}}, // MiB
				})
			}
			if usedMem, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
				metrics = append(metrics, &proto.Metric{
					Name:   "gpu.nvidia.memory.used",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: usedMem}}, // MiB
				})
			}
			if freeMem, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64); err == nil {
				metrics = append(metrics, &proto.Metric{
					Name:   "gpu.nvidia.memory.free",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: freeMem}}, // MiB
				})
			}
		}
	}

	// Get temperature
	if g.config.CollectTemp {
		tempInfo := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=temperature.gpu,temperature.memory", "--format=csv,noheader,nounits")
		if tempInfo != "" {
			parts := strings.Split(strings.TrimSpace(tempInfo), ",")
			if len(parts) >= 1 {
				if gpuTemp, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.temperature.gpu",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: gpuTemp}},
					})
				}
			}
			if len(parts) >= 2 {
				if memTemp, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.temperature.memory",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: memTemp}},
					})
				}
			}
		}

		// Temperature limits
		tempLimit := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=temperature.gpu_slow_limit,temperature.gpu_shutdown_limit", "--format=csv,noheader,nounits")
		if tempLimit != "" {
			parts := strings.Split(strings.TrimSpace(tempLimit), ",")
			if len(parts) >= 1 {
				if slowLimit, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil && slowLimit > 0 {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.temperature.slow_limit",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: slowLimit}},
					})
				}
			}
			if len(parts) >= 2 {
				if shutdownLimit, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil && shutdownLimit > 0 {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.temperature.shutdown_limit",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: shutdownLimit}},
					})
				}
			}
		}
	}

	// Get power consumption
	if g.config.CollectPower {
		powerInfo := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=power.draw,power.limit,enforced.power.limit", "--format=csv,noheader,nounits")
		if powerInfo != "" {
			parts := strings.Split(strings.TrimSpace(powerInfo), ",")
			if len(parts) >= 1 {
				if powerDraw, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.power.draw",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: powerDraw}},
					})
				}
			}
			if len(parts) >= 2 {
				if powerLimit, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.power.limit",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: powerLimit}},
					})
				}
			}
		}
	}

	// Get clock speeds
	if g.config.CollectClocks {
		clockInfo := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=clocks.gr,clocks.sm,clocks.memory,clocks.video", "--format=csv,noheader,nounits")
		if clockInfo != "" {
			parts := strings.Split(strings.TrimSpace(clockInfo), ",")
			if len(parts) >= 1 {
				if grClock, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.clock.graphics",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: grClock}},
					})
				}
			}
			if len(parts) >= 2 {
				if smClock, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.clock.sm",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: smClock}},
					})
				}
			}
			if len(parts) >= 3 {
				if memClock, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.clock.memory",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: memClock}},
					})
				}
			}
		}
	}

	// Get PCIe throughput
	if g.config.CollectPcie {
		pcieInfo := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=pcie.link.gen.current,pcie.link.width.current,pcie.throughput.gp_util.tx,pcie.throughput.gp_util.rx", "--format=csv,noheader,nounits")
		if pcieInfo != "" {
			parts := strings.Split(strings.TrimSpace(pcieInfo), ",")
			if len(parts) >= 1 {
				if gen, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.pcie.gen",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: gen}},
					})
				}
			}
			if len(parts) >= 2 {
				if width, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.pcie.width",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: width}},
					})
				}
			}
			if len(parts) >= 3 {
				if tx, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.pcie.throughput.tx",
						Type:   proto.MetricType_METRIC_TYPE_COUNTER,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: tx}},
					})
				}
			}
			if len(parts) >= 4 {
				if rx, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.pcie.throughput.rx",
						Type:   proto.MetricType_METRIC_TYPE_COUNTER,
						Labels: labels,
						Points: []*proto.MetricPoint{{Timestamp: now, Value: rx}},
					})
				}
			}
		}
	}

	// Get ECC errors
	eccInfo := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=ecc.errors.aggregate.single_bit,ecc.errors.aggregate.double_bit", "--format=csv,noheader,nounits")
	if eccInfo != "" {
		parts := strings.Split(strings.TrimSpace(eccInfo), ",")
		if len(parts) >= 1 {
			if sbErr, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err == nil {
				metrics = append(metrics, &proto.Metric{
					Name:   "gpu.nvidia.ecc.single_bit_errors",
					Type:   proto.MetricType_METRIC_TYPE_COUNTER,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: sbErr}},
				})
			}
		}
		if len(parts) >= 2 {
			if dbErr, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
				metrics = append(metrics, &proto.Metric{
					Name:   "gpu.nvidia.ecc.double_bit_errors",
					Type:   proto.MetricType_METRIC_TYPE_COUNTER,
					Labels: labels,
					Points: []*proto.MetricPoint{{Timestamp: now, Value: dbErr}},
				})
			}
		}
	}

	// Get fan speed
	fanInfo := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=fan.speed", "--format=csv,noheader,nounits")
	if fanInfo != "" {
		if fanSpeed, err := strconv.ParseFloat(strings.TrimSpace(fanInfo), 64); err == nil {
			metrics = append(metrics, &proto.Metric{
				Name:   "gpu.nvidia.fan.speed",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: labels,
				Points: []*proto.MetricPoint{{Timestamp: now, Value: fanSpeed}},
			})
		}
	}

	// Get performance state
	pState := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-gpu=pstate", "--format=csv,noheader")
	if pState != "" {
		pState = strings.TrimSpace(pState)
		metrics = append(metrics, &proto.Metric{
			Name:   "gpu.nvidia.performance_state",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: []*proto.MetricLabel{{Key: "gpu_id", Value: gpuID}, {Key: "state", Value: pState}},
			Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
		})
	}

	// Get running processes
	procsInfo := g.runNvidiaSMI(fmt.Sprintf("--id=%s", gpuID), "--query-compute-apps=pid,used_memory", "--format=csv,noheader,nounits")
	if procsInfo != "" {
		scanner := bufio.NewScanner(strings.NewReader(procsInfo))
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.Split(line, ",")
			if len(parts) >= 2 {
				pid := strings.TrimSpace(parts[0])
				if mem, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					metrics = append(metrics, &proto.Metric{
						Name:   "gpu.nvidia.process.memory",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: []*proto.MetricLabel{{Key: "gpu_id", Value: gpuID}, {Key: "pid", Value: pid}},
						Points: []*proto.MetricPoint{{Timestamp: now, Value: mem}},
					})
				}
			}
		}
	}

	return metrics
}

// collectAMD collects AMD GPU metrics
func (g *GPUSource) collectAMD(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Check for ROCm SMI
	_, err := exec.LookPath("rocm-smi")
	if err != nil {
		return metrics
	}

	// Basic AMD GPU detection - can be expanded with rocm-smi queries
	metrics = append(metrics, &proto.Metric{
		Name:   "gpu.amd.available",
		Type:   proto.MetricType_METRIC_TYPE_GAUGE,
		Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
	})

	return metrics
}

// collectIntel collects Intel GPU metrics
func (g *GPUSource) collectIntel(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Check for Intel GPU tools
	_, err := exec.LookPath("intel_gpu_top")
	if err != nil {
		return metrics
	}

	// Basic Intel GPU detection
	metrics = append(metrics, &proto.Metric{
		Name:   "gpu.intel.available",
		Type:   proto.MetricType_METRIC_TYPE_GAUGE,
		Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
	})

	return metrics
}

// Helper functions

func (g *GPUSource) getNVIDIA_gpuCount() (int, error) {
	output := g.runNvidiaSMI("--query-gpu=count", "--format=csv,noheader,nounits")
	if output == "" {
		return 0, fmt.Errorf("no GPU detected")
	}

	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, err
	}

	return count, nil
}

func (g *GPUSource) runNvidiaSMI(args ...string) string {
	if g.nvidiaSMIPath == "" {
		return ""
	}

	cmdArgs := append([]string{}, args...)
	cmd := exec.Command(g.nvidiaSMIPath, cmdArgs...)

	output, err := cmd.Output()
	if err != nil {
		g.logger.Debug("nvidia-smi command failed", zap.Error(err), zap.String("args", strings.Join(cmdArgs, " ")))
		return ""
	}

	return strings.TrimSpace(string(output))
}
