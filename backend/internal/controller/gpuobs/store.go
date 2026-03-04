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

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/collections/ring"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
)

// Store aggregates GPU metrics across the fleet and persists snapshots/history for later scheduling decisions.
//
// Performance notes:
// - Avoid per-metric label map allocations by scanning label slices into fixed fields.
// - Avoid O(N log N) sorts on every per-process metric update by keeping a PID->state map and only sorting when needed.
// - Avoid per-batch file opens by buffering history/event records and flushing in batches on a timer.
// - Persist only dirty nodes on flush to reduce disk churn.
type Store struct {
	cfg Config

	mu sync.RWMutex

	nodes      map[string]*nodeState      // key: collector_id
	dirtyNodes map[string]struct{}        // nodes changed since last flush
	historyBuf map[string][]historyRecord // key: history file path
	eventBuf   map[string][]Event         // key: events file path

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
	events   *ring.Ring[Event]
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
	utilSMPercent       float64
	utilMemPercent      float64
	utilEncPercent      float64
	utilDecPercent      float64
	utilJpegPercent     float64
	utilOFAPercent      float64
	powerState          float64
	memTotalMiB         float64
	memUsedMiB          float64
	memFreeMiB          float64
	memReservedMiB      float64
	bar1MemTotalMiB     float64
	bar1MemUsedMiB      float64
	bar1MemFreeMiB      float64
	tempC               float64
	tempMemC            float64
	powerDrawW          float64
	powerLimitW         float64
	pcieGen             float64
	pcieWidth           float64
	pcieGenMax          float64
	pcieWidthMax        float64
	pcieRxMBs           float64
	pcieTxMBs           float64
	pcieTheoreticalMBs  float64
	pcieBandwidthMaxMBs float64
	pcieRxUtilPercent   float64
	pcieTxUtilPercent   float64
	pcieLinkUtilPercent float64

	throttleActive                  float64
	throttleThermal                 float64
	throttlePower                   float64
	processCount                    float64
	contextCount                    float64
	xidErrorsTotal                  float64
	uvmFaultsTotal                  float64
	resetEventsTotal                float64
	reliabilityEventsTotal          float64
	eccSingleBitErrorsTotal         float64
	eccDoubleBitErrorsTotal         float64
	eccVolatileSingleBitErrorsTotal float64
	eccVolatileDoubleBitErrorsTotal float64
	retiredPagesSingleBitTotal      float64
	retiredPagesDoubleBitTotal      float64
	retiredPagesPending             float64
	remappedRowsCorrectableTotal    float64
	remappedRowsUncorrectableTotal  float64
	remappedRowsPending             float64
	resetRequired                   float64
	resetRecommended                float64
	migEnabled                      float64
	migPending                      float64
	persistenceModeOn               float64
	nvlinkLinks                     float64
	kernelHotspotPeakSMUtilPercent  float64
	kernelActiveContexts            float64

	// Per-process aggregation (bounded at query/persist time).
	processes   map[string]*processState // key: pid
	topDirty    bool
	topCached   []Process
	maxTopLimit int

	// Timelines / events
	timeline      *ring.Ring[deviceTimelineSample]
	eventCounters map[string]float64
}

type processState struct {
	pid           string
	name          string
	memMiB        float64
	utilSMPct     float64
	utilMemPct    float64
	utilEncPct    float64
	utilDecPct    float64
	contextActive float64
	contextType   string
	lastSeen      time.Time
	timeline      *ring.Ring[processTimelineSample]
}

type deviceTimelineSample struct {
	timestamp                      time.Time
	utilSMPercent                  float64
	utilMemPercent                 float64
	utilEncPercent                 float64
	utilDecPercent                 float64
	memUsedMiB                     float64
	memTotalMiB                    float64
	powerDrawW                     float64
	tempC                          float64
	pcieRxMBs                      float64
	pcieTxMBs                      float64
	pcieLinkUtilPercent            float64
	throttleActive                 float64
	xidErrorsTotal                 float64
	uvmFaultsTotal                 float64
	resetEventsTotal               float64
	reliabilityEventsTotal         float64
	kernelHotspotPeakSMUtilPercent float64
}

