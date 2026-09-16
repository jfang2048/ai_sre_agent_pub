package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRankFinalHypothesesUsesSpecificRunbookToBreakCloseTie(t *testing.T) {
	state := &workflowState{
		incident: IncidentSynthesis{CandidateRootCauseCluster: "storage or IO bottleneck"},
		hypotheses: []RCAHypothesis{
			{ID: "cpu", Title: "cpu scheduling contention", Confidence: 0.741},
			{ID: "distributed", Title: "distributed resource contention", Confidence: 0.741},
			{ID: "memory", Title: "memory pressure and reclaim", Confidence: 0.721},
			{ID: "storage", Title: "storage io bottleneck", Confidence: 0.721},
		},
		retrievedRunbooks: []RetrievedDocumentEvidence{
			{
				DocID:        "memory-pressure",
				EvidenceID:   "ev-memory-runbook",
				Title:        "Memory Pressure Runbook",
				Summary:      "sustained RSS growth with reclaim pressure",
				LikelyCauses: []string{"memory leak", "unbounded cache growth"},
			},
			// A duplicate chunk from the same document must not stack weight.
			{
				DocID:        "memory-pressure",
				EvidenceID:   "ev-memory-runbook-duplicate",
				Title:        "Memory Pressure Runbook",
				LikelyCauses: []string{"memory leak with reclaim pressure"},
			},
		},
	}

	rankFinalHypotheses(state)

	require.Equal(t, "memory", state.hypotheses[0].ID)
	require.InDelta(t, 0.801, state.hypotheses[0].Confidence, 0.000001)
	require.Contains(t, state.hypotheses[0].EvidenceIDs, "ev-memory-runbook")
	require.NotContains(t, state.hypotheses[0].EvidenceIDs, "ev-memory-runbook-duplicate")
}

func TestRankFinalHypothesesCombinesIncidentClusterAndRunbook(t *testing.T) {
	state := &workflowState{
		incident: IncidentSynthesis{CandidateRootCauseCluster: "storage or IO bottleneck"},
		hypotheses: []RCAHypothesis{
			{ID: "distributed", Title: "distributed resource contention", Confidence: 0.741},
			{ID: "errors", Title: "service-level error burst", Confidence: 0.741},
			{ID: "storage", Title: "storage io bottleneck", Confidence: 0.721},
		},
		retrievedRunbooks: []RetrievedDocumentEvidence{{
			DocID:      "disk-latency",
			EvidenceID: "ev-disk-runbook",
			Title:      "Disk Latency Runbook",
			Summary:    "rising disk latency and queue depth indicate a storage bottleneck",
		}},
	}

	rankFinalHypotheses(state)

	require.Equal(t, "storage", state.hypotheses[0].ID)
	require.InDelta(t, 0.841, state.hypotheses[0].Confidence, 0.000001)
}

func TestRankFinalHypothesesIgnoresWeakRetrievalOverlap(t *testing.T) {
	state := &workflowState{
		hypotheses: []RCAHypothesis{
			{ID: "cpu", Title: "cpu scheduling contention", Confidence: 0.7},
			{ID: "storage", Title: "storage io bottleneck", Confidence: 0.6},
		},
		retrievedRunbooks: []RetrievedDocumentEvidence{{
			DocID:   "generic",
			Title:   "Generic pressure guide",
			Summary: "check contention before making a change",
		}},
	}

	rankFinalHypotheses(state)

	require.Equal(t, "cpu", state.hypotheses[0].ID)
	require.InDelta(t, 0.7, state.hypotheses[0].Confidence, 0.000001)
	require.InDelta(t, 0.6, state.hypotheses[1].Confidence, 0.000001)
}
