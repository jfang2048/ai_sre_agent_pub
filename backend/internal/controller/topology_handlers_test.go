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

func TestHandleTopologyBuildsHostAndProcessNodes(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "node-a"}, now)
	store.StoreProcesses("collector-a", []*telemetryv1.ProcessSample{
		{Pid: 101, Name: "cpu-burn", CpuPercent: 96, RssBytes: 512 * 1024 * 1024},
		{Pid: 202, Name: "mem-heavy", CpuPercent: 30, RssBytes: 2 * 1024 * 1024 * 1024},
	}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/topology", nil)
	w := httptest.NewRecorder()
	ctrl.handleTopology(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp TopologyResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.NotEmpty(t, resp.GeneratedAt)
			assert.GreaterOrEqual(t, resp.Summary.HostCount, 1)
			assert.GreaterOrEqual(t, resp.Summary.ProcessCount, 1)
			assert.NotEmpty(t, resp.Nodes)
			assert.NotEmpty(t, resp.Links)
		}
	}
}

func TestHandleTopologyCollectorFilter(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()
	store.StoreProcesses("collector-a", []*telemetryv1.ProcessSample{
		{Pid: 1, Name: "alpha", CpuPercent: 90},
	}, now)
	store.StoreProcesses("collector-b", []*telemetryv1.ProcessSample{
		{Pid: 2, Name: "beta", CpuPercent: 80},
	}, now)

	ctrl := &Controller{ingestStore: store}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/topology?collector_id=collector-a", nil)
	w := httptest.NewRecorder()
	ctrl.handleTopology(w, req)

	if assert.Equal(t, http.StatusOK, w.Code) {
		var resp TopologyResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		if assert.NoError(t, err) {
			assert.Equal(t, "collector-a", resp.CollectorID)
			for _, node := range resp.Nodes {
				if node.Type == "fleet" {
					continue
				}
				assert.Equal(t, "collector-a", node.CollectorID)
			}
		}
	}
}
