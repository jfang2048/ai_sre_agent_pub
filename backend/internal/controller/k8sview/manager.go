package k8sview

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var defaultGPUResourceNames = []corev1.ResourceName{
	"nvidia.com/gpu",
	"amd.com/gpu",
	"intel.com/gpu",
}

// SnapshotProvider is the ingest source used to link K8s inventory with observed metrics/logs.
type SnapshotProvider interface {
	Snapshot() []*ingest.NodeSnapshot
}

// ObservedSignals are linked from probe telemetry.
type ObservedSignals struct {
	CollectorID        string    `json:"collector_id,omitempty"`
	Hostname           string    `json:"hostname,omitempty"`
	LastSeen           time.Time `json:"last_seen,omitempty"`
	CPUUsagePercent    float64   `json:"cpu_usage_percent,omitempty"`
	MemoryUsagePercent float64   `json:"memory_usage_percent,omitempty"`
	GPUUtilPercent     float64   `json:"gpu_util_percent,omitempty"`
	GPUMemoryUsedMiB   float64   `json:"gpu_memory_used_mib,omitempty"`
	NetworkUtilPercent float64   `json:"network_utilization_percent,omitempty"`
	TCPRetransmitRatio float64   `json:"tcp_retransmit_ratio,omitempty"`
	SoftnetDroppedPS   float64   `json:"softnet_dropped_per_second,omitempty"`
	RDMACongestionPS   float64   `json:"rdma_congestion_per_second,omitempty"`
	DiskLatencyP99MS   float64   `json:"disk_latency_p99_ms,omitempty"`
	DiskUtilPercent    float64   `json:"disk_utilization_percent,omitempty"`
	IOPressureFull10   float64   `json:"io_pressure_full_avg10,omitempty"`
	FSSpacePressurePct float64   `json:"filesystem_space_pressure_percent,omitempty"`
	NetworkPressure    float64   `json:"network_pressure_score,omitempty"`
	StoragePressure    float64   `json:"storage_pressure_score,omitempty"`
	LogErrors          uint64    `json:"log_errors,omitempty"`
	LogWarnings        uint64    `json:"log_warnings,omitempty"`
	TopProcesses       []string  `json:"top_processes,omitempty"`
}

// NodeCapacity summarizes node allocatable or capacity resources.
type NodeCapacity struct {
	CPUCores    float64 `json:"cpu_cores,omitempty"`
	MemoryBytes float64 `json:"memory_bytes,omitempty"`
	Pods        float64 `json:"pods,omitempty"`
	GPUs        float64 `json:"gpus,omitempty"`
}

