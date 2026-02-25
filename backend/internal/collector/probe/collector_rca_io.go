// Package probe implements I/O root cause analysis collection.
// This provides process-level and device-level I/O attribution for diagnosing I/O bottlenecks.
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

// IOProcessInfo holds detailed I/O information for a process
type IOProcessInfo struct {
	PID             int
	Name            string
	ReadBytes       uint64 // Bytes read from storage
	WriteBytes      uint64 // Bytes written to storage
	ReadChars       uint64 // Bytes read (including cached)
	WriteChars      uint64 // Bytes written (including buffered)
	ReadSyscalls    uint64 // Number of read() syscalls
	WriteSyscalls   uint64 // Number of write() syscalls
	CancelledWrites uint64 // Cancelled write bytes

	// Calculated rates
	ReadBytesRate  float64
	WriteBytesRate float64

	// File descriptors with I/O
	TopFiles []FileIOInfo
}

// FileIOInfo holds I/O information for a file descriptor
type FileIOInfo struct {
	FD     int
	Path   string
	Mode   string // read, write, rw
	Device string
	Size   uint64
	Pos    uint64
}

// IODeviceInfo holds detailed I/O information for a device
type IODeviceInfo struct {
	Device          string
	ReadOps         uint64
	ReadBytes       uint64
	ReadTime        float64 // seconds
	WriteOps        uint64
	WriteBytes      uint64
	WriteTime       float64 // seconds
	IOTime          float64 // seconds spent doing I/O
	WeightedIOTime  float64 // weighted I/O time
	InProgress      int
	AvgQueueDepth   float64
	Utilization     float64 // percentage
	AvgReadLatency  float64 // ms
	AvgWriteLatency float64 // ms
	ReadBytesRate   float64
	WriteBytesRate  float64

	// Top processes using this device
	TopProcesses []IOProcessInfo
}

// IORootCauseCollector collects detailed I/O attribution data
type IORootCauseCollector struct {
	mu sync.Mutex

	// Previous state for rate calculations
	lastProcessIO map[int]*IOProcessInfo
	lastDeviceIO  map[string]*IODeviceInfo
	lastCollect   time.Time

	// Configuration
	topN        int
	topFiles    int
	deviceMatch string
}

// NewIORootCauseCollector creates a new I/O RCA collector
func NewIORootCauseCollector(topN, topFiles int) *IORootCauseCollector {
	if topN <= 0 {
		topN = 20
	}
	if topFiles <= 0 {
		topFiles = 5
	}
	return &IORootCauseCollector{
		lastProcessIO: make(map[int]*IOProcessInfo),
		lastDeviceIO:  make(map[string]*IODeviceInfo),
		topN:          topN,
		topFiles:      topFiles,
	}
}

// Collect gathers I/O root cause metrics
func (c *IORootCauseCollector) Collect(now time.Time) ([]Metric, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elapsed := now.Sub(c.lastCollect).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	var metrics []Metric

	// Collect device-level I/O
	deviceMetrics, devices := c.collectDeviceIO(now, elapsed)
	metrics = append(metrics, deviceMetrics...)

	// Collect process-level I/O
	processMetrics := c.collectProcessIO(now, elapsed)
	metrics = append(metrics, processMetrics...)

	// Update state
	c.lastCollect = now
	c.lastDeviceIO = devices

	return metrics, nil
}

