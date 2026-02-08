package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgentQueryAndExecuteHandlers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agent.Enabled = true
	cfg.Agent.PolicyFile = ""
	cfg.Agent.LLMEnabled = false
	cfg.GPU.Enabled = false

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, ctrl.agentService)
	require.NotNil(t, ctrl.ingestStore)

	now := time.Now().UTC()
	ctrl.ingestStore.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 91},
	}, now)

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	queryBody := bytes.NewBufferString(`{"query":"RCA for hot node","node":"collector-a"}`)
	queryReq := httptest.NewRequest(http.MethodPost, "/api/v1/agent/query", queryBody)
	queryReq.Header.Set("Content-Type", "application/json")
	queryRes := httptest.NewRecorder()
	mux.ServeHTTP(queryRes, queryReq)

	require.Equal(t, http.StatusOK, queryRes.Code)

	var queryPayload struct {
		QueryID string `json:"query_id"`
		Actions []struct {
			ID string `json:"id"`
		} `json:"actions"`
	}
	require.NoError(t, json.NewDecoder(queryRes.Body).Decode(&queryPayload))
	require.NotEmpty(t, queryPayload.QueryID)
	require.NotEmpty(t, queryPayload.Actions)

	executeBody := bytes.NewBufferString(`{"action_id":"` + queryPayload.Actions[0].ID + `"}`)
	execReq := httptest.NewRequest(http.MethodPost, "/api/v1/agent/execute", executeBody)
	execReq.Header.Set("Content-Type", "application/json")
	execRes := httptest.NewRecorder()
	mux.ServeHTTP(execRes, execReq)

	require.Equal(t, http.StatusOK, execRes.Code)
	var execPayload struct {
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(execRes.Body).Decode(&execPayload))
	require.NotEmpty(t, execPayload.Result.Status)
}

func TestAgentExecuteHandlerReturnsNotFoundForUnknownAction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agent.Enabled = true
	cfg.Agent.PolicyFile = ""
	cfg.Agent.LLMEnabled = false

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	executeBody := bytes.NewBufferString(`{"action_id":"does-not-exist"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/execute", executeBody)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)

	require.Equal(t, http.StatusNotFound, res.Code)
}