// NodeSummary describes one Kubernetes node.
type NodeSummary struct {
	Name         string            `json:"name"`
	Cluster      string            `json:"cluster"`
	Ready        bool              `json:"ready"`
	Schedulable  bool              `json:"schedulable"`
	Zone         string            `json:"zone,omitempty"`
	Architecture string            `json:"architecture,omitempty"`
	OSImage      string            `json:"os_image,omitempty"`
	Kubelet      string            `json:"kubelet,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Capacity     NodeCapacity      `json:"capacity"`
	Allocatable  NodeCapacity      `json:"allocatable"`
	Observed     ObservedSignals   `json:"observed"`
}

// WorkloadSummary aggregates pods into a workload-level view.
type WorkloadSummary struct {
	Cluster           string    `json:"cluster"`
	Namespace         string    `json:"namespace"`
	Kind              string    `json:"kind"`
	Name              string    `json:"name"`
	Service           string    `json:"service"`
	PodsTotal         int       `json:"pods_total"`
	PodsRunning       int       `json:"pods_running"`
	PodsPending       int       `json:"pods_pending"`
	PodsFailed        int       `json:"pods_failed"`
	ContainerRestarts int64     `json:"container_restarts"`
	GPURequests       float64   `json:"gpu_requests,omitempty"`
	GPULimits         float64   `json:"gpu_limits,omitempty"`
	Nodes             []string  `json:"nodes,omitempty"`
	AvgNodeCPUPercent float64   `json:"avg_node_cpu_percent,omitempty"`
	AvgNodeMemoryPct  float64   `json:"avg_node_memory_percent,omitempty"`
	AvgNodeGPUPercent float64   `json:"avg_node_gpu_percent,omitempty"`
	AvgNodeNetwork    float64   `json:"avg_node_network_pressure,omitempty"`
	AvgNodeStorage    float64   `json:"avg_node_storage_pressure,omitempty"`
	NodeLogErrors     uint64    `json:"node_log_errors,omitempty"`
	NodeLogWarnings   uint64    `json:"node_log_warnings,omitempty"`
	TopProcesses      []string  `json:"top_processes,omitempty"`
	LastObservedPodAt time.Time `json:"last_observed_pod_at,omitempty"`
}

// GPUNodeSummary is node-level GPU scheduling + runtime visibility.
type GPUNodeSummary struct {
	Cluster               string  `json:"cluster"`
	Node                  string  `json:"node"`
	GPUAllocatable        float64 `json:"gpu_allocatable"`
	GPURequested          float64 `json:"gpu_requested"`
	ObservedUtilPercent   float64 `json:"observed_util_percent,omitempty"`
	ObservedMemoryUsedMiB float64 `json:"observed_memory_used_mib,omitempty"`
}

// ClusterSnapshot represents one cluster's latest read-only inventory snapshot.
type ClusterSnapshot struct {
	Name                string            `json:"name"`
	Namespace           string            `json:"namespace,omitempty"`
	Healthy             bool              `json:"healthy"`
	GeneratedAt         time.Time         `json:"generated_at"`
	LastError           string            `json:"last_error,omitempty"`
	Nodes               []NodeSummary     `json:"nodes"`
	Workloads           []WorkloadSummary `json:"workloads"`
	GPUNodes            []GPUNodeSummary  `json:"gpu_nodes"`
	NodeCount           int               `json:"node_count"`
	ReadyNodeCount      int               `json:"ready_node_count"`
	WorkloadCount       int               `json:"workload_count"`
	RunningPodCount     int               `json:"running_pod_count"`
	GPUAllocatableTotal float64           `json:"gpu_allocatable_total"`
	GPURequestedTotal   float64           `json:"gpu_requested_total"`
}

// ClusterSummary is the compact view used in list APIs.
type ClusterSummary struct {
	Name                string    `json:"name"`
	Namespace           string    `json:"namespace,omitempty"`
	Healthy             bool      `json:"healthy"`
	GeneratedAt         time.Time `json:"generated_at"`
	LastError           string    `json:"last_error,omitempty"`
	NodeCount           int       `json:"node_count"`
	ReadyNodeCount      int       `json:"ready_node_count"`
	WorkloadCount       int       `json:"workload_count"`
	RunningPodCount     int       `json:"running_pod_count"`
	GPUAllocatableTotal float64   `json:"gpu_allocatable_total"`
	GPURequestedTotal   float64   `json:"gpu_requested_total"`
}

// MetricsSnapshot captures manager-level metrics.
type MetricsSnapshot struct {
	RefreshTotal        uint64  `json:"refresh_total"`
	RefreshFailedTotal  uint64  `json:"refresh_failed_total"`
	ClustersConfigured  int     `json:"clusters_configured"`
	ClustersHealthy     int     `json:"clusters_healthy"`
	NodesTotal          int     `json:"nodes_total"`
	WorkloadsTotal      int     `json:"workloads_total"`
	GPUAllocatableTotal float64 `json:"gpu_allocatable_total"`
	GPURequestedTotal   float64 `json:"gpu_requested_total"`
}

// Status is runtime status for API exposure.
type Status struct {
	Enabled         bool            `json:"enabled"`
	Running         bool            `json:"running"`
	RefreshInterval string          `json:"refresh_interval"`
	RequestTimeout  string          `json:"request_timeout"`
	LastRefreshAt   time.Time       `json:"last_refresh_at,omitempty"`
	Metrics         MetricsSnapshot `json:"metrics"`
}

// ServiceMapNode is one node in the service->node->process->gpu topology graph.
type ServiceMapNode struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Type      string  `json:"type"` // cluster | node | workload | process | gpu
	Cluster   string  `json:"cluster,omitempty"`
	Namespace string  `json:"namespace,omitempty"`
	Status    string  `json:"status,omitempty"`
	Score     float64 `json:"score,omitempty"`
}

// ServiceMapEdge is one directed relation.
type ServiceMapEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"` // contains | runs_on | uses | attached
}

// ServiceMap is the unified graph payload.
type ServiceMap struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Nodes       []ServiceMapNode `json:"nodes"`
	Edges       []ServiceMapEdge `json:"edges"`
}

type clientFactory func(ClusterConfig) (kubernetes.Interface, error)

// Manager maintains periodic, read-only Kubernetes cluster snapshots.
type Manager struct {
	cfg    Config
	store  SnapshotProvider
	logger *zap.Logger

	mu          sync.RWMutex
	clients     map[string]kubernetes.Interface
	snapshots   map[string]ClusterSnapshot
	lastErrors  map[string]string
	metrics     MetricsSnapshot
	lastRefresh time.Time
	running     bool

	factory clientFactory

	ctx     context.Context
	cancel  context.CancelFunc
	refresh chan struct{}
}

// NewManager creates a new Kubernetes integration manager.
func NewManager(cfg Config, store SnapshotProvider, logger *zap.Logger) *Manager {
	cfg = normalizeConfig(cfg)
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Manager{
		cfg:        cfg,
		store:      store,
		logger:     logger.With(zap.String("component", "k8s_integration")),
		clients:    make(map[string]kubernetes.Interface),
		snapshots:  make(map[string]ClusterSnapshot),
		lastErrors: make(map[string]string),
		factory:    newClusterClient,
		refresh:    make(chan struct{}, 1),
	}
}

