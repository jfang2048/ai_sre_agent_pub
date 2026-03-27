package orchestration

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"go.uber.org/zap"
)

const (
	defaultRealtimeCPUCores    = 2
	defaultRealtimeMemoryBytes = 8 * 1024 * 1024 * 1024
	defaultBatchCPUCores       = 1
	defaultBatchMemoryBytes    = 4 * 1024 * 1024 * 1024
)

// Config controls orchestration and scheduling behavior.
type Config struct {
	Enabled                     bool          `yaml:"enabled" json:"enabled"`
	ReconcileInterval           time.Duration `yaml:"reconcile_interval" json:"reconcile_interval"`
	TelemetryStaleAfter         time.Duration `yaml:"telemetry_stale_after" json:"telemetry_stale_after"`
	MaxQueueSize                int           `yaml:"max_queue_size" json:"max_queue_size"`
	PeakPressureThreshold       float64       `yaml:"peak_pressure_threshold" json:"peak_pressure_threshold"`
	MinBatchRunWindow           time.Duration `yaml:"min_batch_run_window" json:"min_batch_run_window"`
	SafetyMarginRatio           float64       `yaml:"safety_margin_ratio" json:"safety_margin_ratio"`
	DefaultCPUCores             float64       `yaml:"default_cpu_cores" json:"default_cpu_cores"`
	DefaultGPUCards             float64       `yaml:"default_gpu_cards" json:"default_gpu_cards"`
	DefaultNPUSlices            float64       `yaml:"default_npu_slices" json:"default_npu_slices"`
	DefaultMemoryBytes          float64       `yaml:"default_memory_bytes" json:"default_memory_bytes"`
	DefaultNetworkMbps          float64       `yaml:"default_network_mbps" json:"default_network_mbps"`
	DefaultStorageIOPS          float64       `yaml:"default_storage_iops" json:"default_storage_iops"`
	ConcurrencyPerPartition     int           `yaml:"concurrency_per_partition" json:"concurrency_per_partition"`
	MaxEvents                   int           `yaml:"max_events" json:"max_events"`
	SLOBreachRatio              float64       `yaml:"slo_breach_ratio" json:"slo_breach_ratio"`
	SLOBreachConsecutive        int           `yaml:"slo_breach_consecutive" json:"slo_breach_consecutive"`
	AutoRemediationEnabled      bool          `yaml:"auto_remediation_enabled" json:"auto_remediation_enabled"`
	RemediationCooldown         time.Duration `yaml:"remediation_cooldown" json:"remediation_cooldown"`
	MaxRemediationsPerReconcile int           `yaml:"max_remediations_per_reconcile" json:"max_remediations_per_reconcile"`
	MaxRemediationsPerWorkload  int           `yaml:"max_remediations_per_workload" json:"max_remediations_per_workload"`
	RemediationMinImprovement   float64       `yaml:"remediation_min_improvement" json:"remediation_min_improvement"`
}

// DefaultConfig provides conservative orchestration defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:                     true,
		ReconcileInterval:           5 * time.Second,
		TelemetryStaleAfter:         45 * time.Second,
		MaxQueueSize:                4096,
		PeakPressureThreshold:       0.75,
		MinBatchRunWindow:           2 * time.Minute,
		SafetyMarginRatio:           0.05,
		DefaultCPUCores:             32,
		DefaultGPUCards:             0,
		DefaultNPUSlices:            0,
		DefaultMemoryBytes:          64 * 1024 * 1024 * 1024,
		DefaultNetworkMbps:          10000,
		DefaultStorageIOPS:          50000,
		ConcurrencyPerPartition:     32,
		MaxEvents:                   200,
		SLOBreachRatio:              1.05,
		SLOBreachConsecutive:        2,
		AutoRemediationEnabled:      true,
		RemediationCooldown:         90 * time.Second,
		MaxRemediationsPerReconcile: 2,
		MaxRemediationsPerWorkload:  4,
		RemediationMinImprovement:   0.12,
	}
}

// Status exposes lightweight runtime state.
type Status struct {
	Enabled             bool            `json:"enabled"`
	Running             bool            `json:"running"`
	ReconcileInterval   string          `json:"reconcile_interval"`
	TelemetryStaleAfter string          `json:"telemetry_stale_after"`
	LastReconciledAt    time.Time       `json:"last_reconciled_at,omitempty"`
	Policy              PolicySnapshot  `json:"policy"`
	Metrics             MetricsSnapshot `json:"metrics"`
}

type workloadRuntimeState struct {
	ConsecutiveSLOBreaches int
	LastEstimatedLatencyMs float64
	LastEvaluatedAt        time.Time
	LastRemediationAt      time.Time
	RemediationsTotal      int
}

// SnapshotProvider is the ingest snapshot source.
type SnapshotProvider interface {
	Snapshot() []*ingest.NodeSnapshot
}

// Manager coordinates unified resource pooling and scheduling.
type Manager struct {
	cfg    Config
	store  SnapshotProvider
	logger *zap.Logger

	mu             sync.RWMutex
	workloads      map[string]*Workload
	nodes          map[string]*ResourceNode
	routes         map[string]RoutingPlan
	events         []HealingEvent
	runtime        map[string]*workloadRuntimeState
	blockedReasons map[string]uint64

	metrics        MetricsSnapshot
	lastReconciled time.Time
	idSeq          uint64
	eventSeq       uint64

	reconcileMu sync.Mutex
	triggerCh   chan struct{}

	ctx     context.Context
	cancel  context.CancelFunc
	running bool
}

// NewManager creates a new orchestration manager.
func NewManager(cfg Config, store SnapshotProvider, logger *zap.Logger) *Manager {
	cfg = normalizedConfig(cfg)
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Manager{
		cfg:            cfg,
		store:          store,
		logger:         logger.With(zap.String("component", "orchestration")),
		workloads:      make(map[string]*Workload),
		nodes:          make(map[string]*ResourceNode),
		routes:         make(map[string]RoutingPlan),
		events:         make([]HealingEvent, 0, cfg.MaxEvents),
		runtime:        make(map[string]*workloadRuntimeState),
		blockedReasons: make(map[string]uint64),
		triggerCh:      make(chan struct{}, 1),
	}
}

// Start runs the periodic reconcile loop.
func (m *Manager) Start(ctx context.Context) error {
	if !m.cfg.Enabled {
		return nil
	}

	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("orchestration manager already running")
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.running = true
	m.mu.Unlock()

	go m.loop()
	m.ReconcileNow()
	return nil
}

// Stop terminates background reconciliation.
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

// Submit registers a new workload for scheduling.
func (m *Manager) Submit(spec WorkloadSpec) (Workload, error) {
	spec = normalizeWorkloadSpec(spec)
	if spec.Service == "" {
		return Workload{}, fmt.Errorf("service is required")
	}

	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.queueDepthLocked() >= m.cfg.MaxQueueSize {
		return Workload{}, fmt.Errorf("workload queue at capacity")
	}

	if spec.ID == "" {
		m.idSeq++
		spec.ID = fmt.Sprintf("wl-%d-%04d", now.UnixMilli(), m.idSeq%10000)
	}
	if _, exists := m.workloads[spec.ID]; exists {
		return Workload{}, fmt.Errorf("workload %s already exists", spec.ID)
	}

	w := &Workload{
		Spec:      spec,
		State:     WorkloadStateQueued,
		Reason:    "awaiting placement",
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.workloads[w.Spec.ID] = w
	m.triggerLocked()
	return cloneWorkload(*w), nil
}

// Complete marks a running workload as completed and releases allocations.
func (m *Manager) Complete(id string) (Workload, bool) {
	m.mu.Lock()
	w, ok := m.workloads[id]
	if !ok {
		m.mu.Unlock()
		return Workload{}, false
	}
	w.State = WorkloadStateCompleted
	w.Reason = "completed by operator"
	w.Assignments = nil
	w.UpdatedAt = time.Now()
	delete(m.runtime, id)
	out := cloneWorkload(*w)
	m.mu.Unlock()
	m.TriggerReconcile()
	return out, true
}

// Delete removes a workload from orchestration state.
func (m *Manager) Delete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workloads[id]; !ok {
		return false
	}
	delete(m.workloads, id)
	delete(m.runtime, id)
	m.triggerLocked()
	return true
}

