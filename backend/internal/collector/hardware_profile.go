package collector

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

type hardwarePathRoots struct {
	procRoot string
	sysRoot  string
}

var defaultHardwarePathRoots = hardwarePathRoots{
	procRoot: "/proc",
	sysRoot:  "/sys",
}

type hardwareCache struct {
	logger *zap.Logger

	mu      sync.RWMutex
	profile hardwareProfile
}

type hardwareProfile struct {
	Discovered  bool
	LastRefresh time.Time

	CPU       hardwareCPUProfile
	Storage   hardwareStorageProfile
	Network   hardwareNetworkProfile
	GPU       hardwareGPUProfile
	Sampling  hardwareSamplingProfile
	Threshold hardwareThresholdProfile
}

type hardwareCPUProfile struct {
	Architecture string
	Vendor       string
	Model        string
	Sockets      int
	Cores        int
	Threads      int
	NUMANodes    int
	Hybrid       bool
}

type hardwareStorageProfile struct {
	DeviceCount   int
	NVMeCount     int
	SSDCount      int
	HDDCount      int
	DominantClass string
	MaxQueueDepth int
}

type hardwareNetworkProfile struct {
	InterfaceCount int
	HighSpeedCount int
	MaxSpeedMbps   int
	DominantType   string
	Driver         string
	RDMACapable    bool
}

type hardwareGPUProfile struct {
	DeviceCount int
	Vendor      string
	Driver      string
	Runtime     string
}

type hardwareSamplingProfile struct {
	ProcessIntervalSamples          int
	HostProcFallbackIntervalSamples int
	PressureIntervalSamples         int
	NetlinkIntervalSamples          int
	GPUIntervalSamples              int
}

type hardwareThresholdProfile struct {
	CPUBusyPercent         float64
	CPUCriticalPercent     float64
	MemoryPressurePercent  float64
	MemoryCriticalPercent  float64
	DiskLatencySeconds     float64
	DiskQueueDepth         float64
	IOPressurePercent      float64
	NetworkRetransmitRatio float64
	NetworkSoftnetDrops    float64
	GPULowUtilPercent      float64
	GPUMemoryPressurePct   float64
	GPUCriticalMemoryPct   float64
}

func newHardwareCache(logger *zap.Logger) *hardwareCache {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &hardwareCache{
		logger:  logger.With(zap.String("component", "collector_hardware")),
		profile: defaultHardwareProfile(time.Time{}),
	}
}