// Start starts periodic refresh.
func (m *Manager) Start(ctx context.Context) error {
	if !m.cfg.Enabled {
		return nil
	}

	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("kubernetes integration already running")
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.running = true
	m.mu.Unlock()

	go m.loop()
	m.RefreshNow()
	return nil
}

// Stop stops periodic refresh.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.running = false
}

// RefreshNow forces a synchronous refresh.
func (m *Manager) RefreshNow() {
	if !m.cfg.Enabled {
		return
	}
	m.refreshOnce("manual")
}

// TriggerRefresh requests asynchronous refresh.
func (m *Manager) TriggerRefresh() {
	select {
	case m.refresh <- struct{}{}:
	default:
	}
}

// Status returns manager status.
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Status{
		Enabled:         m.cfg.Enabled,
		Running:         m.running,
		RefreshInterval: m.cfg.RefreshInterval.String(),
		RequestTimeout:  m.cfg.RequestTimeout.String(),
		LastRefreshAt:   m.lastRefresh,
		Metrics:         m.metrics,
	}
}

// Metrics returns manager metrics.
func (m *Manager) Metrics() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metrics
}

// ClusterSummaries returns compact summaries for all clusters.
func (m *Manager) ClusterSummaries() []ClusterSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ClusterSummary, 0, len(m.snapshots))
	for _, snap := range m.snapshots {
		out = append(out, ClusterSummary{
			Name:                snap.Name,
			Namespace:           snap.Namespace,
			Healthy:             snap.Healthy,
			GeneratedAt:         snap.GeneratedAt,
			LastError:           snap.LastError,
			NodeCount:           snap.NodeCount,
			ReadyNodeCount:      snap.ReadyNodeCount,
			WorkloadCount:       snap.WorkloadCount,
			RunningPodCount:     snap.RunningPodCount,
			GPUAllocatableTotal: snap.GPUAllocatableTotal,
			GPURequestedTotal:   snap.GPURequestedTotal,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ClusterSnapshot returns one cluster snapshot by name.
func (m *Manager) ClusterSnapshot(name string) (ClusterSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snap, ok := m.snapshots[name]
	return snap, ok
}

// Snapshots returns all cluster snapshots sorted by cluster name.
func (m *Manager) Snapshots() []ClusterSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ClusterSnapshot, 0, len(m.snapshots))
	for _, snap := range m.snapshots {
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ServiceMap returns a unified graph from current snapshots.
func (m *Manager) ServiceMap() ServiceMap {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nodeByID := make(map[string]ServiceMapNode)
	edges := make([]ServiceMapEdge, 0)
	edgeSeen := make(map[string]struct{})

	addNode := func(n ServiceMapNode) {
		if n.ID == "" {
			return
		}
		if _, ok := nodeByID[n.ID]; ok {
			return
		}
		nodeByID[n.ID] = n
	}
	addEdge := func(source, target, kind string) {
		if source == "" || target == "" {
			return
		}
		k := source + "->" + target + ":" + kind
		if _, ok := edgeSeen[k]; ok {
			return
		}
		edgeSeen[k] = struct{}{}
		edges = append(edges, ServiceMapEdge{Source: source, Target: target, Kind: kind})
	}

	for _, snap := range m.snapshots {
		clusterID := "cluster:" + snap.Name
		status := "degraded"
		if snap.Healthy {
			status = "healthy"
		}
		addNode(ServiceMapNode{
			ID:      clusterID,
			Name:    snap.Name,
			Type:    "cluster",
			Cluster: snap.Name,
			Status:  status,
		})

		for _, node := range snap.Nodes {
			nodeID := "node:" + snap.Name + ":" + node.Name
			nodeStatus := "degraded"
			if node.Ready {
				nodeStatus = "healthy"
			}
			score := maxFloat(node.Observed.CPUUsagePercent, node.Observed.MemoryUsagePercent, node.Observed.GPUUtilPercent)
			addNode(ServiceMapNode{
				ID:      nodeID,
				Name:    node.Name,
				Type:    "node",
				Cluster: snap.Name,
				Status:  nodeStatus,
				Score:   score,
			})
			addEdge(clusterID, nodeID, "contains")
		}

		for _, workload := range snap.Workloads {
			workloadID := "workload:" + snap.Name + ":" + workload.Namespace + ":" + workload.Kind + ":" + workload.Name
			status := "degraded"
			if workload.PodsTotal > 0 && workload.PodsRunning == workload.PodsTotal {
				status = "healthy"
			}
			score := maxFloat(workload.AvgNodeCPUPercent, workload.AvgNodeMemoryPct, workload.AvgNodeGPUPercent)
			if workload.NodeLogErrors > 0 {
				score = maxFloat(score, 90)
			}
			addNode(ServiceMapNode{
				ID:        workloadID,
				Name:      workload.Service,
				Type:      "workload",
				Cluster:   snap.Name,
				Namespace: workload.Namespace,
				Status:    status,
				Score:     score,
			})
			for _, nodeName := range workload.Nodes {
				nodeID := "node:" + snap.Name + ":" + nodeName
				addEdge(nodeID, workloadID, "runs_on")
			}

			for _, process := range workload.TopProcesses {
				processID := "process:" + snap.Name + ":" + workload.Namespace + ":" + workload.Name + ":" + process
				addNode(ServiceMapNode{
					ID:        processID,
					Name:      process,
					Type:      "process",
					Cluster:   snap.Name,
					Namespace: workload.Namespace,
				})
				addEdge(workloadID, processID, "uses")
			}
		}

		for _, gpu := range snap.GPUNodes {
			gpuID := "gpu:" + snap.Name + ":" + gpu.Node
			addNode(ServiceMapNode{
				ID:      gpuID,
				Name:    gpu.Node,
				Type:    "gpu",
				Cluster: snap.Name,
				Status:  "healthy",
				Score:   gpu.ObservedUtilPercent,
			})
			nodeID := "node:" + snap.Name + ":" + gpu.Node
			addEdge(nodeID, gpuID, "attached")
		}
	}

	nodes := make([]ServiceMapNode, 0, len(nodeByID))
	for _, n := range nodeByID {
		nodes = append(nodes, n)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		if edges[i].Target != edges[j].Target {
			return edges[i].Target < edges[j].Target
		}
		return edges[i].Kind < edges[j].Kind
	})

	return ServiceMap{
		GeneratedAt: time.Now(),
		Nodes:       nodes,
		Edges:       edges,
	}
}

func (m *Manager) loop() {
	ticker := time.NewTicker(m.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.refreshOnce("periodic")
		case <-m.refresh:
			m.refreshOnce("trigger")
		}
	}
}

func (m *Manager) refreshOnce(reason string) {
	now := time.Now()
	m.logger.Debug("refreshing kubernetes integration snapshot", zap.String("reason", reason))

	observed := deriveObservedNodes(m.store)
	configured := map[string]ClusterConfig{}
	for _, cluster := range m.cfg.Clusters {
		name := strings.TrimSpace(cluster.Name)
		if name == "" {
			continue
		}
		configured[name] = cluster
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.metrics.RefreshTotal++
	m.lastRefresh = now

	for name := range m.clients {
		if _, ok := configured[name]; !ok {
			delete(m.clients, name)
			delete(m.snapshots, name)
			delete(m.lastErrors, name)
		}
	}

	for name, cluster := range configured {
		client := m.clients[name]
		if client == nil {
			created, err := m.factory(cluster)
			if err != nil {
				m.lastErrors[name] = err.Error()
				m.snapshots[name] = ClusterSnapshot{
					Name:        name,
					Namespace:   normalizedNamespace(cluster.Namespace),
					Healthy:     false,
					GeneratedAt: now,
					LastError:   err.Error(),
					Nodes:       []NodeSummary{},
					Workloads:   []WorkloadSummary{},
					GPUNodes:    []GPUNodeSummary{},
				}
				m.metrics.RefreshFailedTotal++
				continue
			}
			m.clients[name] = created
			client = created
		}

		snap, err := m.collectCluster(m.ctx, name, cluster, client, observed)
		if err != nil {
			m.lastErrors[name] = err.Error()
			snap.Healthy = false
			snap.LastError = err.Error()
			m.metrics.RefreshFailedTotal++
		}
		m.snapshots[name] = snap
	}

	m.recomputeMetricsLocked()
}

func (m *Manager) collectCluster(ctx context.Context, name string, clusterCfg ClusterConfig, client kubernetes.Interface, observed map[string]ObservedSignals) (ClusterSnapshot, error) {
	timeout := m.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSnapshot{Name: name, Namespace: normalizedNamespace(clusterCfg.Namespace), GeneratedAt: time.Now(), Nodes: []NodeSummary{}, Workloads: []WorkloadSummary{}, GPUNodes: []GPUNodeSummary{}}, err
	}

	namespace := normalizedNamespace(clusterCfg.Namespace)
	podNS := namespace
	if namespace == "*" {
		podNS = ""
	}
	pods, err := client.CoreV1().Pods(podNS).List(ctx, metav1.ListOptions{LabelSelector: strings.TrimSpace(clusterCfg.LabelSelector)})
	if err != nil {
		return ClusterSnapshot{Name: name, Namespace: namespace, GeneratedAt: time.Now(), Nodes: []NodeSummary{}, Workloads: []WorkloadSummary{}, GPUNodes: []GPUNodeSummary{}}, err
	}

	maxPods := m.cfg.MaxPodsPerCluster
	if maxPods > 0 && len(pods.Items) > maxPods {
		pods.Items = pods.Items[:maxPods]
	}

	nodeSummaries := make([]NodeSummary, 0, len(nodes.Items))
	nodeReady := 0
	nodeGPURequested := make(map[string]float64)
	gpuByNode := map[string]GPUNodeSummary{}

	for _, node := range nodes.Items {
		ready := nodeReadyCondition(node)
		if ready {
			nodeReady++
		}

		obs := observed[node.Name]
		if obs.CollectorID == "" {
			obs = observed[node.Labels["kubernetes.io/hostname"]]
		}

		capacity := summarizeCapacity(node.Status.Capacity)
		alloc := summarizeCapacity(node.Status.Allocatable)

		nodeSummaries = append(nodeSummaries, NodeSummary{
			Name:         node.Name,
			Cluster:      name,
			Ready:        ready,
			Schedulable:  !node.Spec.Unschedulable,
			Zone:         firstNonEmpty(node.Labels["topology.kubernetes.io/zone"], node.Labels["failure-domain.beta.kubernetes.io/zone"]),
			Architecture: node.Status.NodeInfo.Architecture,
			OSImage:      node.Status.NodeInfo.OSImage,
			Kubelet:      node.Status.NodeInfo.KubeletVersion,
			Labels:       compactNodeLabels(node.Labels),
			Capacity:     capacity,
			Allocatable:  alloc,
			Observed:     obs,
		})

		if alloc.GPUs > 0 || obs.GPUUtilPercent > 0 || obs.GPUMemoryUsedMiB > 0 {
			gpuByNode[node.Name] = GPUNodeSummary{
				Cluster:               name,
				Node:                  node.Name,
				GPUAllocatable:        alloc.GPUs,
				ObservedUtilPercent:   obs.GPUUtilPercent,
				ObservedMemoryUsedMiB: obs.GPUMemoryUsedMiB,
			}
		}
	}
	sort.Slice(nodeSummaries, func(i, j int) bool { return nodeSummaries[i].Name < nodeSummaries[j].Name })

	workloads := aggregateWorkloads(name, pods.Items, observed, nodeGPURequested)
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Namespace != workloads[j].Namespace {
			return workloads[i].Namespace < workloads[j].Namespace
		}
		if workloads[i].Service != workloads[j].Service {
			return workloads[i].Service < workloads[j].Service
		}
		if workloads[i].Kind != workloads[j].Kind {
			return workloads[i].Kind < workloads[j].Kind
		}
		return workloads[i].Name < workloads[j].Name
	})

	gpuNodes := make([]GPUNodeSummary, 0, len(gpuByNode))
	gpuAllocTotal := 0.0
	gpuReqTotal := 0.0
	for nodeName, gpu := range gpuByNode {
		gpu.GPURequested = nodeGPURequested[nodeName]
		gpuAllocTotal += gpu.GPUAllocatable
		gpuReqTotal += gpu.GPURequested
		gpuNodes = append(gpuNodes, gpu)
	}
	sort.Slice(gpuNodes, func(i, j int) bool { return gpuNodes[i].Node < gpuNodes[j].Node })

	runningPods := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			runningPods++
		}
	}

	snap := ClusterSnapshot{
		Name:                name,
		Namespace:           namespace,
		Healthy:             true,
		GeneratedAt:         time.Now(),
		Nodes:               nodeSummaries,
		Workloads:           workloads,
		GPUNodes:            gpuNodes,
		NodeCount:           len(nodeSummaries),
		ReadyNodeCount:      nodeReady,
		WorkloadCount:       len(workloads),
		RunningPodCount:     runningPods,
		GPUAllocatableTotal: gpuAllocTotal,
		GPURequestedTotal:   gpuReqTotal,
	}
	return snap, nil
}

