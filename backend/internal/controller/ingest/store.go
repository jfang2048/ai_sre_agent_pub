package ingest

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/collections/ring"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
)

// Store is the ingestion storage interface.
type Store interface {
	UpsertCollector(info *telemetryv1.CollectorInfo, heartbeat time.Time)
	StoreMetrics(collectorID string, metrics []*telemetryv1.Metric, receivedAt time.Time)
	StoreProcesses(collectorID string, processes []*telemetryv1.ProcessSample, receivedAt time.Time)
	StoreLogs(collectorID string, logs []*telemetryv1.LogFingerprint, receivedAt time.Time)
	StoreBatchMeta(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time)
	Snapshot() []*NodeSnapshot
	Node(collectorID string) *NodeSnapshot
}

// NodeSnapshot summarizes collector state for the API.
type NodeSnapshot struct {
	CollectorID string                        `json:"collector_id"`
	Hostname    string                        `json:"hostname"`
	Version     string                        `json:"version"`
	OS          string                        `json:"os"`
	Arch        string                        `json:"arch"`
	Labels      map[string]string             `json:"labels"`
	LastSeen    time.Time                     `json:"last_seen"`
	LastBatchID string                        `json:"last_batch_id"`
	IngestLagMs float64                       `json:"ingest_lag_ms"`
	Metrics     map[string]float64            `json:"metrics"`
	Processes   []*telemetryv1.ProcessSample  `json:"processes"`
	Logs        []*telemetryv1.LogFingerprint `json:"logs"`
	UpdatedAt   time.Time                     `json:"updated_at"`
	MetricCount int                           `json:"metric_count"`
	LogCount    int                           `json:"log_count"`

	// ProcessNetwork captures per-PID network pressure derived from RCA metrics
	// (connections, queued bytes, approximate throughput). This is kept lightweight
	// and only populated when metrics carry a pid label.
	ProcessNetwork map[string]*ProcessNetworkSample `json:"process_network,omitempty"`

	// ProcessResources stores kernel-level per-process/service resource signals.
	// It keeps latest values plus cumulative totals/frequencies for ranking.
	ProcessResources map[string]*ProcessResourceSample `json:"process_resources,omitempty"`

	// StorageDevices and StoragePartitions keep labeled low-level disk counters/rates.
	StorageDevices    map[string]*StorageDeviceSample `json:"storage_devices,omitempty"`
	StoragePartitions map[string]*StorageDeviceSample `json:"storage_partitions,omitempty"`
	Filesystems       map[string]*FilesystemSample    `json:"filesystems,omitempty"`

	// ProbeSource and ProbeCoreModules preserve label-aware probe runtime context
	// that cannot be represented by flat metric-name aggregation alone.
	ProbeSource      string                            `json:"probe_source,omitempty"`
	ProbeCoreModules map[string]*ProbeCoreModuleSample `json:"probe_core_modules,omitempty"`

	// eBPF-first runtime security/event graph payloads used by Security APIs and
	// agentic RCA context.
	RuntimeSecurityEvents []RuntimeSecurityEvent `json:"runtime_security_events,omitempty"`
	ProcessGraphSnapshot  ProcessGraphSnapshot   `json:"process_graph_snapshot,omitempty"`
	NetworkBehavior       NetworkBehaviorSummary `json:"network_behavior_summary,omitempty"`
	SyscallStatistics     map[string]uint64      `json:"syscall_statistics,omitempty"`
}

// ProbeCoreModuleSample captures requested/active state per probe-core module.
type ProbeCoreModuleSample struct {
	Module    string  `json:"module"`
	Requested float64 `json:"requested"`
	Active    float64 `json:"active"`
}

// ProcessNetworkSample represents network-related signals for a process.
type ProcessNetworkSample struct {
	PID            string  `json:"pid"`
	Name           string  `json:"name,omitempty"`
	Connections    int     `json:"connections"`
	QueuedBytes    float64 `json:"queued_bytes"`
	BytesPerSecond float64 `json:"bytes_per_second"`
}

// ProcessResourceSample holds per-process/per-service signal aggregates.
type ProcessResourceSample struct {
	Key      string    `json:"key"`
	PID      string    `json:"pid,omitempty"`
	Name     string    `json:"name,omitempty"`
	LastSeen time.Time `json:"last_seen"`

	WorkloadClass string `json:"workload_class,omitempty"`
	Job           string `json:"job,omitempty"`
	CommPattern   string `json:"comm_pattern,omitempty"`
	PodUID        string `json:"pod_uid,omitempty"`

	SignalValues    map[string]float64 `json:"signal_values,omitempty"`    // latest values
	SignalTotals    map[string]float64 `json:"signal_totals,omitempty"`    // cumulative totals (delta for counters)
	SignalFrequency map[string]uint64  `json:"signal_frequency,omitempty"` // observation count

	CategoryTotals    map[string]float64 `json:"category_totals,omitempty"`    // cumulative totals by resource category
	CategoryFrequency map[string]uint64  `json:"category_frequency,omitempty"` // observation count by category

	LogErrors   uint64 `json:"log_errors,omitempty"`
	LogWarnings uint64 `json:"log_warnings,omitempty"`
}

