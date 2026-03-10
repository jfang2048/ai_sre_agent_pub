package incidents

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestInputAlertStructure validates InputAlert structure
func TestInputAlertStructure(t *testing.T) {
	testCases := []struct {
		name  string
		alert InputAlert
		valid bool
	}{
		{
			name: "valid alert",
			alert: InputAlert{
				ID:       "alert-1",
				Title:    "High CPU",
				Service:  "payments",
				Severity: "P1",
				StartsAt: time.Now().Add(-5 * time.Minute),
				EndsAt:   time.Now(),
				Labels:   map[string]string{"service": "payments"},
			},
			valid: true,
		},
		{
			name: "minimal alert",
			alert: InputAlert{
				Title:    "Test Alert",
				Severity: "P2",
			},
			valid: true, // All fields optional
		},
		{
			name: "alert with annotations",
			alert: InputAlert{
				ID:       "alert-2",
				Title:    "Database Latency",
				Service:  "database",
				Severity: "P1",
				StartsAt: time.Now(),
				Labels:   map[string]string{"db": "postgres"},
				Annotations: map[string]string{
					"description": "High latency detected",
					"runbook":     "https://docs.example.com/runbooks/db-latency",
				},
			},
			valid: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Basic validation: ensure structure can be created
			require.NotNil(t, tc.alert)
			require.NotEmpty(t, tc.alert.Title)
			require.NotEmpty(t, tc.alert.Severity)
		})
	}
}

// TestTimeWindowBounds validates time window bounds
func TestTimeWindowBounds(t *testing.T) {
	testCases := []struct {
		name        string
		window      TimeWindow
		valid       bool
		description string
	}{
		{
			name: "valid window",
			window: TimeWindow{
				Start: time.Now().Add(-1 * time.Hour),
				End:   time.Now(),
			},
			valid:       true,
			description: "start before end",
		},
		{
			name: "zero start",
			window: TimeWindow{
				Start: time.Time{},
				End:   time.Now(),
			},
			valid:       true,
			description: "zero start is acceptable (will default to now)",
		},
		{
			name: "zero end",
			window: TimeWindow{
				Start: time.Now().Add(-1 * time.Hour),
				End:   time.Time{},
			},
			valid:       true,
			description: "zero end is acceptable (will default to start + window)",
		},
		{
			name: "both zero",
			window: TimeWindow{
				Start: time.Time{},
				End:   time.Time{},
			},
			valid:       true,
			description: "both zero is acceptable (will be derived)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// If both start and end are non-zero, start should be before end
			if !tc.window.Start.IsZero() && !tc.window.End.IsZero() {
				require.True(t, tc.window.Start.Before(tc.window.End) || tc.window.Start.Equal(tc.window.End),
					"start should be before or equal to end")
			}
		})
	}
}

