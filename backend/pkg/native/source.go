//go:build linux

package native

import (
	"context"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/monitoring/sources"
	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NativeSource provides system metrics using native Go implementations
// Falls back to /proc parsing when C++ collector is unavailable
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
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
	now := time.Now()

	// Stub implementation - returns minimal metrics
	// In production, this would interface with the C++ eBPF collector
	metrics := []*proto.Metric{
		{
			Name: "native.cpu.load",
			Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Points: []*proto.MetricPoint{
				{
					Timestamp: timestamppb.New(now),
					Value:     0.0,
				},
			},
		},
		{
			Name: "native.mem.total",
			Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Points: []*proto.MetricPoint{
				{
					Timestamp: timestamppb.New(now),
					Value:     0.0,
				},
			},
		},
		{
			Name: "native.procs.count",
			Type: proto.MetricType_METRIC_TYPE_GAUGE,
			Points: []*proto.MetricPoint{
				{
					Timestamp: timestamppb.New(now),
					Value:     0.0,
				},
			},
		},
	}

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "native",
		CollectedAt: timestamppb.New(now),
	}, nil
}

// collectSystemStats collects system stats using Go instead of C++
func collectSystemStats() []sources.Metric {
	// This would be implemented with Go-based /proc parsing
	return []sources.Metric{}
}
