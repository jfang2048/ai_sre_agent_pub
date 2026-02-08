package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/assert"
)

func TestHandleFleetTimeseriesBuildsSeriesAndSummary(t *testing.T) {
	store := ingest.NewMemoryStore()
	ctrl := &Controller{ingestStore: store}
	now := time.Now().Add(-2 * time.Minute).Truncate(time.Second)

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 20},
		{Name: "node_memory_MemTotal_bytes", Value: 1000},
		{Name: "node_memory_Used_bytes", Value: 300},
		{Name: "node_network_receive_bytes_per_second", Value: 100},
		{Name: "node_network_transmit_bytes_per_second", Value: 50},
	}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 45},
		{Name: "node_memory_MemTotal_bytes", Value: 1000},
		{Name: "node_memory_Used_bytes", Value: 450},
		{Name: "node_network_receive_bytes_per_second", Value: 120},
		{Name: "node_network_transmit_bytes_per_second", Value: 70},
	}, now.Add(20*time.Second))
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 92},
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
			if assert.Len(t, resp.Series, 1) {
				assert.Equal(t, "cpu_usage_percent", resp.Series[0].Key)
				assert.InDelta(t, 92.0, resp.Series[0].Latest, 0.001)
			}
			assert.InDelta(t, 92.0, resp.NumericSummary["cpu_usage_percent"], 0.001)
			assert.InDelta(t, 270.0, resp.NumericSummary["network_total_bytes_per_second"], 0.001)
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