func (c *IORootCauseCollector) collectDeviceIO(now time.Time, elapsed float64) ([]Metric, map[string]*IODeviceInfo) {
	var metrics []Metric
	devices := make(map[string]*IODeviceInfo)

	f, err := os.Open("/proc/diskstats")
	if err != nil {
		return metrics, devices
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 14 {
			continue
		}

		device := fields[2]
		if c.skipDevice(device) {
			continue
		}

		dev := &IODeviceInfo{Device: device}

		dev.ReadOps, _ = strconv.ParseUint(fields[3], 10, 64)
		readSectors, _ := strconv.ParseUint(fields[5], 10, 64)
		dev.ReadBytes = readSectors * 512
		readTimeMs, _ := strconv.ParseFloat(fields[6], 64)
		dev.ReadTime = readTimeMs / 1000.0

		dev.WriteOps, _ = strconv.ParseUint(fields[7], 10, 64)
		writeSectors, _ := strconv.ParseUint(fields[9], 10, 64)
		dev.WriteBytes = writeSectors * 512
		writeTimeMs, _ := strconv.ParseFloat(fields[10], 64)
		dev.WriteTime = writeTimeMs / 1000.0

		inProgress, _ := strconv.Atoi(fields[11])
		dev.InProgress = inProgress

		ioTimeMs, _ := strconv.ParseFloat(fields[12], 64)
		dev.IOTime = ioTimeMs / 1000.0

		if len(fields) >= 14 {
			weightedMs, _ := strconv.ParseFloat(fields[13], 64)
			dev.WeightedIOTime = weightedMs / 1000.0
		}

		// Calculate rates and latencies
		if prev, ok := c.lastDeviceIO[device]; ok {
			readBytesDelta := dev.ReadBytes - prev.ReadBytes
			writeBytesDelta := dev.WriteBytes - prev.WriteBytes
			dev.ReadBytesRate = float64(readBytesDelta) / elapsed
			dev.WriteBytesRate = float64(writeBytesDelta) / elapsed

			if dev.ReadBytesRate < 0 {
				dev.ReadBytesRate = 0
			}
			if dev.WriteBytesRate < 0 {
				dev.WriteBytesRate = 0
			}

			// Calculate average latencies
			readOpsDelta := dev.ReadOps - prev.ReadOps
			writeOpsDelta := dev.WriteOps - prev.WriteOps
			readTimeDelta := dev.ReadTime - prev.ReadTime
			writeTimeDelta := dev.WriteTime - prev.WriteTime

			if readOpsDelta > 0 {
				dev.AvgReadLatency = (readTimeDelta * 1000.0) / float64(readOpsDelta) // ms
			}
			if writeOpsDelta > 0 {
				dev.AvgWriteLatency = (writeTimeDelta * 1000.0) / float64(writeOpsDelta)
			}

			// Utilization = time spent doing I/O / elapsed time
			ioTimeDelta := dev.IOTime - prev.IOTime
			dev.Utilization = (ioTimeDelta / elapsed) * 100.0
			if dev.Utilization > 100 {
				dev.Utilization = 100
			}
		}

		devices[device] = dev

		// Emit metrics
		labels := map[string]string{"device": device}

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_read_bytes_total",
			Type:      "counter",
			Value:     float64(dev.ReadBytes),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_write_bytes_total",
			Type:      "counter",
			Value:     float64(dev.WriteBytes),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_read_ops_total",
			Type:      "counter",
			Value:     float64(dev.ReadOps),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_write_ops_total",
			Type:      "counter",
			Value:     float64(dev.WriteOps),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_read_bytes_per_second",
			Type:      "gauge",
			Value:     dev.ReadBytesRate,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_write_bytes_per_second",
			Type:      "gauge",
			Value:     dev.WriteBytesRate,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_in_progress",
			Type:      "gauge",
			Value:     float64(dev.InProgress),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_utilization_percent",
			Type:      "gauge",
			Value:     dev.Utilization,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_avg_read_latency_ms",
			Type:      "gauge",
			Value:     dev.AvgReadLatency,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_avg_write_latency_ms",
			Type:      "gauge",
			Value:     dev.AvgWriteLatency,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_device_io_time_seconds_total",
			Type:      "counter",
			Value:     dev.IOTime,
			Labels:    labels,
			Timestamp: now,
		})
	}

	return metrics, devices
}

func (c *IORootCauseCollector) collectProcessIO(now time.Time, elapsed float64) []Metric {
	var metrics []Metric

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return metrics
	}

	var procs []IOProcessInfo

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		proc, err := c.readProcessIO(pid)
		if err != nil {
			continue
		}

		// Calculate rates
		if prev, ok := c.lastProcessIO[pid]; ok {
			readDelta := proc.ReadBytes - prev.ReadBytes
			writeDelta := proc.WriteBytes - prev.WriteBytes
			proc.ReadBytesRate = float64(readDelta) / elapsed
			proc.WriteBytesRate = float64(writeDelta) / elapsed

			if proc.ReadBytesRate < 0 {
				proc.ReadBytesRate = 0
			}
			if proc.WriteBytesRate < 0 {
				proc.WriteBytesRate = 0
			}
		}

		procs = append(procs, proc)
	}

	// Sort by total I/O rate
	sort.Slice(procs, func(i, j int) bool {
		return (procs[i].ReadBytesRate + procs[i].WriteBytesRate) > (procs[j].ReadBytesRate + procs[j].WriteBytesRate)
	})

	// Update lastProcessIO
	newMap := make(map[int]*IOProcessInfo)
	for i := range procs {
		p := procs[i]
		newMap[p.PID] = &p
	}
	c.lastProcessIO = newMap

	// Emit metrics for top N
	count := c.topN
	if count > len(procs) {
		count = len(procs)
	}

	for _, p := range procs[:count] {
		if p.ReadBytesRate < 1024 && p.WriteBytesRate < 1024 {
			continue // Skip < 1KB/s
		}

		labels := map[string]string{
			"pid":  fmt.Sprintf("%d", p.PID),
			"name": sanitizeProcName(p.Name),
		}
		labels = applyProcessContextLabels(labels, buildProcessContext(p.PID, p.Name))

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_read_bytes_total",
			Type:      "counter",
			Value:     float64(p.ReadBytes),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_write_bytes_total",
			Type:      "counter",
			Value:     float64(p.WriteBytes),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_read_bytes_per_second",
			Type:      "gauge",
			Value:     p.ReadBytesRate,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_write_bytes_per_second",
			Type:      "gauge",
			Value:     p.WriteBytesRate,
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_read_syscalls_total",
			Type:      "counter",
			Value:     float64(p.ReadSyscalls),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_write_syscalls_total",
			Type:      "counter",
			Value:     float64(p.WriteSyscalls),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_read_chars_total",
			Type:      "counter",
			Value:     float64(p.ReadChars),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_write_chars_total",
			Type:      "counter",
			Value:     float64(p.WriteChars),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_cancelled_write_bytes_total",
			Type:      "counter",
			Value:     float64(p.CancelledWrites),
			Labels:    labels,
			Timestamp: now,
		})

		metrics = append(metrics, Metric{
			Name:      "rca_io_process_bytes_per_second",
			Type:      "gauge",
			Value:     p.ReadBytesRate + p.WriteBytesRate,
			Labels:    labels,
			Timestamp: now,
		})

		// Top files for this process
		for _, f := range p.TopFiles {
			fileLabels := copyLabels(labels)
			fileLabels["fd"] = fmt.Sprintf("%d", f.FD)
			if f.Path != "" && len(f.Path) < 64 {
				fileLabels["path"] = f.Path
			}
			if f.Device != "" {
				fileLabels["device"] = f.Device
			}

			metrics = append(metrics, Metric{
				Name:      "rca_io_process_file_fd",
				Type:      "gauge",
				Value:     1,
				Labels:    fileLabels,
				Timestamp: now,
			})
		}
	}

	return metrics
}

