package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agent "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
)

type stubHistoryProvider struct {
	samples []ingest.MetricHistorySample
}

func (s stubHistoryProvider) MetricHistory(_ string, _ time.Time, limit int) []ingest.MetricHistorySample {
	if limit > 0 && len(s.samples) > limit {
		return append([]ingest.MetricHistorySample(nil), s.samples[len(s.samples)-limit:]...)
	}
	return append([]ingest.MetricHistorySample(nil), s.samples...)
}

func TestHandleFleetTimeseriesBuildsSeriesAndSummary(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-2 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 20},
		{Name: "node_cpu_iowait_percent", Value: 3},
		{Name: "node_memory_MemTotal_bytes", Value: 1000},
		{Name: "node_memory_Used_bytes", Value: 300},
		{Name: "node_network_receive_bytes_per_second", Value: 100},
		{Name: "node_network_transmit_bytes_per_second", Value: 50},
	}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 45},
		{Name: "node_cpu_iowait_percent", Value: 7},
		{Name: "node_memory_MemTotal_bytes", Value: 1000},
		{Name: "node_memory_Used_bytes", Value: 450},
		{Name: "node_network_receive_bytes_per_second", Value: 120},
		{Name: "node_network_transmit_bytes_per_second", Value: 70},
	}, now.Add(20*time.Second))
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 92},
		{Name: "node_cpu_iowait_percent", Value: 18},
		{Name: "node_memory_MemTotal_bytes", Value: 1000},
		{Name: "node_memory_Used_bytes", Value: 700},
		{Name: "node_network_receive_bytes_per_second", Value: 180},
		{Name: "node_network_transmit_bytes_per_second", Value: 90},
	}, now.Add(40*time.Second))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/timeseries?collector_id=collector-a&metric=cpu_usage_percent&window=5m", nil)
	w := httptest.NewRecorder()
	ctrl.handleFleetTimeseries(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp fleetTrendResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-a", resp.CollectorID)
			assert.Equal(t, "node-a", resp.Hostname)
			assert.Equal(t, 3, resp.SampleCount)
			if assert.Len(t, resp.Series, 1) {
				assert.Equal(t, "cpu_usage_percent", resp.Series[0].Key)
				assert.Equal(t, "tier1_runtime", resp.Series[0].Tier)
				assert.Equal(t, "bursty rise", resp.Series[0].Trend)
				assert.InDelta(t, 92.0, resp.Series[0].Latest, 0.001)
			}
			assert.InDelta(t, 92.0, resp.NumericSummary["cpu_usage_percent"], 0.001)
			assert.InDelta(t, 18.0, resp.NumericSummary["cpu_iowait_percent"], 0.001)
			assert.InDelta(t, 1000.0, resp.NumericSummary["memory_total_bytes"], 0.001)
			assert.InDelta(t, 700.0, resp.NumericSummary["memory_used_bytes"], 0.001)
			assert.InDelta(t, 270.0, resp.NumericSummary["network_total_bytes_per_second"], 0.001)
		}
	}
}

func TestHandleFleetTimeseriesBuildsOperationalInsights(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-2 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-risk", Hostname: "node-risk"}, now)
	store.StoreMetrics("collector-risk", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 45},
		{Name: "node_cpu_iowait_percent", Value: 4},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.012},
		{Name: "node_disk_queue_depth_total", Value: 2},
	}, now)
	store.StoreMetrics("collector-risk", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 62},
		{Name: "node_cpu_iowait_percent", Value: 11},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.045},
		{Name: "node_disk_queue_depth_total", Value: 9},
	}, now.Add(20*time.Second))
	store.StoreMetrics("collector-risk", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 78},
		{Name: "node_cpu_iowait_percent", Value: 17},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.095},
		{Name: "node_disk_queue_depth_total", Value: 21},
	}, now.Add(40*time.Second))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/timeseries?collector_id=collector-risk&window=5m", nil)
	w := httptest.NewRecorder()
	ctrl.handleFleetTimeseries(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp fleetTrendResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			if assert.NotEmpty(t, resp.OperationalInsights) {
				assert.Equal(t, "storage_bottleneck_risk", resp.OperationalInsights[0].Key)
				assert.Contains(t, resp.OperationalInsights[0].Summary, "blocked on storage")
			}
		}
	}
}

