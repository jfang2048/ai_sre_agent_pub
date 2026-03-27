package incidentmemory

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStoreAppendAndQuery(t *testing.T) {
	store := NewStore(t.TempDir(), zap.NewNop())
	path, err := store.Append(Record{
		RecordID:            "incident-1",
		WorkflowID:          "wf-1",
		IncidentID:          "inc-1",
		WorkflowType:        "rca",
		CollectorID:         "collector-a",
		Status:              "resolved",
		Title:               "GPU timeout after rollout",
		Summary:             "GPU jobs timed out after a rollout",
		RootCauseEntity:     "node/collector-a",
		MostLikelyCause:     "driver mismatch",
		ResolutionSummary:   "rolled back the driver change",
		VerificationSummary: "latency returned to baseline",
		CausalPath:          []string{"deploy/gpu-driver", "node/collector-a", "job/training"},
		Signals:             []string{"gpu", "timeout", "latency"},
		Actions:             []string{"rollback driver"},
		ActionOutcomes: []ActionOutcome{{
			Action:       "rollback driver",
			Status:       "verified",
			Verification: "latency returned to baseline",
			Success:      true,
			Useful:       true,
		}},
		CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, path)

	hits := store.Query("gpu timeout after rollout", QueryOptions{
		Intent:      "historical_incident",
		CollectorID: "collector-a",
		TopK:        3,
	})
	require.NotEmpty(t, hits)
	require.Contains(t, hits[0].Snippet, "rolled back")
	require.Equal(t, "driver mismatch", hits[0].Record.MostLikelyCause)
}

func TestStoreQueryPrefersVerifiedSignalMatchedRecords(t *testing.T) {
	store := NewStore(t.TempDir(), zap.NewNop())
	now := time.Now().UTC()

	_, err := store.Append(Record{
		RecordID:            "verified-recent",
		WorkflowID:          "wf-verified",
		IncidentID:          "inc-verified",
		WorkflowType:        "rca",
		CollectorID:         "collector-a",
		Status:              "resolved",
		Title:               "GPU timeout after rollout",
		Summary:             "GPU jobs timed out after rollout until the driver rollback was applied",
		MostLikelyCause:     "driver mismatch after rollout",
		ResolutionSummary:   "rolled back the driver change",
		VerificationSummary: "latency returned to baseline",
		Signals:             []string{"gpu", "timeout", "latency"},
		Actions:             []string{"rollback driver"},
		Tags:                []string{"gpu", "rollout", "driver"},
		ActionOutcomes: []ActionOutcome{{
			Action:       "rollback driver",
			Status:       "verified",
			Verification: "latency returned to baseline",
			Success:      true,
			Useful:       true,
		}},
		UpdatedAt: now.Add(-2 * time.Hour),
		CreatedAt: now.Add(-2 * time.Hour),
	})
	require.NoError(t, err)

	_, err = store.Append(Record{
		RecordID:        "lexical-only",
		WorkflowID:      "wf-lexical",
		IncidentID:      "inc-lexical",
		WorkflowType:    "rca",
		CollectorID:     "collector-b",
		Status:          "investigating",
		Title:           "GPU timeout after rollout",
		Summary:         "GPU timeout after rollout is being investigated",
		MostLikelyCause: "unknown",
		Signals:         []string{"gpu"},
		Tags:            []string{"rollout"},
		UpdatedAt:       now.Add(-10 * time.Minute),
		CreatedAt:       now.Add(-10 * time.Minute),
	})
	require.NoError(t, err)

	hits := store.Query("gpu timeout after rollout rollback driver latency", QueryOptions{
		Intent:      "historical_incident",
		CollectorID: "collector-a",
		TopK:        2,
	})
	require.Len(t, hits, 2)
	require.Equal(t, "verified-recent", hits[0].Record.RecordID)
	require.Greater(t, hits[0].Score, hits[1].Score)
	require.NotEmpty(t, hits[0].Reasons)
	require.Contains(t, strings.ToLower(strings.Join(hits[0].Reasons, " ")), "verified")
}