// GetWorkload returns one workload by id.
func (m *Manager) GetWorkload(id string) (Workload, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workloads[id]
	if !ok {
		return Workload{}, false
	}
	return cloneWorkload(*w), true
}

// Workloads returns all workloads sorted by update time.
func (m *Manager) Workloads() []Workload {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Workload, 0, len(m.workloads))
	for _, w := range m.workloads {
		out = append(out, cloneWorkload(*w))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].Spec.ID < out[j].Spec.ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

// Resources returns pooled resource view.
func (m *Manager) Resources() []ResourceNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ResourceNode, 0, len(m.nodes))
	for _, node := range m.nodes {
		out = append(out, cloneNode(*node))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Routes returns routing plans with optional service/model filtering.
func (m *Manager) Routes(service, model string) []RoutingPlan {
	service = strings.TrimSpace(service)
	model = strings.TrimSpace(model)

	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]RoutingPlan, 0, len(m.routes))
	for _, route := range m.routes {
		if service != "" && route.Service != service {
			continue
		}
		if model != "" && route.Model != model {
			continue
		}
		out = append(out, cloneRoute(route))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].Class < out[j].Class
	})
	return out
}

// Events returns recent self-healing events.
func (m *Manager) Events() []HealingEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]HealingEvent, len(m.events))
	copy(out, m.events)
	return out
}

// Metrics returns the latest orchestration counters.
func (m *Manager) Metrics() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metrics
}

// Status returns runtime state for status endpoints.
func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Status{
		Enabled:             m.cfg.Enabled,
		Running:             m.running,
		ReconcileInterval:   m.cfg.ReconcileInterval.String(),
		TelemetryStaleAfter: m.cfg.TelemetryStaleAfter.String(),
		LastReconciledAt:    m.lastReconciled,
		Policy:              m.policyLocked(),
		Metrics:             m.metrics,
	}
}

// Policy returns active SLO/remediation policy knobs.
func (m *Manager) Policy() PolicySnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policyLocked()
}

// Diagnostics returns policy effectiveness and currently violating workloads.
func (m *Manager) Diagnostics() DiagnosticsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.diagnosticsLocked()
}

// Snapshot returns a full orchestrator dump.
func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nodes := make([]ResourceNode, 0, len(m.nodes))
	for _, node := range m.nodes {
		nodes = append(nodes, cloneNode(*node))
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	workloads := make([]Workload, 0, len(m.workloads))
	for _, w := range m.workloads {
		workloads = append(workloads, cloneWorkload(*w))
	}
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].UpdatedAt.Equal(workloads[j].UpdatedAt) {
			return workloads[i].Spec.ID < workloads[j].Spec.ID
		}
		return workloads[i].UpdatedAt.After(workloads[j].UpdatedAt)
	})

	routes := make([]RoutingPlan, 0, len(m.routes))
	for _, route := range m.routes {
		routes = append(routes, cloneRoute(route))
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Service != routes[j].Service {
			return routes[i].Service < routes[j].Service
		}
		if routes[i].Model != routes[j].Model {
			return routes[i].Model < routes[j].Model
		}
		return routes[i].Class < routes[j].Class
	})

	events := make([]HealingEvent, len(m.events))
	copy(events, m.events)

	return Snapshot{
		GeneratedAt: time.Now(),
		Nodes:       nodes,
		Workloads:   workloads,
		Routes:      routes,
		Events:      events,
		Metrics:     m.metrics,
	}
}

// TriggerReconcile requests a near-term reconcile.
func (m *Manager) TriggerReconcile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.triggerLocked()
}

// ReconcileNow performs synchronous reconciliation.
func (m *Manager) ReconcileNow() {
	if !m.cfg.Enabled {
		return
	}
	m.reconcile("manual")
}

func (m *Manager) loop() {
	ticker := time.NewTicker(m.cfg.ReconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.reconcile("periodic")
		case <-m.triggerCh:
			m.reconcile("trigger")
		}
	}
}

