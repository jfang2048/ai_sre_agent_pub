package agent

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
)

func TestSecurityCheckToolUsesCollectorSecurityFindings(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	collectorID := "collector-agent-security"
	now := time.Now().UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: "agent-sec"}, now)
	store.StoreMetrics(collectorID, []*telemetryv1.Metric{{
		Name:              "node_security_finding",
		Value:             0.93,
		TimestampUnixNano: now.UnixNano(),
		Labels: []*telemetryv1.Label{
			{Key: "finding_id", Value: "sf-agent"},
			{Key: "category", Value: "privilege_escalation"},
			{Key: "severity", Value: "critical"},
			{Key: "scope", Value: "runtime"},
			{Key: "summary", Value: "Privilege escalation patterns were observed in live processes"},
			{Key: "evidence", Value: "pid=99 uid=1000 euid=0"},
		},
	}}, now)

	tool := &securityCheckTool{store: store, index: index}
	result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: collectorID, Window: time.Hour, Limit: 10})
	require.NoError(t, err)
	data, ok := result.Data.(securityToolData)
	require.True(t, ok)
	require.NotEmpty(t, data.Findings)
	require.Contains(t, data.Categories, "privilege_escalation")
	require.Contains(t, data.FindingIDs, "sf-agent")
	require.Len(t, data.StructuredFindings, 1)
	require.Equal(t, "sf-agent", data.StructuredFindings[0].FindingID)
	require.Equal(t, "privilege_escalation", data.StructuredFindings[0].Category)
	require.Equal(t, "critical", data.StructuredFindings[0].Severity)
}

func TestSecurityCheckToolKeepsCategoryHintsSeparated(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	collectorID := "collector-agent-security-hints"
	now := time.Now().UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: "agent-sec-hints"}, now)
	store.StoreMetrics(collectorID, []*telemetryv1.Metric{
		{
			Name:              "node_security_finding",
			Value:             0.88,
			TimestampUnixNano: now.UnixNano(),
			Labels: []*telemetryv1.Label{
				{Key: "finding_id", Value: "sf-network"},
				{Key: "category", Value: "network"},
				{Key: "severity", Value: "high"},
				{Key: "scope", Value: "runtime"},
				{Key: "summary", Value: "Unexpected outbound connection to 198.51.100.10:4444"},
				{Key: "evidence", Value: "dst=198.51.100.10:4444 pid=4321"},
			},
		},
		{
			Name:              "node_security_finding",
			Value:             0.81,
			TimestampUnixNano: now.UnixNano(),
			Labels: []*telemetryv1.Label{
				{Key: "finding_id", Value: "sf-filesystem"},
				{Key: "category", Value: "filesystem_permission"},
				{Key: "severity", Value: "high"},
				{Key: "scope", Value: "filesystem"},
				{Key: "summary", Value: "World-writable service directory under /etc/systemd/system"},
				{Key: "evidence", Value: "path=/etc/systemd/system mode=0777"},
			},
		},
	}, now)

	tool := &securityCheckTool{store: store, index: index}
	result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: collectorID, Window: time.Hour, Limit: 10})
	require.NoError(t, err)

	data, ok := result.Data.(securityToolData)
	require.True(t, ok)
	require.Contains(t, data.SuspiciousPortCandidates, "Unexpected outbound connection to 198.51.100.10:4444")
	require.Contains(t, data.SuspiciousPortCandidates, "dst=198.51.100.10:4444 pid=4321")
	require.NotContains(t, data.SuspiciousPortCandidates, "World-writable service directory under /etc/systemd/system")
	require.NotContains(t, data.SuspiciousPortCandidates, "path=/etc/systemd/system mode=0777")
	require.Contains(t, data.WeakPermissionHints, "World-writable service directory under /etc/systemd/system")
	require.Contains(t, data.WeakPermissionHints, "path=/etc/systemd/system mode=0777")
	require.NotContains(t, data.WeakPermissionHints, "Unexpected outbound connection to 198.51.100.10:4444")
	require.NotContains(t, data.WeakPermissionHints, "dst=198.51.100.10:4444 pid=4321")
}
