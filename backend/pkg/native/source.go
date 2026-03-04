//go:build linux

package native

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/monitoring/sources"
	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NativeSource provides low-overhead host metrics from procfs.
type NativeSource struct {
	enabled  bool
	mu       sync.RWMutex
	running  bool
	healthy  bool
	lastErr  string
	lastSeen time.Time
}

func NewNativeSource() *NativeSource {
	return &NativeSource{
		enabled: true,
	}
}

func (s *NativeSource) Name() string {
	return "native"
}

func (s *NativeSource) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		s.running = false
		s.healthy = false
		s.lastErr = "disabled"
		return nil
	}
	s.running = true
	s.healthy = true
	s.lastErr = ""
	s.lastSeen = time.Now()
	return nil
}

func (s *NativeSource) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.healthy = false
	s.lastErr = ""
	return nil
}

func (s *NativeSource) Status() sources.SourceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sources.SourceStatus{
		Name:      s.Name(),
		Enabled:   s.enabled,
		Running:   s.running,
		Healthy:   s.healthy,
		LastError: s.lastErr,
		LastSeen:  s.lastSeen,
	}
}

func (s *NativeSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	now := time.Now()
	load1, loadErr := readLoad1()
	memTotal, memAvailable, memErr := readMem()
	procsRunning, procErr := readProcsRunning()

	metrics := []*proto.Metric{
		buildGaugeMetric("native.cpu.load1", load1, now),
		buildGaugeMetric("native.mem.total_bytes", float64(memTotal), now),
		buildGaugeMetric("native.mem.available_bytes", float64(memAvailable), now),
		buildGaugeMetric("native.procs.running", float64(procsRunning), now),
	}
	if memTotal > 0 && memAvailable <= memTotal {
		memUsed := float64(memTotal - memAvailable)
		metrics = append(metrics,
			buildGaugeMetric("native.mem.used_bytes", memUsed, now),
			buildGaugeMetric("native.mem.used_percent", memUsed/float64(memTotal)*100.0, now),
		)
	}

	s.mu.Lock()
	s.lastSeen = now
	s.healthy = true
	s.lastErr = ""
	if loadErr != nil || memErr != nil || procErr != nil {
		s.healthy = false
		s.lastErr = joinCollectErrors(loadErr, memErr, procErr)
	}
	s.mu.Unlock()

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "native",
		CollectedAt: timestamppb.New(now),
	}, nil
}

func buildGaugeMetric(name string, value float64, ts time.Time) *proto.Metric {
	return &proto.Metric{
		Name: name,
		Type: proto.MetricType_METRIC_TYPE_GAUGE,
		Points: []*proto.MetricPoint{
			{
				Timestamp: timestamppb.New(ts),
				Value:     value,
			},
		},
	}
}

func readLoad1() (float64, error) {
	content, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0, fmt.Errorf("loadavg is empty")
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parse loadavg: %w", err)
	}
	return load1, nil
}

func readMem() (uint64, uint64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	var totalKB uint64
	var availKB uint64

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if parsed, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
					totalKB = parsed
				}
			}
		case strings.HasPrefix(line, "MemAvailable:"):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if parsed, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
					availKB = parsed
				}
			}
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, 0, scanErr
	}
	if totalKB == 0 {
		return 0, 0, fmt.Errorf("memtotal missing")
	}
	return totalKB * 1024, availKB * 1024, nil
}

func readProcsRunning() (uint64, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "procs_running ") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("procs_running malformed")
			}
			value, parseErr := strconv.ParseUint(fields[1], 10, 64)
			if parseErr != nil {
				return 0, fmt.Errorf("parse procs_running: %w", parseErr)
			}
			return value, nil
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return 0, scanErr
	}
	return 0, fmt.Errorf("procs_running missing")
}

func joinCollectErrors(errs ...error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "; ")
}
