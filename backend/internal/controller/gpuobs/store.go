package gpuobs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

// Store aggregates GPU metrics across the fleet and persists snapshots/history for later scheduling decisions.
//
// Performance notes (industry-practical):
// - Avoid per-metric label map allocations by scanning label slices into fixed fields.
// - Avoid O(N log N) sorts on every per-process metric update by keeping a PID->state map and only sorting when needed.
// - Avoid per-batch file opens by buffering history records and flushing in batches on a timer.
// - Persist only dirty nodes on flush to reduce disk churn.
type Store struct {
	cfg Config

	mu sync.RWMutex

	nodes      map[string]*nodeState      // key: collector_id
	dirtyNodes map[string]struct{}        // nodes changed since last flush
	historyBuf map[string][]historyRecord // key: history file path

	stopCh chan struct{}
	wg     sync.WaitGroup
}

type nodeState struct {
	collectorID string
	hostname    string
	labels      map[string]string
	lastSeen    time.Time

	gpuCount int
	gpus     map[string]*deviceState // key: gpu_index
}

type deviceState struct {
	gpuIndex string
	lastSeen time.Time

	// Inventory
	uuid          string
	name          string
	driverVersion string
	cudaVersion   string
	labels        map[string]string

	// Scalar metrics used for scheduling / balancing.
	utilSMPercent     float64
	utilMemPercent    float64
	memTotalMiB       float64
	memUsedMiB        float64
	memFreeMiB        float64
	tempC             float64
	tempMemC          float64
	powerDrawW        float64
	powerLimitW       float64
	pcieRxMBs         float64
	pcieTxMBs         float64
	throttleActive    float64
	throttleThermal   float64
	throttlePower     float64
	processCount      float64
	contextCount      float64
	xidErrorsTotal    float64
	migEnabled        float64
	migPending        float64
	persistenceModeOn float64

	// Per-process aggregation (bounded at query/persist time).
	processes   map[string]*processState // key: pid
	topDirty    bool
	topCached   []Process
	maxTopLimit int
}

type processState struct {
	pid        string
	name       string
	memMiB     float64
	utilSMPct  float64
	utilMemPct float64
}

type historyRecord struct {
	TimeUnixNano int64   `json:"t"`
	Host         string  `json:"host,omitempty"`
	GPU          string  `json:"gpu"`
	UUID         string  `json:"uuid,omitempty"`
	Util         float64 `json:"util_sm,omitempty"`
	MemUsedMiB   float64 `json:"mem_used_mib,omitempty"`
	MemTotalMiB  float64 `json:"mem_total_mib,omitempty"`
	TempC        float64 `json:"temp_c,omitempty"`
	PowerW       float64 `json:"power_w,omitempty"`
}

// DeviceLite is the minimal set of fields used for fast Prometheus export.
type DeviceLite struct {
	GPUIndex    string
	UUID        string
	Name        string
	UtilSMPct   float64
	MemUsedMiB  float64
	MemTotalMiB float64
}

func New(cfg Config) *Store {
	def := DefaultConfig()
	if cfg.PersistDir == "" {
		cfg.PersistDir = def.PersistDir
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = def.FlushInterval
	}
	if cfg.Retention <= 0 {
		cfg.Retention = def.Retention
	}
	if cfg.MaxProcessesPerGPU <= 0 {
		cfg.MaxProcessesPerGPU = def.MaxProcessesPerGPU
	}

	return &Store{
		cfg:        cfg,
		nodes:      make(map[string]*nodeState),
		dirtyNodes: make(map[string]struct{}),
		historyBuf: make(map[string][]historyRecord),
		stopCh:     make(chan struct{}),
	}
}

func (s *Store) Start() error {
	if err := os.MkdirAll(s.cfg.PersistDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.cfg.PersistDir, "snapshots"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.cfg.PersistDir, "history"), 0o755); err != nil {
		return err
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.cfg.FlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = s.flushDirty()
				_ = s.flushHistory()
				_ = s.cleanupHistory()
			case <-s.stopCh:
				_ = s.flushDirty()
				_ = s.flushHistory()
				return
			}
		}
	}()

	return nil
}

func (s *Store) Stop() {
	close(s.stopCh)
	s.wg.Wait()
}

// Snapshot returns copies for API responses.
func (s *Store) Snapshot() []*Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, s.snapshotNodeLocked(n))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hostname < out[j].Hostname })
	return out
}

