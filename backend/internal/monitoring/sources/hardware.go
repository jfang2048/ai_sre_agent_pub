package sources

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// HardwareSource collects hardware-level metrics for bare-metal and VM detection
type HardwareSource struct {
	BaseSource
	config HardwareConfig
}

// NewHardwareSource creates a new hardware metrics source
func NewHardwareSource(config HardwareConfig) (*HardwareSource, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("hardware source is disabled")
	}

	return &HardwareSource{
		BaseSource: BaseSource{
			name:    "hardware",
			enabled: config.Enabled,
		},
		config: config,
	}, nil
}

func (h *HardwareSource) Name() string {
	return "hardware"
}

func (h *HardwareSource) Start(ctx context.Context) error {
	h.setStatus(true, true, "")
	return nil
}

func (h *HardwareSource) Stop() error {
	h.setStatus(false, false, "")
	return nil
}

// Collect collects hardware metrics
func (h *HardwareSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	metrics := []*proto.Metric{}
	now := timestamppb.Now()

	// Detect machine type and collect relevant metrics
	machineType := h.detectMachineType()
	metrics = append(metrics, createGauge("hardware.machine.type", float64(machineType), now))

	// CPU Information
	if h.config.IncludeCPUInfo {
		cpuMetrics := h.collectCPUInfo(now)
		metrics = append(metrics, cpuMetrics...)
	}

	// Memory Information
	if h.config.IncludeMemInfo {
		memMetrics := h.collectMemoryInfo(now)
		metrics = append(metrics, memMetrics...)
	}

	// Disk Information
	if h.config.IncludeDiskInfo {
		diskMetrics := h.collectDiskInfo(now)
		metrics = append(metrics, diskMetrics...)
	}

	// Network Interface Information
	if h.config.IncludeNetwork {
		netMetrics := h.collectNetworkInfo(now)
		metrics = append(metrics, netMetrics...)
	}

	// System Information
	sysMetrics := h.collectSystemInfo(now)
	metrics = append(metrics, sysMetrics...)

	// Virtualization detection
	virtMetrics := h.collectVirtualizationInfo(now)
	metrics = append(metrics, virtMetrics...)

	h.setStatus(true, true, "")

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "hardware",
		CollectedAt: now,
	}, nil
}

// MachineType constants
const (
	MachineTypeBareMetal = 0
	MachineTypeVM        = 1
	MachineTypeContainer = 2
	MachineTypeUnknown   = 99
)

// detectMachineType detects if running on bare-metal, VM, or container
func (h *HardwareSource) detectMachineType() int {
	// Check for container indicators
	if h.isFilePresent("/.dockerenv") || h.isFilePresent("/.dockerinit") {
		return MachineTypeContainer
	}
	if h.readFileContents("/proc/1/cgroup", "") != "" {
		cgroup := h.readFileContents("/proc/1/cgroup", "")
		if strings.Contains(cgroup, "docker") || strings.Contains(cgroup, "kubepods") {
			return MachineTypeContainer
		}
	}

	// Check for VM indicators using DMI
	if h.isFilePresent("/sys/class/dmi/id/product_name") {
		productName := h.readFileContents("/sys/class/dmi/id/product_name", "")
		productName = strings.ToLower(strings.TrimSpace(productName))

		// Common VM signatures
		vmSignatures := []string{"vmware", "virtualbox", "qemu", "kvm", "xen",
			"bochs", "parallels", "virtual machine", "hyperv"}

		for _, sig := range vmSignatures {
			if strings.Contains(productName, sig) {
				return MachineTypeVM
			}
		}
	}

	// Check CPU flags for virtualization
	if h.isFilePresent("/proc/cpuinfo") {
		cpuinfo := h.readFileContents("/proc/cpuinfo", "")
		if strings.Contains(cpuinfo, "hypervisor") {
			return MachineTypeVM
		}
	}

	// Default to bare-metal
	return MachineTypeBareMetal
}

