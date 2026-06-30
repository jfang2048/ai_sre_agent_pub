package agent

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestToolExperienceMemoryStorePrunesToBoundedSize(t *testing.T) {
	store := NewToolExperienceMemoryStore(t.TempDir(), zap.NewNop())
	for i := 0; i < workflowToolExperienceMaxRecords+25; i++ {
		contract := WorkflowToolContract{ToolName: ToolDNSCheck, CapabilityFamily: "service_health"}
		store.Observe(SceneFamilyNetworkConnectivity, fmt.Sprintf("dns-failure-%d", i), []string{"dns health"}, contract, AdaptiveProgressAssessment{
			ConfidenceDelta:          0.01,
			EvidenceGapCoverageDelta: 1,
			Progress:                 true,
		}, &NormalizedToolResult{Summary: "dns confirmed"})
	}
	require.LessOrEqual(t, len(store.Snapshot()), workflowToolExperienceMaxRecords)
}

func TestToolExperienceMemoryStorePenalizesLowYield(t *testing.T) {
	store := NewToolExperienceMemoryStore(t.TempDir(), zap.NewNop())
	contract := WorkflowToolContract{ToolName: ToolDNSCheck, CapabilityFamily: "service_health"}
	store.Observe(SceneFamilyNetworkConnectivity, "dns failures", []string{"dns health"}, contract, AdaptiveProgressAssessment{
		Progress: false,
		Plateau:  true,
	}, &NormalizedToolResult{Summary: "no signal", ResultQuality: "low", LowYieldSignal: true})

	prior := store.Prior(SceneFamilyNetworkConnectivity, "dns failures", []string{"dns health"}, contract)
	require.Less(t, prior, 0.0)
}
