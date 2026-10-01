package inferenceobs_test

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inferenceobs"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

// These tests exercise the public JSON contract so changes to the store's
// internal representation cannot accidentally weaken the monitoring semantics.
type acceptanceReport struct {
	GeneratedAt       time.Time            `json:"generated_at"`
	StaleAfterSeconds float64              `json:"stale_after_seconds"`
	Endpoints         []acceptanceEndpoint `json:"endpoints"`
	GPUs              []acceptanceGPU      `json:"gpus"`
}

type acceptanceEndpoint struct {
	CollectorID   string            `json:"collector_id"`
	Hostname      string            `json:"hostname"`
	Endpoint      string            `json:"endpoint"`
	Status        string            `json:"status"`
	LastAttemptAt time.Time         `json:"last_attempt_at"`
	LastSuccessAt time.Time         `json:"last_success_at"`
	Models        []acceptanceModel `json:"models"`
}

type acceptanceModel struct {
	Name       string                           `json:"name"`
	Engine     string                           `json:"engine"`
	ObservedAt time.Time                        `json:"observed_at"`
	Status     string                           `json:"status"`
	Metrics    map[string]acceptanceMeasurement `json:"metrics"`
	Findings   []acceptanceFinding              `json:"findings"`
}

type acceptanceGPU struct {
	CollectorID string                           `json:"collector_id"`
	Hostname    string                           `json:"hostname"`
	GPUID       string                           `json:"gpu_id"`
	ObservedAt  time.Time                        `json:"observed_at"`
	Status      string                           `json:"status"`
	Metrics     map[string]acceptanceMeasurement `json:"metrics"`
	Findings    []acceptanceFinding              `json:"findings"`
}

type acceptanceMeasurement struct {
	Value         float64   `json:"value"`
	Unit          string    `json:"unit"`
	ObservedAt    time.Time `json:"observed_at"`
	WindowSeconds float64   `json:"window_seconds"`
}

type acceptanceFinding struct {
	Code           string   `json:"code"`
	Severity       string   `json:"severity"`
	Summary        string   `json:"summary"`
	Recommendation string   `json:"recommendation"`
	Evidence       []string `json:"evidence"`
}

func acceptanceSnapshot(t *testing.T, s *inferenceobs.Store, collector string, now time.Time) acceptanceReport {
	t.Helper()
	data, err := json.Marshal(s.Snapshot(collector, now))
	if err != nil {
		t.Fatalf("Snapshot cannot be encoded as API JSON: %v", err)
	}
	var report acceptanceReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("Snapshot does not match the API JSON contract: %v; JSON=%s", err, data)
	}
	if !report.GeneratedAt.Equal(now) || report.StaleAfterSeconds <= 0 {
		t.Fatalf("invalid report metadata: %+v", report)
	}
	return report
}

func acceptanceBatch(s *inferenceobs.Store, collector string, received time.Time, metrics ...*telemetryv1.Metric) {
	s.ProcessBatch(collector, &telemetryv1.TelemetryBatch{
		Collector:        &telemetryv1.CollectorInfo{CollectorId: collector, Hostname: "host-" + collector},
		WallTimeUnixNano: received.UnixNano(),
		Metrics:          metrics,
	}, received)
}

func acceptanceInference(key, endpoint, model, engine string, value float64, at time.Time) *telemetryv1.Metric {
	labels := []*telemetryv1.Label{{Key: "inference_endpoint", Value: endpoint}}
	if model != "" {
		labels = append(labels, &telemetryv1.Label{Key: "model", Value: model})
	}
	if engine != "" {
		labels = append(labels, &telemetryv1.Label{Key: "engine", Value: engine})
	}
	return &telemetryv1.Metric{Name: "node_inference_" + key, Value: value, TimestampUnixNano: at.UnixNano(), Labels: labels}
}

