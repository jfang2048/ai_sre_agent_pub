package core

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/metrics/prom"
	"github.com/jfang2048/ai_sre_agent_pub/internal/monitoring"
	"github.com/jfang2048/ai_sre_agent_pub/internal/monitoring/sources"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/config"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/native"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/utils"
	"go.uber.org/zap"
)

// Agent is the main observability agent
type Agent struct {
	config *config.Config
	logger *zap.Logger

	// Components
	collector  *monitoring.Collector
	sliTracker *monitoring.SLITracker
	sloTracker *monitoring.SLOTracker
	sreManager *SREManager

	// State
	stateMachine *Machine
	mu           sync.RWMutex

	// Lifecycle
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool

	// Servers
	apiServer     *http.Server
	metricsServer *http.Server

	latestMetrics []sources.Metric
	latestIndex   map[string]int
	latestValues  map[string]float64
	metricCh      chan sources.Metric
}

var labelKeyPool = sync.Pool{New: func() any { return make([]string, 0, 8) }}

// NewAgent creates a new observability agent
func NewAgent(cfg *config.Config, logger *zap.Logger) (*Agent, error) {
	if logger == nil {
		logger = utils.GetLogger()
	}

	// Create logger for agent
	agentLogger := logger.With(zap.String("component", "agent"))

	a := &Agent{
		config:       cfg,
		logger:       agentLogger,
		stateMachine: NewMachine(agentLogger),
		running:      false,
		metricCh:     make(chan sources.Metric, 100),
		latestIndex:  make(map[string]int),
		latestValues: make(map[string]float64),
	}

	// Initialize collector
	monitoringConfig, err := a.buildMonitoringConfig()
	if err != nil {
		return nil, utils.Wrap(err, utils.ErrCodeInitFailed, "failed to build monitoring config")
	}

	a.collector, err = monitoring.NewCollector(monitoringConfig, logger)
	if err != nil {
		return nil, utils.Wrap(err, utils.ErrCodeInitFailed, "failed to create collector")
	}

	a.collector.SetMetricChannel(a.metricCh)

	// Register sources
	if monitoringConfig.Proc.Enabled {
		a.collector.RegisterSource(sources.NewProcSource(monitoringConfig.Proc))
	}

	if monitoringConfig.EBPF.Enabled {
		ebpfSrc, err := sources.NewEBPFSource(monitoringConfig.EBPF, logger)
		if err == nil {
			a.collector.RegisterSource(ebpfSrc)
		} else {
			logger.Warn("Failed to initialize eBPF source", zap.Error(err))
		}
	}

	if monitoringConfig.Process.Enabled {
		processSrc, err := sources.NewProcessSource(monitoringConfig.Process, logger)
		if err == nil {
			a.collector.RegisterSource(processSrc)
		} else {
			logger.Warn("Failed to initialize process source", zap.Error(err))
		}
	}

	// Register native C++ source
	a.collector.RegisterSource(native.NewNativeSource())

	// Register hardware source (always available on Linux)
	hardwareConfig := sources.HardwareConfig{
		Enabled:         utils.GetBool(a.config.Monitoring, "hardware.enabled", true),
		IncludeCPUInfo:  utils.GetBool(a.config.Monitoring, "hardware.include_cpu_info", true),
		IncludeMemInfo:  utils.GetBool(a.config.Monitoring, "hardware.include_mem_info", true),
		IncludeDiskInfo: utils.GetBool(a.config.Monitoring, "hardware.include_disk_info", true),
		IncludeNetwork:  utils.GetBool(a.config.Monitoring, "hardware.include_network", true),
		IncludeSmart:    utils.GetBool(a.config.Monitoring, "hardware.include_smart", false),
	}
	if hardwareSource, err := sources.NewHardwareSource(hardwareConfig); err == nil {
		a.collector.RegisterSource(hardwareSource)
	} else {
		logger.Debug("Hardware source not available", zap.Error(err))
	}

	// Register GPU source (if nvidia-smi is available)
	gpuConfig := sources.GPUConfig{
		Enabled:       utils.GetBool(a.config.Monitoring, "gpu.enabled", true),
		IncludeNVIDIA: utils.GetBool(a.config.Monitoring, "gpu.include_nvidia", true),
		IncludeAMD:    utils.GetBool(a.config.Monitoring, "gpu.include_amd", true),
		IncludeIntel:  utils.GetBool(a.config.Monitoring, "gpu.include_intel", true),
		CollectClocks: utils.GetBool(a.config.Monitoring, "gpu.collect_clocks", true),
		CollectPower:  utils.GetBool(a.config.Monitoring, "gpu.collect_power", true),
		CollectTemp:   utils.GetBool(a.config.Monitoring, "gpu.collect_temp", true),
		CollectPcie:   utils.GetBool(a.config.Monitoring, "gpu.collect_pcie", true),
	}
	if gpuSource, err := sources.NewGPUSource(gpuConfig, logger); err == nil {
		a.collector.RegisterSource(gpuSource)
	} else {
		logger.Debug("GPU source not available", zap.Error(err))
	}

	// Register Kubernetes source (if in cluster)
	k8sConfig := sources.KubernetesConfig{
		Enabled:        utils.GetBool(a.config.Monitoring, "kubernetes.enabled", true),
		InCluster:      utils.GetBool(a.config.Monitoring, "kubernetes.in_cluster", false),
		IncludePods:    utils.GetBool(a.config.Monitoring, "kubernetes.include_pods", true),
		IncludeNodes:   utils.GetBool(a.config.Monitoring, "kubernetes.include_nodes", true),
		IncludePV:      utils.GetBool(a.config.Monitoring, "kubernetes.include_pv", true),
		KubeconfigPath: utils.GetString(a.config.Monitoring, "kubernetes.kubeconfig_path", ""),
	}
	if k8sSource, err := sources.NewKubernetesSource(k8sConfig, logger); err == nil {
		a.collector.RegisterSource(k8sSource)
	} else {
		logger.Debug("Kubernetes source not available", zap.Error(err))
	}

	// Initialize Monitoring Trackers
	sliConfig := monitoring.SLIConfig{
		RollingWindow:   monitoringConfig.SLIConfig.RollingWindow,
		RetentionPeriod: monitoringConfig.SLIConfig.RetentionPeriod,
	}
	a.sliTracker = monitoring.NewSLITracker(sliConfig, logger)

	sloConfig := monitoring.SLOConfig{
		ConfigPath:                 monitoringConfig.SLOConfig.ConfigPath,
		EvaluationInterval:         monitoringConfig.SLOConfig.EvaluationInterval,
		ErrorBudgetWarningPercent:  monitoringConfig.SLOConfig.ErrorBudgetWarningPercent,
		ErrorBudgetCriticalPercent: monitoringConfig.SLOConfig.ErrorBudgetCriticalPercent,
	}
	a.sloTracker = monitoring.NewSLOTracker(sloConfig, logger, a.sliTracker)

	// Initialize SRE Manager
	a.sreManager = NewSREManager(logger)

	return a, nil
}

