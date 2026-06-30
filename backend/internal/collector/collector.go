package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/collect"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probecore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/spool"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/transport"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/observability"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/release"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

const (
	defaultMarshalBufferCap = 64 * 1024
	maxMarshalBufferCap     = 1 * 1024 * 1024
	maxExternalMetrics      = 512
	maxExternalOutputBytes  = 1 * 1024 * 1024
	maxMetricNameRunes      = 200
	maxLabelKeyRunes        = 100
	maxLabelValueRunes      = 256
)

var probeCoreCollectorModuleOrder = []string{
	"host",
	"disk",
	"network",
	"rdma",
	"netlink",
	"ethtool",
	"perf",
	"ebpf",
	"gpu",
	"process",
}

type primaryEBPFRuntime interface {
	Start() error
	Stop()
	GetMetrics(time.Time) []probe.Metric
	Summary() probe.EBPFSummary
	Events(limit int) []probe.EBPFEvent
}

type processCollector interface {
	Collect(time.Time) []*telemetryv1.ProcessSample
}

type logCollector interface {
	Collect(time.Time) []*telemetryv1.LogFingerprint
}

type externalMetricPayloadFetcher func(context.Context, string, time.Duration) (extMetricPayload, error)

// Collector is the push-first telemetry collector.
type Collector struct {
	mu sync.RWMutex

	cfg             Config
	logger          *zap.Logger
	ebpfRuntime     primaryEBPFRuntime
	ebpfExpected    bool
	ebpfHealthy     bool
	ebpfReason      string
	compatProbe     compatibilityProbeRuntime
	probeCore       probeCoreRuntime
	sourcePipeline  *sourcePipeline
	runtimeMode     collectorRuntimeInspection
	compatProcTopK  processCollector
	logTail         logCollector
	shm             *collect.ShmCollector
	spool           *spool.Spool
	transport       *transport.Client
	hardware        *hardwareCache
	protection      *protectionGovernor
	securityAuditor *collectorSecurityAuditor
	auxState        auxiliaryCollectionState
	processState    processPayloadSuppressionState
	lowChurnState   lowChurnSuppressionState
	externalFetch   externalMetricPayloadFetcher
	info            *telemetryv1.CollectorInfo
	profileRuntime  *runtimeProfileRuntime
	level           int

	batchSeq        int64
	currentInterval time.Duration
	failureStreak   int
	jitterUnit      func() float64
	marshalPool     sync.Pool
	promMetrics     *runtimePromMetrics
}

type cycleSnapshot struct {
	cpuPercent       float64
	spoolBacklog     int64
	probeSource      string
	signalPressure   int
	selfCPUPercent   float64
	selfRSSBytes     uint64
	protectionMode   protectionMode
	drainMaxRecords  int
	drainMaxDuration time.Duration
}

type extMetricPayload struct {
	Metrics []extMetric `json:"metrics"`
}

type extMetric struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
}

var (
	errExternalMetricName = errors.New("external metric name is invalid")
	errExternalMetricCmd  = errors.New("external metric command is invalid")
)

// New creates a new push-first collector.
func New(cfg Config, logger *zap.Logger) (*Collector, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid collector config: %w", err)
	}

	cfg = withRuntimeIdentity(cfg)
	runtimeMode := detectCollectorRuntimeInspection(cfg)
	hardware := newHardwareCache(logger)
	hardwareProfile := hardware.RefreshIfNeeded(time.Now(), cfg.Hardware)
	if cfg.Hardware.Enabled {
		cfg = applyHardwareSamplingProfile(cfg, hardwareProfile)
	}
	collectionLevel := cfg.Level
	ebpfCfg := probe.EBPFConfig{
		Enabled:               cfg.EBPF.Enabled,
		SocketPath:            cfg.EBPF.SocketPath,
		Categories:            cfg.EBPF.Categories,
		MaxMsgBytes:           cfg.EBPF.MaxMsgBytes,
		RingSize:              cfg.EBPF.RingSize,
		EventFlushLimit:       cfg.EBPF.EventFlushLimit,
		AllowedListenPorts:    append([]int(nil), cfg.EBPF.AllowedListenPorts...),
		SyntheticPollInterval: cfg.EBPF.SyntheticPollInterval,
		LongLivedTCPThreshold: cfg.EBPF.LongLivedTCPThreshold,
	}

	var compatProbe compatibilityProbeRuntime
	if !cfg.ProbeCore.Enabled || cfg.ProbeCore.FallbackToGo {
		probeCollector, err := probe.NewCollector(
			probe.WithLevel(collectionLevel),
			probe.WithAdaptiveSampling(cfg.CollectionInterval),
			probe.WithSuppressCachedHardwarePayloads(cfg.SuppressCachedCompatHardwareMetrics),
			probe.WithEBPF(ebpfCfg),
		)
		if err != nil {
			return nil, fmt.Errorf("create compatibility probe collector: %w", err)
		}
		compatProbe = probeCollector
	}

	var probeCoreClient probeCoreRuntime
	if cfg.ProbeCore.Enabled {
		coreCfg := probecore.Config{
			BinaryPath:                      cfg.ProbeCore.BinaryPath,
			Collectors:                      append([]string(nil), cfg.ProbeCore.Collectors...),
			Args:                            applyProbeCoreHostMode(cfg.ProbeCore.Args, runtimeMode),
			Interval:                        cfg.ProbeCore.Interval,
			TopK:                            cfg.TopK,
			WindowSamples:                   cfg.ProbeCore.WindowSamples,
			ProcessIntervalSamples:          cfg.ProbeCore.ProcessIntervalSamples,
			HostProcFallbackIntervalSamples: cfg.ProbeCore.HostProcFallbackIntervalSamples,
			PressureIntervalSamples:         cfg.ProbeCore.PressureIntervalSamples,
			NetlinkIntervalSamples:          cfg.ProbeCore.NetlinkIntervalSamples,
			QueueDepth:                      cfg.ProbeCore.QueueDepth,
			Compression:                     cfg.ProbeCore.Compression,
			GPUIntervalSamples:              cfg.ProbeCore.GPUIntervalSamples,
			EBPFSocketPath:                  cfg.EBPF.SocketPath,
			StartupTimeout:                  cfg.ProbeCore.StartupTimeout,
			StaleAfter:                      cfg.ProbeCore.StaleAfter,
			FrameMaxBytes:                   cfg.ProbeCore.FrameMaxBytes,
			Nice:                            cfg.ProbeCore.Nice,
		}
		client, err := probecore.NewClient(coreCfg, logger)
		if err != nil {
			if !cfg.ProbeCore.FallbackToGo {
				return nil, fmt.Errorf("create probe-core client: %w", err)
			}
			logger.Warn("probe-core unavailable during client initialization; enabling compatibility fallback",
				zap.Error(err),
			)
			client = nil
		}
		probeCoreClient = client
	}
	var primaryEBPF primaryEBPFRuntime
	if cfg.EBPF.Enabled {
		primaryEBPF = probe.NewEBPFCollectorWithConfig(ebpfCfg)
	}

	spooler, err := spool.NewWithOptions(cfg.SpoolDir, cfg.SpoolMaxBytes, spool.Options{
		DataSyncInterval:   cfg.SpoolSyncInterval,
		OffsetSyncInterval: cfg.SpoolOffsetSyncInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("create spool: %w", err)
	}

	transportClient, err := transport.New(toTransportConfig(cfg), logger)
	if err != nil {
		return nil, fmt.Errorf("create transport client: %w", err)
	}

	collector := &Collector{
		cfg:             cfg,
		logger:          logger.With(zap.String("component", "collector")),
		ebpfRuntime:     primaryEBPF,
		ebpfExpected:    cfg.EBPF.Enabled,
		compatProbe:     compatProbe,
		probeCore:       probeCoreClient,
		compatProcTopK:  collect.NewProcessCollector(cfg.TopK),
		logTail:         collect.NewLogCollector(cfg.LogPaths, cfg.TopK),
		spool:           spooler,
		transport:       transportClient,
		hardware:        hardware,
		protection:      newProtectionGovernor(logger),
		securityAuditor: newCollectorSecurityAuditor(cfg.Security, logger),
		info:            buildCollectorInfo(cfg),
		profileRuntime:  newRuntimeProfileRuntime(),
		runtimeMode:     runtimeMode,
		level:           collectionLevel,
		currentInterval: cfg.CollectionInterval,
		promMetrics:     newRuntimePromMetrics(),
	}
	appendCollectorInfoRuntimeLabels(collector.info, runtimeMode)
	if cfg.Hardware.Enabled {
		appendCollectorInfoHardwareLabels(collector.info, hardwareProfile)
	}
	if cfg.ShmEnabled {
		collector.shm = collect.NewShmCollector(cfg.ShmName)
	}
	collector.sourcePipeline = newSourcePipeline(collector.probeCore, collector.compatProbe, collector.logger)
	collector.externalFetch = collector.fetchExternalMetricPayload

	collector.marshalPool = sync.Pool{
		New: func() any {
			buf := make([]byte, 0, defaultMarshalBufferCap)
			return &buf
		},
	}
	return collector, nil
}

