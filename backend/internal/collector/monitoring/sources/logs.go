package sources

import (
	"context"
	"fmt"
	"sync"

	"os"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// LogConfig configures the log source
type LogConfig struct {
	Enabled bool `yaml:"enabled"`

	// Journal (journald) settings
	EnableJournal bool   `yaml:"enable_journal"`
	JournalPath   string `yaml:"journal_path"`

	// Syslog settings
	EnableSyslog bool   `yaml:"enable_syslog"`
	SyslogPath   string `yaml:"syslog_path"`

	// Log filtering
	FilterPriorities []string `yaml:"filter_priorities"`
	FilterUnits      []string `yaml:"filter_units"`
	FilterPatterns   []string `yaml:"filter_patterns"`
}

// LogSource collects log metrics
type LogSource struct {
	BaseSource
	config LogConfig
	logger *zap.Logger

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewLogSource creates a new log source
func NewLogSource(config LogConfig, logger *zap.Logger) (*LogSource, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("log source is disabled")
	}

	return &LogSource{
		BaseSource: BaseSource{
			name:    "logs",
			enabled: config.Enabled,
		},
		config: config,
		logger: logger.With(zap.String("source", "logs")),
	}, nil
}

// Start starts the log source
func (s *LogSource) Start(ctx context.Context) error {
	s.setStatus(true, true, "")
	s.logger.Info("log source started")
	return nil
}

// Stop stops the log source
func (s *LogSource) Stop() error {
	s.setStatus(false, false, "")
	s.logger.Info("log source stopped")
	return nil
}

// Collect performs a one-time collection
func (s *LogSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	metrics := []*proto.Metric{}
	now := timestamppb.Now()

	if s.config.EnableSyslog && s.config.SyslogPath != "" {
		info, err := os.Stat(s.config.SyslogPath)
		if err == nil {
			metrics = append(metrics, &proto.Metric{
				Name:   "system.log.syslog_size",
				Type:   proto.MetricType_METRIC_TYPE_GAUGE,
				Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(info.Size())}},
				Labels: []*proto.MetricLabel{{Key: "path", Value: s.config.SyslogPath}},
			})
		}
	}

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "logs",
		CollectedAt: now,
	}, nil
}
