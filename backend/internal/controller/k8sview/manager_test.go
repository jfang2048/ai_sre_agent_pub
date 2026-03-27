package k8sview

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"go.uber.org/zap"
)

type fakeSnapshotProvider struct {
	nodes []*ingest.NodeSnapshot
}

func (f *fakeSnapshotProvider) Snapshot() []*ingest.NodeSnapshot {
	out := make([]*ingest.NodeSnapshot, 0, len(f.nodes))
	out = append(out, f.nodes...)
	return out
}

func TestManagerCollectsClusterAndBuildsServiceMap(t *testing.T) {
	now := time.Now()
	store := &fakeSnapshotProvider{
		nodes: []*ingest.NodeSnapshot{
			{
				CollectorID: "collector-a",
				Hostname:    "node-a",
				LastSeen:    now,
				Metrics: map[string]float64{
					"node_cpu_usage_percent":                   72,
					"node_memory_MemTotal_bytes":               100,
					"node_memory_Used_bytes":                   68,
					"node_gpu_utilization_sm_avg_percent":      65,
					"node_gpu_memory_used_total_mib":           2048,
					"node_network_receive_bytes_per_second":    1024,
					"node_network_transmit_bytes_per_second":   2048,
					"node_disk_total_iops_per_second":          100,
					"node_disk_utilization_peak_percent":       40,
					"node_disk_total_written_bytes_per_second": 1024,
				},
				Processes: []*telemetryv1.ProcessSample{
					{Pid: 1001, Name: "trainer", CpuPercent: 88, RssBytes: 1024},
				},
				Logs: []*telemetryv1.LogFingerprint{
					{Fingerprint: "e1", Example: "error: oom", Count: 3},
				},
			},
		},
	}

	client := k8sfake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-a",
				Labels: map[string]string{
					"topology.kubernetes.io/zone": "zone-a",
				},
			},
			Status: corev1.NodeStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceCPU:                    resource.MustParse("16"),
					corev1.ResourceMemory:                 resource.MustParse("64Gi"),
					corev1.ResourcePods:                   resource.MustParse("110"),
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("8"),
				},
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:                    resource.MustParse("15"),
					corev1.ResourceMemory:                 resource.MustParse("60Gi"),
					corev1.ResourcePods:                   resource.MustParse("100"),
					corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("8"),
				},
				Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				NodeInfo: corev1.NodeSystemInfo{
					Architecture:   "amd64",
					OSImage:        "Ubuntu",
					KubeletVersion: "v1.32.0",
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "trainer-0",
				Namespace: "ml",
				Labels: map[string]string{
					"app.kubernetes.io/name": "trainer",
				},
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       "trainer",
						Controller: ptr(true),
					},
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "node-a",
				Containers: []corev1.Container{
					{
						Name: "trainer",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{RestartCount: 1},
				},
			},
		},
	)

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Clusters = []ClusterConfig{
		{Name: "cluster-a", Namespace: "*"},
	}
	mgr := NewManager(cfg, store, zap.NewNop())
	mgr.factory = func(clusterCfg ClusterConfig) (kubernetes.Interface, error) {
		return client, nil
	}
	mgr.ctx = context.Background()
	mgr.refreshOnce("test")

	summaries := mgr.ClusterSummaries()
	if len(summaries) != 1 {
		t.Fatalf("ClusterSummaries() len = %d, want 1", len(summaries))
	}
	if summaries[0].Name != "cluster-a" {
		t.Fatalf("summary cluster name = %q, want cluster-a", summaries[0].Name)
	}
	if summaries[0].NodeCount != 1 {
		t.Fatalf("summary node count = %d, want 1", summaries[0].NodeCount)
	}
	if summaries[0].WorkloadCount != 1 {
		t.Fatalf("summary workload count = %d, want 1", summaries[0].WorkloadCount)
	}
	if summaries[0].GPUAllocatableTotal <= 0 {
		t.Fatalf("summary gpu allocatable = %f, want >0", summaries[0].GPUAllocatableTotal)
	}
	if summaries[0].GPURequestedTotal <= 0 {
		t.Fatalf("summary gpu requested = %f, want >0", summaries[0].GPURequestedTotal)
	}

	snapshot, ok := mgr.ClusterSnapshot("cluster-a")
	if !ok {
		t.Fatalf("ClusterSnapshot(cluster-a) missing")
	}
	if len(snapshot.Nodes) != 1 {
		t.Fatalf("snapshot nodes len = %d, want 1", len(snapshot.Nodes))
	}
	if snapshot.Nodes[0].Observed.CPUUsagePercent <= 0 {
		t.Fatalf("node observed cpu = %f, want >0", snapshot.Nodes[0].Observed.CPUUsagePercent)
	}
	if len(snapshot.Workloads) != 1 {
		t.Fatalf("snapshot workloads len = %d, want 1", len(snapshot.Workloads))
	}
	if snapshot.Workloads[0].GPURequests <= 0 {
		t.Fatalf("workload gpu requests = %f, want >0", snapshot.Workloads[0].GPURequests)
	}

	serviceMap := mgr.ServiceMap()
	if len(serviceMap.Nodes) == 0 {
		t.Fatalf("ServiceMap().Nodes empty, want non-empty")
	}
	if len(serviceMap.Edges) == 0 {
		t.Fatalf("ServiceMap().Edges empty, want non-empty")
	}
}

func ptr[T any](v T) *T { return &v }
