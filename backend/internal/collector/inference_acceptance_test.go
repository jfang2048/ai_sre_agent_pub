package collector

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/transport"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

const inferenceAcceptanceFixture = `# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="model-a",engine_id="0"} 0
vllm:num_requests_running{model_name="model-a",engine_id="1"} 7
vllm:num_requests_running{model_name="model-b",engine="worker"} 2
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="model-a",engine_id="0"} 12
# TYPE vllm:kv_cache_usage_perc gauge
vllm:kv_cache_usage_perc{model_name="model-a",engine_id="0"} 0.92
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc{model_name="model-a",engine_id="0"} 0.4
vllm:gpu_cache_usage_perc{model_name="model-a",engine_id="1"} 0.5
# TYPE vllm:prompt_tokens_total counter
vllm:prompt_tokens_total{model_name="model-a",engine_id="0"} 15000
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="model-a",engine_id="0"} 9000
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{model_name="model-a",engine_id="0",le="1"} 3
vllm:time_to_first_token_seconds_bucket{model_name="model-a",engine_id="0",le="+Inf"} 4
vllm:time_to_first_token_seconds_sum{model_name="model-a",engine_id="0"} 6
vllm:time_to_first_token_seconds_count{model_name="model-a",engine_id="0"} 4
# TYPE vllm:inter_token_latency_seconds histogram
vllm:inter_token_latency_seconds_sum{model_name="model-a",engine_id="0"} 2
vllm:inter_token_latency_seconds_count{model_name="model-a",engine_id="0"} 20
# TYPE vllm:time_per_output_token_seconds histogram
vllm:time_per_output_token_seconds_sum{model_name="model-a",engine_id="0"} 99
vllm:time_per_output_token_seconds_count{model_name="model-a",engine_id="0"} 99
vllm:time_per_output_token_seconds_sum{model_name="model-a",engine_id="1"} 3
vllm:time_per_output_token_seconds_count{model_name="model-a",engine_id="1"} 30
# TYPE vllm:e2e_request_latency_seconds histogram
vllm:e2e_request_latency_seconds_sum{model_name="model-a",engine_id="0"} 21
vllm:e2e_request_latency_seconds_count{model_name="model-a",engine_id="0"} 4
# TYPE vllm:request_time_per_output_token_seconds histogram
vllm:request_time_per_output_token_seconds_sum{model_name="model-a",engine_id="0"} 999
vllm:request_time_per_output_token_seconds_count{model_name="model-a",engine_id="0"} 999
# TYPE unrelated_cpu_percent gauge
unrelated_cpu_percent 85
`

type inferenceAcceptanceKey struct{ name, endpoint, model, engine string }

func inferenceAcceptanceConfig(url string) Config {
	cfg := DefaultConfig()
	cfg.InferenceMetrics.Endpoints = []InferenceEndpoint{{Name: "serving-a", URL: url}}
	return cfg
}

func inferenceAcceptanceReadings(t *testing.T, metrics []*telemetryv1.Metric) map[inferenceAcceptanceKey]*telemetryv1.Metric {
	t.Helper()
	out := map[inferenceAcceptanceKey]*telemetryv1.Metric{}
	for _, metric := range metrics {
		if !strings.HasPrefix(metric.Name, "node_inference_") {
			continue
		}
		key := inferenceAcceptanceKey{name: strings.TrimPrefix(metric.Name, "node_inference_")}
		for _, label := range metric.Labels {
			switch label.Key {
			case "inference_endpoint":
				key.endpoint = label.Value
			case "model":
				key.model = label.Value
			case "engine":
				key.engine = label.Value
			default:
				t.Errorf("exporter-only label leaked into canonical telemetry: %+v", label)
			}
		}
		if _, exists := out[key]; exists {
			t.Fatalf("duplicate canonical series %+v", key)
		}
		out[key] = metric
	}
	return out
}

func inferenceAcceptanceValue(t *testing.T, values map[inferenceAcceptanceKey]*telemetryv1.Metric, key inferenceAcceptanceKey, expected float64, at time.Time) {
	t.Helper()
	metric, exists := values[key]
	if !exists {
		t.Fatalf("missing canonical series %+v in %+v", key, values)
	}
	if metric.Value != expected || metric.TimestampUnixNano != at.UnixNano() {
		t.Errorf("series %+v: got value=%g timestamp=%d, want value=%g timestamp=%d", key, metric.Value, metric.TimestampUnixNano, expected, at.UnixNano())
	}
}

