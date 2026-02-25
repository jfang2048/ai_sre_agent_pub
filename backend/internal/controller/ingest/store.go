package ingest

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/collections/ring"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
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
	maxProcessResourcesPerNode     = 4096
	maxMetricHistorySamplesPerNode = 1440
)

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu       sync.RWMutex
	nodes    map[string]*NodeSnapshot
	history  map[string]*ring.Ring[MetricHistorySample]
	logIndex *logindex.Index
}

// NewMemoryStore returns a new MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nodes:   make(map[string]*NodeSnapshot),
		history: make(map[string]*ring.Ring[MetricHistorySample]),
	}
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
	for _, metric := range metrics {
		if metric == nil || metric.Name == "" {
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
	pruneProcessResources(node)
	s.recordMetricHistory(collectorID, receivedAt, node.Metrics)
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
		h = ring.New[MetricHistorySample](maxMetricHistorySamplesPerNode)
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
		"collector_probe_core_fresh":
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