// collectCPUInfo collects detailed CPU information
func (h *HardwareSource) collectCPUInfo(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Get CPU model from /proc/cpuinfo
	if f, err := os.Open("/proc/cpuinfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		modelName := ""
		vendorID := ""
		cpuCores := 0
		siblings := 0
		cpuMHz := 0.0
		flags := ""

		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					modelName = strings.TrimSpace(parts[1])
				}
			} else if strings.HasPrefix(line, "vendor_id") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					vendorID = strings.TrimSpace(parts[1])
				}
			} else if strings.HasPrefix(line, "cpu cores") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					if val, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
						cpuCores = val
					}
				}
			} else if strings.HasPrefix(line, "siblings") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					if val, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
						siblings = val
					}
				}
			} else if strings.HasPrefix(line, "cpu MHz") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					if val, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
						cpuMHz = val
					}
				}
			} else if strings.HasPrefix(line, "flags") || strings.HasPrefix(line, "Features") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					flags = strings.TrimSpace(parts[1])
				}
			}
		}

		// Emit CPU info as labels
		if modelName != "" {
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.cpu.model",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "model", Value: modelName}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
		if vendorID != "" {
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.cpu.vendor",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "vendor", Value: vendorID}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
		if cpuCores > 0 {
			metrics = append(metrics, createGauge("hardware.cpu.cores", float64(cpuCores), now))
		}
		if siblings > 0 {
			metrics = append(metrics, createGauge("hardware.cpu.threads", float64(siblings), now))
		}
		if cpuMHz > 0 {
			metrics = append(metrics, createGauge("hardware.cpu.mhz", cpuMHz, now))
		}

		// Check for virtualization support in flags
		if flags != "" {
			hasVMX := strings.Contains(flags, "vmx")
			hasSVM := strings.Contains(flags, "svm")
			if hasVMX || hasSVM {
				metrics = append(metrics, createGauge("hardware.cpu.virtualization", 1, now))
			}
		}
	}

	// Get CPU topology
	if h.isFilePresent("/sys/devices/system/cpu/present") {
		present := h.readFileContents("/sys/devices/system/cpu/present", "")
		// Parse range like "0-11"
		if strings.Contains(present, "-") {
			parts := strings.Split(strings.TrimSpace(present), "-")
			if len(parts) == 2 {
				if start, err1 := strconv.Atoi(strings.TrimSpace(parts[0])); err1 == nil {
					if end, err2 := strconv.Atoi(strings.TrimSpace(parts[1])); err2 == nil {
						metrics = append(metrics, createGauge("hardware.cpu.logical_cpus", float64(end-start+1), now))
					}
				}
			}
		}
	}

	return metrics
}

// collectMemoryInfo collects detailed memory information
func (h *HardwareSource) collectMemoryInfo(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Get memory slot info from dmidecode (if available as root)
	// For now, use /proc/meminfo for additional details
	if f, err := os.Open("/proc/meminfo"); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)

		hugePagesTotal := 0.0
		hugePagesFree := 0.0
		hugePageSize := 0.0

		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}

			val, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				continue
			}

			switch {
			case strings.HasPrefix(line, "HugePages_Total:"):
				hugePagesTotal = val
			case strings.HasPrefix(line, "HugePages_Free:"):
				hugePagesFree = val
			case strings.HasPrefix(line, "Hugepagesize:"):
				hugePageSize = val
			}
		}

		if hugePagesTotal > 0 {
			metrics = append(metrics, createGauge("hardware.memory.hugepages.total", hugePagesTotal, now))
			metrics = append(metrics, createGauge("hardware.memory.hugepages.free", hugePagesFree, now))
			if hugePageSize > 0 {
				metrics = append(metrics, createGauge("hardware.memory.hugepages.size_kb", hugePageSize, now))
			}
		}
	}

	return metrics
}

