package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/stretchr/testify/require"
)

func TestEvaluationWorkflowStoresAreIsolatedFromEachOtherAndEnvironment(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("SRE_AGENT_WORKFLOW_DATA_PATH", shared)
	t.Setenv("SRE_AGENT_WORKFLOW_STORE_PATH", filepath.Join(shared, "runs.db"))
	cfg := agentcore.WorkflowConfigFromEnv(agentcore.DefaultWorkflowConfig())
	store, index := ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig())
	first, closeFirst, err := newEvaluationWorkflowEngine(cfg, store, index)
	require.NoError(t, err)
	t.Cleanup(closeFirst)
	firstStatus := first.DurabilityStatus()
	require.NotEqual(t, shared, firstStatus.DataPath, "environment must not undo experiment isolation")
	second, closeSecond, err := newEvaluationWorkflowEngine(cfg, store, index)
	require.NoError(t, err)
	t.Cleanup(closeSecond)
	secondStatus := second.DurabilityStatus()
	require.NotEqual(t, firstStatus.StorePath, secondStatus.StorePath)
	require.NotEqual(t, firstStatus.DataPath, secondStatus.DataPath)
	for _, engine := range []*agentcore.WorkflowEngine{first, second} {
		require.True(t, engine.DurabilityStatus().Persistent)
		require.False(t, engine.DurabilityStatus().FallbackActive)
		require.True(t, engine.ArtifactStatus().MetadataPersistent)
	}
	// Close releases both databases, not just their directory entries.
	require.NoError(t, first.Close())
	reopened, err := agentcore.NewBoltDurableStore(firstStatus.StorePath)
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
	closeFirst()
	require.NoDirExists(t, firstStatus.DataPath)
	entries, err := os.ReadDir(shared)
	require.NoError(t, err)
	require.Empty(t, entries, "experiment must not write into the caller's shared runtime directory")
}

func TestWorkflowExecutionKeepsArtifactsUntilScoringCompletes(t *testing.T) {
	root, err := resolveRepoRoot("")
	require.NoError(t, err)
	cases, err := loadIncidentCases(root)
	require.NoError(t, err)
	require.NotEmpty(t, cases)
	kb, cleanup, err := buildKnowledgeBase(context.Background(), root)
	require.NoError(t, err)
	defer cleanup()
	execution, err := runWorkflowCaseDetailed(context.Background(), kb, cases[0], WorkflowCaseRunOptions{})
	require.NoError(t, err)
	defer execution.Close()
	require.NotEmpty(t, execution.Report.MessageHistory)
	for _, ref := range execution.Report.MessageHistory {
		raw, err := os.ReadFile(ref.Path)
		require.NoError(t, err, "downstream scorers must still be able to read message evidence")
		require.NotEmpty(t, raw)
	}
	execution.Close()
	for _, ref := range execution.Report.MessageHistory {
		require.NoFileExists(t, ref.Path)
	}
}
