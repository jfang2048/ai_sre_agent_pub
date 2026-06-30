package causalgraph

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeRanksChangeAheadOfSymptoms(t *testing.T) {
	result := Analyze(Input{
		Nodes: []Node{
			{ID: "change:deploy", Kind: "change", Label: "trainer rollout", Score: 0.8, Role: "cause"},
			{ID: "service:trainer", Kind: "topology", Label: "trainer-service", Score: 0.4},
			{ID: "symptom:latency", Kind: "signal", Label: "gpu latency spike", Score: 0.6, Role: "symptom"},
		},
		Edges: []Edge{
			{Source: "change:deploy", Target: "service:trainer", Kind: "changes", Weight: 0.8},
			{Source: "service:trainer", Target: "symptom:latency", Kind: "impacts", Weight: 0.9},
		},
		SymptomNodes: []string{"symptom:latency"},
		ImpactScope:  []string{"service:trainer"},
	})

	require.NotEmpty(t, result.Candidates)
	require.Equal(t, "trainer rollout", result.SuspectedRootCauseEntity)
	require.Equal(t, []string{"trainer rollout", "trainer-service", "gpu latency spike"}, result.CausePath)
	require.NotEmpty(t, result.ImpactPath)
}