// RunOnce performs a single collection and analysis cycle
func (a *Agent) RunOnce(ctx context.Context) ([]sources.Metric, error) {
	metrics, err := a.collector.CollectOnce(ctx)
	if err != nil {
		return nil, err
	}

	// Update latest metrics
	a.setLatestMetrics(metrics)

	return metrics, nil
}

// Start starts the agent
func (a *Agent) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.running {
		return fmt.Errorf("agent already running")
	}

	ctx, a.cancel = context.WithCancel(ctx)
	a.ctx = ctx

	// Transition to starting state
	a.stateMachine.Transition(StateStarting)

	a.logger.Info("starting observability agent")

	// Start collector
	if err := a.collector.Start(ctx); err != nil {
		a.stateMachine.Transition(StateFailed)
		return utils.Wrap(err, utils.ErrCodeStartFailed, "failed to start collector")
	}

	// Start SLO tracker
	if a.sloTracker != nil {
		if err := a.sloTracker.Start(ctx); err != nil {
			a.logger.Warn("failed to start SLO tracker", zap.Error(err))
		}
	}

	// Start SRE Manager
	if a.sreManager != nil {
		if err := a.sreManager.Start(ctx); err != nil {
			a.logger.Warn("failed to start SRE manager", zap.Error(err))
		}
	}

	// Start health check loop
	a.wg.Add(1)
	go a.healthCheckLoop(ctx)

	// Start status reporting loop
	a.wg.Add(1)
	go a.statusLoop(ctx)

	// Start metric listener loop
	a.wg.Add(1)
	go a.metricListenerLoop(ctx)

	// Start Metrics server
	if a.config.Server.MetricsPort > 0 {
		a.startMetricsServer()
	}

	// Start API server
	if a.config.Server.Port > 0 {
		a.startAPIServer()
	}

	// Transition to running state
	a.stateMachine.Transition(StateRunning)
	a.running = true

	a.logger.Info("observability agent started",
		zap.String("version", Version()),
		zap.String("commit", Commit()))

	return nil
}

