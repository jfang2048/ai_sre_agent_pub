package incidents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/platform/kubernetes"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/store"
	"go.uber.org/zap"
)

// ResourceProvider resolves service → resource relationships and blast radius.
type ResourceProvider interface {
	Resolve(ctx context.Context, service string, labels map[string]string) ServiceImpact
}

// MonitoringProvider queries metrics/anomalies.
type MonitoringProvider interface {
	QueryContext(ctx context.Context, req MonitoringRequest) ([]MetricFinding, error)
}

// LoggingProvider queries log platforms.
type LoggingProvider interface {
	Search(ctx context.Context, req LogRequest) ([]LogFinding, error)
}

// KubernetesProvider collects cluster/workload snapshots.
type KubernetesProvider interface {
	Snapshot(ctx context.Context, req KubernetesRequest) (*KubernetesFinding, error)
}

// ---------------------------------------------------------------------------//
// Resource provider implementations

type staticResourceProvider struct {
	mappings map[string]StaticServiceMapping
	topology *store.TopologyStore
	logger   *zap.Logger
}

func newStaticResourceProvider(cfg ResourcePlatformConfig, logger *zap.Logger) *staticResourceProvider {
	m := make(map[string]StaticServiceMapping)
	for _, s := range cfg.Static {
		m[strings.ToLower(s.Service)] = s
	}
	return &staticResourceProvider{
		mappings: m,
		topology: store.NewTopologyStore(),
		logger:   logger,
	}
}

func (p *staticResourceProvider) Resolve(_ context.Context, service string, labels map[string]string) ServiceImpact {
	if service == "" {
		// Fall back to host/pod if service unknown
		service = labels["service"]
		if service == "" {
			service = labels["app"]
		}
	}

	key := strings.ToLower(service)
	if mapping, ok := p.mappings[key]; ok {
		return ServiceImpact{
			Service:      mapping.Service,
			Environment:  mapping.Environment,
			Dependencies: mapping.Dependencies,
			Resources:    mapping.ResourceScope,
		}
	}

	// Minimal fallback derived from labels
	resources := make([]ResourceRef, 0)
	if host := labels["instance"]; host != "" {
		resources = append(resources, ResourceRef{ID: host, Type: "host", Name: host})
	}
	if pod := labels["pod"]; pod != "" {
		resources = append(resources, ResourceRef{ID: pod, Type: "pod", Name: pod, Scope: labels["namespace"]})
	}
	if node := labels["node"]; node != "" {
		resources = append(resources, ResourceRef{ID: node, Type: "node", Name: node})
	}

	blast, _ := p.topology.CalculateBlastRadius(service)
	return ServiceImpact{
		Service:     service,
		Environment: labels["environment"],
		BlastRadius: blast,
		Resources:   resources,
	}
}

type httpResourceProvider struct {
	endpoint string
	token    string
	client   *http.Client
	logger   *zap.Logger
}

func newHTTPResourceProvider(cfg ResourcePlatformConfig, logger *zap.Logger) *httpResourceProvider {
	if cfg.Endpoint == "" {
		return nil
	}
	return &httpResourceProvider{
		endpoint: strings.TrimSuffix(cfg.Endpoint, "/"),
		token:    resolveToken(cfg.APITokenEnv),
		client:   newHTTPClient(cfg.Timeout),
		logger:   logger,
	}
}