func (m *Manager) reconcile(reason string) {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()

	now := time.Now()
	snapshots := []*ingest.NodeSnapshot(nil)
	if m.store != nil {
		snapshots = m.store.Snapshot()
	}
	nodes := m.deriveNodes(snapshots, now)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.metrics.ReconcilesTotal++
	m.lastReconciled = now
	if len(m.nodes) > 0 {
		for id, node := range nodes {
			prev, ok := m.nodes[id]
			if !ok || prev == nil || len(prev.ModelCaches) == 0 {
				continue
			}
			node.ModelCaches = make(map[string]ModelCache, len(prev.ModelCaches))
			for model, cache := range prev.ModelCaches {
				node.ModelCaches[model] = cache
			}
		}
	}
	m.nodes = nodes
	m.pruneRuntimeStateLocked()
	m.metrics.SLOViolationsActive = 0

	// Track current reservations from already running placements before attempting new scheduling.
	for _, node := range m.nodes {
		node.Reserved = ResourceVector{}
	}
	runningWorkloads := make([]*Workload, 0, len(m.workloads))
	for _, w := range m.workloads {
		if w.State != WorkloadStateRunning {
			continue
		}
		unhealthy, previousNodes := m.runningPlacementUnhealthyLocked(w)
		if unhealthy {
			w.State = WorkloadStateQueued
			w.Reason = "self-healing requeue due unhealthy or stale placement"
			w.Assignments = nil
			w.UpdatedAt = now
			w.QueueDelaySeconds = now.Sub(w.CreatedAt).Seconds()
			if state := m.runtimeStateLocked(w.Spec.ID); state != nil {
				state.ConsecutiveSLOBreaches = 0
			}
			m.metrics.SelfHealActionsTotal++
			m.metrics.RemediationActionsTotal++
			m.appendEventLocked(HealingEvent{
				Timestamp:     now,
				WorkloadID:    w.Spec.ID,
				Action:        "requeue",
				Reason:        "placement lost health",
				PreviousNodes: previousNodes,
			})
			continue
		}
		runningWorkloads = append(runningWorkloads, w)
		for _, a := range w.Assignments {
			node, ok := m.nodes[a.NodeID]
			if !ok {
				continue
			}
			node.Reserved = addResource(node.Reserved, a.Reserved)
		}
	}
	for _, node := range m.nodes {
		node.Available = subtractWithFloor(node.Available, node.Reserved)
	}

	remediationActionsThisCycle := 0
	for _, w := range runningWorkloads {
		breached, estimatedLatencyMs := m.evaluateSLOLocked(w, now)
		if !breached {
			continue
		}
		m.metrics.SLOViolationsActive++
		m.metrics.SLOViolationsTotal++
		m.metrics.RemediationAttemptsTotal++

		remediate, remediationReason := m.shouldAutoRemediateLocked(w, now, remediationActionsThisCycle)
		if !remediate {
			m.metrics.RemediationBlockedTotal++
			m.recordBlockedReasonLocked(remediationReason)
			if remediationReason != "" {
				w.Reason = remediationReason
				w.UpdatedAt = now
			}
			continue
		}

		m.releaseAssignmentsLocked(w.Assignments)
		previousNodes := assignmentNodeIDs(w.Assignments)
		state := m.runtimeStateLocked(w.Spec.ID)
		if state != nil {
			state.ConsecutiveSLOBreaches = 0
			state.LastRemediationAt = now
			state.RemediationsTotal++
		}

		w.State = WorkloadStateQueued
		w.Reason = fmt.Sprintf("auto-remediation requeue after SLO breach (estimated %.1fms)", estimatedLatencyMs)
		w.Assignments = nil
		w.UpdatedAt = now
		w.QueueDelaySeconds = now.Sub(w.CreatedAt).Seconds()

		remediationActionsThisCycle++
		m.metrics.SelfHealActionsTotal++
		m.metrics.RemediationActionsTotal++
		m.appendEventLocked(HealingEvent{
			Timestamp:     now,
			WorkloadID:    w.Spec.ID,
			Action:        "requeue_slo",
			Reason:        "consecutive SLO breach with safer candidate available",
			PreviousNodes: previousNodes,
		})
	}

	pendingIDs := m.pendingWorkloadsLocked()
	realtimeBacklog := m.realtimeBacklogLocked(pendingIDs)
	clusterPressure := m.clusterPressureLocked()

	for _, id := range pendingIDs {
		w := m.workloads[id]
		if w == nil {
			continue
		}

		m.metrics.SchedulingAttemptsTotal++

		if w.Spec.NotBefore != nil && now.Before(*w.Spec.NotBefore) {
			w.State = WorkloadStateDeferred
			w.Reason = "scheduled start time not reached"
			w.UpdatedAt = now
			w.QueueDelaySeconds = now.Sub(w.CreatedAt).Seconds()
			continue
		}

		if w.Spec.Class == WorkloadClassBatch && m.shouldDeferBatchLocked(w, clusterPressure, realtimeBacklog, now) {
			w.State = WorkloadStateDeferred
			w.Reason = "deferred for peak shifting and realtime headroom"
			w.UpdatedAt = now
			w.QueueDelaySeconds = now.Sub(w.CreatedAt).Seconds()
			m.metrics.BatchDeferralsTotal++
			continue
		}

		assignments, placementReason := m.allocateWorkloadLocked(w, now)
		if len(assignments) == 0 {
			m.metrics.SchedulingFailuresTotal++
			if w.Spec.Deadline != nil && now.After(*w.Spec.Deadline) {
				w.State = WorkloadStateFailed
				w.Reason = "deadline exceeded without allocatable resources"
			} else {
				w.State = WorkloadStateQueued
				if placementReason == "" {
					placementReason = "no eligible capacity"
				}
				w.Reason = placementReason
			}
			w.UpdatedAt = now
			w.QueueDelaySeconds = now.Sub(w.CreatedAt).Seconds()
			continue
		}

		w.State = WorkloadStateRunning
		w.Assignments = assignments
		w.Reason = placementReason
		w.UpdatedAt = now
		w.QueueDelaySeconds = now.Sub(w.CreatedAt).Seconds()
		if state := m.runtimeStateLocked(w.Spec.ID); state != nil {
			state.ConsecutiveSLOBreaches = 0
			state.LastEstimatedLatencyMs = averageAssignmentLatency(assignments)
			state.LastEvaluatedAt = now
		}

		model := strings.TrimSpace(w.Spec.Model)
		if model != "" {
			for _, assignment := range assignments {
				node := m.nodes[assignment.NodeID]
				if node == nil {
					continue
				}
				if node.ModelCaches == nil {
					node.ModelCaches = make(map[string]ModelCache)
				}
				node.ModelCaches[model] = ModelCache{
					Model:          model,
					Warm:           true,
					EstimatedBytes: w.Spec.Requested.MemoryBytes,
					HitRate:        0.9,
					LastUsedAt:     now,
				}
			}
		}
	}

	m.buildRoutesLocked(now)
	m.refreshMetricsLocked()

	m.logger.Debug("orchestration reconcile complete",
		zap.String("reason", reason),
		zap.Int("nodes", len(m.nodes)),
		zap.Int("queue", m.metrics.QueueDepth),
		zap.Int("running", m.metrics.RunningWorkloads))
}

