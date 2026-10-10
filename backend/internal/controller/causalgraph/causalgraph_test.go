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

func TestAnalyzeDoesNotTreatObservationAnchorAsRootCause(t *testing.T) {
	result := Analyze(Input{
		Nodes: []Node{
			{ID: "collector", Kind: "topology", Label: "collector-a", Score: 0.2},
			{ID: "process", Kind: "process", Label: "checkout-api", Score: 0.3},
			{ID: "cpu", Kind: "signal", Label: "CPU usage", Score: 0.5, Role: "symptom"},
		},
		Edges: []Edge{
			{Source: "collector", Target: "process", Kind: "runs", Weight: 0.5},
			{Source: "collector", Target: "cpu", Kind: "emits", Weight: 0.5},
		},
		SymptomNodes: []string{"cpu"},
	})

	require.Equal(t, "checkout-api", result.SuspectedRootCauseEntity)
	require.Empty(t, result.CausePath, "a collector-to-metric link is not a causal explanation")
	for _, candidate := range result.Candidates {
		require.NotEqual(t, "collector-a", candidate.Label)
		require.NotEqual(t, "CPU usage", candidate.Label)
	}
}

func TestAnalyzeKeepsEvidenceBackedTopologyPath(t *testing.T) {
	result := Analyze(Input{
		Nodes: []Node{
			{ID: "database", Kind: "topology", Label: "database", Score: 0.7},
			{ID: "payment", Kind: "topology", Label: "payment service", Score: 0.3},
			{ID: "timeout", Kind: "signal", Label: "request timeout", Score: 0.5, Role: "symptom"},
		},
		Edges: []Edge{
			{Source: "database", Target: "payment", Kind: "dependency", Weight: 0.8},
			{Source: "payment", Target: "timeout", Kind: "emits", Weight: 0.8},
		},
		SymptomNodes: []string{"timeout"},
	})

	require.Equal(t, []string{"database", "payment service", "request timeout"}, result.CausePath)
}