func inferenceAcceptanceFailure(t *testing.T, metrics []*telemetryv1.Metric, at time.Time, endpoints ...string) {
	t.Helper()
	values := inferenceAcceptanceReadings(t, metrics)
	if len(values) != len(endpoints) {
		t.Errorf("failed scrape must publish only endpoint statuses; got %d canonical series, want %d", len(values), len(endpoints))
	}
	for _, endpoint := range endpoints {
		inferenceAcceptanceValue(t, values, inferenceAcceptanceKey{name: "scrape_success", endpoint: endpoint}, 0, at)
	}
}

func TestInferenceAcceptanceHTTPScrapeCanonicalMappingsAndAliases(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
			t.Errorf("unexpected metrics request: %s %s", r.Method, r.URL)
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/plain") {
			t.Errorf("missing Prometheus text negotiation: %q", r.Header.Get("Accept"))
		}
		_, _ = io.WriteString(w, inferenceAcceptanceFixture)
	}))
	defer server.Close()
	cfg := inferenceAcceptanceConfig(server.URL + "/metrics")
	c := &Collector{cfg: cfg}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	values := inferenceAcceptanceReadings(t, c.collectInferenceMetrics(context.Background(), at, cfg, protectionDecision{}))
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests=%d, want 1", requests.Load())
	}
	want := map[inferenceAcceptanceKey]float64{
		{"scrape_success", "serving-a", "", ""}:                       1,
		{"requests_running", "serving-a", "model-a", "0"}:             0,
		{"requests_running", "serving-a", "model-a", "1"}:             7,
		{"requests_running", "serving-a", "model-b", "worker"}:        2,
		{"requests_waiting", "serving-a", "model-a", "0"}:             12,
		{"kv_cache_utilization_percent", "serving-a", "model-a", "0"}: 92,
		{"kv_cache_utilization_percent", "serving-a", "model-a", "1"}: 50,
		{"prompt_tokens_total", "serving-a", "model-a", "0"}:          15000,
		{"generation_tokens_total", "serving-a", "model-a", "0"}:      9000,
		{"ttft_seconds_sum", "serving-a", "model-a", "0"}:             6,
		{"ttft_seconds_count", "serving-a", "model-a", "0"}:           4,
		{"itl_seconds_sum", "serving-a", "model-a", "0"}:              2,
		{"itl_seconds_count", "serving-a", "model-a", "0"}:            20,
		{"itl_seconds_sum", "serving-a", "model-a", "1"}:              3,
		{"itl_seconds_count", "serving-a", "model-a", "1"}:            30,
		{"e2e_seconds_sum", "serving-a", "model-a", "0"}:              21,
		{"e2e_seconds_count", "serving-a", "model-a", "0"}:            4,
	}
	if len(values) != len(want) {
		t.Errorf("got %d canonical series, want %d; aliases/buckets must not create extra series", len(values), len(want))
	}
	for key, value := range want {
		inferenceAcceptanceValue(t, values, key, value, at)
	}
}

func TestInferenceAcceptanceInvalidUnsupportedAndAmbiguousSourcesFail(t *testing.T) {
	cases := map[string]string{
		"malformed":                 "not valid{broken\n",
		"empty":                     "",
		"unsupported":               "unrelated_cpu_percent 3\n",
		"request averaged alias":    "# TYPE vllm:request_time_per_output_token_seconds histogram\nvllm:request_time_per_output_token_seconds_sum 10\nvllm:request_time_per_output_token_seconds_count 2\n",
		"nan":                       "vllm:num_requests_running NaN\n",
		"positive infinity":         "vllm:num_requests_running +Inf\n",
		"negative infinity":         "vllm:num_requests_running -Inf\n",
		"negative gauge":            "vllm:num_requests_running -1\n",
		"negative counter":          "# TYPE vllm:prompt_tokens_total counter\nvllm:prompt_tokens_total -1\n",
		"cache over 100 percent":    "vllm:kv_cache_usage_perc 1.01\n",
		"negative histogram sum":    "# TYPE vllm:time_to_first_token_seconds histogram\nvllm:time_to_first_token_seconds_sum -1\nvllm:time_to_first_token_seconds_count 2\n",
		"duplicate model engine":    "vllm:num_requests_running{model_name=\"m\",engine_id=\"0\",replica=\"a\"} 1\nvllm:num_requests_running{model_name=\"m\",engine_id=\"0\",replica=\"b\"} 2\n",
		"oversized model identity":  "vllm:num_requests_running{model_name=\"" + strings.Repeat("m", 257) + "\"} 1\n",
		"oversized engine identity": "vllm:num_requests_running{engine_id=\"" + strings.Repeat("e", 257) + "\"} 1\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			cfg := inferenceAcceptanceConfig(server.URL)
			c := &Collector{cfg: cfg}
			at := time.Now().UTC()
			inferenceAcceptanceFailure(t, c.collectInferenceMetrics(context.Background(), at, cfg, protectionDecision{}), at, "serving-a")
		})
	}
}

