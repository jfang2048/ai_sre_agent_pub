package collector

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"encoding/json"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/collect"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/transport"
	"github.com/jfang2048/ai_sre_agent_pub/internal/probe"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"strings"
)

// Collector is the push-first telemetry collector.
type Collector struct {
	cfg       Config
	logger    *zap.Logger
	collector *probe.Collector
	procTopK  *collect.ProcessCollector
	logTail   *collect.LogCollector
	shm       *collect.ShmCollector
	spool     *spool.Spool
	transport *transport.Client
	info      *telemetryv1.CollectorInfo
	batchSeq  int64
	level     int
}

// New creates a new push-first collector.
func New(cfg Config, logger *zap.Logger) (*Collector, error) {
	if logger == nil {
		logger, _ = zap.NewProduction()
	}
	if cfg.Hostname == "" {
		hostname, _ := os.Hostname()
		cfg.Hostname = hostname
	}
	if cfg.CollectorID == "" {
		cfg.CollectorID = fmt.Sprintf("%s-%d", cfg.Hostname, time.Now().UnixNano())
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}

	collectionLevel := cfg.Level
	if collectionLevel <= 0 {
		collectionLevel = 2
	}

	ebpfCfg := probe.EBPFConfig{
		Enabled:     cfg.EBPF.Enabled,
		SocketPath:  cfg.EBPF.SocketPath,
		Categories:  cfg.EBPF.Categories,
		MaxMsgBytes: cfg.EBPF.MaxMsgBytes,
	}

	collector, err := probe.NewCollector(
		probe.WithLevel(collectionLevel),
		probe.WithEBPF(ebpfCfg),
	)
	if err != nil {
		return nil, err
	}

	procTopK := collect.NewProcessCollector(cfg.TopK)
	logCollector := collect.NewLogCollector(cfg.LogPaths, cfg.TopK)
	var shmCollector *collect.ShmCollector
	if cfg.ShmEnabled {
		shmCollector = collect.NewShmCollector(cfg.ShmName)
	}

	spooler, err := spool.New(cfg.SpoolDir, cfg.SpoolMaxBytes)
	if err != nil {
		return nil, err
	}

	transportClient := transport.New(cfg.ControllerEndpoints, cfg.MirrorSend, cfg.GrpcCompress, logger)

	collectorInfo := &telemetryv1.CollectorInfo{
		CollectorId: cfg.CollectorID,
		Hostname:    cfg.Hostname,
		Version:     cfg.Version,
		Os:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Labels:      buildLabels(cfg.Labels),
	}

	return &Collector{
		cfg:       cfg,
		logger:    logger.With(zap.String("component", "collector")),
		collector: collector,
		procTopK:  procTopK,
		logTail:   logCollector,
		shm:       shmCollector,
		spool:     spooler,
		transport: transportClient,
		info:      collectorInfo,
		level:     collectionLevel,
	}, nil
}

// Run starts the collector loop.
func (c *Collector) Run(ctx context.Context) error {
	c.collector.Start()
	defer c.collector.Stop()
	if c.shm != nil {
		defer c.shm.Close()
	}
	ticker := time.NewTicker(c.cfg.CollectionInterval)
	defer ticker.Stop()

	c.logger.Info("collector started",
		zap.String("collector_id", c.cfg.CollectorID),
		zap.Strings("controllers", c.cfg.ControllerEndpoints),
		zap.Duration("interval", c.cfg.CollectionInterval),
		zap.Int("level", c.level))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.collectAndSend(ctx); err != nil {
				c.logger.Warn("collect/send failed", zap.Error(err))
			}
		}
	}
}

func (c *Collector) collectAndSend(ctx context.Context) error {
	batch, err := c.collectBatch(ctx)
	if err != nil {
		return err
	}

	payload, err := proto.Marshal(batch)
	if err != nil {
		return err
	}

	if err := c.spool.Enqueue(payload); err != nil {
		return err
	}

	return c.transport.Drain(ctx, c.spool, func(bytes []byte) (string, error) {
		ack, err := c.transport.Send(ctx, bytes)
		if err != nil {
			return "", err
		}
		return ack.BatchId, nil
	})
}

