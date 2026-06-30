package orchestration

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

type snapshotStore struct {
	mu    sync.RWMutex
	nodes []*ingest.NodeSnapshot
}

func (s *snapshotStore) Snapshot() []*ingest.NodeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ingest.NodeSnapshot, 0, len(s.nodes))
	for _, node := range s.nodes {
		if node == nil {
			continue
		}
		clone := *node
		clone.Metrics = cloneMetrics(node.Metrics)
		clone.Labels = cloneLabels(node.Labels)
		out = append(out, &clone)
	}
	return out
}

func (s *snapshotStore) set(nodes ...*ingest.NodeSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes = nodes
}

func TestManagerPrefersRealtimeBeforeBatch(t *testing.T) {
	store := &snapshotStore{}
	store.set(testNodeSnapshot("node-a", 15, 64, 56, time.Now(), map[string]string{"resource.cpu.capacity": "8"}))

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.PeakPressureThreshold = 0.95
	cfg.SafetyMarginRatio = 0
	mgr := NewManager(cfg, store, zap.NewNop())

	batch, err := mgr.Submit(WorkloadSpec{
		Service:  "embedding",
		Class:    WorkloadClassBatch,
		Priority: PriorityP0,
		Requested: ResourceVector{
			CPUCores:    6,
			MemoryBytes: 4 * 1024 * 1024 * 1024,
		},
	})
	if err != nil {
		t.Fatalf("submit batch: %v", err)
	}
	realtime, err := mgr.Submit(WorkloadSpec{
		Service:  "chat",
		Class:    WorkloadClassRealtime,
		Priority: PriorityP2,
		Requested: ResourceVector{
			CPUCores:    6,
			MemoryBytes: 4 * 1024 * 1024 * 1024,
		},
	})
	if err != nil {
		t.Fatalf("submit realtime: %v", err)
	}

	mgr.ReconcileNow()

	gotRealtime, ok := mgr.GetWorkload(realtime.Spec.ID)
	if !ok {
		t.Fatalf("realtime workload missing")
	}
	if gotRealtime.State != WorkloadStateRunning {
		t.Fatalf("realtime state = %s, want %s", gotRealtime.State, WorkloadStateRunning)
	}

	gotBatch, ok := mgr.GetWorkload(batch.Spec.ID)
	if !ok {
		t.Fatalf("batch workload missing")
	}
	if gotBatch.State == WorkloadStateRunning {
		t.Fatalf("batch should not run when capacity is exhausted by realtime")
	}
}