func (s *Store) Node(collectorID string) *Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.nodes[collectorID]
	if n == nil {
		return nil
	}
	return s.snapshotNodeLocked(n)
}

// ForEachDeviceLite iterates current devices without allocating full snapshots.
func (s *Store) ForEachDeviceLite(fn func(hostname, collectorID string, d DeviceLite)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, n := range s.nodes {
		for _, d := range n.gpus {
			fn(n.hostname, n.collectorID, DeviceLite{
				GPUIndex:    d.gpuIndex,
				UUID:        d.uuid,
				Name:        d.name,
				UtilSMPct:   d.utilSMPercent,
				MemUsedMiB:  d.memUsedMiB,
				MemTotalMiB: d.memTotalMiB,
			})
		}
	}
}

// ProcessBatch ingests a telemetry batch and extracts GPU info/metrics.
func (s *Store) ProcessBatch(collectorID string, batch *telemetryv1.TelemetryBatch, receivedAt time.Time) {
	if batch == nil {
		return
	}
	if collectorID == "" || collectorID == "unknown" {
		if batch.Collector != nil {
			collectorID = batch.Collector.CollectorId
		}
	}
	if collectorID == "" {
		return
	}

	s.mu.Lock()
	node := s.ensureNodeLocked(collectorID)
	if batch.Collector != nil {
		node.collectorID = batch.Collector.CollectorId
		node.hostname = batch.Collector.Hostname
		node.labels = labelsToMap(batch.Collector.Labels)
	}
	node.lastSeen = receivedAt

	for _, m := range batch.Metrics {
		if m == nil {
			continue
		}
		if !strings.HasPrefix(m.Name, "node_gpu_") {
			continue
		}
		s.applyMetricLocked(node, m)
	}

	// Mark dirty for persistence.
	s.dirtyNodes[collectorID] = struct{}{}

	// Buffer a compact per-GPU history record (batched file IO on flush).
	if len(node.gpus) > 0 {
		day := receivedAt.UTC().Format("2006-01-02")
		historyPath := filepath.Join(s.cfg.PersistDir, "history", fmt.Sprintf("%s-%s.jsonl", sanitizeFilename(collectorID), day))
		for _, dev := range node.gpus {
			s.historyBuf[historyPath] = append(s.historyBuf[historyPath], historyRecord{
				TimeUnixNano: receivedAt.UnixNano(),
				Host:         node.hostname,
				GPU:          dev.gpuIndex,
				UUID:         dev.uuid,
				Util:         dev.utilSMPercent,
				MemUsedMiB:   dev.memUsedMiB,
				MemTotalMiB:  dev.memTotalMiB,
				TempC:        dev.tempC,
				PowerW:       dev.powerDrawW,
			})
		}
	}

	s.mu.Unlock()
}

func (s *Store) ensureNodeLocked(collectorID string) *nodeState {
	n := s.nodes[collectorID]
	if n == nil {
		n = &nodeState{
			collectorID: collectorID,
			gpus:        make(map[string]*deviceState),
		}
		s.nodes[collectorID] = n
	}
	if n.gpus == nil {
		n.gpus = make(map[string]*deviceState)
	}
	return n
}

func (s *Store) ensureDeviceLocked(node *nodeState, gpuIndex string) *deviceState {
	if gpuIndex == "" {
		gpuIndex = "unknown"
	}
	d := node.gpus[gpuIndex]
	if d == nil {
		d = &deviceState{
			gpuIndex:    gpuIndex,
			processes:   make(map[string]*processState),
			topDirty:    true,
			maxTopLimit: s.cfg.MaxProcessesPerGPU,
		}
		node.gpus[gpuIndex] = d
	}
	d.lastSeen = node.lastSeen
	return d
}

