package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	proto "github.com/jfang2048/ai_sre_agent_pub/pkg/proto/metrics"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// KubernetesSource collects Kubernetes-related metrics
type KubernetesSource struct {
	BaseSource
	config KubernetesConfig
	logger *zap.Logger

	inCluster   bool
	kubectlPath string
	nodeName    string
	namespace   string
	podName     string
}

// NewKubernetesSource creates a new Kubernetes metrics source
func NewKubernetesSource(config KubernetesConfig, logger *zap.Logger) (*KubernetesSource, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("kubernetes source is disabled")
	}

	source := &KubernetesSource{
		BaseSource: BaseSource{
			name:    "kubernetes",
			enabled: config.Enabled,
		},
		config: config,
		logger: logger.With(zap.String("source", "kubernetes")),
	}

	// Auto-detect if running in Kubernetes cluster
	source.detectEnvironment()

	// Find kubectl
	if path, err := exec.LookPath("kubectl"); err == nil {
		source.kubectlPath = path
	}

	// If not in-cluster and no kubectl, disable
	if !source.inCluster && source.kubectlPath == "" {
		return nil, fmt.Errorf("not running in-cluster and kubectl not found")
	}

	return source, nil
}

func (k *KubernetesSource) Name() string {
	return "kubernetes"
}

func (k *KubernetesSource) Start(ctx context.Context) error {
	k.setStatus(true, true, "")
	k.logger.Info("kubernetes source started",
		zap.Bool("in_cluster", k.inCluster),
		zap.String("node", k.nodeName),
		zap.String("namespace", k.namespace),
		zap.String("pod", k.podName))
	return nil
}

func (k *KubernetesSource) Stop() error {
	k.setStatus(false, false, "")
	k.logger.Info("kubernetes source stopped")
	return nil
}

// detectEnvironment detects if running in a Kubernetes cluster
func (k *KubernetesSource) detectEnvironment() {
	// Check for service account token
	if _, err := os.Stat("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
		k.inCluster = true

		// Read namespace
		if namespaceData, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			k.namespace = strings.TrimSpace(string(namespaceData))
		}

		// Try to get node name from various sources
		// Method 1: NODE_NAME env var
		if nodeName := os.Getenv("NODE_NAME"); nodeName != "" {
			k.nodeName = nodeName
		}

		// Method 2: Try to read from hostname (often matches node name in some setups)
		if k.nodeName == "" {
			if hostname, err := os.Hostname(); err == nil {
				k.nodeName = hostname
			}
		}

		// Method 3: Try to query the API
		if k.nodeName == "" {
			k.nodeName = k.queryNodeName()
		}
	}

	// Get pod name from env
	k.podName = os.Getenv("POD_NAME")
	if k.podName == "" {
		k.podName = os.Getenv("HOSTNAME")
	}
}

// queryNodeName queries the Kubernetes API for the node name
func (k *KubernetesSource) queryNodeName() string {
	if !k.inCluster {
		return ""
	}

	// Use curl to access the Kubernetes API
	// This is a simple fallback that works within a pod
	hostname, _ := os.Hostname()
	return hostname
}

// Collect collects Kubernetes metrics
func (k *KubernetesSource) Collect(ctx context.Context) (*proto.MetricBatch, error) {
	metrics := []*proto.Metric{}
	now := timestamppb.Now()

	// Emit cluster membership info
	membershipValue := 0.0
	if k.inCluster {
		membershipValue = 1.0
	}
	metrics = append(metrics, &proto.Metric{
		Name:   "kubernetes.cluster.member",
		Type:   proto.MetricType_METRIC_TYPE_GAUGE,
		Points: []*proto.MetricPoint{{Timestamp: now, Value: membershipValue}},
	})

	if k.inCluster {
		// In-cluster metrics
		podMetrics := k.collectInClusterPodMetrics(now)
		metrics = append(metrics, podMetrics...)

		nodeMetrics := k.collectNodeMetrics(now)
		metrics = append(metrics, nodeMetrics...)

		containerMetrics := k.collectContainerMetrics(now)
		metrics = append(metrics, containerMetrics...)

		cgroupMetrics := k.collectCgroupMetrics(now)
		metrics = append(metrics, cgroupMetrics...)
	}

	// Cluster-wide metrics (if kubectl available)
	if k.kubectlPath != "" {
		clusterMetrics := k.collectClusterMetrics(now)
		metrics = append(metrics, clusterMetrics...)
	}

	k.setStatus(true, true, "")

	return &proto.MetricBatch{
		Metrics:     metrics,
		Source:      "kubernetes",
		CollectedAt: now,
	}, nil
}