func (c *hardwareCache) Snapshot() hardwareProfile {
	if c == nil {
		return defaultHardwareProfile(time.Time{})
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.profile
}

func (c *hardwareCache) RefreshIfNeeded(now time.Time, cfg HardwareConfig) hardwareProfile {
	if c == nil {
		return defaultHardwareProfile(now)
	}

	c.mu.RLock()
	current := c.profile
	c.mu.RUnlock()

	if !cfg.Enabled {
		return current
	}
	if current.Discovered && cfg.RefreshInterval > 0 && !current.LastRefresh.IsZero() &&
		now.Sub(current.LastRefresh) < cfg.RefreshInterval {
		return current
	}

	profile := discoverHardwareProfile(defaultHardwarePathRoots, now)
	c.mu.Lock()
	c.profile = profile
	c.mu.Unlock()

	if profile.Discovered {
		c.logger.Debug("hardware profile refreshed",
			zap.String("cpu_vendor", profile.CPU.Vendor),
			zap.Int("cpu_threads", profile.CPU.Threads),
			zap.Int("numa_nodes", profile.CPU.NUMANodes),
			zap.String("storage_class", profile.Storage.DominantClass),
			zap.String("network_type", profile.Network.DominantType),
			zap.Int("gpu_devices", profile.GPU.DeviceCount),
		)
	}
	return profile
}

func defaultHardwareProfile(now time.Time) hardwareProfile {
	profile := hardwareProfile{
		LastRefresh: now,
		CPU: hardwareCPUProfile{
			Architecture: runtime.GOARCH,
			Vendor:       "unknown",
			Sockets:      1,
			Cores:        maxPositiveInt(runtime.NumCPU(), 1),
			Threads:      maxPositiveInt(runtime.NumCPU(), 1),
			NUMANodes:    1,
		},
		Storage: hardwareStorageProfile{
			DominantClass: "unknown",
		},
		Network: hardwareNetworkProfile{
			DominantType: "unknown",
		},
		GPU: hardwareGPUProfile{
			Vendor:  "none",
			Runtime: "unknown",
		},
	}
	profile.Sampling = deriveHardwareSamplingProfile(profile)
	profile.Threshold = deriveHardwareThresholdProfile(profile)
	return profile
}

func discoverHardwareProfile(paths hardwarePathRoots, now time.Time) hardwareProfile {
	profile := defaultHardwareProfile(now)
	profile.Discovered = true
	profile.CPU = discoverCPUProfile(paths, profile.CPU)
	profile.Storage = discoverStorageProfile(paths)
	profile.Network = discoverNetworkProfile(paths)
	profile.GPU = discoverGPUProfile(paths)
	profile.Sampling = deriveHardwareSamplingProfile(profile)
	profile.Threshold = deriveHardwareThresholdProfile(profile)
	return profile
}

func discoverCPUProfile(paths hardwarePathRoots, fallback hardwareCPUProfile) hardwareCPUProfile {
	out := fallback
	cpuinfoPath := filepath.Join(paths.procRoot, "cpuinfo")
	if raw, err := os.ReadFile(cpuinfoPath); err == nil {
		lines := strings.Split(string(raw), "\n")
		for _, line := range lines {
			key, value, ok := splitKV(line)
			if !ok {
				continue
			}
			switch strings.ToLower(key) {
			case "vendor_id":
				if out.Vendor == "unknown" {
					out.Vendor = normalizeCPUVendor(value)
				}
			case "model name":
				if out.Model == "" {
					out.Model = strings.TrimSpace(value)
				}
			case "cpu implementer":
				if out.Vendor == "unknown" {
					out.Vendor = normalizeCPUVendor(value)
				}
			case "hardware":
				if out.Model == "" {
					out.Model = strings.TrimSpace(value)
				}
			}
		}
	}

	cpuDirs, _ := filepath.Glob(filepath.Join(paths.sysRoot, "devices/system/cpu/cpu[0-9]*"))
	if len(cpuDirs) > 0 {
		out.Threads = len(cpuDirs)
		socketSet := make(map[string]struct{}, len(cpuDirs))
		coreSet := make(map[string]struct{}, len(cpuDirs))
		capacitySet := make(map[string]struct{}, len(cpuDirs))
		coreTypeSet := make(map[string]struct{}, len(cpuDirs))
		for _, cpuDir := range cpuDirs {
			packageID := firstNonEmptyValue(
				readTrimmed(filepath.Join(cpuDir, "topology/physical_package_id")),
				"0",
			)
			coreID := firstNonEmptyValue(
				readTrimmed(filepath.Join(cpuDir, "topology/core_id")),
				filepath.Base(cpuDir),
			)
			socketSet[packageID] = struct{}{}
			coreSet[packageID+":"+coreID] = struct{}{}
			if capacity := readTrimmed(filepath.Join(cpuDir, "cpu_capacity")); capacity != "" {
				capacitySet[capacity] = struct{}{}
			}
			if coreType := readTrimmed(filepath.Join(cpuDir, "topology/core_type")); coreType != "" {
				coreTypeSet[coreType] = struct{}{}
			}
		}
		out.Sockets = maxPositiveInt(len(socketSet), 1)
		out.Cores = maxPositiveInt(len(coreSet), 1)
		if len(capacitySet) > 1 || len(coreTypeSet) > 1 {
			out.Hybrid = true
		}
	}

	nodeDirs, _ := filepath.Glob(filepath.Join(paths.sysRoot, "devices/system/node/node[0-9]*"))
	out.NUMANodes = maxPositiveInt(len(nodeDirs), 1)
	return out
}

func discoverStorageProfile(paths hardwarePathRoots) hardwareStorageProfile {
	out := hardwareStorageProfile{DominantClass: "unknown"}
	entries, err := os.ReadDir(filepath.Join(paths.sysRoot, "block"))
	if err != nil {
		return out
	}
	classCounts := map[string]int{
		"nvme": 0,
		"ssd":  0,
		"hdd":  0,
	}
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name())
		if name == "" || shouldSkipBlockDevice(name) {
			continue
		}
		deviceDir := filepath.Join(paths.sysRoot, "block", name)
		class := classifyBlockDevice(name, readTrimmed(filepath.Join(deviceDir, "queue/rotational")))
		switch class {
		case "nvme":
			out.NVMeCount++
		case "ssd":
			out.SSDCount++
		case "hdd":
			out.HDDCount++
		}
		classCounts[class]++
		out.DeviceCount++
		if depth := readIntFile(filepath.Join(deviceDir, "queue/nr_requests")); depth > out.MaxQueueDepth {
			out.MaxQueueDepth = depth
		}
	}
	out.DominantClass = dominantHardwareClass(classCounts, "unknown")
	return out
}

