package collector

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/prometheus/common/expfmt"
	prommodel "github.com/prometheus/common/model"
)

const (
	maxInferenceEndpoints     = 8
	maxInferenceResponseBytes = 1 << 20
	maxInferenceModels        = 32
)

type InferenceEndpoint struct {
	Name string `yaml:"name" json:"name"`
	URL  string `yaml:"url" json:"url"`
}
type InferenceMetricsConfig struct {
	Endpoints []InferenceEndpoint `yaml:"endpoints" json:"endpoints"`
	Interval  time.Duration       `yaml:"interval" json:"interval"`
	Timeout   time.Duration       `yaml:"timeout" json:"timeout"`
}

func defaultInferenceMetricsConfig() InferenceMetricsConfig {
	return InferenceMetricsConfig{Interval: 30 * time.Second, Timeout: 2 * time.Second}
}
func (cfg InferenceMetricsConfig) validate() error {
	if len(cfg.Endpoints) == 0 {
		return nil
	}
	if len(cfg.Endpoints) > maxInferenceEndpoints {
		return fmt.Errorf("inference_metrics allows at most %d endpoints", maxInferenceEndpoints)
	}
	if cfg.Interval < time.Second || cfg.Interval > time.Minute {
		return fmt.Errorf("inference_metrics.interval must be between 1s and 1m")
	}
	if cfg.Timeout <= 0 || cfg.Timeout > 10*time.Second {
		return fmt.Errorf("inference_metrics.timeout must be between 0 and 10s")
	}
	names := map[string]bool{}
	for _, ep := range cfg.Endpoints {
		if strings.TrimSpace(ep.Name) != ep.Name || ep.Name == "" || len(ep.Name) > 128 || strings.ContainsAny(ep.Name, "\x00\r\n") || names[ep.Name] {
			return fmt.Errorf("inference_metrics endpoint names must be unique, nonempty and at most 128 bytes")
		}
		names[ep.Name] = true
		u, err := url.Parse(ep.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return fmt.Errorf("inference_metrics endpoint %q requires an HTTP(S) URL without credentials, query or fragment", ep.Name)
		}
	}
	return nil
}

type inferenceCollectionState struct {
	signature string
	cache     cachedTelemetryMetrics
}

func (c *Collector) collectInferenceMetrics(ctx context.Context, now time.Time, cfg Config, decision protectionDecision) []*telemetryv1.Metric {
	settings := cfg.InferenceMetrics
	if len(settings.Endpoints) == 0 {
		c.inferenceState = inferenceCollectionState{}
		return nil
	}
	// Settings are validated on load. Keep direct/programmatic callers bounded too.
	if settings.validate() != nil {
		return nil
	}
	signature := fmt.Sprintf("%v/%s/%s", settings.Endpoints, settings.Interval, settings.Timeout)
	if c.inferenceState.signature != signature {
		c.inferenceState = inferenceCollectionState{signature: signature}
	}
	interval := effectiveAuxiliaryInterval(settings.Interval, c.effectiveCollectionInterval(cfg), decision)
	if decision.DisableExternal {
		return auxiliaryCadenceMetrics(now, "inference", interval, false, c.inferenceState.cache.lastCollected)
	}
	if cached, hit := c.inferenceState.cache.get(now, interval); hit {
		return append(cloneTelemetryMetrics(cached), auxiliaryCadenceMetrics(now, "inference", interval, true, c.inferenceState.cache.lastCollected)...)
	}
	// One deadline bounds the entire cycle, even with multiple slow endpoints.
	cycle, cancel := context.WithTimeout(ctx, settings.Timeout)
	defer cancel()
	client := &http.Client{Timeout: settings.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	results := make([][]*telemetryv1.Metric, len(settings.Endpoints))
	var wg sync.WaitGroup
	for i, ep := range settings.Endpoints {
		wg.Add(1)
		go func(i int, ep InferenceEndpoint) {
			defer wg.Done()
			metrics, err := scrapeInferenceMetrics(cycle, client, ep, now)
			ok := 1.0
			if err != nil {
				ok = 0
				metrics = nil
			}
			status := &telemetryv1.Metric{Name: "node_inference_scrape_success", Value: ok, TimestampUnixNano: now.UnixNano(), Labels: []*telemetryv1.Label{{Key: "inference_endpoint", Value: ep.Name}}}
			results[i] = append([]*telemetryv1.Metric{status}, metrics...)
		}(i, ep)
	}
	wg.Wait()
	metrics := []*telemetryv1.Metric{}
	for _, result := range results {
		metrics = append(metrics, result...)
	}
	c.inferenceState.cache.set(now, metrics)
	return append(cloneTelemetryMetrics(metrics), auxiliaryCadenceMetrics(now, "inference", interval, false, now)...)
}

func scrapeInferenceMetrics(ctx context.Context, client *http.Client, ep InferenceEndpoint, now time.Time) ([]*telemetryv1.Metric, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/plain; version=0.0.4")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInferenceResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxInferenceResponseBytes {
		return nil, fmt.Errorf("metrics response exceeds size limit")
	}
	return parseInferenceMetrics(body, ep.Name, now)
}