// collectDiskInfo collects disk and storage information
func (h *HardwareSource) collectDiskInfo(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Get block device information
	devices, _ := os.ReadDir("/sys/block")
	for _, device := range devices {
		deviceName := device.Name()

		// Skip loop devices
		if strings.HasPrefix(deviceName, "loop") {
			continue
		}

		devicePath := "/sys/block/" + deviceName

		// Get device size
		if sizeData, err := os.ReadFile(devicePath + "/size"); err == nil {
			sizeStr := strings.TrimSpace(string(sizeData))
			if size, err := strconv.ParseUint(sizeStr, 10, 64); err == nil {
				// Size is in 512-byte sectors
				sizeBytes := size * 512
				metrics = append(metrics, &proto.Metric{
					Name:   "hardware.disk.size_bytes",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: []*proto.MetricLabel{{Key: "device", Value: deviceName}},
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(sizeBytes)}},
				})
			}
		}

		// Get device type (rotational vs ssd)
		if rotationalData, err := os.ReadFile(devicePath + "/queue/rotational"); err == nil {
			rotationalStr := strings.TrimSpace(string(rotationalData))
			if rotational, err := strconv.Atoi(rotationalStr); err == nil {
				deviceType := "ssd"
				if rotational == 1 {
					deviceType = "hdd"
				}
				metrics = append(metrics, &proto.Metric{
					Name:   "hardware.disk.type",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: []*proto.MetricLabel{{Key: "device", Value: deviceName}, {Key: "type", Value: deviceType}},
					Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
				})
			}
		}

		// Get scheduler
		if schedulerData, err := os.ReadFile(devicePath + "/queue/scheduler"); err == nil {
			schedulerStr := strings.TrimSpace(string(schedulerData))
			// Format: "noop deadline [cfq]"
			if strings.Contains(schedulerStr, "[") {
				start := strings.Index(schedulerStr, "[") + 1
				end := strings.Index(schedulerStr, "]")
				if start > 0 && end > start {
					currentScheduler := schedulerStr[start:end]
					metrics = append(metrics, &proto.Metric{
						Name:   "hardware.disk.scheduler",
						Type:   proto.MetricType_METRIC_TYPE_GAUGE,
						Labels: []*proto.MetricLabel{{Key: "device", Value: deviceName}, {Key: "scheduler", Value: currentScheduler}},
						Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
					})
				}
			}
		}
	}

	return metrics
}

// collectNetworkInfo collects network interface information
func (h *HardwareSource) collectNetworkInfo(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Get network interface info from /sys/class/net
	interfaces, _ := os.ReadDir("/sys/class/net")
	for _, iface := range interfaces {
		ifName := iface.Name()
		if ifName == "lo" {
			continue
		}

		ifacePath := "/sys/class/net/" + ifName

		// Check if interface is operational
		if operStateData, err := os.ReadFile(ifacePath + "/operstate"); err == nil {
			operState := strings.TrimSpace(string(operStateData))
			isUp := operState == "up"
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.network.interface.up",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "interface", Value: ifName}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: boolToFloat(isUp)}},
			})
		}

		// Get interface speed (in Mbps)
		if speedData, err := os.ReadFile(ifacePath + "/speed"); err == nil {
			speedStr := strings.TrimSpace(string(speedData))
			if speed, err := strconv.ParseFloat(speedStr, 64); err == nil && speed > 0 {
				metrics = append(metrics, &proto.Metric{
					Name:   "hardware.network.speed_mbps",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: []*proto.MetricLabel{{Key: "interface", Value: ifName}},
					Points: []*proto.MetricPoint{{Timestamp: now, Value: speed}},
				})
			}
		}

		// Get duplex mode
		if duplexData, err := os.ReadFile(ifacePath + "/duplex"); err == nil {
			duplex := strings.TrimSpace(string(duplexData))
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.network.duplex",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "interface", Value: ifName}, {Key: "duplex", Value: duplex}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}

		// Get MAC address
		if addrData, err := os.ReadFile(ifacePath + "/address"); err == nil {
			addr := strings.TrimSpace(string(addrData))
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.network.mac",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "interface", Value: ifName}, {Key: "address", Value: addr}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
	}

	return metrics
}