// Stop stops the agent
func (a *Agent) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}

	a.logger.Info("stopping observability agent")

	// Transition to stopping state
	a.stateMachine.Transition(StateStopping)

	// Cancel context
	if a.cancel != nil {
		a.cancel()
	}

	// Wait for goroutines
	a.wg.Wait()

	// Stop SLO tracker
	if a.sloTracker != nil {
		if err := a.sloTracker.Stop(); err != nil {
			a.logger.Error("error stopping SLO tracker", zap.Error(err))
		}
	}

	// Stop metrics server
	if a.metricsServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.metricsServer.Shutdown(ctx)
	}

	// Stop API server
	if a.apiServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.apiServer.Shutdown(ctx)
	}

	// Stop collector
	if err := a.collector.Stop(); err != nil {
		a.logger.Error("error stopping collector", zap.Error(err))
	}

	// Transition to stopped state
	a.stateMachine.Transition(StateStopped)
	a.running = false

	a.logger.Info("observability agent stopped")
	return nil
}

// Running returns whether the agent is running
func (a *Agent) Running() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.running
}

// State returns the current agent state
func (a *Agent) State() State {
	return a.stateMachine.Current()
}

// Status returns the agent status
func (a *Agent) Status() *Status {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return &Status{
		Version:   Version(),
		Commit:    Commit(),
		State:     a.stateMachine.Current().String(),
		Uptime:    a.uptime(),
		StartedAt: a.stateMachine.StartedAt(),
		Collector: a.collector.Status(),
	}
}