func TestInferenceAcceptanceMissingHistogramComponentsAreNotZero(t *testing.T) {
	for _, missing := range []string{"sum", "count"} {
		t.Run(missing, func(t *testing.T) {
			body := "# TYPE vllm:time_to_first_token_seconds histogram\n"
			if missing == "sum" {
				body += "vllm:time_to_first_token_seconds_count 2\n"
			} else {
				body += "vllm:time_to_first_token_seconds_sum 3\n"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			cfg := inferenceAcceptanceConfig(server.URL)
			c := &Collector{cfg: cfg}
			at := time.Now().UTC()
			values := inferenceAcceptanceReadings(t, c.collectInferenceMetrics(context.Background(), at, cfg, protectionDecision{}))
			for key, value := range values {
				if key.name == "ttft_seconds_"+missing {
					t.Fatalf("missing histogram %s was fabricated as %g", missing, value.Value)
				}
			}
		})
	}
}

func TestInferenceAcceptanceHTTPFailuresAndRedirectAreIsolated(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		_, _ = io.WriteString(w, inferenceAcceptanceFixture)
	}))
	defer target.Close()
	cases := map[string]http.HandlerFunc{
		"failed": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		},
		"oversize": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("# padding\n", 110000))
		},
		"timeout": func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(time.Second):
			}
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			cfg := inferenceAcceptanceConfig(server.URL)
			cfg.InferenceMetrics.Timeout = 200 * time.Millisecond
			// A healthy independent target must survive another target's failure.
			cfg.InferenceMetrics.Endpoints = append(cfg.InferenceMetrics.Endpoints, InferenceEndpoint{Name: "healthy", URL: target.URL})
			c := &Collector{cfg: cfg}
			at := time.Now().UTC()
			values := inferenceAcceptanceReadings(t, c.collectInferenceMetrics(context.Background(), at, cfg, protectionDecision{}))
			inferenceAcceptanceValue(t, values, inferenceAcceptanceKey{name: "scrape_success", endpoint: "serving-a"}, 0, at)
			inferenceAcceptanceValue(t, values, inferenceAcceptanceKey{name: "scrape_success", endpoint: "healthy"}, 1, at)
			for key := range values {
				if key.endpoint == "serving-a" && key.name != "scrape_success" {
					t.Errorf("failed target leaked a partial measurement: %+v", key)
				}
			}
		})
	}
	if redirected.Load() != int32(len(cases)) {
		t.Errorf("redirect followed: target requests=%d, want exactly %d explicit healthy scrapes", redirected.Load(), len(cases))
	}
}

func TestInferenceAcceptanceCycleDeadlineBoundsAllEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()
	cfg := inferenceAcceptanceConfig(server.URL)
	cfg.InferenceMetrics.Timeout = 200 * time.Millisecond
	cfg.InferenceMetrics.Endpoints = nil
	names := make([]string, 8)
	for i := range names {
		names[i] = fmt.Sprintf("serving-%d", i)
		cfg.InferenceMetrics.Endpoints = append(cfg.InferenceMetrics.Endpoints, InferenceEndpoint{Name: names[i], URL: server.URL})
	}
	c := &Collector{cfg: cfg}
	at := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	metrics := c.collectInferenceMetrics(ctx, at, cfg, protectionDecision{})
	if elapsed := time.Since(at); elapsed > time.Second {
		t.Errorf("scrape cycle did not honor a shared 200ms deadline: %s", elapsed)
	}
	inferenceAcceptanceFailure(t, metrics, at, names...)
}