func (s *Store) applyMetricLocked(node *nodeState, m *telemetryv1.Metric) {
	switch m.Name {
	case "node_gpu_count":
		node.gpuCount = int(m.Value)
		return
	}

	lv := extractLabelVals(m.Labels)

	if m.Name == "node_gpu_info" {
		d := s.ensureDeviceLocked(node, lv.gpuID)
		d.uuid = lv.uuid
		d.name = lv.name
		d.driverVersion = lv.driverVersion
		d.cudaVersion = lv.cudaVersion

		// Keep remaining labels (best-effort) without allocating in the hot path:
		// Only capture non-empty extra labels if present.
		// This is mainly for joins/diagnostics; scheduling should use scalar fields.
		if len(m.Labels) > 0 {
			var extra map[string]string
			for _, l := range m.Labels {
				if l == nil {
					continue
				}
				switch l.Key {
				case "gpu_id", "uuid", "name", "driver_version", "cuda_version":
					continue
				default:
					if l.Value != "" {
						if extra == nil {
							extra = make(map[string]string, 4)
						}
						extra[l.Key] = l.Value
					}
				}
			}
			if len(extra) > 0 {
				d.labels = extra
			}
		}
		return
	}

	d := s.ensureDeviceLocked(node, lv.gpuID)

	switch m.Name {
	case "node_gpu_persistence_mode":
		d.persistenceModeOn = m.Value
	case "node_gpu_utilization_sm_percent":
		d.utilSMPercent = m.Value
	case "node_gpu_utilization_memory_percent":
		d.utilMemPercent = m.Value
	case "node_gpu_memory_total_mib":
		d.memTotalMiB = m.Value
	case "node_gpu_memory_used_mib":
		d.memUsedMiB = m.Value
	case "node_gpu_memory_free_mib":
		d.memFreeMiB = m.Value
	case "node_gpu_temperature_celsius":
		d.tempC = m.Value
	case "node_gpu_temperature_memory_celsius":
		d.tempMemC = m.Value
	case "node_gpu_power_draw_watts":
		d.powerDrawW = m.Value
	case "node_gpu_power_limit_watts":
		d.powerLimitW = m.Value
	case "node_gpu_pcie_rx_mb_s":
		d.pcieRxMBs = m.Value
	case "node_gpu_pcie_tx_mb_s":
		d.pcieTxMBs = m.Value
	case "node_gpu_throttle_active":
		d.throttleActive = m.Value
	case "node_gpu_throttle_thermal_active":
		d.throttleThermal = m.Value
	case "node_gpu_throttle_power_active":
		d.throttlePower = m.Value
	case "node_gpu_process_count":
		d.processCount = m.Value
	case "node_gpu_context_count":
		d.contextCount = m.Value
	case "node_gpu_xid_errors_total":
		d.xidErrorsTotal = m.Value
	case "node_gpu_mig_enabled":
		d.migEnabled = m.Value
	case "node_gpu_mig_pending":
		d.migPending = m.Value
	case "node_gpu_process_memory_mib", "node_gpu_process_sm_util_percent", "node_gpu_process_mem_util_percent":
		s.applyProcessMetricLocked(d, m.Name, lv, m.Value)
	}
}

func (s *Store) applyProcessMetricLocked(d *deviceState, metricName string, lv labelVals, value float64) {
	if lv.pid == "" {
		return
	}
	p := d.processes[lv.pid]
	if p == nil {
		p = &processState{pid: lv.pid, name: lv.process}
		d.processes[lv.pid] = p
		d.topDirty = true
	} else if lv.process != "" && p.name == "" {
		p.name = lv.process
	}

	switch metricName {
	case "node_gpu_process_memory_mib":
		if p.memMiB != value {
			p.memMiB = value
			d.topDirty = true // changes ordering
		}
	case "node_gpu_process_sm_util_percent":
		p.utilSMPct = value
	case "node_gpu_process_mem_util_percent":
		p.utilMemPct = value
	}
}

func (s *Store) snapshotNodeLocked(n *nodeState) *Node {
	out := &Node{
		CollectorID: n.collectorID,
		Hostname:    n.hostname,
		Labels:      cloneStringMap(n.labels),
		LastSeen:    n.lastSeen,
		GPUCount:    n.gpuCount,
		GPUs:        make(map[string]Device, len(n.gpus)),
	}

	for gpuIndex, d := range n.gpus {
		out.GPUs[gpuIndex] = Device{
			GPUIndex:      d.gpuIndex,
			UUID:          d.uuid,
			Name:          d.name,
			DriverVersion: d.driverVersion,
			CUDAVersion:   d.cudaVersion,
			Labels:        cloneStringMap(d.labels),
			LastSeen:      d.lastSeen,

			UtilSMPercent:     d.utilSMPercent,
			UtilMemPercent:    d.utilMemPercent,
			MemTotalMiB:       d.memTotalMiB,
			MemUsedMiB:        d.memUsedMiB,
			MemFreeMiB:        d.memFreeMiB,
			TempC:             d.tempC,
			TempMemC:          d.tempMemC,
			PowerDrawW:        d.powerDrawW,
			PowerLimitW:       d.powerLimitW,
			PCIERxMBs:         d.pcieRxMBs,
			PCIETxMBs:         d.pcieTxMBs,
			ThrottleActive:    d.throttleActive,
			ThrottleThermal:   d.throttleThermal,
			ThrottlePower:     d.throttlePower,
			ProcessCount:      d.processCount,
			ContextCount:      d.contextCount,
			XidErrorsTotal:    d.xidErrorsTotal,
			MigEnabled:        d.migEnabled,
			MigPending:        d.migPending,
			PersistenceModeOn: d.persistenceModeOn,

			Processes: s.topProcessesLocked(d),
		}
	}
	return out
}