// RuntimeSecurityEvent is a normalized eBPF behavior/security event.
type RuntimeSecurityEvent struct {
	EvidenceID  string            `json:"evidence_id"`
	Timestamp   time.Time         `json:"timestamp"`
	Type        string            `json:"type"`
	Category    string            `json:"category"`
	PID         string            `json:"pid,omitempty"`
	Container   string            `json:"container,omitempty"`
	NodeScope   string            `json:"node_scope,omitempty"`
	Severity    string            `json:"severity"`
	Confidence  float64           `json:"confidence"`
	Description string            `json:"description"`
	Port        int               `json:"port,omitempty"`
	RemoteIP    string            `json:"remote_ip,omitempty"`
	Path        string            `json:"path,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// ProcessGraphSnapshot is a compact process lineage graph projection.
type ProcessGraphSnapshot struct {
	Nodes []ProcessGraphNode `json:"nodes,omitempty"`
	Edges []ProcessGraphEdge `json:"edges,omitempty"`
}

// ProcessGraphNode is one process node in lineage view.
type ProcessGraphNode struct {
	ID       string  `json:"id"`
	PID      string  `json:"pid,omitempty"`
	Name     string  `json:"name,omitempty"`
	Category string  `json:"category,omitempty"`
	Score    float64 `json:"score,omitempty"`
}

// ProcessGraphEdge is one process lineage edge.
type ProcessGraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

// NetworkBehaviorSummary keeps coarse runtime network behavior counters.
type NetworkBehaviorSummary struct {
	ConnectCalls       uint64 `json:"connect_calls"`
	AcceptCalls        uint64 `json:"accept_calls"`
	BindCalls          uint64 `json:"bind_calls"`
	LongLivedTCP       uint64 `json:"long_lived_tcp"`
	AbnormalBindPorts  uint64 `json:"abnormal_bind_ports"`
	UnexpectedOutbound uint64 `json:"unexpected_outbound"`
}

// StorageDeviceSample represents disk-level or partition-level IO state.
type StorageDeviceSample struct {
	Device        string    `json:"device"`
	Partition     string    `json:"partition,omitempty"`
	ParentDevice  string    `json:"parent_device,omitempty"`
	Scope         string    `json:"scope"` // device|partition
	LastUpdatedAt time.Time `json:"last_updated_at"`

	ReadBytesTotal       float64 `json:"read_bytes_total,omitempty"`
	WriteBytesTotal      float64 `json:"write_bytes_total,omitempty"`
	ReadBytesPerSecond   float64 `json:"read_bytes_per_second,omitempty"`
	WriteBytesPerSecond  float64 `json:"write_bytes_per_second,omitempty"`
	ReadIOPS             float64 `json:"read_iops,omitempty"`
	WriteIOPS            float64 `json:"write_iops,omitempty"`
	IOPS                 float64 `json:"iops,omitempty"`
	InFlightIO           float64 `json:"in_flight_io,omitempty"`
	QueueDepth           float64 `json:"queue_depth,omitempty"`
	QueueCapacity        float64 `json:"queue_capacity_requests,omitempty"`
	QueueFillPercent     float64 `json:"queue_fill_percent,omitempty"`
	InflightFillPercent  float64 `json:"inflight_fill_percent,omitempty"`
	UtilizationPercent   float64 `json:"utilization_percent,omitempty"`
	AvgReadLatencyMS     float64 `json:"avg_read_latency_ms,omitempty"`
	AvgWriteLatencyMS    float64 `json:"avg_write_latency_ms,omitempty"`
	AvgRequestLatencyMS  float64 `json:"avg_request_latency_ms,omitempty"`
	IOTimeSecondsTotal   float64 `json:"io_time_seconds_total,omitempty"`
	WeightedIOTimeSecond float64 `json:"weighted_io_time_seconds_total,omitempty"`
}

// FilesystemSample captures space/inode pressure per mounted filesystem.
type FilesystemSample struct {
	Mountpoint string `json:"mountpoint"`
	Device     string `json:"device,omitempty"`
	FSType     string `json:"fstype,omitempty"`
	ReadOnly   bool   `json:"read_only,omitempty"`

	SizeBytes        float64   `json:"size_bytes,omitempty"`
	FreeBytes        float64   `json:"free_bytes,omitempty"`
	AvailBytes       float64   `json:"avail_bytes,omitempty"`
	UsedBytes        float64   `json:"used_bytes,omitempty"`
	UsedPercent      float64   `json:"used_percent,omitempty"`
	FilesTotal       float64   `json:"files_total,omitempty"`
	FilesFree        float64   `json:"files_free,omitempty"`
	FilesUsed        float64   `json:"files_used,omitempty"`
	FilesUsedPercent float64   `json:"files_used_percent,omitempty"`
	LastUpdatedAt    time.Time `json:"last_updated_at"`
}

// MetricHistorySample captures a compact snapshot of trend-relevant metrics.
type MetricHistorySample struct {
	Timestamp time.Time          `json:"timestamp"`
	Metrics   map[string]float64 `json:"metrics"`
}

const (
	maxProcessResourcesPerNode = 4096
)

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu       sync.RWMutex
	cfg      StoreConfig
	logger   *zap.Logger
	nodes    map[string]*NodeSnapshot
	history  map[string]*ring.Ring[MetricHistorySample]
	logIndex *logindex.Index

	persistence *boltPersistence
	stopPersist chan struct{}
	donePersist chan struct{}

	stateVersion     uint64
	persistedVersion uint64
	lastPersistErr   string
}

// NewMemoryStore returns a new MemoryStore.
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithConfig(DefaultStoreConfig(), nil)
}

// NewMemoryStoreWithConfig creates a store with optional embedded persistence.
func NewMemoryStoreWithConfig(cfg StoreConfig, logger *zap.Logger) *MemoryStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = cfg.normalized()
	return &MemoryStore{
		cfg:     cfg,
		logger:  logger.With(zap.String("component", "ingest_store")),
		nodes:   make(map[string]*NodeSnapshot, 32),
		history: make(map[string]*ring.Ring[MetricHistorySample], 32),
	}
}

// StartPersistence enables periodic snapshot persistence to embedded storage.
// The hot in-memory path remains primary; persistence is asynchronous.
func (s *MemoryStore) StartPersistence() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.persistence != nil || !s.cfg.Persistence.Enabled {
		return
	}

	p, err := newBoltPersistence(s.cfg.Persistence)
	if err != nil {
		s.lastPersistErr = err.Error()
		s.logger.Warn("ingest persistence disabled; falling back to memory only", zap.Error(err))
		return
	}
	s.persistence = p

	nodes, history, err := p.loadSnapshot()
	if err != nil {
		s.lastPersistErr = err.Error()
		s.logger.Warn("failed to restore persisted ingest snapshot", zap.Error(err))
	} else {
		s.restoreSnapshotLocked(nodes, history, time.Now().UTC())
		s.logger.Info("restored ingest snapshot from embedded store",
			zap.Int("nodes", len(s.nodes)))
	}

	s.stopPersist = make(chan struct{})
	s.donePersist = make(chan struct{})
	go s.persistenceLoop()
}

// Close flushes persistence state and closes embedded resources.
func (s *MemoryStore) Close() error {
	s.mu.Lock()
	stop := s.stopPersist
	done := s.donePersist
	p := s.persistence
	s.mu.Unlock()

	if stop != nil {
		close(stop)
		if done != nil {
			<-done
		}
	}
	s.mu.Lock()
	s.stopPersist = nil
	s.donePersist = nil
	s.persistence = nil
	s.mu.Unlock()
	if p == nil {
		return nil
	}
	if err := p.close(); err != nil {
		return err
	}
	return nil
}

// AttachLogIndex wires the native log index to ingestion snapshots.
func (s *MemoryStore) AttachLogIndex(index *logindex.Index) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logIndex = index
}

// LogIndex returns the currently attached log index.
func (s *MemoryStore) LogIndex() *logindex.Index {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.logIndex
}

// FederationHints exposes stable partitioning contracts for future sharding/federation.
type FederationHints struct {
	PartitionKey string `json:"partition_key"`
	ShardKey     string `json:"shard_key"`
	Strategy     string `json:"strategy"`
}

// StoreStats summarizes retention and persistence runtime state.
type StoreStats struct {
	Nodes                 int              `json:"nodes"`
	HistorySeries         int              `json:"history_series"`
	HistorySamples        int              `json:"history_samples"`
	NodeRetention         string           `json:"node_retention"`
	HistorySamplesPerNode int              `json:"history_samples_per_node"`
	MaxNodes              int              `json:"max_nodes"`
	LastPersistError      string           `json:"last_persist_error,omitempty"`
	Persistence           PersistenceStats `json:"persistence"`
	Federation            FederationHints  `json:"federation"`
}

// Stats returns ingest store runtime metadata.
func (s *MemoryStore) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	historySamples := 0
	for _, h := range s.history {
		if h == nil {
			continue
		}
		historySamples += len(h.SliceOldest())
	}

	stats := StoreStats{
		Nodes:                 len(s.nodes),
		HistorySeries:         len(s.history),
		HistorySamples:        historySamples,
		NodeRetention:         s.cfg.NodeRetention.String(),
		HistorySamplesPerNode: s.cfg.HistorySamplesPerNode,
		MaxNodes:              s.cfg.MaxNodes,
		LastPersistError:      strings.TrimSpace(s.lastPersistErr),
		Persistence: PersistenceStats{
			Enabled:      false,
			MaxDBBytes:   s.cfg.Persistence.MaxDBSizeBytes,
			SyncInterval: s.cfg.Persistence.SyncInterval.String(),
		},
		Federation: FederationHints{
			PartitionKey: "collector_id",
			ShardKey:     "collector_id",
			Strategy:     "single-node-now, consistent-hash-ready",
		},
	}
	if s.persistence != nil {
		stats.Persistence = s.persistence.stats()
	}
	return stats
}

// SetRetention updates bounded in-memory retention and applies it immediately.
func (s *MemoryStore) SetRetention(nodeRetention time.Duration, historySamplesPerNode int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if nodeRetention <= 0 {
		return errors.New("node retention must be > 0")
	}
	if historySamplesPerNode <= 0 {
		return errors.New("history_samples_per_node must be > 0")
	}

	s.cfg.NodeRetention = nodeRetention
	s.cfg.HistorySamplesPerNode = historySamplesPerNode
	s.pruneNodesLocked(time.Now().UTC())
	for collectorID, h := range s.history {
		if h == nil {
			continue
		}
		points := h.SliceOldest()
		next := ring.New[MetricHistorySample](historySamplesPerNode)
		start := 0
		if len(points) > historySamplesPerNode {
			start = len(points) - historySamplesPerNode
		}
		for i := start; i < len(points); i++ {
			next.Push(points[i])
		}
		s.history[collectorID] = next
	}
	s.markDirtyLocked(time.Now().UTC())
	return nil
}

func (s *MemoryStore) persistenceLoop() {
	interval := s.cfg.Persistence.SyncInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(s.donePersist)

	for {
		select {
		case <-ticker.C:
			s.flushPersistence(false)
		case <-s.stopPersist:
			s.flushPersistence(true)
			return
		}
	}
}

func (s *MemoryStore) flushPersistence(force bool) {
	nodes, history, version, shouldPersist, persistence := s.snapshotForPersistence(force)
	if persistence == nil {
		return
	}

	if shouldPersist {
		if err := persistence.saveSnapshot(nodes, history); err != nil {
			s.mu.Lock()
			s.lastPersistErr = err.Error()
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		if version > s.persistedVersion {
			s.persistedVersion = version
		}
		s.lastPersistErr = ""
		s.mu.Unlock()
	}

	if force {
		if err := persistence.compactNow(); err != nil {
			s.mu.Lock()
			s.lastPersistErr = err.Error()
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		s.lastPersistErr = ""
		s.mu.Unlock()
	}
}

func (s *MemoryStore) snapshotForPersistence(force bool) (map[string]*NodeSnapshot, map[string][]MetricHistorySample, uint64, bool, *boltPersistence) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.persistence == nil {
		return nil, nil, 0, false, nil
	}
	shouldPersist := s.persistedVersion < s.stateVersion
	if !shouldPersist && !force {
		return nil, nil, s.stateVersion, false, s.persistence
	}

	nodes := make(map[string]*NodeSnapshot, len(s.nodes))
	for key, node := range s.nodes {
		if node == nil {
			continue
		}
		nodes[key] = cloneNode(node)
	}
	history := make(map[string][]MetricHistorySample, len(s.history))
	for key, h := range s.history {
		if h == nil {
			continue
		}
		samples := h.SliceOldest()
		cloned := make([]MetricHistorySample, 0, len(samples))
		for _, sample := range samples {
			cloned = append(cloned, MetricHistorySample{
				Timestamp: sample.Timestamp,
				Metrics:   cloneMetricMap(sample.Metrics),
			})
		}
		history[key] = cloned
	}
	return nodes, history, s.stateVersion, shouldPersist, s.persistence
}

func (s *MemoryStore) restoreSnapshotLocked(nodes map[string]*NodeSnapshot, history map[string][]MetricHistorySample, now time.Time) {
	if len(nodes) > 0 {
		s.nodes = make(map[string]*NodeSnapshot, len(nodes))
		for key, node := range nodes {
			if node == nil {
				continue
			}
			s.nodes[key] = cloneNode(node)
		}
	}
	if len(history) > 0 {
		s.history = make(map[string]*ring.Ring[MetricHistorySample], len(history))
		for key, samples := range history {
			series := ring.New[MetricHistorySample](s.cfg.HistorySamplesPerNode)
			start := 0
			if len(samples) > s.cfg.HistorySamplesPerNode {
				start = len(samples) - s.cfg.HistorySamplesPerNode
			}
			for i := start; i < len(samples); i++ {
				series.Push(MetricHistorySample{
					Timestamp: samples[i].Timestamp,
					Metrics:   cloneMetricMap(samples[i].Metrics),
				})
			}
			s.history[key] = series
		}
	}
	s.pruneNodesLocked(now)
}

func (s *MemoryStore) markDirtyLocked(now time.Time) {
	s.stateVersion++
	s.pruneNodesLocked(now)
}

func (s *MemoryStore) pruneNodesLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if s.cfg.NodeRetention > 0 {
		cutoff := now.Add(-s.cfg.NodeRetention)
		for collectorID, node := range s.nodes {
			if node == nil {
				continue
			}
			seenAt := node.LastSeen
			if seenAt.IsZero() {
				seenAt = node.UpdatedAt
			}
			if seenAt.IsZero() || seenAt.Before(cutoff) {
				delete(s.nodes, collectorID)
				delete(s.history, collectorID)
			}
		}
	}
	if s.cfg.MaxNodes > 0 && len(s.nodes) > s.cfg.MaxNodes {
		type entry struct {
			id     string
			seenAt time.Time
		}
		entries := make([]entry, 0, len(s.nodes))
		for id, node := range s.nodes {
			if node == nil {
				continue
			}
			seenAt := node.LastSeen
			if seenAt.IsZero() {
				seenAt = node.UpdatedAt
			}
			entries = append(entries, entry{id: id, seenAt: seenAt})
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].seenAt.Before(entries[j].seenAt)
		})
		drop := len(entries) - s.cfg.MaxNodes
		for i := 0; i < drop; i++ {
			delete(s.nodes, entries[i].id)
			delete(s.history, entries[i].id)
		}
	}
}

func (s *MemoryStore) UpsertCollector(info *telemetryv1.CollectorInfo, heartbeat time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node := s.ensureNode(info.CollectorId)
	node.CollectorID = info.CollectorId
	node.Hostname = info.Hostname
	node.Version = info.Version
	node.OS = info.Os
	node.Arch = info.Arch
	node.Labels = labelsToMap(info.Labels)
	node.LastSeen = heartbeat
	node.UpdatedAt = heartbeat
	s.markDirtyLocked(heartbeat)
}

func (s *MemoryStore) StoreMetrics(collectorID string, metrics []*telemetryv1.Metric, receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node := s.ensureNode(collectorID)
	node.Metrics = make(map[string]float64, len(metrics))
	node.ProcessNetwork = nil
	node.StorageDevices = nil
	node.StoragePartitions = nil
	node.Filesystems = nil
	node.ProbeSource = ""
	node.ProbeCoreModules = nil
	if node.SyscallStatistics == nil {
		node.SyscallStatistics = make(map[string]uint64)
	}
	for _, metric := range metrics {
		if metric == nil || metric.Name == "" {
			continue
		}

		if captureRuntimeEBPFMetric(node, metric, receivedAt) {
			continue
		}

		captureProbeCoreMetric(node, metric)

		captureStorageMetric(node, metric, receivedAt)

		if shouldAggregateMetric(metric.Name) {
			node.Metrics[metric.Name] += metric.Value
		} else {
			node.Metrics[metric.Name] = metric.Value
		}

		pid, name := metricProcessIdentity(metric)
		if (pid != "" || name != "") && metric.Name != "" {
			if process := ensureProcessResource(node, pid, name); process != nil {
				applyProcessContextFromMetric(process, metric)
				categories := metricCategories(metric)
				recordProcessSignal(process, metric.Name, metric.Value, categories, 1, receivedAt)
			}
		}

		// Capture per-process network RCA metrics so we can surface top programs
		// without losing label context. We only track a small set to avoid
		// excessive memory growth.
		pidLabel, processName := metricProcessIdentity(metric)
		if pidLabel != "" {
			sample := ensureProcessNetwork(node, pidLabel)
			if processName != "" {
				sample.Name = processName
			}

			switch metric.Name {
			case "rca_net_process_connections":
				sample.Connections = int(metric.Value)
			case "rca_net_process_queued_bytes":
				sample.QueuedBytes = metric.Value
			case "rca_net_process_bytes_per_second":
				// Not currently emitted, but supported for forward-compat.
				sample.BytesPerSecond = metric.Value
			}
		}
	}
	node.MetricCount = len(metrics)
	node.UpdatedAt = receivedAt
	node.ProcessGraphSnapshot = buildProcessGraphSnapshot(node)
	pruneProcessResources(node)
	s.recordMetricHistory(collectorID, receivedAt, node.Metrics)
	s.markDirtyLocked(receivedAt)
}

func captureProbeCoreMetric(node *NodeSnapshot, metric *telemetryv1.Metric) {
	if node == nil || metric == nil {
		return
	}
	switch metric.Name {
	case "collector_probe_source":
		source := strings.TrimSpace(labelValue(metric, "source"))
		if source != "" {
			node.ProbeSource = source
		}
	case "collector_probe_core_collector_module_requested":
		module := strings.TrimSpace(labelValue(metric, "module"))
		if module == "" {
			return
		}
		sample := ensureProbeCoreModuleSample(node, module)
		sample.Requested = metric.Value
	case "collector_probe_core_collector_module_active":
		module := strings.TrimSpace(labelValue(metric, "module"))
		if module == "" {
			return
		}
		sample := ensureProbeCoreModuleSample(node, module)
		sample.Active = metric.Value
	}
}

const maxRuntimeSecurityEvents = 512

// captureRuntimeEBPFMetric ingests eBPF runtime events/summary metrics emitted by
// the collector. It returns true when the metric is fully handled and should not
// enter flat metric aggregation.
func captureRuntimeEBPFMetric(node *NodeSnapshot, metric *telemetryv1.Metric, receivedAt time.Time) bool {
	if node == nil || metric == nil {
		return false
	}

	switch metric.Name {
	case "node_ebpf_runtime_event":
		event := RuntimeSecurityEvent{
			EvidenceID:  strings.TrimSpace(labelValue(metric, "evidence_id")),
			Type:        strings.TrimSpace(labelValue(metric, "type")),
			Category:    strings.TrimSpace(labelValue(metric, "category")),
			PID:         strings.TrimSpace(labelValue(metric, "pid")),
			Container:   strings.TrimSpace(labelValue(metric, "container")),
			NodeScope:   firstNonEmpty(strings.TrimSpace(labelValue(metric, "scope")), "node"),
			Severity:    firstNonEmpty(strings.TrimSpace(labelValue(metric, "severity")), "medium"),
			Confidence:  metric.Value,
			Description: strings.TrimSpace(labelValue(metric, "description")),
			RemoteIP:    strings.TrimSpace(labelValue(metric, "remote_ip")),
			Path:        strings.TrimSpace(labelValue(metric, "path")),
			Port:        parseLabelInt(labelValue(metric, "port")),
			Metadata:    map[string]string{},
		}
		if conf := strings.TrimSpace(labelValue(metric, "confidence")); conf != "" {
			if parsed, err := strconv.ParseFloat(conf, 64); err == nil {
				event.Confidence = parsed
			}
		}
		if event.Confidence <= 0 {
			event.Confidence = 0.5
		}
		if event.EvidenceID == "" {
			event.EvidenceID = "ev-ebpf-unknown"
		}
		if ts := strings.TrimSpace(labelValue(metric, "ts_unix_nano")); ts != "" {
			if ns, err := strconv.ParseInt(ts, 10, 64); err == nil {
				event.Timestamp = time.Unix(0, ns).UTC()
			}
		}
		if event.Timestamp.IsZero() {
			event.Timestamp = receivedAt
		}

		if node.RuntimeSecurityEvents == nil {
			node.RuntimeSecurityEvents = make([]RuntimeSecurityEvent, 0, 64)
		}
		if !runtimeEventExists(node.RuntimeSecurityEvents, event.EvidenceID) {
			node.RuntimeSecurityEvents = append(node.RuntimeSecurityEvents, event)
		}
		if len(node.RuntimeSecurityEvents) > maxRuntimeSecurityEvents {
			node.RuntimeSecurityEvents = node.RuntimeSecurityEvents[len(node.RuntimeSecurityEvents)-maxRuntimeSecurityEvents:]
		}
		updateNetworkBehaviorFromEvent(&node.NetworkBehavior, event)
		return true

	case "node_ebpf_syscall_statistics_total", "node_ebpf_syscall_count":
		syscall := strings.TrimSpace(labelValue(metric, "syscall"))
		if syscall != "" {
			if node.SyscallStatistics == nil {
				node.SyscallStatistics = make(map[string]uint64)
			}
			if metric.Value >= 0 {
				node.SyscallStatistics[syscall] = uint64(metric.Value)
			}
		}
		return true

	case "node_ebpf_abnormal_bind_ports_count":
		node.NetworkBehavior.AbnormalBindPorts = uint64(maxFloat64(metric.Value, 0))
		return true
	case "node_ebpf_long_lived_tcp_connections":
		node.NetworkBehavior.LongLivedTCP = uint64(maxFloat64(metric.Value, 0))
		return true
	}
	return false
}

func runtimeEventExists(events []RuntimeSecurityEvent, evidenceID string) bool {
	for _, event := range events {
		if strings.EqualFold(event.EvidenceID, evidenceID) {
			return true
		}
	}
	return false
}

func updateNetworkBehaviorFromEvent(summary *NetworkBehaviorSummary, event RuntimeSecurityEvent) {
	if summary == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(event.Type)) {
	case "connect":
		summary.ConnectCalls++
	case "accept":
		summary.AcceptCalls++
	case "bind":
		summary.BindCalls++
	case "long_lived_tcp":
		summary.LongLivedTCP++
	case "abnormal_bind_port":
		summary.AbnormalBindPorts++
	case "unexpected_outbound", "suspicious_outbound":
		summary.UnexpectedOutbound++
	}
}

func parseLabelInt(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return v
}

func maxFloat64(v, fallback float64) float64 {
	if v < fallback {
		return fallback
	}
	return v
}

func ensureProbeCoreModuleSample(node *NodeSnapshot, module string) *ProbeCoreModuleSample {
	if node == nil {
		return nil
	}
	module = strings.TrimSpace(strings.ToLower(module))
	if module == "" {
		return nil
	}
	if node.ProbeCoreModules == nil {
		node.ProbeCoreModules = make(map[string]*ProbeCoreModuleSample)
	}
	sample, ok := node.ProbeCoreModules[module]
	if !ok {
		sample = &ProbeCoreModuleSample{Module: module}
		node.ProbeCoreModules[module] = sample
	}
	return sample
}

func (s *MemoryStore) StoreProcesses(collectorID string, processes []*telemetryv1.ProcessSample, receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node := s.ensureNode(collectorID)
	node.Processes = processes
	for _, p := range processes {
		if p == nil || p.Pid <= 0 {
			continue
		}
		process := ensureProcessResource(node, strconv.Itoa(int(p.Pid)), p.Name)
		if process == nil {
			continue
		}
		recordProcessSignal(process, "node_process_cpu_percent", p.CpuPercent, []string{"cpu"}, 1, receivedAt)
		recordProcessSignal(process, "node_process_memory_rss_bytes", float64(p.RssBytes), []string{"memory"}, 1, receivedAt)
		recordProcessSignal(process, "node_process_io_read_bytes_per_second", p.IoReadBps, []string{"disk_io", "disk"}, 1, receivedAt)
		recordProcessSignal(process, "node_process_io_write_bytes_per_second", p.IoWriteBps, []string{"disk_io", "disk"}, 1, receivedAt)
	}
	node.UpdatedAt = receivedAt
	pruneProcessResources(node)
	s.markDirtyLocked(receivedAt)
}

func (s *MemoryStore) StoreLogs(collectorID string, logs []*telemetryv1.LogFingerprint, receivedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node := s.ensureNode(collectorID)
	node.Logs = logs
	node.LogCount = len(logs)
	node.UpdatedAt = receivedAt

	for _, lf := range logs {
		if lf == nil || lf.Example == "" || lf.Count == 0 {
			continue
		}
		pid, name := guessProcessFromLog(lf.Example)
		process := ensureProcessResource(node, pid, name)
		if process == nil {
			continue
		}

		// Always account for observed log volume so "Logs" ranking works even when
		// messages are mostly informational and do not include warning/error tokens.
		recordProcessSignal(process, "log_lines", float64(lf.Count), []string{"logs"}, lf.Count, receivedAt)

		severity := logSeverity(lf.Example)
		switch severity {
		case "error":
			process.LogErrors += lf.Count
			recordProcessSignal(process, "log_errors", float64(lf.Count), nil, lf.Count, receivedAt)
		case "warn":
			process.LogWarnings += lf.Count
			recordProcessSignal(process, "log_warnings", float64(lf.Count), nil, lf.Count, receivedAt)
		}
	}

	pruneProcessResources(node)
	if s.logIndex != nil {
		s.indexLogFingerprintsLocked(node, collectorID, logs, receivedAt)
	}
	s.markDirtyLocked(receivedAt)
}

func (s *MemoryStore) StoreBatchMeta(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) {
	if batch == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	node := s.ensureNode(collectorID)
	node.LastBatchID = batch.BatchId
	node.LastSeen = receivedAt
	node.UpdatedAt = receivedAt
	if batch.WallTimeUnixNano > 0 {
		lag := receivedAt.Sub(time.Unix(0, batch.WallTimeUnixNano)).Seconds() * 1000
		if lag < 0 {
			lag = 0
		}
		node.IngestLagMs = lag
	}
	s.markDirtyLocked(receivedAt)
}

func (s *MemoryStore) Snapshot() []*NodeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*NodeSnapshot, 0, len(s.nodes))
	for _, node := range s.nodes {
		out = append(out, cloneNode(node))
	}
	return out
}

func (s *MemoryStore) Node(collectorID string) *NodeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if node, ok := s.nodes[collectorID]; ok {
		return cloneNode(node)
	}
	return nil
}

// MetricHistory returns trend-relevant metric snapshots for one collector.
func (s *MemoryStore) MetricHistory(collectorID string, since time.Time, limit int) []MetricHistorySample {
	s.mu.RLock()
	h := s.history[collectorID]
	if h == nil {
		s.mu.RUnlock()
		return []MetricHistorySample{}
	}
	samples := h.SliceOldest()
	s.mu.RUnlock()

	if len(samples) == 0 {
		return []MetricHistorySample{}
	}

	out := make([]MetricHistorySample, 0, len(samples))
	for _, sample := range samples {
		if !since.IsZero() && sample.Timestamp.Before(since) {
			continue
		}
		if len(sample.Metrics) == 0 {
			continue
		}
		out = append(out, MetricHistorySample{
			Timestamp: sample.Timestamp,
			Metrics:   cloneMetricMap(sample.Metrics),
		})
	}

	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}

	return out
}

func (s *MemoryStore) recordMetricHistory(collectorID string, receivedAt time.Time, metrics map[string]float64) {
	if collectorID == "" || len(metrics) == 0 {
		return
	}
	selected := selectTrendMetrics(metrics)
	if len(selected) == 0 {
		return
	}
	h := s.history[collectorID]
	if h == nil {
		h = ring.New[MetricHistorySample](s.cfg.HistorySamplesPerNode)
		s.history[collectorID] = h
	}
	h.Push(MetricHistorySample{
		Timestamp: receivedAt,
		Metrics:   selected,
	})
}

func (s *MemoryStore) ensureNode(collectorID string) *NodeSnapshot {
	node, ok := s.nodes[collectorID]
	if !ok {
		node = &NodeSnapshot{CollectorID: collectorID}
		s.nodes[collectorID] = node
	}
	return node
}

// labelValue fetches a label value from a metric's label slice.
func labelValue(m *telemetryv1.Metric, key string) string {
	if m == nil {
		return ""
	}
	for _, l := range m.Labels {
		if l != nil && l.Key == key {
			return l.Value
		}
	}
	return ""
}

func ensureProcessNetwork(node *NodeSnapshot, pid string) *ProcessNetworkSample {
	if node.ProcessNetwork == nil {
		node.ProcessNetwork = make(map[string]*ProcessNetworkSample)
	}
	sample, ok := node.ProcessNetwork[pid]
	if !ok {
		sample = &ProcessNetworkSample{PID: pid}
		node.ProcessNetwork[pid] = sample
	}
	return sample
}

func ensureProcessResource(node *NodeSnapshot, pid, name string) *ProcessResourceSample {
	key := processResourceKey(pid, name)
	if key == "" {
		return nil
	}
	if node.ProcessResources == nil {
		node.ProcessResources = make(map[string]*ProcessResourceSample)
	}
	process, ok := node.ProcessResources[key]
	if !ok {
		process = &ProcessResourceSample{
			Key:               key,
			PID:               pid,
			Name:              name,
			SignalValues:      make(map[string]float64, 16),
			SignalTotals:      make(map[string]float64, 16),
			SignalFrequency:   make(map[string]uint64, 16),
			CategoryTotals:    make(map[string]float64, 8),
			CategoryFrequency: make(map[string]uint64, 8),
		}
		node.ProcessResources[key] = process
	}
	if process.PID == "" && pid != "" {
		process.PID = pid
	}
	if process.Name == "" && name != "" {
		process.Name = name
	}
	return process
}

func processResourceKey(pid, name string) string {
	pid = strings.TrimSpace(pid)
	if pid != "" {
		return "pid|" + pid
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	return "name|" + name
}

func metricProcessIdentity(metric *telemetryv1.Metric) (string, string) {
	if metric == nil {
		return "", ""
	}
	pid := labelValue(metric, "pid")
	if pid == "" {
		pid = labelValue(metric, "tgid")
	}
	if pid == "" {
		pid = labelValue(metric, "process_id")
	}
	name := labelValue(metric, "name")
	if name == "" {
		name = labelValue(metric, "process")
	}
	if name == "" {
		name = labelValue(metric, "comm")
	}
	if name == "" {
		name = labelValue(metric, "service")
	}
	if name == "" {
		name = labelValue(metric, "program")
	}
	if name == "" {
		name = labelValue(metric, "exe")
	}
	if name == "" {
		name = labelValue(metric, "command")
	}
	if name == "" {
		name = labelValue(metric, "cmd")
	}
	if name == "" {
		name = labelValue(metric, "unit")
	}
	if name == "" {
		name = labelValue(metric, "app")
	}
	if name == "" {
		name = labelValue(metric, "logger")
	}
	return strings.TrimSpace(pid), strings.TrimSpace(name)
}

func applyProcessContextFromMetric(process *ProcessResourceSample, metric *telemetryv1.Metric) {
	if process == nil || metric == nil {
		return
	}

	workloadClass := normalizeContextValue(firstNonEmpty(
		labelValue(metric, "workload_class"),
		labelValue(metric, "workload"),
		labelValue(metric, "workload_type"),
	))
	job := normalizeContextValue(firstNonEmpty(
		labelValue(metric, "job"),
		labelValue(metric, "job_name"),
		labelValue(metric, "run_name"),
		labelValue(metric, "experiment"),
	))
	commPattern := normalizeContextValue(firstNonEmpty(
		labelValue(metric, "comm_pattern"),
		labelValue(metric, "collective"),
		labelValue(metric, "comm"),
	))
	podUID := normalizeContextValue(firstNonEmpty(
		labelValue(metric, "pod_uid"),
		labelValue(metric, "k8s_pod_uid"),
	))

	if workloadClass != "" && (process.WorkloadClass == "" || process.WorkloadClass == "unknown") {
		process.WorkloadClass = workloadClass
	}
	if job != "" && process.Job == "" {
		process.Job = job
	}
	if commPattern != "" && process.CommPattern == "" {
		process.CommPattern = commPattern
	}
	if podUID != "" && process.PodUID == "" {
		process.PodUID = podUID
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeContextValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 96 {
		value = value[:96]
	}
	return value
}

func captureStorageMetric(node *NodeSnapshot, metric *telemetryv1.Metric, now time.Time) {
	if node == nil || metric == nil {
		return
	}

	name := metric.Name
	switch {
	case strings.HasPrefix(name, "node_disk_"):
		device := strings.TrimSpace(labelValue(metric, "device"))
		if device == "" {
			return
		}
		partition := strings.TrimSpace(labelValue(metric, "partition"))
		parent := device
		scope := "device"
		if strings.HasPrefix(name, "node_disk_partition_") || partition != "" || strings.EqualFold(labelValue(metric, "scope"), "partition") {
			scope = "partition"
			if partition == "" {
				partition = device
			}
		}
		if scope == "partition" {
			parent = device
		}

		sample := ensureStorageDeviceSample(node, device, partition, parent, scope)
		if sample == nil {
			return
		}
		sample.LastUpdatedAt = now

		switch name {
		case "node_disk_read_bytes_total", "node_disk_partition_read_bytes_total":
			sample.ReadBytesTotal = metric.Value
		case "node_disk_written_bytes_total", "node_disk_partition_written_bytes_total":
			sample.WriteBytesTotal = metric.Value
		case "node_disk_read_bytes_per_second", "node_disk_partition_read_bytes_per_second":
			sample.ReadBytesPerSecond = metric.Value
		case "node_disk_written_bytes_per_second", "node_disk_partition_written_bytes_per_second":
			sample.WriteBytesPerSecond = metric.Value
		case "node_disk_reads_per_second", "node_disk_partition_reads_per_second":
			sample.ReadIOPS = metric.Value
		case "node_disk_writes_per_second", "node_disk_partition_writes_per_second":
			sample.WriteIOPS = metric.Value
		case "node_disk_iops_per_second", "node_disk_partition_iops_per_second":
			sample.IOPS = metric.Value
		case "node_disk_io_now":
			sample.InFlightIO = metric.Value
		case "node_disk_queue_depth":
			sample.QueueDepth = metric.Value
		case "node_disk_queue_capacity_requests":
			sample.QueueCapacity = metric.Value
		case "node_disk_queue_depth_fill_percent":
			sample.QueueFillPercent = metric.Value
		case "node_disk_io_inflight_fill_percent":
			sample.InflightFillPercent = metric.Value
		case "node_disk_utilization_percent":
			sample.UtilizationPercent = metric.Value
		case "node_disk_avg_read_latency_seconds":
			sample.AvgReadLatencyMS = metric.Value * 1000.0
		case "node_disk_avg_write_latency_seconds":
			sample.AvgWriteLatencyMS = metric.Value * 1000.0
		case "node_disk_avg_request_latency_seconds":
			sample.AvgRequestLatencyMS = metric.Value * 1000.0
		case "node_disk_io_time_seconds_total":
			sample.IOTimeSecondsTotal = metric.Value
		case "node_disk_weighted_io_time_seconds_total":
			sample.WeightedIOTimeSecond = metric.Value
		}
		if sample.IOPS == 0 {
			sample.IOPS = sample.ReadIOPS + sample.WriteIOPS
		}
		return

	case strings.HasPrefix(name, "node_filesystem_"):
		mountpoint := strings.TrimSpace(labelValue(metric, "mountpoint"))
		if mountpoint == "" {
			return
		}
		sample := ensureFilesystemSample(node, mountpoint)
		if sample == nil {
			return
		}
		sample.LastUpdatedAt = now
		if device := strings.TrimSpace(labelValue(metric, "device")); device != "" {
			sample.Device = device
		}
		if fsType := strings.TrimSpace(labelValue(metric, "fstype")); fsType != "" {
			sample.FSType = fsType
		}
		switch name {
		case "node_filesystem_size_bytes":
			sample.SizeBytes = metric.Value
		case "node_filesystem_free_bytes":
			sample.FreeBytes = metric.Value
		case "node_filesystem_avail_bytes":
			sample.AvailBytes = metric.Value
		case "node_filesystem_used_bytes":
			sample.UsedBytes = metric.Value
		case "node_filesystem_used_percent":
			sample.UsedPercent = metric.Value
		case "node_filesystem_files":
			sample.FilesTotal = metric.Value
		case "node_filesystem_files_free":
			sample.FilesFree = metric.Value
		case "node_filesystem_files_used":
			sample.FilesUsed = metric.Value
		case "node_filesystem_files_used_percent":
			sample.FilesUsedPercent = metric.Value
		case "node_filesystem_readonly":
			sample.ReadOnly = metric.Value >= 0.5
		}
		return
	}
}

func ensureStorageDeviceSample(node *NodeSnapshot, device, partition, parent, scope string) *StorageDeviceSample {
	if node == nil {
		return nil
	}
	key := strings.TrimSpace(device)
	if strings.EqualFold(scope, "partition") {
		key = strings.TrimSpace(partition)
		if key == "" {
			return nil
		}
		if node.StoragePartitions == nil {
			node.StoragePartitions = make(map[string]*StorageDeviceSample)
		}
		sample, ok := node.StoragePartitions[key]
		if !ok {
			sample = &StorageDeviceSample{
				Device:       strings.TrimSpace(parent),
				Partition:    key,
				ParentDevice: strings.TrimSpace(parent),
				Scope:        "partition",
			}
			node.StoragePartitions[key] = sample
		}
		if sample.Device == "" {
			sample.Device = strings.TrimSpace(parent)
		}
		if sample.ParentDevice == "" {
			sample.ParentDevice = strings.TrimSpace(parent)
		}
		if sample.Partition == "" {
			sample.Partition = key
		}
		return sample
	}

	if key == "" {
		return nil
	}
	if node.StorageDevices == nil {
		node.StorageDevices = make(map[string]*StorageDeviceSample)
	}
	sample, ok := node.StorageDevices[key]
	if !ok {
		sample = &StorageDeviceSample{
			Device: key,
			Scope:  "device",
		}
		node.StorageDevices[key] = sample
	}
	return sample
}

func ensureFilesystemSample(node *NodeSnapshot, mountpoint string) *FilesystemSample {
	if node == nil || mountpoint == "" {
		return nil
	}
	if node.Filesystems == nil {
		node.Filesystems = make(map[string]*FilesystemSample)
	}
	sample, ok := node.Filesystems[mountpoint]
	if !ok {
		sample = &FilesystemSample{Mountpoint: mountpoint}
		node.Filesystems[mountpoint] = sample
	}
	return sample
}

func metricCategories(metric *telemetryv1.Metric) []string {
	if metric == nil {
		return nil
	}
	name := metric.Name
	switch {
	case strings.HasPrefix(name, "rca_cpu_process_"),
		name == "node_process_cpu_percent":
		return []string{"cpu"}

	case strings.HasPrefix(name, "rca_memory_process_"),
		name == "rca_memory_region_rss_bytes",
		name == "node_process_memory_rss_bytes":
		return []string{"memory"}

	case name == "rca_io_process_read_bytes_total",
		name == "rca_io_process_write_bytes_total":
		return []string{"disk"}

	case strings.HasPrefix(name, "rca_io_process_"),
		name == "node_process_io_read_bytes_per_second",
		name == "node_process_io_write_bytes_per_second":
		return []string{"disk_io", "disk"}

	case strings.HasPrefix(name, "rca_net_process_"),
		name == "rca_net_connection_queue_bytes":
		return []string{"network"}

	case strings.HasPrefix(name, "node_gpu_process_"):
		return []string{"gpu"}

	case name == "node_ebpf_process_events_total":
		switch ebpfProcessCategory(labelValue(metric, "type")) {
		case "cpu":
			return []string{"cpu"}
		case "disk_io":
			return []string{"disk_io", "disk"}
		case "network":
			return []string{"network"}
		case "memory":
			return []string{"memory"}
		case "gpu":
			return []string{"gpu"}
		default:
			return nil
		}
	default:
		return nil
	}
}

func ebpfProcessCategory(eventType string) string {
	evt := strings.ToLower(strings.TrimSpace(eventType))
	switch {
	case strings.Contains(evt, "sched"), strings.Contains(evt, "wake"), strings.Contains(evt, "switch"):
		return "cpu"
	case strings.Contains(evt, "block"), strings.Contains(evt, "rq"), strings.Contains(evt, "io"):
		return "disk_io"
	case strings.Contains(evt, "net"), strings.Contains(evt, "tcp"), strings.Contains(evt, "udp"), strings.Contains(evt, "sock"), strings.Contains(evt, "connect"), strings.Contains(evt, "accept"):
		return "network"
	case strings.Contains(evt, "mem"), strings.Contains(evt, "oom"), strings.Contains(evt, "page"), strings.Contains(evt, "psi"):
		return "memory"
	case strings.Contains(evt, "gpu"), strings.Contains(evt, "cuda"), strings.Contains(evt, "nv"), strings.Contains(evt, "nvidia"):
		return "gpu"
	default:
		return ""
	}
}

func recordProcessSignal(process *ProcessResourceSample, signal string, value float64, categories []string, frequencyWeight uint64, now time.Time) {
	if process == nil || signal == "" {
		return
	}
	if process.SignalValues == nil {
		process.SignalValues = make(map[string]float64, 16)
	}
	if process.SignalTotals == nil {
		process.SignalTotals = make(map[string]float64, 16)
	}
	if process.SignalFrequency == nil {
		process.SignalFrequency = make(map[string]uint64, 16)
	}
	if process.CategoryTotals == nil {
		process.CategoryTotals = make(map[string]float64, 8)
	}
	if process.CategoryFrequency == nil {
		process.CategoryFrequency = make(map[string]uint64, 8)
	}
	if frequencyWeight == 0 {
		frequencyWeight = 1
	}

	prev, hadPrev := process.SignalValues[signal]
	process.SignalValues[signal] = value
	process.LastSeen = now

	delta := value
	if isCounterSignal(signal) && hadPrev && value >= prev {
		delta = value - prev
	}
	if delta < 0 {
		delta = 0
	}
	process.SignalTotals[signal] += delta
	if value > 0 {
		process.SignalFrequency[signal] += frequencyWeight
	}

	for _, category := range categories {
		if category == "" {
			continue
		}
		process.CategoryTotals[category] += delta
		if value > 0 {
			process.CategoryFrequency[category] += frequencyWeight
		}
	}
}

func isCounterSignal(signal string) bool {
	return strings.HasSuffix(signal, "_total")
}

func pruneProcessResources(node *NodeSnapshot) {
	if node == nil || len(node.ProcessResources) <= maxProcessResourcesPerNode {
		return
	}
	type ranked struct {
		key      string
		lastSeen time.Time
		score    float64
	}
	rankedEntries := make([]ranked, 0, len(node.ProcessResources))
	for key, process := range node.ProcessResources {
		if process == nil {
			continue
		}
		score := 0.0
		for _, v := range process.CategoryTotals {
			score += v
		}
		score += float64(process.LogErrors*2 + process.LogWarnings)
		rankedEntries = append(rankedEntries, ranked{key: key, lastSeen: process.LastSeen, score: score})
	}
	sort.Slice(rankedEntries, func(i, j int) bool {
		if !rankedEntries[i].lastSeen.Equal(rankedEntries[j].lastSeen) {
			return rankedEntries[i].lastSeen.After(rankedEntries[j].lastSeen)
		}
		if rankedEntries[i].score != rankedEntries[j].score {
			return rankedEntries[i].score > rankedEntries[j].score
		}
		return rankedEntries[i].key < rankedEntries[j].key
	})

	keep := maxProcessResourcesPerNode
	if keep > len(rankedEntries) {
		keep = len(rankedEntries)
	}
	keepSet := make(map[string]struct{}, keep)
	for i := 0; i < keep; i++ {
		keepSet[rankedEntries[i].key] = struct{}{}
	}
	for key := range node.ProcessResources {
		if _, ok := keepSet[key]; !ok {
			delete(node.ProcessResources, key)
		}
	}
}

func buildProcessGraphSnapshot(node *NodeSnapshot) ProcessGraphSnapshot {
	if node == nil || len(node.ProcessResources) == 0 {
		return ProcessGraphSnapshot{}
	}
	type scored struct {
		process *ProcessResourceSample
		score   float64
	}
	rows := make([]scored, 0, len(node.ProcessResources))
	for _, process := range node.ProcessResources {
		if process == nil {
			continue
		}
		score := 0.0
		for _, total := range process.CategoryTotals {
			score += total
		}
		score += float64(process.LogErrors*2 + process.LogWarnings)
		rows = append(rows, scored{process: process, score: score})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].process.Key < rows[j].process.Key
		}
		return rows[i].score > rows[j].score
	})
	if len(rows) > 48 {
		rows = rows[:48]
	}

	nodes := make([]ProcessGraphNode, 0, len(rows)+1)
	edges := make([]ProcessGraphEdge, 0, len(rows))
	rootID := "node|" + firstNonEmpty(node.CollectorID, node.Hostname, "unknown")
	nodes = append(nodes, ProcessGraphNode{
		ID:       rootID,
		Name:     firstNonEmpty(node.Hostname, node.CollectorID),
		Category: "node",
		Score:    1,
	})

	for _, row := range rows {
		process := row.process
		if process == nil {
			continue
		}
		id := "process|" + process.Key
		category := "process"
		switch {
		case process.CategoryTotals["network"] > 0:
			category = "network"
		case process.CategoryTotals["disk_io"] > 0 || process.CategoryTotals["disk"] > 0:
			category = "io"
		case process.CategoryTotals["memory"] > 0:
			category = "memory"
		case process.CategoryTotals["cpu"] > 0:
			category = "cpu"
		}
		nodes = append(nodes, ProcessGraphNode{
			ID:       id,
			PID:      process.PID,
			Name:     firstNonEmpty(process.Name, process.Key),
			Category: category,
			Score:    row.score,
		})
		edges = append(edges, ProcessGraphEdge{
			Source: rootID,
			Target: id,
			Kind:   "observed_on",
		})
	}
	return ProcessGraphSnapshot{
		Nodes: nodes,
		Edges: edges,
	}
}

func logSeverity(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "error"),
		strings.Contains(lower, "err "),
		strings.Contains(lower, "fatal"),
		strings.Contains(lower, "panic"),
		strings.Contains(lower, "critical"):
		return "error"
	case strings.Contains(lower, "warn"),
		strings.Contains(lower, "deprecated"):
		return "warn"
	default:
		return ""
	}
}

func guessProcessFromLog(line string) (string, string) {
	token := strings.TrimSpace(line)
	if token == "" {
		return "", ""
	}

	if pid, name, ok := guessProcessFromJSONLog(token); ok {
		return pid, name
	}

	// Prefer explicit key-value process hints when they exist.
	type kvPattern struct {
		key      string
		trimUnit string
	}
	for _, candidate := range []kvPattern{
		{key: "process=", trimUnit: ""},
		{key: "service=", trimUnit: ".service"},
		{key: "comm=", trimUnit: ""},
		{key: "unit=", trimUnit: ".service"},
	} {
		if value := extractLogKVValue(token, candidate.key); value != "" {
			value = strings.TrimSpace(value)
			if candidate.trimUnit != "" {
				value = strings.TrimSuffix(value, candidate.trimUnit)
			}
			value = strings.Trim(value, "[](){}<>:;,. \t\r\n\"'")
			if value != "" {
				return "", value
			}
		}
	}

	if idx := strings.Index(token, ": "); idx > 0 {
		token = strings.TrimSpace(token[:idx])
	} else if parts := strings.SplitN(token, ":", 2); len(parts) > 0 {
		token = strings.TrimSpace(parts[0])
	}

	fields := strings.Fields(token)
	if len(fields) > 0 {
		token = fields[len(fields)-1]
	}
	if slash := strings.LastIndex(token, "/"); slash >= 0 && slash+1 < len(token) {
		token = token[slash+1:]
	}

	pid := ""
	if left := strings.Index(token, "["); left > 0 {
		if right := strings.Index(token[left:], "]"); right > 1 {
			rawPID := token[left+1 : left+right]
			if _, err := strconv.Atoi(rawPID); err == nil {
				pid = rawPID
			}
		}
		token = token[:left]
	}

	name := strings.Trim(token, "[](){}<>:;,. \t\r\n")
	switch strings.ToLower(name) {
	case "", "error", "warn", "warning", "info", "debug", "fatal", "panic", "critical":
		name = ""
	}
	return pid, name
}

func extractLogKVValue(line, key string) string {
	idx := strings.Index(strings.ToLower(line), key)
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	if start >= len(line) {
		return ""
	}

	rest := line[start:]
	end := len(rest)
	for i, ch := range rest {
		if ch == ' ' || ch == '\t' || ch == ',' || ch == ';' {
			end = i
			break
		}
	}
	return rest[:end]
}

func guessProcessFromJSONLog(line string) (string, string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return "", "", false
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return "", "", false
	}

	var pid string
	for _, key := range []string{"pid", "process_id", "tgid"} {
		if raw, ok := obj[key]; ok {
			pid = toScalarString(raw)
			if pid != "" {
				break
			}
		}
	}

	var name string
	for _, key := range []string{"process", "service", "comm", "unit", "app", "logger", "program", "cmd", "command"} {
		if raw, ok := obj[key]; ok {
			name = toScalarString(raw)
			if name != "" {
				break
			}
		}
	}

	if strings.HasSuffix(strings.ToLower(name), ".service") {
		name = strings.TrimSuffix(name, ".service")
	}
	name = strings.Trim(name, "[](){}<>:;,. \t\r\n\"'")
	if name == "" && pid == "" {
		return "", "", true
	}
	return pid, name, true
}

func toScalarString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v >= 0 {
			return strconv.FormatInt(int64(v), 10)
		}
		return ""
	case int64:
		return strconv.FormatInt(v, 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	case uint64:
		return strconv.FormatUint(v, 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	default:
		return ""
	}
}

func shouldAggregateMetric(name string) bool {
	switch name {
	case "node_network_receive_bytes_total",
		"node_network_transmit_bytes_total",
		"node_network_receive_packets_total",
		"node_network_transmit_packets_total",
		"node_network_receive_errs_total",
		"node_network_transmit_errs_total",
		"node_network_receive_drop_total",
		"node_network_transmit_drop_total",
		"node_network_receive_bytes_per_second",
		"node_network_transmit_bytes_per_second",
		"node_network_receive_packets_per_second",
		"node_network_transmit_packets_per_second",
		"node_network_receive_errs_per_second",
		"node_network_transmit_errs_per_second",
		"node_network_receive_drop_per_second",
		"node_network_transmit_drop_per_second",
		"node_disk_read_bytes_total",
		"node_disk_written_bytes_total",
		"node_disk_reads_completed_total",
		"node_disk_writes_completed_total",
		"node_disk_read_bytes_per_second",
		"node_disk_written_bytes_per_second",
		"node_disk_io_now",
		"node_schedstat_running_seconds_total",
		"node_schedstat_waiting_seconds_total",
		"node_schedstat_timeslices_total",
		"node_kmsg_messages_total":
		return true
	default:
		return false
	}
}

func shouldStoreTrendMetric(name string) bool {
	switch name {
	case "node_cpu_usage_percent",
		"node_cpu_iowait_percent",
		"node_load1",
		"node_load5",
		"node_load15",
		"node_memory_Used_bytes",
		"node_memory_Dirty_bytes",
		"node_memory_Writeback_bytes",
		"node_memory_MemTotal_bytes",
		"node_memory_MemAvailable_bytes",
		"node_memory_SwapTotal_bytes",
		"node_memory_SwapFree_bytes",
		"node_network_receive_bytes_per_second",
		"node_network_transmit_bytes_per_second",
		"node_network_total_receive_bytes_per_second",
		"node_network_total_transmit_bytes_per_second",
		"node_network_total_receive_packets_per_second",
		"node_network_total_transmit_packets_per_second",
		"node_network_total_errs_per_second",
		"node_network_total_drop_per_second",
		"node_network_utilization_peak_percent",
		"node_network_utilization_avg_percent",
		"node_network_capacity_utilization_percent",
		"node_network_interrupts_per_second",
		"node_tcp_retransmits_per_second",
		"node_tcp_retransmit_ratio",
		"node_softnet_dropped_per_second",
		"node_softnet_times_squeezed_per_second",
		"node_rdma_errors_per_second",
		"node_rdma_congestion_events_per_second",
		"node_disk_read_bytes_per_second",
		"node_disk_written_bytes_per_second",
		"node_disk_total_read_bytes_per_second",
		"node_disk_total_written_bytes_per_second",
		"node_disk_total_reads_per_second",
		"node_disk_total_writes_per_second",
		"node_disk_total_iops_per_second",
		"node_disk_queue_depth_total",
		"node_disk_queue_depth_avg",
		"node_disk_utilization_peak_percent",
		"node_disk_avg_read_latency_seconds",
		"node_disk_avg_write_latency_seconds",
		"node_disk_avg_request_latency_seconds",
		"node_disk_request_latency_p50_seconds",
		"node_disk_request_latency_p90_seconds",
		"node_disk_request_latency_p99_seconds",
		"node_nvme_total_read_bytes_per_second",
		"node_nvme_total_written_bytes_per_second",
		"node_nvme_total_iops_per_second",
		"node_nvme_queue_depth_total",
		"node_nvme_utilization_peak_percent",
		"node_nvme_avg_request_latency_seconds",
		"node_filesystem_total_used_percent",
		"node_filesystem_space_pressure_percent",
		"node_filesystem_inode_pressure_percent",
		"node_vmstat_pgpgin_per_second",
		"node_vmstat_pgpgout_per_second",
		"node_vmstat_nr_dirtied_per_second",
		"node_vmstat_nr_written_per_second",
		"node_numa_locality_ratio_percent",
		"node_numa_miss_ratio_percent",
		"node_numa_hit_ratio_percent",
		"node_vmstat_nr_dirty_pages",
		"node_vmstat_nr_writeback_pages",
		"node_pressure_io_some_avg10",
		"node_pressure_io_full_avg10",
		"node_procs_running",
		"node_procs_blocked",
		"node_filefd_allocated",
		"node_filefd_maximum",
		"node_gpu_utilization_sm_avg_percent",
		"node_gpu_memory_used_total_mib",
		"node_gpu_memory_total_all_mib",
		"collector_probe_core_client_available",
		"collector_probe_core_active",
		"collector_probe_core_collector_selection_valid",
		"collector_probe_core_decode_errors_total",
		"collector_probe_core_crc_failures_total",
		"collector_probe_core_restarts_total",
		"collector_probe_core_last_frame_age_seconds",
		"collector_probe_core_fresh",
		"node_security_world_writable_sensitive_paths",
		"node_security_sensitive_readable_files_count",
		"node_security_weak_permission_count",
		"node_security_suid_sgid_binaries_count",
		"node_security_ssh_weak_permissions_count",
		"node_security_ssh_insecure_config_count",
		"node_security_large_files_count",
		"node_security_large_file_growth_bytes",
		"node_security_listening_ports_count",
		"node_security_unexpected_listening_ports_count",
		"node_security_stale_listening_ports_count",
		"node_security_suspicious_outbound_destinations_count",
		"node_security_syn_backlog_pressure_ratio",
		"node_security_sysctl_risky_count",
		"node_security_firewall_disabled",
		"node_security_selinux_disabled",
		"node_security_apparmor_disabled",
		"node_security_privileged_unusual_path_process_count",
		"node_security_cron_anomalies_count",
		"node_security_systemd_unknown_units_count",
		"node_security_container_privileged_count",
		"node_security_container_capability_risk_count",
		"node_security_package_vulnerability_count":
		return true
	default:
		return false
	}
}

func selectTrendMetrics(metrics map[string]float64) map[string]float64 {
	if len(metrics) == 0 {
		return nil
	}
	out := make(map[string]float64)
	for name, value := range metrics {
		if shouldStoreTrendMetric(name) {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneMetricMap(src map[string]float64) map[string]float64 {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func labelsToMap(labels []*telemetryv1.Label) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for _, label := range labels {
		if label == nil {
			continue
		}
		key := strings.TrimSpace(label.Key)
		if key == "" {
			continue
		}
		out[key] = label.Value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneNode(node *NodeSnapshot) *NodeSnapshot {
	clone := *node
	if node.Labels != nil {
		labels := make(map[string]string, len(node.Labels))
		for k, v := range node.Labels {
			labels[k] = v
		}
		clone.Labels = labels
	}
	if node.Metrics != nil {
		metrics := make(map[string]float64, len(node.Metrics))
		for k, v := range node.Metrics {
			metrics[k] = v
		}
		clone.Metrics = metrics
	}
	if node.Processes != nil {
		clone.Processes = make([]*telemetryv1.ProcessSample, 0, len(node.Processes))
		for _, process := range node.Processes {
			clone.Processes = append(clone.Processes, cloneProcessSample(process))
		}
	}
	if node.Logs != nil {
		clone.Logs = make([]*telemetryv1.LogFingerprint, 0, len(node.Logs))
		for _, log := range node.Logs {
			clone.Logs = append(clone.Logs, cloneLogFingerprint(log))
		}
	}
	if node.ProcessNetwork != nil {
		clone.ProcessNetwork = make(map[string]*ProcessNetworkSample, len(node.ProcessNetwork))
		for k, v := range node.ProcessNetwork {
			if v == nil {
				continue
			}
			copy := *v
			clone.ProcessNetwork[k] = &copy
		}
	}
	if node.ProcessResources != nil {
		clone.ProcessResources = make(map[string]*ProcessResourceSample, len(node.ProcessResources))
		for k, v := range node.ProcessResources {
			if v == nil {
				continue
			}
			copy := *v
			copy.SignalValues = cloneFloatMap(v.SignalValues)
			copy.SignalTotals = cloneFloatMap(v.SignalTotals)
			copy.SignalFrequency = cloneUint64Map(v.SignalFrequency)
			copy.CategoryTotals = cloneFloatMap(v.CategoryTotals)
			copy.CategoryFrequency = cloneUint64Map(v.CategoryFrequency)
			clone.ProcessResources[k] = &copy
		}
	}
	if node.StorageDevices != nil {
		clone.StorageDevices = make(map[string]*StorageDeviceSample, len(node.StorageDevices))
		for k, v := range node.StorageDevices {
			if v == nil {
				continue
			}
			copy := *v
			clone.StorageDevices[k] = &copy
		}
	}
	if node.StoragePartitions != nil {
		clone.StoragePartitions = make(map[string]*StorageDeviceSample, len(node.StoragePartitions))
		for k, v := range node.StoragePartitions {
			if v == nil {
				continue
			}
			copy := *v
			clone.StoragePartitions[k] = &copy
		}
	}
	if node.Filesystems != nil {
		clone.Filesystems = make(map[string]*FilesystemSample, len(node.Filesystems))
		for k, v := range node.Filesystems {
			if v == nil {
				continue
			}
			copy := *v
			clone.Filesystems[k] = &copy
		}
	}
	if node.ProbeCoreModules != nil {
		clone.ProbeCoreModules = make(map[string]*ProbeCoreModuleSample, len(node.ProbeCoreModules))
		for k, v := range node.ProbeCoreModules {
			if v == nil {
				continue
			}
			copy := *v
			clone.ProbeCoreModules[k] = &copy
		}
	}
	if node.RuntimeSecurityEvents != nil {
		clone.RuntimeSecurityEvents = make([]RuntimeSecurityEvent, 0, len(node.RuntimeSecurityEvents))
		for _, event := range node.RuntimeSecurityEvents {
			copyEvent := event
			if event.Metadata != nil {
				copyEvent.Metadata = make(map[string]string, len(event.Metadata))
				for k, v := range event.Metadata {
					copyEvent.Metadata[k] = v
				}
			}
			clone.RuntimeSecurityEvents = append(clone.RuntimeSecurityEvents, copyEvent)
		}
	}
	if node.SyscallStatistics != nil {
		clone.SyscallStatistics = cloneUint64Map(node.SyscallStatistics)
	}
	clone.NetworkBehavior = node.NetworkBehavior
	clone.ProcessGraphSnapshot = ProcessGraphSnapshot{
		Nodes: append([]ProcessGraphNode(nil), node.ProcessGraphSnapshot.Nodes...),
		Edges: append([]ProcessGraphEdge(nil), node.ProcessGraphSnapshot.Edges...),
	}
	return &clone
}

func cloneProcessSample(process *telemetryv1.ProcessSample) *telemetryv1.ProcessSample {
	if process == nil {
		return nil
	}
	return &telemetryv1.ProcessSample{
		Pid:        process.Pid,
		Name:       process.Name,
		CpuPercent: process.CpuPercent,
		RssBytes:   process.RssBytes,
		IoReadBps:  process.IoReadBps,
		IoWriteBps: process.IoWriteBps,
	}
}

func cloneLogFingerprint(log *telemetryv1.LogFingerprint) *telemetryv1.LogFingerprint {
	if log == nil {
		return nil
	}
	return &telemetryv1.LogFingerprint{
		Fingerprint:       log.Fingerprint,
		Count:             log.Count,
		Example:           log.Example,
		TimestampUnixNano: log.TimestampUnixNano,
	}
}

func cloneFloatMap(src map[string]float64) map[string]float64 {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]float64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func cloneUint64Map(src map[string]uint64) map[string]uint64 {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