func TestInferenceAcceptanceCachePreservesObservationAndCannotBeMutated(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		value := calls.Add(1)
		_, _ = fmt.Fprintf(w, "vllm:num_requests_running %d\n", value)
	}))
	defer server.Close()
	cfg := inferenceAcceptanceConfig(server.URL)
	c := &Collector{cfg: cfg}
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	first := c.collectInferenceMetrics(context.Background(), start, cfg, protectionDecision{})
	key := inferenceAcceptanceKey{"requests_running", "serving-a", "unknown", ""}
	inferenceAcceptanceValue(t, inferenceAcceptanceReadings(t, first), key, 1, start)
	// Callers may add labels or rewrite their outgoing payload. Cache ownership
	// must be independent of the returned protobuf graph.
	for _, metric := range first {
		if metric.Name == "node_inference_requests_running" {
			metric.Value = 999
			metric.Labels[0].Value = "mutated"
		}
	}
	second := inferenceAcceptanceReadings(t, c.collectInferenceMetrics(context.Background(), start.Add(time.Second), cfg, protectionDecision{}))
	inferenceAcceptanceValue(t, second, key, 1, start)
	inferenceAcceptanceValue(t, second, inferenceAcceptanceKey{name: "scrape_success", endpoint: "serving-a"}, 1, start)
	if calls.Load() != 1 {
		t.Fatalf("cached collection made %d requests, want 1", calls.Load())
	}
	refresh := start.Add(cfg.InferenceMetrics.Interval)
	inferenceAcceptanceValue(t, inferenceAcceptanceReadings(t, c.collectInferenceMetrics(context.Background(), refresh, cfg, protectionDecision{})), key, 2, refresh)
}

func TestInferenceAcceptanceReloadedSettingsInvalidateCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "vllm:num_requests_running %d\n", calls.Add(1))
	}))
	defer server.Close()
	cases := map[string]func(*Config){
		"URL":      func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].URL += "/new-metrics" },
		"name":     func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].Name = "renamed" },
		"interval": func(cfg *Config) { cfg.InferenceMetrics.Interval = 20 * time.Second },
		"timeout":  func(cfg *Config) { cfg.InferenceMetrics.Timeout = time.Second },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := inferenceAcceptanceConfig(server.URL)
			client, err := transport.New(toTransportConfig(cfg), zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			c := &Collector{cfg: cfg, transport: client, logger: zap.NewNop(), promMetrics: newRuntimePromMetrics()}
			at := time.Now().UTC()
			_ = c.collectInferenceMetrics(context.Background(), at, cfg, protectionDecision{})
			before := calls.Load()
			change(&cfg)
			if err := c.ReloadConfig(cfg); err != nil {
				t.Fatalf("valid inference settings could not reload: %v", err)
			}
			later := at.Add(time.Second)
			values := inferenceAcceptanceReadings(t, c.collectInferenceMetrics(context.Background(), later, c.configSnapshot(), protectionDecision{}))
			if calls.Load() != before+1 {
				t.Fatalf("new %s settings reused old cached payload", name)
			}
			key := inferenceAcceptanceKey{"requests_running", cfg.InferenceMetrics.Endpoints[0].Name, "unknown", ""}
			inferenceAcceptanceValue(t, values, key, float64(before+1), later)
		})
	}
}

func TestInferenceAcceptancePressureAndOptOutStopRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "vllm:num_requests_running 0\n")
	}))
	defer server.Close()
	cfg := inferenceAcceptanceConfig(server.URL)
	c := &Collector{cfg: cfg}
	at := time.Now().UTC()
	for _, step := range []struct {
		offset   time.Duration
		disable  bool
		wantCall int32
	}{
		{0, true, 0},
		{time.Second, false, 1},
		{2 * time.Second, true, 1},
		{time.Minute, true, 1},
	} {
		metrics := c.collectInferenceMetrics(context.Background(), at.Add(step.offset), cfg, protectionDecision{DisableExternal: step.disable})
		if step.disable && len(inferenceAcceptanceReadings(t, metrics)) != 0 {
			t.Error("DisableExternal returned inference observations")
		}
		if calls.Load() != step.wantCall {
			t.Fatalf("DisableExternal=%v calls=%d, want %d", step.disable, calls.Load(), step.wantCall)
		}
	}
	cfg.InferenceMetrics.Endpoints = nil
	if metrics := c.collectInferenceMetrics(context.Background(), at.Add(3*time.Second), cfg, protectionDecision{}); len(metrics) != 0 {
		t.Errorf("disabled inference metrics returned a payload: %+v", metrics)
	}
	cfg.InferenceMetrics.Endpoints = []InferenceEndpoint{{Name: "serving-a", URL: server.URL}}
	_ = c.collectInferenceMetrics(context.Background(), at.Add(4*time.Second), cfg, protectionDecision{})
	if calls.Load() != 2 {
		t.Error("opt-out failed to clear cache before later re-enablement")
	}
	defaults := DefaultConfig()
	if len(defaults.InferenceMetrics.Endpoints) != 0 || defaults.InferenceMetrics.Interval != 30*time.Second || defaults.InferenceMetrics.Timeout != 2*time.Second {
		t.Errorf("inference scrape is not opt-in with documented defaults: %+v", defaults.InferenceMetrics)
	}
	if metrics := c.collectInferenceMetrics(context.Background(), at, defaults, protectionDecision{}); len(metrics) != 0 {
		t.Errorf("default config initiated inference monitoring: %+v", metrics)
	}
}