func withRuntimeIdentity(cfg Config) Config {
	if cfg.Hostname == "" {
		hostname, _ := os.Hostname()
		cfg.Hostname = hostname
	}
	if cfg.CollectorID == "" {
		cfg.CollectorID = fmt.Sprintf("%s-%d", cfg.Hostname, time.Now().UnixNano())
	}
	if cfg.Version == "" {
		cfg.Version = release.EffectiveVersion()
	}
	return cfg
}

func buildCollectorInfo(cfg Config) *telemetryv1.CollectorInfo {
	labels := make(map[string]string, len(cfg.Labels)+1)
	for key, value := range cfg.Labels {
		labels[key] = value
	}
	labels["privilege_profile"] = cfg.PrivilegeProfile
	return &telemetryv1.CollectorInfo{
		CollectorId: cfg.CollectorID,
		Hostname:    cfg.Hostname,
		Version:     cfg.Version,
		Os:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Labels:      buildLabels(labels),
	}
}

func toTransportConfig(cfg Config) transport.Config {
	return transport.Config{
		Endpoints:      append([]string(nil), cfg.ControllerEndpoints...),
		Mirror:         cfg.MirrorSend,
		Compress:       cfg.GrpcCompress,
		DialTimeout:    cfg.Transport.DialTimeout,
		RPCTimeout:     cfg.Transport.RPCTimeout,
		AllowPlaintext: cfg.Transport.AllowPlaintext,
		TLS: transport.TLSConfig{
			Enabled:            cfg.Transport.TLS.Enabled,
			CAFile:             cfg.Transport.TLS.CAFile,
			CertFile:           cfg.Transport.TLS.CertFile,
			KeyFile:            cfg.Transport.TLS.KeyFile,
			ServerName:         cfg.Transport.TLS.ServerName,
			InsecureSkipVerify: cfg.Transport.TLS.InsecureSkipVerify,
			ReloadInterval:     cfg.Transport.TLS.ReloadInterval,
		},
		Auth: transport.AuthConfig{
			Enabled:     cfg.Transport.Auth.Enabled,
			BearerToken: cfg.Transport.Auth.BearerToken,
		},
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ReloadConfig updates mutable runtime settings without restarting the process.
func (c *Collector) ReloadConfig(next Config) error {
	next = applyDeploymentDefaults(next)
	if err := next.Validate(); err != nil {
		c.promMetrics.configReloads.WithLabelValues("failed").Inc()
		return fmt.Errorf("validate reloaded config: %w", err)
	}
	next = withRuntimeIdentity(next)

	if err := c.transport.ApplyConfig(toTransportConfig(next)); err != nil {
		c.promMetrics.configReloads.WithLabelValues("failed").Inc()
		return fmt.Errorf("apply transport config: %w", err)
	}

	c.mu.Lock()
	prev := c.cfg
	c.cfg = next
	c.info = buildCollectorInfo(next)
	appendCollectorInfoRuntimeLabels(c.info, c.runtimeMode)
	if c.hardware != nil && next.Hardware.Enabled {
		appendCollectorInfoHardwareLabels(c.info, c.hardware.RefreshIfNeeded(time.Now(), next.Hardware))
	}
	c.maybeExpireRuntimeProfileLocked(time.Now().UTC())
	c.currentInterval = clampDuration(c.currentInterval, next.MinCollectionInterval, next.MaxCollectionInterval)
	c.mu.Unlock()

	c.promMetrics.configReloads.WithLabelValues("success").Inc()
	c.logger.Info("collector config reloaded",
		zap.Duration("collection_interval", next.CollectionInterval),
		zap.Bool("adaptive_polling", next.AdaptivePolling),
		zap.Strings("controller_endpoints", next.ControllerEndpoints),
		zap.Bool("tls_enabled", next.Transport.TLS.Enabled),
	)
	if prev.Level != next.Level {
		c.logger.Warn("level changed but will apply after restart",
			zap.Int("old_level", prev.Level),
			zap.Int("new_level", next.Level),
		)
	}
	if prev.SpoolDir != next.SpoolDir {
		c.logger.Warn("spool_dir changed but will apply after restart",
			zap.String("old_spool_dir", prev.SpoolDir),
			zap.String("new_spool_dir", next.SpoolDir),
		)
	}
	if prev.ProbeCore.Enabled != next.ProbeCore.Enabled ||
		prev.ProbeCore.BinaryPath != next.ProbeCore.BinaryPath ||
		!equalStringSlices(prev.ProbeCore.Collectors, next.ProbeCore.Collectors) ||
		!equalStringSlices(prev.ProbeCore.Args, next.ProbeCore.Args) ||
		prev.ProbeCore.Compression != next.ProbeCore.Compression ||
		prev.ProbeCore.QueueDepth != next.ProbeCore.QueueDepth ||
		prev.ProbeCore.WindowSamples != next.ProbeCore.WindowSamples ||
		prev.ProbeCore.ProcessIntervalSamples != next.ProbeCore.ProcessIntervalSamples ||
		prev.ProbeCore.HostProcFallbackIntervalSamples != next.ProbeCore.HostProcFallbackIntervalSamples ||
		prev.ProbeCore.PressureIntervalSamples != next.ProbeCore.PressureIntervalSamples ||
		prev.ProbeCore.NetlinkIntervalSamples != next.ProbeCore.NetlinkIntervalSamples ||
		prev.ProbeCore.GPUIntervalSamples != next.ProbeCore.GPUIntervalSamples ||
		prev.ProbeCore.Nice != next.ProbeCore.Nice ||
		prev.ProbeCore.StaleAfter != next.ProbeCore.StaleAfter {
		c.logger.Warn("probe_core settings changed but will apply after restart")
	}
	return nil
}

// Run starts the collector loop.
// Think of this loop like a metronome that can speed up or slow down depending on
// system stress: stable systems keep a steady beat, stressed systems back off.
func (c *Collector) Run(ctx context.Context) error {
	cfg := c.configSnapshot()
	applyCurrentProcessNice(cfg.Protection.Nice, c.logger)
	if stopEBPF := c.startPrimaryEBPFRuntime(); stopEBPF != nil {
		defer stopEBPF()
	}
	if c.sourcePipeline != nil {
		if err := c.sourcePipeline.Start(ctx, c.configSnapshot()); err != nil {
			return err
		}
		defer c.sourcePipeline.Stop()
	}
	if c.shm != nil {
		defer c.shm.Close()
	}
	if c.transport != nil {
		defer c.transport.Close()
	}
	if c.spool != nil {
		defer c.spool.Close()
	}

	interval := c.intervalSnapshot()
	timer := time.NewTimer(interval)
	defer timer.Stop()

	hardwareProfile := defaultHardwareProfile(time.Now())
	if c.hardware != nil {
		hardwareProfile = c.hardware.RefreshIfNeeded(time.Now(), cfg.Hardware)
	}
	c.logger.Info("collector started",
		zap.String("collector_id", cfg.CollectorID),
		zap.Strings("controllers", cfg.ControllerEndpoints),
		zap.Duration("interval", cfg.CollectionInterval),
		zap.String("runtime_mode_requested", c.runtimeMode.RequestedMode),
		zap.String("runtime_mode_applied", c.runtimeMode.AppliedMode),
		zap.Bool("runtime_containerized", c.runtimeMode.Containerized),
		zap.Bool("runtime_ebpf_capable", c.runtimeMode.CanUseEBPF),
		zap.Int("level", c.level),
		zap.Bool("probe_core_primary_enabled", cfg.ProbeCore.Enabled),
		zap.Bool("compatibility_fallback_enabled", cfg.ProbeCore.FallbackToGo),
		zap.String("hardware_cpu_vendor", hardwareProfile.CPU.Vendor),
		zap.String("hardware_storage_class", hardwareProfile.Storage.DominantClass),
		zap.String("hardware_network_type", hardwareProfile.Network.DominantType),
		zap.String("hardware_gpu_vendor", hardwareProfile.GPU.Vendor),
	)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			snapshot, err := c.collectAndSend(ctx)
			if err != nil {
				c.promMetrics.reportFailures.WithLabelValues(classifyError(err)).Inc()
				c.logger.Warn("collect/send failed", zap.Error(err))
			}
			interval = c.nextInterval(snapshot, err)
			timer.Reset(interval)
		}
	}
}