func (m *Manager) deriveNodes(snapshots []*ingest.NodeSnapshot, now time.Time) map[string]*ResourceNode {
	nodes := make(map[string]*ResourceNode, len(snapshots))

	for _, snap := range snapshots {
		if snap == nil || strings.TrimSpace(snap.CollectorID) == "" {
			continue
		}

		id := strings.TrimSpace(snap.CollectorID)
		host := strings.TrimSpace(snap.Hostname)
		if host == "" {
			host = id
		}

		labels := copyStringMap(snap.Labels)
		cluster := firstNonEmpty(
			labels["cluster"],
			labels["k8s_cluster"],
			labels["topology.kubernetes.io/cluster"],
			labels["failure-domain.beta.kubernetes.io/cluster"],
		)
		zone := firstNonEmpty(
			labels["zone"],
			labels["topology.kubernetes.io/zone"],
			labels["failure-domain.beta.kubernetes.io/zone"],
		)

		lastSeen := snap.LastSeen
		if lastSeen.IsZero() {
			lastSeen = snap.UpdatedAt
		}
		ageSeconds := 0.0
		if !lastSeen.IsZero() {
			ageSeconds = now.Sub(lastSeen).Seconds()
			if ageSeconds < 0 {
				ageSeconds = 0
			}
		}
		healthy := true
		if m.cfg.TelemetryStaleAfter > 0 && !lastSeen.IsZero() {
			healthy = now.Sub(lastSeen) <= m.cfg.TelemetryStaleAfter
		}

		metrics := snap.Metrics

		cpuCapacity := firstPositive(
			metricValue(metrics, "node_cpu_capacity_cores"),
			labelFloat(labels, "resource.cpu.capacity", "capacity.cpu", "node.capacity.cpu"),
			m.cfg.DefaultCPUCores,
		)
		gpuCapacity := m.cfg.DefaultGPUCards
		if value, ok := metricValueIfPresent(metrics, "node_gpu_count"); ok && value >= 0 {
			gpuCapacity = value
		} else if value := labelFloat(labels, "resource.gpu.capacity", "capacity.gpu", "node.capacity.gpu"); value >= 0 {
			gpuCapacity = value
		}
		npuCapacity := m.cfg.DefaultNPUSlices
		if value, ok := metricValueIfPresent(metrics, "node_npu_count"); ok && value >= 0 {
			npuCapacity = value
		} else if value := labelFloat(labels, "resource.npu.capacity", "capacity.npu", "node.capacity.npu"); value >= 0 {
			npuCapacity = value
		}
		memoryCapacity := firstPositive(
			metricValue(metrics, "node_memory_MemTotal_bytes"),
			labelFloat(labels, "resource.memory.bytes", "capacity.memory.bytes", "node.capacity.memory.bytes"),
			m.cfg.DefaultMemoryBytes,
		)
		networkCapacity := firstPositive(
			labelFloat(labels, "resource.network.mbps", "capacity.network.mbps", "node.capacity.network.mbps"),
			m.cfg.DefaultNetworkMbps,
		)
		storageCapacity := firstPositive(
			labelFloat(labels, "resource.storage.iops", "capacity.storage.iops", "node.capacity.storage.iops"),
			m.cfg.DefaultStorageIOPS,
		)

		cpuUtil := clamp01(metricValue(metrics, "node_cpu_usage_percent") / 100)
		memUsed := metricValue(metrics, "node_memory_Used_bytes")
		memAvail := metricValue(metrics, "node_memory_MemAvailable_bytes")
		memoryUtil := 0.0
		switch {
		case memUsed > 0 && memoryCapacity > 0:
			memoryUtil = clamp01(memUsed / memoryCapacity)
		case memAvail > 0 && memoryCapacity > 0:
			memoryUtil = clamp01(1 - (memAvail / memoryCapacity))
		}
		gpuUtil := 0.0
		if value, ok := metricValueIfPresent(metrics, "node_gpu_utilization_sm_avg_percent"); ok {
			gpuUtil = clamp01(value / 100)
		} else if value, ok := metricValueIfPresent(metrics, "node_gpu_utilization_sm_percent"); ok {
			gpuUtil = clamp01(value / 100)
		}
		npuUtil := clamp01(metricValue(metrics, "node_npu_utilization_percent") / 100)

		rxBps := metricValue(metrics, "node_network_receive_bytes_per_second")
		txBps := metricValue(metrics, "node_network_transmit_bytes_per_second")
		networkUtil := 0.0
		if networkCapacity > 0 {
			networkUtil = clamp01((rxBps + txBps) / (networkCapacity * 125000))
		}

		iops := 0.0
		if value, ok := metricValueIfPresent(metrics, "node_disk_total_iops_per_second"); ok {
			iops = maxFloat(0, value)
		} else if value, ok := metricValueIfPresent(metrics, "node_disk_iops_per_second"); ok {
			iops = maxFloat(0, value)
		}
		storageUtil := 0.0
		if storageCapacity > 0 {
			storageUtil = clamp01(iops / storageCapacity)
		}

		headroom := clamp01(1 - m.cfg.SafetyMarginRatio)
		capacity := ResourceVector{
			CPUCores:    cpuCapacity,
			GPUCards:    gpuCapacity,
			NPUSlices:   npuCapacity,
			MemoryBytes: memoryCapacity,
			NetworkMbps: networkCapacity,
			StorageIOPS: storageCapacity,
		}
		available := ResourceVector{
			CPUCores:    cpuCapacity * clamp01((1-cpuUtil)*headroom),
			GPUCards:    gpuCapacity * clamp01((1-gpuUtil)*headroom),
			NPUSlices:   npuCapacity * clamp01((1-npuUtil)*headroom),
			MemoryBytes: memoryCapacity * clamp01((1-memoryUtil)*headroom),
			NetworkMbps: networkCapacity * clamp01((1-networkUtil)*headroom),
			StorageIOPS: storageCapacity * clamp01((1-storageUtil)*headroom),
		}

		load1 := firstNonNegative(metricValue(metrics, "node_load1"), 0)
		ioPressure := clamp01(metricValue(metrics, "node_pressure_io_some_avg10") / 100)
		latencyScore := clamp01(0.45*maxFloat(cpuUtil, memoryUtil, gpuUtil) + 0.35*clamp01(load1/maxFloat(cpuCapacity, 1)) + 0.20*ioPressure)

		nodes[id] = &ResourceNode{
			ID:                  id,
			Hostname:            host,
			Cluster:             cluster,
			Zone:                zone,
			Labels:              labels,
			Healthy:             healthy,
			LastSeen:            lastSeen,
			TelemetryAgeSeconds: ageSeconds,
			Capacity:            capacity,
			Available:           available,
			Reserved:            ResourceVector{},
			Utilization: UtilizationVector{
				CPU:     cpuUtil,
				GPU:     gpuUtil,
				NPU:     npuUtil,
				Memory:  memoryUtil,
				Network: networkUtil,
				Storage: storageUtil,
			},
			LatencyScore: latencyScore,
			ModelCaches:  map[string]ModelCache{},
		}
	}

	return nodes
}

func (m *Manager) runningPlacementUnhealthyLocked(w *Workload) (bool, []string) {
	if w == nil || len(w.Assignments) == 0 {
		return false, nil
	}
	previousNodes := make([]string, 0, len(w.Assignments))
	for _, assignment := range w.Assignments {
		previousNodes = append(previousNodes, assignment.NodeID)
		node, ok := m.nodes[assignment.NodeID]
		if !ok || !node.Healthy {
			return true, previousNodes
		}
	}
	return false, previousNodes
}

func (m *Manager) pendingWorkloadsLocked() []string {
	ids := make([]string, 0)
	for id, w := range m.workloads {
		if w.State != WorkloadStateQueued && w.State != WorkloadStateDeferred {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := m.workloads[ids[i]]
		right := m.workloads[ids[j]]
		if left == nil || right == nil {
			return ids[i] < ids[j]
		}

		leftClass := classRank(left.Spec.Class)
		rightClass := classRank(right.Spec.Class)
		if leftClass != rightClass {
			return leftClass < rightClass
		}

		leftPriority := priorityRank(left.Spec.Priority)
		rightPriority := priorityRank(right.Spec.Priority)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}

		leftDeadline := deadlineValue(left.Spec.Deadline)
		rightDeadline := deadlineValue(right.Spec.Deadline)
		if !leftDeadline.Equal(rightDeadline) {
			return leftDeadline.Before(rightDeadline)
		}

		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.Spec.ID < right.Spec.ID
	})
	return ids
}

func (m *Manager) realtimeBacklogLocked(ids []string) int {
	count := 0
	for _, id := range ids {
		w := m.workloads[id]
		if w == nil {
			continue
		}
		if w.Spec.Class == WorkloadClassRealtime {
			count++
		}
	}
	return count
}

func (m *Manager) clusterPressureLocked() float64 {
	if len(m.nodes) == 0 {
		return 0
	}
	total := 0.0
	count := 0.0
	for _, node := range m.nodes {
		if node == nil || !node.Healthy {
			continue
		}
		pressure := maxFloat(node.Utilization.CPU, node.Utilization.GPU, node.Utilization.NPU, node.Utilization.Memory)
		total += pressure
		count++
	}
	if count == 0 {
		return 0
	}
	return total / count
}

func (m *Manager) shouldDeferBatchLocked(w *Workload, pressure float64, realtimeBacklog int, now time.Time) bool {
	if w == nil || w.Spec.Class != WorkloadClassBatch {
		return false
	}
	if w.Spec.Deadline != nil {
		if now.After(*w.Spec.Deadline) {
			return false
		}
		if now.Add(m.cfg.MinBatchRunWindow).After(*w.Spec.Deadline) {
			return false
		}
	}
	if realtimeBacklog > 0 {
		return true
	}
	return pressure >= m.cfg.PeakPressureThreshold
}

