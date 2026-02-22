// Package probe implements eBPF-based security and deep observability (Level 5+).
// It interfaces with the underlying eBPF probes (C/C++ SDK) to receive high-fidelity
// kernel and device events over a Unix domain socket. The actual kernel eBPF programs
// live outside this Go code; this collector focuses on low-overhead aggregation and export.
package probe

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// EBPFEvent is the unified envelope emitted by the privileged eBPF agent.
// It intentionally stays small to minimize ring-buffer traffic.
type EBPFEvent struct {
	Timestamp int64  `json:"timestamp"` // unix nsec
	Category  string `json:"category"`  // sched, io, net, mem, gpu, syscall, security
	Type      string `json:"type"`      // e.g., "sched_wakeup", "block_rq_complete"
	PID       int    `json:"pid,omitempty"`
	CPU       int    `json:"cpu,omitempty"`
	Comm      string `json:"comm,omitempty"`
	Cgroup    string `json:"cgroup,omitempty"`
	Device    string `json:"device,omitempty"` // block / net dev / gpu id
	GPUIndex  int    `json:"gpu_index,omitempty"`
	Bytes     uint64 `json:"bytes,omitempty"`
	LatencyNs uint64 `json:"latency_ns,omitempty"`
	Details   string `json:"details,omitempty"` // filename, ip:port, throttling reason, etc.
}

// EBPFConfig controls which events are accepted and from where.
type EBPFConfig struct {
	Enabled     bool
	SocketPath  string
	Categories  []string      // allow-list; empty = all
	MaxMsgBytes int           // scanner buffer; default 64k
	RateWindow  time.Duration // for rate/gauge derivation; default based on caller cadence
}

// EBPFCollector collects and aggregates eBPF events
type EBPFCollector struct {
	cfg      EBPFConfig
	listener net.Listener

	mu     sync.Mutex
	counts map[string]uint64 // key(category|type) -> count
	bytes  map[string]uint64 // key -> bytes
	latSum map[string]uint64 // key -> latency sum (ns)
	latCnt map[string]uint64 // key -> latency count

	lastCounts map[string]uint64
	lastBytes  map[string]uint64
	lastLatSum map[string]uint64
	lastLatCnt map[string]uint64
	lastEmit   time.Time

	// GPU keyed aggregates (optional, keeps labels small)
	gpuCounts map[int]uint64
	gpuBytes  map[int]uint64
	gpuLatSum map[int]uint64
	gpuLatCnt map[int]uint64

	byComm map[string]map[string]uint64 // type -> comm -> count

	stopCh chan struct{}
}

// NewEBPFCollector creates a new eBPF collector
func NewEBPFCollector(socketPath string) *EBPFCollector {
	return &EBPFCollector{
		cfg: EBPFConfig{
			Enabled:    true,
			SocketPath: socketPath,
		},
		counts:     make(map[string]uint64),
		bytes:      make(map[string]uint64),
		latSum:     make(map[string]uint64),
		latCnt:     make(map[string]uint64),
		lastCounts: make(map[string]uint64),
		lastBytes:  make(map[string]uint64),
		lastLatSum: make(map[string]uint64),
		lastLatCnt: make(map[string]uint64),
		gpuCounts:  make(map[int]uint64),
		gpuBytes:   make(map[int]uint64),
		gpuLatSum:  make(map[int]uint64),
		gpuLatCnt:  make(map[int]uint64),
		byComm:     make(map[string]map[string]uint64),
		stopCh:     make(chan struct{}),
	}
}

// NewEBPFCollectorWithConfig creates a collector using explicit config.
func NewEBPFCollectorWithConfig(cfg EBPFConfig) *EBPFCollector {
	if cfg.SocketPath == "" {
		cfg.SocketPath = "/var/run/sre_collector_ebpf.sock"
	}
	if cfg.MaxMsgBytes == 0 {
		cfg.MaxMsgBytes = 64 * 1024
	}
	if cfg.RateWindow <= 0 {
		cfg.RateWindow = 10 * time.Second
	}
	return &EBPFCollector{
		cfg:        cfg,
		counts:     make(map[string]uint64),
		bytes:      make(map[string]uint64),
		latSum:     make(map[string]uint64),
		latCnt:     make(map[string]uint64),
		lastCounts: make(map[string]uint64),
		lastBytes:  make(map[string]uint64),
		lastLatSum: make(map[string]uint64),
		lastLatCnt: make(map[string]uint64),
		gpuCounts:  make(map[int]uint64),
		gpuBytes:   make(map[int]uint64),
		gpuLatSum:  make(map[int]uint64),
		gpuLatCnt:  make(map[int]uint64),
		byComm:     make(map[string]map[string]uint64),
		stopCh:     make(chan struct{}),
	}
}