// buildMonitoringConfig builds the monitoring config from agent config
func (a *Agent) buildMonitoringConfig() (*monitoring.Config, error) {
	cfg := &monitoring.Config{
		ScrapeInterval:    utils.GetDuration(a.config.Monitoring, "scrape_interval", 30*time.Second),
		AggregateInterval: utils.GetDuration(a.config.Monitoring, "aggregate_interval", 1*time.Minute),
		EBPF: sources.EBPFConfig{
			Enabled:      utils.GetBool(a.config.Monitoring, "ebpf.enabled", false),
			ProgramsPath: utils.GetString(a.config.Monitoring, "ebpf.programs_path", "/etc/sre-collector/bpf"),
			Syscalls:     utils.GetBool(a.config.Monitoring, "ebpf.syscalls", true),
			Network:      utils.GetBool(a.config.Monitoring, "ebpf.network", true),
			Process:      utils.GetBool(a.config.Monitoring, "ebpf.process", true),
			IO:           utils.GetBool(a.config.Monitoring, "ebpf.io", true),
		},
		Process: sources.ProcessConfig{
			Enabled:          utils.GetBool(a.config.Monitoring, "process.enabled", true),
			ScanInterval:     utils.GetDuration(a.config.Monitoring, "process.scan_interval", 15*time.Second),
			EnablePerProcess: utils.GetBool(a.config.Monitoring, "process.enable_per_process", true),
			EnableOpenFiles:  utils.GetBool(a.config.Monitoring, "process.enable_open_files", true),
			EnableIO:         utils.GetBool(a.config.Monitoring, "process.enable_io", true),
			TopNProcesses:    utils.GetInt(a.config.Monitoring, "process.top_n", 50),
		},
		Proc: sources.ProcConfig{
			Enabled: utils.GetBool(a.config.Monitoring, "proc.enabled", true),
		},
		Hardware: sources.HardwareConfig{
			Enabled:         utils.GetBool(a.config.Monitoring, "hardware.enabled", true),
			IncludeCPUInfo:  utils.GetBool(a.config.Monitoring, "hardware.include_cpu_info", true),
			IncludeMemInfo:  utils.GetBool(a.config.Monitoring, "hardware.include_mem_info", true),
			IncludeDiskInfo: utils.GetBool(a.config.Monitoring, "hardware.include_disk_info", true),
			IncludeNetwork:  utils.GetBool(a.config.Monitoring, "hardware.include_network", true),
			IncludeSmart:    utils.GetBool(a.config.Monitoring, "hardware.include_smart", false),
		},
		GPU: sources.GPUConfig{
			Enabled:       utils.GetBool(a.config.Monitoring, "gpu.enabled", true),
			IncludeNVIDIA: utils.GetBool(a.config.Monitoring, "gpu.include_nvidia", true),
			IncludeAMD:    utils.GetBool(a.config.Monitoring, "gpu.include_amd", true),
			IncludeIntel:  utils.GetBool(a.config.Monitoring, "gpu.include_intel", true),
			CollectClocks: utils.GetBool(a.config.Monitoring, "gpu.collect_clocks", true),
			CollectPower:  utils.GetBool(a.config.Monitoring, "gpu.collect_power", true),
			CollectTemp:   utils.GetBool(a.config.Monitoring, "gpu.collect_temp", true),
			CollectPcie:   utils.GetBool(a.config.Monitoring, "gpu.collect_pcie", true),
		},
		Kubernetes: sources.KubernetesConfig{
			Enabled:        utils.GetBool(a.config.Monitoring, "kubernetes.enabled", true),
			InCluster:      utils.GetBool(a.config.Monitoring, "kubernetes.in_cluster", false),
			IncludePods:    utils.GetBool(a.config.Monitoring, "kubernetes.include_pods", true),
			IncludeNodes:   utils.GetBool(a.config.Monitoring, "kubernetes.include_nodes", true),
			IncludePV:      utils.GetBool(a.config.Monitoring, "kubernetes.include_pv", true),
			KubeconfigPath: utils.GetString(a.config.Monitoring, "kubernetes.kubeconfig_path", ""),
		},
		SLIConfig: monitoring.SLIConfig{
			RollingWindow:   1 * time.Hour,
			RetentionPeriod: 24 * time.Hour,
		},
		SLOConfig: monitoring.SLOConfig{
			EvaluationInterval:         1 * time.Minute,
			ErrorBudgetWarningPercent:  20,
			ErrorBudgetCriticalPercent: 10,
		},
	}

	return cfg, nil
}

// healthCheckLoop runs periodic health checks
func (a *Agent) healthCheckLoop(ctx context.Context) {
	defer a.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.runHealthCheck()
		}
	}
}

// runHealthCheck runs a single health check
func (a *Agent) runHealthCheck() {
	// Check collector health
	status := a.collector.Status()
	allHealthy := true

	for name, source := range status {
		if !source.Healthy {
			a.logger.Warn("collector unhealthy",
				zap.String("source", name),
				zap.String("error", source.LastError))
			allHealthy = false
		}
	}

	if allHealthy {
		a.logger.Debug("all collectors healthy")
	}
}

// statusLoop logs periodic status updates
func (a *Agent) statusLoop(ctx context.Context) {
	defer a.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.RLock()
			metricCount := len(a.latestMetrics)
			exampleMetrics := ""
			if metricCount > 0 {
				names := []string{}
				for i := 0; i < 3 && i < metricCount; i++ {
					names = append(names, a.latestMetrics[i].Name)
				}
				exampleMetrics = strings.Join(names, ", ")
			}
			a.mu.RUnlock()

			a.logger.Info("agent status summary",
				zap.String("state", a.stateMachine.Current().String()),
				zap.Duration("uptime", a.uptime()),
				zap.Int("metrics_collected", metricCount),
				zap.String("examples", exampleMetrics))
		}
	}
}

