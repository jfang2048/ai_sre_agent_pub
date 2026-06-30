package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector"
	"github.com/stretchr/testify/require"
)

func TestBuildMetricsHandlerExposesCollectorStatus(t *testing.T) {
	handler := buildMetricsHandler(&collector.Collector{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.NotEmpty(t, payload["version"])
}
