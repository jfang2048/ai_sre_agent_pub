package controller

import (
	"path/filepath"
	"strings"
)

const (
	defaultDeploymentMode = "local-dev"
	defaultClusterName    = "local"
	defaultControllerRoot = "/var/lib/ai-sre-agent"
)

// DeploymentConfig describes cluster-facing runtime placement without forcing
// callers to rewrite multiple path fields by hand.
type DeploymentConfig struct {
	Mode             string `yaml:"mode" json:"mode"`
	ClusterName      string `yaml:"cluster_name" json:"cluster_name"`
	DataRoot         string `yaml:"data_root" json:"data_root"`
	ExternalURL      string `yaml:"external_url" json:"external_url"`
	InsecureOverride bool   `yaml:"insecure_override" json:"insecure_override"`
}

func DefaultDeploymentConfig() DeploymentConfig {
	return DeploymentConfig{
		Mode:        defaultDeploymentMode,
		ClusterName: defaultClusterName,
	}
}

// ApplyDeploymentDefaults rewrites repo-local defaults into deployment-friendly
// paths only when callers have not already supplied explicit values.
func ApplyDeploymentDefaults(cfg Config) Config {
	deployment := cfg.Deployment
	mode := normalizeControllerDeploymentMode(deployment.Mode)
	if mode == "" {
		mode = defaultDeploymentMode
	}
	deployment.Mode = mode
	if strings.TrimSpace(deployment.ClusterName) == "" {
		deployment.ClusterName = defaultClusterName
	}
	if mode != defaultDeploymentMode && strings.TrimSpace(deployment.DataRoot) == "" {
		deployment.DataRoot = defaultControllerRoot
	}
	if strings.TrimSpace(deployment.DataRoot) != "" {
		deployment.DataRoot = filepath.Clean(deployment.DataRoot)
	}
	if mode != defaultDeploymentMode && deployment.DataRoot != "" {
		if pathMatchesDefault(cfg.WebPath, "./web") {
			cfg.WebPath = filepath.Join(deployment.DataRoot, "controller", "web")
		}
		if pathMatchesDefault(cfg.Ingest.Persistence.Path, "./data/controller/ingest/store.db") {
			cfg.Ingest.Persistence.Path = filepath.Join(deployment.DataRoot, "controller", "data", "ingest", "store.db")
		}
		if pathMatchesDefault(cfg.Agent.PersistDir, "./data/agent") {
			cfg.Agent.PersistDir = filepath.Join(deployment.DataRoot, "controller", "data", "agent")
		}
		if pathMatchesDefault(cfg.Agent.RAGDatasetPath, "./dataset") {
			cfg.Agent.RAGDatasetPath = filepath.Join(deployment.DataRoot, "controller", "dataset")
		}
		if pathMatchesDefault(cfg.Agent.RAGIndexPath, "./data/agent/rag/index.json") {
			cfg.Agent.RAGIndexPath = filepath.Join(deployment.DataRoot, "controller", "data", "agent", "rag", "index.json")
		}
		if pathMatchesDefault(cfg.Agent.PolicyFile, "./configs/agent_playbooks.yaml") {
			cfg.Agent.PolicyFile = filepath.Join(deployment.DataRoot, "controller", "configs", "agent_playbooks.yaml")
		}
		if pathMatchesDefault(cfg.Inventory.TargetsFile, "./configs/controller_targets.yaml") {
			cfg.Inventory.TargetsFile = "/etc/ai-sre-agent/controller_targets.yaml"
		}
		if cfg.ListenAddr == "127.0.0.1:8080" {
			cfg.ListenAddr = "0.0.0.0:8080"
		}
		if cfg.GRPCListenAddr == "127.0.0.1:9090" {
			cfg.GRPCListenAddr = "0.0.0.0:9090"
		}
	}
	cfg.Deployment = deployment
	return cfg
}

func normalizeControllerDeploymentMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "local", "local-dev", "dev":
		return defaultDeploymentMode
	case "standalone", "cluster-lite", "distributed":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func pathMatchesDefault(current string, expected string) bool {
	current = filepath.Clean(strings.TrimSpace(current))
	if current == "." || current == "" {
		return false
	}
	return current == filepath.Clean(expected)
}