func acceptanceScrape(endpoint string, success float64, at time.Time) *telemetryv1.Metric {
	return acceptanceInference("scrape_success", endpoint, "", "", success, at)
}

func acceptanceGPUReading(key, id string, value float64, at time.Time) *telemetryv1.Metric {
	return &telemetryv1.Metric{Name: "node_gpu_" + key, Value: value, TimestampUnixNano: at.UnixNano(), Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: id}}}
}

func acceptanceEndpointByID(t *testing.T, report acceptanceReport, collector, endpoint string) acceptanceEndpoint {
	t.Helper()
	for _, item := range report.Endpoints {
		if item.CollectorID == collector && item.Endpoint == endpoint {
			return item
		}
	}
	t.Fatalf("missing endpoint %s/%s in %+v", collector, endpoint, report.Endpoints)
	return acceptanceEndpoint{}
}

func acceptanceModelByID(t *testing.T, endpoint acceptanceEndpoint, model, engine string) acceptanceModel {
	t.Helper()
	for _, item := range endpoint.Models {
		if item.Name == model && item.Engine == engine {
			return item
		}
	}
	t.Fatalf("missing model %s/engine=%s in %+v", model, engine, endpoint.Models)
	return acceptanceModel{}
}

func acceptanceGPUByID(t *testing.T, report acceptanceReport, collector, id string) acceptanceGPU {
	t.Helper()
	for _, item := range report.GPUs {
		if item.CollectorID == collector && item.GPUID == id {
			return item
		}
	}
	t.Fatalf("missing GPU %s/%s in %+v", collector, id, report.GPUs)
	return acceptanceGPU{}
}

func acceptanceValue(t *testing.T, metrics map[string]acceptanceMeasurement, key string, want float64, at time.Time) acceptanceMeasurement {
	t.Helper()
	got, ok := metrics[key]
	if !ok {
		t.Fatalf("missing measured %s in %+v", key, metrics)
	}
	if math.Abs(got.Value-want) > 1e-9 || !got.ObservedAt.Equal(at) || got.Unit == "" {
		t.Fatalf("%s=%+v; want value %g, observed_at %s, and a unit", key, got, want, at)
	}
	return got
}

func acceptanceAbsent(t *testing.T, metrics map[string]acceptanceMeasurement, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if got, exists := metrics[key]; exists {
			t.Errorf("unmeasured %s must be absent, got %+v", key, got)
		}
	}
}

func TestAcceptanceSeriesIdentityAndCollectorFilter(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	series := []struct {
		collector, endpoint, model, engine string
		baseline, rate                     float64
	}{
		{"a", "serving-a", "model-a", "0", 100, 1},
		{"a", "serving-a", "model-a", "1", 200, 2},
		{"a", "serving-a", "model-b", "0", 300, 3},
		{"a", "serving-b", "model-a", "0", 400, 4},
		{"b", "serving-a", "model-a", "0", 500, 5},
		{"b", "serving-b", "unknown", "", 600, 6},
	}
	for _, item := range series {
		acceptanceBatch(s, item.collector, start,
			acceptanceScrape(item.endpoint, 1, start),
			acceptanceInference("generation_tokens_total", item.endpoint, item.model, item.engine, item.baseline, start))
	}
	end := start.Add(30 * time.Second)
	for _, item := range series {
		acceptanceBatch(s, item.collector, end,
			acceptanceScrape(item.endpoint, 1, end),
			acceptanceInference("generation_tokens_total", item.endpoint, item.model, item.engine, item.baseline+30*item.rate, end))
	}
	report := acceptanceSnapshot(t, s, "", end)
	if len(report.Endpoints) != 4 {
		t.Fatalf("endpoint identity collision: %+v", report.Endpoints)
	}
	for _, item := range series {
		endpoint := acceptanceEndpointByID(t, report, item.collector, item.endpoint)
		if endpoint.Hostname != "host-"+item.collector {
			t.Errorf("wrong host identity: %+v", endpoint)
		}
		model := acceptanceModelByID(t, endpoint, item.model, item.engine)
		acceptanceValue(t, model.Metrics, "generation_tokens_per_second", item.rate, end)
	}
	filtered := acceptanceSnapshot(t, s, "a", end)
	if len(filtered.Endpoints) != 2 {
		t.Fatalf("collector filter: %+v", filtered.Endpoints)
	}
	for _, endpoint := range filtered.Endpoints {
		if endpoint.CollectorID != "a" {
			t.Errorf("collector filter leaked %+v", endpoint)
		}
	}
}