func aggregateWorkloads(cluster string, pods []corev1.Pod, observed map[string]ObservedSignals, nodeGPURequested map[string]float64) []WorkloadSummary {
	type aggregate struct {
		summary WorkloadSummary
		nodes   map[string]struct{}
		procs   map[string]struct{}
	}

	items := map[string]*aggregate{}
	for _, pod := range pods {
		kind, name := workloadOwner(pod)
		service := workloadServiceName(pod.Labels, name)
		key := pod.Namespace + "|" + kind + "|" + name
		entry, ok := items[key]
		if !ok {
			entry = &aggregate{
				summary: WorkloadSummary{
					Cluster:   cluster,
					Namespace: pod.Namespace,
					Kind:      kind,
					Name:      name,
					Service:   service,
				},
				nodes: map[string]struct{}{},
				procs: map[string]struct{}{},
			}
			items[key] = entry
		}

		entry.summary.PodsTotal++
		switch pod.Status.Phase {
		case corev1.PodRunning:
			entry.summary.PodsRunning++
		case corev1.PodPending:
			entry.summary.PodsPending++
		case corev1.PodFailed:
			entry.summary.PodsFailed++
		}
		if pod.CreationTimestamp.Time.After(entry.summary.LastObservedPodAt) {
			entry.summary.LastObservedPodAt = pod.CreationTimestamp.Time
		}

		nodeName := strings.TrimSpace(pod.Spec.NodeName)
		if nodeName != "" {
			entry.nodes[nodeName] = struct{}{}
		}

		requests, limits := podGPUResources(pod)
		entry.summary.GPURequests += requests
		entry.summary.GPULimits += limits
		if nodeName != "" {
			nodeGPURequested[nodeName] += requests
		}

		for _, st := range pod.Status.ContainerStatuses {
			entry.summary.ContainerRestarts += int64(st.RestartCount)
		}

		if nodeName != "" {
			obs := observed[nodeName]
			entry.summary.AvgNodeCPUPercent += obs.CPUUsagePercent
			entry.summary.AvgNodeMemoryPct += obs.MemoryUsagePercent
			entry.summary.AvgNodeGPUPercent += obs.GPUUtilPercent
			entry.summary.AvgNodeNetwork += obs.NetworkPressure
			entry.summary.AvgNodeStorage += obs.StoragePressure
			entry.summary.NodeLogErrors += obs.LogErrors
			entry.summary.NodeLogWarnings += obs.LogWarnings
			for _, p := range obs.TopProcesses {
				entry.procs[p] = struct{}{}
			}
		}
	}

	result := make([]WorkloadSummary, 0, len(items))
	for _, entry := range items {
		nodes := make([]string, 0, len(entry.nodes))
		for nodeName := range entry.nodes {
			nodes = append(nodes, nodeName)
		}
		sort.Strings(nodes)
		entry.summary.Nodes = nodes

		nodeCount := float64(len(nodes))
		if nodeCount > 0 {
			entry.summary.AvgNodeCPUPercent /= nodeCount
			entry.summary.AvgNodeMemoryPct /= nodeCount
			entry.summary.AvgNodeGPUPercent /= nodeCount
			entry.summary.AvgNodeNetwork /= nodeCount
			entry.summary.AvgNodeStorage /= nodeCount
		}

		procs := make([]string, 0, len(entry.procs))
		for p := range entry.procs {
			procs = append(procs, p)
		}
		sort.Strings(procs)
		if len(procs) > 5 {
			procs = procs[:5]
		}
		entry.summary.TopProcesses = procs

		result = append(result, entry.summary)
	}

	return result
}

