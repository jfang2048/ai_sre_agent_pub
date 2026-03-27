package changeintel

import (
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDeriveAndCorrelateChanges(t *testing.T) {
	now := time.Now().UTC()
	node := &ingest.NodeSnapshot{
		CollectorID:      "collector-a",
		Hostname:         "gpu-node-a",
		UpdatedAt:        now.Add(-5 * time.Minute),
		LastCollectionAt: now.Add(-5 * time.Minute),
		Labels: map[string]string{
			"nvidia.driver.version": "550.54.15",
			"release.image":         "trainer:v2",
		},
	}

	store := NewStore(t.TempDir(), zap.NewNop())
	_, err := store.Append(ChangeEvent{
		ChangeID:    "chg-feature",
		Category:    "feature_flag",
		Summary:     "canary flag enabled for trainer-service",
		CollectorID: "collector-a",
		Entity:      "trainer-service",
		Scope:       "service",
		StartedAt:   now.Add(-10 * time.Minute),
	})
	require.NoError(t, err)

	events, err := store.Query(QueryOptions{CollectorID: "collector-a"})
	require.NoError(t, err)
	events = append(events, DeriveFromNode(node)...)
	events = append(events, DeriveFromLogMessages("collector-a", []string{
		"deployment completed for trainer-service image trainer:v2",
		"nvidia driver upgraded on gpu-node-a",
	}, now.Add(-3*time.Minute))...)

	result := Correlate(events, QueryOptions{
		CollectorID:     "collector-a",
		IncidentSummary: "GPU jobs timed out after driver rollout",
		WindowStart:     now.Add(-30 * time.Minute),
		WindowEnd:       now,
		ScopeHints:      []string{"gpu-node-a", "trainer-service"},
		Limit:           4,
	})
	require.NotEmpty(t, result.Events)
	require.NotNil(t, result.Strongest)
	require.NotEmpty(t, result.Categories)
	require.True(t, result.Strongest.ChangeScore > 0)
	require.NotEmpty(t, result.Strongest.HypothesisHints)
}
