package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/store"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWithCORSHandlesOptionsPreflight(t *testing.T) {
	handler := withCORS(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/incidents", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Equal(t, "GET, POST, PUT, DELETE, OPTIONS", rec.Header().Get("Access-Control-Allow-Methods"))
	require.Equal(t, "Content-Type", rec.Header().Get("Access-Control-Allow-Headers"))
}

func TestHandleMetricsHistoryReturnsRequestedMetricSeries(t *testing.T) {
	manager := NewSREManager(zap.NewNop())
	manager.RecordMetrics(map[string]float64{
		"cpu_usage_percent":   55.2,
		"memory_used_percent": 63.1,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history?duration=1h&metric=cpu_usage_percent", nil)
	rec := httptest.NewRecorder()

	manager.handleMetricsHistory(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var payload map[string][]MetricPoint
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Contains(t, payload, "cpu_usage_percent")
	require.NotContains(t, payload, "memory_used_percent")
	require.Len(t, payload["cpu_usage_percent"], 1)
	require.Equal(t, 55.2, payload["cpu_usage_percent"][0].Value)
}

func TestHandleSLOSummaryConcurrentReadWrite(t *testing.T) {
	manager := NewSREManager(zap.NewNop())

	base := &store.Incident{
		ID:         "inc-1",
		Title:      "network regression",
		Severity:   store.SeverityP0,
		State:      store.StateDetected,
		DetectedAt: time.Now(),
		CreatedAt:  time.Now(),
	}
	require.NoError(t, manager.CreateIncident(base))

	var wg sync.WaitGroup
	writers := 4
	readers := 6
	iterations := 250

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				manager.RecordSLOViolation(SLOViolation{
					ID:           "",
					SLOName:      "availability",
					SLOID:        "slo-api",
					CurrentValue: 97.0,
					Target:       99.9,
					Severity:     "critical",
					StartTime:    time.Now(),
				})
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/slo/summary", nil)
				rec := httptest.NewRecorder()
				manager.handleSLOSummary(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("unexpected status code: %d", rec.Code)
					return
				}

				var summary map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
					t.Errorf("failed to decode summary: %v", err)
					return
				}
				if _, ok := summary["violations_count"]; !ok {
					t.Errorf("summary missing violations_count")
					return
				}
			}
		}()
	}

	wg.Wait()
}