func deriveObservedNodes(store SnapshotProvider) map[string]ObservedSignals {
	if store == nil {
		return map[string]ObservedSignals{}
	}

	snapshots := store.Snapshot()
	out := make(map[string]ObservedSignals, len(snapshots))
	for _, node := range snapshots {
		if node == nil {
			continue
		}
		rdmaCongestion := node.Metrics["node_rdma_congestion_events_per_second"]
		if rdmaCongestion <= 0 {
			rdmaCongestion = node.Metrics["node_rdma_port_congestion_events_per_second"]
		}
		observed := ObservedSignals{
			CollectorID:        node.CollectorID,
			Hostname:           node.Hostname,
			LastSeen:           firstNonZeroTime(node.LastSeen, node.UpdatedAt),
			CPUUsagePercent:    node.Metrics["node_cpu_usage_percent"],
			GPUUtilPercent:     node.Metrics["node_gpu_utilization_sm_avg_percent"],
			GPUMemoryUsedMiB:   node.Metrics["node_gpu_memory_used_total_mib"],
			NetworkUtilPercent: node.Metrics["node_network_utilization_peak_percent"],
			TCPRetransmitRatio: node.Metrics["node_tcp_retransmit_ratio"],
			SoftnetDroppedPS:   node.Metrics["node_softnet_dropped_per_second"],
			RDMACongestionPS:   rdmaCongestion,
			DiskLatencyP99MS:   node.Metrics["node_disk_request_latency_p99_seconds"] * 1000.0,
			DiskUtilPercent:    node.Metrics["node_disk_utilization_peak_percent"],
			IOPressureFull10:   node.Metrics["node_pressure_io_full_avg10"],
			FSSpacePressurePct: node.Metrics["node_filesystem_space_pressure_percent"],
			TopProcesses:       topProcessNames(node.Processes),
		}
		observed.NetworkPressure = observedNetworkPressureScore(node.Metrics)
		observed.StoragePressure = observedStoragePressureScore(node.Metrics)

		totalMem := node.Metrics["node_memory_MemTotal_bytes"]
		usedMem := node.Metrics["node_memory_Used_bytes"]
		availMem := node.Metrics["node_memory_MemAvailable_bytes"]
		if totalMem > 0 && usedMem > 0 {
			observed.MemoryUsagePercent = clampPercent((usedMem / totalMem) * 100)
		} else if totalMem > 0 && availMem > 0 {
			used := totalMem - availMem
			if used < 0 {
				used = 0
			}
			observed.MemoryUsagePercent = clampPercent((used / totalMem) * 100)
		}

		for _, log := range node.Logs {
			if log == nil {
				continue
			}
			example := strings.ToLower(strings.TrimSpace(log.Example))
			switch {
			case strings.Contains(example, "error"), strings.Contains(example, "fatal"), strings.Contains(example, "panic"):
				observed.LogErrors += log.Count
			case strings.Contains(example, "warn"):
				observed.LogWarnings += log.Count
			}
		}

		keys := []string{
			strings.TrimSpace(node.Hostname),
			strings.TrimSpace(node.Labels["node"]),
			strings.TrimSpace(node.Labels["k8s_node"]),
			strings.TrimSpace(node.Labels["kubernetes_node"]),
			strings.TrimSpace(node.Labels["kubernetes.io/hostname"]),
		}
		for _, key := range keys {
			if key == "" {
				continue
			}
			prev, exists := out[key]
			if exists && prev.LastSeen.After(observed.LastSeen) {
				continue
			}
			out[key] = observed
		}
	}
	return out
}