func discoverNetworkProfile(paths hardwarePathRoots) hardwareNetworkProfile {
	out := hardwareNetworkProfile{DominantType: "unknown"}
	entries, err := os.ReadDir(filepath.Join(paths.sysRoot, "class/net"))
	if err != nil {
		return out
	}
	typeCounts := make(map[string]int, len(entries))
	driverCounts := map[string]int{}
	for _, entry := range entries {
		iface := strings.TrimSpace(entry.Name())
		if iface == "" || iface == "lo" {
			continue
		}
		ifaceDir := filepath.Join(paths.sysRoot, "class/net", iface)
		out.InterfaceCount++
		speed := readIntFile(filepath.Join(ifaceDir, "speed"))
		if speed > out.MaxSpeedMbps {
			out.MaxSpeedMbps = speed
		}
		if speed >= 25000 {
			out.HighSpeedCount++
		}
		kind := classifyNetworkInterface(readTrimmed(filepath.Join(ifaceDir, "type")), speed, ifaceDir)
		typeCounts[kind]++
		if kind == "rdma" {
			out.RDMACapable = true
		}
		if driver := filepath.Base(readLink(filepath.Join(ifaceDir, "device/driver"))); driver != "." && driver != "/" {
			driverCounts[driver]++
		}
	}
	out.DominantType = dominantHardwareClass(typeCounts, "unknown")
	out.Driver = dominantHardwareClass(driverCounts, "")
	return out
}

func discoverGPUProfile(paths hardwarePathRoots) hardwareGPUProfile {
	out := hardwareGPUProfile{Vendor: "none", Runtime: "unknown"}
	cardDirs, _ := filepath.Glob(filepath.Join(paths.sysRoot, "class/drm/card[0-9]*"))
	vendorCounts := map[string]int{}
	driverCounts := map[string]int{}
	for _, cardDir := range cardDirs {
		vendor := normalizePCIVendor(readTrimmed(filepath.Join(cardDir, "device/vendor")))
		if vendor == "" {
			vendor = "unknown"
		}
		vendorCounts[vendor]++
		if driver := filepath.Base(readLink(filepath.Join(cardDir, "device/driver"))); driver != "." && driver != "/" {
			driverCounts[driver]++
		}
		out.DeviceCount++
	}
	if out.DeviceCount > 0 {
		out.Vendor = dominantHardwareClass(vendorCounts, "unknown")
		out.Driver = dominantHardwareClass(driverCounts, "")
	}
	if _, err := os.Stat(filepath.Join(paths.procRoot, "driver/nvidia/version")); err == nil {
		out.Runtime = "nvidia"
		if out.Vendor == "none" || out.Vendor == "unknown" {
			out.Vendor = "nvidia"
		}
	}
	if out.DeviceCount == 0 && out.Vendor == "none" {
		out.Runtime = "none"
	}
	return out
}