// collectSystemInfo collects system-level information
func (h *HardwareSource) collectSystemInfo(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Kernel version
	if uname := h.readFileContents("/proc/sys/kernel/osrelease", ""); uname != "" {
		metrics = append(metrics, &proto.Metric{
			Name:   "hardware.kernel.version",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: []*proto.MetricLabel{{Key: "version", Value: strings.TrimSpace(uname)}},
			Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
		})
	}

	// Architecture
	if uname := h.readFileContents("/proc/sys/kernel/arch", ""); uname != "" {
		metrics = append(metrics, &proto.Metric{
			Name:   "hardware.architecture",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: []*proto.MetricLabel{{Key: "arch", Value: strings.TrimSpace(uname)}},
			Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
		})
	}

	// Boot time
	if bootTimeData, err := os.ReadFile("/proc/stat"); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(bootTimeData)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "btime") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if btime, err := strconv.ParseFloat(fields[1], 64); err == nil {
						metrics = append(metrics, createGauge("hardware.boot.time", btime, now))
					}
				}
				break
			}
		}
	}

	// System timezone
	if tzLink, err := os.Readlink("/etc/localtime"); err == nil {
		// Extract timezone from path like "../usr/share/zoneinfo/America/New_York"
		if strings.Contains(tzLink, "zoneinfo/") {
			tz := strings.Split(tzLink, "zoneinfo/")[1]
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.timezone",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "timezone", Value: tz}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
	}

	return metrics
}

// collectVirtualizationInfo collects virtualization information
func (h *HardwareSource) collectVirtualizationInfo(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// DMI BIOS Information
	if h.isFilePresent("/sys/class/dmi/id/bios_vendor") {
		biosVendor := h.readFileContents("/sys/class/dmi/id/bios_vendor", "")
		if biosVendor != "" {
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.bios.vendor",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "vendor", Value: strings.TrimSpace(biosVendor)}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
	}

	if h.isFilePresent("/sys/class/dmi/id/bios_version") {
		biosVersion := h.readFileContents("/sys/class/dmi/id/bios_version", "")
		if biosVersion != "" {
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.bios.version",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "version", Value: strings.TrimSpace(biosVersion)}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
	}

	if h.isFilePresent("/sys/class/dmi/id/bios_date") {
		biosDate := h.readFileContents("/sys/class/dmi/id/bios_date", "")
		if biosDate != "" {
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.bios.date",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "date", Value: strings.TrimSpace(biosDate)}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
	}

	// System Manufacturer
	if h.isFilePresent("/sys/class/dmi/id/sys_vendor") {
		sysVendor := h.readFileContents("/sys/class/dmi/id/sys_vendor", "")
		if sysVendor != "" {
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.system.vendor",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "vendor", Value: strings.TrimSpace(sysVendor)}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
	}

	if h.isFilePresent("/sys/class/dmi/id/product_name") {
		productName := h.readFileContents("/sys/class/dmi/id/product_name", "")
		if productName != "" {
			metrics = append(metrics, &proto.Metric{
				Name:   "hardware.system.product",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Labels: []*proto.MetricLabel{{Key: "product", Value: strings.TrimSpace(productName)}},
				Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
			})
		}
	}

	if h.isFilePresent("/sys/class/dmi/id/product_serial") {
		serial := h.readFileContents("/sys/class/dmi/id/product_serial", "")
		if serial != "" && !strings.Contains(strings.ToLower(serial), "not specified") {
			// Don't expose actual serial, just indicate presence
			metrics = append(metrics, createGauge("hardware.system.serial_present", 1, now))
		}
	}

	return metrics
}

// Helper functions

func (h *HardwareSource) isFilePresent(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (h *HardwareSource) readFileContents(path, defaultVal string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultVal
	}
	return strings.TrimSpace(string(data))
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