func summarizeCapacity(resources corev1.ResourceList) NodeCapacity {
	return NodeCapacity{
		CPUCores:    resourceCPU(resources),
		MemoryBytes: resourceMemory(resources),
		Pods:        resourcePods(resources),
		GPUs:        resourceGPU(resources),
	}
}

func compactNodeLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	keys := []string{
		"kubernetes.io/hostname",
		"topology.kubernetes.io/zone",
		"topology.kubernetes.io/region",
		"node.kubernetes.io/instance-type",
		"kubernetes.io/arch",
	}
	out := make(map[string]string)
	for _, key := range keys {
		if value := strings.TrimSpace(labels[key]); value != "" {
			out[key] = value
		}
	}
	return out
}

func workloadOwner(pod corev1.Pod) (string, string) {
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return ref.Kind, ref.Name
		}
	}
	if len(pod.OwnerReferences) > 0 {
		ref := pod.OwnerReferences[0]
		return ref.Kind, ref.Name
	}
	return "Pod", pod.Name
}

func workloadServiceName(labels map[string]string, fallback string) string {
	if labels == nil {
		return fallback
	}
	for _, key := range []string{"app.kubernetes.io/name", "app", "service", "app.kubernetes.io/instance"} {
		if value := strings.TrimSpace(labels[key]); value != "" {
			return value
		}
	}
	return fallback
}

