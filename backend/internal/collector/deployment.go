package collector

import (
	"path/filepath"
	"strings"
)

const (
	defaultCollectorDeploymentMode = "local-dev"
	defaultCollectorClusterName    = "local"
	defaultCollectorDataRoot       = "/var/lib/ai-sre-agent"
)

// DeploymentConfig controls collector placement assumptions without requiring
// code edits for cluster-friendly layouts.
type DeploymentConfig struct {
	Mode        string `yaml:"mode" json:"mode"`
	ClusterName string `yaml:"cluster_name" json:"cluster_name"`
	DataRoot    string `yaml:"data_root" json:"data_root"`
}

func DefaultDeploymentConfig() DeploymentConfig {
	return DeploymentConfig{
		Mode:        defaultCollectorDeploymentMode,
		ClusterName: defaultCollectorClusterName,
	}
}

func normalizeDeploymentMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "local", "local-dev", "dev":
		return defaultCollectorDeploymentMode
	case "standalone", "cluster-lite", "distributed":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func applyDeploymentDefaults(cfg Config) Config {
	deployment := cfg.Deployment
	mode := normalizeDeploymentMode(deployment.Mode)
	if mode == "" {
		mode = defaultCollectorDeploymentMode
	}
	deployment.Mode = mode
	if strings.TrimSpace(deployment.ClusterName) == "" {
		deployment.ClusterName = defaultCollectorClusterName
	}
	if mode != defaultCollectorDeploymentMode && strings.TrimSpace(deployment.DataRoot) == "" {
		deployment.DataRoot = defaultCollectorDataRoot
	}
	if strings.TrimSpace(deployment.DataRoot) != "" {
		deployment.DataRoot = filepath.Clean(deployment.DataRoot)
	}
	if mode != defaultCollectorDeploymentMode && deployment.DataRoot != "" {
		if pathMatches(cfg.SpoolDir, defaultSpoolDir) {
			cfg.SpoolDir = filepath.Join(deployment.DataRoot, "collector", "data", "spool")
		}
		if pathMatches(cfg.EBPF.SocketPath, defaultEBPFSock) {
			cfg.EBPF.SocketPath = filepath.Join(deployment.DataRoot, "collector", "data", "run", "sre_collector_ebpf.sock")
		}
		if pathMatches(cfg.ProbeCore.BinaryPath, defaultProbeCoreBinaryPath) {
			cfg.ProbeCore.BinaryPath = "/usr/local/bin/sre-probe-core"
		}
	}
	cfg.Deployment = deployment
	cfg.Labels = applyDeploymentLabels(cfg.Labels, deployment)
	return cfg
}

func applyDeploymentLabels(base map[string]string, deployment DeploymentConfig) map[string]string {
	labels := make(map[string]string, len(base)+2)
	for k, v := range base {
		labels[k] = v
	}
	if _, exists := labels["cluster"]; !exists && strings.TrimSpace(deployment.ClusterName) != "" {
		labels["cluster"] = strings.TrimSpace(deployment.ClusterName)
	}
	if _, exists := labels["deployment_mode"]; !exists && strings.TrimSpace(deployment.Mode) != "" {
		labels["deployment_mode"] = strings.TrimSpace(deployment.Mode)
	}
	return labels
}

func pathMatches(current string, candidates ...string) bool {
	current = filepath.Clean(strings.TrimSpace(current))
	if current == "." || current == "" {
		return false
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if current == filepath.Clean(candidate) {
			return true
		}
	}
	return false
}
