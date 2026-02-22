package controller

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

const (
	defaultFleetTrendWindow = 30 * time.Minute
	maxFleetTrendWindow     = 24 * time.Hour
	defaultFleetTrendLimit  = 360
	maxFleetTrendLimit      = 2000
)

type fleetTrendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	IsAnomaly bool      `json:"is_anomaly,omitempty"`
	ZScore    float64   `json:"z_score,omitempty"`
}

type fleetTrendSeries struct {
	Key        string            `json:"key"`
	Display    string            `json:"display"`
	Unit       string            `json:"unit"`
	Latest     float64           `json:"latest"`
	Min        float64           `json:"min"`
	Max        float64           `json:"max"`
	Avg        float64           `json:"avg"`
	ChangePct  float64           `json:"change_pct"`
	SpikeCount int               `json:"spike_count"`
	Points     []fleetTrendPoint `json:"points"`
}

type fleetTrendResponse struct {
	CollectorID    string             `json:"collector_id"`
	Hostname       string             `json:"hostname"`
	Window         string             `json:"window"`
	GeneratedAt    time.Time          `json:"generated_at"`
	LatestAt       time.Time          `json:"latest_at,omitempty"`
	SampleCount    int                `json:"sample_count"`
	NumericSummary map[string]float64 `json:"numeric_summary"`
	Series         []fleetTrendSeries `json:"series"`
}

type fleetTrendMetricSpec struct {
	Key     string
	Display string
	Unit    string
	Extract func(map[string]float64) (float64, bool)
}