func podGPUResources(pod corev1.Pod) (float64, float64) {
	requests := 0.0
	limits := 0.0
	for _, c := range pod.Spec.Containers {
		for _, resourceName := range defaultGPUResourceNames {
			if quantity, ok := c.Resources.Requests[resourceName]; ok {
				requests += quantity.AsApproximateFloat64()
			}
			if quantity, ok := c.Resources.Limits[resourceName]; ok {
				limits += quantity.AsApproximateFloat64()
			}
		}
		for name, quantity := range c.Resources.Requests {
			if strings.HasSuffix(string(name), "/gpu") && !isKnownGPUName(name) {
				requests += quantity.AsApproximateFloat64()
			}
		}
		for name, quantity := range c.Resources.Limits {
			if strings.HasSuffix(string(name), "/gpu") && !isKnownGPUName(name) {
				limits += quantity.AsApproximateFloat64()
			}
		}
	}
	return requests, limits
}

func isKnownGPUName(name corev1.ResourceName) bool {
	for _, known := range defaultGPUResourceNames {
		if name == known {
			return true
		}
	}
	return false
}

func topProcessNames(processes []*telemetryv1.ProcessSample) []string {
	if len(processes) == 0 {
		return nil
	}
	sorted := append([]*telemetryv1.ProcessSample(nil), processes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].CpuPercent != sorted[j].CpuPercent {
			return sorted[i].CpuPercent > sorted[j].CpuPercent
		}
		if sorted[i].RssBytes != sorted[j].RssBytes {
			return sorted[i].RssBytes > sorted[j].RssBytes
		}
		return sorted[i].Name < sorted[j].Name
	})

	seen := make(map[string]struct{}, 5)
	out := make([]string, 0, 5)
	for _, process := range sorted {
		if process == nil {
			continue
		}
		name := strings.TrimSpace(process.Name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
		if len(out) >= 5 {
			break
		}
	}
	return out
}