// collectInClusterPodMetrics collects metrics for pods running on this node
func (k *KubernetesSource) collectInClusterPodMetrics(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Read pod info from /etc/hosts or environment
	labels := []*proto.MetricLabel{}
	if k.namespace != "" {
		labels = append(labels, &proto.MetricLabel{Key: "namespace", Value: k.namespace})
	}
	if k.podName != "" {
		labels = append(labels, &proto.MetricLabel{Key: "pod", Value: k.podName})
	}
	if k.nodeName != "" {
		labels = append(labels, &proto.MetricLabel{Key: "node", Value: k.nodeName})
	}

	metrics = append(metrics, &proto.Metric{
		Name:   "kubernetes.pod.running",
		Type:   proto.MetricType_METRIC_TYPE_GAUGE,
		Labels: labels,
		Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
	})

	// Try to get pod UID from service account
	if _, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/..data/pod-uid"); err == nil {
		metrics = append(metrics, &proto.Metric{
			Name:   "kubernetes.pod.uid",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: labels,
			Points: []*proto.MetricPoint{{Timestamp: now, Value: 1}},
		})
	}

	// Collect container info from cgroups
	containerPath := "/sys/fs/cgroup"
	kubePodPath := filepath.Join(containerPath, "kubePods")

	if _, err := os.Stat(kubePodPath); err == nil {
		// Using cgroup v1
		pods, _ := os.ReadDir(kubePodPath)
		metrics = append(metrics, createK8sGauge("kubernetes.node.pods.count", float64(len(pods)), now))
	} else {
		// Try cgroup v2
		kubePodPath = filepath.Join(containerPath, "kubepods")
		if _, err := os.Stat(kubePodPath); err == nil {
			pods, _ := os.ReadDir(kubePodPath)
			metrics = append(metrics, createK8sGauge("kubernetes.node.pods.count", float64(len(pods)), now))
		}
	}

	// Count containers per pod
	containerCount := k.countContainers()
	metrics = append(metrics, createK8sGauge("kubernetes.node.containers.count", float64(containerCount), now))

	return metrics
}

// collectNodeMetrics collects node-level metrics
func (k *KubernetesSource) collectNodeMetrics(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	if k.nodeName == "" {
		return metrics
	}

	// Get allocatable resources from /sys/fs/cgroup
	// CPU limits
	cpuQuota := k.readCgroupValue("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	cpuPeriod := k.readCgroupValue("/sys/fs/cgroup/cpu/cpu.cfs_period_us")

	if cpuQuota > 0 && cpuPeriod > 0 {
		cpuLimit := float64(cpuQuota) / float64(cpuPeriod)
		metrics = append(metrics, &proto.Metric{
			Name:   "kubernetes.node.cpu.limit",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: []*proto.MetricLabel{{Key: "node", Value: k.nodeName}},
			Points: []*proto.MetricPoint{{Timestamp: now, Value: cpuLimit}},
		})
	}

	// Memory limit
	if memLimit := k.readCgroupValue("/sys/fs/cgroup/memory/memory.limit_in_bytes"); memLimit > 0 {
		metrics = append(metrics, &proto.Metric{
			Name:   "kubernetes.node.memory.limit",
			Type:   proto.MetricType_METRIC_TYPE_GAUGE,
			Labels: []*proto.MetricLabel{{Key: "node", Value: k.nodeName}},
			Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(memLimit)}},
		})
	}

	// Get node info from kubelet if available
	if kubepodStatus, err := os.ReadFile("/var/lib/kubelet/pod-status.json"); err == nil {
		var status map[string]interface{}
		if err := json.Unmarshal(kubepodStatus, &status); err == nil {
			// Parse pod status for metrics
			if pods, ok := status["pods"].([]interface{}); ok {
				runningPods := 0
				for _, pod := range pods {
					if podMap, ok := pod.(map[string]interface{}); ok {
						if phase, ok := podMap["phase"].(string); ok && phase == "Running" {
							runningPods++
						}
					}
				}
				metrics = append(metrics, &proto.Metric{
					Name:   "kubernetes.node.pods.running",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: []*proto.MetricLabel{{Key: "node", Value: k.nodeName}},
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(runningPods)}},
				})
			}
		}
	}

	return metrics
}

