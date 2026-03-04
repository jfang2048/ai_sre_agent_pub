package sources

import (
	"context"
	"fmt"
	"time"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PrometheusConfig configures the prometheus source
type PrometheusConfig struct {
	Enabled        bool          `yaml:"enabled"`
	URL            string        `yaml:"url"`
	Timeout        time.Duration `yaml:"timeout"`
	ScrapeInterval time.Duration `yaml:"scrape_interval"`

	// Queries to run
	Queries []PrometheusQuery `yaml:"queries"`
}

// PrometheusQuery defines a prometheus query
type PrometheusQuery struct {
	Name   string            `yaml:"name"`
	Query  string            `yaml:"query"`
	Labels map[string]string `yaml:"labels"`
}

// PrometheusSource collects metrics from Prometheus
type PrometheusSource struct {
	BaseSource
	config PrometheusConfig
	logger *zap.Logger
	client v1.API
}

// NewPrometheusSource creates a new prometheus source
func NewPrometheusSource(config PrometheusConfig, logger *zap.Logger) (*PrometheusSource, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("prometheus source is disabled")
	}

	if config.URL == "" {
		return nil, fmt.Errorf("prometheus URL is required")
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	if config.ScrapeInterval == 0 {
		config.ScrapeInterval = 30 * time.Second
	}

	client, err := api.NewClient(api.Config{
		Address: config.URL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus client: %w", err)
	}

	return &PrometheusSource{
		BaseSource: BaseSource{
			name:    "prometheus",
			enabled: config.Enabled,
		},
		config: config,
		logger: logger.With(zap.String("source", "prometheus")),
		client: v1.NewAPI(client),
	}, nil
}

// Start starts the prometheus source
func (s *PrometheusSource) Start(ctx context.Context) error {
	s.setStatus(true, true, "")
	s.logger.Info("prometheus source started", zap.String("url", s.config.URL))
	return nil
}

// Stop stops the prometheus source
func (s *PrometheusSource) Stop() error {
	s.setStatus(false, false, "")
	s.logger.Info("prometheus source stopped")
	return nil
}

// Collect performs a one-time collection
func (s *PrometheusSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	metrics, err := s.runQueries(ctx)
	if err != nil {
		return nil, err
	}

	protoMetrics := make([]*proto.Metric, len(metrics))
	for i, m := range metrics {
		labels := []*proto.MetricLabel{}
		for k, v := range m.Labels {
			labels = append(labels, &proto.MetricLabel{Key: k, Value: v})
		}

		protoMetrics[i] = &proto.Metric{
			Name:   m.Name,
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels,
			Points: []*proto.MetricPoint{
				{
					Timestamp: timestamppb.New(m.Timestamp),
					Value:     m.Value,
				},
			},
		}
	}

	return &proto.MetricBatch{
		Metrics:     protoMetrics,
		Source:      "prometheus",
		CollectedAt: timestamppb.Now(),
	}, nil
}

// runQueries executes all configured queries
func (s *PrometheusSource) runQueries(ctx context.Context) ([]Metric, error) {
	var metrics []Metric
	queryCtx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()

	for _, queryConfig := range s.config.Queries {
		result, warnings, err := s.client.Query(queryCtx, queryConfig.Query, time.Now())
		if err != nil {
			s.logger.Warn("query failed",
				zap.String("query", queryConfig.Query),
				zap.Error(err))
			s.setStatus(s.running, false, err.Error())
			continue
		}

		if len(warnings) > 0 {
			s.logger.Debug("query warnings", zap.Any("warnings", warnings))
		}

		// Parse result
		queryMetrics := s.parseQueryResult(result, queryConfig)
		metrics = append(metrics, queryMetrics...)
	}

	s.setStatus(s.running, true, "")
	return metrics, nil
}

// parseQueryResult parses a prometheus query result
func (s *PrometheusSource) parseQueryResult(result model.Value, config PrometheusQuery) []Metric {
	var metrics []Metric
	now := time.Now()

	switch result.Type() {
	case model.ValVector:
		vector := result.(model.Vector)
		for _, sample := range vector {
			labels := make(map[string]string)
			// Add configured labels
			for k, v := range config.Labels {
				labels[k] = v
			}
			// Add metric labels
			for k, v := range sample.Metric {
				labels[string(k)] = string(v)
			}

			name := config.Name
			if name == "" {
				name = string(sample.Metric["__name__"])
			}
			if name == "" {
				name = "prometheus_query"
			}

			metrics = append(metrics, Metric{
				Name:      name,
				Type:      "gauge",
				Value:     float64(sample.Value),
				Timestamp: now,
				Labels:    labels,
				Source:    "prometheus",
			})
		}

	case model.ValScalar:
		scalar := result.(*model.Scalar)
		metrics = append(metrics, Metric{
			Name:      config.Name,
			Type:      "gauge",
			Value:     float64(scalar.Value),
			Timestamp: now,
			Labels:    config.Labels,
			Source:    "prometheus",
		})
	}

	return metrics
}