type processTimelineSample struct {
	timestamp     time.Time
	memMiB        float64
	utilSMPct     float64
	utilMemPct    float64
	utilEncPct    float64
	utilDecPct    float64
	contextActive float64
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
	PCIERxMBs    float64 `json:"pcie_rx_mb_s,omitempty"`
	PCIETxMBs    float64 `json:"pcie_tx_mb_s,omitempty"`
	PCIELinkUtil float64 `json:"pcie_link_util_percent,omitempty"`
	Throttle     float64 `json:"throttle_active,omitempty"`
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
	if cfg.TimelineSamplesPerGPU <= 0 {
		cfg.TimelineSamplesPerGPU = def.TimelineSamplesPerGPU
	}
	if cfg.TimelineSamplesPerProcess <= 0 {
		cfg.TimelineSamplesPerProcess = def.TimelineSamplesPerProcess
	}
	if cfg.EventBufferPerNode <= 0 {
		cfg.EventBufferPerNode = def.EventBufferPerNode
	}
	if cfg.RecentEventsInSnapshot <= 0 {
		cfg.RecentEventsInSnapshot = def.RecentEventsInSnapshot
	}

	return &Store{
		cfg:        cfg,
		nodes:      make(map[string]*nodeState),
		dirtyNodes: make(map[string]struct{}),
		historyBuf: make(map[string][]historyRecord),
		eventBuf:   make(map[string][]Event),
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
	if err := os.MkdirAll(filepath.Join(s.cfg.PersistDir, "events"), 0o755); err != nil {
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
				_ = s.flushEvents()
				_ = s.cleanupHistory()
			case <-s.stopCh:
				_ = s.flushDirty()
				_ = s.flushHistory()
				_ = s.flushEvents()
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

// Events returns filtered recent events for one collector/GPU.
func (s *Store) Events(collectorID, gpuID string, since time.Time, limit int, severity string) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.nodes[collectorID]
	if n == nil || n.events == nil {
		return nil
	}
	all := n.events.SliceOldest()
	if len(all) == 0 {
		return nil
	}
	out := make([]Event, 0, len(all))
	for _, evt := range all {
		if !since.IsZero() && evt.Timestamp.Before(since) {
			continue
		}
		if gpuID != "" && evt.GPUIndex != gpuID {
			continue
		}
		if severity != "" && !strings.EqualFold(evt.Severity, severity) {
			continue
		}
		out = append(out, evt)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// DeviceMetricTimeline returns timeline points for one GPU metric.
func (s *Store) DeviceMetricTimeline(collectorID, gpuID, metric string, since time.Time, limit int) []MetricPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.nodes[collectorID]
	if n == nil {
		return nil
	}
	d := n.gpus[gpuID]
	if d == nil || d.timeline == nil {
		return nil
	}
	samples := d.timeline.SliceOldest()
	out := make([]MetricPoint, 0, len(samples))
	for _, sample := range samples {
		if !since.IsZero() && sample.timestamp.Before(since) {
			continue
		}
		value, ok := deviceTimelineMetricValue(sample, metric)
		if !ok {
			continue
		}
		out = append(out, MetricPoint{Timestamp: sample.timestamp, Value: value})
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// ProcessMetricTimeline returns timeline points for one process metric on one GPU.
func (s *Store) ProcessMetricTimeline(collectorID, gpuID, pid, metric string, since time.Time, limit int) []MetricPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.nodes[collectorID]
	if n == nil {
		return nil
	}
	d := n.gpus[gpuID]
	if d == nil {
		return nil
	}
	p := d.processes[pid]
	if p == nil || p.timeline == nil {
		return nil
	}
	samples := p.timeline.SliceOldest()
	out := make([]MetricPoint, 0, len(samples))
	for _, sample := range samples {
		if !since.IsZero() && sample.timestamp.Before(since) {
			continue
		}
		value, ok := processTimelineMetricValue(sample, metric)
		if !ok {
			continue
		}
		out = append(out, MetricPoint{Timestamp: sample.timestamp, Value: value})
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// RankedProcesses returns sorted process rows for a GPU.
func (s *Store) RankedProcesses(collectorID, gpuID, sortBy string, limit int) []Process {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.nodes[collectorID]
	if n == nil {
		return nil
	}
	d := n.gpus[gpuID]
	if d == nil {
		return nil
	}
	rows := s.topProcessesLocked(d)
	if len(rows) == 0 {
		return nil
	}
	sortBy = strings.TrimSpace(strings.ToLower(sortBy))
	sort.Slice(rows, func(i, j int) bool {
		left := rows[i]
		right := rows[j]
		switch sortBy {
		case "sm", "sm_util", "util_sm", "gpu_util":
			if left.UtilSMPct != right.UtilSMPct {
				return left.UtilSMPct > right.UtilSMPct
			}
		case "mem_util", "gpu_mem_util":
			if left.UtilMemPct != right.UtilMemPct {
				return left.UtilMemPct > right.UtilMemPct
			}
		case "enc", "encoder", "encoder_util":
			if left.UtilEncPct != right.UtilEncPct {
				return left.UtilEncPct > right.UtilEncPct
			}
		case "dec", "decoder", "decoder_util":
			if left.UtilDecPct != right.UtilDecPct {
				return left.UtilDecPct > right.UtilDecPct
			}
		case "context", "context_active":
			if left.ContextActive != right.ContextActive {
				return left.ContextActive > right.ContextActive
			}
		default:
			if left.MemMiB != right.MemMiB {
				return left.MemMiB > right.MemMiB
			}
		}
		if left.UtilSMPct != right.UtilSMPct {
			return left.UtilSMPct > right.UtilSMPct
		}
		if left.MemMiB != right.MemMiB {
			return left.MemMiB > right.MemMiB
		}
		return left.PID < right.PID
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
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
		s.applyMetricLocked(node, m, receivedAt)
	}

	s.appendTimelinesLocked(node, receivedAt)
	s.pruneStaleProcessesLocked(node, receivedAt)

	// Mark dirty for persistence.
	s.dirtyNodes[collectorID] = struct{}{}

	// Buffer compact per-GPU history records (batched file IO on flush).
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
				PCIERxMBs:    dev.pcieRxMBs,
				PCIETxMBs:    dev.pcieTxMBs,
				PCIELinkUtil: dev.pcieLinkUtilPercent,
				Throttle:     dev.throttleActive,
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
			events:      ring.New[Event](s.cfg.EventBufferPerNode),
		}
		s.nodes[collectorID] = n
	}
	if n.gpus == nil {
		n.gpus = make(map[string]*deviceState)
	}
	if n.events == nil {
		n.events = ring.New[Event](s.cfg.EventBufferPerNode)
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
			gpuIndex:      gpuIndex,
			processes:     make(map[string]*processState),
			topDirty:      true,
			maxTopLimit:   s.cfg.MaxProcessesPerGPU,
			timeline:      ring.New[deviceTimelineSample](s.cfg.TimelineSamplesPerGPU),
			eventCounters: make(map[string]float64),
		}
		node.gpus[gpuIndex] = d
	}
	d.lastSeen = node.lastSeen
	if d.timeline == nil {
		d.timeline = ring.New[deviceTimelineSample](s.cfg.TimelineSamplesPerGPU)
	}
	if d.eventCounters == nil {
		d.eventCounters = make(map[string]float64)
	}
	return d
}

func (s *Store) applyMetricLocked(node *nodeState, m *telemetryv1.Metric, ts time.Time) {
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
	case "node_gpu_utilization_encoder_percent":
		d.utilEncPercent = m.Value
	case "node_gpu_utilization_decoder_percent":
		d.utilDecPercent = m.Value
	case "node_gpu_utilization_jpeg_percent":
		d.utilJpegPercent = m.Value
	case "node_gpu_utilization_ofa_percent":
		d.utilOFAPercent = m.Value
	case "node_gpu_power_state":
		d.powerState = m.Value
	case "node_gpu_memory_total_mib":
		d.memTotalMiB = m.Value
	case "node_gpu_memory_used_mib":
		d.memUsedMiB = m.Value
	case "node_gpu_memory_free_mib":
		d.memFreeMiB = m.Value
	case "node_gpu_memory_reserved_mib":
		d.memReservedMiB = m.Value
	case "node_gpu_bar1_memory_total_mib":
		d.bar1MemTotalMiB = m.Value
	case "node_gpu_bar1_memory_used_mib":
		d.bar1MemUsedMiB = m.Value
	case "node_gpu_bar1_memory_free_mib":
		d.bar1MemFreeMiB = m.Value
	case "node_gpu_temperature_celsius":
		d.tempC = m.Value
	case "node_gpu_temperature_memory_celsius":
		d.tempMemC = m.Value
	case "node_gpu_power_draw_watts":
		d.powerDrawW = m.Value
	case "node_gpu_power_limit_watts":
		d.powerLimitW = m.Value
	case "node_gpu_pcie_gen":
		d.pcieGen = m.Value
	case "node_gpu_pcie_width":
		d.pcieWidth = m.Value
	case "node_gpu_pcie_gen_max":
		d.pcieGenMax = m.Value
	case "node_gpu_pcie_width_max":
		d.pcieWidthMax = m.Value
	case "node_gpu_pcie_rx_mb_s":
		d.pcieRxMBs = m.Value
	case "node_gpu_pcie_tx_mb_s":
		d.pcieTxMBs = m.Value
	case "node_gpu_pcie_bandwidth_theoretical_mb_s":
		d.pcieTheoreticalMBs = m.Value
	case "node_gpu_pcie_bandwidth_max_mb_s":
		d.pcieBandwidthMaxMBs = m.Value
	case "node_gpu_pcie_rx_utilization_percent":
		d.pcieRxUtilPercent = m.Value
	case "node_gpu_pcie_tx_utilization_percent":
		d.pcieTxUtilPercent = m.Value
	case "node_gpu_pcie_link_utilization_percent":
		d.pcieLinkUtilPercent = m.Value
	case "node_gpu_throttle_active":
		prev := d.throttleActive
		d.throttleActive = m.Value
		if prev <= 0 && m.Value > 0 {
			s.recordEventLocked(node, d.gpuIndex, "throttle_active", "warning", "", 1, m.Name, ts, "GPU throttle became active")
		}
	case "node_gpu_throttle_thermal_active":
		d.throttleThermal = m.Value
	case "node_gpu_throttle_power_active":
		d.throttlePower = m.Value
	case "node_gpu_process_count":
		d.processCount = m.Value
	case "node_gpu_context_count":
		d.contextCount = m.Value
	case "node_gpu_kernel_active_contexts":
		d.kernelActiveContexts = m.Value
	case "node_gpu_kernel_hotspot_sm_util_percent":
		if m.Value > d.kernelHotspotPeakSMUtilPercent {
			d.kernelHotspotPeakSMUtilPercent = m.Value
		}
	case "node_gpu_xid_errors_total":
		prev := d.xidErrorsTotal
		d.xidErrorsTotal = m.Value
		s.applyCounterDeltaEventLocked(node, d, m.Value, prev, "xid", severityFromXidCode(lv.code), lv.code, m.Name, ts)
	case "node_gpu_uvm_faults_total":
		prev := d.uvmFaultsTotal
		d.uvmFaultsTotal = m.Value
		s.applyCounterDeltaEventLocked(node, d, m.Value, prev, "uvm_fault", "warning", lv.code, m.Name, ts)
	case "node_gpu_reset_events_total":
		prev := d.resetEventsTotal
		d.resetEventsTotal = m.Value
		s.applyCounterDeltaEventLocked(node, d, m.Value, prev, "reset", "critical", lv.code, m.Name, ts)
	case "node_gpu_reliability_events_total":
		prev := d.reliabilityEventsTotal
		d.reliabilityEventsTotal = m.Value
		s.applyCounterDeltaEventLocked(node, d, m.Value, prev, "reliability", "warning", lv.code, m.Name, ts)
	case "node_gpu_ecc_single_bit_errors_total":
		d.eccSingleBitErrorsTotal = m.Value
	case "node_gpu_ecc_double_bit_errors_total":
		prev := d.eccDoubleBitErrorsTotal
		d.eccDoubleBitErrorsTotal = m.Value
		s.applyCounterDeltaEventLocked(node, d, m.Value, prev, "ecc_double_bit", "critical", lv.code, m.Name, ts)
	case "node_gpu_ecc_volatile_single_bit_errors_total":
		d.eccVolatileSingleBitErrorsTotal = m.Value
	case "node_gpu_ecc_volatile_double_bit_errors_total":
		d.eccVolatileDoubleBitErrorsTotal = m.Value
	case "node_gpu_retired_pages_single_bit_total":
		d.retiredPagesSingleBitTotal = m.Value
	case "node_gpu_retired_pages_double_bit_total":
		d.retiredPagesDoubleBitTotal = m.Value
	case "node_gpu_retired_pages_pending":
		d.retiredPagesPending = m.Value
	case "node_gpu_remapped_rows_correctable_total":
		d.remappedRowsCorrectableTotal = m.Value
	case "node_gpu_remapped_rows_uncorrectable_total":
		d.remappedRowsUncorrectableTotal = m.Value
	case "node_gpu_remapped_rows_pending":
		d.remappedRowsPending = m.Value
	case "node_gpu_reset_required":
		prev := d.resetRequired
		d.resetRequired = m.Value
		if prev <= 0 && m.Value > 0 {
			s.recordEventLocked(node, d.gpuIndex, "reset_required", "critical", "", 1, m.Name, ts, "Driver requested GPU reset")
		}
	case "node_gpu_reset_recommended":
		d.resetRecommended = m.Value
	case "node_gpu_mig_enabled":
		d.migEnabled = m.Value
	case "node_gpu_mig_pending":
		d.migPending = m.Value
	case "node_gpu_nvlink_links":
		d.nvlinkLinks = m.Value
	case "node_gpu_event_total":
		eventType := lv.eventType
		if eventType == "" {
			eventType = "gpu_event"
		}
		severity := lv.severity
		if severity == "" {
			severity = defaultSeverity(eventType)
		}
		s.applyEventCounterLocked(node, d, eventType, severity, lv.code, m.Value, m.Name, ts)
	case "node_gpu_process_memory_mib",
		"node_gpu_process_sm_util_percent",
		"node_gpu_process_mem_util_percent",
		"node_gpu_process_encoder_util_percent",
		"node_gpu_process_decoder_util_percent",
		"node_gpu_process_context_active":
		s.applyProcessMetricLocked(d, m.Name, lv, m.Value, ts)
	}
}

func (s *Store) applyCounterDeltaEventLocked(node *nodeState, d *deviceState, next, prev float64, eventType, severity, code, source string, ts time.Time) {
	delta := counterDelta(prev, next)
	if delta <= 0 {
		return
	}
	s.recordEventLocked(node, d.gpuIndex, eventType, severity, code, delta, source, ts, "")
}

func (s *Store) applyEventCounterLocked(node *nodeState, d *deviceState, eventType, severity, code string, value float64, source string, ts time.Time) {
	key := eventType + "|" + severity + "|" + code + "|" + source
	prev := d.eventCounters[key]
	delta := counterDelta(prev, value)
	d.eventCounters[key] = value
	if delta <= 0 {
		return
	}
	s.recordEventLocked(node, d.gpuIndex, eventType, severity, code, delta, source, ts, "")
}

func counterDelta(prev, current float64) float64 {
	if current <= 0 {
		return 0
	}
	if current >= prev {
		return current - prev
	}
	// Counter reset.
	return current
}

func (s *Store) recordEventLocked(node *nodeState, gpuID, eventType, severity, code string, count float64, source string, ts time.Time, message string) {
	if count <= 0 {
		return
	}
	if strings.TrimSpace(eventType) == "" {
		return
	}
	if strings.TrimSpace(severity) == "" {
		severity = defaultSeverity(eventType)
	}
	if gpuID == "" {
		gpuID = "unknown"
	}
	evt := Event{
		Timestamp:   ts,
		CollectorID: node.collectorID,
		Hostname:    node.hostname,
		GPUIndex:    gpuID,
		EventType:   eventType,
		Severity:    severity,
		Code:        code,
		Count:       count,
		Source:      source,
		Message:     message,
	}
	if node.events == nil {
		node.events = ring.New[Event](s.cfg.EventBufferPerNode)
	}
	node.events.Push(evt)

	day := ts.UTC().Format("2006-01-02")
	eventPath := filepath.Join(s.cfg.PersistDir, "events", fmt.Sprintf("%s-%s.jsonl", sanitizeFilename(node.collectorID), day))
	s.eventBuf[eventPath] = append(s.eventBuf[eventPath], evt)
}

func defaultSeverity(eventType string) string {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "xid", "reset", "reset_required", "ecc_double_bit":
		return "critical"
	case "uvm_fault", "ecc", "reliability", "throttle_active":
		return "warning"
	default:
		return "info"
	}
}

func severityFromXidCode(code string) string {
	switch strings.TrimSpace(code) {
	case "13", "31", "43", "48", "63", "79", "94":
		return "critical"
	case "8", "14", "32", "45", "74":
		return "warning"
	default:
		return "info"
	}
}

func (s *Store) applyProcessMetricLocked(d *deviceState, metricName string, lv labelVals, value float64, ts time.Time) {
	if lv.pid == "" {
		return
	}
	p := d.processes[lv.pid]
	if p == nil {
		p = &processState{
			pid:      lv.pid,
			name:     lv.process,
			timeline: ring.New[processTimelineSample](s.cfg.TimelineSamplesPerProcess),
		}
		d.processes[lv.pid] = p
		d.topDirty = true
	} else {
		if lv.process != "" && p.name == "" {
			p.name = lv.process
			d.topDirty = true
		}
		if p.timeline == nil {
			p.timeline = ring.New[processTimelineSample](s.cfg.TimelineSamplesPerProcess)
		}
	}

	if lv.contextType != "" {
		p.contextType = lv.contextType
	}
	p.lastSeen = ts

	switch metricName {
	case "node_gpu_process_memory_mib":
		if p.memMiB != value {
			p.memMiB = value
			d.topDirty = true // changes ordering
		}
	case "node_gpu_process_sm_util_percent":
		if p.utilSMPct != value {
			p.utilSMPct = value
			d.topDirty = true
		}
	case "node_gpu_process_mem_util_percent":
		if p.utilMemPct != value {
			p.utilMemPct = value
			d.topDirty = true
		}
	case "node_gpu_process_encoder_util_percent":
		if p.utilEncPct != value {
			p.utilEncPct = value
			d.topDirty = true
		}
	case "node_gpu_process_decoder_util_percent":
		if p.utilDecPct != value {
			p.utilDecPct = value
			d.topDirty = true
		}
	case "node_gpu_process_context_active":
		if p.contextActive != value {
			p.contextActive = value
			d.topDirty = true
		}
	}
}

func (s *Store) appendTimelinesLocked(node *nodeState, ts time.Time) {
	for _, d := range node.gpus {
		if d == nil {
			continue
		}
		if d.timeline == nil {
			d.timeline = ring.New[deviceTimelineSample](s.cfg.TimelineSamplesPerGPU)
		}
		d.timeline.Push(deviceTimelineSample{
			timestamp:                      ts,
			utilSMPercent:                  d.utilSMPercent,
			utilMemPercent:                 d.utilMemPercent,
			utilEncPercent:                 d.utilEncPercent,
			utilDecPercent:                 d.utilDecPercent,
			memUsedMiB:                     d.memUsedMiB,
			memTotalMiB:                    d.memTotalMiB,
			powerDrawW:                     d.powerDrawW,
			tempC:                          d.tempC,
			pcieRxMBs:                      d.pcieRxMBs,
			pcieTxMBs:                      d.pcieTxMBs,
			pcieLinkUtilPercent:            d.pcieLinkUtilPercent,
			throttleActive:                 d.throttleActive,
			xidErrorsTotal:                 d.xidErrorsTotal,
			uvmFaultsTotal:                 d.uvmFaultsTotal,
			resetEventsTotal:               d.resetEventsTotal,
			reliabilityEventsTotal:         d.reliabilityEventsTotal,
			kernelHotspotPeakSMUtilPercent: d.kernelHotspotPeakSMUtilPercent,
		})

		for _, p := range d.processes {
			if p == nil || p.timeline == nil {
				continue
			}
			if !p.lastSeen.Equal(ts) {
				continue
			}
			p.timeline.Push(processTimelineSample{
				timestamp:     ts,
				memMiB:        p.memMiB,
				utilSMPct:     p.utilSMPct,
				utilMemPct:    p.utilMemPct,
				utilEncPct:    p.utilEncPct,
				utilDecPct:    p.utilDecPct,
				contextActive: p.contextActive,
			})
		}
	}
}

func (s *Store) pruneStaleProcessesLocked(node *nodeState, now time.Time) {
	cutoff := now.Add(-30 * time.Minute)
	for _, d := range node.gpus {
		if d == nil || len(d.processes) == 0 {
			continue
		}
		for pid, p := range d.processes {
			if p == nil {
				delete(d.processes, pid)
				d.topDirty = true
				continue
			}
			if p.lastSeen.IsZero() || p.lastSeen.Before(cutoff) {
				delete(d.processes, pid)
				d.topDirty = true
			}
		}
	}
}

func (s *Store) snapshotNodeLocked(n *nodeState) *Node {
	out := &Node{
		CollectorID:  n.collectorID,
		Hostname:     n.hostname,
		Labels:       cloneStringMap(n.labels),
		LastSeen:     n.lastSeen,
		GPUCount:     n.gpuCount,
		GPUs:         make(map[string]Device, len(n.gpus)),
		RecentEvents: s.recentEventsLocked(n),
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

			UtilSMPercent:       d.utilSMPercent,
			UtilMemPercent:      d.utilMemPercent,
			UtilEncPercent:      d.utilEncPercent,
			UtilDecPercent:      d.utilDecPercent,
			UtilJpegPercent:     d.utilJpegPercent,
			UtilOFAPercent:      d.utilOFAPercent,
			PowerState:          d.powerState,
			MemTotalMiB:         d.memTotalMiB,
			MemUsedMiB:          d.memUsedMiB,
			MemFreeMiB:          d.memFreeMiB,
			MemReservedMiB:      d.memReservedMiB,
			Bar1MemTotalMiB:     d.bar1MemTotalMiB,
			Bar1MemUsedMiB:      d.bar1MemUsedMiB,
			Bar1MemFreeMiB:      d.bar1MemFreeMiB,
			TempC:               d.tempC,
			TempMemC:            d.tempMemC,
			PowerDrawW:          d.powerDrawW,
			PowerLimitW:         d.powerLimitW,
			PCIEGen:             d.pcieGen,
			PCIEWidth:           d.pcieWidth,
			PCIEGenMax:          d.pcieGenMax,
			PCIEWidthMax:        d.pcieWidthMax,
			PCIERxMBs:           d.pcieRxMBs,
			PCIETxMBs:           d.pcieTxMBs,
			PCIETheoreticalMBs:  d.pcieTheoreticalMBs,
			PCIEBandwidthMaxMBs: d.pcieBandwidthMaxMBs,
			PCIERxUtilPercent:   d.pcieRxUtilPercent,
			PCIETxUtilPercent:   d.pcieTxUtilPercent,
			PCIELinkUtilPercent: d.pcieLinkUtilPercent,

			ThrottleActive:                  d.throttleActive,
			ThrottleThermal:                 d.throttleThermal,
			ThrottlePower:                   d.throttlePower,
			ProcessCount:                    d.processCount,
			ContextCount:                    d.contextCount,
			XidErrorsTotal:                  d.xidErrorsTotal,
			UVMFaultsTotal:                  d.uvmFaultsTotal,
			ResetEventsTotal:                d.resetEventsTotal,
			ReliabilityEventsTotal:          d.reliabilityEventsTotal,
			ECCSingleBitErrorsTotal:         d.eccSingleBitErrorsTotal,
			ECCDoubleBitErrorsTotal:         d.eccDoubleBitErrorsTotal,
			ECCVolatileSingleBitErrorsTotal: d.eccVolatileSingleBitErrorsTotal,
			ECCVolatileDoubleBitErrorsTotal: d.eccVolatileDoubleBitErrorsTotal,
			RetiredPagesSingleBitTotal:      d.retiredPagesSingleBitTotal,
			RetiredPagesDoubleBitTotal:      d.retiredPagesDoubleBitTotal,
			RetiredPagesPending:             d.retiredPagesPending,
			RemappedRowsCorrectableTotal:    d.remappedRowsCorrectableTotal,
			RemappedRowsUncorrectableTotal:  d.remappedRowsUncorrectableTotal,
			RemappedRowsPending:             d.remappedRowsPending,
			ResetRequired:                   d.resetRequired,
			ResetRecommended:                d.resetRecommended,
			MigEnabled:                      d.migEnabled,
			MigPending:                      d.migPending,
			PersistenceModeOn:               d.persistenceModeOn,
			NVLinkLinks:                     d.nvlinkLinks,
			KernelHotspotPeakSMUtilPercent:  d.kernelHotspotPeakSMUtilPercent,
			KernelActiveContexts:            d.kernelActiveContexts,

			Processes: s.topProcessesLocked(d),
		}
	}
	return out
}

func (s *Store) recentEventsLocked(n *nodeState) []Event {
	if n == nil || n.events == nil {
		return nil
	}
	limit := s.cfg.RecentEventsInSnapshot
	if limit <= 0 {
		limit = 200
	}
	events := n.events.SliceLastN(limit)
	if len(events) == 0 {
		return nil
	}
	out := make([]Event, len(events))
	copy(out, events)
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
			PID:           p.pid,
			Name:          p.name,
			MemMiB:        p.memMiB,
			UtilSMPct:     p.utilSMPct,
			UtilMemPct:    p.utilMemPct,
			UtilEncPct:    p.utilEncPct,
			UtilDecPct:    p.utilDecPct,
			ContextActive: p.contextActive,
			ContextType:   p.contextType,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i]
		right := out[j]
		leftScore := left.MemMiB + left.UtilSMPct*4 + left.UtilEncPct*2 + left.ContextActive*10
		rightScore := right.MemMiB + right.UtilSMPct*4 + right.UtilEncPct*2 + right.ContextActive*10
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if left.UtilSMPct != right.UtilSMPct {
			return left.UtilSMPct > right.UtilSMPct
		}
		if left.MemMiB != right.MemMiB {
			return left.MemMiB > right.MemMiB
		}
		return left.PID < right.PID
	})
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
		if err := appendJSONLines(path, records); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) flushEvents() error {
	s.mu.Lock()
	if len(s.eventBuf) == 0 {
		s.mu.Unlock()
		return nil
	}
	buf := s.eventBuf
	s.eventBuf = make(map[string][]Event)
	s.mu.Unlock()

	for path, records := range buf {
		if len(records) == 0 {
			continue
		}
		if err := appendJSONLines(path, records); err != nil {
			return err
		}
	}
	return nil
}