func TestAcceptanceZeroIsMeasuredAndLifetimeCountersNeedWarmup(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	acceptanceBatch(s, "a", now,
		acceptanceScrape("serving", 1, now),
		acceptanceInference("requests_running", "serving", "model", "", 0, now),
		acceptanceInference("generation_tokens_total", "serving", "model", "", 9999999, now),
		acceptanceInference("ttft_seconds_sum", "serving", "model", "", 9000, now),
		acceptanceInference("ttft_seconds_count", "serving", "model", "", 100, now))
	endpoint := acceptanceEndpointByID(t, acceptanceSnapshot(t, s, "a", now), "a", "serving")
	model := acceptanceModelByID(t, endpoint, "model", "")
	if endpoint.Status != "fresh" || model.Status != "fresh" {
		t.Fatalf("successful current observation should be fresh: endpoint=%+v model=%+v", endpoint, model)
	}
	acceptanceValue(t, model.Metrics, "requests_running", 0, now)
	acceptanceAbsent(t, model.Metrics, "requests_waiting", "kv_cache_utilization_percent", "generation_tokens_per_second", "prompt_tokens_per_second", "ttft_mean_seconds", "itl_mean_seconds", "e2e_mean_seconds")
	if len(model.Findings) != 0 {
		t.Errorf("lifetime totals must not create current latency findings: %+v", model.Findings)
	}
}

func TestAcceptanceFailedScrapesDiscoverTargetsAndGateRetainedValues(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	acceptanceBatch(s, "a", start, acceptanceScrape("never-reached", 0, start))
	failed := acceptanceEndpointByID(t, acceptanceSnapshot(t, s, "a", start), "a", "never-reached")
	if failed.Status != "unavailable" || !failed.LastAttemptAt.Equal(start) || !failed.LastSuccessAt.IsZero() {
		t.Fatalf("first failure lost discovery or invented success: %+v", failed)
	}
	acceptanceBatch(s, "a", start, acceptanceScrape("serving", 1, start), acceptanceInference("requests_waiting", "serving", "model", "", 12, start))
	end := start.Add(30 * time.Second)
	acceptanceBatch(s, "a", end, acceptanceScrape("serving", 0, end))
	endpoint := acceptanceEndpointByID(t, acceptanceSnapshot(t, s, "a", end), "a", "serving")
	if endpoint.Status != "unavailable" || !endpoint.LastAttemptAt.Equal(end) || !endpoint.LastSuccessAt.Equal(start) {
		t.Fatalf("failed scrape must preserve last success and report unavailable: %+v", endpoint)
	}
	model := acceptanceModelByID(t, endpoint, "model", "")
	if model.Status == "fresh" || !model.ObservedAt.Equal(start) {
		t.Fatalf("failed scrape made retained model values look current: %+v", model)
	}
	if measurement, ok := model.Metrics["requests_waiting"]; ok && !measurement.ObservedAt.Equal(start) {
		t.Errorf("retained measurement timestamp was refreshed: %+v", measurement)
	}
}