func (a *Agent) startMetricsServer() {
	addr := fmt.Sprintf(":%d", a.config.Server.MetricsPort)
	mux := http.NewServeMux()

	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		if a.Running() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}

	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/health", healthHandler)

	// Prometheus Metrics Exporter
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		metrics := append([]sources.Metric(nil), a.latestMetrics...)
		a.mu.RUnlock()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		bw := bufio.NewWriterSize(w, 64*1024)
		defer func() { _ = bw.Flush() }()

		sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })

		var prevName string
		for _, m := range metrics {
			if m.Name == "" {
				continue
			}
			name := prom.SanitizeMetricName(m.Name)
			if name == "" {
				continue
			}
			if m.Name != prevName {
				typ := m.Type
				if typ == "" {
					typ = "gauge"
				}
				fmt.Fprintf(bw, "# TYPE %s %s\n", name, typ)
				prevName = m.Name
			}

			fmt.Fprint(bw, name)
			if len(m.Labels) > 0 {
				fmt.Fprint(bw, "{")
				keys := make([]string, 0, len(m.Labels))
				for k := range m.Labels {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				first := true
				for _, k := range keys {
					k = prom.SanitizeLabelKey(k)
					if k == "" {
						continue
					}
					if !first {
						fmt.Fprint(bw, ",")
					}
					fmt.Fprintf(bw, "%s=%s", k, prom.QuoteLabelValue(m.Labels[k]))
					first = false
				}
				fmt.Fprint(bw, "}")
			}
			fmt.Fprintf(bw, " %g\n", m.Value)
		}
	})

	a.metricsServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		a.logger.Info("starting metrics server", zap.String("addr", addr))
		if err := a.metricsServer.ListenAndServe(); err != http.ErrServerClosed {
			a.logger.Error("metrics server failed", zap.Error(err))
		}
	}()
}

func (a *Agent) startAPIServer() {
	addr := fmt.Sprintf(":%d", a.config.Server.Port)
	mux := http.NewServeMux()

	// Determine web UI path - try multiple locations
	// Determine web UI path - try multiple locations
	webPaths := []string{}
	if a.config.Server.WebPath != "" {
		webPaths = append(webPaths, a.config.Server.WebPath)
	}
	// Add default locations
	webPaths = append(webPaths,
		"/var/lib/sre-collector/web",
		"/usr/local/share/sre-collector/web",
		"./web",
		"./web/dist",
	)

	webPath := "./web" // default for development
	for _, path := range webPaths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			webPath = path
			break
		}
	}

	a.logger.Info("serving web UI", zap.String("path", webPath))

	// Create a file server for the web UI
	fileServer := http.FileServer(http.Dir(webPath))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// API requests skip file serving
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		// Check if file exists, otherwise serve index.html
		path, ok := sanitizeWebRelativePath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		// Try to serve the file directly.
		if _, err := os.Stat(filepath.Join(webPath, path)); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Default to index.html for SPA routing
		http.ServeFile(w, r, filepath.Join(webPath, "index.html"))
	})

	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		if a.Running() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}

	withCORS := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			h(w, r)
		}
	}

	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/health", healthHandler)

	// Agent Status API
	mux.HandleFunc("/api/v1/status", withCORS(func(w http.ResponseWriter, r *http.Request) {
		status := a.Status()
		utils.WriteJSON(w, http.StatusOK, status)
	}))

	// Metrics API
	mux.HandleFunc("/api/v1/metrics", withCORS(func(w http.ResponseWriter, r *http.Request) {
		metrics := a.GetLatestMetrics()
		utils.WriteJSON(w, http.StatusOK, metrics)
	}))

	// SLO Status API
	mux.HandleFunc("/api/v1/slo/status", withCORS(func(w http.ResponseWriter, r *http.Request) {
		if a.sloTracker != nil {
			status := a.sloTracker.Evaluate()
			utils.WriteJSON(w, http.StatusOK, status)
		} else {
			utils.WriteJSON(w, http.StatusOK, map[string]interface{}{})
		}
	}))

	// AI Reasoner API (placeholder for future integration)
	mux.HandleFunc("/api/v1/reasoner/latest", withCORS(func(w http.ResponseWriter, r *http.Request) {
		// Basic health assessment based on metrics
		a.mu.RLock()
		metrics := a.latestMetrics
		a.mu.RUnlock()

		assessment := a.generateAssessment(metrics)
		utils.WriteJSON(w, http.StatusOK, assessment)
	}))

	// Register SRE API endpoints (incidents, changes, SLO violations, metrics history)
	if a.sreManager != nil {
		a.sreManager.RegisterSREHandlers(mux)
	}

	a.apiServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		a.logger.Info("starting API server with web UI", zap.String("addr", addr))
		if err := a.apiServer.ListenAndServe(); err != http.ErrServerClosed {
			a.logger.Error("API server failed", zap.Error(err))
		}
	}()
}

