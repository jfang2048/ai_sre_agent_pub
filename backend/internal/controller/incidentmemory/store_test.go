package incidentmemory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	artifactstore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/artifacts"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStoreReloadsRecordsFromArtifactMetadata(t *testing.T) {
	root := t.TempDir()
	cfg := artifactstore.Config{
		MetadataBackend: "bbolt",
		MetadataPath:    filepath.Join(root, "artifacts.db"),
		PayloadBackend:  "filesystem",
		PayloadRootPath: filepath.Join(root, "payloads"),
	}

	manager, _, err := artifactstore.NewManager(context.Background(), cfg, zap.NewNop())
	require.NoError(t, err)

	store := NewStoreWithArtifacts(root, manager, zap.NewNop())
	_, err = store.AppendWithMetadata(Record{
		RecordID:     "incident-store-reload",
		WorkflowID:   "wf-store-reload",
		WorkflowType: "rca",
		CollectorID:  "collector-a",
		Status:       "resolved",
		Title:        "Checkout rollback validated",
		Summary:      "Rollback restored checkout after rollout regression",
		Signals:      []string{"checkout", "rollback"},
		Actions:      []string{"rollback deployment"},
		Tags:         []string{"rollout"},
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, manager.Close())

	reloadedManager, _, err := artifactstore.NewManager(context.Background(), cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reloadedManager.Close()) })

	reloaded := NewStoreWithArtifacts(root, reloadedManager, zap.NewNop())
	results := reloaded.Query("checkout rollback", QueryOptions{TopK: 3})
	require.NotEmpty(t, results)
	require.Equal(t, "incident-store-reload", results[0].Record.RecordID)
}
