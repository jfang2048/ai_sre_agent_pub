package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyDeploymentDefaultsForClusterMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:8080"
	cfg.GRPCListenAddr = "127.0.0.1:9090"
	cfg.Deployment.Mode = "cluster-lite"
	cfg.Deployment.ClusterName = "prod-a"

	cfg = ApplyDeploymentDefaults(cfg)

	require.Equal(t, "cluster-lite", cfg.Deployment.Mode)
	require.Equal(t, "prod-a", cfg.Deployment.ClusterName)
	require.Equal(t, "/var/lib/ai-sre-agent", cfg.Deployment.DataRoot)
	require.Equal(t, "0.0.0.0:8080", cfg.ListenAddr)
	require.Equal(t, "0.0.0.0:9090", cfg.GRPCListenAddr)
	require.Equal(t, "/var/lib/ai-sre-agent/controller/web", cfg.WebPath)
	require.Equal(t, "/var/lib/ai-sre-agent/controller/data/ingest/store.db", cfg.Ingest.Persistence.Path)
	require.Equal(t, "/var/lib/ai-sre-agent/controller/data/agent", cfg.Agent.PersistDir)
	require.Equal(t, "/var/lib/ai-sre-agent/controller/dataset", cfg.Agent.RAGDatasetPath)
	require.Equal(t, "/var/lib/ai-sre-agent/controller/data/agent/rag/index.json", cfg.Agent.RAGIndexPath)
	require.Equal(t, "/var/lib/ai-sre-agent/controller/configs/agent_playbooks.yaml", cfg.Agent.PolicyFile)
	require.Empty(t, cfg.Inventory.TargetsFile)
}
