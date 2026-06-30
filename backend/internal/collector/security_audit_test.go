package collector

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe"
	ebpfcore "github.com/jfang2048/ai_sre_agent_pub/internal/collector/probe/ebpf"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCollectorSecurityAuditorFindingsFromEBPFEvents(t *testing.T) {
	auditor := newCollectorSecurityAuditor(DefaultConfig().Security, zap.NewNop())
	now := time.Now().UTC()
	findings := auditor.findingsFromEBPFEvents(now, []probe.EBPFEvent{
		{EvidenceID: "ev-1", Timestamp: now, Type: "execve", Path: "/tmp/runner.sh", PID: 123, Comm: "runner"},
		{EvidenceID: "ev-2", Timestamp: now, Type: "privilege_escalation", PID: 123, Comm: "runner", Description: "uid 1000 -> 0"},
		{EvidenceID: "ev-3", Timestamp: now, Type: "connect", PID: 123, Comm: "curl", RemoteIP: "8.8.8.8"},
	}, map[int]securityProcessResource{123: {CPUPercent: 82, RSSBytes: 2 << 30}}, nil)

	require.NotEmpty(t, findings)
	categories := make([]string, 0, len(findings))
	for _, finding := range findings {
		categories = append(categories, finding.Category)
	}
	require.Contains(t, categories, "execution_from_suspicious_path")
	require.Contains(t, categories, "privilege_escalation")
	require.Contains(t, categories, "unexpected_outbound")
}

func TestCollectorSecurityAuditorProcessProfileDrift(t *testing.T) {
	auditor := newCollectorSecurityAuditor(DefaultConfig().Security, zap.NewNop())
	now := time.Now().UTC()
	baseline := []ebpfcore.ProcessStatsSnapshot{{PID: 99, Comm: "python", ConnectCalls: 3, OpenCalls: 20, ExecCalls: 1, ForkCalls: 1}}
	for i := 0; i < auditor.cfg.BaselineWarmupSamples; i++ {
		require.Empty(t, auditor.findingsFromProcessProfiles(now, baseline, nil, nil))
	}
	findings := auditor.findingsFromProcessProfiles(now, []ebpfcore.ProcessStatsSnapshot{{
		PID:          99,
		Comm:         "python",
		ConnectCalls: 80,
		OpenCalls:    240,
		ExecCalls:    12,
		ForkCalls:    20,
	}}, map[int]securityProcessResource{99: {CPUPercent: 91}}, nil)

	require.Len(t, findings, 1)
	require.Equal(t, "process_behavior_profile", findings[0].Category)
	require.NotEmpty(t, findings[0].Evidence)
}

func TestFilterMetricsByPrefixRemovesLegacySecuritySeries(t *testing.T) {
	metrics := []*telemetryv1.Metric{
		{Name: "node_security_world_writable_sensitive_paths"},
		{Name: "node_cpu_usage_percent"},
		{Name: "node_security_finding"},
	}
	filtered := filterMetricsByPrefix(metrics, "node_security_")
	require.Len(t, filtered, 1)
	require.Equal(t, "node_cpu_usage_percent", filtered[0].Name)
}
