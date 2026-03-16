package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/inventory"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestControllerApplyRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	initialPlaybooks := filepath.Join(dir, "initial_playbooks.yaml")
	reloadedPlaybooks := filepath.Join(dir, "reloaded_playbooks.yaml")
	require.NoError(t, os.WriteFile(initialPlaybooks, []byte(`playbooks:
  - id: cpu-hot
    summary: cpu hot
    severity: P2
    conditions:
      - metric: node_cpu_usage_percent
        op: ">="
        threshold: 80
    actions:
      - type: shell
        command: echo cpu
        safe: true
`), 0o644))
	require.NoError(t, os.WriteFile(reloadedPlaybooks, []byte(`playbooks:
  - id: mem-hot
    summary: mem hot
    severity: P1
    conditions:
      - metric: node_memory_used_percent
        op: ">="
        threshold: 90
    actions:
      - type: shell
        command: echo mem
        safe: true
`), 0o644))

	cfg := DefaultConfig()
	cfg.Nodes = []NodeConfig{{Name: "node-a", Address: "10.0.0.10:9090"}}
	cfg.Agent.PolicyFile = initialPlaybooks

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	next := cfg
	next.ListenAddr = ":9099"
	next.Ingest.NodeRetention = 48 * time.Hour
	next.Ingest.HistorySamplesPerNode = 300
	next.Inventory.HeartbeatTTL = 2 * time.Second
	next.Inventory.StaticTargets = []inventory.StaticProbe{
		{ID: "probe-b", Address: "10.0.0.11", Port: 9091, Enabled: true},
	}
	next.Agent.PolicyFile = reloadedPlaybooks

	report, err := ctrl.ApplyRuntimeConfig(next)
	require.NoError(t, err)
	require.Contains(t, report.Applied, "agent.policy_file")
	require.Contains(t, report.Applied, "ingest.node_retention")
	require.Contains(t, report.Applied, "inventory.targets_file")
	require.Contains(t, report.RestartRequired, "listen")

	stats := ctrl.ingestStore.Stats()
	require.Equal(t, "48h0m0s", stats.NodeRetention)
	require.Equal(t, 300, stats.HistorySamplesPerNode)

	var probeBFound bool
	for _, probe := range ctrl.inventoryManager.List() {
		if probe.ID == "probe-b" && probe.Address == "10.0.0.11:9091" {
			probeBFound = true
			break
		}
	}
	require.True(t, probeBFound)
	require.Equal(t, reloadedPlaybooks, ctrl.config.Agent.PolicyFile)
}

func TestControllerConfigReloadHandler(t *testing.T) {
	ctrl, err := New(DefaultConfig(), zap.NewNop())
	require.NoError(t, err)
	ctrl.SetRuntimeConfigReloader(func(ctx context.Context, source string) (RuntimeConfigReloadReport, error) {
		return RuntimeConfigReloadReport{
			Source:     source,
			Applied:    []string{"inventory.targets_file"},
			Reloadable: append([]string(nil), runtimeReloadableFields...),
			Timestamp:  time.Now().UTC(),
		}, nil
	})

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/controller/config/reload", nil)
	getResp := httptest.NewRecorder()
	mux.ServeHTTP(getResp, getReq)
	require.Equal(t, http.StatusOK, getResp.Code)

	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/controller/config/reload", nil)
	postResp := httptest.NewRecorder()
	mux.ServeHTTP(postResp, postReq)
	require.Equal(t, http.StatusOK, postResp.Code)

	var report RuntimeConfigReloadReport
	require.NoError(t, json.NewDecoder(postResp.Body).Decode(&report))
	require.Equal(t, "api", report.Source)
	require.Equal(t, []string{"inventory.targets_file"}, report.Applied)
}