func TestManagerDefersBatchDuringPeakPressure(t *testing.T) {
	store := &snapshotStore{}
	store.set(testNodeSnapshot("node-a", 95, 64, 6, time.Now(), map[string]string{"resource.cpu.capacity": "8"}))

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.PeakPressureThreshold = 0.7
	cfg.SafetyMarginRatio = 0
	mgr := NewManager(cfg, store, zap.NewNop())

	deadline := time.Now().Add(15 * time.Minute)
	w, err := mgr.Submit(WorkloadSpec{
		Service:  "offline-train",
		Class:    WorkloadClassBatch,
		Priority: PriorityP2,
		Deadline: &deadline,
		Requested: ResourceVector{
			CPUCores:    1,
			MemoryBytes: 2 * 1024 * 1024 * 1024,
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	mgr.ReconcileNow()
	got, ok := mgr.GetWorkload(w.Spec.ID)
	if !ok {
		t.Fatalf("workload missing")
	}
	if got.State != WorkloadStateDeferred {
		t.Fatalf("state = %s, want %s", got.State, WorkloadStateDeferred)
	}
	if !strings.Contains(strings.ToLower(got.Reason), "deferred") {
		t.Fatalf("reason = %q, want deferred hint", got.Reason)
	}
}

func TestManagerPrefersWarmModelCacheForReuse(t *testing.T) {
	store := &snapshotStore{}
	now := time.Now()
	store.set(
		testNodeSnapshot("node-a", 10, 64, 56, now, map[string]string{"resource.cpu.capacity": "8", "zone": "z1"}),
		testNodeSnapshot("node-b", 10, 64, 56, now, map[string]string{"resource.cpu.capacity": "8", "zone": "z2"}),
	)

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.SafetyMarginRatio = 0
	mgr := NewManager(cfg, store, zap.NewNop())

	first, err := mgr.Submit(WorkloadSpec{
		Service:  "llm-gateway",
		Model:    "model-a",
		Class:    WorkloadClassRealtime,
		Priority: PriorityP1,
		Requested: ResourceVector{
			CPUCores:    2,
			MemoryBytes: 4 * 1024 * 1024 * 1024,
		},
		PreferredZones: []string{"z1"},
	})
	if err != nil {
		t.Fatalf("submit first: %v", err)
	}
	mgr.ReconcileNow()

	firstScheduled, ok := mgr.GetWorkload(first.Spec.ID)
	if !ok || firstScheduled.State != WorkloadStateRunning || len(firstScheduled.Assignments) == 0 {
		t.Fatalf("first workload did not schedule")
	}
	firstNode := firstScheduled.Assignments[0].NodeID
	if firstNode != "node-a" {
		t.Fatalf("first placement = %s, want node-a", firstNode)
	}

	second, err := mgr.Submit(WorkloadSpec{
		Service:             "llm-gateway",
		Model:               "model-a",
		Class:               WorkloadClassRealtime,
		Priority:            PriorityP1,
		CacheReusePreferred: true,
		Requested: ResourceVector{
			CPUCores:    2,
			MemoryBytes: 4 * 1024 * 1024 * 1024,
		},
	})
	if err != nil {
		t.Fatalf("submit second: %v", err)
	}

	mgr.ReconcileNow()
	secondScheduled, ok := mgr.GetWorkload(second.Spec.ID)
	if !ok || secondScheduled.State != WorkloadStateRunning || len(secondScheduled.Assignments) == 0 {
		t.Fatalf("second workload did not schedule")
	}
	if secondScheduled.Assignments[0].NodeID != firstNode {
		t.Fatalf("second placement = %s, want cache-reused node %s", secondScheduled.Assignments[0].NodeID, firstNode)
	}
}

func TestManagerSelfHealsStaleAssignments(t *testing.T) {
	store := &snapshotStore{}
	store.set(testNodeSnapshot("node-a", 20, 64, 56, time.Now(), map[string]string{"resource.cpu.capacity": "8"}))

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.TelemetryStaleAfter = 20 * time.Second
	cfg.SafetyMarginRatio = 0
	mgr := NewManager(cfg, store, zap.NewNop())

	w, err := mgr.Submit(WorkloadSpec{
		Service: "router",
		Class:   WorkloadClassRealtime,
		Requested: ResourceVector{
			CPUCores:    2,
			MemoryBytes: 2 * 1024 * 1024 * 1024,
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	mgr.ReconcileNow()
	before, ok := mgr.GetWorkload(w.Spec.ID)
	if !ok || before.State != WorkloadStateRunning {
		t.Fatalf("expected running workload before stale transition")
	}

	store.set(testNodeSnapshot("node-a", 20, 64, 56, time.Now().Add(-5*time.Minute), map[string]string{"resource.cpu.capacity": "8"}))
	mgr.ReconcileNow()

	after, ok := mgr.GetWorkload(w.Spec.ID)
	if !ok {
		t.Fatalf("workload missing")
	}
	if after.State != WorkloadStateQueued {
		t.Fatalf("state = %s, want %s", after.State, WorkloadStateQueued)
	}
	if len(after.Assignments) != 0 {
		t.Fatalf("assignments should be cleared on self-heal")
	}
	events := mgr.Events()
	if len(events) == 0 {
		t.Fatalf("expected self-healing event")
	}
	if events[len(events)-1].Action != "requeue" {
		t.Fatalf("event action = %s, want requeue", events[len(events)-1].Action)
	}
}

func TestManagerSLORemediationRequeuesToSaferNode(t *testing.T) {
	store := &snapshotStore{}
	now := time.Now()
	store.set(testNodeSnapshot("node-a", 20, 64, 56, now, map[string]string{
		"resource.cpu.capacity": "8",
		"zone":                  "z1",
		"cluster":               "c1",
	}))

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.SafetyMarginRatio = 0
	cfg.SLOBreachRatio = 1.0
	cfg.SLOBreachConsecutive = 2
	cfg.RemediationCooldown = 0
	cfg.MaxRemediationsPerReconcile = 1
	cfg.MaxRemediationsPerWorkload = 3
	cfg.RemediationMinImprovement = 0.1
	mgr := NewManager(cfg, store, zap.NewNop())

	w, err := mgr.Submit(WorkloadSpec{
		Service:      "chat",
		Class:        WorkloadClassRealtime,
		Priority:     PriorityP1,
		LatencySLOMs: 120,
		Requested: ResourceVector{
			CPUCores:    0.5,
			MemoryBytes: 1 * 1024 * 1024 * 1024,
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	mgr.ReconcileNow()
	initial, ok := mgr.GetWorkload(w.Spec.ID)
	if !ok || initial.State != WorkloadStateRunning || len(initial.Assignments) == 0 {
		t.Fatalf("expected initial running workload with assignment")
	}
	if initial.Assignments[0].NodeID != "node-a" {
		t.Fatalf("initial placement = %s, want node-a", initial.Assignments[0].NodeID)
	}

	hotNode := testNodeSnapshot("node-a", 98, 64, 4, now, map[string]string{
		"resource.cpu.capacity": "8",
		"zone":                  "z1",
		"cluster":               "c1",
	})
	hotNode.Metrics["node_load1"] = 16
	hotNode.Metrics["node_pressure_io_some_avg10"] = 100
	coolNode := testNodeSnapshot("node-b", 10, 64, 56, now, map[string]string{
		"resource.cpu.capacity": "8",
		"zone":                  "z2",
		"cluster":               "c1",
	})
	store.set(hotNode, coolNode)

	// First breach increments counters, second breach crosses threshold and remediates.
	mgr.ReconcileNow()
	mgr.ReconcileNow()

	after, ok := mgr.GetWorkload(w.Spec.ID)
	if !ok {
		t.Fatalf("workload missing after remediation")
	}
	if after.State != WorkloadStateRunning {
		t.Fatalf("state = %s, want %s", after.State, WorkloadStateRunning)
	}
	if len(after.Assignments) == 0 {
		t.Fatalf("expected assignment after remediation")
	}
	if after.Assignments[0].NodeID != "node-b" {
		t.Fatalf("post-remediation placement = %s, want node-b", after.Assignments[0].NodeID)
	}
	if !strings.Contains(strings.ToLower(after.Reason), "allocated") {
		t.Fatalf("reason = %q, want allocated message", after.Reason)
	}

	metrics := mgr.Metrics()
	if metrics.SLOViolationsTotal < 2 {
		t.Fatalf("SLOViolationsTotal = %d, want >= 2", metrics.SLOViolationsTotal)
	}
	if metrics.RemediationActionsTotal < 1 {
		t.Fatalf("RemediationActionsTotal = %d, want >= 1", metrics.RemediationActionsTotal)
	}

	events := mgr.Events()
	found := false
	for _, event := range events {
		if event.WorkloadID == w.Spec.ID && event.Action == "requeue_slo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected requeue_slo event for workload %s", w.Spec.ID)
	}
}

func TestManagerSLORemediationBlockedWithoutSaferCandidate(t *testing.T) {
	store := &snapshotStore{}
	now := time.Now()
	store.set(testNodeSnapshot("node-a", 20, 64, 56, now, map[string]string{
		"resource.cpu.capacity": "8",
	}))

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.SafetyMarginRatio = 0
	cfg.SLOBreachRatio = 1.0
	cfg.SLOBreachConsecutive = 1
	cfg.RemediationCooldown = 0
	cfg.MaxRemediationsPerReconcile = 1
	cfg.MaxRemediationsPerWorkload = 3
	cfg.RemediationMinImprovement = 0.1
	mgr := NewManager(cfg, store, zap.NewNop())

	w, err := mgr.Submit(WorkloadSpec{
		Service:      "chat",
		Class:        WorkloadClassRealtime,
		Priority:     PriorityP1,
		LatencySLOMs: 120,
		Requested: ResourceVector{
			CPUCores:    0.5,
			MemoryBytes: 1 * 1024 * 1024 * 1024,
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	mgr.ReconcileNow()

	hot := testNodeSnapshot("node-a", 99, 64, 2, now, map[string]string{
		"resource.cpu.capacity": "8",
	})
	hot.Metrics["node_load1"] = 16
	hot.Metrics["node_pressure_io_some_avg10"] = 100
	store.set(hot)
	mgr.ReconcileNow()

	got, ok := mgr.GetWorkload(w.Spec.ID)
	if !ok {
		t.Fatalf("workload missing")
	}
	if got.State != WorkloadStateRunning {
		t.Fatalf("state = %s, want %s", got.State, WorkloadStateRunning)
	}
	if !strings.Contains(strings.ToLower(got.Reason), "no safer candidate") {
		t.Fatalf("reason = %q, want blocked remediation reason", got.Reason)
	}

	metrics := mgr.Metrics()
	if metrics.RemediationBlockedTotal < 1 {
		t.Fatalf("RemediationBlockedTotal = %d, want >= 1", metrics.RemediationBlockedTotal)
	}
	if metrics.RemediationActionsTotal != 0 {
		t.Fatalf("RemediationActionsTotal = %d, want 0", metrics.RemediationActionsTotal)
	}

	diag := mgr.Diagnostics()
	if len(diag.BlockedReasons) == 0 {
		t.Fatalf("blocked reasons empty, want non-empty diagnostics")
	}
	if diag.BlockedReasons[0].Count < 1 {
		t.Fatalf("blocked reason count = %d, want >= 1", diag.BlockedReasons[0].Count)
	}
}

func testNodeSnapshot(id string, cpuUsagePercent float64, memTotalGB float64, memAvailGB float64, lastSeen time.Time, labels map[string]string) *ingest.NodeSnapshot {
	const gib = 1024 * 1024 * 1024
	metrics := map[string]float64{
		"node_cpu_usage_percent":                 cpuUsagePercent,
		"node_memory_MemTotal_bytes":             memTotalGB * gib,
		"node_memory_MemAvailable_bytes":         memAvailGB * gib,
		"node_load1":                             1,
		"node_network_receive_bytes_per_second":  1_000_000,
		"node_network_transmit_bytes_per_second": 1_000_000,
		"node_disk_total_iops_per_second":        100,
	}
	if labels == nil {
		labels = map[string]string{}
	}
	return &ingest.NodeSnapshot{
		CollectorID: id,
		Hostname:    id,
		Labels:      cloneLabels(labels),
		LastSeen:    lastSeen,
		UpdatedAt:   lastSeen,
		Metrics:     metrics,
	}
}

func cloneMetrics(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
