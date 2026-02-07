package ingest

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

	SignalValues    map[string]float64 `json:"signal_values,omitempty"`    // latest values
	SignalTotals    map[string]float64 `json:"signal_totals,omitempty"`    // cumulative totals (delta for counters)
	SignalFrequency map[string]uint64  `json:"signal_frequency,omitempty"` // observation count

	CategoryTotals    map[string]float64 `json:"category_totals,omitempty"`    // cumulative totals by resource category
	CategoryFrequency map[string]uint64  `json:"category_frequency,omitempty"` // observation count by category

	LogErrors   uint64 `json:"log_errors,omitempty"`
	LogWarnings uint64 `json:"log_warnings,omitempty"`
}

const maxProcessResourcesPerNode = 4096

// MemoryStore is an in-memory implementation of Store.
type MemoryStore struct {
	mu    sync.RWMutex
	nodes map[string]*NodeSnapshot
}

// NewMemoryStore returns a new MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nodes: make(map[string]*NodeSnapshot)}
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
	for _, metric := range metrics {
		if metric == nil || metric.Name == "" {
			continue
		}

		if shouldAggregateMetric(metric.Name) {
			node.Metrics[metric.Name] += metric.Value
		} else {
			node.Metrics[metric.Name] = metric.Value
		}

		pid, name := metricProcessIdentity(metric)
		if (pid != "" || name != "") && metric.Name != "" {
			if process := ensureProcessResource(node, pid, name); process != nil {
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
		severity := logSeverity(lf.Example)
		if severity == "" {
			continue
		}
		pid, name := guessProcessFromLog(lf.Example)
		process := ensureProcessResource(node, pid, name)
		if process == nil {
			continue
		}
		switch severity {
		case "error":
			process.LogErrors += lf.Count
			recordProcessSignal(process, "log_errors", float64(lf.Count), []string{"logs"}, lf.Count, receivedAt)
		case "warn":
			process.LogWarnings += lf.Count
			recordProcessSignal(process, "log_warnings", float64(lf.Count), []string{"logs"}, lf.Count, receivedAt)
		}
	}

	pruneProcessResources(node)
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
		"node_disk_read_bytes_total",
		"node_disk_written_bytes_total",
		"node_disk_reads_completed_total",
		"node_disk_writes_completed_total",
		"node_disk_read_bytes_per_second",
		"node_disk_written_bytes_per_second",
		"node_disk_io_now",
		"node_kmsg_messages_total":
		return true
	default:
		return false
	}
}

func labelsToMap(labels []*telemetryv1.Label) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for _, label := range labels {
		out[label.Key] = label.Value
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
		clone.Processes = append([]*telemetryv1.ProcessSample{}, node.Processes...)
	}
	if node.Logs != nil {
		clone.Logs = append([]*telemetryv1.LogFingerprint{}, node.Logs...)
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
	return &clone
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