// collectContainerMetrics collects container-level metrics from cgroups
func (k *KubernetesSource) collectContainerMetrics(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// Collect from cgroup v1
	cgroupPaths := []string{
		"/sys/fs/cgroup/cpu/kubepods",
		"/sys/fs/cgroup/memory/kubepods",
		"/sys/fs/cgroup/blkio/kubepods",
	}

	for _, basePath := range cgroupPaths {
		if _, err := os.Stat(basePath); err != nil {
			continue
		}

		filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() {
				return nil
			}

			// Extract container ID from path
			parts := strings.Split(path, "/")
			if len(parts) < 2 {
				return nil
			}

			containerID := parts[len(parts)-1]
			if containerID == "" || containerID == "kubepods" || containerID == "burstable" || containerID == "besteffort" {
				return nil
			}

			// Get CPU usage for this container
			if cpuUsage := k.readCgroupValue(path + "/cpuacct.usage"); cpuUsage > 0 {
				metrics = append(metrics, &proto.Metric{
					Name:   "kubernetes.container.cpu.usage_ns",
					Type:   proto.MetricType_METRIC_TYPE_COUNTER,
					Labels: []*proto.MetricLabel{{Key: "container_id", Value: containerID}},
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(cpuUsage)}},
				})
			}

			// Get memory usage
			if memUsage := k.readCgroupValue(strings.Replace(path, "/cpu/", "/memory/", 1) + "/memory.usage_in_bytes"); memUsage > 0 {
				metrics = append(metrics, &proto.Metric{
					Name:   "kubernetes.container.memory.usage",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: []*proto.MetricLabel{{Key: "container_id", Value: containerID}},
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(memUsage)}},
				})
			}

			return nil
		})
	}

	return metrics
}

// collectCgroupMetrics collects cgroup-based metrics
func (k *KubernetesSource) collectCgroupMetrics(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	// cgroup v1
	if _, err := os.Stat("/sys/fs/cgroup/cpu/kubepods.slice"); err == nil {
		// cgroup v2 with systemd
		return k.collectCgroupV2Metrics(now)
	}

	// Check for cgroup v2
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		if strings.Contains(string(data), "cgroup2") {
			return k.collectCgroupV2Metrics(now)
		}
	}

	return metrics
}

// collectCgroupV2Metrics collects metrics from cgroup v2
func (k *KubernetesSource) collectCgroupV2Metrics(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	basePaths := []string{
		"/sys/fs/cgroup/kubepods.slice",
		"/sys/fs/cgroup/kubepods-burstable.slice",
		"/sys/fs/cgroup/kubepods-besteffort.slice",
	}

	for _, basePath := range basePaths {
		if _, err := os.Stat(basePath); err != nil {
			continue
		}

		filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
			if err != nil || !info.IsDir() {
				return nil
			}

			// Read CPU stats
			if cpuStatData, err := os.ReadFile(path + "/cpu.stat"); err == nil {
				lines := strings.Split(string(cpuStatData), "\n")
				for _, line := range lines {
					parts := strings.Fields(line)
					if len(parts) == 2 {
						key := parts[0]
						if val, err := strconv.ParseFloat(parts[1], 64); err == nil {
							metrics = append(metrics, &proto.Metric{
								Name:   "kubernetes.cgroup.cpu." + key,
								Type:   proto.MetricType_METRIC_TYPE_COUNTER,
								Labels: []*proto.MetricLabel{{Key: "cgroup", Value: strings.TrimPrefix(path, "/sys/fs/cgroup/")}},
								Points: []*proto.MetricPoint{{Timestamp: now, Value: val}},
							})
						}
					}
				}
			}

			// Read memory stats
			if memStatData, err := os.ReadFile(path + "/memory.stat"); err == nil {
				lines := strings.Split(string(memStatData), "\n")
				for _, line := range lines {
					parts := strings.Fields(line)
					if len(parts) == 2 {
						if val, err := strconv.ParseFloat(parts[1], 64); err == nil {
							metrics = append(metrics, &proto.Metric{
								Name:   "kubernetes.cgroup.memory." + strings.ReplaceAll(parts[0], ".", "_"),
								Type:   proto.MetricType_METRIC_TYPE_COUNTER,
								Labels: []*proto.MetricLabel{{Key: "cgroup", Value: strings.TrimPrefix(path, "/sys/fs/cgroup/")}},
								Points: []*proto.MetricPoint{{Timestamp: now, Value: val}},
							})
						}
					}
				}
			}

			// Read IO stats
			if ioStatData, err := os.ReadFile(path + "/io.stat"); err == nil {
				lines := strings.Split(string(ioStatData), "\n")
				for _, line := range lines {
					if line == "" {
						continue
					}
					parts := strings.Split(line, ":")
					if len(parts) == 2 {
						deviceID := parts[0]
						stats := strings.Fields(parts[1])
						for _, stat := range stats {
							statParts := strings.Split(stat, "=")
							if len(statParts) == 2 {
								if val, err := strconv.ParseFloat(statParts[1], 64); err == nil {
									metrics = append(metrics, &proto.Metric{
										Name: "kubernetes.cgroup.io." + statParts[0],
										Type: proto.MetricType_METRIC_TYPE_COUNTER,
										Labels: []*proto.MetricLabel{
											{Key: "cgroup", Value: strings.TrimPrefix(path, "/sys/fs/cgroup/")},
											{Key: "device", Value: deviceID},
										},
										Points: []*proto.MetricPoint{{Timestamp: now, Value: val}},
									})
								}
							}
						}
					}
				}
			}

			return nil
		})
	}

	return metrics
}

