package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildDiagnosticActionContractDefaultsToProposalOnlyWhenApprovalAndRollbackAreWeak(t *testing.T) {
	candidate := ValidationActionCandidate{
		ID:               "cand-1",
		Category:         "probable_containment",
		ActionIntent:     "restart_workload",
		ActionCategory:   "containment",
		Summary:          "restart checkout deployment",
		Scope:            "service/checkout",
		RequiresApproval: true,
		DryRunDefault:    true,
		Safe:             false,
		Reversible:       false,
	}

	contract := buildDiagnosticActionContract(candidate, &workflowState{collectorID: "node-a"})
	require.Equal(t, "cand-1", contract.ActionContractID)
	require.True(t, contract.RequiresApproval)
	require.True(t, contract.ProposalOnly)

	compiled := compileValidationActionContract(contract, "node-a", []string{"service/checkout"})
	require.Equal(t, "probable_containment", compiled.ExecutionCategory)
	require.True(t, compiled.RequiresApproval)
	require.Equal(t, ActuatorSafetyTierImpacting, actuatorSafetyTierForContract(&compiled))
}