var fleetTrendMetricSpecs = []fleetTrendMetricSpec{
	{
		Key:     "cpu_usage_percent",
		Display: "CPU Usage",
		Unit:    "percent",
		Extract: directMetricExtractor("node_cpu_usage_percent"),
	},
	{
		Key:     "cpu_iowait_percent",
		Display: "CPU IOWait",
		Unit:    "percent",
		Extract: directMetricExtractor("node_cpu_iowait_percent"),
	},
	{
		Key:     "memory_used_percent",
		Display: "Memory Usage",
		Unit:    "percent",
		Extract: memoryUsagePercentExtractor,
	},
	{
		Key:     "load1",
		Display: "Load (1m)",
		Unit:    "count",
		Extract: directMetricExtractor("node_load1"),
	},
	{
		Key:     "cpu_pressure_some_avg10",
		Display: "CPU Pressure (some, avg10)",
		Unit:    "percent",
		Extract: directMetricExtractor("node_pressure_cpu_some_avg10"),
	},
	{
		Key:     "network_rx_bytes_per_second",
		Display: "Network RX",
		Unit:    "bytes_per_second",
		Extract: firstMetricExtractor("node_network_total_receive_bytes_per_second", "node_network_receive_bytes_per_second"),
	},
	{
		Key:     "network_tx_bytes_per_second",
		Display: "Network TX",
		Unit:    "bytes_per_second",
		Extract: firstMetricExtractor("node_network_total_transmit_bytes_per_second", "node_network_transmit_bytes_per_second"),
	},
	{
		Key:     "network_utilization_peak_percent",
		Display: "Network Utilization (Peak)",
		Unit:    "percent",
		Extract: directMetricExtractor("node_network_utilization_peak_percent"),
	},
	{
		Key:     "network_capacity_utilization_percent",
		Display: "Network Capacity Utilization",
		Unit:    "percent",
		Extract: directMetricExtractor("node_network_capacity_utilization_percent"),
	},
	{
		Key:     "tcp_retransmits_per_second",
		Display: "TCP Retransmits",
		Unit:    "count_per_second",
		Extract: directMetricExtractor("node_tcp_retransmits_per_second"),
	},
	{
		Key:     "tcp_retransmit_ratio",
		Display: "TCP Retransmit Ratio",
		Unit:    "ratio",
		Extract: directMetricExtractor("node_tcp_retransmit_ratio"),
	},
	{
		Key:     "softnet_dropped_per_second",
		Display: "Softnet Drops",
		Unit:    "count_per_second",
		Extract: directMetricExtractor("node_softnet_dropped_per_second"),
	},
	{
		Key:     "rdma_errors_per_second",
		Display: "RDMA Errors",
		Unit:    "count_per_second",
		Extract: directMetricExtractor("node_rdma_errors_per_second"),
	},
	{
		Key:     "rdma_congestion_events_per_second",
		Display: "RDMA Congestion Events",
		Unit:    "count_per_second",
		Extract: directMetricExtractor("node_rdma_congestion_events_per_second"),
	},
	{
		Key:     "probe_core_client_available",
		Display: "Probe-core Client Available",
		Unit:    "count",
		Extract: directMetricExtractor("collector_probe_core_client_available"),
	},
	{
		Key:     "probe_core_active",
		Display: "Probe-core Active",
		Unit:    "count",
		Extract: directMetricExtractor("collector_probe_core_active"),
	},
	{
		Key:     "probe_core_fresh",
		Display: "Probe-core Fresh",
		Unit:    "count",
		Extract: directMetricExtractor("collector_probe_core_fresh"),
	},
	{
		Key:     "probe_core_selection_valid",
		Display: "Probe-core Selection Valid",
		Unit:    "count",
		Extract: directMetricExtractor("collector_probe_core_collector_selection_valid"),
	},
	{
		Key:     "probe_core_last_frame_age_ms",
		Display: "Probe-core Last Frame Age",
		Unit:    "milliseconds",
		Extract: metricWithScaleExtractor("collector_probe_core_last_frame_age_seconds", 1000.0),
	},
	{
		Key:     "probe_core_decode_errors_total",
		Display: "Probe-core Decode Errors",
		Unit:    "count",
		Extract: directMetricExtractor("collector_probe_core_decode_errors_total"),
	},
	{
		Key:     "probe_core_crc_failures_total",
		Display: "Probe-core CRC Failures",
		Unit:    "count",
		Extract: directMetricExtractor("collector_probe_core_crc_failures_total"),
	},
	{
		Key:     "probe_core_restarts_total",
		Display: "Probe-core Restarts",
		Unit:    "count",
		Extract: directMetricExtractor("collector_probe_core_restarts_total"),
	},
	{
		Key:     "disk_read_bytes_per_second",
		Display: "Disk Read",
		Unit:    "bytes_per_second",
		Extract: firstMetricExtractor("node_disk_total_read_bytes_per_second", "node_disk_read_bytes_per_second"),
	},
	{
		Key:     "disk_write_bytes_per_second",
		Display: "Disk Write",
		Unit:    "bytes_per_second",
		Extract: firstMetricExtractor("node_disk_total_written_bytes_per_second", "node_disk_written_bytes_per_second"),
	},
	{
		Key:     "disk_total_iops_per_second",
		Display: "Disk IOPS",
		Unit:    "iops",
		Extract: directMetricExtractor("node_disk_total_iops_per_second"),
	},
	{
		Key:     "disk_utilization_peak_percent",
		Display: "Disk Utilization (Peak)",
		Unit:    "percent",
		Extract: directMetricExtractor("node_disk_utilization_peak_percent"),
	},
	{
		Key:     "disk_queue_depth_total",
		Display: "Disk Queue Depth",
		Unit:    "count",
		Extract: directMetricExtractor("node_disk_queue_depth_total"),
	},
	{
		Key:     "disk_avg_request_latency_ms",
		Display: "Disk Request Latency",
		Unit:    "milliseconds",
		Extract: metricWithScaleExtractor("node_disk_avg_request_latency_seconds", 1000.0),
	},
	{
		Key:     "disk_request_latency_p50_ms",
		Display: "Disk Latency P50",
		Unit:    "milliseconds",
		Extract: metricWithScaleExtractor("node_disk_request_latency_p50_seconds", 1000.0),
	},
	{
		Key:     "disk_request_latency_p90_ms",
		Display: "Disk Latency P90",
		Unit:    "milliseconds",
		Extract: metricWithScaleExtractor("node_disk_request_latency_p90_seconds", 1000.0),
	},
	{
		Key:     "disk_request_latency_p99_ms",
		Display: "Disk Latency P99",
		Unit:    "milliseconds",
		Extract: metricWithScaleExtractor("node_disk_request_latency_p99_seconds", 1000.0),
	},
	{
		Key:     "nvme_total_iops_per_second",
		Display: "NVMe IOPS",
		Unit:    "iops",
		Extract: directMetricExtractor("node_nvme_total_iops_per_second"),
	},
	{
		Key:     "nvme_queue_depth_total",
		Display: "NVMe Queue Depth",
		Unit:    "count",
		Extract: directMetricExtractor("node_nvme_queue_depth_total"),
	},
	{
		Key:     "nvme_utilization_peak_percent",
		Display: "NVMe Utilization (Peak)",
		Unit:    "percent",
		Extract: directMetricExtractor("node_nvme_utilization_peak_percent"),
	},
	{
		Key:     "nvme_avg_request_latency_ms",
		Display: "NVMe Request Latency",
		Unit:    "milliseconds",
		Extract: metricWithScaleExtractor("node_nvme_avg_request_latency_seconds", 1000.0),
	},
	{
		Key:     "filesystem_space_pressure_percent",
		Display: "Filesystem Space Pressure",
		Unit:    "percent",
		Extract: directMetricExtractor("node_filesystem_space_pressure_percent"),
	},
	{
		Key:     "filesystem_inode_pressure_percent",
		Display: "Filesystem Inode Pressure",
		Unit:    "percent",
		Extract: directMetricExtractor("node_filesystem_inode_pressure_percent"),
	},
	{
		Key:     "pagecache_dirty_bytes",
		Display: "Page Cache Dirty",
		Unit:    "bytes",
		Extract: directMetricExtractor("node_memory_Dirty_bytes"),
	},
	{
		Key:     "pagecache_writeback_bytes",
		Display: "Page Cache Writeback",
		Unit:    "bytes",
		Extract: directMetricExtractor("node_memory_Writeback_bytes"),
	},
	{
		Key:     "vm_pgpgin_per_second",
		Display: "VM Page-ins",
		Unit:    "pages_per_second",
		Extract: directMetricExtractor("node_vmstat_pgpgin_per_second"),
	},
	{
		Key:     "vm_pgpgout_per_second",
		Display: "VM Page-outs",
		Unit:    "pages_per_second",
		Extract: directMetricExtractor("node_vmstat_pgpgout_per_second"),
	},
	{
		Key:     "vm_dirtied_pages_per_second",
		Display: "VM Dirtied Pages",
		Unit:    "pages_per_second",
		Extract: directMetricExtractor("node_vmstat_nr_dirtied_per_second"),
	},
	{
		Key:     "vm_written_pages_per_second",
		Display: "VM Written Pages",
		Unit:    "pages_per_second",
		Extract: directMetricExtractor("node_vmstat_nr_written_per_second"),
	},
	{
		Key:     "io_pressure_some_avg10",
		Display: "I/O Pressure (some, avg10)",
		Unit:    "percent",
		Extract: directMetricExtractor("node_pressure_io_some_avg10"),
	},
	{
		Key:     "io_pressure_full_avg10",
		Display: "I/O Pressure (full, avg10)",
		Unit:    "percent",
		Extract: directMetricExtractor("node_pressure_io_full_avg10"),
	},
	{
		Key:     "procs_running",
		Display: "Running Processes",
		Unit:    "count",
		Extract: directMetricExtractor("node_procs_running"),
	},
	{
		Key:     "procs_blocked",
		Display: "Blocked Processes",
		Unit:    "count",
		Extract: directMetricExtractor("node_procs_blocked"),
	},
	{
		Key:     "fd_usage_percent",
		Display: "FD Usage",
		Unit:    "percent",
		Extract: fdUsagePercentExtractor,
	},
	{
		Key:     "numa_locality_ratio_percent",
		Display: "NUMA Locality Ratio",
		Unit:    "percent",
		Extract: directMetricExtractor("node_numa_locality_ratio_percent"),
	},
	{
		Key:     "gpu_utilization_percent",
		Display: "GPU Utilization",
		Unit:    "percent",
		Extract: directMetricExtractor("node_gpu_utilization_sm_avg_percent"),
	},
	{
		Key:     "gpu_memory_used_mib",
		Display: "GPU Memory Used",
		Unit:    "mib",
		Extract: directMetricExtractor("node_gpu_memory_used_total_mib"),
	},
}