// Reload reloads the agent configuration
func (a *Agent) Reload(cfg *config.Config) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.logger.Info("reloading agent configuration")
	a.config = cfg

	// Update intervals, re-init sources, etc.
	return nil
}

// metricListenerLoop listens for collected metrics and updates latestMetrics
func (a *Agent) metricListenerLoop(ctx context.Context) {
	defer a.wg.Done()

	sampleTicker := time.NewTicker(10 * time.Second)
	defer sampleTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sampleTicker.C:
			if a.sreManager == nil {
				continue
			}
			a.mu.RLock()
			snap := make(map[string]float64, len(a.latestValues))
			for k, v := range a.latestValues {
				snap[k] = v
			}
			a.mu.RUnlock()
			a.sreManager.RecordMetrics(snap)
		case m := <-a.metricCh:
			key := metricKey(m.Name, m.Labels)
			a.mu.Lock()
			if idx, ok := a.latestIndex[key]; ok {
				a.latestMetrics[idx] = m
			} else {
				a.latestIndex[key] = len(a.latestMetrics)
				a.latestMetrics = append(a.latestMetrics, m)
			}
			a.latestValues[key] = m.Value
			a.mu.Unlock()

			// Record in SLI tracker
			if a.sliTracker != nil {
				a.sliTracker.RecordMetric(m)
			}
		}
	}
}

func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := labelKeyPool.Get().([]string)
	keys = keys[:0]
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.Grow(len(name) + 2 + len(keys)*16)
	b.WriteString(name)
	b.WriteByte('|')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	labelKeyPool.Put(keys[:0])
	return b.String()
}

func sanitizeWebRelativePath(requestPath string) (string, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(requestPath), "/")
	if trimmed == "" {
		return "index.html", true
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == "." {
		return "index.html", true
	}
	if cleaned == "" || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || filepath.IsAbs(cleaned) {
		return "", false
	}
	return cleaned, true
}

// uptime returns the agent uptime
func (a *Agent) uptime() time.Duration {
	startedAt := a.stateMachine.StartedAt()
	if startedAt.IsZero() {
		return 0
	}
	return time.Since(startedAt)
}

// GetLatestMetrics returns the latest collected metrics
func (a *Agent) GetLatestMetrics() []sources.Metric {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return cloneMetrics(a.latestMetrics)
}

func (a *Agent) setLatestMetrics(metrics []sources.Metric) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cloned := cloneMetrics(metrics)

	deduped := make([]sources.Metric, 0, len(cloned))
	index := make(map[string]int, len(cloned))
	values := make(map[string]float64, len(cloned))

	for _, metric := range cloned {
		key := metricKey(metric.Name, metric.Labels)
		if i, ok := index[key]; ok {
			deduped[i] = metric
		} else {
			index[key] = len(deduped)
			deduped = append(deduped, metric)
		}
		values[key] = metric.Value
	}

	a.latestMetrics = deduped
	a.latestIndex = index
	a.latestValues = values
}