func (c *Collector) collectAndSend(ctx context.Context) (cycleSnapshot, error) {
	cfg := c.configSnapshot()
	ctx, span := observability.StartSpan(ctx, "collector.collect_send",
		attribute.String("collector.id", cfg.CollectorID),
		attribute.Int("collector.level", c.level),
	)
	defer span.End()

	start := time.Now()
	c.promMetrics.reportAttempts.Inc()
	batch, snapshot, err := c.collectBatch(ctx)
	if err != nil {
		observability.RecordError(ctx, err)
		return cycleSnapshot{}, err
	}

	payload, release, err := c.marshalBatch(batch)
	if err != nil {
		observability.RecordError(ctx, err)
		return cycleSnapshot{}, fmt.Errorf("marshal telemetry batch: %w", err)
	}
	defer release()

	if err := c.spool.Enqueue(payload); err != nil {
		observability.RecordError(ctx, err)
		return snapshot, fmt.Errorf("enqueue telemetry batch: %w", err)
	}
	c.promMetrics.batchesEnqueued.Inc()

	if err := c.transport.DrainWithOptions(ctx, c.spool, func(bytes []byte) (string, error) {
		ack, sendErr := c.transport.Send(ctx, bytes)
		if sendErr != nil {
			return "", sendErr
		}
		if ack == nil || strings.TrimSpace(ack.BatchId) == "" {
			return "", fmt.Errorf("empty ack batch id from controller")
		}
		return ack.BatchId, nil
	}, transport.DrainOptions{
		MaxRecords:  snapshot.drainMaxRecords,
		MaxDuration: snapshot.drainMaxDuration,
	}); err != nil {
		observability.RecordError(ctx, err)
		return snapshot, fmt.Errorf("drain transport spool: %w", err)
	}

	duration := time.Since(start).Seconds()
	c.promMetrics.batchesSent.Inc()
	c.promMetrics.collectionDuration.Observe(duration)
	return snapshot, nil
}

