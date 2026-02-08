package sources

import (
	"context"
	"fmt"
	"os"
	"sync"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
)

// EbpfSource collects metrics using eBPF and perf events
type EbpfSource struct {
	BaseSource
	config    EBPFConfig
	logger    *zap.Logger
	collector *EBPFCollector
	mu        sync.RWMutex
}

// NewEBPFSource creates a new eBPF source
func NewEBPFSource(config EBPFConfig, logger *zap.Logger) (*EbpfSource, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("eBPF source is disabled")
	}

	return &EbpfSource{
		BaseSource: BaseSource{
			name:    "ebpf",
			enabled: config.Enabled,
		},
		config:    config,
		logger:    logger.With(zap.String("source", "ebpf")),
		collector: NewEBPFCollector(config, logger),
	}, nil
}

// Start starts the eBPF source
func (s *EbpfSource) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.config.Enabled {
		return nil
	}

	if os.Geteuid() != 0 {
		msg := "eBPF source requires root or CAP_SYS_ADMIN/CAP_PERFMON; disabling"
		s.logger.Warn(msg)
		s.enabled = false
		s.setStatus(false, false, msg)
		return nil
	}

	if err := s.collector.Start(ctx); err != nil {
		s.setStatus(false, false, err.Error())
		return err
	}

	s.setStatus(true, true, "")
	s.logger.Info("eBPF source started")
	return nil
}

// Stop stops the eBPF source
func (s *EbpfSource) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.enabled {
		return nil
	}

	if err := s.collector.Stop(); err != nil {
		s.logger.Error("error stopping eBPF collector", zap.Error(err))
	}

	s.setStatus(false, false, "")
	s.logger.Info("eBPF source stopped")
	return nil
}

// Collect performs a one-time collection
func (s *EbpfSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.enabled {
		return &proto.MetricBatch{
			Metrics:     []*proto.Metric{},
			Source:      "ebpf",
			CollectedAt: nil,
		}, nil
	}

	return s.collector.Collect(ctx)
}
