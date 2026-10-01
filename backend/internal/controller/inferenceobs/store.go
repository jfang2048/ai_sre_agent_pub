package inferenceobs

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

type sample struct {
	value float64
	at    time.Time
}
type series struct {
	last    time.Time
	raw     map[string]sample
	metrics map[string]Measurement
}
type endpointKey struct{ collector, endpoint string }
type modelKey struct{ name, engine string }
type gpuKey struct{ collector, gpu string }
type endpointState struct {
	hostname         string
	attempt, success time.Time
	available        bool
	models           map[modelKey]*series
}
type gpuState struct {
	hostname string
	values   *series
}

type Store struct {
	mu        sync.Mutex
	cfg       Config
	endpoints map[endpointKey]*endpointState
	gpus      map[gpuKey]*gpuState
	limited   bool
}

func New(cfg Config) *Store {
	d := DefaultConfig()
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = d.StaleAfter
	}
	if cfg.Retention < cfg.StaleAfter {
		cfg.Retention = max(d.Retention, cfg.StaleAfter)
	}
	if cfg.MaxEndpoints <= 0 || cfg.MaxEndpoints > d.MaxEndpoints {
		cfg.MaxEndpoints = d.MaxEndpoints
	}
	if cfg.MaxModelsPerEndpoint <= 0 || cfg.MaxModelsPerEndpoint > d.MaxModelsPerEndpoint {
		cfg.MaxModelsPerEndpoint = d.MaxModelsPerEndpoint
	}
	if cfg.MaxGPUs <= 0 || cfg.MaxGPUs > d.MaxGPUs {
		cfg.MaxGPUs = d.MaxGPUs
	}
	for _, pair := range []struct {
		dst               *float64
		fallback, maximum float64
	}{
		{&cfg.WaitingWarning, d.WaitingWarning, 1e9}, {&cfg.KVCacheWarningPercent, d.KVCacheWarningPercent, 100},
		{&cfg.TTFTWarningSeconds, d.TTFTWarningSeconds, 3600}, {&cfg.ITLWarningSeconds, d.ITLWarningSeconds, 3600},
		{&cfg.GPUMemoryWarningPercent, d.GPUMemoryWarningPercent, 100},
	} {
		if !finite(*pair.dst) || *pair.dst <= 0 || *pair.dst > pair.maximum {
			*pair.dst = pair.fallback
		}
	}
	return &Store{cfg: cfg, endpoints: map[endpointKey]*endpointState{}, gpus: map[gpuKey]*gpuState{}}
}

var inferenceUnits = map[string]string{
	"requests_running": "requests", "requests_waiting": "requests", "kv_cache_utilization_percent": "percent",
	"prompt_tokens_total": "tokens", "generation_tokens_total": "tokens",
	"ttft_seconds_sum": "seconds", "ttft_seconds_count": "observations",
	"itl_seconds_sum": "seconds", "itl_seconds_count": "observations",
	"e2e_seconds_sum": "seconds", "e2e_seconds_count": "observations",
}
var gpuUnits = map[string]string{
	"utilization_sm_percent": "percent", "memory_used_mib": "MiB", "memory_total_mib": "MiB",
	"temperature_celsius": "celsius", "throttle_thermal_active": "boolean", "throttle_power_active": "boolean",
	"xid_errors_total": "events", "ecc_double_bit_errors_total": "events",
}

func newSeries() *series    { return &series{raw: map[string]sample{}, metrics: map[string]Measurement{}} }
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func label(m *telemetryv1.Metric, name string) string {
	for _, l := range m.Labels {
		if l != nil && l.Key == name {
			return l.Value
		}
	}
	return ""
}

// ReplaySafe opts this memory-only projection into retained receipt recovery.
// Per-series observation timestamps make duplicate replay idempotent.
func (s *Store) ReplaySafe() bool { return true }
func validID(v string) bool       { return len(v) <= 256 && !strings.ContainsAny(v, "\x00\r\n") }
func fresh(at, now time.Time, ttl time.Duration) bool {
	return !at.IsZero() && !at.After(now.Add(5*time.Second)) && now.Sub(at) <= ttl
}

