package incidents

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

// Orchestrator glues the upstream platforms together to build aggregated context.
type Orchestrator struct {
	cfg Config

	resource   ResourceProvider
	monitoring MonitoringProvider
	logging    LoggingProvider
	k8s        KubernetesProvider

	logger *zap.Logger
}

// NewOrchestrator wires all providers with graceful fallbacks.
func NewOrchestrator(cfg Config, store *ingest.MemoryStore, logger *zap.Logger) (*Orchestrator, error) {
	if logger == nil {
		logger, _ = zap.NewDevelopment()
	}

	resource := ResourceProvider(newStaticResourceProvider(cfg.ResourcePlatform, logger))
	if httpRes := newHTTPResourceProvider(cfg.ResourcePlatform, logger); httpRes != nil {
		resource = resourceChain{primary: httpRes, fallback: resource}
	}

	monitoring := MonitoringProvider(newIngestMonitoringProvider(store, logger))
	if prom := newPromMonitoringProvider(cfg.MonitoringPlatform, logger); prom != nil {
		monitoring = monitoringChain{primary: prom, fallback: monitoring}
	}

	logging := LoggingProvider(newIngestLoggingProvider(store, logger))
	if httpLog := newHTTPLoggingProvider(cfg.LoggingPlatform, logger); httpLog != nil {
		logging = loggingChain{primary: httpLog, fallback: logging}
	}

	var k8sProv KubernetesProvider
	if cfg.Kubernetes.Enabled {
		kp, err := newK8sProvider(cfg.Kubernetes, logger)
		if err != nil {
			logger.Warn("kubernetes integration disabled (init failed)", zap.Error(err))
		} else {
			k8sProv = kp
		}
	}

	return &Orchestrator{
		cfg:        cfg,
		resource:   resource,
		monitoring: monitoring,
		logging:    logging,
		k8s:        k8sProv,
		logger:     logger.With(zap.String("component", "incident_orchestrator")),
	}, nil
}

// BuildContext pulls data from all configured providers for a given alert.
func (o *Orchestrator) BuildContext(ctx context.Context, alert InputAlert, incidentID string) (*AggregatedContext, error) {
	window := o.deriveWindow(alert)
	services := o.deriveServices(alert)
	keywords := o.deriveKeywords(alert)

	serviceImpacts := make([]ServiceImpact, 0, len(services))
	scope := make([]ResourceRef, 0)
	for _, svc := range services {
		impact := o.resource.Resolve(ctx, svc, alert.Labels)
		if len(impact.Resources) > 0 {
			scope = append(scope, impact.Resources...)
		}
		serviceImpacts = append(serviceImpacts, impact)
	}

	metricFindings, err := o.monitoring.QueryContext(ctx, MonitoringRequest{
		Services:  services,
		Resources: scope,
		Window:    window,
		Keywords:  keywords,
	})
	if err != nil {
		o.logger.Warn("monitoring query failed", zap.Error(err))
	}

	logFindings, err := o.logging.Search(ctx, LogRequest{
		Services:  services,
		Resources: scope,
		Window:    window,
		Keywords:  keywords,
		Limit:     o.cfg.LoggingPlatform.DefaultSize,
	})
	if err != nil {
		o.logger.Warn("log query failed", zap.Error(err))
	}

	var kubeFinding *KubernetesFinding
	if o.k8s != nil {
		kubeFinding, err = o.k8s.Snapshot(ctx, KubernetesRequest{
			Services:  services,
			Resources: scope,
			Namespace: o.cfg.Kubernetes.Namespace,
		})
		if err != nil {
			o.logger.Warn("kubernetes snapshot failed", zap.Error(err))
		}
	}

	suspected := o.deriveCauses(metricFindings, logFindings, keywords)

	ctxBundle := &AggregatedContext{
		IncidentID:     incidentID,
		AlertID:        alert.ID,
		Alert:          alert,
		Window:         window,
		Services:       serviceImpacts,
		ResourceScope:  scope,
		Keywords:       keywords,
		Metrics:        metricFindings,
		Logs:           logFindings,
		Kubernetes:     kubeFinding,
		SuspectedCause: suspected,
		GeneratedAt:    time.Now(),
		Notes:          fmt.Sprintf("context window %s - %s", window.Start.UTC().Format(time.RFC3339), window.End.UTC().Format(time.RFC3339)),
	}

	return ctxBundle, nil
}

func (o *Orchestrator) deriveWindow(alert InputAlert) TimeWindow {
	start := alert.StartsAt
	end := alert.EndsAt
	if start.IsZero() {
		start = time.Now()
	}
	if end.IsZero() {
		end = start.Add(o.cfg.WindowAfter)
	}
	return TimeWindow{
		Start: start.Add(-o.cfg.WindowBefore),
		End:   end.Add(o.cfg.WindowAfter),
	}
}

func (o *Orchestrator) deriveServices(alert InputAlert) []string {
	candidates := []string{
		alert.Service,
		alert.Labels["service"],
		alert.Labels["app"],
		alert.Labels["application"],
		alert.Labels["workload"],
	}
	return dedupeStrings(candidates)
}

func (o *Orchestrator) deriveKeywords(alert InputAlert) []string {
	words := make([]string, 0, 12)
	words = append(words, alert.Title, alert.Service, alert.Severity)
	for k, v := range alert.Labels {
		if k == "" || v == "" {
			continue
		}
		if k == "service" || k == "app" {
			continue
		}
		words = append(words, k, v)
	}
	for _, v := range alert.Annotations {
		words = append(words, strings.Fields(v)...)
	}
	return dedupeStrings(words)
}

func (o *Orchestrator) deriveCauses(metrics []MetricFinding, logs []LogFinding, keywords []string) []string {
	causes := make([]string, 0)

	for _, m := range metrics {
		for _, s := range m.Symptoms {
			switch {
			case strings.Contains(s, "CPU"):
				causes = append(causes, "CPU saturation")
			case strings.Contains(s, "memory"):
				causes = append(causes, "memory pressure or leak")
			case strings.Contains(s, "disk"):
				causes = append(causes, "disk I/O contention")
			case strings.Contains(s, "network"):
				causes = append(causes, "network congestion")
			default:
				causes = append(causes, s)
			}
		}
	}

	for _, lf := range logs {
		for _, m := range lf.Matches {
			msg := strings.ToLower(m.Example)
			switch {
			case strings.Contains(msg, "timeout"):
				causes = append(causes, "downstream timeout")
			case strings.Contains(msg, "oom"):
				causes = append(causes, "out-of-memory termination")
			case strings.Contains(msg, "refused"):
				causes = append(causes, "connection refused")
			}
		}
	}

	// Include high-signal keywords such as error codes
	for _, kw := range keywords {
		if strings.HasPrefix(strings.ToLower(kw), "5") || strings.Contains(strings.ToLower(kw), "error") {
			causes = append(causes, fmt.Sprintf("error pattern: %s", kw))
		}
	}

	causes = dedupeStrings(causes)
	sort.Strings(causes)
	return causes
}
