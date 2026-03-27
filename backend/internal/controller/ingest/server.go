package ingest

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
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
	recentBatchWindow    = 256

	auxPayloadRefreshedMetric = "collector_aux_payload_refreshed"
)

// Server implements TelemetryIngest.
type Server struct {
	telemetryv1.UnimplementedTelemetryIngestServer
	store         Store
	logger        *zap.Logger
	processors    []Processor
	recentBatches map[string]*recentBatchSet
	mu            sync.RWMutex
	stats         Stats
}

// Stats summarizes ingest behavior and quality.
type Stats struct {
	BatchesTotal    uint64    `json:"batches_total"`
	DuplicatesTotal uint64    `json:"duplicates_total"`
	RejectedTotal   uint64    `json:"rejected_total"`
	MetricsTotal    uint64    `json:"metrics_total"`
	ProcessesTotal  uint64    `json:"processes_total"`
	LogsTotal       uint64    `json:"logs_total"`
	LastBatchAt     time.Time `json:"last_batch_at,omitempty"`
	LastRejectAt    time.Time `json:"last_reject_at,omitempty"`
	LastCollector   string    `json:"last_collector,omitempty"`
	LastBatchID     string    `json:"last_batch_id,omitempty"`
	LastError       string    `json:"last_error,omitempty"`
}

type recentBatchSet struct {
	order []string
	seen  map[string]struct{}
}

// Schema captures ingest validation contract exposed to operators and integration tests.
type Schema struct {
	Version              string `json:"version"`
	MaxMetricsPerBatch   int    `json:"max_metrics_per_batch"`
	MaxProcessesPerBatch int    `json:"max_processes_per_batch"`
	MaxLogsPerBatch      int    `json:"max_logs_per_batch"`
	MaxNameLength        int    `json:"max_name_length"`
	MaxLabelLength       int    `json:"max_label_length"`
	MaxBatchIDLength     int    `json:"max_batch_id_length"`
	MaxCollectorIDLength int    `json:"max_collector_id_length"`
}

// Processor can observe or derive additional data from incoming telemetry batches.
// Processors must be fast and non-blocking; they should do best-effort work and never return errors.
type Processor interface {
	ProcessBatch(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time)
}

// NewServer creates a new ingest server.
func NewServer(store Store, logger *zap.Logger, processors ...Processor) *Server {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Server{
		store:         store,
		logger:        logger.With(zap.String("component", "ingest")),
		processors:    processors,
		recentBatches: make(map[string]*recentBatchSet),
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
			s.recordRejected(err)
			return status.Error(codes.InvalidArgument, err.Error())
		}

		receivedAt := time.Now()
		collectorID := "unknown"
		if batch.Collector != nil {
			collectorID = batch.Collector.CollectorId
			s.store.UpsertCollector(batch.Collector, receivedAt)
		}
		if s.isDuplicateBatch(collectorID, batch.BatchId) {
			ack := &telemetryv1.Ack{BatchId: batch.BatchId}
			if err := stream.Send(ack); err != nil {
				return err
			}
			s.recordDuplicate(collectorID, batch.BatchId, receivedAt)
			continue
		}
		s.store.StoreBatchMeta(collectorID, batch, receivedAt)
		if len(batch.Metrics) > 0 {
			s.store.StoreMetrics(collectorID, batch.Metrics, receivedAt)
		}
		if len(batch.Processes) > 0 || auxPayloadRefreshed(batch.Metrics, "process_fallback") {
			s.store.StoreProcesses(collectorID, batch.Processes, receivedAt)
		}
		if len(batch.Logs) > 0 || auxPayloadRefreshed(batch.Metrics, "logs") {
			s.store.StoreLogs(collectorID, batch.Logs, receivedAt)
		}

		for _, p := range s.processors {
			if p != nil {
				s.processBatchSafely(p, collectorID, batch, receivedAt)
			}
		}

		ack := &telemetryv1.Ack{BatchId: batch.BatchId}
		if err := stream.Send(ack); err != nil {
			return err
		}
		s.recordAccepted(batch, collectorID, receivedAt)
	}
}

func (s *Server) processBatchSafely(p Processor, collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("ingest processor panic recovered",
				zap.String("collector_id", collectorID),
				zap.String("batch_id", strings.TrimSpace(batch.GetBatchId())),
				zap.Any("panic", r))
		}
	}()

	p.ProcessBatch(collectorID, batch, receivedAt)
}

func auxPayloadRefreshed(metrics []*telemetryv1.Metric, component string) bool {
	component = strings.TrimSpace(component)
	if component == "" {
		return false
	}
	for _, metric := range metrics {
		if metric == nil || metric.Name != auxPayloadRefreshedMetric || metric.Value < 0.5 {
			continue
		}
		for _, label := range metric.Labels {
			if label != nil && label.Key == "component" && strings.TrimSpace(label.Value) == component {
				return true
			}
		}
	}
	return false
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

// Stats returns ingest counters and latest batch metadata.
func (s *Server) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// Schema returns ingest payload limits and validation contract.
func (s *Server) Schema() Schema {
	return Schema{
		Version:              "v1",
		MaxMetricsPerBatch:   maxMetricsPerBatch,
		MaxProcessesPerBatch: maxProcessesPerBatch,
		MaxLogsPerBatch:      maxLogsPerBatch,
		MaxNameLength:        maxNameLength,
		MaxLabelLength:       maxLabelLength,
		MaxBatchIDLength:     maxBatchIDLength,
		MaxCollectorIDLength: maxCollectorIDLength,
	}
}

func (s *Server) recordAccepted(batch *telemetryv1.TelemetryBatch, collectorID string, receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.BatchesTotal++
	s.stats.MetricsTotal += uint64(len(batch.Metrics))
	s.stats.ProcessesTotal += uint64(len(batch.Processes))
	s.stats.LogsTotal += uint64(len(batch.Logs))
	s.stats.LastBatchAt = receivedAt
	s.stats.LastCollector = strings.TrimSpace(collectorID)
	s.stats.LastBatchID = strings.TrimSpace(batch.BatchId)
}

func (s *Server) recordDuplicate(collectorID, batchID string, receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.DuplicatesTotal++
	s.stats.LastBatchAt = receivedAt
	s.stats.LastCollector = strings.TrimSpace(collectorID)
	s.stats.LastBatchID = strings.TrimSpace(batchID)
}

func (s *Server) recordRejected(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.RejectedTotal++
	s.stats.LastRejectAt = time.Now()
	if err != nil {
		s.stats.LastError = err.Error()
	}
}

func (s *Server) isDuplicateBatch(collectorID, batchID string) bool {
	collectorID = strings.TrimSpace(collectorID)
	batchID = strings.TrimSpace(batchID)
	if collectorID == "" || batchID == "" {
		return false
	}
	if s.store != nil {
		if node := s.store.Node(collectorID); node != nil && strings.TrimSpace(node.LastBatchID) == batchID {
			return true
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.recentBatches[collectorID]
	if entry == nil {
		entry = &recentBatchSet{
			order: make([]string, 0, recentBatchWindow),
			seen:  make(map[string]struct{}, recentBatchWindow),
		}
		s.recentBatches[collectorID] = entry
	}
	if _, ok := entry.seen[batchID]; ok {
		return true
	}
	entry.seen[batchID] = struct{}{}
	entry.order = append(entry.order, batchID)
	if len(entry.order) > recentBatchWindow {
		evicted := entry.order[0]
		entry.order = entry.order[1:]
		delete(entry.seen, evicted)
	}
	return false
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
