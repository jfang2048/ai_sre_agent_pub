package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"go.uber.org/zap"
)

// Each treatment and trial starts with empty local state. Sharing stores would
// leak earlier answers into later cases and make the second engine lose its
// persistent store to a bbolt lock held by the first engine.
func newEvaluationWorkflowEngine(cfg agentcore.WorkflowConfig, store *ingest.MemoryStore, index *logindex.Index) (*agentcore.WorkflowEngine, func(), error) {
	root, err := os.MkdirTemp("", "ai-sre-agent-eval-workflow-*")
	if err != nil {
		return nil, nil, err
	}
	cfg.WorkflowDataPath = root
	cfg.WorkflowStoreBackend = "bbolt"
	cfg.WorkflowStorePath = filepath.Join(root, "workflow_runs.db")
	cfg.ArtifactMetadataBackend = "bbolt"
	cfg.ArtifactMetadataPath = filepath.Join(root, "artifacts.db")
	cfg.ArtifactPayloadBackend = "filesystem"
	cfg.ArtifactPayloadRootPath = root
	cfg.ArtifactPayloadShared = false
	cfg.AgentMessageDir = filepath.Join(root, "messages")
	engine := agentcore.NewWorkflowEngineFromConfig(cfg, store, index, nil, zap.NewNop())
	cleanup := sync.OnceFunc(func() {
		_ = engine.Close()
		_ = os.RemoveAll(root)
	})
	status, artifacts := engine.DurabilityStatus(), engine.ArtifactStatus()
	if !status.Persistent || status.FallbackActive || !artifacts.MetadataPersistent {
		cleanup()
		return nil, nil, fmt.Errorf("evaluation requires isolated persistent stores: workflow=%s artifacts=%s", status.LastError, artifacts.LastError)
	}
	return engine, cleanup, nil
}
