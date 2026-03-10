package timeseries

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

// Service provides durable controller-side metric history and in-memory fallback.
type Service struct {
	cfg      Config
	logger   *zap.Logger
	client   *influxClient
	fallback ingest.MetricHistoryProvider

	mu      sync.RWMutex
	cancel  context.CancelFunc
	running bool
	status  Status
	queue   chan []metricPoint
}

// NewService creates a new controller-side timeseries service.
func NewService(cfg Config, fallback ingest.MetricHistoryProvider, logger *zap.Logger) (*Service, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = normalizeConfig(cfg)
	service := &Service{
		cfg:      cfg,
		logger:   logger.With(zap.String("component", "controller_timeseries")),
		fallback: fallback,
		status: Status{
			Enabled:          cfg.Enabled,
			Provider:         cfg.Provider,
			Mode:             "disabled",
			Ready:            !cfg.Enabled,
			Healthy:          !cfg.Enabled,
			FallbackToMemory: cfg.FallbackToMemory,
			ManageBucket:     cfg.ManageBucket,
			Endpoint:         cfg.URL,
			Org:              cfg.Org,
			Bucket:           cfg.Bucket,
			Measurement:      cfg.Measurement,
			Retention:        cfg.Retention.String(),
			WriteBatchSize:   cfg.WriteBatchSize,
			WriteQueueSize:   cfg.WriteQueueSize,
			FlushInterval:    cfg.FlushInterval.String(),
			QueryTimeout:     cfg.QueryTimeout.String(),
			HealthInterval:   cfg.HealthInterval.String(),
			BackupDirectory:  cfg.BackupDirectory,
		},
	}
	if !cfg.Enabled {
		service.status.Mode = "memory"
		return service, nil
	}
	if cfg.Provider != defaultProvider {
		return nil, fmt.Errorf("unsupported tsdb provider %q", cfg.Provider)
	}
	if strings.TrimSpace(cfg.URL) == "" || strings.TrimSpace(cfg.Org) == "" || strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("tsdb url, org, and bucket must be configured")
	}
	service.client = newInfluxClient(cfg)
	service.queue = make(chan []metricPoint, cfg.WriteQueueSize)
	service.status.Mode = "tsdb"
	return service, nil
}

