package ingest

import (
	"fmt"
	"testing"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestStoreMetricsCapturesSecurityFinding(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	collectorID := "collector-security-store"
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: "sec-store"}, now)
	store.StoreMetrics(collectorID, []*telemetryv1.Metric{{
		Name:              "node_security_finding",
		Value:             0.91,
		TimestampUnixNano: now.UnixNano(),
		Labels: []*telemetryv1.Label{
			{Key: "finding_id", Value: "sf-test"},
			{Key: "category", Value: "unexpected_outbound"},
			{Key: "severity", Value: "high"},
			{Key: "scope", Value: "runtime"},
			{Key: "summary", Value: "Unexpected outbound connections escaped the baseline"},
			{Key: "evidence", Value: "remote=8.8.8.8:443 || process=curl"},
			{Key: "next_step", Value: "Validate egress policy"},
			{Key: "source", Value: "collector_security_audit"},
		},
	}}, now)

	node := store.Node(collectorID)
	require.NotNil(t, node)
	require.Len(t, node.SecurityFindings, 1)
	require.Equal(t, "sf-test", node.SecurityFindings[0].FindingID)
	require.Equal(t, "unexpected_outbound", node.SecurityFindings[0].Category)
	require.ElementsMatch(t, []string{"remote=8.8.8.8:443", "process=curl"}, node.SecurityFindings[0].Evidence)
	require.False(t, node.SecurityFindings[0].SourceTimestampKnown, "batch receipt time is not a source event timestamp")
}

func TestRuntimeSecuritySourceTimestampProvenance(t *testing.T) {
	now := time.Now().UTC()
	sourceAt := now.Add(-time.Minute)
	store := NewMemoryStore()
	collectorID := "collector-source-times"
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID}, now)
	store.StoreMetrics(collectorID, []*telemetryv1.Metric{
		{
			Name: "node_security_finding", Value: 0.8, TimestampUnixNano: now.UnixNano(),
			Labels: []*telemetryv1.Label{{Key: "finding_id", Value: "sf-with-source"}, {Key: "ts_unix_nano", Value: fmt.Sprint(sourceAt.UnixNano())}},
		},
		{
			Name: "node_ebpf_runtime_event", Value: 0.8, TimestampUnixNano: now.UnixNano(),
			Labels: []*telemetryv1.Label{{Key: "evidence_id", Value: "ev-with-source"}, {Key: "type", Value: "execve"}, {Key: "ts_unix_nano", Value: fmt.Sprint(sourceAt.UnixNano())}},
		},
		{
			Name: "node_ebpf_runtime_event", Value: 0.8, TimestampUnixNano: now.UnixNano(),
			Labels: []*telemetryv1.Label{{Key: "evidence_id", Value: "ev-receipt-only"}, {Key: "type", Value: "execve"}},
		},
	}, now)

	node := store.Node(collectorID)
	require.NotNil(t, node)
	require.Len(t, node.SecurityFindings, 1)
	require.True(t, node.SecurityFindings[0].SourceTimestampKnown)
	require.Equal(t, sourceAt, node.SecurityFindings[0].Timestamp)
	require.Len(t, node.RuntimeSecurityEvents, 2)
	require.True(t, node.RuntimeSecurityEvents[0].SourceTimestampKnown)
	require.Equal(t, sourceAt, node.RuntimeSecurityEvents[0].Timestamp)
	require.False(t, node.RuntimeSecurityEvents[1].SourceTimestampKnown)
}