// Start starts listening for eBPF events
func (ec *EBPFCollector) Start() error {
	// Simple listener (mockable integration point for C++ SDK)
	// In production, the C++ collector connects here or writes to this socket
	if _, err := os.Stat(ec.cfg.SocketPath); err == nil {
		os.Remove(ec.cfg.SocketPath)
	}

	l, err := net.Listen("unix", ec.cfg.SocketPath)
	if err != nil {
		// Log error but don't fail, maybe we're client?
		// Assuming we are server for the C++ probe
		return err
	}
	ec.listener = l

	go ec.acceptLoop()
	return nil
}

// Stop stops collection
func (ec *EBPFCollector) Stop() {
	close(ec.stopCh)
	if ec.listener != nil {
		ec.listener.Close()
	}
}

func (ec *EBPFCollector) acceptLoop() {
	for {
		conn, err := ec.listener.Accept()
		if err != nil {
			select {
			case <-ec.stopCh:
				return
			default:
				continue
			}
		}
		go ec.handleConn(conn)
	}
}

func (ec *EBPFCollector) handleConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	if ec.cfg.MaxMsgBytes > 0 {
		buf := make([]byte, 0, ec.cfg.MaxMsgBytes)
		scanner.Buffer(buf, ec.cfg.MaxMsgBytes)
	}

	for scanner.Scan() {
		text := scanner.Text()
		// Support both legacy SecurityEvent and new EBPFEvent
		var evt EBPFEvent
		if err := json.Unmarshal([]byte(text), &evt); err == nil && evt.Timestamp != 0 {
			ec.recordEvent(&evt)
			continue
		}
		var legacy SecurityEvent
		if err := json.Unmarshal([]byte(text), &legacy); err == nil && legacy.Type != "" {
			evt = EBPFEvent{
				Timestamp: time.Now().UnixNano(),
				Category:  "security",
				Type:      legacy.Type,
				PID:       legacy.PID,
				Comm:      legacy.Comm,
				Details:   legacy.Details,
			}
			ec.recordEvent(&evt)
		}
	}
}

func (ec *EBPFCollector) recordEvent(evt *EBPFEvent) {
	if len(ec.cfg.Categories) > 0 && !contains(ec.cfg.Categories, evt.Category) {
		return
	}

	ec.mu.Lock()
	defer ec.mu.Unlock()

	key := evt.Category + "|" + evt.Type
	ec.counts[key]++
	if evt.Bytes > 0 {
		ec.bytes[key] += evt.Bytes
	}
	if evt.LatencyNs > 0 {
		ec.latSum[key] += evt.LatencyNs
		ec.latCnt[key]++
	}

	if evt.Comm != "" {
		if _, ok := ec.byComm[evt.Type]; !ok {
			ec.byComm[evt.Type] = make(map[string]uint64)
		}
		ec.byComm[evt.Type][evt.Comm]++
	}

	if evt.Category == "gpu" {
		idx := evt.GPUIndex
		if idx < 0 {
			idx = 0
		}
		ec.gpuCounts[idx]++
		if evt.Bytes > 0 {
			ec.gpuBytes[idx] += evt.Bytes
		}
		if evt.LatencyNs > 0 {
			ec.gpuLatSum[idx] += evt.LatencyNs
			ec.gpuLatCnt[idx]++
		}
	}
}