func (s *Store) topProcessesLocked(d *deviceState) []Process {
	limit := d.maxTopLimit
	if limit <= 0 {
		limit = 20
	}

	// Sorting a bounded list is cheap, but only do it when the ordering may have changed.
	if !d.topDirty && len(d.topCached) > 0 {
		return append([]Process{}, d.topCached...)
	}

	if len(d.processes) == 0 {
		d.topCached = nil
		d.topDirty = false
		return nil
	}

	out := make([]Process, 0, len(d.processes))
	for _, p := range d.processes {
		out = append(out, Process{
			PID:        p.pid,
			Name:       p.name,
			MemMiB:     p.memMiB,
			UtilSMPct:  p.utilSMPct,
			UtilMemPct: p.utilMemPct,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MemMiB > out[j].MemMiB })
	if len(out) > limit {
		out = out[:limit]
	}
	d.topCached = out
	d.topDirty = false
	return append([]Process{}, out...)
}

func (s *Store) flushDirty() error {
	// Grab dirty ids quickly.
	s.mu.Lock()
	if len(s.dirtyNodes) == 0 {
		s.mu.Unlock()
		return nil
	}
	ids := make([]string, 0, len(s.dirtyNodes))
	for id := range s.dirtyNodes {
		ids = append(ids, id)
	}
	s.dirtyNodes = make(map[string]struct{})
	s.mu.Unlock()

	for _, id := range ids {
		s.mu.RLock()
		n := s.nodes[id]
		var snap *Node
		if n != nil {
			snap = s.snapshotNodeLocked(n)
		}
		s.mu.RUnlock()
		if snap == nil {
			continue
		}
		if err := s.writeSnapshot(snap); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) writeSnapshot(node *Node) error {
	if node == nil {
		return nil
	}
	path := filepath.Join(s.cfg.PersistDir, "snapshots", fmt.Sprintf("%s.json", sanitizeFilename(node.CollectorID)))
	tmp := path + ".tmp"

	// Compact JSON is significantly cheaper to write/parse than indented JSON at scale.
	b, err := json.Marshal(node)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) flushHistory() error {
	s.mu.Lock()
	if len(s.historyBuf) == 0 {
		s.mu.Unlock()
		return nil
	}
	buf := s.historyBuf
	s.historyBuf = make(map[string][]historyRecord)
	s.mu.Unlock()

	for path, records := range buf {
		if len(records) == 0 {
			continue
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(f)
		for _, r := range records {
			line, err := json.Marshal(r)
			if err != nil {
				_ = f.Close()
				return err
			}
			if _, err := w.Write(line); err != nil {
				_ = f.Close()
				return err
			}
			if err := w.WriteByte('\n'); err != nil {
				_ = f.Close()
				return err
			}
		}
		if err := w.Flush(); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) cleanupHistory() error {
	if s.cfg.Retention <= 0 {
		return nil
	}
	dir := filepath.Join(s.cfg.PersistDir, "history")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-s.cfg.Retention)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

type labelVals struct {
	gpuID         string
	uuid          string
	name          string
	driverVersion string
	cudaVersion   string
	pid           string
	process       string
}

func extractLabelVals(labels []*telemetryv1.Label) labelVals {
	var lv labelVals
	for _, l := range labels {
		if l == nil {
			continue
		}
		switch l.Key {
		case "gpu_id":
			lv.gpuID = l.Value
		case "uuid":
			lv.uuid = l.Value
		case "name":
			lv.name = l.Value
		case "driver_version":
			lv.driverVersion = l.Value
		case "cuda_version":
			lv.cudaVersion = l.Value
		case "pid":
			lv.pid = l.Value
		case "process":
			lv.process = l.Value
		}
	}
	return lv
}

func labelsToMap(labels []*telemetryv1.Label) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for _, l := range labels {
		if l == nil {
			continue
		}
		out[l.Key] = l.Value
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}