func TestHandleFleetTimeseriesDefaultsToNewestCollector(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-2 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-old", Hostname: "node-old"}, now)
	store.StoreMetrics("collector-old", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 11},
	}, now)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-new", Hostname: "node-new"}, now.Add(30*time.Second))
	store.StoreMetrics("collector-new", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 77},
	}, now.Add(30*time.Second))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/timeseries?window=5m", nil)
	w := httptest.NewRecorder()
	ctrl.handleFleetTimeseries(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp fleetTrendResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-new", resp.CollectorID)
			assert.Equal(t, "node-new", resp.Hostname)
			assert.InDelta(t, 77.0, resp.NumericSummary["cpu_usage_percent"], 0.001)
		}
	}
}

func TestHandleFleetTimeseriesSupportsModernMemoryMetricAliases(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-modern", Hostname: "node-modern"}, now)
	store.StoreMetrics("collector-modern", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 42},
		{Name: "node_memory_total_bytes", Value: 2048},
		{Name: "node_memory_used_bytes", Value: 1024},
		{Name: "node_memory_available_bytes", Value: 1024},
	}, now)
	store.StoreMetrics("collector-modern", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 55},
		{Name: "node_memory_total_bytes", Value: 2048},
		{Name: "node_memory_used_bytes", Value: 1536},
		{Name: "node_memory_available_bytes", Value: 512},
	}, now.Add(20*time.Second))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/timeseries?collector_id=collector-modern&window=5m", nil)
	w := httptest.NewRecorder()
	ctrl.handleFleetTimeseries(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp fleetTrendResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-modern", resp.CollectorID)
			assert.InDelta(t, 2048.0, resp.NumericSummary["memory_total_bytes"], 0.001)
			assert.InDelta(t, 1536.0, resp.NumericSummary["memory_used_bytes"], 0.001)
			assert.InDelta(t, 512.0, resp.NumericSummary["memory_available_bytes"], 0.001)
			assert.InDelta(t, 75.0, resp.NumericSummary["memory_used_percent"], 0.001)
		}
	}
}

func TestHandleFleetTimeseriesIncludesStorageSummaryFields(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_disk_total_read_bytes_per_second", Value: 128 * 1024 * 1024},
		{Name: "node_disk_total_written_bytes_per_second", Value: 64 * 1024 * 1024},
		{Name: "node_disk_total_iops_per_second", Value: 9000},
		{Name: "node_disk_queue_depth_total", Value: 7.5},
		{Name: "node_disk_utilization_peak_percent", Value: 92.5},
		{Name: "node_disk_avg_request_latency_seconds", Value: 0.003},
		{Name: "node_disk_request_latency_p50_seconds", Value: 0.0015},
		{Name: "node_disk_request_latency_p90_seconds", Value: 0.004},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.009},
		{Name: "node_filesystem_space_pressure_percent", Value: 88},
		{Name: "node_filesystem_inode_pressure_percent", Value: 74},
		{Name: "node_memory_Dirty_bytes", Value: 16 * 1024 * 1024},
		{Name: "node_memory_Writeback_bytes", Value: 8 * 1024 * 1024},
		{Name: "node_pressure_io_some_avg10", Value: 12},
		{Name: "node_pressure_io_full_avg10", Value: 2},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/timeseries?collector_id=collector-a&window=5m", nil)
	w := httptest.NewRecorder()
	ctrl.handleFleetTimeseries(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp fleetTrendResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.InDelta(t, 192*1024*1024, resp.NumericSummary["disk_total_bytes_per_second"], 0.001)
			assert.InDelta(t, 9000.0, resp.NumericSummary["disk_total_iops_per_second"], 0.001)
			assert.InDelta(t, 7.5, resp.NumericSummary["disk_queue_depth_total"], 0.001)
			assert.InDelta(t, 92.5, resp.NumericSummary["disk_utilization_peak_percent"], 0.001)
			assert.InDelta(t, 3.0, resp.NumericSummary["disk_avg_request_latency_ms"], 0.001)
			assert.InDelta(t, 1.5, resp.NumericSummary["disk_request_latency_p50_ms"], 0.001)
			assert.InDelta(t, 4.0, resp.NumericSummary["disk_request_latency_p90_ms"], 0.001)
			assert.InDelta(t, 9.0, resp.NumericSummary["disk_request_latency_p99_ms"], 0.001)
			assert.InDelta(t, 88.0, resp.NumericSummary["filesystem_space_pressure_percent"], 0.001)
			assert.InDelta(t, 74.0, resp.NumericSummary["filesystem_inode_pressure_percent"], 0.001)
			assert.InDelta(t, 12.0, resp.NumericSummary["io_pressure_some_avg10"], 0.001)
			assert.InDelta(t, 2.0, resp.NumericSummary["io_pressure_full_avg10"], 0.001)
		}
	}
}

