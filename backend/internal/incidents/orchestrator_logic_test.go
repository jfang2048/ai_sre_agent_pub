package incidents

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestOrchestratorDeriveWindow validates time window derivation
func TestOrchestratorDeriveWindow(t *testing.T) {
	testCases := []struct {
		name     string
		alert    InputAlert
		before   time.Duration
		after    time.Duration
		validate func(TimeWindow, InputAlert)
	}{
		{
			name: "window with both start and end",
			alert: InputAlert{
				StartsAt: time.Now().Add(-10 * time.Minute),
				EndsAt:   time.Now().Add(5 * time.Minute),
			},
			before: 15 * time.Minute,
			after:  20 * time.Minute,
			validate: func(win TimeWindow, alert InputAlert) {
				expectedStart := alert.StartsAt.Add(-15 * time.Minute)
				expectedEnd := alert.EndsAt.Add(20 * time.Minute)
				require.True(t, expectedStart.Sub(win.Start) < time.Second)
				require.True(t, expectedEnd.Sub(win.End) < time.Second)
			},
		},
		{
			name: "window with only start time",
			alert: InputAlert{
				StartsAt: time.Now().Add(-10 * time.Minute),
				EndsAt:   time.Time{},
			},
			before: 5 * time.Minute,
			after:  10 * time.Minute,
			validate: func(win TimeWindow, alert InputAlert) {
				expectedStart := alert.StartsAt.Add(-5 * time.Minute)
				expectedEnd := alert.StartsAt.Add(10 * time.Minute)
				require.True(t, expectedStart.Sub(win.Start) < time.Second)
				require.True(t, expectedEnd.Sub(win.End) < time.Second)
			},
		},
		{
			name: "window with zero start time",
			alert: InputAlert{
				StartsAt: time.Time{},
				EndsAt:   time.Time{},
			},
			before: 5 * time.Minute,
			after:  10 * time.Minute,
			validate: func(win TimeWindow, alert InputAlert) {
				// Zero start should default to now
				require.False(t, win.Start.IsZero())
				require.False(t, win.End.IsZero())
				require.True(t, win.Start.Before(win.End))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.WindowBefore = tc.before
			cfg.WindowAfter = tc.after

			orch := &Orchestrator{cfg: cfg}
			window := orch.deriveWindow(tc.alert)

			tc.validate(window, tc.alert)
		})
	}
}

// TestOrchestratorDeriveServices validates service derivation
func TestOrchestratorDeriveServices(t *testing.T) {
	testCases := []struct {
		name             string
		alert            InputAlert
		expectedCount    int
		expectedServices []string
	}{
		{
			name: "service from Service field",
			alert: InputAlert{
				Service: "payments",
				Labels:  map[string]string{},
			},
			expectedCount:    1,
			expectedServices: []string{"payments"},
		},
		{
			name: "service from labels",
			alert: InputAlert{
				Service: "",
				Labels: map[string]string{
					"service": "checkout",
				},
			},
			expectedCount:    1,
			expectedServices: []string{"checkout"},
		},
		{
			name: "multiple service candidates",
			alert: InputAlert{
				Service: "payments",
				Labels: map[string]string{
					"service":  "payments",
					"app":      "payments-api",
					"workload": "payments-worker",
				},
			},
			expectedCount:    3, // Service field + app + workload (service label deduped)
			expectedServices: []string{"payments", "payments-api", "payments-worker"},
		},
		{
			name: "service deduplication",
			alert: InputAlert{
				Service: "api",
				Labels: map[string]string{
					"service": "api",
					"app":     "api",
				},
			},
			expectedCount:    1, // Should dedupe
			expectedServices: []string{"api"},
		},
		{
			name: "no service specified",
			alert: InputAlert{
				Service: "",
				Labels:  map[string]string{},
			},
			expectedCount:    0,
			expectedServices: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orch := &Orchestrator{}
			services := orch.deriveServices(tc.alert)

			require.Equal(t, tc.expectedCount, len(services))
			for _, expected := range tc.expectedServices {
				require.Contains(t, services, expected)
			}
		})
	}
}