// ProcessBatch is deterministic for a receipt timestamp and ignores replayed or
// older observations. Cached metrics must retain their original timestamps.
func (s *Store) ProcessBatch(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) {
	if batch == nil || receivedAt.IsZero() {
		return
	}
	host := ""
	if batch.Collector != nil {
		if collectorID == "" {
			collectorID = batch.Collector.CollectorId
		}
		host = batch.Collector.Hostname
	}
	if collectorID == "" || !validID(collectorID) {
		return
	}
	if !validID(host) {
		host = ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(receivedAt)
	type groupKey struct {
		endpoint endpointKey
		model    modelKey
	}
	groups := map[groupKey]map[string]sample{}
	gpuGroups := map[gpuKey]map[string]sample{}
	for _, m := range batch.Metrics {
		if m == nil || !finite(m.Value) || m.Value < 0 {
			continue
		}
		at := receivedAt
		if m.TimestampUnixNano != 0 {
			at = time.Unix(0, m.TimestampUnixNano)
		}
		if at.After(receivedAt.Add(5*time.Second)) || receivedAt.Sub(at) > s.cfg.Retention {
			continue
		}
		if strings.HasPrefix(m.Name, "node_inference_") {
			name := strings.TrimPrefix(m.Name, "node_inference_")
			if _, ok := inferenceUnits[name]; !ok && name != "scrape_success" {
				continue
			}
			if name == "kv_cache_utilization_percent" && m.Value > 100 {
				continue
			}
			ep := label(m, "inference_endpoint")
			if ep == "" || !validID(ep) {
				continue
			}
			key := endpointKey{collectorID, ep}
			state := s.endpoints[key]
			if state == nil {
				if len(s.endpoints) >= s.cfg.MaxEndpoints {
					s.limited = true
					continue
				}
				state = &endpointState{hostname: host, models: map[modelKey]*series{}}
				s.endpoints[key] = state
			}
			if name == "scrape_success" {
				if (m.Value != 0 && m.Value != 1) || !at.After(state.attempt) {
					continue
				}
				state.hostname, state.attempt, state.available = host, at, m.Value == 1
				if state.available {
					state.success = at
				}
				continue
			}
			model, engine := label(m, "model"), label(m, "engine")
			if model == "" {
				model = "unknown"
			}
			if !validID(model) || !validID(engine) {
				continue
			}
			mk := modelKey{model, engine}
			if state.models[mk] == nil {
				if len(state.models) >= s.cfg.MaxModelsPerEndpoint {
					s.limited = true
					continue
				}
				state.models[mk] = newSeries()
			}
			gk := groupKey{key, mk}
			if groups[gk] == nil {
				groups[gk] = map[string]sample{}
			}
			if prev, ok := groups[gk][name]; !ok || at.After(prev.at) {
				groups[gk][name] = sample{m.Value, at}
			}
		} else if strings.HasPrefix(m.Name, "node_gpu_") {
			name := strings.TrimPrefix(m.Name, "node_gpu_")
			switch name {
			case "throttle_thermal_any":
				name = "throttle_thermal_active"
			case "throttle_power_any":
				name = "throttle_power_active"
			}
			if _, ok := gpuUnits[name]; !ok {
				continue
			}
			if name == "utilization_sm_percent" && m.Value > 100 {
				continue
			}
			gpu := label(m, "gpu_id")
			if gpu == "" || !validID(gpu) {
				continue
			}
			key := gpuKey{collectorID, gpu}
			if s.gpus[key] == nil {
				if len(s.gpus) >= s.cfg.MaxGPUs {
					s.limited = true
					continue
				}
				s.gpus[key] = &gpuState{hostname: host, values: newSeries()}
			}
			if gpuGroups[key] == nil {
				gpuGroups[key] = map[string]sample{}
			}
			if prev, ok := gpuGroups[key][name]; !ok || at.After(prev.at) {
				gpuGroups[key][name] = sample{m.Value, at}
			}
		}
	}
	for key, values := range groups {
		ep := s.endpoints[key.endpoint]
		state := ep.models[key.model]
		s.update(state, values, inferenceUnits, false)
		// An explicit failure at the same observation time takes precedence.
		if state.last.After(ep.attempt) {
			ep.attempt, ep.success, ep.available = state.last, state.last, true
		}
		if !state.last.Before(ep.attempt) {
			ep.hostname = host
		}
	}
	for key, values := range gpuGroups {
		s.update(s.gpus[key].values, values, gpuUnits, true)
	}
}

func (s *Store) update(state *series, incoming map[string]sample, units map[string]string, gpu bool) {
	values := map[string]sample{}
	for key, next := range incoming {
		if prev, ok := state.raw[key]; ok && !next.at.After(prev.at) {
			continue
		}
		values[key] = next
	}
	if len(values) == 0 {
		return
	}
	if gpu {
		s.delta(state, values, "xid_errors_total", "xid_errors_delta", "events", false)
		s.delta(state, values, "ecc_double_bit_errors_total", "ecc_uncorrectable_delta", "events", false)
		if _, used := values["memory_used_mib"]; used {
			delete(state.metrics, "memory_used_percent")
		}
		if _, total := values["memory_total_mib"]; total {
			delete(state.metrics, "memory_used_percent")
		}
		used, uok := values["memory_used_mib"]
		total, tok := values["memory_total_mib"]
		if uok && tok && used.at.Equal(total.at) && total.value > 0 && used.value <= total.value {
			state.metrics["memory_used_percent"] = Measurement{Value: 100 * used.value / total.value, Unit: "percent", ObservedAt: used.at}
		}
	} else {
		s.delta(state, values, "prompt_tokens_total", "prompt_tokens_per_second", "tokens/s", true)
		s.delta(state, values, "generation_tokens_total", "generation_tokens_per_second", "tokens/s", true)
		for _, prefix := range []string{"ttft", "itl", "e2e"} {
			s.mean(state, values, prefix)
		}
	}
	for key, value := range values {
		state.raw[key] = value
		if value.at.After(state.last) {
			state.last = value.at
		}
		if strings.HasSuffix(key, "_total") || strings.HasSuffix(key, "_sum") || strings.HasSuffix(key, "_count") || key == "memory_used_mib" || key == "memory_total_mib" {
			continue
		}
		state.metrics[key] = Measurement{Value: value.value, Unit: units[key], ObservedAt: value.at}
	}
}

func (s *Store) delta(state *series, values map[string]sample, input, output, unit string, rate bool) {
	next, ok := values[input]
	if !ok {
		return
	}
	delete(state.metrics, output)
	prev, ok := state.raw[input]
	gap := next.at.Sub(prev.at)
	if !ok || gap <= 0 || gap > s.cfg.StaleAfter || next.value < prev.value {
		return
	}
	value := next.value - prev.value
	if rate {
		value /= gap.Seconds()
	}
	if !finite(value) {
		return
	}
	state.metrics[output] = Measurement{Value: value, Unit: unit, ObservedAt: next.at, WindowSeconds: gap.Seconds()}
}

func (s *Store) mean(state *series, values map[string]sample, prefix string) {
	sumKey, countKey, out := prefix+"_seconds_sum", prefix+"_seconds_count", prefix+"_mean_seconds"
	sum, sok := values[sumKey]
	count, cok := values[countKey]
	if !sok && !cok {
		return
	}
	delete(state.metrics, out)
	oldSum, osok := state.raw[sumKey]
	oldCount, ocok := state.raw[countKey]
	gap := sum.at.Sub(oldSum.at)
	if !sok || !cok || !osok || !ocok || !sum.at.Equal(count.at) || !oldSum.at.Equal(oldCount.at) || gap <= 0 || gap > s.cfg.StaleAfter || sum.value < oldSum.value || count.value <= oldCount.value {
		return
	}
	value := (sum.value - oldSum.value) / (count.value - oldCount.value)
	if !finite(value) {
		return
	}
	state.metrics[out] = Measurement{Value: value, Unit: "seconds", ObservedAt: sum.at, WindowSeconds: gap.Seconds()}
}

func (s *Store) prune(now time.Time) {
	for key, ep := range s.endpoints {
		for mk, model := range ep.models {
			if now.Sub(model.last) > s.cfg.Retention {
				delete(ep.models, mk)
			}
		}
		if now.Sub(ep.attempt) > s.cfg.Retention && len(ep.models) == 0 {
			delete(s.endpoints, key)
		}
	}
	for key, gpu := range s.gpus {
		if now.Sub(gpu.values.last) > s.cfg.Retention {
			delete(s.gpus, key)
		}
	}
}

func (s *Store) measurements(values map[string]Measurement, now time.Time) map[string]Measurement {
	out := map[string]Measurement{}
	for name, value := range values {
		if fresh(value.ObservedAt, now, s.cfg.StaleAfter) {
			out[name] = value
		}
	}
	return out
}

func (s *Store) Snapshot(collectorID string, now time.Time) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	out := Report{GeneratedAt: now, StaleAfterSeconds: s.cfg.StaleAfter.Seconds(), CapacityLimited: s.limited, Endpoints: []Endpoint{}, GPUs: []GPU{}}
	for key, ep := range s.endpoints {
		if collectorID != "" && key.collector != collectorID {
			continue
		}
		status := "fresh"
		if !fresh(ep.attempt, now, s.cfg.StaleAfter) {
			status = "stale"
		} else if !ep.available {
			status = "unavailable"
		}
		e := Endpoint{CollectorID: key.collector, Hostname: ep.hostname, Endpoint: key.endpoint, Status: status, Models: []Model{}}
		if !ep.attempt.IsZero() {
			at := ep.attempt
			e.LastAttemptAt = &at
		}
		if !ep.success.IsZero() {
			at := ep.success
			e.LastSuccessAt = &at
		}
		for mk, state := range ep.models {
			modelStatus := status
			if status == "fresh" && !fresh(state.last, now, s.cfg.StaleAfter) {
				modelStatus = "stale"
			}
			m := Model{Name: mk.name, Engine: mk.engine, ObservedAt: state.last, Status: modelStatus, Metrics: s.measurements(state.metrics, now), Findings: []Finding{}}
			if modelStatus == "fresh" {
				m.Findings = s.modelFindings(m.Metrics)
			}
			e.Models = append(e.Models, m)
		}
		sort.Slice(e.Models, func(i, j int) bool {
			if e.Models[i].Name == e.Models[j].Name {
				return e.Models[i].Engine < e.Models[j].Engine
			}
			return e.Models[i].Name < e.Models[j].Name
		})
		out.Endpoints = append(out.Endpoints, e)
	}
	for key, state := range s.gpus {
		if collectorID != "" && key.collector != collectorID {
			continue
		}
		status := "fresh"
		if !fresh(state.values.last, now, s.cfg.StaleAfter) {
			status = "stale"
		}
		g := GPU{CollectorID: key.collector, Hostname: state.hostname, GPUID: key.gpu, ObservedAt: state.values.last, Status: status, Metrics: s.measurements(state.values.metrics, now), Findings: []Finding{}}
		if status == "fresh" {
			g.Findings = s.gpuFindings(g.Metrics)
		}
		out.GPUs = append(out.GPUs, g)
	}
	sort.Slice(out.Endpoints, func(i, j int) bool {
		a, b := out.Endpoints[i], out.Endpoints[j]
		if a.CollectorID == b.CollectorID {
			return a.Endpoint < b.Endpoint
		}
		return a.CollectorID < b.CollectorID
	})
	sort.Slice(out.GPUs, func(i, j int) bool {
		a, b := out.GPUs[i], out.GPUs[j]
		if a.CollectorID == b.CollectorID {
			return a.GPUID < b.GPUID
		}
		return a.CollectorID < b.CollectorID
	})
	return out
}

