package ingest

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
)

const (
	maxMetricsPerBatch   = 20000
	maxProcessesPerBatch = 5000
	maxLogsPerBatch      = 5000
	maxNameLength        = 256
	maxLabelLength       = 256
	maxBatchIDLength     = 256
	maxCollectorIDLength = 128
)

// Server implements TelemetryIngest.
type Server struct {
	telemetryv1.UnimplementedTelemetryIngestServer
	store      Store
	logger     *zap.Logger
	processors []Processor
}

// Processor can observe or derive additional data from incoming telemetry batches.
// Processors must be fast and non-blocking; they should do best-effort work and never return errors.
type Processor interface {
	ProcessBatch(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time)
}

// NewServer creates a new ingest server.
func NewServer(store Store, logger *zap.Logger, processors ...Processor) *Server {
	return &Server{
		store:      store,
		logger:     logger.With(zap.String("component", "ingest")),
		processors: processors,
	}
}

// Push receives telemetry batches over a gRPC stream.
func (s *Server) Push(stream telemetryv1.TelemetryIngest_PushServer) error {
	for {
		batch, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := validateBatch(batch); err != nil {
			s.logger.Warn("rejecting invalid telemetry batch", zap.Error(err))
			return status.Error(codes.InvalidArgument, err.Error())
		}

		receivedAt := time.Now()
		collectorID := "unknown"
		if batch.Collector != nil {
			collectorID = batch.Collector.CollectorId
			s.store.UpsertCollector(batch.Collector, receivedAt)
		}
		s.store.StoreBatchMeta(collectorID, batch, receivedAt)
		if len(batch.Metrics) > 0 {
			s.store.StoreMetrics(collectorID, batch.Metrics, receivedAt)
		}
		if len(batch.Processes) > 0 {
			s.store.StoreProcesses(collectorID, batch.Processes, receivedAt)
		}
		if len(batch.Logs) > 0 {
			s.store.StoreLogs(collectorID, batch.Logs, receivedAt)
		}

		for _, p := range s.processors {
			if p != nil {
				p.ProcessBatch(collectorID, batch, receivedAt)
			}
		}

		ack := &telemetryv1.Ack{BatchId: batch.BatchId}
		if err := stream.Send(ack); err != nil {
			return err
		}
	}
}

// Register registers the server with a gRPC registrar.
func (s *Server) Register(registrar interface {
	RegisterService(*grpc.ServiceDesc, interface{})
}) {
	telemetryv1.RegisterTelemetryIngestServer(registrar, s)
}

// HealthCheck provides a simple health check for the ingest subsystem.
func (s *Server) HealthCheck(ctx context.Context) error {
	_, _ = ctx, s
	return nil
}

func validateBatch(batch *telemetryv1.TelemetryBatch) error {
	if batch == nil {
		return fmt.Errorf("batch cannot be nil")
	}
	if strings.TrimSpace(batch.BatchId) == "" {
		return fmt.Errorf("batch_id is required")
	}
	if len(batch.BatchId) > maxBatchIDLength {
		return fmt.Errorf("batch_id too long")
	}
	if batch.Collector == nil {
		return fmt.Errorf("collector is required")
	}
	if err := validateCollector(batch.Collector); err != nil {
		return err
	}
	if len(batch.Metrics) > maxMetricsPerBatch {
		return fmt.Errorf("too many metrics: %d", len(batch.Metrics))
	}
	if len(batch.Processes) > maxProcessesPerBatch {
		return fmt.Errorf("too many process samples: %d", len(batch.Processes))
	}
	if len(batch.Logs) > maxLogsPerBatch {
		return fmt.Errorf("too many log fingerprints: %d", len(batch.Logs))
	}

	for i, metric := range batch.Metrics {
		if err := validateMetric(metric); err != nil {
			return fmt.Errorf("metric[%d]: %w", i, err)
		}
	}
	for i, process := range batch.Processes {
		if err := validateProcess(process); err != nil {
			return fmt.Errorf("process[%d]: %w", i, err)
		}
	}
	for i, log := range batch.Logs {
		if err := validateLog(log); err != nil {
			return fmt.Errorf("log[%d]: %w", i, err)
		}
	}
	return nil
}

func validateCollector(collector *telemetryv1.CollectorInfo) error {
	if strings.TrimSpace(collector.CollectorId) == "" {
		return fmt.Errorf("collector_id is required")
	}
	if len(collector.CollectorId) > maxCollectorIDLength {
		return fmt.Errorf("collector_id too long")
	}
	if strings.TrimSpace(collector.Hostname) == "" {
		return fmt.Errorf("collector hostname is required")
	}
	for _, label := range collector.Labels {
		if err := validateLabel(label); err != nil {
			return fmt.Errorf("collector label invalid: %w", err)
		}
	}
	return nil
}

func validateMetric(metric *telemetryv1.Metric) error {
	if metric == nil {
		return fmt.Errorf("metric cannot be nil")
	}
	if strings.TrimSpace(metric.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(metric.Name) > maxNameLength {
		return fmt.Errorf("name too long")
	}
	if !finite(metric.Value) {
		return fmt.Errorf("value must be finite")
	}
	for _, label := range metric.Labels {
		if err := validateLabel(label); err != nil {
			return err
		}
	}
	return nil
}

func validateProcess(process *telemetryv1.ProcessSample) error {
	if process == nil {
		return fmt.Errorf("process sample cannot be nil")
	}
	if process.Pid < 0 {
		return fmt.Errorf("pid must be non-negative")
	}
	if len(process.Name) > maxNameLength {
		return fmt.Errorf("process name too long")
	}
	if !finite(process.CpuPercent) || !finite(process.IoReadBps) || !finite(process.IoWriteBps) {
		return fmt.Errorf("process contains non-finite numeric values")
	}
	return nil
}

func validateLog(log *telemetryv1.LogFingerprint) error {
	if log == nil {
		return fmt.Errorf("log fingerprint cannot be nil")
	}
	if strings.TrimSpace(log.Fingerprint) == "" {
		return fmt.Errorf("fingerprint is required")
	}
	if len(log.Fingerprint) > maxNameLength {
		return fmt.Errorf("fingerprint too long")
	}
	if len(log.Example) > 4096 {
		return fmt.Errorf("example too long")
	}
	return nil
}

func validateLabel(label *telemetryv1.Label) error {
	if label == nil {
		return fmt.Errorf("label cannot be nil")
	}
	key := strings.TrimSpace(label.Key)
	if key == "" {
		return fmt.Errorf("label key is required")
	}
	if len(key) > maxLabelLength {
		return fmt.Errorf("label key too long")
	}
	if len(label.Value) > maxLabelLength {
		return fmt.Errorf("label value too long")
	}
	if strings.ContainsAny(key, "\n\r\t") || strings.ContainsAny(label.Value, "\n\r\t") {
		return fmt.Errorf("label contains control characters")
	}
	return nil
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