func (c *Controller) initIngest() {
	if c.ingestStore != nil {
		return
	}
	c.ingestStore = ingest.NewMemoryStore()

	var processors []ingest.Processor
	if c.config.GPU.Enabled {
		c.gpuStore = gpuobs.New(c.config.GPU)
		processors = append(processors, c.gpuStore)
	}

	c.ingestServer = ingest.NewServer(c.ingestStore, c.logger, processors...)
}

func (c *Controller) startIngest() error {
	if c.ingestServer == nil {
		c.initIngest()
	}

	if c.config.GRPCListenAddr == "" {
		c.logger.Info("ingest server disabled (grpc_listen empty)")
		return nil
	}

	listener, resolvedAddr, err := listenWithFallback(c.config.GRPCListenAddr, c.logger, "grpc")
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer()
	telemetryv1.RegisterTelemetryIngestServer(grpcServer, c.ingestServer)

	c.grpcServer = grpcServer
	c.grpcListener = listener
	c.actualGRPCAddr = resolvedAddr

	c.logger.Info("ingest server started", zap.String("listen", c.GRPCAddr()))

	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			c.logger.Error("ingest server stopped", zap.Error(err))
		}
	}()

	return nil
}

func (c *Controller) stopIngest() {
	if c.grpcServer != nil {
		c.grpcServer.GracefulStop()
	}
	if c.grpcListener != nil {
		_ = c.grpcListener.Close()
	}
}