func (c *Collector) collectBatch(ctx context.Context) (*telemetryv1.TelemetryBatch, cycleSnapshot, error) {
	now := time.Now()
	cfg := c.configSnapshot()
	metrics := make([]*telemetryv1.Metric, 0, 256)
	snapshot := cycleSnapshot{probeSource: "go"}
	hardwareProfile := defaultHardwareProfile(now)
	if c.hardware != nil {
		hardwareProfile = c.hardware.RefreshIfNeeded(now, cfg.Hardware)
	}

	probeData, err := c.collectProbeData(now)
	if err != nil {
		c.promMetrics.collectionErrors.WithLabelValues("probe").Inc()
		c.logger.Warn("probe metric collection failed", zap.Error(err))
	} else {
		c.promMetrics.collectionSuccess.WithLabelValues("probe").Inc()
	}
	snapshot.probeSource = probeData.source
	metrics = append(metrics, filterMetricsByPrefix(probeData.metrics, "node_security_")...)
	if ebpfMetrics := c.collectPrimaryEBPFMetrics(now, probeData.metrics, probeData.compatibilityFallback); len(ebpfMetrics) > 0 {
		c.promMetrics.collectionSuccess.WithLabelValues("ebpf").Inc()
		metrics = append(metrics, ebpfMetrics...)
	}
	snapshot.cpuPercent = metricValueAny(metrics, "node_cpu_usage_percent", "probe_core_cpu_usage_percent")
	selfSample := collectorSelfSample{}
	if c.protection != nil {
		selfSample = c.protection.Sample(now)
	}
	spoolSnapshot := c.spool.Snapshot()
	decision := protectionDecision{
		Mode:             protectionModeNormal,
		MaxDrainRecords:  maxPositiveInt(cfg.Protection.MaxDrainRecordsPerCycle, 1),
		MaxDrainDuration: cfg.Protection.MaxDrainDuration,
	}
	if c.protection != nil {
		decision = c.protection.Decide(
			cfg.Protection,
			transportSnapshotFromClient(c.transport),
			spoolSnapshot,
			metrics,
			hardwareProfile,
			selfSample,
		)
	}
	snapshot.spoolBacklog = spoolSnapshot.BacklogBytes
	snapshot.signalPressure = decision.SignalPressure
	snapshot.selfCPUPercent = selfSample.CPUPercent
	snapshot.selfRSSBytes = selfSample.RSSBytes
	snapshot.protectionMode = decision.Mode
	snapshot.drainMaxRecords = decision.MaxDrainRecords
	snapshot.drainMaxDuration = decision.MaxDrainDuration

	processes, auxProcessMetrics := c.collectProcessFallbackWithCadence(now, cfg, decision, probeData)
	if len(processes) == 0 && len(probeData.processes) > 0 {
		processes = probeData.processes
	}
	profileStatus := c.runtimeProfileStatusSnapshot()
	processes = applyRuntimeProfileToProcesses(processes, profileStatus)
	metrics = append(metrics, auxProcessMetrics...)
	logs, auxLogMetrics := c.collectLogsWithCadence(now, cfg, decision)
	logs = applyRuntimeProfileToLogs(logs, profileStatus)
	metrics = append(metrics, auxLogMetrics...)

	if c.securityAuditor != nil && !decision.DisableSecurity {
		ebpfSummary := probe.EBPFSummary{}
		var ebpfEvents []probe.EBPFEvent
		if c.ebpfRuntime != nil {
			ebpfSummary = c.ebpfRuntime.Summary()
			ebpfEvents = c.ebpfRuntime.Events(cfg.Security.RecentEventLimit)
		}
		metrics = append(metrics, c.securityAuditor.Collect(now, collectorSecurityInput{
			Source:             probeData.source,
			Processes:          processes,
			AllowedListenPorts: append([]int(nil), cfg.EBPF.AllowedListenPorts...),
			EBPFSummary:        ebpfSummary,
			EBPFEvents:         ebpfEvents,
		})...)
	}

	c.appendSHMMetrics(now, &metrics)
	c.appendSpoolMetrics(now, spoolSnapshot, &metrics)
	c.appendTransportMetrics(now, &metrics)
	c.appendProbeSourceMetrics(now, probeData.source, &metrics)
	c.appendRuntimeModeMetrics(now, &metrics)
	c.appendEBPFRuntimeMetrics(now, &metrics)
	c.appendSourcePipelineMetrics(now, probeData, &metrics)
	c.appendProbeCoreRuntimeMetrics(now, probeData.source, c.configSnapshot().ProbeCore, &metrics)
	appendHardwareMetrics(now, hardwareProfile, &metrics)
	appendProtectionMetrics(now, decision, selfSample, &metrics)
	appendHardwareWarningMetrics(now, &metrics, metrics, hardwareProfile)

	metrics = append(metrics, c.collectExternalMetricsWithCadence(ctx, now, cfg, decision)...)
	if decision.DisableLogs {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_logs_collection_shed",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
		})
	}
	if decision.DisableSecurity {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_security_collection_shed",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
		})
	}
	if decision.DisableExternal {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_external_collection_shed",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
		})
	}
	if decision.SkipProcessFallback && len(processes) == 0 && probeData.compatibilityFallback {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              "collector_process_fallback_shed",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
		})
	}
	processPayload, processPayloadMetrics := c.suppressUnchangedProcessPayload(now, processes)
	metrics = append(metrics, processPayloadMetrics...)
	sanitizeTelemetryMetrics(metrics)
	metrics = c.suppressUnchangedLowChurnMetrics(now, metrics)

	c.mu.Lock()
	c.maybeExpireRuntimeProfileLocked(now)
	c.batchSeq++
	batchID := fmt.Sprintf("%s-%d", c.cfg.CollectorID, c.batchSeq)
	info := cloneCollectorInfo(c.info)
	c.mu.Unlock()
	appendCollectorInfoRuntimeProfileLabels(info, profileStatus)

	batch := &telemetryv1.TelemetryBatch{
		Collector:             info,
		WallTimeUnixNano:      now.UnixNano(),
		MonotonicTimeUnixNano: now.UnixNano(),
		Metrics:               metrics,
		Processes:             processPayload,
		Logs:                  logs,
		BatchId:               batchID,
	}
	return batch, snapshot, nil
}

func convertProbeMetricBatch(metricBatch *probe.MetricBatch) []*telemetryv1.Metric {
	if metricBatch == nil || len(metricBatch.Metrics) == 0 {
		return nil
	}
	return convertProbeMetrics(metricBatch.Metrics)
}

func convertProbeMetrics(metricBatch []probe.Metric) []*telemetryv1.Metric {
	if len(metricBatch) == 0 {
		return nil
	}
	metrics := make([]*telemetryv1.Metric, 0, len(metricBatch))
	for _, metric := range metricBatch {
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              metric.Name,
			Value:             metric.Value,
			TimestampUnixNano: metric.Timestamp.UnixNano(),
			Labels:            buildLabels(metric.Labels),
		})
	}
	return metrics
}

func (c *Collector) collectPrimaryEBPFMetrics(now time.Time, existing []*telemetryv1.Metric, compatibilityFallback bool) []*telemetryv1.Metric {
	if c.ebpfRuntime == nil || compatibilityFallback {
		return nil
	}
	if hasMetricPrefix(existing, "node_ebpf_") {
		return nil
	}
	return convertProbeMetrics(c.ebpfRuntime.GetMetrics(now))
}

func (c *Collector) startPrimaryEBPFRuntime() func() {
	if c == nil {
		return nil
	}

	expected := c.configSnapshot().EBPF.Enabled
	if c.ebpfRuntime == nil {
		reason := ""
		if expected {
			reason = "unavailable"
		} else {
			reason = "disabled"
		}
		c.setEBPFRuntimeStatus(expected, false, reason)
		return nil
	}
	if expected && !c.runtimeMode.CanUseEBPF {
		c.logger.Info("primary eBPF runtime skipped due to runtime capability constraints",
			zap.Bool("containerized", c.runtimeMode.Containerized),
			zap.Bool("host_pid_namespace", c.runtimeMode.HostPIDNamespace),
			zap.Bool("cap_bpf", c.runtimeMode.CAPBPF),
			zap.Bool("cap_sys_admin", c.runtimeMode.CAPSysAdmin),
			zap.Bool("bpf_fs_visible", c.runtimeMode.BPFFSVisible),
		)
		c.ebpfRuntime = nil
		c.setEBPFRuntimeStatus(expected, false, "capability_unavailable")
		return nil
	}

	if err := c.ebpfRuntime.Start(); err != nil {
		c.logger.Warn("primary eBPF runtime unavailable; continuing with degraded collector path",
			zap.Error(err),
			zap.Bool("ebpf_expected", expected),
		)
		c.ebpfRuntime = nil
		c.setEBPFRuntimeStatus(expected, false, "start_failed")
		return nil
	}

	c.setEBPFRuntimeStatus(expected, true, "")
	return c.ebpfRuntime.Stop
}