func (c *IORootCauseCollector) readProcessIO(pid int) (IOProcessInfo, error) {
	procPath := fmt.Sprintf("/proc/%d", pid)
	proc := IOProcessInfo{PID: pid}

	// Read name from comm
	if data, err := os.ReadFile(filepath.Join(procPath, "comm")); err == nil {
		proc.Name = strings.TrimSpace(string(data))
	}

	// Read /proc/[pid]/io
	ioData, err := os.ReadFile(filepath.Join(procPath, "io"))
	if err != nil {
		return proc, err
	}

	for _, line := range strings.Split(string(ioData), "\n") {
		parts := strings.Split(line, ": ")
		if len(parts) != 2 {
			continue
		}

		val, _ := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)

		switch parts[0] {
		case "rchar":
			proc.ReadChars = val
		case "wchar":
			proc.WriteChars = val
		case "syscr":
			proc.ReadSyscalls = val
		case "syscw":
			proc.WriteSyscalls = val
		case "read_bytes":
			proc.ReadBytes = val
		case "write_bytes":
			proc.WriteBytes = val
		case "cancelled_write_bytes":
			proc.CancelledWrites = val
		}
	}

	// Read top files from /proc/[pid]/fd
	proc.TopFiles = c.readTopFiles(procPath)

	return proc, nil
}

func (c *IORootCauseCollector) readTopFiles(procPath string) []FileIOInfo {
	fdPath := filepath.Join(procPath, "fd")
	entries, err := os.ReadDir(fdPath)
	if err != nil {
		return nil
	}

	var files []FileIOInfo

	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		// Read symlink to get file path
		link, err := os.Readlink(filepath.Join(fdPath, entry.Name()))
		if err != nil {
			continue
		}

		// Skip special file descriptors
		if strings.HasPrefix(link, "pipe:") ||
			strings.HasPrefix(link, "socket:") ||
			strings.HasPrefix(link, "anon_inode:") {
			continue
		}

		file := FileIOInfo{
			FD:   fd,
			Path: link,
		}

		// Try to get device from path
		if strings.HasPrefix(link, "/dev/") {
			file.Device = filepath.Base(link)
		} else if strings.HasPrefix(link, "/") {
			// Try to determine device from mount point
			file.Device = c.getDeviceForPath(link)
		}

		files = append(files, file)
	}

	// Limit to topFiles
	if len(files) > c.topFiles {
		files = files[:c.topFiles]
	}

	return files
}

func (c *IORootCauseCollector) getDeviceForPath(path string) string {
	// Simple implementation - could be enhanced with /proc/mounts parsing
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return ""
	}
	defer f.Close()

	var best string
	var bestLen int

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		device := fields[0]
		mountpoint := fields[1]

		if strings.HasPrefix(path, mountpoint) && len(mountpoint) > bestLen {
			best = device
			bestLen = len(mountpoint)
		}
	}

	if strings.HasPrefix(best, "/dev/") {
		return filepath.Base(best)
	}
	return ""
}

func (c *IORootCauseCollector) skipDevice(device string) bool {
	// Skip loop, ram, dm, zram devices
	for _, prefix := range []string{"loop", "ram", "dm-", "zram"} {
		if strings.HasPrefix(device, prefix) {
			return true
		}
	}

	// Skip partitions
	if strings.HasPrefix(device, "nvme") {
		if idx := strings.LastIndex(device, "p"); idx != -1 && idx < len(device)-1 {
			afterP := device[idx+1:]
			if len(afterP) > 0 && afterP[0] >= '0' && afterP[0] <= '9' {
				return true
			}
		}
		return false
	}

	if len(device) > 2 {
		lastChar := device[len(device)-1]
		if lastChar >= '0' && lastChar <= '9' {
			prevChar := device[len(device)-2]
			if prevChar >= 'a' && prevChar <= 'z' && prevChar != 'n' {
				return true
			}
		}
	}

	return false
}