func TestAcceptanceCounterDuplicatesOutOfOrderAndReset(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	push := func(at, received time.Time, value float64) {
		acceptanceBatch(s, "a", received, acceptanceScrape("serving", 1, at), acceptanceInference("generation_tokens_total", "serving", "model", "", value, at))
	}
	metrics := func(now time.Time) map[string]acceptanceMeasurement {
		return acceptanceModelByID(t, acceptanceEndpointByID(t, acceptanceSnapshot(t, s, "a", now), "a", "serving"), "model", "").Metrics
	}
	push(start, start, 1000)
	second := start.Add(30 * time.Second)
	push(second, second, 1060)
	rate := acceptanceValue(t, metrics(second), "generation_tokens_per_second", 2, second)
	if rate.WindowSeconds != 30 {
		t.Errorf("rate window_seconds=%g; want 30", rate.WindowSeconds)
	}
	received := start.Add(45 * time.Second)
	push(second, received, 999999)
	push(start.Add(10*time.Second), received, 99999)
	acceptanceValue(t, metrics(received), "generation_tokens_per_second", 2, second)
	third := start.Add(60 * time.Second)
	push(third, third, 1120)
	acceptanceValue(t, metrics(third), "generation_tokens_per_second", 2, third)
	reset := start.Add(90 * time.Second)
	push(reset, reset, 7)
	acceptanceAbsent(t, metrics(reset), "generation_tokens_per_second")
	recovered := start.Add(120 * time.Second)
	push(recovered, recovered, 37)
	acceptanceValue(t, metrics(recovered), "generation_tokens_per_second", 1, recovered)
}

func TestAcceptanceHistogramMeansUseCompletedIntervalAndExpireAtIdle(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	push := func(at time.Time, count, ttft, itl, e2e float64) {
		readings := []*telemetryv1.Metric{acceptanceScrape("serving", 1, at)}
		for key, value := range map[string]float64{"ttft": ttft, "itl": itl, "e2e": e2e} {
			readings = append(readings, acceptanceInference(key+"_seconds_sum", "serving", "model", "", value, at), acceptanceInference(key+"_seconds_count", "serving", "model", "", count, at))
		}
		acceptanceBatch(s, "a", at, readings...)
	}
	modelAt := func(at time.Time) acceptanceModel {
		return acceptanceModelByID(t, acceptanceEndpointByID(t, acceptanceSnapshot(t, s, "a", at), "a", "serving"), "model", "")
	}
	push(start, 100, 1000, 500, 2000)
	acceptanceAbsent(t, modelAt(start).Metrics, "ttft_mean_seconds", "itl_mean_seconds", "e2e_mean_seconds")
	second := start.Add(30 * time.Second)
	push(second, 104, 1012, 500.8, 2020)
	for key, want := range map[string]float64{"ttft_mean_seconds": 3, "itl_mean_seconds": 0.2, "e2e_mean_seconds": 5} {
		measurement := acceptanceValue(t, modelAt(second).Metrics, key, want, second)
		if measurement.WindowSeconds != 30 {
			t.Errorf("%s window_seconds=%g; want 30", key, measurement.WindowSeconds)
		}
	}
	idle := start.Add(60 * time.Second)
	push(idle, 104, 1012, 500.8, 2020)
	acceptanceAbsent(t, modelAt(idle).Metrics, "ttft_mean_seconds", "itl_mean_seconds", "e2e_mean_seconds")
	reset := start.Add(90 * time.Second)
	push(reset, 1, 1, 0.01, 2)
	acceptanceAbsent(t, modelAt(reset).Metrics, "ttft_mean_seconds", "itl_mean_seconds", "e2e_mean_seconds")
}

