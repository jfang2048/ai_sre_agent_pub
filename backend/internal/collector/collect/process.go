package collect

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

type processSample struct {
	pid      int
	name     string
	cpuTotal uint64
	cpuDelta float64
	rssBytes uint64
	ioRead   uint64
	ioWrite  uint64
}

// ProcessCollector collects top-K process samples.
type ProcessCollector struct {
	topK      int
	lastTotal uint64
	lastCPU   map[int]uint64
	lastIO    map[int]ioSample
	lastTime  time.Time
}

type ioSample struct {
	readBytes  uint64
	writeBytes uint64
}

// NewProcessCollector creates a process collector.
func NewProcessCollector(topK int) *ProcessCollector {
	if topK <= 0 {
		topK = 10
	}
	return &ProcessCollector{
		topK:     topK,
		lastCPU:  make(map[int]uint64),
		lastIO:   make(map[int]ioSample),
		lastTime: time.Now(),
	}
}

// Collect returns top-K processes by CPU usage.
func (c *ProcessCollector) Collect(now time.Time) []*telemetryv1.ProcessSample {
	totalCPU := readTotalCPU()
	totalDelta := float64(totalCPU - c.lastTotal)
	if totalDelta <= 0 {
		totalDelta = 1
	}
	elapsed := now.Sub(c.lastTime).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	procs, currentCPU, currentIO := scanProcesses(totalDelta, elapsed, c.lastCPU, c.lastIO)

	sort.Slice(procs, func(i, j int) bool {
		return procs[i].cpuDelta > procs[j].cpuDelta
	})

	if len(procs) > c.topK {
		procs = procs[:c.topK]
	}

	out := make([]*telemetryv1.ProcessSample, 0, len(procs))
	for _, proc := range procs {
		out = append(out, &telemetryv1.ProcessSample{
			Pid:        int32(proc.pid),
			Name:       proc.name,
			CpuPercent: proc.cpuDelta,
			RssBytes:   proc.rssBytes,
			IoReadBps:  float64(proc.ioRead) / elapsed,
			IoWriteBps: float64(proc.ioWrite) / elapsed,
		})
	}

	c.lastTotal = totalCPU
	c.lastTime = now
	c.lastCPU = currentCPU
	c.lastIO = currentIO

	return out
}

func scanProcesses(totalDelta, elapsed float64, lastCPU map[int]uint64, lastIO map[int]ioSample) ([]processSample, map[int]uint64, map[int]ioSample) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, lastCPU, lastIO
	}

	procs := make([]processSample, 0, 128)
	currentCPU := make(map[int]uint64)
	currentIO := make(map[int]ioSample)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		statPath := filepath.Join("/proc", entry.Name(), "stat")
		statData, err := os.ReadFile(statPath)
		if err != nil {
			continue
		}
		fields := parseStat(string(statData))
		if len(fields) < 24 {
			continue
		}

		comm := strings.Trim(fields[1], "()")
		utime := parseUint(fields[13])
		stime := parseUint(fields[14])
		total := utime + stime

		prev := lastCPU[pid]
		delta := float64(total - prev)
		cpuPercent := (delta / totalDelta) * 100.0

		rss := parseUint(fields[23])
		rssBytes := rss * uint64(os.Getpagesize())

		readBytes, writeBytes := readProcIO(pid)
		prevIO := lastIO[pid]
		ioReadDelta := readBytes - prevIO.readBytes
		ioWriteDelta := writeBytes - prevIO.writeBytes

		procs = append(procs, processSample{
			pid:      pid,
			name:     comm,
			cpuTotal: total,
			cpuDelta: cpuPercent,
			rssBytes: rssBytes,
			ioRead:   ioReadDelta,
			ioWrite:  ioWriteDelta,
		})

		currentCPU[pid] = total
		currentIO[pid] = ioSample{readBytes: readBytes, writeBytes: writeBytes}
	}

	return procs, currentCPU, currentIO
}

func parseStat(statLine string) []string {
	return strings.Fields(statLine)
}

func parseUint(value string) uint64 {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func readTotalCPU() uint64 {
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
	for _, val := range fields[1:] {
		total += parseUint(val)
	}
	return total
}

func readProcIO(pid int) (uint64, uint64) {
	path := filepath.Join("/proc", strconv.Itoa(pid), "io")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}
	var readBytes, writeBytes uint64
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "read_bytes:") {
			readBytes = parseUint(strings.TrimSpace(strings.TrimPrefix(line, "read_bytes:")))
		}
		if strings.HasPrefix(line, "write_bytes:") {
			writeBytes = parseUint(strings.TrimSpace(strings.TrimPrefix(line, "write_bytes:")))
		}
	}
	return readBytes, writeBytes
}
