package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidationActionContractRoundTripPreservesGovernanceFields(t *testing.T) {
	raw := encodeValidationActionContract(ValidationActionContract{
		ID:                 "contract-1",
		Intent:             "rollback_revision",
		ActionCategory:     "rollback",
		Summary:            "rollback checkout-v2",
		ExecutionCategory:  "Probable Containment",
		ValidationCategory: "Probable Containment",
		ActuatorSafetyTier: "impacting",
		ExecutionLevel:     "approval_required",
		TargetScope:        "service:checkout",
		Target: ActionTargetRef{
			CollectorID: "collector-a",
			Scope:       "service:checkout",
			Namespace:   "prod",
			Resource:    "deployment",
			Name:        "checkout",
		},
		Preconditions:       []string{"operator approval", "healthy replacement ready", "operator approval"},
		ExpectedImpact:      "restore the previous checkout revision",
		BlastRadiusEstimate: 2,
		BlastRadiusScope:    []string{"service:checkout", "service:payments", "service:checkout"},
		BlastRadiusNotes:    []string{"target scope: service:checkout", "human approval remains mandatory before any non-read-only execution"},
		DryRunDefault:       true,
		DryRunState:         "dry_run_default",
		RequiresApproval:    true,
		Safe:                true,
		Rollback: RollbackContract{
			Summary:    "re-apply checkout-v1",
			Command:    "kubectl rollout undo deploy/checkout",
			Required:   true,
			Reversible: true,
		},
		Metadata: map[string]string{
			"scope": "service:checkout",
		},
	})

	contract, err := decodeValidationActionContract(raw)
	require.NoError(t, err)
	require.NotNil(t, contract)
	require.Equal(t, "contract-1", contract.ID)
	require.Equal(t, "rollback_revision", contract.Intent)
	require.Equal(t, "rollback", contract.ActionCategory)
	require.Equal(t, "probable_containment", contract.ExecutionCategory)
	require.Equal(t, "probable_containment", contract.ValidationCategory)
	require.Equal(t, "impacting", contract.ActuatorSafetyTier)
	require.Equal(t, "service:checkout", contract.TargetScope)
	require.Equal(t, []string{"operator approval", "healthy replacement ready"}, contract.Preconditions)
	require.Equal(t, []string{"service:checkout", "service:payments"}, contract.BlastRadiusScope)
	require.Equal(t, []string{"target scope: service:checkout", "human approval remains mandatory before any non-read-only execution"}, contract.BlastRadiusNotes)
	require.Equal(t, "dry_run_default", contract.DryRunState)
	require.Equal(t, "re-apply checkout-v1", contract.Rollback.Summary)
	require.Equal(t, "impacting", contract.Metadata["actuator_safety_tier"])
	require.Equal(t, "service:checkout", contract.Metadata["scope"])
}