func (m *Manager) allocateWorkloadLocked(w *Workload, now time.Time) ([]Assignment, string) {
	if w == nil {
		return nil, "workload not found"
	}

	partitions := m.partitionCount(w.Spec)
	request := perPartitionRequest(w.Spec, partitions)
	available := make(map[string]ResourceVector, len(m.nodes))
	for id, node := range m.nodes {
		available[id] = node.Available
	}

	assignments := make([]Assignment, 0, partitions)
	for partition := 0; partition < partitions; partition++ {
		nodeID, score, reason := m.selectNodeLocked(w.Spec, request, available)
		if nodeID == "" {
			if reason == "" {
				reason = "insufficient resources"
			}
			return nil, reason
		}
		node := m.nodes[nodeID]
		if node == nil {
			return nil, "selected node disappeared"
		}

		available[nodeID] = subtractWithFloor(available[nodeID], request)
		latencyMs := m.estimateLatency(node, w.Spec)
		assignments = append(assignments, Assignment{
			WorkloadID:         w.Spec.ID,
			NodeID:             nodeID,
			Zone:               node.Zone,
			Cluster:            node.Cluster,
			Partition:          partition,
			Reserved:           request,
			RouteWeight:        maxFloat(0.05, score),
			EstimatedLatencyMs: latencyMs,
			Reason:             "best-fit placement",
			CreatedAt:          now,
		})
	}

	totalWeight := 0.0
	for i := range assignments {
		node := m.nodes[assignments[i].NodeID]
		if node != nil {
			node.Reserved = addResource(node.Reserved, assignments[i].Reserved)
			node.Available = subtractWithFloor(node.Available, assignments[i].Reserved)
		}
		totalWeight += assignments[i].RouteWeight
	}
	if totalWeight > 0 {
		for i := range assignments {
			assignments[i].RouteWeight = assignments[i].RouteWeight / totalWeight
		}
	}

	reason := fmt.Sprintf("allocated across %d partition(s)", partitions)
	if w.Spec.Class == WorkloadClassBatch {
		reason += ", batch-ready"
	}
	return assignments, reason
}

func (m *Manager) selectNodeLocked(spec WorkloadSpec, req ResourceVector, available map[string]ResourceVector) (string, float64, string) {
	preferredZones := toSet(spec.PreferredZones)
	preferredClusters := toSet(spec.PreferredClusters)

	bestNodeID := ""
	bestScore := math.Inf(-1)
	reason := "no healthy nodes"

	for nodeID, node := range m.nodes {
		if node == nil || !node.Healthy {
			continue
		}
		avail := available[nodeID]
		if !resourceFits(avail, req) {
			reason = "insufficient available resources"
			continue
		}

		score := 0.0
		score += headroomScore(avail.CPUCores, node.Capacity.CPUCores) * 35
		score += headroomScore(avail.MemoryBytes, node.Capacity.MemoryBytes) * 22
		score += headroomScore(avail.GPUCards, node.Capacity.GPUCards) * 18
		score += headroomScore(avail.NPUSlices, node.Capacity.NPUSlices) * 18
		score += headroomScore(avail.NetworkMbps, node.Capacity.NetworkMbps) * 4
		score += headroomScore(avail.StorageIOPS, node.Capacity.StorageIOPS) * 3

		score -= node.Utilization.Network * 8
		score -= node.Utilization.Storage * 6
		score -= node.LatencyScore * 10

		if spec.Class == WorkloadClassRealtime {
			score += (1 - node.LatencyScore) * 18
			score -= maxFloat(node.Utilization.CPU, node.Utilization.Memory, node.Utilization.GPU) * 8
		}
		if spec.Class == WorkloadClassBatch {
			score += headroomScore(avail.CPUCores, node.Capacity.CPUCores) * 8
			score += headroomScore(avail.GPUCards, node.Capacity.GPUCards) * 8
		}

		if len(preferredZones) > 0 {
			if preferredZones[node.Zone] {
				score += 8
			} else {
				score -= 8
			}
		}
		if len(preferredClusters) > 0 {
			if preferredClusters[node.Cluster] {
				score += 6
			} else {
				score -= 6
			}
		}
		if spec.CacheReusePreferred && spec.Model != "" {
			if cache, ok := node.ModelCaches[spec.Model]; ok && cache.Warm {
				score += 14
			}
		}

		if score > bestScore {
			bestScore = score
			bestNodeID = nodeID
		}
	}

	if bestNodeID == "" {
		return "", 0, reason
	}
	return bestNodeID, bestScore, ""
}

func (m *Manager) buildRoutesLocked(now time.Time) {
	type aggregate struct {
		class   WorkloadClass
		targets map[string]RouteTarget
	}

	agg := map[string]*aggregate{}
	for _, workload := range m.workloads {
		if workload == nil || workload.State != WorkloadStateRunning || len(workload.Assignments) == 0 {
			continue
		}

		service := strings.TrimSpace(workload.Spec.Service)
		if service == "" {
			continue
		}
		model := strings.TrimSpace(workload.Spec.Model)
		key := routeKey(service, model, workload.Spec.Class)
		entry, ok := agg[key]
		if !ok {
			entry = &aggregate{class: workload.Spec.Class, targets: map[string]RouteTarget{}}
			agg[key] = entry
		}

		for _, assignment := range workload.Assignments {
			node := m.nodes[assignment.NodeID]
			if node == nil {
				continue
			}
			target := entry.targets[assignment.NodeID]
			if target.NodeID == "" {
				target = RouteTarget{
					NodeID:  assignment.NodeID,
					Zone:    assignment.Zone,
					Cluster: assignment.Cluster,
				}
			}
			target.EstimatedLatencyMs = assignment.EstimatedLatencyMs
			target.SuggestedBatchSize = suggestedBatchSize(workload.Spec)

			headroom := headroomScore(node.Available.CPUCores, node.Capacity.CPUCores)
			pressure := maxFloat(node.Utilization.CPU, node.Utilization.Memory, node.Utilization.GPU, node.Utilization.NPU)
			weight := maxFloat(0.05, 1.2+(0.6*headroom)-(0.8*pressure)-node.LatencyScore)
			target.Weight += weight
			entry.targets[assignment.NodeID] = target
		}
	}

	routes := make(map[string]RoutingPlan, len(agg))
	for key, entry := range agg {
		parts := strings.SplitN(key, "|", 3)
		service := parts[0]
		model := parts[1]
		class := WorkloadClass(parts[2])

		targets := make([]RouteTarget, 0, len(entry.targets))
		total := 0.0
		for _, t := range entry.targets {
			targets = append(targets, t)
			total += t.Weight
		}
		if total > 0 {
			for i := range targets {
				targets[i].Weight = targets[i].Weight / total
			}
		}
		sort.Slice(targets, func(i, j int) bool {
			if targets[i].Weight != targets[j].Weight {
				return targets[i].Weight > targets[j].Weight
			}
			return targets[i].NodeID < targets[j].NodeID
		})

		routes[key] = RoutingPlan{
			Service:     service,
			Model:       model,
			Class:       class,
			GeneratedAt: now,
			Targets:     targets,
		}
	}

	m.routes = routes
	m.metrics.RouteUpdatesTotal += uint64(len(routes))
}

func (m *Manager) refreshMetricsLocked() {
	queueDepth := 0
	running := 0
	deferred := 0
	failed := 0
	completed := 0
	assignments := 0

	for _, w := range m.workloads {
		if w == nil {
			continue
		}
		switch w.State {
		case WorkloadStateQueued:
			queueDepth++
		case WorkloadStateDeferred:
			queueDepth++
			deferred++
		case WorkloadStateRunning:
			running++
			assignments += len(w.Assignments)
		case WorkloadStateFailed:
			failed++
		case WorkloadStateCompleted:
			completed++
		}
	}

	m.metrics.QueueDepth = queueDepth
	m.metrics.RunningWorkloads = running
	m.metrics.DeferredWorkloads = deferred
	m.metrics.FailedWorkloads = failed
	m.metrics.CompletedWorkloads = completed
	m.metrics.AssignmentsTotal = assignments
}