// TestOrchestratorDeriveKeywords validates keyword derivation
func TestOrchestratorDeriveKeywords(t *testing.T) {
	testCases := []struct {
		name             string
		alert            InputAlert
		expectedKeywords []string
		minCount         int
	}{
		{
			name: "keywords from alert fields",
			alert: InputAlert{
				Title:    "High CPU detected",
				Service:  "payments",
				Severity: "P1",
				Labels: map[string]string{
					"host": "node-1",
				},
			},
			expectedKeywords: []string{"High CPU detected", "payments", "P1", "host", "node-1"},
			minCount:         5,
		},
		{
			name: "keywords from annotations",
			alert: InputAlert{
				Title:    "Database timeout",
				Service:  "api",
				Severity: "P2",
				Annotations: map[string]string{
					"description": "Connection timeout to database",
					"runbook":     "Check database connectivity",
				},
			},
			expectedKeywords: []string{"Database timeout", "api", "P2", "Connection", "timeout", "database"},
			minCount:         6,
		},
		{
			name: "excludes service and app labels",
			alert: InputAlert{
				Title:   "Test alert",
				Service: "payments",
				Labels: map[string]string{
					"service": "payments",
					"app":     "payments-app",
					"region":  "us-east-1",
				},
			},
			expectedKeywords: []string{"Test alert", "payments", "region", "us-east-1"},
			minCount:         4,
		},
		{
			name: "keyword deduplication",
			alert: InputAlert{
				Title:    "CPU alert",
				Service:  "cpu",
				Severity: "P1",
				Labels: map[string]string{
					"severity": "P1",
					"type":     "cpu",
				},
			},
			minCount: 3, // cpu, P1, severity, type (deduped)
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orch := &Orchestrator{}
			keywords := orch.deriveKeywords(tc.alert)

			require.GreaterOrEqual(t, len(keywords), tc.minCount)
			for _, expected := range tc.expectedKeywords {
				found := false
				for _, kw := range keywords {
					if strings.Contains(kw, expected) || kw == expected {
						found = true
						break
					}
				}
				require.True(t, found, "expected keyword %q not found in %v", expected, keywords)
			}
		})
	}
}

// TestOrchestratorDeriveCauses validates cause derivation
func TestOrchestratorDeriveCauses(t *testing.T) {
	testCases := []struct {
		name           string
		metrics        []MetricFinding
		logs           []LogFinding
		keywords       []string
		expectedCauses []string
	}{
		{
			name: "CPU symptoms",
			metrics: []MetricFinding{
				{
					Symptoms: []string{"CPU usage above 90%"},
				},
			},
			logs:           []LogFinding{},
			keywords:       []string{},
			expectedCauses: []string{"CPU saturation"},
		},
		{
			name: "memory symptoms",
			metrics: []MetricFinding{
				{
					Symptoms: []string{"memory usage high"},
				},
			},
			logs:           []LogFinding{},
			keywords:       []string{},
			expectedCauses: []string{"memory pressure or leak"},
		},
		{
			name: "disk symptoms",
			metrics: []MetricFinding{
				{
					Symptoms: []string{"disk I/O high"},
				},
			},
			logs:           []LogFinding{},
			keywords:       []string{},
			expectedCauses: []string{"disk I/O contention"},
		},
		{
			name: "network symptoms",
			metrics: []MetricFinding{
				{
					Symptoms: []string{"network latency"},
				},
			},
			logs:           []LogFinding{},
			keywords:       []string{},
			expectedCauses: []string{"network congestion"},
		},
		{
			name:    "log timeout patterns",
			metrics: []MetricFinding{},
			logs: []LogFinding{
				{
					Matches: []LogMatch{
						{Example: "connection timeout"},
					},
				},
			},
			keywords:       []string{},
			expectedCauses: []string{"downstream timeout"},
		},
		{
			name:    "log OOM patterns",
			metrics: []MetricFinding{},
			logs: []LogFinding{
				{
					Matches: []LogMatch{
						{Example: "OOM killer killed process"},
					},
				},
			},
			keywords:       []string{},
			expectedCauses: []string{"out-of-memory termination"},
		},
		{
			name:    "log connection refused",
			metrics: []MetricFinding{},
			logs: []LogFinding{
				{
					Matches: []LogMatch{
						{Example: "connection refused"},
					},
				},
			},
			keywords:       []string{},
			expectedCauses: []string{"connection refused"},
		},
		{
			name:           "error code keywords",
			metrics:        []MetricFinding{},
			logs:           []LogFinding{},
			keywords:       []string{"500", "error", "503"},
			expectedCauses: []string{"error pattern: 500", "error pattern: error", "error pattern: 503"},
		},
		{
			name: "combined metrics and logs",
			metrics: []MetricFinding{
				{
					Symptoms: []string{"CPU usage above 90%"},
				},
			},
			logs: []LogFinding{
				{
					Matches: []LogMatch{
						{Example: "request timeout"},
					},
				},
			},
			keywords:       []string{},
			expectedCauses: []string{"CPU saturation", "downstream timeout"},
		},
		{
			name:           "no evidence",
			metrics:        []MetricFinding{},
			logs:           []LogFinding{},
			keywords:       []string{"normal", "operation"},
			expectedCauses: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orch := &Orchestrator{}
			causes := orch.deriveCauses(tc.metrics, tc.logs, tc.keywords)

			// Check for expected causes
			for _, expectedCause := range tc.expectedCauses {
				found := false
				for _, cause := range causes {
					if strings.Contains(cause, expectedCause) || cause == expectedCause {
						found = true
						break
					}
				}
				require.True(t, found, "expected cause %q not found in %v", expectedCause, causes)
			}
		})
	}
}