func (c *Collector) setEBPFRuntimeStatus(expected, healthy bool, reason string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.ebpfExpected = expected
	c.ebpfHealthy = healthy
	c.ebpfReason = strings.TrimSpace(reason)
	c.mu.Unlock()
}

func (c *Collector) ebpfRuntimeStatus() (bool, bool, string) {
	if c == nil {
		return false, false, ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ebpfExpected, c.ebpfHealthy, c.ebpfReason
}

func (c *Collector) collectProbeData(now time.Time) (sourceCollection, error) {
	if c.sourcePipeline == nil {
		return sourceCollection{source: "unknown"}, nil
	}
	return c.sourcePipeline.Collect(now, c.configSnapshot())
}

func (c *Collector) appendSHMMetrics(now time.Time, metrics *[]*telemetryv1.Metric) {
	if c.shm == nil {
		return
	}

	*metrics = append(*metrics, c.shm.Collect(now)...)
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_shm_metrics_read",
		Value:             float64(c.shm.LastReadCount()),
		TimestampUnixNano: now.UnixNano(),
		Labels:            buildLabels(map[string]string{"source": "shm"}),
	})
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_shm_read_errors",
		Value:             float64(c.shm.LastErrorCount()),
		TimestampUnixNano: now.UnixNano(),
		Labels:            buildLabels(map[string]string{"source": "shm"}),
	})
	if capacity := c.shm.Capacity(); capacity > 0 {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_shm_buffer_capacity_bytes",
			Value:             float64(capacity),
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"source": "shm"}),
		})
	}
}

func (c *Collector) appendSpoolMetrics(now time.Time, snapshot spool.Snapshot, metrics *[]*telemetryv1.Metric) int64 {
	backlog := snapshot.BacklogBytes
	size := snapshot.FileSizeBytes
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_spool_backlog_bytes",
		Value:             float64(backlog),
		TimestampUnixNano: now.UnixNano(),
	})
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_spool_size_bytes",
		Value:             float64(size),
		TimestampUnixNano: now.UnixNano(),
	})
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_spool_evicted_records_total",
		Value:             float64(snapshot.EvictedRecords),
		TimestampUnixNano: now.UnixNano(),
	})
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_spool_corruption_recoveries_total",
		Value:             float64(snapshot.CorruptionRecoveries),
		TimestampUnixNano: now.UnixNano(),
	})
	if strings.TrimSpace(snapshot.LastRecoveryReason) != "" {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_spool_last_recovery_reason",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"reason": sanitizeLabelToken(snapshot.LastRecoveryReason, maxLabelValueRunes)}),
		})
	}
	return backlog
}

func (c *Collector) appendTransportMetrics(now time.Time, metrics *[]*telemetryv1.Metric) {
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_transport_send_ms",
		Value:             c.transport.LastSendMs(),
		TimestampUnixNano: now.UnixNano(),
	})
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_transport_ack_ms",
		Value:             c.transport.LastAckMs(),
		TimestampUnixNano: now.UnixNano(),
	})
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_transport_errors_total",
		Value:             float64(c.transport.LastErrs()),
		TimestampUnixNano: now.UnixNano(),
	})
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_transport_retries_total",
		Value:             float64(c.transport.LastRetries()),
		TimestampUnixNano: now.UnixNano(),
	})
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_transport_compressed",
		Value:             boolToFloat(c.transport.LastCompressed()),
		TimestampUnixNano: now.UnixNano(),
	})
	if endpoint := strings.TrimSpace(c.transport.LastEndpoint()); endpoint != "" {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_transport_last_endpoint",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"endpoint": sanitizeLabelToken(endpoint, maxLabelValueRunes)}),
		})
	}
	if kind := strings.TrimSpace(c.transport.LastErrorKind()); kind != "" {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_transport_last_error_kind",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"kind": sanitizeLabelToken(kind, maxLabelValueRunes)}),
		})
	}
}

func (c *Collector) appendProbeSourceMetrics(now time.Time, source string, metrics *[]*telemetryv1.Metric) {
	if strings.TrimSpace(source) == "" {
		source = "unknown"
	}
	*metrics = append(*metrics, &telemetryv1.Metric{
		Name:              "collector_probe_source",
		Value:             1,
		TimestampUnixNano: now.UnixNano(),
		Labels:            buildLabels(map[string]string{"source": source}),
	})
}

func (c *Collector) appendRuntimeModeMetrics(now time.Time, metrics *[]*telemetryv1.Metric) {
	if c == nil {
		return
	}
	runtimeMode := c.runtimeMode
	*metrics = append(*metrics,
		&telemetryv1.Metric{
			Name:              "collector_privilege_profile",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"profile": c.cfg.PrivilegeProfile}),
		},
		&telemetryv1.Metric{
			Name:              "collector_runtime_mode",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"mode": runtimeMode.AppliedMode}),
		},
		&telemetryv1.Metric{
			Name:              "collector_runtime_mode_requested",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"mode": runtimeMode.RequestedMode}),
		},
		&telemetryv1.Metric{
			Name:              "collector_runtime_mode_degraded",
			Value:             boolToFloat(runtimeMode.Degraded),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_runtime_containerized",
			Value:             boolToFloat(runtimeMode.Containerized),
			TimestampUnixNano: now.UnixNano(),
		},
	)
	for capability, available := range map[string]bool{
		"bpf_capability":        runtimeMode.CAPBPF || runtimeMode.CAPSysAdmin,
		"perfmon_capability":    runtimeMode.CAPPerfmon || runtimeMode.CAPSysAdmin,
		"net_admin_capability":  runtimeMode.CAPNetAdmin || runtimeMode.CAPSysAdmin,
		"bpf_filesystem":        runtimeMode.BPFFSVisible,
		"host_pid_namespace":    runtimeMode.HostPIDNamespace,
		"kernel_mounts":         runtimeMode.KernelMountsVisible,
		"proc_visibility":       runtimeMode.ProcVisible,
		"cgroup_visibility":     runtimeMode.CgroupVisible,
		"containerized_runtime": runtimeMode.Containerized,
	} {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_runtime_capability_available",
			Value:             boolToFloat(available),
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"capability": capability}),
		})
	}
	for signal, covered := range map[string]bool{
		"ebpf":                runtimeMode.CanUseEBPF,
		"perf_events":         runtimeMode.CanUsePerfEvents,
		"taskstats":           runtimeMode.CanUseTaskstats,
		"host_processes":      runtimeMode.HostPIDNamespace,
		"namespace_processes": runtimeMode.ProcVisible,
		"cgroup":              runtimeMode.CgroupVisible,
		"kernel_interfaces":   runtimeMode.KernelMountsVisible,
	} {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_runtime_signal_coverage",
			Value:             boolToFloat(covered),
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"signal": signal}),
		})
	}
	for _, reason := range runtimeMode.Reasons {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_runtime_degraded_reason",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"reason": sanitizeLabelToken(reason, maxLabelValueRunes)}),
		})
	}
	for feature, enabled := range collectorProfileFeatures(c.cfg) {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_privilege_profile_feature_enabled",
			Value:             boolToFloat(enabled),
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"feature": feature}),
		})
	}
}

