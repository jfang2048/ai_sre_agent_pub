package controller

import (
	"path/filepath"
	"strings"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
)

// workflowConfigForController roots workflow runtime data under the controller
// data directory. Workflow and artifact metadata can be moved to shared
// backends through env overrides. Filesystem artifact payloads still resolve
// under this root unless a shared payload path is configured explicitly.
func workflowConfigForController(cfg Config) agentcore.WorkflowConfig {
	workflowCfg := agentcore.DefaultWorkflowConfig()
	persistDir := strings.TrimSpace(cfg.Agent.PersistDir)
	if persistDir == "" {
		return workflowCfg
	}
	persistDir = filepath.Clean(persistDir)
	workflowCfg.WorkflowDataPath = filepath.Join(persistDir, "workflows")
	workflowCfg.WorkflowStorePath = filepath.Join(persistDir, "workflow_runs.db")
	workflowCfg.ArtifactMetadataPath = filepath.Join(workflowCfg.WorkflowDataPath, "artifacts.db")
	workflowCfg.ArtifactPayloadRootPath = workflowCfg.WorkflowDataPath
	workflowCfg.AgentMessageDir = filepath.Join(workflowCfg.WorkflowDataPath, "messages")
	return workflowCfg
}
