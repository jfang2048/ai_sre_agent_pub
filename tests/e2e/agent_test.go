//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestAgentFlowE2E validates the public AGENT API contract against a running local stack.
//
// Preconditions:
//  1. ./scripts/run-local.sh --enable-agent is already running.
//  2. Controller is reachable at http://127.0.0.1:8080.
func TestAgentFlowE2E(t *testing.T) {
	client := newE2EClient()
	requireControllerReachable(t, client, controllerURL("/api/v1/fleet"))

	queryReq := newJSONRequest(
		t,
		http.MethodPost,
		controllerURL("/api/v1/agent/query"),
		`{"query":"RCA for high GPU utilization on fleet"}`,
	)

	queryResp, err := client.Do(queryReq)
	if err != nil {
		t.Fatalf("query request failed: %v", err)
	}
	defer queryResp.Body.Close()
	requireHTTPStatus(t, queryResp, http.StatusOK, "query")

	var payload struct {
		Actions []struct {
			ID string `json:"id"`
		} `json:"actions"`
	}
	if err := json.NewDecoder(queryResp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode query response: %v", err)
	}
	if len(payload.Actions) == 0 || payload.Actions[0].ID == "" {
		t.Fatal("query response did not include actionable id")
	}

	execReq := newJSONRequest(
		t,
		http.MethodPost,
		controllerURL("/api/v1/agent/execute"),
		`{"action_id":"`+payload.Actions[0].ID+`"}`,
	)

	execResp, err := client.Do(execReq)
	if err != nil {
		t.Fatalf("execute request failed: %v", err)
	}
	defer execResp.Body.Close()
	requireHTTPStatus(t, execResp, http.StatusOK, "execute")
}
