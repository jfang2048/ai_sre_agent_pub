package k8sview

import "time"

// Config controls read-only Kubernetes integration for cluster inventory and workload visibility.
type Config struct {
	Enabled           bool            `yaml:"enabled" json:"enabled"`
	RefreshInterval   time.Duration   `yaml:"refresh_interval" json:"refresh_interval"`
	RequestTimeout    time.Duration   `yaml:"request_timeout" json:"request_timeout"`
	MaxPodsPerCluster int             `yaml:"max_pods_per_cluster" json:"max_pods_per_cluster"`
	Clusters          []ClusterConfig `yaml:"clusters" json:"clusters"`
}

// ClusterConfig describes one Kubernetes cluster target.
type ClusterConfig struct {
	Name          string `yaml:"name" json:"name"`
	InCluster     bool   `yaml:"in_cluster" json:"in_cluster"`
	Kubeconfig    string `yaml:"kubeconfig" json:"kubeconfig"`
	Context       string `yaml:"context" json:"context"`
	Namespace     string `yaml:"namespace" json:"namespace"`
	LabelSelector string `yaml:"label_selector" json:"label_selector"`
}

// DefaultConfig returns conservative defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:           false,
		RefreshInterval:   20 * time.Second,
		RequestTimeout:    6 * time.Second,
		MaxPodsPerCluster: 5000,
		Clusters:          []ClusterConfig{},
	}
}