func (m *Manager) diagnosticsLocked() DiagnosticsSnapshot {
	blockedReasons := make([]RemediationGateCount, 0, len(m.blockedReasons))
	for reason, count := range m.blockedReasons {
		blockedReasons = append(blockedReasons, RemediationGateCount{
			Reason: reason,
			Count:  count,
		})
	}
	sort.Slice(blockedReasons, func(i, j int) bool {
		if blockedReasons[i].Count != blockedReasons[j].Count {
			return blockedReasons[i].Count > blockedReasons[j].Count
		}
		return blockedReasons[i].Reason < blockedReasons[j].Reason
	})

	violations := make([]SLOViolationSummary, 0, len(m.workloads))
	for _, w := range m.workloads {
		if w == nil || w.State != WorkloadStateRunning {
			continue
		}
		state := m.runtime[strings.TrimSpace(w.Spec.ID)]
		if state == nil || state.ConsecutiveSLOBreaches <= 0 {
			continue
		}
		if w.Spec.LatencySLOMs <= 0 {
			continue
		}
		estimatedLatencyMs := state.LastEstimatedLatencyMs
		if estimatedLatencyMs <= 0 {
			estimatedLatencyMs = m.workloadEstimatedLatencyLocked(w)
		}
		if estimatedLatencyMs <= 0 {
			continue
		}
		breachRatio := estimatedLatencyMs / maxFloat(float64(w.Spec.LatencySLOMs), 1)
		violations = append(violations, SLOViolationSummary{
			WorkloadID:          w.Spec.ID,
			Service:             w.Spec.Service,
			Model:               w.Spec.Model,
			Class:               w.Spec.Class,
			Priority:            w.Spec.Priority,
			LatencySLOMs:        w.Spec.LatencySLOMs,
			EstimatedLatencyMs:  estimatedLatencyMs,
			BreachRatio:         breachRatio,
			ConsecutiveBreaches: state.ConsecutiveSLOBreaches,
			AssignedNodes:       assignmentNodeIDs(w.Assignments),
			LastUpdatedAt:       w.UpdatedAt,
			Reason:              w.Reason,
		})
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].BreachRatio != violations[j].BreachRatio {
			return violations[i].BreachRatio > violations[j].BreachRatio
		}
		if violations[i].ConsecutiveBreaches != violations[j].ConsecutiveBreaches {
			return violations[i].ConsecutiveBreaches > violations[j].ConsecutiveBreaches
		}
		return violations[i].WorkloadID < violations[j].WorkloadID
	})

	return DiagnosticsSnapshot{
		GeneratedAt:    time.Now(),
		Policy:         m.policyLocked(),
		Metrics:        m.metrics,
		BlockedReasons: blockedReasons,
		Violations:     violations,
	}
}

func (m *Manager) recordBlockedReasonLocked(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "blocked_by_unknown_gate"
	}
	if strings.HasPrefix(reason, "awaiting consecutive SLO breaches") {
		reason = "awaiting consecutive SLO breaches"
	}
	m.blockedReasons[reason]++
}

func (m *Manager) policyLocked() PolicySnapshot {
	return PolicySnapshot{
		SLOBreachRatio:              m.cfg.SLOBreachRatio,
		SLOBreachConsecutive:        m.cfg.SLOBreachConsecutive,
		AutoRemediationEnabled:      m.cfg.AutoRemediationEnabled,
		RemediationCooldown:         m.cfg.RemediationCooldown.String(),
		MaxRemediationsPerReconcile: m.cfg.MaxRemediationsPerReconcile,
		MaxRemediationsPerWorkload:  m.cfg.MaxRemediationsPerWorkload,
		RemediationMinImprovement:   m.cfg.RemediationMinImprovement,
	}
}

func (m *Manager) runtimeStateLocked(workloadID string) *workloadRuntimeState {
	workloadID = strings.TrimSpace(workloadID)
	if workloadID == "" {
		return nil
	}
	state := m.runtime[workloadID]
	if state != nil {
		return state
	}
	state = &workloadRuntimeState{}
	m.runtime[workloadID] = state
	return state
}

func (m *Manager) pruneRuntimeStateLocked() {
	if len(m.runtime) == 0 {
		return
	}
	for workloadID := range m.runtime {
		if _, ok := m.workloads[workloadID]; ok {
			continue
		}
		delete(m.runtime, workloadID)
	}
}

func (m *Manager) evaluateSLOLocked(w *Workload, now time.Time) (bool, float64) {
	if w == nil {
		return false, 0
	}
	state := m.runtimeStateLocked(w.Spec.ID)
	if state == nil {
		return false, 0
	}

	estimatedLatencyMs := m.workloadEstimatedLatencyLocked(w)
	state.LastEstimatedLatencyMs = estimatedLatencyMs
	state.LastEvaluatedAt = now

	if w.Spec.LatencySLOMs <= 0 || estimatedLatencyMs <= 0 {
		state.ConsecutiveSLOBreaches = 0
		return false, estimatedLatencyMs
	}
	threshold := float64(w.Spec.LatencySLOMs) * m.cfg.SLOBreachRatio
	if threshold <= 0 {
		threshold = float64(w.Spec.LatencySLOMs)
	}
	if estimatedLatencyMs > threshold {
		state.ConsecutiveSLOBreaches++
		return true, estimatedLatencyMs
	}
	state.ConsecutiveSLOBreaches = 0
	return false, estimatedLatencyMs
}

func (m *Manager) workloadEstimatedLatencyLocked(w *Workload) float64 {
	if w == nil || len(w.Assignments) == 0 {
		return 0
	}
	total := 0.0
	count := 0.0
	for _, assignment := range w.Assignments {
		latencyMs := assignment.EstimatedLatencyMs
		if node := m.nodes[assignment.NodeID]; node != nil {
			latencyMs = m.estimateLatency(node, w.Spec)
		}
		if latencyMs <= 0 {
			continue
		}
		total += latencyMs
		count++
	}
	if count == 0 {
		return 0
	}
	return total / count
}

func (m *Manager) shouldAutoRemediateLocked(w *Workload, now time.Time, remediationActionsThisCycle int) (bool, string) {
	if w == nil {
		return false, "workload not found"
	}
	if !m.cfg.AutoRemediationEnabled {
		return false, "auto-remediation disabled by policy"
	}
	if w.Spec.Class != WorkloadClassRealtime {
		return false, "auto-remediation policy applies to realtime workloads only"
	}
	state := m.runtimeStateLocked(w.Spec.ID)
	if state == nil {
		return false, "workload runtime state unavailable"
	}
	if state.ConsecutiveSLOBreaches < m.cfg.SLOBreachConsecutive {
		return false, fmt.Sprintf("awaiting consecutive SLO breaches (%d/%d)", state.ConsecutiveSLOBreaches, m.cfg.SLOBreachConsecutive)
	}
	if remediationActionsThisCycle >= m.cfg.MaxRemediationsPerReconcile {
		return false, "per-reconcile remediation budget exhausted"
	}
	if m.cfg.MaxRemediationsPerWorkload > 0 && state.RemediationsTotal >= m.cfg.MaxRemediationsPerWorkload {
		return false, "per-workload remediation budget exhausted"
	}
	if m.cfg.RemediationCooldown > 0 && !state.LastRemediationAt.IsZero() && now.Sub(state.LastRemediationAt) < m.cfg.RemediationCooldown {
		return false, "remediation cooldown active"
	}
	if !m.hasBetterNodeCandidateLocked(w) {
		return false, "no safer candidate placement with required latency improvement"
	}
	return true, ""
}

