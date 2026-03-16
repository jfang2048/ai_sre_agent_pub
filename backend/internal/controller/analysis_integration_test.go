package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestHandleGetIncidentReports(t *testing.T) {
	cfg := DefaultAnalysisConfig()
	cfg.Interval = 30 * time.Second
	ext, err := NewAnalysisExtension(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create analysis extension: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analysis/incidents?limit=5", nil)
	rr := httptest.NewRecorder()

	handleGetIncidentReports(rr, req, ext)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	if _, ok := payload["incidents"]; !ok {
		t.Fatalf("missing incidents field")
	}
	if _, ok := payload["classification_count"]; !ok {
		t.Fatalf("missing classification_count field")
	}
}

func TestHandleGetCorrelations(t *testing.T) {
	cfg := DefaultAnalysisConfig()
	cfg.Interval = 30 * time.Second
	ext, err := NewAnalysisExtension(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create analysis extension: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analysis/correlations", nil)
	rr := httptest.NewRecorder()

	handleGetCorrelations(rr, req, ext)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	if _, ok := payload["correlations"]; !ok {
		t.Fatalf("missing correlations field")
	}
}
