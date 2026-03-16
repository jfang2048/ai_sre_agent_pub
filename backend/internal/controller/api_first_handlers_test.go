package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

func newAPIFirstTestController(t *testing.T) *Controller {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	cfg := DefaultConfig()
	ctrl, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return ctrl
}

func TestSecurityDashboardAndTelemetrySecurityEndpoints(t *testing.T) {
	ctrl := newAPIFirstTestController(t)
	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	now := time.Now().UTC()
	collectorID := "collector-sec-api"
	ctrl.ingestStore.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: collectorID,
		Hostname:    "node-sec-api",
	}, now)
	ctrl.ingestStore.StoreMetrics(collectorID, []*telemetryv1.Metric{
		{Name: "node_security_world_writable_sensitive_paths", Value: 3, TimestampUnixNano: now.UnixNano()},
		{Name: "node_security_firewall_disabled", Value: 1, TimestampUnixNano: now.UnixNano()},
		{Name: "node_security_unexpected_listening_ports_count", Value: 2, TimestampUnixNano: now.UnixNano()},
	}, now)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/security/dashboard?collector_id="+collectorID+"&window=1h", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var dashboard struct {
		Findings []map[string]any `json:"findings"`
		Count    int              `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&dashboard); err != nil {
		t.Fatalf("decode dashboard response: %v", err)
	}
	if dashboard.Count == 0 || len(dashboard.Findings) == 0 {
		t.Fatalf("expected security findings, got count=%d findings=%d", dashboard.Count, len(dashboard.Findings))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/controller/telemetry/security?collector_id="+collectorID+"&window=1h", nil)
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("telemetry security status = %d, want 200; body=%s", w2.Code, w2.Body.String())
	}
	var telemetry struct {
		Findings []map[string]any `json:"findings"`
	}
	if err := json.NewDecoder(w2.Body).Decode(&telemetry); err != nil {
		t.Fatalf("decode telemetry security response: %v", err)
	}
	if len(telemetry.Findings) == 0 {
		t.Fatalf("expected telemetry security findings, got 0")
	}
}

func TestAPIFirstIncidentIntakeAuditAndRegistryEndpoints(t *testing.T) {
	ctrl := newAPIFirstTestController(t)
	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	intakeBody := `{
		"id": "intake-api-test",
		"title": "Checkout warning burst",
		"service": "checkout",
		"severity": "high",
		"collector_id": "collector-a",
		"start_investigation": false
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/incidents/intake", bytes.NewBufferString(intakeBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SRE-Actor", "tester")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("incident intake status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var intakeResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&intakeResp); err != nil {
		t.Fatalf("decode intake response: %v", err)
	}
	if accepted, _ := intakeResp["accepted"].(bool); !accepted {
		t.Fatalf("expected accepted=true, response=%v", intakeResp)
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/api/v1/controller/audit?limit=20", nil)
	auditW := httptest.NewRecorder()
	mux.ServeHTTP(auditW, auditReq)
	if auditW.Code != http.StatusOK {
		t.Fatalf("audit status = %d, want 200; body=%s", auditW.Code, auditW.Body.String())
	}
	var auditResp struct {
		Records []struct {
			Action string `json:"action"`
		} `json:"records"`
	}
	if err := json.NewDecoder(auditW.Body).Decode(&auditResp); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(auditResp.Records) == 0 {
		t.Fatalf("expected at least one audit record")
	}
	foundIntake := false
	for _, rec := range auditResp.Records {
		if strings.EqualFold(rec.Action, "incident_intake") {
			foundIntake = true
			break
		}
	}
	if !foundIntake {
		t.Fatalf("expected incident_intake audit record")
	}

	toolsReq := httptest.NewRequest(http.MethodGet, "/api/v1/controller/tools", nil)
	toolsW := httptest.NewRecorder()
	mux.ServeHTTP(toolsW, toolsReq)
	if toolsW.Code != http.StatusOK {
		t.Fatalf("tools status = %d, want 200; body=%s", toolsW.Code, toolsW.Body.String())
	}
	var toolsResp struct {
		Count int `json:"count"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(toolsW.Body).Decode(&toolsResp); err != nil {
		t.Fatalf("decode tools response: %v", err)
	}
	if toolsResp.Count == 0 {
		t.Fatalf("expected tool registry entries")
	}
	foundLogs := false
	foundTrace := false
	for _, tool := range toolsResp.Tools {
		switch strings.TrimSpace(tool.Name) {
		case "logs_query":
			foundLogs = true
		case "trace_query":
			foundTrace = true
		}
	}
	if !foundLogs || !foundTrace {
		t.Fatalf("expected logs_query and trace_query in registry, got %+v", toolsResp.Tools)
	}

	runsReq := httptest.NewRequest(http.MethodGet, "/api/v1/controller/agent/runs?limit=10", nil)
	runsW := httptest.NewRecorder()
	mux.ServeHTTP(runsW, runsReq)
	if runsW.Code != http.StatusOK {
		t.Fatalf("runs status = %d, want 200; body=%s", runsW.Code, runsW.Body.String())
	}

	legacyRespReq := httptest.NewRequest(http.MethodGet, "/api/v1/response/plans", nil)
	legacyRespW := httptest.NewRecorder()
	mux.ServeHTTP(legacyRespW, legacyRespReq)
	if legacyRespW.Code != http.StatusNotFound {
		t.Fatalf("legacy response endpoint status = %d, want 404; body=%s", legacyRespW.Code, legacyRespW.Body.String())
	}
}