// collectClusterMetrics collects cluster-wide metrics using kubectl
func (k *KubernetesSource) collectClusterMetrics(now *timestamppb.Timestamp) []*proto.Metric {
	metrics := []*proto.Metric{}

	if k.kubectlPath == "" {
		return metrics
	}

	// Get node count
	if nodesJSON := k.runKubectl("get", "nodes", "-o", "json"); nodesJSON != "" {
		var nodes struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}

		if err := json.Unmarshal([]byte(nodesJSON), &nodes); err == nil {
			readyNodes := 0
			for _, node := range nodes.Items {
				for _, cond := range node.Status.Conditions {
					if cond.Type == "Ready" && cond.Status == "True" {
						readyNodes++
						break
					}
				}
			}

			metrics = append(metrics, createK8sGauge("kubernetes.cluster.nodes.total", float64(len(nodes.Items)), now))
			metrics = append(metrics, createK8sGauge("kubernetes.cluster.nodes.ready", float64(readyNodes), now))
		}
	}

	// Get pod count by namespace
	if podsJSON := k.runKubectl("get", "pods", "--all-namespaces", "-o", "json"); podsJSON != "" {
		var pods struct {
			Items []struct {
				Metadata struct {
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
				} `json:"status"`
			} `json:"items"`
		}

		if err := json.Unmarshal([]byte(podsJSON), &pods); err == nil {
			phaseCounts := make(map[string]int)
			namespaceCounts := make(map[string]int)

			for _, pod := range pods.Items {
				phaseCounts[pod.Status.Phase]++
				namespaceCounts[pod.Metadata.Namespace]++
			}

			for phase, count := range phaseCounts {
				metrics = append(metrics, &proto.Metric{
					Name:   "kubernetes.cluster.pods.count",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: []*proto.MetricLabel{{Key: "phase", Value: phase}},
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(count)}},
				})
			}

			for ns, count := range namespaceCounts {
				metrics = append(metrics, &proto.Metric{
					Name:   "kubernetes.cluster.namespace.pods",
					Type:   proto.MetricType_METRIC_TYPE_GAUGE,
					Labels: []*proto.MetricLabel{{Key: "namespace", Value: ns}},
					Points: []*proto.MetricPoint{{Timestamp: now, Value: float64(count)}},
				})
			}

			metrics = append(metrics, createK8sGauge("kubernetes.cluster.pods.total", float64(len(pods.Items)), now))
		}
	}

	return metrics
}

// Helper functions

func (k *KubernetesSource) runKubectl(args ...string) string {
	if k.kubectlPath == "" {
		return ""
	}

	cmdArgs := []string{}
	if k.config.KubeconfigPath != "" {
		cmdArgs = append(cmdArgs, "--kubeconfig="+k.config.KubeconfigPath)
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command(k.kubectlPath, cmdArgs...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

func (k *KubernetesSource) readCgroupValue(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	val, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}

	return val
}

func (k *KubernetesSource) countContainers() int {
	count := 0

	// Count from cgroup v1
	basePath := "/sys/fs/cgroup/cpu/kubepods"
	if dirs, err := os.ReadDir(basePath); err == nil {
		for _, dir := range dirs {
			if dir.IsDir() {
				subPath := basePath + "/" + dir.Name()
				if subDirs, err := os.ReadDir(subPath); err == nil {
					count += len(subDirs)
				}
			}
		}
	}

	return count
}

func createK8sGauge(name string, value float64, ts *timestamppb.Timestamp) *proto.Metric {
	return &proto.Metric{
		Name: name,
		Type: proto.MetricType_METRIC_TYPE_GAUGE,
		Points: []*proto.MetricPoint{
			{
				Timestamp: ts,
				Value:     value,
			},
		},
	}
}