// TestOrchestratorBuildContext validates full context building
func TestOrchestratorBuildContext(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	// Add test data
	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "node-1",
		Hostname:    "node-1",
		Labels: []*telemetryv1.Label{
			{Key: "service", Value: "payments"},
		},
	}, now)

	store.StoreMetrics("node-1", []*telemetryv1.Metric{
		{Name: "cpu_usage", Value: 95.0},
		{Name: "memory_usage", Value: 85.0},
	}, now)

	store.StoreLogs("node-1", []*telemetryv1.LogFingerprint{
		{Fingerprint: "abc", Count: 10, Example: "connection timeout"},
	}, now)

	cfg := DefaultConfig()
	cfg.Kubernetes.Enabled = false

	orch, err := NewOrchestrator(cfg, store, zap.NewNop())
	require.NoError(t, err)

	alert := InputAlert{
		ID:       "alert-1",
		Title:    "High CPU in payments",
		Service:  "payments",
		Severity: "P1",
		StartsAt: now.Add(-5 * time.Minute),
		Labels: map[string]string{
			"service": "payments",
			"host":    "node-1",
		},
	}

	ctx, err := orch.BuildContext(context.Background(), alert, "inc-1")
	require.NoError(t, err)
	require.NotNil(t, ctx)

	// Verify context structure
	require.Equal(t, "inc-1", ctx.IncidentID)
	require.Equal(t, "alert-1", ctx.AlertID)
	require.Equal(t, "High CPU in payments", ctx.Alert.Title)
	require.NotEmpty(t, ctx.Services)
	require.NotEmpty(t, ctx.Keywords)
	require.NotNil(t, ctx.Window)
	require.NotNil(t, ctx.GeneratedAt)
}

// TestOrchestratorBuildContextWithMinimalData validates context with minimal data
func TestOrchestratorBuildContextWithMinimalData(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	// Add minimal data
	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "test-node",
		Hostname:    "test-node",
	}, now)

	orch, err := NewOrchestrator(DefaultConfig(), store, zap.NewNop())
	require.NoError(t, err)

	alert := InputAlert{
		ID:       "alert-1",
		Title:    "Test Alert",
		Service:  "test",
		Severity: "P1",
		StartsAt: now,
	}

	ctx, err := orch.BuildContext(context.Background(), alert, "inc-1")
	require.NoError(t, err)
	require.NotNil(t, ctx)

	// Should still have basic structure
	require.Equal(t, "inc-1", ctx.IncidentID)
	require.Equal(t, "alert-1", ctx.AlertID)
}

