package securityaudit

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuditSequenceAndHistoricalBaseline(t *testing.T) {
	now := time.Date(2026, 6, 20, 11, 30, 0, 0, time.UTC)
	day := now.Truncate(24 * time.Hour)
	workspace, err := OpenAuditWorkspace(filepath.Join(t.TempDir(), "audit.json"))
	require.NoError(t, err)
	records := []AuditRecord{}
	add := func(asset, actor, rule string, at time.Time) string {
		id := fmt.Sprintf("record-%d", len(records))
		records = append(records, AuditRecord{
			ID: id, Asset: asset, Actor: actor, Rule: rule, Source: "kubernetes", ObservedAt: at,
		})
		return id
	}
	for hour := range 8 {
		for minute := range 10 {
			add("asset", "actor", "api_request", day.Add(time.Hour*time.Duration(hour)+time.Minute*time.Duration(minute)))
		}
	}
	spike := day.Add(9 * time.Hour)
	for minute := range 10 {
		rule := "api_request"
		if minute < 5 {
			rule = "authorization_denied"
		}
		add("asset", "actor", rule, spike.Add(time.Minute*time.Duration(minute)))
	}
	add("asset", "different-actor", "secret_access", spike.Add(5*time.Minute))
	add("asset", "actor", "secret_access", spike.Add(4*time.Minute))
	success := add("asset", "actor", "rbac_change", spike.Add(10*time.Minute))
	add("other-asset", "actor", "secret_access", spike.Add(11*time.Minute))
	_, err = workspace.Import(records, now)
	require.NoError(t, err)

	report := workspace.Report("", 12*time.Hour, now)
	require.Len(t, report.Correlations, 1)
	require.Equal(t, ruleDeniedThenSensitive, report.Correlations[0].Rule)
	require.Len(t, report.Correlations[0].EvidenceIDs, 6)
	require.Equal(t, success, report.Correlations[0].EvidenceIDs[5])
	require.Len(t, report.Anomalies, 1)
	require.Equal(t, 8, report.Anomalies[0].Samples)
	require.Equal(t, 0.0, report.Anomalies[0].Baseline)
	require.InDelta(t, 5.0/13.0, report.Anomalies[0].Observed, 0.001)
	require.Empty(t, workspace.Report("asset", 2*time.Hour, now).Anomalies)
}

type auditModelFunc func(context.Context, string) (string, error)

func (f auditModelFunc) CompletePrompt(ctx context.Context, prompt string) (string, error) {
	return f(ctx, prompt)
}

func TestAuditModelSelectsOnlyKnownEvidence(t *testing.T) {
	report := AuditReport{Count: 2, Records: []AuditRecord{
		{ID: "private-id-1", Rule: "external-text", Asset: "private-asset", Source: "sarif", Severity: "high"},
		{ID: "private-id-2", Actor: "private-actor", Source: "sarif", Severity: "high"},
	}}
	for _, test := range []struct{ response, mode, id string }{
		{`{"index":2}`, "llm", "private-id-2"},
		{`{"index":3}`, "rules", "private-id-1"},
		{`{"index":2,"summary":"untrusted"}`, "rules", "private-id-1"},
	} {
		model := auditModelFunc(func(_ context.Context, prompt string) (string, error) {
			for _, private := range []string{"private-id", "private-asset", "private-actor", "external-text"} {
				require.NotContains(t, prompt, private)
			}
			return test.response, nil
		})
		result := ExplainAudit(context.Background(), report, model)
		require.Equal(t, test.mode, result.Mode)
		require.Equal(t, []string{test.id}, result.EvidenceIDs)
		require.NotContains(t, result.Summary, "untrusted")
	}
}