func cloneMetrics(metrics []sources.Metric) []sources.Metric {
	if len(metrics) == 0 {
		return nil
	}
	out := make([]sources.Metric, len(metrics))
	for i, metric := range metrics {
		metricCopy := metric
		if len(metric.Labels) > 0 {
			labels := make(map[string]string, len(metric.Labels))
			for k, v := range metric.Labels {
				labels[k] = v
			}
			metricCopy.Labels = labels
		}
		out[i] = metricCopy
	}
	return out
}

// Assessment represents a health assessment
type Assessment struct {
	Healthy   bool     `json:"healthy"`
	Reasoning string   `json:"reasoning"`
	Issues    []string `json:"issues,omitempty"`
	Timestamp string   `json:"timestamp"`
}

// generateAssessment generates a basic health assessment from metrics
func (a *Agent) generateAssessment(metrics []sources.Metric) Assessment {
	assessment := Assessment{
		Healthy:   true,
		Reasoning: "All systems operating within normal parameters.",
		Timestamp: time.Now().Format(time.RFC3339),
	}

	var issues []string
	var cpuValue, memValue, loadValue float64

	// Extract key metrics
	for _, m := range metrics {
		switch m.Name {
		case "system.cpu.usage":
			cpuValue = m.Value
		case "system.memory.used":
			if memTotal := getMetricByName(metrics, "system.memory.total"); memTotal != nil {
				memValue = (m.Value / memTotal.Value) * 100
			}
		case "system.load.1m":
			loadValue = m.Value
		}
	}

	// Evaluate CPU
	if cpuValue > 90 {
		assessment.Healthy = false
		issues = append(issues, "CPU critical")
		assessment.Reasoning = fmt.Sprintf("CPU usage is critically high at %.1f%%. System performance may be severely degraded.", cpuValue)
	} else if cpuValue > 75 {
		issues = append(issues, "CPU elevated")
		if assessment.Healthy {
			assessment.Reasoning = fmt.Sprintf("CPU usage is elevated at %.1f%%. Monitor closely.", cpuValue)
		}
	}

	// Evaluate Memory
	if memValue > 90 {
		assessment.Healthy = false
		issues = append(issues, "Memory critical")
		if !assessment.Healthy {
			assessment.Reasoning = fmt.Sprintf("Memory usage is critically high at %.1f%%. Risk of OOM.", memValue)
		}
	} else if memValue > 80 {
		issues = append(issues, "Memory elevated")
		if assessment.Healthy {
			assessment.Reasoning = fmt.Sprintf("Memory usage is elevated at %.1f%%.", memValue)
		}
	}

	// Evaluate Load
	if loadValue > 10 {
		wasHealthy := assessment.Healthy
		assessment.Healthy = false
		issues = append(issues, "Load high")
		if wasHealthy {
			assessment.Reasoning = fmt.Sprintf("System load average is very high (%.2f).", loadValue)
		}
	}

	// Check for unhealthy collectors
	if status := a.Status(); status.Collector != nil {
		for name, source := range status.Collector {
			if !source.Healthy {
				issues = append(issues, fmt.Sprintf("%s unhealthy", name))
			}
		}
	}

	if len(issues) > 0 {
		assessment.Issues = issues
	}

	return assessment
}

// getMetricByName is a helper to find a metric by name
func getMetricByName(metrics []sources.Metric, name string) *sources.Metric {
	for i := range metrics {
		if metrics[i].Name == name {
			return &metrics[i]
		}
	}
	return nil
}

// Status represents the agent status
type Status struct {
	Version   string                          `json:"version"`
	Commit    string                          `json:"commit"`
	State     string                          `json:"state"`
	Uptime    time.Duration                   `json:"uptime"`
	StartedAt time.Time                       `json:"started_at"`
	Collector map[string]sources.SourceStatus `json:"collector"`
}

// Version returns the agent version
func Version() string {
	if version != "" {
		return version
	}
	return "0.1.0"
}

// Commit returns the git commit hash
func Commit() string {
	if commit != "" {
		return commit
	}
	return "unknown"
}

var (
	version = ""
	commit  = ""
)
