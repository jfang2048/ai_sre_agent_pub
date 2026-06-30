package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildRemediationEvidenceKeepsProposalOnlyStepsNonExecuted(t *testing.T) {
	state := &workflowState{
		workflowID: "wf-evidence-proposal",
		now:        time.Now().UTC(),
		planSteps: []AgentPlanStep{{
			ID:     "guarded-action-1",
			Tool:   ToolRemediation,
			Status: "proposal_only",
			Query: map[string]string{
				"action":              "restart checkout",
				"action_intent":       "restart_workload",
				"action_category":     "containment",
				"scope":               "service:checkout",
				"validation_category": "probable_containment",
				"safety_tier":         ActuatorSafetyTierImpacting,
				"proposal_only":       "true",
				"execution_eligible":  "false",
				"approval_state":      "not_applicable",
				"dry_run":             "false",
			},
			ActionContract: &ValidationActionContract{
				ID:                "contract-1",
				Intent:            "restart_workload",
				ActionCategory:    "containment",
				ExecutionCategory: "probable_containment",
				Target:            ActionTargetRef{Scope: "service:checkout"},
			},
		}},
	}

	records := buildRemediationEvidence(state)
	require.Len(t, records, 1)
	require.NotNil(t, records[0].Provenance)
	require.Equal(t, "trusted_derived", records[0].Provenance.TrustClass)
}

func TestBuildRemediationEvidenceMarksExecutedStepsTrustedExecuted(t *testing.T) {
	state := &workflowState{
		workflowID: "wf-evidence-executed",
		now:        time.Now().UTC(),
		planSteps: []AgentPlanStep{{
			ID:       "guarded-action-2",
			Tool:     ToolRemediation,
			Status:   "executed",
			Verified: false,
			Query: map[string]string{
				"action":              "restart checkout",
				"action_intent":       "restart_workload",
				"action_category":     "containment",
				"scope":               "service:checkout",
				"validation_category": "probable_containment",
				"safety_tier":         ActuatorSafetyTierImpacting,
				"proposal_only":       "false",
				"execution_eligible":  "true",
			},
		}},
	}

	records := buildRemediationEvidence(state)
	require.Len(t, records, 1)
	require.NotNil(t, records[0].Provenance)
	require.Equal(t, "trusted_executed", records[0].Provenance.TrustClass)
}