func TestAcceptanceUnrelatedBatchesAndReplayCannotRefreshOldObservations(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	acceptanceBatch(s, "a", start, acceptanceScrape("serving", 1, start), acceptanceInference("generation_tokens_total", "serving", "model", "", 100, start))
	second := start.Add(30 * time.Second)
	original := []*telemetryv1.Metric{acceptanceScrape("serving", 1, second), acceptanceInference("generation_tokens_total", "serving", "model", "", 160, second), acceptanceInference("requests_waiting", "serving", "model", "", 20, second)}
	acceptanceBatch(s, "a", second, original...)
	initial := acceptanceSnapshot(t, s, "a", second)
	later := second.Add(time.Duration(initial.StaleAfterSeconds*float64(time.Second)) + time.Second)
	acceptanceBatch(s, "a", later, &telemetryv1.Metric{Name: "node_cpu_usage_percent", Value: 50, TimestampUnixNano: later.UnixNano()})
	acceptanceBatch(s, "a", later, original...)
	endpoint := acceptanceEndpointByID(t, acceptanceSnapshot(t, s, "a", later), "a", "serving")
	model := acceptanceModelByID(t, endpoint, "model", "")
	if endpoint.Status != "stale" || model.Status != "stale" || !model.ObservedAt.Equal(second) || !endpoint.LastSuccessAt.Equal(second) {
		t.Fatalf("unrelated batch/replay refreshed old data: endpoint=%+v model=%+v", endpoint, model)
	}
	acceptanceAbsent(t, model.Metrics, "generation_tokens_per_second", "requests_waiting")
	if len(model.Findings) != 0 {
		t.Errorf("expired data still raises live findings: %+v", model.Findings)
	}
}

func TestAcceptanceInvalidValuesCannotReplaceMeasuredData(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	acceptanceBatch(s, "a", start, acceptanceScrape("serving", 1, start), acceptanceInference("requests_waiting", "serving", "model", "", 3, start))
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		acceptanceBatch(s, "a", start.Add(time.Second), acceptanceInference("requests_waiting", "serving", "model", "", value, start.Add(time.Second)))
	}
	acceptanceBatch(s, "a", start.Add(time.Second), acceptanceInference("requests_waiting", "serving", "model", "", 99, start.Add(24*time.Hour)))
	model := acceptanceModelByID(t, acceptanceEndpointByID(t, acceptanceSnapshot(t, s, "a", start.Add(time.Second)), "a", "serving"), "model", "")
	acceptanceValue(t, model.Metrics, "requests_waiting", 3, start)
}

func TestAcceptanceGPUMemoryRequiresFreshNumeratorAndDenominator(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	acceptanceBatch(s, "a", start, acceptanceGPUReading("memory_used_mib", "0", 0, start), acceptanceGPUReading("memory_total_mib", "1", 100, start))
	report := acceptanceSnapshot(t, s, "a", start)
	for _, id := range []string{"0", "1"} {
		acceptanceAbsent(t, acceptanceGPUByID(t, report, "a", id).Metrics, "memory_used_percent")
	}
	complete := start.Add(time.Second)
	acceptanceBatch(s, "a", complete, acceptanceGPUReading("memory_used_mib", "0", 0, complete), acceptanceGPUReading("memory_total_mib", "0", 100, complete))
	acceptanceValue(t, acceptanceGPUByID(t, acceptanceSnapshot(t, s, "a", complete), "a", "0").Metrics, "memory_used_percent", 0, complete)
	later := start.Add(time.Duration(report.StaleAfterSeconds*float64(time.Second)) + time.Second)
	acceptanceBatch(s, "a", later, acceptanceGPUReading("memory_used_mib", "0", 99, later))
	gpu := acceptanceGPUByID(t, acceptanceSnapshot(t, s, "a", later), "a", "0")
	acceptanceAbsent(t, gpu.Metrics, "memory_used_percent")
	if len(gpu.Findings) != 0 {
		t.Errorf("stale denominator raised a current memory finding: %+v", gpu.Findings)
	}
	mixed := later.Add(2 * time.Second)
	acceptanceBatch(s, "a", mixed, acceptanceGPUReading("memory_used_mib", "0", 99, mixed), acceptanceGPUReading("memory_total_mib", "0", 100, mixed.Add(-time.Second)))
	acceptanceAbsent(t, acceptanceGPUByID(t, acceptanceSnapshot(t, s, "a", mixed), "a", "0").Metrics, "memory_used_percent")
	recovered := mixed.Add(time.Second)
	acceptanceBatch(s, "a", recovered, acceptanceGPUReading("memory_used_mib", "0", 99, recovered), acceptanceGPUReading("memory_total_mib", "0", 100, recovered))
	gpu = acceptanceGPUByID(t, acceptanceSnapshot(t, s, "a", recovered), "a", "0")
	acceptanceValue(t, gpu.Metrics, "memory_used_percent", 99, recovered)
	if len(gpu.Findings) == 0 {
		t.Error("fresh 99% GPU allocation should have an advisory finding")
	}
}

