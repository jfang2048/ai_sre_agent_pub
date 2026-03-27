//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDataFlowE2E validates the full metrics pipeline from Agent Probe to Controller to UI API.
//
// Preconditions:
//  1. ./scripts/run-local.sh --enable-agent is already running.
//  2. Controller is reachable at http://127.0.0.1:8080.
//  3. Probe is reachable at http://127.0.0.1:8081 (if we needed to query local probe state).
func TestDataFlowE2E(t *testing.T) {
	client := newE2EClient()
	requireControllerReachable(t, client, controllerURL("/api/v1/fleet"))

	// Wait up to 10 seconds for metrics to be pushed and stored from the probe
	var apiResp *http.Response
	var err error

	require.Eventually(t, func() bool {
		req, errReq := http.NewRequest(http.MethodGet, controllerURL("/api/v1/metrics/history"), nil)
		if errReq != nil {
			err = errReq
			return false
		}

		apiResp, err = client.Do(req)
		if err != nil {
			return false
		}

		return apiResp.StatusCode == http.StatusOK
	}, 10*time.Second, 1*time.Second, "Expected 200 OK from /api/v1/metrics/history within 10s")

	require.NotNil(t, apiResp, "API response is nil")
	defer apiResp.Body.Close()

	var payload struct {
		Nodes []struct {
			CollectorID string             `json:"collector_id"`
			Hostname    string             `json:"hostname"`
			Timestamp   string             `json:"timestamp"`
			Values      map[string]float64 `json:"values"`
		} `json:"nodes"`
	}

	err = json.NewDecoder(apiResp.Body).Decode(&payload)
	require.NoError(t, err, "Failed to decode JSON response from /api/v1/metrics/history")

	// Verify the payload shape
	assert.NotEmpty(t, payload.Nodes, "Expected at least one node reporting metrics")

	foundProbeMetrics := false
	for _, node := range payload.Nodes {
		// Just ensure that there's at least one value
		if len(node.Values) > 0 {
			foundProbeMetrics = true

			// Optional specific assertions based on expected low-level metrics from probe
			// like node_cpu_usage_seconds_total or node_process_cpu_percent
			_, hasCPU := node.Values["node_cpu_usage_seconds_total"]
			_, hasMem := node.Values["node_memory_MemTotal_bytes"]
			assert.True(t, hasCPU || hasMem, "expected core CPU or memory metrics for node %s", node.CollectorID)
		}
	}

	assert.True(t, foundProbeMetrics, "No concrete metric values found in any node history")
}