func (m *Manager) hasBetterNodeCandidateLocked(w *Workload) bool {
	if w == nil || len(w.Assignments) == 0 {
		return false
	}
	baselineLatencyMs := m.workloadEstimatedLatencyLocked(w)
	if baselineLatencyMs <= 0 {
		return false
	}
	improvement := clamp01(m.cfg.RemediationMinImprovement)
	targetLatencyMs := baselineLatencyMs * (1 - improvement)
	if targetLatencyMs < 0 {
		targetLatencyMs = baselineLatencyMs
	}
	currentNodes := make(map[string]struct{}, len(w.Assignments))
	for _, assignment := range w.Assignments {
		currentNodes[assignment.NodeID] = struct{}{}
	}

	partitions := m.partitionCount(w.Spec)
	request := perPartitionRequest(w.Spec, partitions)
	for nodeID, node := range m.nodes {
		if node == nil || !node.Healthy {
			continue
		}
		if _, exists := currentNodes[nodeID]; exists {
			continue
		}
		if !resourceFits(node.Available, request) {
			continue
		}
		if m.estimateLatency(node, w.Spec) <= targetLatencyMs {
			return true
		}
	}
	return false
}

func (m *Manager) releaseAssignmentsLocked(assignments []Assignment) {
	for _, assignment := range assignments {
		node := m.nodes[assignment.NodeID]
		if node == nil {
			continue
		}
		node.Reserved = subtractWithFloor(node.Reserved, assignment.Reserved)
		node.Available = addResource(node.Available, assignment.Reserved)
	}
}

func (m *Manager) queueDepthLocked() int {
	count := 0
	for _, w := range m.workloads {
		if w == nil {
			continue
		}
		if w.State == WorkloadStateQueued || w.State == WorkloadStateDeferred {
			count++
		}
	}
	return count
}

func (m *Manager) appendEventLocked(event HealingEvent) {
	m.eventSeq++
	event.ID = fmt.Sprintf("heal-%d-%04d", event.Timestamp.UnixMilli(), m.eventSeq%10000)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	m.events = append(m.events, event)
	if len(m.events) > m.cfg.MaxEvents {
		m.events = m.events[len(m.events)-m.cfg.MaxEvents:]
	}
}

func (m *Manager) partitionCount(spec WorkloadSpec) int {
	maxPartitions := spec.MaxPartitions
	if maxPartitions <= 0 {
		maxPartitions = 1
	}
	if spec.TargetConcurrency <= 0 {
		return 1
	}
	perPartition := m.cfg.ConcurrencyPerPartition
	if perPartition <= 0 {
		perPartition = 32
	}
	parts := int(math.Ceil(float64(spec.TargetConcurrency) / float64(perPartition)))
	if parts < 1 {
		parts = 1
	}
	if parts > maxPartitions {
		parts = maxPartitions
	}
	return parts
}

func (m *Manager) estimateLatency(node *ResourceNode, spec WorkloadSpec) float64 {
	if node == nil {
		return 0
	}
	base := 20.0
	if spec.Class == WorkloadClassBatch {
		base = 60
	}
	lat := base + (node.LatencyScore * 120)
	if spec.CacheReusePreferred && spec.Model != "" {
		if cache, ok := node.ModelCaches[spec.Model]; ok && cache.Warm {
			lat *= 0.8
		}
	}
	return lat
}

func (m *Manager) triggerLocked() {
	select {
	case m.triggerCh <- struct{}{}:
	default:
	}
}

func normalizedConfig(cfg Config) Config {
	if cfg.ReconcileInterval <= 0 {
		cfg.ReconcileInterval = 5 * time.Second
	}
	if cfg.TelemetryStaleAfter <= 0 {
		cfg.TelemetryStaleAfter = 45 * time.Second
	}
	if cfg.MaxQueueSize <= 0 {
		cfg.MaxQueueSize = 4096
	}
	if cfg.PeakPressureThreshold <= 0 || cfg.PeakPressureThreshold > 1 {
		cfg.PeakPressureThreshold = 0.75
	}
	if cfg.MinBatchRunWindow <= 0 {
		cfg.MinBatchRunWindow = 2 * time.Minute
	}
	if cfg.SafetyMarginRatio < 0 || cfg.SafetyMarginRatio > 0.3 {
		cfg.SafetyMarginRatio = 0.05
	}
	if cfg.DefaultCPUCores <= 0 {
		cfg.DefaultCPUCores = 32
	}
	if cfg.DefaultMemoryBytes <= 0 {
		cfg.DefaultMemoryBytes = 64 * 1024 * 1024 * 1024
	}
	if cfg.DefaultNetworkMbps <= 0 {
		cfg.DefaultNetworkMbps = 10000
	}
	if cfg.DefaultStorageIOPS <= 0 {
		cfg.DefaultStorageIOPS = 50000
	}
	if cfg.ConcurrencyPerPartition <= 0 {
		cfg.ConcurrencyPerPartition = 32
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 200
	}
	if cfg.SLOBreachRatio < 1 || cfg.SLOBreachRatio > 3 {
		cfg.SLOBreachRatio = 1.05
	}
	if cfg.SLOBreachConsecutive <= 0 || cfg.SLOBreachConsecutive > 20 {
		cfg.SLOBreachConsecutive = 2
	}
	if cfg.RemediationCooldown < 0 {
		cfg.RemediationCooldown = 90 * time.Second
	}
	if cfg.MaxRemediationsPerReconcile <= 0 {
		cfg.MaxRemediationsPerReconcile = 2
	}
	if cfg.MaxRemediationsPerWorkload <= 0 {
		cfg.MaxRemediationsPerWorkload = 4
	}
	if cfg.RemediationMinImprovement < 0 || cfg.RemediationMinImprovement > 0.9 {
		cfg.RemediationMinImprovement = 0.12
	}
	return cfg
}

func normalizeWorkloadSpec(spec WorkloadSpec) WorkloadSpec {
	spec.Service = strings.TrimSpace(spec.Service)
	spec.Model = strings.TrimSpace(spec.Model)
	if spec.Class == "" {
		if spec.LatencySLOMs > 0 && spec.LatencySLOMs <= 500 {
			spec.Class = WorkloadClassRealtime
		} else {
			spec.Class = WorkloadClassBatch
		}
	}
	if spec.Priority == "" {
		spec.Priority = PriorityP2
	}
	if spec.MaxPartitions <= 0 {
		spec.MaxPartitions = 1
	}
	if spec.Class == WorkloadClassRealtime {
		if spec.Requested.CPUCores <= 0 {
			spec.Requested.CPUCores = defaultRealtimeCPUCores
		}
		if spec.Requested.MemoryBytes <= 0 {
			spec.Requested.MemoryBytes = defaultRealtimeMemoryBytes
		}
		if spec.MaxBatchSize <= 0 {
			spec.MaxBatchSize = 1
		}
		if spec.MinBatchSize <= 0 {
			spec.MinBatchSize = 1
		}
	} else {
		if spec.Requested.CPUCores <= 0 {
			spec.Requested.CPUCores = defaultBatchCPUCores
		}
		if spec.Requested.MemoryBytes <= 0 {
			spec.Requested.MemoryBytes = defaultBatchMemoryBytes
		}
		if spec.MaxBatchSize <= 0 {
			spec.MaxBatchSize = 16
		}
		if spec.MinBatchSize <= 0 {
			spec.MinBatchSize = 1
		}
	}
	if spec.MaxBatchSize < spec.MinBatchSize {
		spec.MaxBatchSize = spec.MinBatchSize
	}
	if spec.Model != "" && !spec.CacheReusePreferred {
		spec.CacheReusePreferred = true
	}
	return spec
}

