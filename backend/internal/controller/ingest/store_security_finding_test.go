package ingest

import (
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
}
