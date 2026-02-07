package ingest

import (
	"context"
	"io"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip"
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