func TestHandleFleetTimeseriesDemoSeedPopulatesOverviewMetrics(t *testing.T) {
	store := ingest.NewMemoryStore()
	agent.SeedDemoData(store, nil)
	ctrl := &Controller{ingestStore: store}

	for _, collectorID := range []string{"demo-web-1", "demo-gpu-1", "demo-db-1"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/timeseries?collector_id="+collectorID+"&window=30m&limit=120", nil)
		w := httptest.NewRecorder()
		ctrl.handleFleetTimeseries(w, req)

		if !assert.Equal(t, http.StatusOK, w.Code, collectorID) {
			continue
		}

		var resp fleetTrendResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if !assert.NoError(t, err, collectorID) {
			continue
		}

		assert.Greater(t, resp.SampleCount, 1, collectorID)
		assert.Greater(t, resp.NumericSummary["memory_total_bytes"], 0.0, collectorID)
		assert.Greater(t, resp.NumericSummary["memory_used_percent"], 0.0, collectorID)
		assert.Greater(t, resp.NumericSummary["network_rx_bytes_per_second"], 0.0, collectorID)
		assert.Greater(t, resp.NumericSummary["network_tx_bytes_per_second"], 0.0, collectorID)
		assert.Greater(t, resp.NumericSummary["disk_read_bytes_per_second"], 0.0, collectorID)
		assert.Greater(t, resp.NumericSummary["disk_write_bytes_per_second"], 0.0, collectorID)
		assert.Greater(t, resp.NumericSummary["procs_running"], 0.0, collectorID)
	}
}

func TestHandleFleetTimeseriesIncludesProbeCoreSummaryAndSeries(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "collector_probe_core_client_available", Value: 1},
		{Name: "collector_probe_core_active", Value: 0},
		{Name: "collector_probe_core_fresh", Value: 0},
		{Name: "collector_probe_core_collector_selection_valid", Value: 0},
		{Name: "collector_probe_core_last_frame_age_seconds", Value: 12},
		{Name: "collector_probe_core_decode_errors_total", Value: 7},
		{Name: "collector_probe_core_crc_failures_total", Value: 2},
		{Name: "collector_probe_core_restarts_total", Value: 3},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/timeseries?collector_id=collector-a&metric=probe_core_active&metric=probe_core_last_frame_age_ms&window=5m", nil)
	w := httptest.NewRecorder()
	ctrl.handleFleetTimeseries(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp fleetTrendResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.InDelta(t, 1.0, resp.NumericSummary["probe_core_client_available"], 0.001)
			assert.InDelta(t, 0.0, resp.NumericSummary["probe_core_active"], 0.001)
			assert.InDelta(t, 0.0, resp.NumericSummary["probe_core_fresh"], 0.001)
			assert.InDelta(t, 0.0, resp.NumericSummary["probe_core_selection_valid"], 0.001)
			assert.InDelta(t, 12000.0, resp.NumericSummary["probe_core_last_frame_age_ms"], 0.001)
			assert.InDelta(t, 7.0, resp.NumericSummary["probe_core_decode_errors_total"], 0.001)
			assert.InDelta(t, 2.0, resp.NumericSummary["probe_core_crc_failures_total"], 0.001)
			assert.InDelta(t, 3.0, resp.NumericSummary["probe_core_restarts_total"], 0.001)
			if assert.Len(t, resp.Series, 2) {
				assert.Equal(t, "probe_core_active", resp.Series[0].Key)
				assert.InDelta(t, 0.0, resp.Series[0].Latest, 0.001)
				assert.Equal(t, "probe_core_last_frame_age_ms", resp.Series[1].Key)
				assert.InDelta(t, 12000.0, resp.Series[1].Latest, 0.001)
			}
		}
	}
}

func TestHandleFleetTimeseriesUsesInjectedHistoryProvider(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().Add(-time.Minute).Truncate(time.Second)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)

	ctrl := &Controller{
		ingestStore: store,
		metricHistory: stubHistoryProvider{samples: []ingest.MetricHistorySample{
			{
				Timestamp: now,
				Metrics: map[string]float64{
					"node_cpu_usage_percent": 33,
				},
			},
			{
				Timestamp: now.Add(30 * time.Second),
				Metrics: map[string]float64{
					"node_cpu_usage_percent": 88,
				},
			},
		}},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/timeseries?collector_id=collector-a&metric=cpu_usage_percent&window=5m", nil)
	w := httptest.NewRecorder()
	ctrl.handleFleetTimeseries(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp fleetTrendResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-a", resp.CollectorID)
			assert.Equal(t, "node-a", resp.Hostname)
			assert.InDelta(t, 88.0, resp.NumericSummary["cpu_usage_percent"], 0.001)
		}
	}
}