func deriveHardwareSamplingProfile(profile hardwareProfile) hardwareSamplingProfile {
	out := hardwareSamplingProfile{
		ProcessIntervalSamples:          2,
		HostProcFallbackIntervalSamples: 10,
		PressureIntervalSamples:         3,
		NetlinkIntervalSamples:          2,
		GPUIntervalSamples:              1,
	}
	if profile.CPU.Threads >= 64 || profile.CPU.NUMANodes > 1 || profile.CPU.Hybrid {
		out.ProcessIntervalSamples = 3
		out.HostProcFallbackIntervalSamples = 12
	}
	if profile.CPU.Threads >= 128 {
		out.ProcessIntervalSamples = 4
		out.HostProcFallbackIntervalSamples = 16
		out.PressureIntervalSamples = 4
	}
	if profile.Network.InterfaceCount > 0 && !profile.Network.RDMACapable && profile.Network.HighSpeedCount == 0 {
		out.NetlinkIntervalSamples = 3
	}
	if profile.GPU.DeviceCount == 0 {
		out.GPUIntervalSamples = 8
	} else if profile.GPU.DeviceCount >= 4 {
		out.GPUIntervalSamples = 2
	}
	return out
}

func deriveHardwareThresholdProfile(profile hardwareProfile) hardwareThresholdProfile {
	out := hardwareThresholdProfile{
		CPUBusyPercent:         75,
		CPUCriticalPercent:     92,
		MemoryPressurePercent:  82,
		MemoryCriticalPercent:  92,
		DiskLatencySeconds:     0.03,
		DiskQueueDepth:         8,
		IOPressurePercent:      10,
		NetworkRetransmitRatio: 0.02,
		NetworkSoftnetDrops:    0,
		GPULowUtilPercent:      35,
		GPUMemoryPressurePct:   88,
		GPUCriticalMemoryPct:   95,
	}

	switch profile.Storage.DominantClass {
	case "nvme":
		out.DiskLatencySeconds = 0.015
		out.DiskQueueDepth = 24
	case "ssd":
		out.DiskLatencySeconds = 0.03
		out.DiskQueueDepth = 8
	case "hdd":
		out.DiskLatencySeconds = 0.08
		out.DiskQueueDepth = 2
		out.IOPressurePercent = 18
	}
	if profile.CPU.Hybrid {
		out.CPUBusyPercent = 70
		out.CPUCriticalPercent = 88
	}
	if profile.CPU.NUMANodes > 1 {
		out.MemoryPressurePercent = 78
	}
	if profile.Network.RDMACapable || profile.Network.HighSpeedCount > 0 {
		out.NetworkRetransmitRatio = 0.01
		out.NetworkSoftnetDrops = 5
	}
	switch profile.GPU.Vendor {
	case "amd":
		out.GPULowUtilPercent = 25
	case "intel":
		out.GPULowUtilPercent = 30
	case "nvidia":
		out.GPULowUtilPercent = 35
		out.GPUMemoryPressurePct = 85
	}
	return out
}