func nodeReadyCondition(node corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func resourceCPU(resources corev1.ResourceList) float64 {
	if quantity, ok := resources[corev1.ResourceCPU]; ok {
		return float64(quantity.MilliValue()) / 1000.0
	}
	return 0
}

func resourceMemory(resources corev1.ResourceList) float64 {
	if quantity, ok := resources[corev1.ResourceMemory]; ok {
		return float64(quantity.Value())
	}
	return 0
}

func resourcePods(resources corev1.ResourceList) float64 {
	if quantity, ok := resources[corev1.ResourcePods]; ok {
		return quantity.AsApproximateFloat64()
	}
	return 0
}

func resourceGPU(resources corev1.ResourceList) float64 {
	total := 0.0
	for _, name := range defaultGPUResourceNames {
		if quantity, ok := resources[name]; ok {
			total += quantity.AsApproximateFloat64()
		}
	}
	for name, quantity := range resources {
		if strings.HasSuffix(string(name), "/gpu") && !isKnownGPUName(name) {
			total += quantity.AsApproximateFloat64()
		}
	}
	return total
}

func newClusterClient(cfg ClusterConfig) (kubernetes.Interface, error) {
	var restCfg *rest.Config
	var err error

	if cfg.InCluster {
		restCfg, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("cluster %s in-cluster config: %w", cfg.Name, err)
		}
	} else {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: strings.TrimSpace(cfg.Kubeconfig)}
		overrides := &clientcmd.ConfigOverrides{}
		if strings.TrimSpace(cfg.Context) != "" {
			overrides.CurrentContext = strings.TrimSpace(cfg.Context)
		}
		clientCfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
		restCfg, err = clientCfg.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("cluster %s kubeconfig: %w", cfg.Name, err)
		}
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("cluster %s client creation: %w", cfg.Name, err)
	}
	return clientset, nil
}

func normalizeConfig(cfg Config) Config {
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 20 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 6 * time.Second
	}
	if cfg.MaxPodsPerCluster <= 0 {
		cfg.MaxPodsPerCluster = 5000
	}
	for i := range cfg.Clusters {
		cfg.Clusters[i].Name = strings.TrimSpace(cfg.Clusters[i].Name)
		cfg.Clusters[i].Namespace = normalizedNamespace(cfg.Clusters[i].Namespace)
		cfg.Clusters[i].LabelSelector = strings.TrimSpace(cfg.Clusters[i].LabelSelector)
	}
	return cfg
}

func normalizedNamespace(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "*"
	}
	return namespace
}

func (m *Manager) recomputeMetricsLocked() {
	metrics := MetricsSnapshot{
		RefreshTotal:       m.metrics.RefreshTotal,
		RefreshFailedTotal: m.metrics.RefreshFailedTotal,
		ClustersConfigured: len(m.cfg.Clusters),
	}

	for _, snap := range m.snapshots {
		if snap.Healthy {
			metrics.ClustersHealthy++
		}
		metrics.NodesTotal += snap.NodeCount
		metrics.WorkloadsTotal += snap.WorkloadCount
		metrics.GPUAllocatableTotal += snap.GPUAllocatableTotal
		metrics.GPURequestedTotal += snap.GPURequestedTotal
	}
	m.metrics = metrics
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func observedNetworkPressureScore(metrics map[string]float64) float64 {
	if len(metrics) == 0 {
		return 0
	}
	util := metrics["node_network_utilization_peak_percent"]
	retrans := metrics["node_tcp_retransmit_ratio"]
	softnetDrop := metrics["node_softnet_dropped_per_second"]
	rdmaCong := metrics["node_rdma_congestion_events_per_second"]
	if rdmaCong <= 0 {
		rdmaCong = metrics["node_rdma_port_congestion_events_per_second"]
	}
	score := 0.0
	score += clampUnit(util/100.0) * 2.0
	score += clampUnit(retrans/0.02) * 1.8
	score += clampUnit(softnetDrop/200.0) * 1.3
	score += clampUnit(rdmaCong/120.0) * 1.2
	return score
}

func observedStoragePressureScore(metrics map[string]float64) float64 {
	if len(metrics) == 0 {
		return 0
	}
	util := metrics["node_disk_utilization_peak_percent"]
	latencyMs := metrics["node_disk_request_latency_p99_seconds"] * 1000.0
	ioPressure := metrics["node_pressure_io_full_avg10"]
	fsPressure := metrics["node_filesystem_space_pressure_percent"]
	score := 0.0
	score += clampUnit(util/100.0) * 1.8
	score += clampUnit(latencyMs/80.0) * 1.8
	score += clampUnit(ioPressure/20.0) * 1.4
	score += clampUnit(fsPressure/100.0) * 1.0
	return score
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func maxFloat(values ...float64) float64 {
	if len(values) == 0 {
		return 0
	}
	best := values[0]
	for i := 1; i < len(values); i++ {
		if values[i] > best {
			best = values[i]
		}
	}
	return best
}