// GetMetrics returns aggregated metrics
func (ec *EBPFCollector) GetMetrics(now time.Time) []Metric {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	var metrics []Metric

	// Total events by category/type
	for key, count := range ec.counts {
		category, typ := splitKey(key)
		metrics = append(metrics, Metric{
			Name:      "node_ebpf_events_total",
			Type:      "counter",
			Value:     float64(count),
			Labels:    map[string]string{"category": category, "type": typ},
			Timestamp: now,
		})
	}

	// Latency and bytes (where applicable)
	for key, sum := range ec.latSum {
		if sum == 0 {
			continue
		}
		category, typ := splitKey(key)
		cnt := ec.latCnt[key]
		metrics = append(metrics, Metric{
			Name:      "node_ebpf_events_latency_seconds_sum",
			Type:      "counter",
			Value:     float64(sum) / 1e9,
			Labels:    map[string]string{"category": category, "type": typ},
			Timestamp: now,
		})
		metrics = append(metrics, Metric{
			Name:      "node_ebpf_events_latency_seconds_count",
			Type:      "counter",
			Value:     float64(cnt),
			Labels:    map[string]string{"category": category, "type": typ},
			Timestamp: now,
		})
	}

	for key, b := range ec.bytes {
		if b == 0 {
			continue
		}
		category, typ := splitKey(key)
		metrics = append(metrics, Metric{
			Name:      "node_ebpf_events_bytes_total",
			Type:      "counter",
			Value:     float64(b),
			Labels:    map[string]string{"category": category, "type": typ},
			Timestamp: now,
		})
	}

	// Rates (delta / window)
	window := now.Sub(ec.lastEmit)
	if ec.lastEmit.IsZero() || window <= 0 || window > 10*ec.cfg.RateWindow {
		window = ec.cfg.RateWindow
	}
	for key, current := range ec.counts {
		prev := ec.lastCounts[key]
		if current < prev {
			prev = 0
		}
		delta := current - prev
		if delta == 0 {
			continue
		}
		category, typ := splitKey(key)
		metrics = append(metrics, Metric{
			Name:      "node_ebpf_events_rate",
			Type:      "gauge",
			Value:     float64(delta) / window.Seconds(),
			Labels:    map[string]string{"category": category, "type": typ},
			Timestamp: now,
		})
	}

	for key, b := range ec.bytes {
		prev := ec.lastBytes[key]
		if b < prev {
			prev = 0
		}
		delta := b - prev
		if delta == 0 {
			continue
		}
		category, typ := splitKey(key)
		metrics = append(metrics, Metric{
			Name:      "node_ebpf_events_bytes_rate",
			Type:      "gauge",
			Value:     float64(delta) / window.Seconds(),
			Labels:    map[string]string{"category": category, "type": typ},
			Timestamp: now,
		})
	}

	for key, sum := range ec.latSum {
		prev := ec.lastLatSum[key]
		prevCnt := ec.lastLatCnt[key]
		cnt := ec.latCnt[key]
		if sum < prev {
			prev = 0
		}
		if cnt < prevCnt {
			prevCnt = 0
		}
		deltaSum := sum - prev
		deltaCnt := cnt - prevCnt
		if deltaCnt > 0 && deltaSum > 0 {
			category, typ := splitKey(key)
			metrics = append(metrics, Metric{
				Name:      "node_ebpf_events_latency_seconds_avg",
				Type:      "gauge",
				Value:     (float64(deltaSum) / float64(deltaCnt)) / 1e9,
				Labels:    map[string]string{"category": category, "type": typ},
				Timestamp: now,
			})
		}
	}

	// GPU aggregates (optional)
	for idx, count := range ec.gpuCounts {
		metrics = append(metrics, Metric{
			Name:      "node_ebpf_gpu_events_total",
			Type:      "counter",
			Value:     float64(count),
			Labels:    map[string]string{"gpu_index": strconv.Itoa(idx)},
			Timestamp: now,
		})
		if b := ec.gpuBytes[idx]; b > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_ebpf_gpu_bytes_total",
				Type:      "counter",
				Value:     float64(b),
				Labels:    map[string]string{"gpu_index": strconv.Itoa(idx)},
				Timestamp: now,
			})
		}
		if ls := ec.gpuLatSum[idx]; ls > 0 && ec.gpuLatCnt[idx] > 0 {
			metrics = append(metrics, Metric{
				Name:      "node_ebpf_gpu_latency_seconds_avg",
				Type:      "gauge",
				Value:     (float64(ls) / float64(ec.gpuLatCnt[idx])) / 1e9,
				Labels:    map[string]string{"gpu_index": strconv.Itoa(idx)},
				Timestamp: now,
			})
		}
	}

	// Top offenders by comm (only top 5 per type to save cardinality)
	// (Simplification: just dumping all for now, assuming low volume or filter in C++)
	for evType, comms := range ec.byComm {
		for comm, count := range comms {
			if count > 0 {
				metrics = append(metrics, Metric{
					Name:      "node_ebpf_process_events_total",
					Type:      "counter",
					Value:     float64(count),
					Labels:    map[string]string{"type": evType, "process": comm},
					Timestamp: now,
				})
			}
		}
	}

	ec.snapshot(now)
	return metrics
}

func splitKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			return key[:i], key[i+1:]
		}
	}
	return "unknown", key
}

func contains(list []string, needle string) bool {
	for _, v := range list {
		if v == needle {
			return true
		}
	}
	return false
}

func (ec *EBPFCollector) snapshot(now time.Time) {
	ec.lastEmit = now
	ec.lastCounts = cloneUint64Map(ec.counts)
	ec.lastBytes = cloneUint64Map(ec.bytes)
	ec.lastLatSum = cloneUint64Map(ec.latSum)
	ec.lastLatCnt = cloneUint64Map(ec.latCnt)
}

func cloneUint64Map(src map[string]uint64) map[string]uint64 {
	dst := make(map[string]uint64, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// SecurityEvent is kept for backward compatibility with the legacy C++ probe.
type SecurityEvent struct {
	Timestamp int64  `json:"timestamp"`
	Type      string `json:"type"` // exec, open, connect, accept
	PID       int    `json:"pid"`
	Comm      string `json:"comm"`
	Details   string `json:"details"` // filename, ip:port, etc.
}
