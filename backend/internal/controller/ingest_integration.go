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
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/signalinsights"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/timeseries"
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
	Tier       string            `json:"tier,omitempty"`
	Latest     float64           `json:"latest"`
	Min        float64           `json:"min"`
	Max        float64           `json:"max"`
	Avg        float64           `json:"avg"`
	ChangePct  float64           `json:"change_pct"`
	SpikeCount int               `json:"spike_count"`
	Trend      string            `json:"trend,omitempty"`
	Pattern    string            `json:"pattern,omitempty"`
	Sustained  bool              `json:"sustained,omitempty"`
	Hint       string            `json:"operational_hint,omitempty"`
	Points     []fleetTrendPoint `json:"points"`
}

type fleetOperationalInsight struct {
	Key      string   `json:"key"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Decision string   `json:"decision"`
	Evidence []string `json:"evidence,omitempty"`
}

type fleetTrendResponse struct {
	CollectorID         string                    `json:"collector_id"`
	Hostname            string                    `json:"hostname"`
	Window              string                    `json:"window"`
	GeneratedAt         time.Time                 `json:"generated_at"`
	LatestAt            time.Time                 `json:"latest_at,omitempty"`
	SampleCount         int                       `json:"sample_count"`
	NumericSummary      map[string]float64        `json:"numeric_summary"`
	Series              []fleetTrendSeries        `json:"series"`
	TelemetryQuality    telemetryQuality          `json:"telemetry_quality"`
	OperationalInsights []fleetOperationalInsight `json:"operational_insights"`
}

type fleetTrendMetricSpec struct {
	Key     string
	Display string
	Unit    string
	Extract func(map[string]float64) (float64, bool)
}

func (c *Controller) grpcIngestWritesGuarded() bool {
	return c != nil && c.ingestServer != nil && c.ingestServer.WriteGuardEnabled()
}

func (c *Controller) grpcIngestWritesBlocked() bool {
	return c.grpcIngestWritesGuarded() && !c.haState().Active
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
		Key:     "gpu_process_total",
		Display: "GPU Active Processes",
		Unit:    "count",
		Extract: directMetricExtractor("node_gpu_process_total"),
	},
	{
		Key:     "gpu_memory_used_mib",
		Display: "GPU Memory Used",
		Unit:    "mib",
		Extract: directMetricExtractor("node_gpu_memory_used_total_mib"),
	},
	{
		Key:     "security_findings_total",
		Display: "Security Findings",
		Unit:    "count",
		Extract: directMetricExtractor("node_security_findings_total"),
	},
}

func (c *Controller) initIngest() error {
	if c.ingestStore != nil {
		return nil
	}
	c.ingestStore = ingest.NewMemoryStoreWithConfig(c.config.Ingest.storeConfig(), c.logger)
	c.metricHistory = c.ingestStore
	c.ingestStore.StartPersistence()
	c.logIndex = logindex.NewIndex(logindex.DefaultConfig())
	c.ingestStore.AttachLogIndex(c.logIndex)
	tsdbCfg := timeseries.ConfigFromEnv(c.config.TSDB)
	tsdbService, err := timeseries.NewService(tsdbCfg, c.ingestStore, c.logger)
	if err != nil {
		return err
	}
	c.timeseriesService = tsdbService
	if c.timeseriesService != nil {
		c.metricHistory = c.timeseriesService
	}

	if c.config.GPU.Enabled {
		c.gpuStore = gpuobs.New(c.config.GPU)
	}
	c.rebuildIngestServer()
	return nil
}

func (c *Controller) rebuildIngestServer() {
	if c.ingestStore == nil {
		return
	}
	processors := make([]ingest.Processor, 0, 3)
	if c.analysisExt != nil && c.analysisExt.engine != nil {
		if processor := newAnalysisIngestProcessor(c.analysisExt.engine); processor != nil {
			processors = append(processors, processor)
		}
	}
	if c.timeseriesService != nil {
		processors = append(processors, c.timeseriesService)
	}
	if c.gpuStore != nil {
		processors = append(processors, c.gpuStore)
	}
	c.ingestServer = ingest.NewServer(c.ingestStore, c.logger, processors...)
	c.ingestServer.SetWriteGuard(func() error {
		return c.activeControllerWriteError("gRPC ingest writes")
	})
	c.ingestServer.SetAuthenticator(c.authenticateIngestStream)
	c.ingestServer.SetAccessPolicy(c.ingestAccessPolicy())
}

func (c *Controller) startIngest() error {
	if c.ingestServer == nil {
		if err := c.initIngest(); err != nil {
			return err
		}
	}
	if c.grpcServer != nil || c.grpcListener != nil {
		return nil
	}

	if c.config.GRPCListenAddr == "" {
		c.logger.Info("ingest server disabled (grpc_listen empty)")
		return nil
	}

	listener, resolvedAddr, err := listenWithFallback(c.config.GRPCListenAddr, c.logger, "grpc")
	if err != nil {
		return err
	}

	serverOpts := make([]grpc.ServerOption, 0, 1)
	if creds, err := loadIngestServerTransportCredentials(c.config.Ingest.Transport); err != nil {
		_ = listener.Close()
		return err
	} else if creds != nil {
		serverOpts = append(serverOpts, grpc.Creds(creds))
	}

	grpcServer := grpc.NewServer(serverOpts...)
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
		c.grpcServer = nil
	}
	if c.grpcListener != nil {
		_ = c.grpcListener.Close()
		c.grpcListener = nil
	}
	c.actualGRPCAddr = ""
}

func (c *Controller) registerIngestHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/ingest/status", c.withCORS(c.handleIngestStatus))
	mux.HandleFunc("/api/v1/ingest/schema", c.withCORS(c.handleIngestSchema))
	mux.HandleFunc("/api/v1/storage/status", c.withCORS(c.handleStorageStatus))
	mux.HandleFunc("/api/v1/storage/retention", c.withCORS(c.handleStorageRetention))
	mux.HandleFunc("/api/v1/finops/signals", c.withCORS(c.handleFinOpsSignals))
	mux.HandleFunc("/api/v1/fleet", c.withCORS(c.handleFleet))
	mux.HandleFunc("/api/v1/fleet/timeseries", c.withCORS(c.handleFleetTimeseries))
	mux.HandleFunc("/api/v1/fleet/", c.withCORS(c.handleFleetNode))
	mux.HandleFunc("/api/v1/logs/status", c.withCORS(c.handleLogsStatus))
	mux.HandleFunc("/api/v1/logs/search", c.withCORS(c.handleLogsSearch))
	mux.HandleFunc("/api/v1/logs/ingest", c.withCORS(c.handleLogsIngest))
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
	resp := map[string]interface{}{
		"stats":       stats,
		"fleet_nodes": nodeCount,
		"timestamp":   time.Now(),
		"auth": map[string]any{
			"enabled":                 c.auth.Enabled && c.auth.IngestAuthEnabled,
			"mode":                    "bearer_token",
			"audience":                c.auth.IngestTokenAudience,
			"anonymous_allowed":       !c.auth.Enabled || !c.auth.IngestAuthEnabled,
			"authentication_failures": stats.AuthnRejectedTotal,
			"authorization_failures":  stats.AuthzRejectedTotal,
			"last_auth_subject":       stats.LastAuthSubject,
		},
		"transport": c.ingestTransportStatus(),
		"ha": map[string]interface{}{
			"enabled":                    c.haState().Enabled,
			"backend":                    c.haState().Backend,
			"mode":                       c.haState().Mode,
			"role":                       c.haState().Role,
			"active":                     c.haState().Active,
			"read_only":                  c.haState().ReadOnly,
			"leader_id":                  c.haState().LeaderID,
			"leader_grpc":                c.haState().LeaderGRPC,
			"allow_follower_read":        c.haState().AllowFollowerRead,
			"grpc_ingest_writes_guarded": c.grpcIngestWritesGuarded(),
			"grpc_ingest_writes_blocked": c.grpcIngestWritesBlocked(),
		},
	}
	if c.ingestStore != nil {
		resp["store"] = c.ingestStore.Stats()
	}
	if c.logIndex != nil {
		resp["logs"] = c.logIndex.Stats()
	}
	_ = json.NewEncoder(w).Encode(resp)
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

	nodes := []interface{}{}
	if c.ingestStore != nil {
		snapshot := c.ingestStore.Snapshot()
		nodes = make([]interface{}, 0, len(snapshot))
		for _, node := range snapshot {
			nodes = append(nodes, node)
		}
	}
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
		now := time.Now().UTC()
		writeFleetTrendResponse(w, fleetTrendResponse{
			GeneratedAt:    now,
			Window:         defaultFleetTrendWindow.String(),
			NumericSummary: map[string]float64{},
			Series:         []fleetTrendSeries{},
			TelemetryQuality: telemetryQuality{
				State:       telemetryStateUnavailable,
				QueryAt:     now,
				QualityHint: "ingest disabled; no telemetry is currently available",
			},
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

	queryAt := time.Now().UTC()
	since := queryAt.Add(-window)
	samples := c.metricHistorySamples(collectorID, since, limit)
	series := buildFleetTrendSeries(samples, metricsFilter)
	latestAt, summary := buildFleetTrendSummary(samples, series)
	quality := buildFleetTelemetryQuality(node, samples, series, summary, metricsFilter, queryAt)

	resp := fleetTrendResponse{
		CollectorID:         collectorID,
		Window:              window.String(),
		GeneratedAt:         queryAt,
		LatestAt:            latestAt,
		SampleCount:         len(samples),
		NumericSummary:      summary,
		Series:              series,
		TelemetryQuality:    quality,
		OperationalInsights: buildFleetOperationalInsights(summary, series),
	}
	if node != nil {
		resp.Hostname = node.Hostname
		if resp.CollectorID == "" {
			resp.CollectorID = node.CollectorID
		}
	}

	writeFleetTrendResponse(w, resp)
}

func (c *Controller) metricHistorySamples(collectorID string, since time.Time, limit int) []ingest.MetricHistorySample {
	if c.metricHistory != nil {
		return c.metricHistory.MetricHistory(collectorID, since, limit)
	}
	if c.ingestStore != nil {
		return c.ingestStore.MetricHistory(collectorID, since, limit)
	}
	return []ingest.MetricHistorySample{}
}

func writeFleetTrendResponse(w http.ResponseWriter, resp fleetTrendResponse) {
	if resp.NumericSummary == nil {
		resp.NumericSummary = map[string]float64{}
	}
	if resp.Series == nil {
		resp.Series = []fleetTrendSeries{}
	}
	if resp.OperationalInsights == nil {
		resp.OperationalInsights = []fleetOperationalInsight{}
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
		values := make([]float64, 0, len(points))
		for _, p := range points {
			total += p.Value
			values = append(values, p.Value)
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
		avg := total / float64(len(points))
		profile := signalinsights.ProfileFromValues(values, spikeCount)

		out = append(out, fleetTrendSeries{
			Key:        spec.Key,
			Display:    spec.Display,
			Unit:       spec.Unit,
			Tier:       signalinsights.TierForMetric(spec.Key),
			Latest:     latest,
			Min:        minValue,
			Max:        maxValue,
			Avg:        avg,
			ChangePct:  changePct,
			SpikeCount: spikeCount,
			Trend:      signalinsights.Summary(profile),
			Pattern:    profile.Pattern,
			Sustained:  profile.Sustained,
			Hint:       fleetSeriesOperationalHint(spec.Key, latest, avg, profile),
			Points:     points,
		})
	}
	return out
}

func fleetSeriesOperationalHint(key string, latest, avg float64, profile signalinsights.Profile) string {
	switch key {
	case "cpu_usage_percent":
		if latest >= 85 && profile.Direction == "rising" {
			return "Sustained CPU pressure reduces scheduler headroom and usually reflects an upstream resource bottleneck, not just busy compute."
		}
	case "cpu_iowait_percent":
		if latest >= 10 || profile.Direction == "rising" {
			return "Rising iowait means CPU time is being lost to storage stalls rather than productive work."
		}
	case "memory_used_percent":
		if latest >= 80 && profile.Direction == "rising" {
			return "Shrinking memory headroom is an early warning for reclaim storms, leak growth, or retry amplification."
		}
	case "disk_request_latency_p99_ms", "disk_avg_request_latency_ms", "disk_queue_depth_total", "io_pressure_full_avg10":
		if latest > avg || profile.Direction == "rising" {
			return "Storage queueing and tail latency usually show up before throughput collapses, so rising values here are operationally meaningful."
		}
	case "tcp_retransmit_ratio", "tcp_retransmits_per_second", "softnet_dropped_per_second":
		if latest > 0 || profile.Pattern == "bursty" {
			return "Retransmits and drops point to congestion or packet loss rather than application-only latency."
		}
	case "gpu_utilization_percent":
		if latest > 0 && latest < 35 {
			return "Low GPU utilization with active GPU work usually means the feeder path is starved by CPU, storage, or network contention."
		}
	case "probe_core_active", "probe_core_last_frame_age_ms":
		if (key == "probe_core_active" && latest < 1) || (key == "probe_core_last_frame_age_ms" && latest >= 5000) {
			return "Telemetry integrity is degraded; validate probe-core freshness before treating blank charts as healthy data."
		}
	case "security_findings_total":
		if latest > 0 {
			return "Security findings should be correlated with performance anomalies before treating the load pattern as benign."
		}
	}

	switch {
	case profile.Pattern == "oscillating":
		return "Oscillating behavior usually means retries, flapping dependencies, or competing schedulers rather than a single sustained bottleneck."
	case profile.Pattern == "bursty":
		return "Bursty behavior is often transient, but repeated bursts usually justify correlation against logs, scheduling, and deployment timing."
	case profile.Sustained && profile.Direction == "rising":
		return "Sustained upward drift is more likely to matter operationally than a single spike because it consumes headroom over time."
	default:
		return ""
	}
}

func buildFleetOperationalInsights(summary map[string]float64, series []fleetTrendSeries) []fleetOperationalInsight {
	seriesByKey := make(map[string]fleetTrendSeries, len(series))
	for _, item := range series {
		seriesByKey[item.Key] = item
	}

	insights := make([]fleetOperationalInsight, 0, 6)
	addInsight := func(key, severity, summaryText, decision string, evidence ...string) {
		insights = append(insights, fleetOperationalInsight{
			Key:      key,
			Severity: severity,
			Summary:  summaryText,
			Decision: decision,
			Evidence: evidence,
		})
	}

	diskLatency := summary["disk_request_latency_p99_ms"]
	diskQueue := summary["disk_queue_depth_total"]
	ioPressure := summary["io_pressure_full_avg10"]
	cpuIOWait := summary["cpu_iowait_percent"]
	cpuSeries := seriesByKey["cpu_usage_percent"]
	diskLatencySeries := seriesByKey["disk_request_latency_p99_ms"]
	retransmits := summary["tcp_retransmits_per_second"]
	retransRatio := summary["tcp_retransmit_ratio"]
	networkSeries := seriesByKey["tcp_retransmit_ratio"]
	memorySeries := seriesByKey["memory_used_percent"]
	gpuSeries := seriesByKey["gpu_utilization_percent"]
	securitySeries := seriesByKey["security_findings_total"]
	probeCoreAge := summary["probe_core_last_frame_age_ms"]
	probeCoreActive := summary["probe_core_active"]

	if (cpuIOWait >= 10 || cpuSeries.Trend == "sustained rise") && (diskLatency >= 40 || diskQueue >= 8 || ioPressure >= 10 || diskLatencySeries.Trend == "sustained rise") {
		addInsight(
			"storage_bottleneck_risk",
			"warning",
			"CPU wait and disk latency are rising together, which usually means the workload is blocked on storage rather than raw compute.",
			"Inspect the hottest device and partition, then verify which process is causing queue growth before scaling CPU.",
			"cpu_iowait + disk latency coupling",
			"queue depth / IO pressure elevated",
		)
	}

	if gpuSeries.Latest > 0 && gpuSeries.Latest < 35 && (cpuSeries.Trend == "sustained rise" || diskLatencySeries.Trend == "sustained rise" || retransRatio >= 0.02 || retransmits >= 0.5) {
		addInsight(
			"gpu_starvation_risk",
			"warning",
			"GPU utilization is low while host-side pressure is increasing, which points to feeder starvation or placement issues.",
			"Inspect data-loader, checkpoint, storage, and network paths before treating this as a GPU-capacity problem.",
			"low GPU utilization",
			"host contention or transport pressure rising",
		)
	}

	if retransRatio >= 0.02 || retransmits >= 0.5 || networkSeries.Pattern == "bursty" || networkSeries.Trend == "sustained rise" {
		addInsight(
			"network_congestion_risk",
			"warning",
			"Retransmits or drops are active, which makes latency and timeout symptoms more likely to be network-bound than application-only.",
			"Inspect link errors, MTU consistency, noisy east-west traffic, and retry bursts before tuning application timeouts.",
			"retransmit growth or burstiness",
			"possible packet loss / congestion path",
		)
	}

	if memorySeries.Latest >= 80 && (memorySeries.Trend == "sustained rise" || memorySeries.Pattern == "bursty") {
		addInsight(
			"capacity_exhaustion_risk",
			"advisory",
			"Memory headroom is shrinking over time, which is more consistent with a structural exhaustion risk than a one-off spike.",
			"Inspect top memory consumers and error logs now, before reclaim, swap, or OOM turns a slow burn into an outage.",
			"memory growth is sustained",
		)
	}

	if securitySeries.Latest > 0 {
		addInsight(
			"security_correlation_required",
			"advisory",
			"Security findings are active in the same window, so performance symptoms should be correlated with process and network evidence before assuming benign load.",
			"Open the Security dashboard and cross-check suspicious process, port, or outbound findings with the current incident timeline.",
			"security findings active",
		)
	}

	if probeCoreActive == 0 || probeCoreAge >= 5000 {
		addInsight(
			"telemetry_integrity_risk",
			"critical",
			"Probe-core freshness is degraded, so missing or flat host metrics may reflect telemetry loss instead of a healthy system.",
			"Validate collector runtime mode, probe-core health, and fallback coverage before trusting empty charts or zeros.",
			"probe-core inactive or stale",
		)
	}

	if len(insights) > 5 {
		insights = insights[:5]
	}
	return insights
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

	if v, ok := metricValueWithAliases(metrics, "node_memory_Used_bytes", "node_memory_used_bytes"); ok {
		summary["memory_used_bytes"] = v
	}
	if v, ok := metricValueWithAliases(metrics, "node_memory_MemTotal_bytes", "node_memory_total_bytes"); ok {
		summary["memory_total_bytes"] = v
	}
	if v, ok := metricValueWithAliases(metrics, "node_memory_MemAvailable_bytes", "node_memory_available_bytes"); ok {
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
	if v, ok := metrics["node_gpu_utilization_sm_avg_percent"]; ok {
		summary["gpu_utilization_percent"] = v
	}
	if v, ok := metrics["node_gpu_memory_used_total_mib"]; ok {
		summary["gpu_memory_used_mib"] = v
	}
	if v, ok := metrics["node_gpu_process_total"]; ok {
		summary["gpu_process_total"] = v
	}
	if v, ok := metrics["node_security_findings_total"]; ok {
		summary["security_findings_total"] = v
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
	total, ok := metricValueWithAliases(metrics, "node_memory_MemTotal_bytes", "node_memory_total_bytes")
	if !ok || total <= 0 {
		return 0, false
	}
	if used, ok := metricValueWithAliases(metrics, "node_memory_Used_bytes", "node_memory_used_bytes"); ok {
		return clampPercent(used / total * 100), true
	}
	if available, ok := metricValueWithAliases(metrics, "node_memory_MemAvailable_bytes", "node_memory_available_bytes"); ok {
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

func metricValueWithAliases(metrics map[string]float64, names ...string) (float64, bool) {
	for _, name := range names {
		if v, ok := metrics[name]; ok {
			return v, true
		}
	}
	return 0, false
}