func (c *Collector) appendEBPFRuntimeMetrics(now time.Time, metrics *[]*telemetryv1.Metric) {
	expected, healthy, reason := c.ebpfRuntimeStatus()
	*metrics = append(*metrics,
		&telemetryv1.Metric{
			Name:              "collector_primary_ebpf_expected",
			Value:             boolToFloat(expected),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_primary_ebpf_healthy",
			Value:             boolToFloat(healthy),
			TimestampUnixNano: now.UnixNano(),
		},
	)
	if strings.TrimSpace(reason) != "" {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_primary_ebpf_reason",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"reason": sanitizeLabelToken(reason, maxLabelValueRunes)}),
		})
	}
}

func (c *Collector) appendSourcePipelineMetrics(now time.Time, data sourceCollection, metrics *[]*telemetryv1.Metric) {
	*metrics = append(*metrics,
		&telemetryv1.Metric{
			Name:              "collector_primary_probe_core_expected",
			Value:             boolToFloat(data.primaryExpected),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_primary_probe_core_healthy",
			Value:             boolToFloat(data.primaryHealthy),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_compatibility_fallback_active",
			Value:             boolToFloat(data.compatibilityFallback),
			TimestampUnixNano: now.UnixNano(),
		},
	)
	if strings.TrimSpace(data.fallbackReason) != "" {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_compatibility_fallback_reason",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"reason": sanitizeLabelToken(data.fallbackReason, maxLabelValueRunes)}),
		})
	}
}

func (c *Collector) appendProbeCoreRuntimeMetrics(now time.Time, probeSource string, probeCfg ProbeCoreConfig, metrics *[]*telemetryv1.Metric) {
	if !probeCfg.Enabled && c.probeCore == nil {
		return
	}
	requestedModules, selectionValid := resolveProbeCoreModuleSelection(probeCfg)
	probeCoreActive := strings.EqualFold(strings.TrimSpace(probeSource), "probe_core")
	*metrics = append(*metrics,
		&telemetryv1.Metric{
			Name:              "collector_probe_core_client_available",
			Value:             boolToFloat(c.probeCore != nil),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_probe_core_active",
			Value:             boolToFloat(probeCoreActive),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_probe_core_collector_selection_valid",
			Value:             boolToFloat(selectionValid),
			TimestampUnixNano: now.UnixNano(),
		},
	)
	for _, module := range probeCoreCollectorModuleOrder {
		_, requested := requestedModules[module]
		labels := buildLabels(map[string]string{"module": module})
		*metrics = append(*metrics,
			&telemetryv1.Metric{
				Name:              "collector_probe_core_collector_module_requested",
				Value:             boolToFloat(requested),
				TimestampUnixNano: now.UnixNano(),
				Labels:            labels,
			},
			&telemetryv1.Metric{
				Name:              "collector_probe_core_collector_module_active",
				Value:             boolToFloat(requested && probeCoreActive),
				TimestampUnixNano: now.UnixNano(),
				Labels:            labels,
			},
		)
	}
	if c.probeCore == nil {
		return
	}
	stats := c.probeCore.Stats()
	fresh := 0.0
	if !stats.LastReceivedAt.IsZero() && stats.LastReceivedAge <= probeCfg.StaleAfter {
		fresh = 1.0
	}
	*metrics = append(*metrics,
		&telemetryv1.Metric{
			Name:              "collector_probe_core_frames_received_total",
			Value:             float64(stats.FramesReceived),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_probe_core_decode_errors_total",
			Value:             float64(stats.DecodeErrors),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_probe_core_crc_failures_total",
			Value:             float64(stats.CRCFailures),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_probe_core_restarts_total",
			Value:             float64(stats.Restarts),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_probe_core_last_sequence",
			Value:             float64(stats.LastSequence),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_probe_core_last_frame_age_seconds",
			Value:             stats.LastReceivedAge.Seconds(),
			TimestampUnixNano: now.UnixNano(),
		},
		&telemetryv1.Metric{
			Name:              "collector_probe_core_fresh",
			Value:             fresh,
			TimestampUnixNano: now.UnixNano(),
		},
	)
	if strings.TrimSpace(stats.LastError) != "" {
		*metrics = append(*metrics, &telemetryv1.Metric{
			Name:              "collector_probe_core_last_error",
			Value:             1,
			TimestampUnixNano: now.UnixNano(),
			Labels:            buildLabels(map[string]string{"error": truncateOutput(stats.LastError, maxLabelValueRunes)}),
		})
	}
}

func hasMetricPrefix(metrics []*telemetryv1.Metric, prefix string) bool {
	if strings.TrimSpace(prefix) == "" {
		return false
	}
	for _, metric := range metrics {
		if strings.HasPrefix(metric.GetName(), prefix) {
			return true
		}
	}
	return false
}

func collectorProfileFeatures(cfg Config) map[string]bool {
	return map[string]bool{
		"logs":             len(cfg.LogPaths) > 0,
		"ebpf":             cfg.EBPF.Enabled,
		"probe_core":       cfg.ProbeCore.Enabled,
		"runtime_security": cfg.Security.Enabled,
		"gpu_evidence":     cfg.PrivilegeProfile == PrivilegeProfileGPU || cfg.PrivilegeProfile == PrivilegeProfileDeepRuntime,
	}
}

func filterMetricsByPrefix(metrics []*telemetryv1.Metric, prefix string) []*telemetryv1.Metric {
	if strings.TrimSpace(prefix) == "" || len(metrics) == 0 {
		return metrics
	}
	out := make([]*telemetryv1.Metric, 0, len(metrics))
	for _, metric := range metrics {
		if metric == nil || strings.HasPrefix(metric.GetName(), prefix) {
			continue
		}
		out = append(out, metric)
	}
	return out
}

func resolveProbeCoreModuleSelection(cfg ProbeCoreConfig) (map[string]struct{}, bool) {
	if modules, allSelected, hasSelection := parseProbeCoreModuleList(cfg.Collectors); hasSelection {
		if allSelected {
			return allProbeCoreCollectorModules(), true
		}
		if len(modules) == 0 {
			return nil, false
		}
		return modules, true
	}
	if rawModules, ok := probeCoreModulesFromArgs(cfg.Args); ok {
		modules, allSelected, _ := parseProbeCoreModuleList(rawModules)
		if allSelected {
			return allProbeCoreCollectorModules(), true
		}
		if len(modules) == 0 {
			return nil, false
		}
		return modules, true
	}
	return allProbeCoreCollectorModules(), true
}

