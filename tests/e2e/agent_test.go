//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestAgentFlowE2E validates the public AGENT API contract against a running local stack.
//
// Preconditions:
//   1. ./scripts/run-local.sh --enable-agent is already running.
//   2. Controller is reachable at http://127.0.0.1:8080.
func TestAgentFlowE2E(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}

	queryBody := bytes.NewBufferString(`{"query":"RCA for high GPU utilization on fleet"}`)
	queryReq, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/agent/query", queryBody)
	if err != nil {
		t.Fatalf("build query request: %v", err)
	}
	queryReq.Header.Set("Content-Type", "application/json")

	queryResp, err := client.Do(queryReq)
	if err != nil {
		t.Fatalf("query request failed: %v", err)
	}
	defer queryResp.Body.Close()
	if queryResp.StatusCode != http.StatusOK {
		t.Fatalf("query status = %d, want 200", queryResp.StatusCode)
	}

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

	execBody := bytes.NewBufferString(`{"action_id":"` + payload.Actions[0].ID + `"}`)
	execReq, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/agent/execute", execBody)
	if err != nil {
		t.Fatalf("build execute request: %v", err)
	}
	execReq.Header.Set("Content-Type", "application/json")

	execResp, err := client.Do(execReq)
	if err != nil {
		t.Fatalf("execute request failed: %v", err)
	}
	defer execResp.Body.Close()
	if execResp.StatusCode != http.StatusOK {
		t.Fatalf("execute status = %d, want 200", execResp.StatusCode)
	}
}