func finding(code, summary, recommendation string, metrics map[string]Measurement, keys ...string) Finding {
	f := Finding{Code: code, Severity: "warning", Summary: summary, Recommendation: recommendation, Evidence: []string{}}
	for _, key := range keys {
		if m, ok := metrics[key]; ok {
			f.Evidence = append(f.Evidence, fmt.Sprintf("%s = %.3g %s at %s", key, m.Value, m.Unit, m.ObservedAt.UTC().Format(time.RFC3339Nano)))
		}
	}
	return f
}
func (s *Store) modelFindings(m map[string]Measurement) []Finding {
	out := []Finding{}
	for _, rule := range []struct {
		key, code, summary, next string
		threshold                float64
	}{
		{"requests_waiting", "request_backlog", "Requests are waiting for serving capacity.", "Compare running requests, cache occupancy and same-node GPU evidence before changing capacity.", s.cfg.WaitingWarning},
		{"kv_cache_utilization_percent", "kv_cache_pressure", "KV-cache occupancy is elevated.", "Inspect queue depth, request lengths and the serving memory budget; occupancy alone does not prove OOM.", s.cfg.KVCacheWarningPercent},
		{"ttft_mean_seconds", "slow_first_token", "Observed interval mean time to first token is elevated.", "Check request queue time, prompt lengths and GPU contention; this is an advisory threshold, not an SLO verdict.", s.cfg.TTFTWarningSeconds},
		{"itl_mean_seconds", "slow_streaming", "Observed interval mean streaming interval is elevated.", "Inspect active requests and GPU throttling; streaming intervals are not request-level time per output token.", s.cfg.ITLWarningSeconds},
	} {
		if v, ok := m[rule.key]; ok && v.Value >= rule.threshold {
			out = append(out, finding(rule.code, rule.summary, rule.next, m, rule.key))
		}
	}
	return out
}
func (s *Store) gpuFindings(m map[string]Measurement) []Finding {
	out := []Finding{}
	for _, rule := range []struct {
		key, code, summary, next string
		threshold                float64
	}{
		{"memory_used_percent", "gpu_memory_pressure", "GPU allocated memory is elevated.", "Inspect process memory and serving cache allocation. High allocation alone is not an out-of-memory failure.", s.cfg.GPUMemoryWarningPercent},
		{"throttle_thermal_active", "gpu_thermal_throttling", "GPU reports active thermal throttling.", "Inspect cooling and device temperature thresholds before changing workloads.", 1},
		{"throttle_power_active", "gpu_power_limiting", "GPU reports active power limiting.", "Compare configured power limits with workload demand; a power cap can be intentional.", 1},
		{"xid_errors_delta", "gpu_xid_event", "New GPU Xid events were observed.", "Inspect GPU event codes and driver logs; Xid alone does not establish hardware failure.", 1},
		{"ecc_uncorrectable_delta", "gpu_ecc_event", "New uncorrectable GPU ECC errors were observed.", "Inspect device health and the associated workload; follow the device vendor's recovery guidance.", 1},
	} {
		if v, ok := m[rule.key]; ok && v.Value >= rule.threshold {
			out = append(out, finding(rule.code, rule.summary, rule.next, m, rule.key))
		}
	}
	return out
}
