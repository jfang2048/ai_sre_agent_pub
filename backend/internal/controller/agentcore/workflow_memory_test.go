package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWorkflowMemoryStoreAppendAndQuery(t *testing.T) {
	store := NewWorkflowMemoryStore(t.TempDir(), zap.NewNop())
	path, err := store.Append(WorkflowMemoryRecord{
		RecordID:            "incident-1",
		WorkflowID:          "wf-1",
		IncidentID:          "inc-1",
		WorkflowType:        "rca",
		CollectorID:         "collector-a",
		Status:              "resolved",
		Title:               "GPU timeout after rollout",
		Summary:             "GPU jobs timed out after a rollout",
		MostLikelyCause:     "driver mismatch",
		ResolutionSummary:   "rolled back the driver change",
		VerificationSummary: "latency returned to baseline",
		Signals:             []string{"gpu", "timeout", "latency"},
		Actions:             []string{"rollback driver"},
		Tags:                []string{"gpu", "rollout"},
		CreatedAt:           time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, path)

	hits := store.Query("gpu timeout after rollout", "historical_incident", "collector-a", 3)
	require.NotEmpty(t, hits)
	require.Equal(t, "incident_memory", hits[0].SourceType)
	require.Equal(t, "historical_incident", hits[0].KnowledgeType)
	require.Contains(t, hits[0].Snippet, "rolled back")
}