type inferenceMetricSpec struct {
	source, target, kind string
	scale                float64
	priority             int
}

var inferenceMetricSpecs = []inferenceMetricSpec{
	{"vllm:num_requests_running", "requests_running", "gauge", 1, 1},
	{"vllm:num_requests_waiting", "requests_waiting", "gauge", 1, 1},
	{"vllm:kv_cache_usage_perc", "kv_cache_utilization_percent", "gauge", 100, 2},
	{"vllm:gpu_cache_usage_perc", "kv_cache_utilization_percent", "gauge", 100, 1},
	{"vllm:prompt_tokens_total", "prompt_tokens_total", "counter", 1, 1},
	{"vllm:generation_tokens_total", "generation_tokens_total", "counter", 1, 1},
	{"vllm:time_to_first_token_seconds", "ttft_seconds", "histogram", 1, 1},
	{"vllm:inter_token_latency_seconds", "itl_seconds", "histogram", 1, 2},
	{"vllm:time_per_output_token_seconds", "itl_seconds", "histogram", 1, 1},
	{"vllm:e2e_request_latency_seconds", "e2e_seconds", "histogram", 1, 1},
}

// Decode only supported exporter families. Bucket quantiles and arbitrary
// exporter labels are deliberately not synthesized into model health.
func parseInferenceMetrics(body []byte, endpoint string, now time.Time) ([]*telemetryv1.Metric, error) {
	parser := expfmt.NewTextParser(prommodel.LegacyValidation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("invalid Prometheus metrics: %w", err)
	}
	type key struct{ model, engine, name string }
	type candidate struct {
		metric   *telemetryv1.Metric
		priority int
	}
	selected := map[key]candidate{}
	identities := map[[2]string]bool{}
	for _, spec := range inferenceMetricSpecs {
		family := families[spec.source]
		if family == nil {
			continue
		}
		for _, item := range family.Metric {
			model, engine := "unknown", ""
			for _, l := range item.Label {
				switch l.GetName() {
				case "model_name":
					model = l.GetValue()
				case "engine":
					engine = l.GetValue()
				}
			}
			if engine == "" {
				for _, l := range item.Label {
					if l.GetName() == "engine_id" {
						engine = l.GetValue()
					}
				}
			}
			if model == "" {
				model = "unknown"
			}
			if len(model) > 256 || len(engine) > 256 || strings.ContainsAny(model+engine, "\x00\r\n") {
				return nil, fmt.Errorf("invalid model identity")
			}
			identity := [2]string{model, engine}
			identities[identity] = true
			if len(identities) > maxInferenceModels {
				return nil, fmt.Errorf("serving model/engine limit exceeded")
			}
			values := map[string]float64{}
			switch spec.kind {
			case "histogram":
				if item.Histogram == nil {
					continue
				}
				if item.Histogram.SampleSum == nil || item.Histogram.SampleCount == nil {
					return nil, fmt.Errorf("incomplete serving histogram")
				}
				values[spec.target+"_sum"] = item.Histogram.GetSampleSum()
				values[spec.target+"_count"] = float64(item.Histogram.GetSampleCount())
			case "counter":
				if item.Counter != nil {
					values[spec.target] = item.Counter.GetValue()
				} else if item.Untyped != nil {
					values[spec.target] = item.Untyped.GetValue()
				}
			case "gauge":
				if item.Gauge != nil {
					values[spec.target] = item.Gauge.GetValue() * spec.scale
				} else if item.Untyped != nil {
					values[spec.target] = item.Untyped.GetValue() * spec.scale
				}
			}
			for name, value := range values {
				if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || (name == "kv_cache_utilization_percent" && value > 100) {
					return nil, fmt.Errorf("invalid serving value")
				}
				k := key{model, engine, name}
				if prev, ok := selected[k]; ok {
					if prev.priority == spec.priority {
						return nil, fmt.Errorf("ambiguous serving series for model and engine")
					}
					if prev.priority > spec.priority {
						continue
					}
				}
				selected[k] = candidate{metric: &telemetryv1.Metric{Name: "node_inference_" + name, Value: value, TimestampUnixNano: now.UnixNano(), Labels: []*telemetryv1.Label{
					{Key: "inference_endpoint", Value: endpoint}, {Key: "model", Value: model}, {Key: "engine", Value: engine},
				}}, priority: spec.priority}
			}
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no supported serving metrics")
	}
	keys := make([]key, 0, len(selected))
	for k := range selected {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.model != b.model {
			return a.model < b.model
		}
		if a.engine != b.engine {
			return a.engine < b.engine
		}
		return a.name < b.name
	})
	out := make([]*telemetryv1.Metric, 0, len(keys))
	for _, k := range keys {
		out = append(out, selected[k].metric)
	}
	return out, nil
}