func (c *Controller) registerIngestHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/ingest/status", c.withCORS(c.handleIngestStatus))
	mux.HandleFunc("/api/v1/ingest/schema", c.withCORS(c.handleIngestSchema))
	mux.HandleFunc("/api/v1/fleet", c.withCORS(c.handleFleet))
	mux.HandleFunc("/api/v1/fleet/timeseries", c.withCORS(c.handleFleetTimeseries))
	mux.HandleFunc("/api/v1/fleet/", c.withCORS(c.handleFleetNode))
}

func (c *Controller) handleIngestStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.ingestServer == nil {
		http.Error(w, "ingest disabled", http.StatusServiceUnavailable)
		return
	}

	stats := c.ingestServer.Stats()
	nodeCount := 0
	if c.ingestStore != nil {
		nodeCount = len(c.ingestStore.Snapshot())
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"stats":       stats,
		"fleet_nodes": nodeCount,
		"timestamp":   time.Now(),
	})
}

func (c *Controller) handleIngestSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.ingestServer == nil {
		http.Error(w, "ingest disabled", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c.ingestServer.Schema())
}

func (c *Controller) handleFleet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if c.ingestStore == nil {
		json.NewEncoder(w).Encode(map[string]interface{}{"nodes": []interface{}{}})
		return
	}

	nodes := c.ingestStore.Snapshot()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"nodes":     nodes,
		"count":     len(nodes),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleFleetNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/v1/fleet/")
	if id == "" {
		http.Error(w, "collector id required", http.StatusBadRequest)
		return
	}

	if c.ingestStore == nil {
		http.Error(w, "ingest disabled", http.StatusNotFound)
		return
	}

	node := c.ingestStore.Node(id)
	if node == nil {
		http.Error(w, "collector not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(node)
}

func (c *Controller) handleFleetTimeseries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c.ingestStore == nil {
		writeFleetTrendResponse(w, fleetTrendResponse{
			GeneratedAt:    time.Now(),
			Window:         defaultFleetTrendWindow.String(),
			NumericSummary: map[string]float64{},
			Series:         []fleetTrendSeries{},
		})
		return
	}

	query := r.URL.Query()
	collectorID := strings.TrimSpace(query.Get("collector_id"))
	if collectorID == "" {
		collectorID = strings.TrimSpace(query.Get("collector"))
	}
	if collectorID == "" {
		collectorID = c.defaultFleetTrendCollector()
	}

	window := parseTrendWindow(query.Get("window"))
	limit := parseTrendLimit(query.Get("limit"))
	metricsFilter := parseTrendMetricFilter(query["metric"])

	var node *ingest.NodeSnapshot
	if collectorID != "" {
		node = c.ingestStore.Node(collectorID)
	}

	since := time.Now().Add(-window)
	samples := c.ingestStore.MetricHistory(collectorID, since, limit)
	series := buildFleetTrendSeries(samples, metricsFilter)
	latestAt, summary := buildFleetTrendSummary(samples, series)

	resp := fleetTrendResponse{
		CollectorID:    collectorID,
		Window:         window.String(),
		GeneratedAt:    time.Now(),
		LatestAt:       latestAt,
		SampleCount:    len(samples),
		NumericSummary: summary,
		Series:         series,
	}
	if node != nil {
		resp.Hostname = node.Hostname
		if resp.CollectorID == "" {
			resp.CollectorID = node.CollectorID
		}
	}

	writeFleetTrendResponse(w, resp)
}