func appendJSONLines[T any](path string, records []T) error {
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
	return nil
}

func (s *Store) cleanupHistory() error {
	if s.cfg.Retention <= 0 {
		return nil
	}
	if err := cleanupDirByRetention(filepath.Join(s.cfg.PersistDir, "history"), s.cfg.Retention); err != nil {
		return err
	}
	if err := cleanupDirByRetention(filepath.Join(s.cfg.PersistDir, "events"), s.cfg.Retention); err != nil {
		return err
	}
	return nil
}

func cleanupDirByRetention(dir string, retention time.Duration) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	cutoff := time.Now().Add(-retention)
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

func deviceTimelineMetricValue(s deviceTimelineSample, metric string) (float64, bool) {
	switch strings.TrimSpace(strings.ToLower(metric)) {
	case "node_gpu_utilization_sm_percent", "util_sm_percent", "sm_util":
		return s.utilSMPercent, true
	case "node_gpu_utilization_memory_percent", "util_mem_percent", "mem_util":
		return s.utilMemPercent, true
	case "node_gpu_utilization_encoder_percent", "util_enc_percent", "enc_util":
		return s.utilEncPercent, true
	case "node_gpu_utilization_decoder_percent", "util_dec_percent", "dec_util":
		return s.utilDecPercent, true
	case "node_gpu_memory_used_mib", "mem_used_mib":
		return s.memUsedMiB, true
	case "node_gpu_memory_total_mib", "mem_total_mib":
		return s.memTotalMiB, true
	case "node_gpu_power_draw_watts", "power_draw_w":
		return s.powerDrawW, true
	case "node_gpu_temperature_celsius", "temp_c":
		return s.tempC, true
	case "node_gpu_pcie_rx_mb_s", "pcie_rx_mb_s":
		return s.pcieRxMBs, true
	case "node_gpu_pcie_tx_mb_s", "pcie_tx_mb_s":
		return s.pcieTxMBs, true
	case "node_gpu_pcie_link_utilization_percent", "pcie_link_util_percent":
		return s.pcieLinkUtilPercent, true
	case "node_gpu_throttle_active", "throttle_active":
		return s.throttleActive, true
	case "node_gpu_xid_errors_total", "xid_errors_total":
		return s.xidErrorsTotal, true
	case "node_gpu_uvm_faults_total", "uvm_faults_total":
		return s.uvmFaultsTotal, true
	case "node_gpu_reset_events_total", "reset_events_total":
		return s.resetEventsTotal, true
	case "node_gpu_reliability_events_total", "reliability_events_total":
		return s.reliabilityEventsTotal, true
	case "node_gpu_kernel_hotspot_peak_sm_util_percent", "kernel_hotspot_peak_sm_util_percent":
		return s.kernelHotspotPeakSMUtilPercent, true
	default:
		return 0, false
	}
}

func processTimelineMetricValue(s processTimelineSample, metric string) (float64, bool) {
	switch strings.TrimSpace(strings.ToLower(metric)) {
	case "node_gpu_process_memory_mib", "mem_mib":
		return s.memMiB, true
	case "node_gpu_process_sm_util_percent", "util_sm_percent", "sm_util":
		return s.utilSMPct, true
	case "node_gpu_process_mem_util_percent", "util_mem_percent", "mem_util":
		return s.utilMemPct, true
	case "node_gpu_process_encoder_util_percent", "util_enc_percent", "enc_util":
		return s.utilEncPct, true
	case "node_gpu_process_decoder_util_percent", "util_dec_percent", "dec_util":
		return s.utilDecPct, true
	case "node_gpu_process_context_active", "context_active":
		return s.contextActive, true
	default:
		return 0, false
	}
}

type labelVals struct {
	gpuID         string
	uuid          string
	name          string
	driverVersion string
	cudaVersion   string
	pid           string
	process       string
	code          string
	eventType     string
	severity      string
	contextType   string
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
		case "code":
			lv.code = l.Value
		case "event_type":
			lv.eventType = l.Value
		case "severity":
			lv.severity = l.Value
		case "context_type":
			lv.contextType = l.Value
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