func TestAcceptanceGPUFaultsUseNewDeltasAndExpireIndependently(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	push := func(at time.Time, xid, ecc float64) {
		acceptanceBatch(s, "a", at, acceptanceGPUReading("xid_errors_total", "0", xid, at), acceptanceGPUReading("ecc_double_bit_errors_total", "0", ecc, at))
	}
	gpuAt := func(at time.Time) acceptanceGPU {
		return acceptanceGPUByID(t, acceptanceSnapshot(t, s, "a", at), "a", "0")
	}
	push(start, 1000, 500)
	first := gpuAt(start)
	acceptanceAbsent(t, first.Metrics, "xid_errors_delta", "ecc_uncorrectable_delta")
	if len(first.Findings) != 0 {
		t.Fatalf("lifetime GPU fault totals are not new failures: %+v", first.Findings)
	}
	second := start.Add(30 * time.Second)
	push(second, 1002, 501)
	gpu := gpuAt(second)
	acceptanceValue(t, gpu.Metrics, "xid_errors_delta", 2, second)
	acceptanceValue(t, gpu.Metrics, "ecc_uncorrectable_delta", 1, second)
	if len(gpu.Findings) < 2 {
		t.Fatalf("new independent Xid/ECC faults need findings: %+v", gpu.Findings)
	}
	for _, finding := range gpu.Findings {
		if finding.Code == "" || finding.Severity == "" || finding.Summary == "" || finding.Recommendation == "" || len(finding.Evidence) == 0 {
			t.Errorf("finding is not actionable evidence: %+v", finding)
		}
	}
	report := acceptanceSnapshot(t, s, "a", second)
	later := second.Add(time.Duration(report.StaleAfterSeconds*float64(time.Second)) + time.Second)
	acceptanceBatch(s, "a", later, acceptanceGPUReading("utilization_sm_percent", "0", 0, later))
	gpu = gpuAt(later)
	acceptanceValue(t, gpu.Metrics, "utilization_sm_percent", 0, later)
	acceptanceAbsent(t, gpu.Metrics, "xid_errors_delta", "ecc_uncorrectable_delta")
	if len(gpu.Findings) != 0 {
		t.Errorf("fresh unrelated GPU gauge refreshed expired fault deltas: %+v", gpu.Findings)
	}
}

func TestAcceptanceGPUCounterResetAndNoNewFaults(t *testing.T) {
	s := inferenceobs.New(inferenceobs.DefaultConfig())
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, value := range []float64{99, 99, 2, 2} {
		at := start.Add(time.Duration(i) * 30 * time.Second)
		acceptanceBatch(s, "a", at, acceptanceGPUReading("xid_errors_total", "0", value, at))
		gpu := acceptanceGPUByID(t, acceptanceSnapshot(t, s, "a", at), "a", "0")
		if i == 0 || i == 2 {
			acceptanceAbsent(t, gpu.Metrics, "xid_errors_delta")
		} else {
			acceptanceValue(t, gpu.Metrics, "xid_errors_delta", 0, at)
		}
		if len(gpu.Findings) != 0 {
			t.Errorf("no new GPU fault at step %d: %+v", i, gpu.Findings)
		}
	}
}