func applyHardwareSamplingProfile(cfg Config, profile hardwareProfile) Config {
	cfg.ProbeCore.ProcessIntervalSamples = maxPositiveInt(
		cfg.ProbeCore.ProcessIntervalSamples,
		profile.Sampling.ProcessIntervalSamples,
	)
	cfg.ProbeCore.HostProcFallbackIntervalSamples = maxPositiveInt(
		cfg.ProbeCore.HostProcFallbackIntervalSamples,
		profile.Sampling.HostProcFallbackIntervalSamples,
	)
	cfg.ProbeCore.PressureIntervalSamples = maxPositiveInt(
		cfg.ProbeCore.PressureIntervalSamples,
		profile.Sampling.PressureIntervalSamples,
	)
	cfg.ProbeCore.NetlinkIntervalSamples = maxPositiveInt(
		cfg.ProbeCore.NetlinkIntervalSamples,
		profile.Sampling.NetlinkIntervalSamples,
	)
	cfg.ProbeCore.GPUIntervalSamples = maxPositiveInt(
		cfg.ProbeCore.GPUIntervalSamples,
		profile.Sampling.GPUIntervalSamples,
	)
	return cfg
}

func appendCollectorInfoHardwareLabels(info *telemetryv1.CollectorInfo, profile hardwareProfile) {
	if info == nil {
		return
	}
	topology := "smp"
	if profile.CPU.NUMANodes > 1 {
		topology = "numa"
	}
	if profile.CPU.Hybrid {
		topology = topology + "_hybrid"
	}
	info.Labels = append(info.Labels,
		&telemetryv1.Label{Key: "hw_cpu_vendor", Value: sanitizeLabelToken(profile.CPU.Vendor, maxLabelValueRunes)},
		&telemetryv1.Label{Key: "hw_cpu_topology", Value: sanitizeLabelToken(topology, maxLabelValueRunes)},
		&telemetryv1.Label{Key: "hw_storage_class", Value: sanitizeLabelToken(profile.Storage.DominantClass, maxLabelValueRunes)},
		&telemetryv1.Label{Key: "hw_network_type", Value: sanitizeLabelToken(profile.Network.DominantType, maxLabelValueRunes)},
		&telemetryv1.Label{Key: "hw_gpu_vendor", Value: sanitizeLabelToken(profile.GPU.Vendor, maxLabelValueRunes)},
	)
}

func appendHardwareMetrics(now time.Time, profile hardwareProfile, metrics *[]*telemetryv1.Metric) {
	if metrics == nil {
		return
	}
	ts := now.UnixNano()
	*metrics = append(*metrics,
		&telemetryv1.Metric{Name: "collector_hardware_refresh_age_seconds", Value: now.Sub(profile.LastRefresh).Seconds(), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_cpu_sockets", Value: float64(profile.CPU.Sockets), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_cpu_cores", Value: float64(profile.CPU.Cores), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_cpu_threads", Value: float64(profile.CPU.Threads), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_cpu_numa_nodes", Value: float64(profile.CPU.NUMANodes), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_cpu_hybrid", Value: boolToFloat(profile.CPU.Hybrid), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_storage_devices_total", Value: float64(profile.Storage.DeviceCount), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_network_interfaces_total", Value: float64(profile.Network.InterfaceCount), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_network_high_speed_interfaces_total", Value: float64(profile.Network.HighSpeedCount), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_network_max_speed_mbps", Value: float64(profile.Network.MaxSpeedMbps), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_network_rdma_capable", Value: boolToFloat(profile.Network.RDMACapable), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_gpu_devices_total", Value: float64(profile.GPU.DeviceCount), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_capability_process_interval_samples", Value: float64(profile.Sampling.ProcessIntervalSamples), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_capability_host_proc_interval_samples", Value: float64(profile.Sampling.HostProcFallbackIntervalSamples), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_capability_pressure_interval_samples", Value: float64(profile.Sampling.PressureIntervalSamples), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_capability_netlink_interval_samples", Value: float64(profile.Sampling.NetlinkIntervalSamples), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_capability_gpu_interval_samples", Value: float64(profile.Sampling.GPUIntervalSamples), TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_threshold_disk_latency_seconds", Value: profile.Threshold.DiskLatencySeconds, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_threshold_disk_queue_depth", Value: profile.Threshold.DiskQueueDepth, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_threshold_gpu_low_util_percent", Value: profile.Threshold.GPULowUtilPercent, TimestampUnixNano: ts},
		&telemetryv1.Metric{Name: "collector_hardware_threshold_network_retransmit_ratio", Value: profile.Threshold.NetworkRetransmitRatio, TimestampUnixNano: ts},
	)
	for class, count := range map[string]int{
		"nvme": profile.Storage.NVMeCount,
		"ssd":  profile.Storage.SSDCount,
		"hdd":  profile.Storage.HDDCount,
	} {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_hardware_storage_class_total",
			Value:             float64(count),
			TimestampUnixNano: ts,
			Labels:            buildLabels(map[string]string{"class": class}),
		})
	}
	*metrics = append(*metrics,
		&telemetryv1.Metric{
			Name:              "collector_hardware_storage_profile",
			Value:             1,
			TimestampUnixNano: ts,
			Labels:            buildLabels(map[string]string{"class": profile.Storage.DominantClass}),
		},
		&telemetryv1.Metric{
			Name:              "collector_hardware_network_profile",
			Value:             1,
			TimestampUnixNano: ts,
			Labels:            buildLabels(map[string]string{"type": profile.Network.DominantType}),
		},
		&telemetryv1.Metric{
			Name:              "collector_hardware_gpu_profile",
			Value:             1,
			TimestampUnixNano: ts,
			Labels:            buildLabels(map[string]string{"vendor": profile.GPU.Vendor}),
		},
	)
}