// Start activates background write batching and validates TSDB availability.
func (s *Service) Start(ctx context.Context) error {
	if s == nil || !s.cfg.Enabled || s.client == nil {
		return nil
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true
	s.mu.Unlock()

	checkCtx, checkCancel := context.WithTimeout(runCtx, s.cfg.QueryTimeout)
	defer checkCancel()
	if err := s.client.Health(checkCtx); err != nil {
		s.recordHealthError(err)
		if !s.cfg.FallbackToMemory {
			cancel()
			s.mu.Lock()
			s.cancel = nil
			s.running = false
			s.mu.Unlock()
			return err
		}
	} else {
		s.recordHealthSuccess()
	}

	if s.cfg.ManageBucket {
		manageCtx, manageCancel := context.WithTimeout(runCtx, s.cfg.QueryTimeout)
		err := s.client.EnsureBucket(manageCtx, s.cfg.Retention)
		manageCancel()
		if err != nil {
			s.recordHealthError(err)
			if !s.cfg.FallbackToMemory {
				cancel()
				s.mu.Lock()
				s.cancel = nil
				s.running = false
				s.mu.Unlock()
				return err
			}
		}
	}

	go s.runWriter(runCtx)
	go s.runHealthLoop(runCtx)
	return nil
}

// Close stops background writes and performs a final flush.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.running = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Status returns current timeseries runtime state.
func (s *Service) Status() Status {
	if s == nil {
		return Status{Enabled: false, Ready: true, Healthy: true, Mode: "memory"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status
	if s.queue != nil {
		status.QueueDepth = len(s.queue)
	}
	return status
}

// ProcessBatch converts trend-safe batch metrics into durable points.
func (s *Service) ProcessBatch(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) {
	if s == nil || !s.cfg.Enabled || s.client == nil || batch == nil || collectorID == "" {
		return
	}
	points := aggregateBatchMetrics(collectorID, batch, receivedAt)
	if len(points) == 0 {
		return
	}
	select {
	case s.queue <- points:
	default:
		s.recordDroppedBatch()
	}
}

// MetricHistory returns durable history when available and falls back to memory otherwise.
func (s *Service) MetricHistory(collectorID string, since time.Time, limit int) []ingest.MetricHistorySample {
	if s == nil {
		return nil
	}
	if collectorID == "" {
		return []ingest.MetricHistorySample{}
	}
	if !s.cfg.Enabled || s.client == nil {
		return s.fallbackHistory(collectorID, since, limit)
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.QueryTimeout)
	points, err := s.client.QueryMetricHistory(ctx, s.cfg.Measurement, collectorID, since)
	cancel()
	if err != nil {
		s.recordQueryError(err)
		return s.fallbackHistory(collectorID, since, limit)
	}

	s.recordQuerySuccess()
	samples := buildHistorySamples(points, limit)
	if len(samples) == 0 {
		return s.fallbackHistory(collectorID, since, limit)
	}
	return samples
}

func (s *Service) fallbackHistory(collectorID string, since time.Time, limit int) []ingest.MetricHistorySample {
	if s.fallback == nil {
		return []ingest.MetricHistorySample{}
	}
	return s.fallback.MetricHistory(collectorID, since, limit)
}

func (s *Service) runWriter(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()

	buffer := make([]metricPoint, 0, s.cfg.WriteBatchSize)
	flush := func() {
		if len(buffer) == 0 {
			return
		}
		if err := s.flushBuffer(ctx, buffer); err != nil {
			s.recordWriteError(err)
		} else {
			s.recordWriteSuccess()
		}
		buffer = buffer[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case points := <-s.queue:
			buffer = append(buffer, points...)
			if len(buffer) >= s.cfg.WriteBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (s *Service) runHealthLoop(ctx context.Context) {
	if s == nil || s.client == nil || s.cfg.HealthInterval <= 0 {
		return
	}
	ticker := time.NewTicker(s.cfg.HealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
			err := s.client.Health(checkCtx)
			cancel()
			if err != nil {
				s.recordHealthError(err)
				continue
			}
			s.recordHealthSuccess()
		}
	}
}

func (s *Service) flushBuffer(ctx context.Context, buffer []metricPoint) error {
	if len(buffer) == 0 || s.client == nil {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(ctx, s.cfg.QueryTimeout)
	defer cancel()
	return s.client.Write(writeCtx, s.cfg.Measurement, buffer)
}

func aggregateBatchMetrics(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) []metricPoint {
	metrics := make(map[string]float64)
	for _, metric := range batch.GetMetrics() {
		if metric == nil || metric.GetName() == "" || !ingest.IsTrendMetric(metric.GetName()) {
			continue
		}
		if ingest.IsAggregatedMetric(metric.GetName()) {
			metrics[metric.GetName()] += metric.GetValue()
			continue
		}
		metrics[metric.GetName()] = metric.GetValue()
	}
	if len(metrics) == 0 {
		return nil
	}
	names := make([]string, 0, len(metrics))
	for name := range metrics {
		names = append(names, name)
	}
	sort.Strings(names)
	points := make([]metricPoint, 0, len(names))
	hostname := strings.TrimSpace(batch.GetCollector().GetHostname())
	for _, name := range names {
		points = append(points, metricPoint{
			CollectorID: collectorID,
			Hostname:    hostname,
			Metric:      name,
			Value:       metrics[name],
			Timestamp:   receivedAt.UTC(),
		})
	}
	return points
}

func buildHistorySamples(points []metricPoint, limit int) []ingest.MetricHistorySample {
	if len(points) == 0 {
		return []ingest.MetricHistorySample{}
	}
	sort.Slice(points, func(i, j int) bool {
		if !points[i].Timestamp.Equal(points[j].Timestamp) {
			return points[i].Timestamp.Before(points[j].Timestamp)
		}
		return points[i].Metric < points[j].Metric
	})

	out := make([]ingest.MetricHistorySample, 0, len(points)/4+1)
	current := ingest.MetricHistorySample{
		Timestamp: points[0].Timestamp.UTC(),
		Metrics:   map[string]float64{},
	}
	for _, point := range points {
		if !point.Timestamp.Equal(current.Timestamp) {
			out = append(out, current)
			current = ingest.MetricHistorySample{
				Timestamp: point.Timestamp.UTC(),
				Metrics:   map[string]float64{},
			}
		}
		current.Metrics[point.Metric] = point.Value
	}
	if len(current.Metrics) > 0 {
		out = append(out, current)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (s *Service) recordWriteSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Ready = true
	s.status.Healthy = true
	s.status.FallbackActive = false
	s.status.Mode = "tsdb"
	s.status.LastWriteAt = time.Now().UTC()
	s.status.LastWriteError = ""
	s.status.DegradedReason = ""
}

func (s *Service) recordWriteError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Ready = s.cfg.FallbackToMemory
	s.status.Healthy = false
	s.status.FallbackActive = s.cfg.FallbackToMemory
	s.status.Mode = fallbackMode(s.cfg.FallbackToMemory)
	s.status.LastWriteAt = time.Now().UTC()
	if err != nil {
		s.status.LastWriteError = err.Error()
		s.status.DegradedReason = err.Error()
	}
}

func (s *Service) recordQuerySuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Ready = true
	s.status.Healthy = true
	s.status.FallbackActive = false
	s.status.Mode = "tsdb"
	s.status.LastQueryAt = time.Now().UTC()
	s.status.LastQueryError = ""
	s.status.DegradedReason = ""
}

func (s *Service) recordQueryError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Ready = s.cfg.FallbackToMemory
	s.status.Healthy = false
	s.status.FallbackActive = s.cfg.FallbackToMemory
	s.status.Mode = fallbackMode(s.cfg.FallbackToMemory)
	s.status.LastQueryAt = time.Now().UTC()
	if err != nil {
		s.status.LastQueryError = err.Error()
		s.status.DegradedReason = err.Error()
	}
}

func (s *Service) recordHealthSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Ready = true
	s.status.Healthy = true
	s.status.FallbackActive = false
	s.status.Mode = "tsdb"
	s.status.LastHealthAt = time.Now().UTC()
	s.status.LastHealthError = ""
	s.status.DegradedReason = ""
}

func (s *Service) recordHealthError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Ready = s.cfg.FallbackToMemory
	s.status.Healthy = false
	s.status.FallbackActive = s.cfg.FallbackToMemory
	s.status.Mode = fallbackMode(s.cfg.FallbackToMemory)
	s.status.LastHealthAt = time.Now().UTC()
	if err != nil {
		s.status.LastHealthError = err.Error()
		s.status.DegradedReason = err.Error()
	}
}

func (s *Service) recordDroppedBatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.DroppedBatches++
	s.status.FallbackActive = s.cfg.FallbackToMemory
	s.status.Mode = fallbackMode(s.cfg.FallbackToMemory)
	s.status.LastWriteError = "tsdb write queue full; batch dropped"
	s.status.DegradedReason = s.status.LastWriteError
}

func fallbackMode(enabled bool) string {
	if enabled {
		return "memory-fallback"
	}
	return "tsdb"
}
