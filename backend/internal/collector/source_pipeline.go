package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe"
	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probecore"
	probeipcv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/probeipc/v1"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

type probeCoreRuntime interface {
	Start(context.Context) error
	Stop()
	Latest(time.Duration) (*probeipcv1.ProbeBatch, bool)
	Stats() probecore.Stats
}

type compatibilityProbeRuntime interface {
	Start()
	Stop()
	Collect() (*probe.MetricBatch, error)
}

type sourcePipeline struct {
	logger *zap.Logger

	primary       probeCoreRuntime
	compatibility compatibilityProbeRuntime

	mu                  sync.RWMutex
	primaryStarted      bool
	compatibilityActive bool
	lastFallbackReason  string
}

type sourceCollection struct {
	metrics               []*telemetryv1.Metric
	processes             []*telemetryv1.ProcessSample
	source                string
	compatibilityFallback bool
	fallbackReason        string
	primaryExpected       bool
	primaryHealthy        bool
}

func newSourcePipeline(primary probeCoreRuntime, compatibility compatibilityProbeRuntime, logger *zap.Logger) *sourcePipeline {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &sourcePipeline{
		logger:        logger.With(zap.String("component", "collector_source_pipeline")),
		primary:       primary,
		compatibility: compatibility,
	}
}

func (p *sourcePipeline) Start(ctx context.Context, cfg Config) error {
	if p == nil {
		return nil
	}
	if p.primary == nil {
		if cfg.ProbeCore.FallbackToGo && p.compatibility != nil {
			return p.activateCompatibilityFallback(compatibilityFallbackReason(cfg))
		}
		return nil
	}

	if err := p.primary.Start(ctx); err != nil {
		if !cfg.ProbeCore.FallbackToGo || p.compatibility == nil {
			return fmt.Errorf("start probe-core primary source: %w", err)
		}
		p.logger.Warn("probe-core primary source unavailable; activating compatibility fallback",
			zap.Error(err),
		)
		return p.activateCompatibilityFallback("probe_core_start_failed")
	}

	p.mu.Lock()
	p.primaryStarted = true
	p.lastFallbackReason = ""
	p.mu.Unlock()

	return nil
}

func (p *sourcePipeline) Stop() {
	if p == nil {
		return
	}

	p.mu.Lock()
	compatibilityActive := p.compatibilityActive
	p.compatibilityActive = false
	p.primaryStarted = false
	p.lastFallbackReason = ""
	p.mu.Unlock()

	if compatibilityActive && p.compatibility != nil {
		p.compatibility.Stop()
	}
	if p.primary != nil {
		p.primary.Stop()
	}
}

func (p *sourcePipeline) Collect(now time.Time, cfg Config) (sourceCollection, error) {
	if p == nil {
		return sourceCollection{source: "unknown"}, nil
	}

	if p.primary != nil {
		return p.collectFromPrimary(now, cfg)
	}

	if cfg.ProbeCore.FallbackToGo && p.compatibility != nil {
		return p.activateAndCollectCompatibility(cfg.ProbeCore.Enabled, compatibilityFallbackReason(cfg), "activate compatibility source")
	}

	return sourceCollection{source: "unknown"}, nil
}

func (p *sourcePipeline) collectFromPrimary(now time.Time, cfg Config) (sourceCollection, error) {
	if !p.primaryIsStarted() {
		if !cfg.ProbeCore.FallbackToGo || p.compatibility == nil {
			return sourceCollection{
				source:          "probe_core",
				primaryExpected: true,
				primaryHealthy:  false,
			}, errors.New("probe-core primary source is not running")
		}
		return p.collectCompatibilityCollection(true)
	}

	if batch, ok := p.primary.Latest(cfg.ProbeCore.StaleAfter); ok && batch != nil {
		metrics, processes := convertProbeCoreBatch(batch, now)
		return sourceCollection{
			metrics:         metrics,
			processes:       processes,
			source:          "probe_core",
			primaryExpected: true,
			primaryHealthy:  true,
		}, nil
	}

	if !cfg.ProbeCore.FallbackToGo || p.compatibility == nil {
		return sourceCollection{
			source:          "probe_core",
			primaryExpected: true,
			primaryHealthy:  false,
		}, errors.New("probe-core batch unavailable or stale")
	}

	return p.activateAndCollectCompatibility(true, "probe_core_stale", "activate compatibility fallback")
}

func (p *sourcePipeline) activateCompatibilityFallback(reason string) error {
	if p == nil || p.compatibility == nil {
		return nil
	}
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" {
		normalizedReason = "compatibility_fallback"
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastFallbackReason = normalizedReason
	if p.compatibilityActive {
		return nil
	}

	p.compatibility.Start()
	p.compatibilityActive = true
	return nil
}

func (p *sourcePipeline) collectCompatibilityMetrics() ([]*telemetryv1.Metric, error) {
	if p == nil || p.compatibility == nil {
		return nil, nil
	}
	metricBatch, err := p.compatibility.Collect()
	if err != nil {
		return nil, fmt.Errorf("collect compatibility metrics: %w", err)
	}
	return convertProbeMetricBatch(metricBatch), nil
}

func (p *sourcePipeline) collectCompatibilityCollection(primaryExpected bool) (sourceCollection, error) {
	data := sourceCollection{
		source:                "go",
		primaryExpected:       primaryExpected,
		primaryHealthy:        false,
		compatibilityFallback: true,
		fallbackReason:        p.fallbackReason(),
	}

	metrics, err := p.collectCompatibilityMetrics()
	if err != nil {
		return data, err
	}
	data.metrics = metrics
	return data, nil
}

func (p *sourcePipeline) activateAndCollectCompatibility(primaryExpected bool, reason, context string) (sourceCollection, error) {
	if err := p.activateCompatibilityFallback(reason); err != nil {
		return sourceCollection{
			source:          "go",
			primaryExpected: primaryExpected,
			primaryHealthy:  false,
		}, fmt.Errorf("%s: %w", context, err)
	}
	return p.collectCompatibilityCollection(primaryExpected)
}

func (p *sourcePipeline) fallbackReason() string {
	if p == nil {
		return ""
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastFallbackReason
}

func (p *sourcePipeline) primaryIsStarted() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.primaryStarted
}

func compatibilityFallbackReason(cfg Config) string {
	if cfg.ProbeCore.Enabled {
		return "probe_core_unavailable"
	}
	return "probe_core_disabled"
}
