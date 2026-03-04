package securityaudit

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestEvaluatorFindingsIncludesSecuritySignals(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	collectorID := "collector-sec-a"
	base := time.Now().Add(-20 * time.Minute).UTC()

	store.UpsertCollector(&telemetryv1.CollectorInfo{
		CollectorId: collectorID,
		Hostname:    "sec-a",
	}, base)

	store.StoreMetrics(collectorID, []*telemetryv1.Metric{
		{Name: "node_filesystem_world_writable_count", Value: 3, TimestampUnixNano: base.UnixNano()},
		{Name: "node_security_unexpected_listening_ports_count", Value: 2, TimestampUnixNano: base.UnixNano()},
		{Name: "node_security_firewall_disabled", Value: 1, TimestampUnixNano: base.UnixNano()},
		{Name: "node_security_sysctl_risky_count", Value: 2, TimestampUnixNano: base.UnixNano()},
		{Name: "node_security_package_vulnerability_count", Value: 7, TimestampUnixNano: base.UnixNano()},
		{Name: "node_softnet_dropped_per_second", Value: 12, TimestampUnixNano: base.UnixNano()},
	}, base)

	store.StoreLogs(collectorID, []*telemetryv1.LogFingerprint{
		{Fingerprint: "perm", Count: 3, Example: "warning weak permission chmod 777 /var/cache/app"},
		{Fingerprint: "auth", Count: 2, Example: "failed password for root from 10.2.3.4"},
	}, base)

	index.AddBatch([]logindex.RawEvent{{
		Timestamp:   base,
		CollectorID: collectorID,
		Hostname:    "sec-a",
		Service:     "checkout",
		Process:     "api",
		PID:         "100",
		Level:       "warn",
		Source:      "app",
		Message:     "security warning world-writable runtime path",
		Count:       1,
	}})

	evaluator := NewEvaluator(store, index)
	findings := evaluator.Findings(Options{CollectorID: collectorID, Window: time.Hour, Limit: 50})
	require.NotEmpty(t, findings)

	var sawCritical bool
	var sawFilesystem bool
	var sawNetwork bool
	var sawLogs bool
	for _, finding := range findings {
		if finding.Severity == SeverityCritical {
			sawCritical = true
		}
		if finding.Category == "filesystem_permissions" {
			sawFilesystem = true
		}
		if finding.Category == "network_exposure" || finding.Category == "network_posture" {
			sawNetwork = true
		}
		if finding.Category == "log_security" {
			sawLogs = true
		}
	}
	// Firewall disabled should be critical.
	require.True(t, sawCritical)
	require.True(t, sawFilesystem)
	require.True(t, sawNetwork)
	require.True(t, sawLogs)
}

func TestDashboardBuildsTrendSeriesFromHistory(t *testing.T) {
	store := ingest.NewMemoryStore()
	evaluator := NewEvaluator(store, nil)
	collectorID := "collector-sec-trend"
	base := time.Now().Add(-40 * time.Minute).UTC()

	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: "sec-trend"}, base)
	for i := 0; i < 15; i++ {
		ts := base.Add(time.Duration(i) * 2 * time.Minute)
		worldWritable := float64(i / 5)
		firewallDisabled := 0.0
		if i >= 10 {
			firewallDisabled = 1
		}
		store.StoreMetrics(collectorID, []*telemetryv1.Metric{
			{Name: "node_security_world_writable_sensitive_paths", Value: worldWritable, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_security_firewall_disabled", Value: firewallDisabled, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_security_sysctl_risky_count", Value: float64(i % 3), TimestampUnixNano: ts.UnixNano()},
		}, ts)
	}

	dashboard := evaluator.Dashboard(Options{CollectorID: collectorID, Window: time.Hour, Limit: 100})
	require.NotEmpty(t, dashboard.Trends)
	require.NotEmpty(t, dashboard.Findings)
	require.Greater(t, dashboard.Summary.Critical+dashboard.Summary.High+dashboard.Summary.Medium+dashboard.Summary.Low, 0)

	last := dashboard.Trends[len(dashboard.Trends)-1]
	require.GreaterOrEqual(t, last.Critical, 1)
	require.GreaterOrEqual(t, last.Total, last.Critical)
}