func writeFleetTrendResponse(w http.ResponseWriter, resp fleetTrendResponse) {
	if resp.NumericSummary == nil {
		resp.NumericSummary = map[string]float64{}
	}
	if resp.Series == nil {
		resp.Series = []fleetTrendSeries{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (c *Controller) defaultFleetTrendCollector() string {
	nodes := c.ingestStore.Snapshot()
	if len(nodes) == 0 {
		return ""
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].UpdatedAt.After(nodes[j].UpdatedAt)
	})
	return nodes[0].CollectorID
}

func parseTrendWindow(raw string) time.Duration {
	if raw == "" {
		return defaultFleetTrendWindow
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultFleetTrendWindow
	}
	if d > maxFleetTrendWindow {
		return maxFleetTrendWindow
	}
	return d
}

func parseTrendLimit(raw string) int {
	if raw == "" {
		return defaultFleetTrendLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultFleetTrendLimit
	}
	if n > maxFleetTrendLimit {
		return maxFleetTrendLimit
	}
	return n
}

func parseTrendMetricFilter(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			name := strings.TrimSpace(part)
			if name != "" {
				out[name] = struct{}{}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildFleetTrendSeries(samples []ingest.MetricHistorySample, filter map[string]struct{}) []fleetTrendSeries {
	if len(samples) == 0 {
		return []fleetTrendSeries{}
	}
	out := make([]fleetTrendSeries, 0, len(fleetTrendMetricSpecs))
	for _, spec := range fleetTrendMetricSpecs {
		if len(filter) > 0 {
			if _, ok := filter[spec.Key]; !ok {
				continue
			}
		}
		points := make([]fleetTrendPoint, 0, len(samples))
		for _, sample := range samples {
			value, ok := spec.Extract(sample.Metrics)
			if !ok {
				continue
			}
			points = append(points, fleetTrendPoint{
				Timestamp: sample.Timestamp,
				Value:     value,
			})
		}
		if len(points) == 0 {
			continue
		}

		spikeCount := annotateTrendAnomalies(points)
		minValue := points[0].Value
		maxValue := points[0].Value
		total := 0.0
		for _, p := range points {
			total += p.Value
			if p.Value < minValue {
				minValue = p.Value
			}
			if p.Value > maxValue {
				maxValue = p.Value
			}
		}
		latest := points[len(points)-1].Value
		first := points[0].Value
		changePct := 0.0
		if math.Abs(first) > 1e-9 {
			changePct = (latest - first) / math.Abs(first) * 100
		} else if math.Abs(latest) > 1e-9 {
			changePct = 100
		}

		out = append(out, fleetTrendSeries{
			Key:        spec.Key,
			Display:    spec.Display,
			Unit:       spec.Unit,
			Latest:     latest,
			Min:        minValue,
			Max:        maxValue,
			Avg:        total / float64(len(points)),
			ChangePct:  changePct,
			SpikeCount: spikeCount,
			Points:     points,
		})
	}
	return out
}

func annotateTrendAnomalies(points []fleetTrendPoint) int {
	const (
		lookbackWindow = 12
		minBaseline    = 4
		zScoreTrigger  = 2.6
		jumpTrigger    = 0.35
	)

	spikes := 0
	for i := range points {
		start := i - lookbackWindow
		if start < 0 {
			start = 0
		}
		windowLen := i - start
		if windowLen < minBaseline {
			continue
		}

		sum := 0.0
		sumSq := 0.0
		for j := start; j < i; j++ {
			v := points[j].Value
			sum += v
			sumSq += v * v
		}

		mean := sum / float64(windowLen)
		variance := sumSq/float64(windowLen) - mean*mean
		if variance < 0 {
			variance = 0
		}
		stddev := math.Sqrt(variance)
		zScore := 0.0
		if stddev > 1e-9 {
			zScore = (points[i].Value - mean) / stddev
		}

		prev := points[i-1].Value
		relJump := 0.0
		if math.Abs(prev) > 1e-9 {
			relJump = (points[i].Value - prev) / math.Abs(prev)
		}

		if math.Abs(zScore) >= zScoreTrigger || math.Abs(relJump) >= jumpTrigger {
			points[i].IsAnomaly = true
			points[i].ZScore = zScore
			spikes++
		}
	}

	return spikes
}

func buildFleetTrendSummary(samples []ingest.MetricHistorySample, series []fleetTrendSeries) (time.Time, map[string]float64) {
	summary := make(map[string]float64)
	if len(series) > 0 {
		for _, s := range series {
			summary[s.Key] = s.Latest
		}
	}
	if len(samples) == 0 {
		return time.Time{}, summary
	}

	lastSample := samples[len(samples)-1]
	latestAt := lastSample.Timestamp
	metrics := lastSample.Metrics
	if metrics == nil {
		return latestAt, summary
	}

	if v, ok := metrics["node_memory_Used_bytes"]; ok {
		summary["memory_used_bytes"] = v
	}
	if v, ok := metrics["node_memory_MemTotal_bytes"]; ok {
		summary["memory_total_bytes"] = v
	}
	if v, ok := metrics["node_memory_MemAvailable_bytes"]; ok {
		summary["memory_available_bytes"] = v
	}
	rx := metricValueOr(metrics, "node_network_total_receive_bytes_per_second", "node_network_receive_bytes_per_second")
	tx := metricValueOr(metrics, "node_network_total_transmit_bytes_per_second", "node_network_transmit_bytes_per_second")
	if rx != 0 || tx != 0 {
		summary["network_total_bytes_per_second"] = rx + tx
	}
	if v, ok := metrics["node_network_utilization_peak_percent"]; ok {
		summary["network_utilization_peak_percent"] = v
	}
	if v, ok := metrics["node_network_capacity_utilization_percent"]; ok {
		summary["network_capacity_utilization_percent"] = v
	}
	if v, ok := metrics["node_tcp_retransmits_per_second"]; ok {
		summary["tcp_retransmits_per_second"] = v
	}
	if v, ok := metrics["node_tcp_retransmit_ratio"]; ok {
		summary["tcp_retransmit_ratio"] = v
	}
	if v, ok := metrics["node_softnet_dropped_per_second"]; ok {
		summary["softnet_dropped_per_second"] = v
	}
	if v, ok := metrics["node_rdma_errors_per_second"]; ok {
		summary["rdma_errors_per_second"] = v
	}
	if v, ok := metrics["node_rdma_congestion_events_per_second"]; ok {
		summary["rdma_congestion_events_per_second"] = v
	}
	if v, ok := metrics["collector_probe_core_client_available"]; ok {
		summary["probe_core_client_available"] = v
	}
	if v, ok := metrics["collector_probe_core_active"]; ok {
		summary["probe_core_active"] = v
	}
	if v, ok := metrics["collector_probe_core_fresh"]; ok {
		summary["probe_core_fresh"] = v
	}
	if v, ok := metrics["collector_probe_core_collector_selection_valid"]; ok {
		summary["probe_core_selection_valid"] = v
	}
	if v, ok := metrics["collector_probe_core_last_frame_age_seconds"]; ok {
		summary["probe_core_last_frame_age_ms"] = v * 1000.0
	}
	if v, ok := metrics["collector_probe_core_decode_errors_total"]; ok {
		summary["probe_core_decode_errors_total"] = v
	}
	if v, ok := metrics["collector_probe_core_crc_failures_total"]; ok {
		summary["probe_core_crc_failures_total"] = v
	}
	if v, ok := metrics["collector_probe_core_restarts_total"]; ok {
		summary["probe_core_restarts_total"] = v
	}
	read := metricValueOr(metrics, "node_disk_total_read_bytes_per_second", "node_disk_read_bytes_per_second")
	write := metricValueOr(metrics, "node_disk_total_written_bytes_per_second", "node_disk_written_bytes_per_second")
	if read != 0 || write != 0 {
		summary["disk_total_bytes_per_second"] = read + write
	}
	if v, ok := metrics["node_disk_total_iops_per_second"]; ok {
		summary["disk_total_iops_per_second"] = v
	}
	if v, ok := metrics["node_disk_utilization_peak_percent"]; ok {
		summary["disk_utilization_peak_percent"] = v
	}
	if v, ok := metrics["node_disk_queue_depth_total"]; ok {
		summary["disk_queue_depth_total"] = v
	}
	if v, ok := metrics["node_disk_avg_request_latency_seconds"]; ok {
		summary["disk_avg_request_latency_ms"] = v * 1000.0
	}
	if v, ok := metrics["node_disk_request_latency_p50_seconds"]; ok {
		summary["disk_request_latency_p50_ms"] = v * 1000.0
	}
	if v, ok := metrics["node_disk_request_latency_p90_seconds"]; ok {
		summary["disk_request_latency_p90_ms"] = v * 1000.0
	}
	if v, ok := metrics["node_disk_request_latency_p99_seconds"]; ok {
		summary["disk_request_latency_p99_ms"] = v * 1000.0
	}
	if v, ok := metrics["node_nvme_total_iops_per_second"]; ok {
		summary["nvme_total_iops_per_second"] = v
	}
	if v, ok := metrics["node_nvme_queue_depth_total"]; ok {
		summary["nvme_queue_depth_total"] = v
	}
	if v, ok := metrics["node_nvme_utilization_peak_percent"]; ok {
		summary["nvme_utilization_peak_percent"] = v
	}
	if v, ok := metrics["node_nvme_avg_request_latency_seconds"]; ok {
		summary["nvme_avg_request_latency_ms"] = v * 1000.0
	}
	if v, ok := metrics["node_filesystem_space_pressure_percent"]; ok {
		summary["filesystem_space_pressure_percent"] = v
	}
	if v, ok := metrics["node_filesystem_inode_pressure_percent"]; ok {
		summary["filesystem_inode_pressure_percent"] = v
	}
	if v, ok := metrics["node_memory_Dirty_bytes"]; ok {
		summary["pagecache_dirty_bytes"] = v
	}
	if v, ok := metrics["node_memory_Writeback_bytes"]; ok {
		summary["pagecache_writeback_bytes"] = v
	}
	if v, ok := metrics["node_vmstat_pgpgin_per_second"]; ok {
		summary["vm_pgpgin_per_second"] = v
	}
	if v, ok := metrics["node_vmstat_pgpgout_per_second"]; ok {
		summary["vm_pgpgout_per_second"] = v
	}
	if v, ok := metrics["node_vmstat_nr_dirtied_per_second"]; ok {
		summary["vm_dirtied_pages_per_second"] = v
	}
	if v, ok := metrics["node_vmstat_nr_written_per_second"]; ok {
		summary["vm_written_pages_per_second"] = v
	}
	if v, ok := metrics["node_pressure_io_some_avg10"]; ok {
		summary["io_pressure_some_avg10"] = v
	}
	if v, ok := metrics["node_pressure_io_full_avg10"]; ok {
		summary["io_pressure_full_avg10"] = v
	}
	if v, ok := metrics["node_load1"]; ok {
		summary["load1"] = v
	}
	if v, ok := metrics["node_cpu_iowait_percent"]; ok {
		summary["cpu_iowait_percent"] = v
	}
	if v, ok := metrics["node_pressure_cpu_some_avg10"]; ok {
		summary["cpu_pressure_some_avg10"] = v
	}
	if v, ok := metrics["node_procs_running"]; ok {
		summary["procs_running"] = v
	}
	if v, ok := metrics["node_procs_blocked"]; ok {
		summary["procs_blocked"] = v
	}
	if v, ok := metrics["node_numa_locality_ratio_percent"]; ok {
		summary["numa_locality_ratio_percent"] = v
	}
	if v, ok := metrics["node_numa_miss_ratio_percent"]; ok {
		summary["numa_miss_ratio_percent"] = v
	}

	return latestAt, summary
}

func directMetricExtractor(name string) func(map[string]float64) (float64, bool) {
	return func(metrics map[string]float64) (float64, bool) {
		if metrics == nil {
			return 0, false
		}
		v, ok := metrics[name]
		return v, ok
	}
}

func firstMetricExtractor(names ...string) func(map[string]float64) (float64, bool) {
	return func(metrics map[string]float64) (float64, bool) {
		if metrics == nil {
			return 0, false
		}
		for _, name := range names {
			if v, ok := metrics[name]; ok {
				return v, true
			}
		}
		return 0, false
	}
}

func metricWithScaleExtractor(name string, scale float64) func(map[string]float64) (float64, bool) {
	return func(metrics map[string]float64) (float64, bool) {
		if metrics == nil {
			return 0, false
		}
		v, ok := metrics[name]
		if !ok {
			return 0, false
		}
		return v * scale, true
	}
}

func memoryUsagePercentExtractor(metrics map[string]float64) (float64, bool) {
	if metrics == nil {
		return 0, false
	}
	total, ok := metrics["node_memory_MemTotal_bytes"]
	if !ok || total <= 0 {
		return 0, false
	}
	if used, ok := metrics["node_memory_Used_bytes"]; ok {
		return clampPercent(used / total * 100), true
	}
	if available, ok := metrics["node_memory_MemAvailable_bytes"]; ok {
		used := total - available
		if used < 0 {
			used = 0
		}
		return clampPercent(used / total * 100), true
	}
	return 0, false
}

func fdUsagePercentExtractor(metrics map[string]float64) (float64, bool) {
	if metrics == nil {
		return 0, false
	}
	allocated, ok := metrics["node_filefd_allocated"]
	if !ok {
		return 0, false
	}
	maximum, ok := metrics["node_filefd_maximum"]
	if !ok || maximum <= 0 {
		return 0, false
	}
	return clampPercent(allocated / maximum * 100), true
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

func metricValueOr(metrics map[string]float64, names ...string) float64 {
	for _, name := range names {
		if v, ok := metrics[name]; ok {
			return v
		}
	}
	return 0
}
