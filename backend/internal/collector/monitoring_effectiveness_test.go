package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inferenceobs"
	probeipcv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/probeipc/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMonitoringEffectivenessPipeline uses synthetic exporter responses and GPU
// frames, but the production parser, collector batch, disk spool, gRPC transport,
// durable controller inbox, projection and HTTP handler. No production device,
// hosted model, or inference call is involved. Expected outcomes are fixed here,
// independently of the detector's thresholds and result-building code.
func TestMonitoringEffectivenessPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var sourceMu sync.Mutex
	status, payload := http.StatusOK, ""
	exporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceMu.Lock()
		defer sourceMu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	defer exporter.Close()
	cc := controller.DefaultConfig()
	cc.ListenAddr, cc.GRPCListenAddr = "127.0.0.1:0", "127.0.0.1:0"
	cc.Agent.Enabled, cc.Analysis.Enabled, cc.Checks.Enabled = false, false, false
	cc.Incidents.Enabled, cc.GPU.Enabled, cc.Orchestration.Enabled = false, false, false
	cc.Ingest.Inbox.Path = filepath.Join(t.TempDir(), "inbox.db")
	cc.Ingest.Persistence.Enabled = false
	cc.Inference.StaleAfter = 5 * time.Second
	startController := func() *controller.Controller {
		c, err := controller.New(cc, zap.NewNop())
		require.NoError(t, err)
		require.NoError(t, c.Start(ctx))
		return c
	}
	control := startController()
	defer func() { _ = control.Stop() }()
	cfg := DefaultConfig()
	cfg.CollectorID, cfg.Hostname = "effectiveness-node", "synthetic-node"
	cfg.SpoolDir = t.TempDir()
	cfg.ControllerEndpoints = []string{control.GRPCAddr()}
	cfg.ProbeCore.Enabled, cfg.ProbeCore.FallbackToGo = false, false
	cfg.Hardware.Enabled, cfg.Security.Enabled, cfg.EBPF.Enabled = false, false, false
	cfg.CollectionInterval, cfg.MinCollectionInterval = time.Second, time.Second
	cfg.InferenceMetrics = InferenceMetricsConfig{Interval: time.Second, Timeout: time.Second,
		Endpoints: []InferenceEndpoint{{Name: "synthetic-serving", URL: exporter.URL + "/metrics"}}}
	c, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	defer c.spool.Close()
	defer c.transport.Close()
	// Replace only the device boundary. Production alias conversion still runs.
	device := &fakeProbeCoreRuntime{ok: true}
	c.probeCore = device
	c.sourcePipeline = newSourcePipeline(device, nil, zap.NewNop())
	require.NoError(t, c.sourcePipeline.Start(ctx, cfg))
	defer c.sourcePipeline.Stop()
	c.protection = nil // host CPU/RSS must not change this controlled experiment
	c.logTail = nil
	client := &http.Client{Timeout: 2 * time.Second}
	read := func(t *testing.T) inferenceobs.Report {
		t.Helper()
		resp, err := client.Get("http://" + control.ListenAddr() + "/api/v1/inference/overview?collector_id=effectiveness-node")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var report inferenceobs.Report
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&report))
		require.Len(t, report.Endpoints, 1)
		require.Len(t, report.Endpoints[0].Models, 2)
		require.Len(t, report.GPUs, 1)
		return report
	}
	var lastCollect time.Time
	collect := func(t *testing.T, queue, cache, tokens, ttft, itl, count, memory, thermal float64, failed bool) inferenceobs.Report {
		t.Helper()
		if delay := time.Until(lastCollect.Add(1100 * time.Millisecond)); delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		sourceMu.Lock()
		status = http.StatusOK
		if failed {
			status = http.StatusServiceUnavailable
		}
		payload = fmt.Sprintf(`# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="synthetic-model",engine="0"} %g
vllm:num_requests_waiting{model_name="synthetic-model",engine="1"} 0
# TYPE vllm:kv_cache_usage_perc gauge
vllm:kv_cache_usage_perc{model_name="synthetic-model",engine="0"} %g
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="synthetic-model",engine="0"} %g
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_sum{model_name="synthetic-model",engine="0"} %g
vllm:time_to_first_token_seconds_count{model_name="synthetic-model",engine="0"} %g
# TYPE vllm:inter_token_latency_seconds histogram
vllm:inter_token_latency_seconds_sum{model_name="synthetic-model",engine="0"} %g
vllm:inter_token_latency_seconds_count{model_name="synthetic-model",engine="0"} %g
`, queue, cache, tokens, ttft, count, itl, count)
		sourceMu.Unlock()
		device.batch = &probeipcv1.ProbeBatch{CollectedAtUnixNano: time.Now().UnixNano()}
		for _, v := range []struct {
			name  string
			value float64
		}{
			{"memory_used_mib", memory}, {"memory_total_mib", 100}, {"utilization_sm_percent", 30}, {"throttle_thermal_any", thermal},
		} {
			device.batch.Metrics = append(device.batch.Metrics, &probeipcv1.Metric{Name: "probe_core_gpu_" + v.name, Value: v.value, Labels: []*probeipcv1.Label{{Key: "gpu", Value: "0"}}})
		}
		lastCollect = time.Now()
		_, err := c.collectAndSend(ctx)
		require.NoError(t, err)
		backlog, _ := c.spool.Stats()
		require.Zero(t, backlog, "ACK must drain the collector disk spool")
		return read(t)
	}
	model := func(r inferenceobs.Report, engine string) inferenceobs.Model {
		for _, m := range r.Endpoints[0].Models {
			if m.Engine == engine {
				return m
			}
		}
		t.Fatalf("missing engine %s", engine)
		return inferenceobs.Model{}
	}
	evidence := func(t *testing.T, scenario, truth string, began time.Time, report inferenceobs.Report, engine string, includeGPU bool, want ...string) {
		t.Helper()
		m := model(report, engine)
		got := []string{}
		for _, f := range m.Findings {
			got = append(got, f.Code)
		}
		if includeGPU {
			for _, f := range report.GPUs[0].Findings {
				got = append(got, f.Code)
			}
		}
		if want == nil {
			want = []string{}
		}
		sort.Strings(got)
		sort.Strings(want)
		record := map[string]any{"scenario": scenario, "ground_truth": truth, "expected_findings": want,
			"observed_findings": got, "elapsed_ms": float64(time.Since(began).Microseconds()) / 1000,
			"endpoint_status": report.Endpoints[0].Status, "engine": engine, "includes_gpu": includeGPU,
			"model_observations": m.Metrics, "gpu_observations": report.GPUs[0].Metrics,
			"input_kind": "synthetic_exporter_and_gpu_frames"}
		data, err := json.Marshal(record)
		require.NoError(t, err)
		t.Logf("EFFECTIVENESS_EVIDENCE %s", data)
		require.Equal(t, want, got, "fixed scenario oracle disagrees with HTTP evidence")
	}
	var normal, fault, latest inferenceobs.Report
	if !t.Run("healthy", func(t *testing.T) {
		began := time.Now()
		normal = collect(t, 0, .2, 0, 0, 0, 0, 20, 0, false)
		evidence(t, "healthy", "healthy", began, normal, "0", true)
		require.Equal(t, 0.0, model(normal, "0").Metrics["requests_waiting"].Value)
		require.NotContains(t, model(normal, "0").Metrics, "ttft_mean_seconds")
	}) {
		return
	}
	if !t.Run("fault_detected", func(t *testing.T) {
		began := time.Now()
		fault = collect(t, 18, .94, 300, 24, 2, 10, 96, 1, false)
		evidence(t, "fault_detected", "anomaly", began, fault, "0", true,
			"request_backlog", "kv_cache_pressure", "slow_first_token", "slow_streaming", "gpu_memory_pressure", "gpu_thermal_throttling")
		m := model(fault, "0")
		require.InDelta(t, 2.4, m.Metrics["ttft_mean_seconds"].Value, 1e-9)
		require.InDelta(t, .2, m.Metrics["itl_mean_seconds"].Value, 1e-9)
		seconds := m.ObservedAt.Sub(model(normal, "0").ObservedAt).Seconds()
		require.InDelta(t, 300/seconds, m.Metrics["generation_tokens_per_second"].Value, 1e-6)
	}) {
		return
	}
	if !t.Run("unrelated_model_isolation", func(t *testing.T) {
		began := time.Now()
		evidence(t, "unrelated_model_isolation", "healthy", began, fault, "1", false)
		require.Equal(t, 0.0, model(fault, "1").Metrics["requests_waiting"].Value)
		require.NotContains(t, model(fault, "1").Metrics, "ttft_mean_seconds")
	}) {
		return
	}
	if !t.Run("recovery", func(t *testing.T) {
		began := time.Now()
		latest = collect(t, 0, .2, 330, 25, 2.1, 20, 20, 0, false)
		evidence(t, "recovery", "healthy", began, latest, "0", true)
		require.InDelta(t, .1, model(latest, "0").Metrics["ttft_mean_seconds"].Value, 1e-9)
	}) {
		return
	}
	if !t.Run("counter_reset", func(t *testing.T) {
		began := time.Now()
		latest = collect(t, 0, .2, 0, 0, 0, 0, 20, 0, false)
		evidence(t, "counter_reset", "healthy", began, latest, "0", true)
		require.NotContains(t, model(latest, "0").Metrics, "ttft_mean_seconds")
		require.NotContains(t, model(latest, "0").Metrics, "generation_tokens_per_second")
	}) {
		return
	}
	if !t.Run("scrape_failure", func(t *testing.T) {
		began := time.Now()
		before := latest.Endpoints[0].LastSuccessAt
		latest = collect(t, 0, .2, 0, 0, 0, 0, 20, 0, true)
		evidence(t, "scrape_failure", "unavailable", began, latest, "0", true)
		require.Equal(t, "unavailable", latest.Endpoints[0].Status)
		require.Equal(t, before, latest.Endpoints[0].LastSuccessAt)
	}) {
		return
	}
	if !t.Run("restart_restore", func(t *testing.T) {
		began := time.Now()
		require.NoError(t, control.Stop())
		control = startController()
		restored := read(t)
		evidence(t, "restart_restore", "replay", began, restored, "0", true)
		require.Equal(t, latest.Endpoints, restored.Endpoints)
		require.Equal(t, latest.GPUs, restored.GPUs)
	}) {
		return
	}
	t.Run("stale", func(t *testing.T) {
		began := time.Now()
		time.Sleep(time.Until(lastCollect.Add(cc.Inference.StaleAfter + 150*time.Millisecond)))
		stale := read(t)
		evidence(t, "stale", "stale", began, stale, "0", true)
		require.Equal(t, "stale", stale.Endpoints[0].Status)
		require.Empty(t, model(stale, "0").Metrics)
		require.Empty(t, stale.GPUs[0].Metrics)
	})
}
