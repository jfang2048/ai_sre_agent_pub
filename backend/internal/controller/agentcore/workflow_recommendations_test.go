package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildRCARecommendationsKeepsRetrievedRunbookUnderRecommendationCap(t *testing.T) {
	hypotheses := make([]RCAHypothesis, 0, 6)
	for i := 1; i <= 6; i++ {
		hypotheses = append(hypotheses, RCAHypothesis{
			ID:         fmt.Sprintf("hypothesis-%d", i),
			Rank:       i,
			Title:      fmt.Sprintf("candidate cause %d", i),
			Confidence: 0.8 - float64(i)*0.05,
		})
	}

	state := &workflowState{
		collectorID: "checkout-node",
		hypotheses:  hypotheses,
		incident: IncidentSynthesis{
			Summary:       "checkout-api is under memory pressure",
			ImpactedScope: []string{"service/checkout-api"},
			Severity:      "high",
			Confidence:    0.82,
		},
		incidentMemoryMatches: []RetrievedDocumentEvidence{{
			EvidenceID: "memory-1",
			Title:      "prior checkout incident",
			Score:      0.8,
		}},
		retrievalSummary:    "retrieved similar cases and an applicable runbook",
		retrievalConfidence: 0.85,
		retrievedCases: []RetrievedDocumentEvidence{
			{EvidenceID: "case-1", Title: "similar case 1", Score: 0.8},
			{EvidenceID: "case-2", Title: "similar case 2", Score: 0.7},
			{EvidenceID: "case-3", Title: "similar case 3", Score: 0.6},
		},
		retrievedRunbooks: []RetrievedDocumentEvidence{{
			EvidenceID:       "runbook-1",
			Title:            "memory pressure runbook",
			SourcePath:       "knowledge/memory-pressure-runbook.md",
			Score:            0.9,
			Commands:         []string{"cat /proc/pressure/memory"},
			RemediationSteps: []string{"inspect top RSS processes", "capture heap profile"},
		}},
	}

	recommendations := buildRCARecommendations(state)
	require.Len(t, recommendations, 12)

	var runbookRecommendation *WorkflowRecommendation
	for i := range recommendations {
		if recommendations[i].ID == "rca-runbook-1" {
			runbookRecommendation = &recommendations[i]
			break
		}
	}
	require.NotNil(t, runbookRecommendation, "concrete runbook guidance must not be displaced by lower-priority case comparisons")

	plan := strings.Join(append([]string{runbookRecommendation.Details}, runbookRecommendation.Checks...), " ")
	require.Contains(t, plan, "cat /proc/pressure/memory")
	require.Contains(t, plan, "inspect top RSS processes")
	require.Contains(t, plan, "capture heap profile")
}