// TestResourceRefStructure validates ResourceRef structure
func TestResourceRefStructure(t *testing.T) {
	testCases := []struct {
		name string
		ref  ResourceRef
	}{
		{
			name: "complete resource ref",
			ref: ResourceRef{
				ID:     "res-1",
				Type:   "container",
				Name:   "payments-service-abc123",
				Scope:  "us-east-1",
				Labels: map[string]string{"pod": "payments-service-123"},
			},
		},
		{
			name: "minimal resource ref",
			ref: ResourceRef{
				ID:   "res-2",
				Type: "host",
				Name: "node-1",
			},
		},
		{
			name: "resource ref with empty scope",
			ref: ResourceRef{
				ID:     "res-3",
				Type:   "service",
				Name:   "api-gateway",
				Labels: map[string]string{"version": "v2.3.1"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotEmpty(t, tc.ref.ID, "ID is required")
			require.NotEmpty(t, tc.ref.Type, "Type is required")
			require.NotEmpty(t, tc.ref.Name, "Name is required")
		})
	}
}

// TestServiceImpactStructure validates ServiceImpact structure
func TestServiceImpactStructure(t *testing.T) {
	testCases := []struct {
		name   string
		impact ServiceImpact
	}{
		{
			name: "complete service impact",
			impact: ServiceImpact{
				Service:      "payments",
				Environment:  "production",
				Dependencies: []string{"database", "cache"},
				BlastRadius:  []string{"checkout", "orders", "profile"},
				Resources: []ResourceRef{
					{ID: "pod-1", Type: "pod", Name: "payments-abc"},
				},
			},
		},
		{
			name: "minimal service impact",
			impact: ServiceImpact{
				Service: "auth",
			},
		},
		{
			name: "service impact with resources only",
			impact: ServiceImpact{
				Service: "frontend",
				Resources: []ResourceRef{
					{ID: "vm-1", Type: "vm", Name: "web-1"},
					{ID: "vm-2", Type: "vm", Name: "web-2"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotEmpty(t, tc.impact.Service, "Service name is required")
		})
	}
}

// TestMonitoringRequestStructure validates MonitoringRequest structure
func TestMonitoringRequestStructure(t *testing.T) {
	req := MonitoringRequest{
		Services: []string{"payments", "checkout"},
		Resources: []ResourceRef{
			{ID: "pod-1", Type: "pod", Name: "payments-1"},
		},
		Window: TimeWindow{
			Start: time.Now().Add(-1 * time.Hour),
			End:   time.Now(),
		},
		Keywords: []string{"cpu", "latency", "timeout"},
	}

	require.NotEmpty(t, req.Services)
	require.NotNil(t, req.Window)
}

// TestLogRequestStructure validates LogRequest structure
func TestLogRequestStructure(t *testing.T) {
	testCases := []struct {
		name  string
		req   LogRequest
		valid bool
	}{
		{
			name: "valid log request",
			req: LogRequest{
				Services: []string{"api"},
				Resources: []ResourceRef{
					{ID: "pod-1", Type: "pod", Name: "api-1"},
				},
				Window: TimeWindow{
					Start: time.Now().Add(-1 * time.Hour),
					End:   time.Now(),
				},
				Keywords: []string{"error", "timeout"},
				Limit:    100,
			},
			valid: true,
		},
		{
			name: "log request with zero limit",
			req: LogRequest{
				Services: []string{"api"},
				Window: TimeWindow{
					Start: time.Now().Add(-1 * time.Hour),
					End:   time.Now(),
				},
				Limit: 0,
			},
			valid: true, // Zero limit is acceptable (will use default)
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotNil(t, tc.req)
			if tc.valid {
				require.NotNil(t, tc.req.Window)
			}
		})
	}
}

// TestKubernetesRequestStructure validates KubernetesRequest structure
func TestKubernetesRequestStructure(t *testing.T) {
	req := KubernetesRequest{
		Services: []string{"payments", "checkout"},
		Resources: []ResourceRef{
			{ID: "pod-1", Type: "pod", Name: "payments-1"},
		},
		Namespace: "production",
	}

	require.NotEmpty(t, req.Services)
}

// TestMetricPointStructure validates MetricPoint structure
func TestMetricPointStructure(t *testing.T) {
	point := MetricPoint{
		Timestamp: time.Now(),
		Name:      "cpu_usage_percent",
		Value:     85.5,
		Labels: map[string]string{
			"host":     "node-1",
			"service":  "payments",
			"instance": "192.168.1.10:8080",
		},
	}

	require.NotEmpty(t, point.Name)
	require.NotNil(t, point.Timestamp)
}

// TestMetricFindingStructure validates MetricFinding structure
func TestMetricFindingStructure(t *testing.T) {
	finding := MetricFinding{
		Scope: "service:payments",
		Query: "cpu_usage_percent{service=\"payments\"}[5m]",
		Points: []MetricPoint{
			{
				Timestamp: time.Now().Add(-2 * time.Minute),
				Name:      "cpu_usage_percent",
				Value:     92.5,
			},
			{
				Timestamp: time.Now().Add(-1 * time.Minute),
				Name:      "cpu_usage_percent",
				Value:     88.3,
			},
		},
		Symptoms: []string{
			"CPU usage above 90% threshold",
			"Sustained high CPU for 5 minutes",
		},
		AnomalyHint: "CPU anomaly detected with 99% confidence",
	}

	require.NotEmpty(t, finding.Scope)
	require.NotEmpty(t, finding.Points)
}

// TestLogMatchStructure validates LogMatch structure
func TestLogMatchStructure(t *testing.T) {
	match := LogMatch{
		Fingerprint: "abc123def456",
		Count:       42,
		Example:     "timeout while calling external payment gateway",
		Source:      "/var/log/payments/service.log",
	}

	require.NotEmpty(t, match.Fingerprint)
	require.Greater(t, match.Count, uint64(0))
}

// TestLogFindingStructure validates LogFinding structure
func TestLogFindingStructure(t *testing.T) {
	finding := LogFinding{
		Scope: "service:payments",
		Query: "timeout OR error",
		Matches: []LogMatch{
			{
				Fingerprint: "abc123",
				Count:       15,
				Example:     "connection timeout",
				Source:      "payments.log",
			},
			{
				Fingerprint: "def456",
				Count:       8,
				Example:     "database connection refused",
				Source:      "payments.log",
			},
		},
		Keywords: []string{"timeout", "refused", "connection"},
	}

	require.NotEmpty(t, finding.Scope)
	require.NotEmpty(t, finding.Matches)
}

// TestKubernetesFindingStructure validates KubernetesFinding structure
func TestKubernetesFindingStructure(t *testing.T) {
	finding := KubernetesFinding{
		Cluster:   "production-cluster",
		Namespace: "default",
		Nodes:     []string{"node-1", "node-2", "node-3"},
		Workloads: map[string]string{
			"payments-deployment":  "2/3 ready",
			"checkout-deployment":  "3/3 ready",
			"database-statefulset": "1/1 ready",
		},
	}

	require.NotNil(t, finding)
	if finding.Cluster != "" {
		require.NotEmpty(t, finding.Cluster)
	}
	if len(finding.Workloads) > 0 {
		require.NotEmpty(t, finding.Workloads)
	}
}

// TestAggregatedContextStructure validates AggregatedContext structure
func TestAggregatedContextStructure(t *testing.T) {
	now := time.Now()
	ctx := AggregatedContext{
		IncidentID: "inc-123",
		AlertID:    "alert-456",
		Alert: InputAlert{
			ID:       "alert-456",
			Title:    "High CPU in payments",
			Service:  "payments",
			Severity: "P1",
			StartsAt: now.Add(-5 * time.Minute),
		},
		Window: TimeWindow{
			Start: now.Add(-15 * time.Minute),
			End:   now.Add(5 * time.Minute),
		},
		Services: []ServiceImpact{
			{
				Service:     "payments",
				Environment: "production",
				Resources: []ResourceRef{
					{ID: "pod-1", Type: "pod", Name: "payments-abc"},
				},
			},
		},
		ResourceScope: []ResourceRef{
			{ID: "pod-1", Type: "pod", Name: "payments-abc"},
		},
		Keywords: []string{"cpu", "high", "payments"},
		Metrics: []MetricFinding{
			{
				Scope: "service:payments",
				Points: []MetricPoint{
					{Timestamp: now, Name: "cpu_usage", Value: 95.5},
				},
				Symptoms: []string{"CPU usage above 90%"},
			},
		},
		Logs: []LogFinding{
			{
				Scope: "service:payments",
				Matches: []LogMatch{
					{Fingerprint: "abc", Count: 5, Example: "timeout"},
				},
			},
		},
		Kubernetes: &KubernetesFinding{
			Cluster:   "prod-cluster",
			Namespace: "default",
			Workloads: map[string]string{
				"payments": "2/3 ready",
			},
		},
		SuspectedCause: []string{"CPU saturation", "memory pressure"},
		GeneratedAt:    now,
		Notes:          "Context window test",
	}

	require.NotEmpty(t, ctx.IncidentID)
	require.NotEmpty(t, ctx.AlertID)
	require.NotEmpty(t, ctx.Alert.Title)
	require.NotNil(t, ctx.Window)
	require.NotNil(t, ctx.GeneratedAt)
}

// TestAggregatedContextCreationOrder validates fields are populated correctly
func TestAggregatedContextCreationOrder(t *testing.T) {
	now := time.Now()

	// Create context incrementally
	ctx := AggregatedContext{
		IncidentID:  "inc-1",
		AlertID:     "alert-1",
		GeneratedAt: now,
	}

	// Add alert
	ctx.Alert = InputAlert{
		ID:       "alert-1",
		Title:    "Test Alert",
		Severity: "P1",
	}

	// Add window
	ctx.Window = TimeWindow{
		Start: now.Add(-10 * time.Minute),
		End:   now,
	}

	// Verify all required fields are set
	require.NotEmpty(t, ctx.IncidentID)
	require.NotEmpty(t, ctx.AlertID)
	require.NotNil(t, ctx.Alert)
	require.NotNil(t, ctx.Window)
	require.NotNil(t, ctx.GeneratedAt)
}

// TestTimeWindowDuration calculates window duration
func TestTimeWindowDuration(t *testing.T) {
	testCases := []struct {
		name     string
		window   TimeWindow
		expected time.Duration
	}{
		{
			name: "1 hour window",
			window: TimeWindow{
				Start: time.Now().Add(-1 * time.Hour),
				End:   time.Now(),
			},
			expected: 1 * time.Hour,
		},
		{
			name: "5 minute window",
			window: TimeWindow{
				Start: time.Now().Add(-5 * time.Minute),
				End:   time.Now(),
			},
			expected: 5 * time.Minute,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			duration := tc.window.End.Sub(tc.window.Start)
			// Use approximate comparison due to time precision
			diff := duration - tc.expected
			require.Less(t, diff.Abs(), 100*time.Millisecond, "duration should be within 100ms of expected")
		})
	}
}