func parseProbeCoreModuleList(raw []string) (map[string]struct{}, bool, bool) {
	if len(raw) == 0 {
		return nil, false, false
	}
	modules := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		module := strings.TrimSpace(strings.ToLower(item))
		if module == "" {
			continue
		}
		if module == "all" {
			return nil, true, true
		}
		if !isValidProbeCoreCollectorModule(module) {
			continue
		}
		modules[module] = struct{}{}
	}
	if _, ok := modules["process"]; ok {
		modules["host"] = struct{}{}
	}
	return modules, false, true
}

func probeCoreModulesFromArgs(args []string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		raw := strings.TrimSpace(args[i])
		if raw == "" {
			continue
		}
		normalized := strings.ToLower(raw)
		if normalized == "--collectors" {
			if i+1 >= len(args) {
				return nil, true
			}
			return splitCSV(args[i+1]), true
		}
		if !strings.HasPrefix(normalized, "--collectors=") {
			continue
		}
		idx := strings.Index(raw, "=")
		if idx < 0 {
			return nil, true
		}
		return splitCSV(raw[idx+1:]), true
	}
	return nil, false
}

func allProbeCoreCollectorModules() map[string]struct{} {
	out := make(map[string]struct{}, len(probeCoreCollectorModuleOrder))
	for _, module := range probeCoreCollectorModuleOrder {
		out[module] = struct{}{}
	}
	return out
}

func (c *Collector) marshalBatch(batch *telemetryv1.TelemetryBatch) ([]byte, func(), error) {
	bufPtr := c.marshalPool.Get().(*[]byte)
	encoded, err := proto.MarshalOptions{}.MarshalAppend((*bufPtr)[:0], batch)
	if err != nil {
		c.marshalPool.Put(bufPtr)
		return nil, nil, err
	}

	release := func() {
		if cap(encoded) > maxMarshalBufferCap {
			*bufPtr = make([]byte, 0, defaultMarshalBufferCap)
		} else {
			*bufPtr = encoded[:0]
		}
		c.marshalPool.Put(bufPtr)
	}
	return encoded, release, nil
}

func (c *Collector) nextInterval(snapshot cycleSnapshot, cycleErr error) time.Duration {
	cfg := c.configSnapshot()
	if !cfg.AdaptivePolling {
		return clampDuration(cfg.CollectionInterval, cfg.MinCollectionInterval, cfg.MaxCollectionInterval)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	interval := c.currentInterval
	if interval <= 0 {
		interval = cfg.CollectionInterval
	}

	highSelfCPU := cfg.Protection.MaxCPUPercent > 0 && snapshot.selfCPUPercent >= cfg.Protection.MaxCPUPercent
	highCPU := snapshot.cpuPercent >= 85 || highSelfCPU
	highBacklog := cfg.SpoolMaxBytes > 0 && snapshot.spoolBacklog > cfg.SpoolMaxBytes/2
	emergingIncident := snapshot.signalPressure >= 2 && snapshot.cpuPercent < 80 && snapshot.spoolBacklog == 0
	mode := snapshot.protectionMode
	if mode == protectionModeNormal {
		switch {
		case highCPU || highBacklog:
			mode = protectionModePressure
		case emergingIncident:
			mode = protectionModeIncident
		}
	}
	switch {
	case cycleErr != nil:
		c.failureStreak++
		if isTransientCollectorFailure(cycleErr) {
			interval = time.Duration(float64(cfg.CollectionInterval) * math.Pow(1.5, float64(c.failureStreak)))
			interval = c.withIntervalJitter(interval, 0.1)
		} else {
			interval = interval + interval/2
		}
	case mode == protectionModeCritical:
		c.failureStreak = 0
		interval = interval + interval
		if minimum := cfg.CollectionInterval * 2; interval < minimum {
			interval = minimum
		}
	case mode == protectionModePressure:
		c.failureStreak = 0
		interval = interval + interval/2
	case mode == protectionModeIncident:
		c.failureStreak = 0
		interval = interval - interval/4
	default:
		c.failureStreak = 0
		interval = cfg.CollectionInterval
	}

	interval = clampDuration(interval, cfg.MinCollectionInterval, cfg.MaxCollectionInterval)
	c.currentInterval = interval
	c.promMetrics.currentPollInterval.Set(interval.Seconds())
	c.promMetrics.retryBackoff.Set(interval.Seconds())
	c.promMetrics.failureStreak.Set(float64(c.failureStreak))
	return interval
}

func (c *Collector) withIntervalJitter(interval time.Duration, ratio float64) time.Duration {
	if interval <= 0 || ratio <= 0 {
		return interval
	}
	unit := 0.5
	if c != nil && c.jitterUnit != nil {
		unit = c.jitterUnit()
	} else {
		unit = rand.Float64()
	}
	if unit < 0 {
		unit = 0
	}
	if unit > 1 {
		unit = 1
	}
	factor := 1 + ((unit*2)-1)*ratio
	if factor < 0.1 {
		factor = 0.1
	}
	return time.Duration(float64(interval) * factor)
}

func isTransientCollectorFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if transport.IsPermanentPayloadError(err) {
		return false
	}

	var transportErr *transport.Error
	if errors.As(err, &transportErr) {
		switch transportErr.Kind {
		case transport.ErrorKindDial,
			transport.ErrorKindStream,
			transport.ErrorKindSend,
			transport.ErrorKindReceive,
			transport.ErrorKindRetryExhaust,
			transport.ErrorKindTLS:
			return true
		}
	}
	return false
}

func samplingSignalPressure(metrics []*telemetryv1.Metric) int {
	return detectHardwareAnomalies(metrics, defaultHardwareProfile(time.Time{})).PressureCount()
}

func memoryPercentFromMetrics(metrics []*telemetryv1.Metric) float64 {
	used := metricValueAny(metrics, "node_memory_Used_bytes", "node_memory_used_bytes")
	total := metricValueAny(metrics, "node_memory_MemTotal_bytes", "node_memory_total_bytes")
	if total <= 0 {
		return 0
	}
	return clampPercent(used / total * 100)
}

func (c *Collector) configSnapshot() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maybeExpireRuntimeProfileLocked(time.Now().UTC())
	cfg := c.cfg
	return applyRuntimeProfileToConfig(cfg, c.runtimeProfileStatusSnapshot())
}

func (c *Collector) intervalSnapshot() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maybeExpireRuntimeProfileLocked(time.Now().UTC())
	return c.currentInterval
}