func TestInferenceAcceptanceConfigurationValidationAndYAML(t *testing.T) {
	cases := map[string]func(*Config){
		"too many endpoints": func(cfg *Config) {
			for i := 1; i < 9; i++ {
				cfg.InferenceMetrics.Endpoints = append(cfg.InferenceMetrics.Endpoints, InferenceEndpoint{Name: fmt.Sprintf("serving-%d", i), URL: "http://127.0.0.1/metrics"})
			}
		},
		"duplicate name": func(cfg *Config) {
			cfg.InferenceMetrics.Endpoints = append(cfg.InferenceMetrics.Endpoints, cfg.InferenceMetrics.Endpoints[0])
		},
		"empty name":     func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].Name = "" },
		"long name":      func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].Name = strings.Repeat("n", 129) },
		"spaced name":    func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].Name = " serving " },
		"multiline name": func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].Name = "serving\nother" },
		"credentials":    func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].URL = "http://user:secret@127.0.0.1/metrics" },
		"fragment":       func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].URL += "#fragment" },
		"query":          func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].URL += "?token=secret" },
		"scheme":         func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].URL = "file:///tmp/metrics" },
		"no host":        func(cfg *Config) { cfg.InferenceMetrics.Endpoints[0].URL = "http:///metrics" },
		"short interval": func(cfg *Config) { cfg.InferenceMetrics.Interval = time.Millisecond },
		"long interval":  func(cfg *Config) { cfg.InferenceMetrics.Interval = time.Minute + time.Second },
		"zero timeout":   func(cfg *Config) { cfg.InferenceMetrics.Timeout = 0 },
		"long timeout":   func(cfg *Config) { cfg.InferenceMetrics.Timeout = 11 * time.Second },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := inferenceAcceptanceConfig("http://127.0.0.1/metrics")
			change(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("invalid inference config accepted: %+v", cfg.InferenceMetrics)
			}
		})
	}
	configPath := filepath.Join(t.TempDir(), "collector.yaml")
	contents := "inference_metrics:\n  endpoints:\n    - name: serving-local\n      url: http://127.0.0.1:8000/metrics\n  interval: 15s\n  timeout: 500ms\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("documented YAML cannot load: %v", err)
	}
	if len(cfg.InferenceMetrics.Endpoints) != 1 || cfg.InferenceMetrics.Endpoints[0].Name != "serving-local" || cfg.InferenceMetrics.Interval != 15*time.Second || cfg.InferenceMetrics.Timeout != 500*time.Millisecond {
		t.Errorf("YAML inference settings not applied: %+v", cfg.InferenceMetrics)
	}
}

func TestInferenceAcceptanceModelEngineCardinalityIsBounded(t *testing.T) {
	for _, count := range []int{32, 33} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var body strings.Builder
			for i := 0; i < count; i++ {
				_, _ = fmt.Fprintf(&body, "vllm:num_requests_running{model_name=\"same-model\",engine_id=\"%d\"} 1\n", i)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body.String()) }))
			defer server.Close()
			cfg := inferenceAcceptanceConfig(server.URL)
			c := &Collector{cfg: cfg}
			at := time.Now().UTC()
			metrics := c.collectInferenceMetrics(context.Background(), at, cfg, protectionDecision{})
			if count == 33 {
				inferenceAcceptanceFailure(t, metrics, at, "serving-a")
			} else {
				values := inferenceAcceptanceReadings(t, metrics)
				if len(values) != count+1 {
					t.Errorf("valid model/engine identities collapsed or rejected: got %d series", len(values))
				}
				inferenceAcceptanceValue(t, values, inferenceAcceptanceKey{name: "scrape_success", endpoint: "serving-a"}, 1, at)
			}
		})
	}
}