func perPartitionRequest(spec WorkloadSpec, partitions int) ResourceVector {
	if partitions <= 1 {
		return spec.Requested
	}
	return ResourceVector{
		CPUCores:    spec.Requested.CPUCores / float64(partitions),
		GPUCards:    spec.Requested.GPUCards / float64(partitions),
		NPUSlices:   spec.Requested.NPUSlices / float64(partitions),
		MemoryBytes: spec.Requested.MemoryBytes / float64(partitions),
		NetworkMbps: spec.Requested.NetworkMbps / float64(partitions),
		StorageIOPS: spec.Requested.StorageIOPS / float64(partitions),
	}
}

func averageAssignmentLatency(assignments []Assignment) float64 {
	if len(assignments) == 0 {
		return 0
	}
	total := 0.0
	count := 0.0
	for _, assignment := range assignments {
		if assignment.EstimatedLatencyMs <= 0 {
			continue
		}
		total += assignment.EstimatedLatencyMs
		count++
	}
	if count == 0 {
		return 0
	}
	return total / count
}

func assignmentNodeIDs(assignments []Assignment) []string {
	if len(assignments) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(assignments))
	out := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		nodeID := strings.TrimSpace(assignment.NodeID)
		if nodeID == "" {
			continue
		}
		if _, exists := seen[nodeID]; exists {
			continue
		}
		seen[nodeID] = struct{}{}
		out = append(out, nodeID)
	}
	sort.Strings(out)
	return out
}

func resourceFits(available, requested ResourceVector) bool {
	if requested.CPUCores > 0 && available.CPUCores+1e-9 < requested.CPUCores {
		return false
	}
	if requested.GPUCards > 0 && available.GPUCards+1e-9 < requested.GPUCards {
		return false
	}
	if requested.NPUSlices > 0 && available.NPUSlices+1e-9 < requested.NPUSlices {
		return false
	}
	if requested.MemoryBytes > 0 && available.MemoryBytes+1e-9 < requested.MemoryBytes {
		return false
	}
	if requested.NetworkMbps > 0 && available.NetworkMbps+1e-9 < requested.NetworkMbps {
		return false
	}
	if requested.StorageIOPS > 0 && available.StorageIOPS+1e-9 < requested.StorageIOPS {
		return false
	}
	return true
}

func addResource(a, b ResourceVector) ResourceVector {
	return ResourceVector{
		CPUCores:    a.CPUCores + b.CPUCores,
		GPUCards:    a.GPUCards + b.GPUCards,
		NPUSlices:   a.NPUSlices + b.NPUSlices,
		MemoryBytes: a.MemoryBytes + b.MemoryBytes,
		NetworkMbps: a.NetworkMbps + b.NetworkMbps,
		StorageIOPS: a.StorageIOPS + b.StorageIOPS,
	}
}

func subtractWithFloor(a, b ResourceVector) ResourceVector {
	return ResourceVector{
		CPUCores:    maxFloat(0, a.CPUCores-b.CPUCores),
		GPUCards:    maxFloat(0, a.GPUCards-b.GPUCards),
		NPUSlices:   maxFloat(0, a.NPUSlices-b.NPUSlices),
		MemoryBytes: maxFloat(0, a.MemoryBytes-b.MemoryBytes),
		NetworkMbps: maxFloat(0, a.NetworkMbps-b.NetworkMbps),
		StorageIOPS: maxFloat(0, a.StorageIOPS-b.StorageIOPS),
	}
}

func headroomScore(available, capacity float64) float64 {
	if capacity <= 0 {
		if available > 0 {
			return 1
		}
		return 0
	}
	return clamp01(available / capacity)
}

func classRank(class WorkloadClass) int {
	switch class {
	case WorkloadClassRealtime:
		return 0
	case WorkloadClassBatch:
		return 1
	default:
		return 2
	}
}

func priorityRank(priority PriorityClass) int {
	switch strings.ToUpper(strings.TrimSpace(string(priority))) {
	case "P0":
		return 0
	case "P1":
		return 1
	case "P2":
		return 2
	case "P3":
		return 3
	default:
		return 4
	}
}

func deadlineValue(deadline *time.Time) time.Time {
	if deadline == nil {
		return time.Unix(1<<62, 0)
	}
	return *deadline
}

func routeKey(service, model string, class WorkloadClass) string {
	return service + "|" + model + "|" + string(class)
}

func suggestedBatchSize(spec WorkloadSpec) int {
	if spec.Class == WorkloadClassRealtime {
		if spec.LatencySLOMs > 0 && spec.LatencySLOMs <= 120 {
			return 1
		}
		if spec.MaxBatchSize > 0 {
			if spec.MaxBatchSize > 4 {
				return 4
			}
			return spec.MaxBatchSize
		}
		return 1
	}

	if spec.MaxBatchSize > 0 {
		return spec.MaxBatchSize
	}
	if spec.TargetConcurrency > 0 {
		v := int(math.Ceil(float64(spec.TargetConcurrency) / 4))
		if v < 8 {
			return 8
		}
		if v > 64 {
			return 64
		}
		return v
	}
	return 16
}

func metricValue(metrics map[string]float64, key string) float64 {
	if metrics == nil {
		return 0
	}
	return metrics[key]
}

func metricValueIfPresent(metrics map[string]float64, key string) (float64, bool) {
	if metrics == nil {
		return 0, false
	}
	value, ok := metrics[key]
	return value, ok
}

func labelFloat(labels map[string]string, keys ...string) float64 {
	for _, key := range keys {
		value := strings.TrimSpace(labels[key])
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseFloat(value, 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func firstPositive(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonNegative(values ...float64) float64 {
	for _, value := range values {
		if value >= 0 {
			return value
		}
	}
	return 0
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

func toSet(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[value] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneNode(node ResourceNode) ResourceNode {
	node.Labels = copyStringMap(node.Labels)
	if len(node.ModelCaches) > 0 {
		caches := make(map[string]ModelCache, len(node.ModelCaches))
		for k, v := range node.ModelCaches {
			caches[k] = v
		}
		node.ModelCaches = caches
	} else {
		node.ModelCaches = map[string]ModelCache{}
	}
	return node
}

func cloneWorkload(w Workload) Workload {
	w.Spec.PreferredZones = append([]string(nil), w.Spec.PreferredZones...)
	w.Spec.PreferredClusters = append([]string(nil), w.Spec.PreferredClusters...)
	if len(w.Assignments) > 0 {
		assignments := make([]Assignment, len(w.Assignments))
		copy(assignments, w.Assignments)
		w.Assignments = assignments
	} else {
		w.Assignments = nil
	}
	return w
}

func cloneRoute(route RoutingPlan) RoutingPlan {
	if len(route.Targets) > 0 {
		targets := make([]RouteTarget, len(route.Targets))
		copy(targets, route.Targets)
		route.Targets = targets
	} else {
		route.Targets = nil
	}
	return route
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
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