// runExternalMetrics executes an optional external command (configured) to collect extra metrics.
// The command must output JSON: {"metrics":[{"name":"foo","value":1.23,"labels":{"k":"v"}}]}
func (c *Collector) runExternalMetrics(ctx context.Context) []*telemetryv1.Metric {
	cfg := c.configSnapshot()
	if strings.TrimSpace(cfg.ExternalMetricsCmd) == "" {
		return nil
	}

	fetcher := c.externalFetch
	if fetcher == nil {
		fetcher = c.fetchExternalMetricPayload
	}
	payload, err := fetcher(ctx, cfg.ExternalMetricsCmd, cfg.ExternalMetricsTimeout)
	if err != nil {
		c.promMetrics.collectionErrors.WithLabelValues("external").Inc()
		c.logger.Debug("external metrics collection failed", zap.Error(err))
		return nil
	}

	metrics, dropped := convertExternalMetrics(payload.Metrics, time.Now().UnixNano())
	if len(metrics) == 0 {
		if dropped > 0 {
			c.logger.Warn("all external metrics dropped after validation",
				zap.Int("dropped", dropped),
			)
		}
		return nil
	}
	c.promMetrics.collectionSuccess.WithLabelValues("external").Inc()
	if dropped > 0 {
		c.logger.Warn("external metrics dropped after validation",
			zap.Int("accepted", len(metrics)),
			zap.Int("dropped", dropped),
		)
	}
	return metrics
}

func (c *Collector) fetchExternalMetricPayload(ctx context.Context, command string, timeout time.Duration) (extMetricPayload, error) {
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	cmdName, args, err := parseExternalMetricCommand(command)
	if err != nil {
		return extMetricPayload{}, err
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, cmdName, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return extMetricPayload{}, fmt.Errorf("run command: %w (output=%q)", err, truncateOutput(strings.TrimSpace(string(out)), 256))
	}
	if len(out) > maxExternalOutputBytes {
		return extMetricPayload{}, fmt.Errorf("command output too large: %d bytes exceeds %d", len(out), maxExternalOutputBytes)
	}

	var payload extMetricPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		return extMetricPayload{}, fmt.Errorf("decode command output: %w", err)
	}
	return payload, nil
}

func parseExternalMetricCommand(raw string) (string, []string, error) {
	command := strings.TrimSpace(raw)
	if command == "" {
		return "", nil, fmt.Errorf("%w: empty", errExternalMetricCmd)
	}

	// External metrics command execution is intentionally non-shell to avoid
	// command chaining/injection risks from config or env payloads.
	if strings.ContainsAny(command, "|;&><`()$\\\n\r") {
		return "", nil, fmt.Errorf("%w: shell control operators are not allowed", errExternalMetricCmd)
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("%w: empty argv", errExternalMetricCmd)
	}
	return parts[0], parts[1:], nil
}

func convertExternalMetrics(input []extMetric, timestampUnixNano int64) ([]*telemetryv1.Metric, int) {
	if len(input) == 0 {
		return nil, 0
	}

	dropped := 0
	if len(input) > maxExternalMetrics {
		dropped = len(input) - maxExternalMetrics
		input = input[:maxExternalMetrics]
	}

	metrics := make([]*telemetryv1.Metric, 0, len(input))
	for _, metric := range input {
		name, err := normalizeExternalMetricName(metric.Name)
		if err != nil {
			dropped++
			continue
		}
		if math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
			dropped++
			continue
		}
		metrics = append(metrics, &telemetryv1.Metric{
			Name:              name,
			Value:             metric.Value,
			TimestampUnixNano: timestampUnixNano,
			Labels:            buildLabels(normalizeExternalMetricLabels(metric.Labels)),
		})
	}
	return metrics, dropped
}

func normalizeExternalMetricName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errExternalMetricName
	}
	if utf8.RuneCountInString(name) > maxMetricNameRunes {
		return "", fmt.Errorf("%w: too long", errExternalMetricName)
	}
	for idx, r := range name {
		if !isMetricNameRune(r, idx == 0) {
			return "", fmt.Errorf("%w: invalid rune %q", errExternalMetricName, r)
		}
	}
	return name, nil
}

func isMetricNameRune(r rune, first bool) bool {
	if first {
		return r == '_' || r == ':' || unicode.IsLetter(r)
	}
	return r == '_' || r == ':' || r == '.' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func normalizeExternalMetricLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		normalizedKey, err := normalizeExternalMetricName(key)
		if err != nil {
			continue
		}
		if utf8.RuneCountInString(normalizedKey) > maxLabelKeyRunes || utf8.RuneCountInString(value) > maxLabelValueRunes {
			continue
		}
		out[normalizedKey] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func truncateOutput(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "..."
}

func classifyError(err error) string {
	var typed *transport.Error
	if errors.As(err, &typed) {
		return string(typed.Kind)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "context"
	}
	return "unknown"
}

func clampDuration(value, minValue, maxValue time.Duration) time.Duration {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func metricValue(metrics []*telemetryv1.Metric, name string) float64 {
	for _, metric := range metrics {
		if metric.Name == name {
			return metric.Value
		}
	}
	return 0
}

func buildLabels(labels map[string]string) []*telemetryv1.Label {
	if len(labels) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(labels))
	for rawKey, rawValue := range labels {
		key := sanitizeLabelToken(rawKey, maxLabelKeyRunes)
		if key == "" {
			continue
		}
		value := sanitizeLabelToken(rawValue, maxLabelValueRunes)
		normalized[key] = value
	}
	keys := make([]string, 0, len(normalized))
	for key := range normalized {
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys)

	out := make([]*telemetryv1.Label, 0, len(keys))
	for _, key := range keys {
		value := normalized[key]
		out = append(out, &telemetryv1.Label{Key: key, Value: value})
	}
	return out
}

// sanitizeTelemetryMetrics enforces label normalization on every metric source
// (probe/probe-core/shared-memory/external) before batching.
func sanitizeTelemetryMetrics(metrics []*telemetryv1.Metric) {
	for _, metric := range metrics {
		if metric == nil || len(metric.Labels) == 0 {
			continue
		}
		normalized := make(map[string]string, len(metric.Labels))
		for _, label := range metric.Labels {
			if label == nil {
				continue
			}
			key := sanitizeLabelToken(label.Key, maxLabelKeyRunes)
			value := sanitizeLabelToken(label.Value, maxLabelValueRunes)
			if key == "" || value == "" {
				continue
			}
			normalized[key] = value
		}
		metric.Labels = buildLabels(normalized)
	}
}

func sanitizeLabelToken(raw string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	clean := strings.TrimSpace(stripControlRunes(raw))
	if clean == "" {
		return ""
	}
	if utf8.RuneCountInString(clean) > maxRunes {
		runes := []rune(clean)
		clean = string(runes[:maxRunes])
	}
	// Ingest-side validation enforces byte-length caps; ensure labels are also
	// bounded by bytes to avoid multi-byte rune overflow.
	for len(clean) > maxRunes {
		clean = clean[:len(clean)-1]
		for len(clean) > 0 && !utf8.ValidString(clean) {
			clean = clean[:len(clean)-1]
		}
	}
	return clean
}

func stripControlRunes(raw string) string {
	if raw == "" {
		return ""
	}
	builder := strings.Builder{}
	builder.Grow(len(raw))
	for _, r := range raw {
		if unicode.IsControl(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
