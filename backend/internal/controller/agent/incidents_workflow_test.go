package agent

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/incidents"
	"github.com/stretchr/testify/require"
)

func TestIngestIncidentContextBuildsStructuredWorkflow(t *testing.T) {
	engine := &Engine{
		incidentContexts:    make(map[string]incidents.AggregatedContext),
		incidentAssessments: make(map[string]IncidentAssessment),
	}

	ctx := testIncidentContext("alert-workflow")
	assessment := engine.IngestIncidentContext(ctx)

	require.Equal(t, "alert-workflow", assessment.AlertID)
	require.Equal(t, "checkout", assessment.Service)
	require.NotEmpty(t, assessment.LikelyCauses)
	require.NotEmpty(t, assessment.Recommendations)
	require.NotEmpty(t, assessment.Workflow)
	require.Len(t, assessment.Workflow, 6)
	require.Equal(t, "incident_intake", assessment.Workflow[0].Name)
	require.Equal(t, "guarded_execution", assessment.Workflow[5].Name)
	require.NotEmpty(t, assessment.Signals)
	require.NotEmpty(t, assessment.Evidence)
	require.NotEmpty(t, assessment.NextActions)
	require.NotEmpty(t, assessment.Correlations)
	require.NotEmpty(t, assessment.Diagnosis.ProbableRootCause)
	require.Greater(t, assessment.Diagnosis.Confidence, 0.35)
	require.True(t, assessment.AutomationPlan.Enabled)
	require.NotEmpty(t, assessment.AutomationPlan.Actions)
	require.Equal(t, "deterministic-agent-workflow", assessment.AssessmentSource)
}

func TestExecuteIncidentActionRecordsOutcome(t *testing.T) {
	engine := &Engine{
		incidentContexts:    make(map[string]incidents.AggregatedContext),
		incidentAssessments: make(map[string]IncidentAssessment),
	}

	ctx := testIncidentContext("alert-exec")
	assessment := engine.IngestIncidentContext(ctx)
	require.NotEmpty(t, assessment.AutomationPlan.Actions)
	actionID := assessment.AutomationPlan.Actions[0].ID

	result, err := engine.ExecuteIncidentAction("alert-exec", actionID, IncidentActionExecuteRequest{})
	require.NoError(t, err)
	require.Equal(t, "alert-exec", result.AlertID)
	require.Equal(t, actionID, result.ActionID)
	require.Equal(t, "executed", result.Status)
	require.NotEmpty(t, result.AuditID)
	require.NotEmpty(t, result.Message)

	updated, ok := engine.IncidentAssessment("alert-exec")
	require.True(t, ok)
	found := false
	for _, action := range updated.AutomationPlan.Actions {
		if action.ID == actionID {
			found = true
			require.Equal(t, "executed", action.LastStatus)
			require.NotNil(t, action.LastExecutedAt)
			require.NotEmpty(t, action.LastMessage)
		}
	}
	require.True(t, found)
}

func TestIncidentActionAuditAndRollback(t *testing.T) {
	engine := &Engine{
		incidentContexts:     make(map[string]incidents.AggregatedContext),
		incidentAssessments:  make(map[string]IncidentAssessment),
		incidentActionAudits: make(map[string][]IncidentActionAuditRecord),
	}

	assessment := engine.IngestIncidentContext(testIncidentContext("alert-rollback"))
	require.NotEmpty(t, assessment.AutomationPlan.Actions)

	actionID := assessment.AutomationPlan.Actions[0].ID
	result, err := engine.ExecuteIncidentAction("alert-rollback", actionID, IncidentActionExecuteRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, result.RollbackID)

	audits := engine.IncidentActionAudits("alert-rollback", 10)
	require.NotEmpty(t, audits)
	require.Equal(t, result.AuditID, audits[0].AuditID)

	dryRun := true
	rollback, err := engine.RollbackIncidentAction("alert-rollback", actionID, IncidentActionRollbackRequest{
		DryRun:     &dryRun,
		RollbackID: result.RollbackID,
	})
	require.NoError(t, err)
	require.Equal(t, "dry_run", rollback.Status)
	require.Equal(t, result.RollbackID, rollback.RollbackID)
	require.NotEmpty(t, rollback.AuditID)
}

func TestExecuteIncidentActionApprovalGuard(t *testing.T) {
	engine := &Engine{
		incidentContexts:    make(map[string]incidents.AggregatedContext),
		incidentAssessments: make(map[string]IncidentAssessment),
	}
	assessment := engine.IngestIncidentContext(testIncidentContext("alert-approval"))

	unsafeActionID := ""
	for _, action := range assessment.AutomationPlan.Actions {
		if action.RequiresApproval {
			unsafeActionID = action.ID
			break
		}
	}
	require.NotEmpty(t, unsafeActionID)

	dryRunFalse := false
	_, err := engine.ExecuteIncidentAction("alert-approval", unsafeActionID, IncidentActionExecuteRequest{
		DryRun: &dryRunFalse,
	})
	require.ErrorIs(t, err, ErrIncidentActionApprovalRequired)

	t.Setenv("SRE_AGENT_APPROVAL_TOKEN", "secret-token")
	_, err = engine.ExecuteIncidentAction("alert-approval", unsafeActionID, IncidentActionExecuteRequest{
		DryRun:        &dryRunFalse,
		ApprovalToken: "bad-token",
	})
	require.ErrorIs(t, err, ErrIncidentActionApprovalInvalid)

	result, err := engine.ExecuteIncidentAction("alert-approval", unsafeActionID, IncidentActionExecuteRequest{
		DryRun:        &dryRunFalse,
		ApprovalToken: "secret-token",
	})
	require.NoError(t, err)
	require.Equal(t, "blocked", result.Status)
	require.False(t, result.DryRun)
}

func testIncidentContext(alertID string) incidents.AggregatedContext {
	start := time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC)
	return incidents.AggregatedContext{
		IncidentID: alertID + "-incident",
		AlertID:    alertID,
		Alert: incidents.InputAlert{
			ID:       alertID,
			Title:    "Checkout API high latency",
			Service:  "checkout",
			Severity: "critical",
			StartsAt: start,
			Labels: map[string]string{
				"service": "checkout",
				"commit":  "abc123",
			},
			Annotations: map[string]string{
				"summary": "latency p99 breached",
				"runbook": "docs/runbooks/checkout-latency.md",
			},
		},
		Window: incidents.TimeWindow{
			Start: start.Add(-10 * time.Minute),
			End:   start.Add(20 * time.Minute),
		},
		Services: []incidents.ServiceImpact{
			{Service: "checkout"},
		},
		ResourceScope: []incidents.ResourceRef{
			{ID: "pod-1", Type: "pod", Name: "checkout-api-7d4f7f"},
		},
		Metrics: []incidents.MetricFinding{
			{
				Scope:    "node-a",
				Query:    "cpu_usage",
				Symptoms: []string{"CPU saturation", "request queue growth"},
			},
		},
		Logs: []incidents.LogFinding{
			{
				Scope: "checkout",
				Query: "error OR timeout",
				Matches: []incidents.LogMatch{
					{
						Fingerprint: "timeout-1",
						Count:       37,
						Example:     "downstream timeout while calling payment gateway",
					},
				},
			},
		},
		Kubernetes: &incidents.KubernetesFinding{
			Cluster:   "prod-a",
			Namespace: "default",
			Nodes:     []string{"node-a"},
			Workloads: map[string]string{"checkout-api": "degraded"},
		},
		GeneratedAt: start.Add(2 * time.Minute),
	}
}