func splitKV(line string) (string, string, bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func shouldSkipBlockDevice(name string) bool {
	switch {
	case strings.HasPrefix(name, "loop"),
		strings.HasPrefix(name, "ram"),
		strings.HasPrefix(name, "zram"),
		strings.HasPrefix(name, "dm-"),
		strings.HasPrefix(name, "md"):
		return true
	default:
		return false
	}
}

func classifyBlockDevice(name, rotational string) string {
	switch {
	case strings.HasPrefix(name, "nvme"):
		return "nvme"
	case strings.TrimSpace(rotational) == "1":
		return "hdd"
	case strings.TrimSpace(rotational) == "0":
		return "ssd"
	default:
		return "unknown"
	}
}

func classifyNetworkInterface(typeValue string, speed int, ifaceDir string) string {
	switch strings.TrimSpace(typeValue) {
	case "32":
		return "rdma"
	case "1":
		if speed >= 25000 {
			return "high_speed"
		}
		return "ethernet"
	}
	if _, err := os.Stat(filepath.Join(ifaceDir, "device/infiniband")); err == nil {
		return "rdma"
	}
	if _, err := os.Stat(filepath.Join(ifaceDir, "device")); err != nil {
		return "virtual"
	}
	return "other"
}

func dominantHardwareClass(counts map[string]int, fallback string) string {
	type kv struct {
		key   string
		count int
	}
	items := make([]kv, 0, len(counts))
	for key, count := range counts {
		if strings.TrimSpace(key) == "" || count <= 0 {
			continue
		}
		items = append(items, kv{key: key, count: count})
	}
	if len(items) == 0 {
		return fallback
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].key < items[j].key
		}
		return items[i].count > items[j].count
	})
	return items[0].key
}

func normalizeCPUVendor(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(value, "intel"), value == "genuineintel":
		return "intel"
	case strings.Contains(value, "amd"), value == "authenticamd":
		return "amd"
	case strings.Contains(value, "0x41"), strings.Contains(value, "arm"), strings.Contains(value, "aarch"):
		return "arm"
	default:
		if value == "" {
			return "unknown"
		}
		return value
	}
}

func normalizePCIVendor(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0x10de":
		return "nvidia"
	case "0x1002", "0x1022":
		return "amd"
	case "0x8086":
		return "intel"
	default:
		return ""
	}
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readIntFile(path string) int {
	value, err := strconv.Atoi(readTrimmed(path))
	if err != nil {
		return 0
	}
	return value
}

func readLink(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return target
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func maxPositiveInt(values ...int) int {
	best := 0
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}
