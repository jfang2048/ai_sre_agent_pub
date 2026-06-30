package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateToolContractRejectsMissingRequiredFields(t *testing.T) {
	err := validateToolContract(ToolContract{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required fields")
}

func TestValidateToolContractRejectsApprovalForReadOnly(t *testing.T) {
	err := validateToolContract(ToolContract{
		Name:                   ToolMetrics,
		Version:                "v1",
		Purpose:                "collect metrics",
		CapabilityFamily:       ToolCapabilityFamilyTelemetry,
		Determinism:            "deterministic",
		ReadOnly:               true,
		SafetyClass:            "read_only",
		AllowedRuntimeContexts: []string{"rca"},
		InputSchema:            "{}",
		OutputSchema:           "{}",
		TimeoutBudget:          "10s",
		ApprovalRequirement:    WorkflowToolApprovalRequirement{Required: true},
		RetryPolicy:            WorkflowToolRetryPolicy{MaxAttempts: 1},
		ReplaySemantics:        "replay_safe",
		LegacyContract: WorkflowToolContract{
			ToolName:               ToolMetrics,
			Version:                "v1",
			Purpose:                "collect metrics",
			CapabilityFamily:       "telemetry",
			AllowedStages:          []string{"*"},
			AllowedRuntimeContexts: []string{"rca"},
			InputSchema:            "{}",
			OutputSchema:           "{}",
			Determinism:            "deterministic",
			ReadOnly:               true,
			SafetyClass:            "read_only",
			TimeoutBudget:          "10s",
			RetryPolicy:            WorkflowToolRetryPolicy{MaxAttempts: 1},
			Approval:               WorkflowToolApprovalRequirement{Required: false},
			ReplaySemantics:        "replayable_when_source_window_and_artifact_refs_are_available",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot require approval when read-only")
}

func TestValidateToolContractRejectsInvalidReplaySemantics(t *testing.T) {
	err := validateToolContract(ToolContract{
		Name:                   ToolLogs,
		Version:                "v1",
		Purpose:                "search logs",
		CapabilityFamily:       ToolCapabilityFamilyLogs,
		Determinism:            "deterministic",
		ReadOnly:               true,
		SafetyClass:            "read_only",
		AllowedRuntimeContexts: []string{"rca"},
		InputSchema:            "{}",
		OutputSchema:           "{}",
		TimeoutBudget:          "10s",
		RetryPolicy:            WorkflowToolRetryPolicy{MaxAttempts: 1},
		ReplaySemantics:        "mystery_mode",
		LegacyContract: WorkflowToolContract{
			ToolName:               ToolLogs,
			Version:                "v1",
			Purpose:                "search logs",
			CapabilityFamily:       "logs",
			AllowedStages:          []string{"*"},
			AllowedRuntimeContexts: []string{"rca"},
			InputSchema:            "{}",
			OutputSchema:           "{}",
			Determinism:            "deterministic",
			ReadOnly:               true,
			SafetyClass:            "read_only",
			TimeoutBudget:          "10s",
			RetryPolicy:            WorkflowToolRetryPolicy{MaxAttempts: 1},
			ReplaySemantics:        "replayable_when_source_window_and_artifact_refs_are_available",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid replay semantics")
}

func TestToolContractRegistryBuildsRichContracts(t *testing.T) {
	manager := newWorkflowToolManager(nil, &mockTool{name: ToolMetrics, deterministic: true})
	require.NotNil(t, manager.contracts)
	contracts := manager.contracts.List()
	require.Len(t, contracts, 1)
	require.Equal(t, ToolMetrics, contracts[0].Name)
	require.Equal(t, ToolAutonomyEligibilityReadOnlyEligible, contracts[0].AutonomousSelectionEligible)
	require.NotEmpty(t, contracts[0].LikelyFollowupToolFamilies)
}