func (p *httpResourceProvider) Resolve(ctx context.Context, service string, labels map[string]string) ServiceImpact {
	if p == nil {
		return ServiceImpact{Service: service}
	}
	if service == "" {
		service = labels["service"]
	}
	target := fmt.Sprintf("%s/api/v1/services/%s", p.endpoint, url.PathEscape(service))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return ServiceImpact{Service: service}
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil || resp == nil {
		return ServiceImpact{Service: service}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return ServiceImpact{Service: service}
	}

	var body struct {
		Environment  string        `json:"environment"`
		Dependencies []string      `json:"dependencies"`
		Resources    []ResourceRef `json:"resources"`
		BlastRadius  []string      `json:"blast_radius"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		p.logger.Debug("resource platform decode failed", zap.Error(err))
		return ServiceImpact{Service: service}
	}

	return ServiceImpact{
		Service:      service,
		Environment:  body.Environment,
		Dependencies: body.Dependencies,
		BlastRadius:  body.BlastRadius,
		Resources:    body.Resources,
	}
}

type resourceChain struct {
	primary  ResourceProvider
	fallback ResourceProvider
}

func (c resourceChain) Resolve(ctx context.Context, service string, labels map[string]string) ServiceImpact {
	var primary ServiceImpact
	if c.primary != nil {
		primary = c.primary.Resolve(ctx, service, labels)
	}
	if c.fallback == nil {
		return primary
	}

	fb := c.fallback.Resolve(ctx, service, labels)

	if primary.Service == "" {
		primary.Service = fb.Service
	}
	if primary.Environment == "" {
		primary.Environment = fb.Environment
	}
	primary.Dependencies = dedupeStrings(append(primary.Dependencies, fb.Dependencies...))
	primary.BlastRadius = dedupeStrings(append(primary.BlastRadius, fb.BlastRadius...))
	primary.Resources = append(primary.Resources, fb.Resources...)

	return primary
}

// ---------------------------------------------------------------------------//
// Monitoring provider implementations

type ingestMonitoringProvider struct {
	store  *ingest.MemoryStore
	logger *zap.Logger
}

func newIngestMonitoringProvider(store *ingest.MemoryStore, logger *zap.Logger) *ingestMonitoringProvider {
	return &ingestMonitoringProvider{
		store:  store,
		logger: logger,
	}
}

func (p *ingestMonitoringProvider) QueryContext(_ context.Context, req MonitoringRequest) ([]MetricFinding, error) {
	if p.store == nil {
		return nil, nil
	}

	// Build a set for quick service match
	serviceSet := make(map[string]struct{})
	for _, s := range req.Services {
		serviceSet[strings.ToLower(s)] = struct{}{}
	}

	out := make([]MetricFinding, 0)
	for _, node := range p.store.Snapshot() {
		// Match by collector/service labels
		nodeService := strings.ToLower(node.Labels["service"])
		if nodeService == "" {
			nodeService = strings.ToLower(node.Hostname)
		}
		if len(serviceSet) > 0 {
			if _, ok := serviceSet[nodeService]; !ok {
				continue
			}
		}

		points := make([]MetricPoint, 0, len(node.Metrics))
		symptoms := make([]string, 0)
		for name, value := range node.Metrics {
			points = append(points, MetricPoint{
				Name:  name,
				Value: value,
			})

			// quick symptom heuristics
			switch {
			case strings.Contains(name, "cpu") && value > 85:
				symptoms = append(symptoms, "high CPU")
			case strings.Contains(name, "memory") && value > 85:
				symptoms = append(symptoms, "memory pressure")
			case strings.Contains(name, "disk_io") && value > 70:
				symptoms = append(symptoms, "disk saturation")
			case strings.Contains(name, "network") && value > 200_000_000:
				symptoms = append(symptoms, "network saturation")
			}
		}

		out = append(out, MetricFinding{
			Scope:    node.Hostname,
			Points:   points,
			Symptoms: dedupeStrings(symptoms),
		})
	}
	return out, nil
}

// promMonitoringProvider queries a Prometheus-compatible API (e.g., Prometheus, VictoriaMetrics).
type promMonitoringProvider struct {
	endpoint string
	token    string
	step     time.Duration
	client   *http.Client
	logger   *zap.Logger
}

func newPromMonitoringProvider(cfg MonitoringPlatformConfig, logger *zap.Logger) *promMonitoringProvider {
	if cfg.Endpoint == "" {
		return nil
	}
	step := cfg.Step
	if step == 0 {
		step = 30 * time.Second
	}
	return &promMonitoringProvider{
		endpoint: strings.TrimSuffix(cfg.Endpoint, "/"),
		token:    resolveToken(cfg.APITokenEnv),
		step:     step,
		client:   newHTTPClient(cfg.Timeout),
		logger:   logger,
	}
}

func (p *promMonitoringProvider) QueryContext(ctx context.Context, req MonitoringRequest) ([]MetricFinding, error) {
	if p == nil || p.endpoint == "" {
		return nil, nil
	}

	queries := []string{
		`rate(http_requests_total{service="%s"}[5m])`,
		`rate(http_requests_total{service="%s",status=~"5.."}[5m])`,
		`avg(node_cpu_usage_percent{service="%s"})`,
		`avg(node_memory_Used_bytes{service="%s"})`,
	}

	results := make([]MetricFinding, 0)
	for _, svc := range req.Services {
		for _, tmpl := range queries {
			q := fmt.Sprintf(tmpl, svc)
			points := p.queryRange(ctx, q, req.Window)
			if len(points) == 0 {
				continue
			}
			results = append(results, MetricFinding{
				Scope:  svc,
				Query:  q,
				Points: points,
			})
		}
	}
	return results, nil
}

func (p *promMonitoringProvider) queryRange(ctx context.Context, query string, window TimeWindow) []MetricPoint {
	u, _ := url.Parse(p.endpoint + "/api/v1/query_range")
	q := u.Query()
	q.Set("query", query)
	q.Set("start", fmt.Sprintf("%d", window.Start.Unix()))
	q.Set("end", fmt.Sprintf("%d", window.End.Unix()))
	q.Set("step", fmt.Sprintf("%.0f", p.step.Seconds()))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil || resp == nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil
	}

	var matrix store.MatrixResult
	if err := json.NewDecoder(resp.Body).Decode(&matrix); err != nil {
		p.logger.Debug("prometheus decode failed", zap.Error(err))
		return nil
	}

	points := make([]MetricPoint, 0)
	for _, series := range matrix.Data.Result {
		for _, val := range series.Values {
			if len(val) < 2 {
				continue
			}
			tsFloat, _ := val[0].(float64)
			numStr, _ := val[1].(string)
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				continue
			}
			points = append(points, MetricPoint{
				Timestamp: time.Unix(int64(tsFloat), 0),
				Name:      query,
				Value:     num,
				Labels:    series.Metric,
			})
		}
	}
	return points
}

type monitoringChain struct {
	primary  MonitoringProvider
	fallback MonitoringProvider
}

func (m monitoringChain) QueryContext(ctx context.Context, req MonitoringRequest) ([]MetricFinding, error) {
	if m.primary == nil {
		if m.fallback == nil {
			return nil, nil
		}
		return m.fallback.QueryContext(ctx, req)
	}
	primary, _ := m.primary.QueryContext(ctx, req)
	if m.fallback == nil {
		return primary, nil
	}
	fb, _ := m.fallback.QueryContext(ctx, req)
	return append(primary, fb...), nil
}

// ---------------------------------------------------------------------------//
// Logging provider implementations

type ingestLoggingProvider struct {
	store  *ingest.MemoryStore
	logger *zap.Logger
}

func newIngestLoggingProvider(store *ingest.MemoryStore, logger *zap.Logger) *ingestLoggingProvider {
	return &ingestLoggingProvider{store: store, logger: logger}
}

func (p *ingestLoggingProvider) Search(_ context.Context, req LogRequest) ([]LogFinding, error) {
	if p.store == nil {
		return nil, nil
	}

	serviceSet := make(map[string]struct{})
	for _, s := range req.Services {
		serviceSet[strings.ToLower(s)] = struct{}{}
	}

	out := make([]LogFinding, 0)
	for _, node := range p.store.Snapshot() {
		nodeService := strings.ToLower(node.Labels["service"])
		if nodeService == "" {
			nodeService = strings.ToLower(node.Hostname)
		}
		if len(serviceSet) > 0 {
			if _, ok := serviceSet[nodeService]; !ok {
				continue
			}
		}

		matches := make([]LogMatch, 0)
		for _, lf := range node.Logs {
			if len(req.Keywords) > 0 {
				hit := false
				for _, kw := range req.Keywords {
					if kw == "" {
						continue
					}
					if strings.Contains(strings.ToLower(lf.Example), strings.ToLower(kw)) {
						hit = true
						break
					}
				}
				if !hit {
					continue
				}
			}
			matches = append(matches, LogMatch{
				Fingerprint: lf.Fingerprint,
				Count:       lf.Count,
				Example:     lf.Example,
				Source:      node.Hostname,
			})
			if req.Limit > 0 && len(matches) >= req.Limit {
				break
			}
		}

		// If keyword filtering yielded nothing, fallback to top logs to keep context non-empty.
		if len(matches) == 0 && len(req.Keywords) > 0 {
			for _, lf := range node.Logs {
				matches = append(matches, LogMatch{
					Fingerprint: lf.Fingerprint,
					Count:       lf.Count,
					Example:     lf.Example,
					Source:      node.Hostname,
				})
				if req.Limit > 0 && len(matches) >= req.Limit {
					break
				}
			}
		}

		if len(matches) == 0 {
			continue
		}
		out = append(out, LogFinding{
			Scope:    node.Hostname,
			Matches:  matches,
			Keywords: req.Keywords,
		})
	}
	return out, nil
}

type httpLoggingProvider struct {
	endpoint string
	token    string
	client   *http.Client
	logger   *zap.Logger
}

func newHTTPLoggingProvider(cfg LoggingPlatformConfig, logger *zap.Logger) *httpLoggingProvider {
	if cfg.Endpoint == "" {
		return nil
	}
	return &httpLoggingProvider{
		endpoint: strings.TrimSuffix(cfg.Endpoint, "/"),
		token:    resolveToken(cfg.APITokenEnv),
		client:   newHTTPClient(cfg.Timeout),
		logger:   logger,
	}
}

func (p *httpLoggingProvider) Search(ctx context.Context, req LogRequest) ([]LogFinding, error) {
	if p == nil || p.endpoint == "" {
		return nil, nil
	}

	payload := map[string]interface{}{
		"services": req.Services,
		"keywords": req.Keywords,
		"start":    req.Window.Start.Unix(),
		"end":      req.Window.End.Unix(),
		"limit":    req.Limit,
	}
	body, _ := json.Marshal(payload)
	target := p.endpoint + "/api/v1/logs/search"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.token)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil || resp == nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("log search failed: %s", resp.Status)
	}

	var parsed struct {
		Results []struct {
			Message string `json:"message"`
			Count   uint64 `json:"count"`
			Source  string `json:"source"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	out := make([]LogFinding, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		out = append(out, LogFinding{
			Scope:    r.Source,
			Keywords: req.Keywords,
			Matches: []LogMatch{
				{Fingerprint: r.Message, Count: r.Count, Example: r.Message, Source: r.Source},
			},
		})
	}
	return out, nil
}

type loggingChain struct {
	primary  LoggingProvider
	fallback LoggingProvider
}

func (l loggingChain) Search(ctx context.Context, req LogRequest) ([]LogFinding, error) {
	if l.primary == nil {
		if l.fallback == nil {
			return nil, nil
		}
		return l.fallback.Search(ctx, req)
	}
	primary, _ := l.primary.Search(ctx, req)
	if l.fallback == nil {
		return primary, nil
	}
	fb, _ := l.fallback.Search(ctx, req)
	return append(primary, fb...), nil
}

// ---------------------------------------------------------------------------//
// Kubernetes provider implementations

type k8sProvider struct {
	client *kubernetes.Client
	logger *zap.Logger
}

func newK8sProvider(cfg KubernetesConfig, logger *zap.Logger) (*k8sProvider, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	client, err := kubernetes.NewClient(kubernetes.Config{
		Kubeconfig: cfg.Kubeconfig,
		Namespace:  cfg.Namespace,
		InCluster:  cfg.InCluster,
		Timeout:    cfg.Timeout,
	}, logger)
	if err != nil {
		return nil, err
	}
	return &k8sProvider{client: client, logger: logger}, nil
}

func (p *k8sProvider) Snapshot(ctx context.Context, req KubernetesRequest) (*KubernetesFinding, error) {
	if p == nil || p.client == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	pods, err := p.client.GetPods(ctx)
	if err != nil {
		return nil, err
	}

	workloads := make(map[string]string)
	for _, pod := range pods {
		for _, svc := range req.Services {
			if strings.Contains(pod, svc) {
				workloads[pod] = "inferred-match"
			}
		}
	}

	return &KubernetesFinding{
		Namespace: req.Namespace,
		Workloads: workloads,
	}, nil
}

// ---------------------------------------------------------------------------//
// Helpers

func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   timeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
}

func resolveToken(env string) string {
	if env == "" {
		return ""
	}
	return os.Getenv(env)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}
