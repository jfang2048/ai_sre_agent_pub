package monitoring

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/monitoring/sources"
	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
)

// Config defines configuration for the collector.
type Config struct {
	ScrapeInterval    time.Duration
	AggregateInterval time.Duration
	EBPF              sources.EBPFConfig
	Process           sources.ProcessConfig
	Proc              sources.ProcConfig
	Hardware          sources.HardwareConfig
	GPU               sources.GPUConfig
	Kubernetes        sources.KubernetesConfig
	SLIConfig         SLIConfig
	SLOConfig         SLOConfig
}

// Collector manages metric sources and orchestrates collection.
// It follows the UNIX principle of composition: multiple independent
// sources are combined into a unified collection system.
type Collector struct {
	config   *Config
	logger   *zap.Logger
	sources  []sources.MetricSource
	interval time.Duration
	stopCh   chan struct{}
	running  bool
	mu       sync.RWMutex
	metricCh chan<- sources.Metric
}

// NewCollector creates a new Collector with the given configuration.
func NewCollector(cfg *Config, logger *zap.Logger) (*Collector, error) {
	if cfg == nil {
		cfg = &Config{ScrapeInterval: 10 * time.Second}
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Collector{
		config:   cfg,
		logger:   logger.With(zap.String("component", "collector")),
		interval: cfg.ScrapeInterval,
		stopCh:   make(chan struct{}),
		running:  false,
		sources:  make([]sources.MetricSource, 0),
	}, nil
}

// RegisterSource adds a new metric source to the collector.
// This allows modular composition of different metric providers.
func (c *Collector) RegisterSource(source sources.MetricSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sources = append(c.sources, source)
	c.logger.Info("registered metric source", zap.String("source", source.Name()))
}

// Start begins the periodic collection loop in a background goroutine.
func (c *Collector) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	stopCh := make(chan struct{})
	c.stopCh = stopCh
	c.running = true
	c.mu.Unlock()

	c.logger.Info("starting collector", zap.Duration("interval", c.interval))
	go c.run(ctx, stopCh)
	return nil
}

// Stop halts the collection loop gracefully.
func (c *Collector) Stop() error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	c.running = false
	stopCh := c.stopCh
	c.mu.Unlock()

	c.logger.Info("stopping collector")
	select {
	case <-stopCh:
	default:
		close(stopCh)
	}
	return nil
}

// SetMetricChannel configures the output channel for collected metrics.
func (c *Collector) SetMetricChannel(ch chan<- sources.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metricCh = ch
}

// CollectOnce performs a single collection cycle and returns all metrics.
// This is useful for one-shot collection scenarios and testing.
func (c *Collector) CollectOnce(ctx context.Context) ([]sources.Metric, error) {
	c.mu.RLock()
	sourceCopy := make([]sources.MetricSource, len(c.sources))
	copy(sourceCopy, c.sources)
	c.mu.RUnlock()

	var mu sync.Mutex
	allMetrics := make([]sources.Metric, 0)
	var wg sync.WaitGroup
	errCh := make(chan error, len(sourceCopy))

	for _, src := range sourceCopy {
		wg.Add(1)
		go func(s sources.MetricSource) {
			defer wg.Done()

			batch, err := s.Collect(ctx)
			if err != nil {
				c.logger.Warn("source collection failed",
					zap.String("source", s.Name()),
					zap.Error(err))
				errCh <- err
				return
			}

			if batch == nil || len(batch.Metrics) == 0 {
				return
			}

			metrics := convertBatchToMetrics(batch, s.Name())

			mu.Lock()
			allMetrics = append(allMetrics, metrics...)
			mu.Unlock()
		}(src)
	}

	wg.Wait()
	close(errCh)

	failedSources := 0
	var firstErr error
	for err := range errCh {
		if err == nil {
			continue
		}
		failedSources++
		if firstErr == nil {
			firstErr = err
		}
	}

	c.logger.Debug("collection cycle complete",
		zap.Int("total_metrics", len(allMetrics)),
		zap.Int("sources", len(sourceCopy)))

	if len(allMetrics) == 0 && failedSources > 0 {
		return nil, fmt.Errorf("collection failed: %d/%d sources failed: %w", failedSources, len(sourceCopy), firstErr)
	}

	return allMetrics, nil
}

// Status returns the health status of all registered sources.
func (c *Collector) Status() map[string]sources.SourceStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := make(map[string]sources.SourceStatus, len(c.sources))
	for _, s := range c.sources {
		sourceStatus := s.Status()
		if sourceStatus.Name == "" {
			sourceStatus.Name = s.Name()
		}
		status[s.Name()] = sourceStatus
	}
	return status
}

// run is the main collection loop that runs in a background goroutine.
func (c *Collector) run(ctx context.Context, stopCh chan struct{}) {
	defer func() {
		c.mu.Lock()
		if c.stopCh == stopCh {
			c.running = false
		}
		c.mu.Unlock()
	}()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("context cancelled, stopping collector")
			return
		case <-stopCh:
			c.logger.Info("stop signal received")
			return
		case <-ticker.C:
			c.collectAll(ctx)
		}
	}
}

// collectAll collects metrics from all sources and pushes them to the metric channel.
func (c *Collector) collectAll(ctx context.Context) {
	c.mu.RLock()
	sourceCopy := make([]sources.MetricSource, len(c.sources))
	copy(sourceCopy, c.sources)
	metricCh := c.metricCh
	c.mu.RUnlock()

	var wg sync.WaitGroup
	for _, src := range sourceCopy {
		wg.Add(1)
		go func(s sources.MetricSource) {
			defer wg.Done()

			batch, err := s.Collect(ctx)
			if err != nil {
				c.logger.Error("collection failed",
					zap.String("source", s.Name()),
					zap.Error(err))
				return
			}

			if batch == nil || len(batch.Metrics) == 0 {
				return
			}

			c.logger.Debug("collected metrics",
				zap.String("source", s.Name()),
				zap.Int("count", len(batch.Metrics)))

			// Push to channel if configured
			if metricCh != nil {
				metrics := convertBatchToMetrics(batch, s.Name())
				for _, m := range metrics {
					select {
					case metricCh <- m:
					default:
						c.logger.Warn("metric channel full, dropping metric",
							zap.String("metric", m.Name),
							zap.String("source", s.Name()))
					}
				}
			}
		}(src)
	}
	wg.Wait()
}

// convertBatchToMetrics converts a protobuf MetricBatch to internal Metric format.
// This centralizes the conversion logic to maintain DRY principle.
func convertBatchToMetrics(batch *proto.MetricBatch, sourceName string) []sources.Metric {
	if batch == nil || len(batch.Metrics) == 0 {
		return nil
	}

	metrics := make([]sources.Metric, 0, len(batch.Metrics))
	now := time.Now()

	for _, m := range batch.Metrics {
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		if len(m.Points) == 0 {
			continue
		}
		value := 0.0
		hasFinitePoint := false
		for _, point := range m.Points {
			if point == nil {
				continue
			}
			if math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
				continue
			}
			value = point.Value
			hasFinitePoint = true
			break
		}
		if !hasFinitePoint {
			continue
		}

		labels := make(map[string]string, len(m.Labels))
		for _, l := range m.Labels {
			if l == nil {
				continue
			}
			key := strings.TrimSpace(l.Key)
			if key == "" {
				continue
			}
			labels[key] = l.Value
		}

		metrics = append(metrics, sources.Metric{
			Name:      name,
			Type:      m.Type.String(),
			Value:     value,
			Timestamp: now,
			Source:    sourceName,
			Labels:    labels,
		})
	}

	return metrics
}