func (c *Collector) collectBatch(ctx context.Context) (*telemetryv1.TelemetryBatch, error) {
	now := time.Now()
	metrics := make([]*telemetryv1.Metric, 0, 256)

	metricBatch, err := c.collector.Collect()
	if err == nil && metricBatch != nil {
		for _, metric := range metricBatch.Metrics {
			metrics = append(metrics, &telemetryv1.Metric{
				Name:              metric.Name,
				Value:             metric.Value,
				TimestampUnixNano: metric.Timestamp.UnixNano(),
				Labels:            buildLabels(metric.Labels),
			})
		}
	}

	processes := c.procTopK.Collect(now)
	logs := c.logTail.Collect(now)
	if c.shm != nil {
		metrics = append(metrics, c.shm.Collect(now)...)
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_shm_metrics_read",
			Value:             float64(c.shm.LastReadCount()),
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"source": "shm"}),
		})
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_shm_read_errors",
			Value:             float64(c.shm.LastErrorCount()),
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"source": "shm"}),
		})
		if capacity := c.shm.Capacity(); capacity > 0 {
			metrics = append(metrics, &telemetryv1.Metric{
				Name:              "collector_shm_buffer_capacity_bytes",
				Value:             float64(capacity),
				TimestampUnixNano: now.UnixNano(),
				Labels:            buildLabels(map[string]string{"source": "shm"}),
			})
		}
	}

	if c.spool != nil {
		backlog, size := c.spool.Stats()
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_spool_backlog_bytes",
			Value:             float64(backlog),
			TimestampUnixNano: now.UnixNano(),
		})
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_spool_size_bytes",
			Value:             float64(size),
			TimestampUnixNano: now.UnixNano(),
		})
	}

	if extMetrics := c.runExternalMetrics(ctx); len(extMetrics) > 0 {
		metrics = append(metrics, extMetrics...)
	}

	if c.transport != nil {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_transport_send_ms",
			Value:             c.transport.LastSendMs(),
			TimestampUnixNano: now.UnixNano(),
		})
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_transport_ack_ms",
			Value:             c.transport.LastAckMs(),
			TimestampUnixNano: now.UnixNano(),
		})
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_transport_errors_total",
			Value:             float64(c.transport.LastErrs()),
			TimestampUnixNano: now.UnixNano(),
		})
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_transport_compressed",
			Value:             boolToFloat(c.transport.LastCompressed()),
			TimestampUnixNano: now.UnixNano(),
		})
	}

	c.batchSeq++
	batchID := fmt.Sprintf("%s-%d", c.cfg.CollectorID, c.batchSeq)

	return &telemetryv1.TelemetryBatch{
		Collector:             c.info,
		WallTimeUnixNano:      now.UnixNano(),
		MonotonicTimeUnixNano: now.UnixNano(),
		Metrics:               metrics,
		Processes:             processes,
		Logs:                  logs,
		BatchId:               batchID,
	}, nil
}

type extMetricPayload struct {
	Metrics []extMetric `json:"metrics"`
}

type extMetric struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
}

// runExternalMetrics executes an optional external command (configured) to collect extra metrics.
// The command must output JSON: {"metrics":[{"name":"foo","value":1.23,"labels":{"k":"v"}}]}
func (c *Collector) runExternalMetrics(ctx context.Context) []*telemetryv1.Metric {
	if c.cfg.ExternalMetricsCmd == "" {
		return nil
	}

	timeout := c.cfg.ExternalMetricsTimeout
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Use shell so operators can pass pipes if needed; still bounded by timeout.
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", c.cfg.ExternalMetricsCmd)
	out, err := cmd.Output()
	if err != nil {
		c.logger.Debug("external metrics command failed", zap.Error(err))
		return nil
	}

	var payload extMetricPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		c.logger.Debug("external metrics json parse failed", zap.Error(err))
		return nil
	}
	if len(payload.Metrics) == 0 {
		return nil
	}

	now := time.Now().UnixNano()
	metrics := make([]*telemetryv1.Metric, 0, len(payload.Metrics))
	for _, m := range payload.Metrics {
		if strings.TrimSpace(m.Name) == "" {
			continue
		}
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              m.Name,
			Value:             m.Value,
			TimestampUnixNano: now,
			Labels:            buildLabels(m.Labels),
		})
	}
	return metrics
}

func buildLabels(labels map[string]string) []*telemetryv1.Label {
	if len(labels) == 0 {
		return nil
	}
	out := make([]*telemetryv1.Label, 0, len(labels))
	for k, v := range labels {
		out = append(out, &telemetryv1.Label{Key: k, Value: v})
	}
	return out
}

func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