// TestOrchestratorBuildContextWindowCalculation validates window is correct
func TestOrchestratorBuildContextWindowCalculation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WindowBefore = 5 * time.Minute
	cfg.WindowAfter = 10 * time.Minute

	orch, err := NewOrchestrator(cfg, ingest.NewMemoryStore(), zap.NewNop())
	require.NoError(t, err)

	now := time.Now()
	alert := InputAlert{
		ID:       "alert-1",
		Title:    "Test",
		Service:  "test",
		Severity: "P1",
		StartsAt: now.Add(-10 * time.Minute),
		EndsAt:   now.Add(5 * time.Minute),
	}

	ctx, err := orch.BuildContext(context.Background(), alert, "inc-1")
	require.NoError(t, err)

	// Window should be: start - before, end + after
	expectedStart := alert.StartsAt.Add(-cfg.WindowBefore)
	expectedEnd := alert.EndsAt.Add(cfg.WindowAfter)

	require.True(t, expectedStart.Sub(ctx.Window.Start) < time.Second)
	require.True(t, expectedEnd.Sub(ctx.Window.End) < time.Second)
}

// TestOrchestratorCauseDeduplication validates causes are deduplicated
func TestOrchestratorCauseDeduplication(t *testing.T) {
	orch := &Orchestrator{}

	metrics := []MetricFinding{
		{Symptoms: []string{"CPU usage high", "CPU usage high", "CPU spike"}},
		{Symptoms: []string{"CPU usage high"}},
	}

	causes := orch.deriveCauses(metrics, []LogFinding{}, []string{})

	// Should not have duplicates
	require.NotEmpty(t, causes)

	seen := make(map[string]bool)
	for _, cause := range causes {
		require.False(t, seen[cause], "duplicate cause found: %s", cause)
		seen[cause] = true
	}
}

// TestOrchestratorContextWithAnnotations validates annotations are processed
func TestOrchestratorContextWithAnnotations(t *testing.T) {
	orch, err := NewOrchestrator(DefaultConfig(), ingest.NewMemoryStore(), zap.NewNop())
	require.NoError(t, err)

	alert := InputAlert{
		ID:       "alert-1",
		Title:    "Test Alert",
		Service:  "test",
		Severity: "P1",
		StartsAt: time.Now(),
		Annotations: map[string]string{
			"description": "This is a test alert",
			"runbook":     "https://docs.example.com/runbooks/test",
		},
	}

	ctx, err := orch.BuildContext(context.Background(), alert, "inc-1")
	require.NoError(t, err)

	// Keywords should include annotation text
	keywords := strings.Join(ctx.Keywords, " ")
	require.Contains(t, keywords, "test")
}

// TestOrchestratorResourceScope validates resource scope is built
func TestOrchestratorResourceScope(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now()

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: "node-1",
		Hostname:    "node-1",
	}, now)

	cfg := DefaultConfig()
	// Add static resource mapping
	cfg.ResourcePlatform.Static = []StaticServiceMapping{
		{
			Service: "payments",
			ResourceScope: []ResourceRef{
				{ID: "pod-1", Type: "pod", Name: "payments-abc"},
				{ID: "pod-2", Type: "pod", Name: "payments-def"},
			},
		},
	}

	orch, err := NewOrchestrator(cfg, store, zap.NewNop())
	require.NoError(t, err)

	alert := InputAlert{
		ID:       "alert-1",
		Title:    "Payments Issue",
		Service:  "payments",
		Severity: "P1",
		StartsAt: now,
	}

	ctx, err := orch.BuildContext(context.Background(), alert, "inc-1")
	require.NoError(t, err)

	// Should have resource scope
	require.NotEmpty(t, ctx.ResourceScope)
}

// TestOrchestratorSuspectedCauseSorting validates causes are sorted
func TestOrchestratorSuspectedCauseSorting(t *testing.T) {
	orch := &Orchestrator{}

	metrics := []MetricFinding{
		{Symptoms: []string{"memory high"}},
		{Symptoms: []string{"CPU usage high"}},
		{Symptoms: []string{"disk I/O high"}},
	}

	causes := orch.deriveCauses(metrics, []LogFinding{}, []string{})

	// Causes should be sorted alphabetically
	require.NotEmpty(t, causes)
	for i := 1; i < len(causes); i++ {
		require.True(t, causes[i-1] <= causes[i],
			"causes not sorted: %s should be <= %s", causes[i-1], causes[i])
	}
}
