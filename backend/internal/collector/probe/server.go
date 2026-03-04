// Package probe implements the HTTP server for the controlled node (probe).
//
// Terminology:
//   - Probe: This component - a lightweight data collector on monitored hosts
//   - Controller: The central aggregation server
//   - Agent: The overall AI SRE system (NOT this component)
package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/metrics/prom"
	"go.uber.org/zap"
)

// Config holds probe configuration
type Config struct {
	ListenAddr      string        `yaml:"listen"`
	ScrapeInterval  time.Duration `yaml:"scrape_interval"`
	LogLevel        string        `yaml:"log_level"`
	Version         string        `yaml:"version"`
	CollectionLevel int           `yaml:"collection_level"` // 1-5: Basic, Extended, Deep, Logs, RCA
	TopProcesses    int           `yaml:"top_processes"`
	ControllerURL   string        `yaml:"controller_url"` // For Push model
}

// DefaultConfig returns a default configuration
func DefaultConfig() Config {
	return Config{
		ListenAddr:      ":9100",
		ControllerURL:   "",
		ScrapeInterval:  10 * time.Second,
		LogLevel:        "info",
		Version:         "v0.5",
		CollectionLevel: 2, // Default: Basic + Extended
		TopProcesses:    20,
	}
}

// Probe is the main probe server
type Probe struct {
	config    Config
	logger    *zap.Logger
	collector *Collector
	filter    *MetricsFilter

	// Cached metrics
	mu            sync.RWMutex
	latestBatch   *MetricBatch
	lastScrapeErr error

	// Lifecycle
	server  *http.Server
	ctx     context.Context
	cancel  context.CancelFunc
	running bool
}

// New creates a new probe
func New(cfg Config, logger *zap.Logger) (*Probe, error) {
	if logger == nil {
		logger, _ = zap.NewProduction()
	}

	// Build collector options based on config
	var opts []CollectorOption
	if cfg.CollectionLevel > 0 {
		opts = append(opts, WithLevel(cfg.CollectionLevel))
	}
	if cfg.TopProcesses > 0 {
		opts = append(opts, WithTopNProcesses(cfg.TopProcesses))
	}

	collector, err := NewCollector(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create collector: %w", err)
	}

	return &Probe{
		config:    cfg,
		logger:    logger.With(zap.String("component", "probe")),
		collector: collector,
		filter:    NewMetricsFilter(0.7), // Alpha 0.7 = moderate smoothing
	}, nil
}

// Start starts the probe
func (p *Probe) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return fmt.Errorf("probe already running")
	}
	p.running = true
	p.mu.Unlock()

	p.ctx, p.cancel = context.WithCancel(ctx)

	// Initial scrape
	p.scrape()

	// Start collector background tasks
	p.collector.Start()

	// Start background scraper
	go p.scrapeLoop()

	// Enable Log pushing
	if p.collector.logCollector != nil && p.config.ControllerURL != "" {
		endpoint := p.config.ControllerURL + "/api/v1/logs/ingest"
		p.collector.logCollector.EnablePush(endpoint, 10*time.Second)
	}

	// Setup HTTP server
	mux := http.NewServeMux()
	p.registerHandlers(mux)

	p.server = &http.Server{
		Addr:         p.config.ListenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	p.logger.Info("starting probe",
		zap.String("listen", p.config.ListenAddr),
		zap.Duration("scrape_interval", p.config.ScrapeInterval))

	go func() {
		if err := p.server.ListenAndServe(); err != http.ErrServerClosed {
			p.logger.Error("server error", zap.Error(err))
		}
	}()

	return nil
}

// Stop stops the probe
func (p *Probe) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	p.logger.Info("stopping probe")

	// Stop collector
	p.collector.Stop()

	if p.cancel != nil {
		p.cancel()
	}

	if p.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.server.Shutdown(ctx)
	}

	p.running = false
	return nil
}

// registerHandlers sets up HTTP routes
func (p *Probe) registerHandlers(mux *http.ServeMux) {
	// Prometheus-compatible metrics endpoint
	mux.HandleFunc("/metrics", p.handleMetrics)

	// Health check
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/healthz", p.handleHealth)

	// JSON API
	mux.HandleFunc("/api/v1/metrics", p.handleMetricsJSON)
	mux.HandleFunc("/api/v1/status", p.handleStatus)

	// Root redirects to metrics
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/metrics", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})
}

// handleMetrics returns metrics in Prometheus text format
func (p *Probe) handleMetrics(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	batch := p.latestBatch
	p.mu.RUnlock()

	if batch == nil {
		http.Error(w, "No metrics available", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	bw := bufio.NewWriterSize(w, 64*1024)
	defer func() { _ = bw.Flush() }()

	// Sort a copy so we can stream HELP/TYPE once per metric name without allocating a grouping map.
	metrics := append([]Metric(nil), batch.Metrics...)
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
			if m.Help != "" {
				fmt.Fprintf(bw, "# HELP %s %s\n", name, m.Help)
			}
			fmt.Fprintf(bw, "# TYPE %s %s\n", name, m.Type)
			prevName = m.Name
		}

		fmt.Fprint(bw, name)
		if len(m.Labels) > 0 {
			fmt.Fprint(bw, "{")
			first := true
			keys := make([]string, 0, len(m.Labels))
			for k := range m.Labels {
				keys = append(keys, k)
			}
			sort.Strings(keys)
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
}

// handleMetricsJSON returns metrics in JSON format
func (p *Probe) handleMetricsJSON(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	batch := p.latestBatch
	p.mu.RUnlock()

	if batch == nil {
		http.Error(w, `{"error":"no metrics available"}`, http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(batch)
}

// handleHealth returns health status
func (p *Probe) handleHealth(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	lastErr := p.lastScrapeErr
	p.mu.RUnlock()

	if lastErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("UNHEALTHY: " + lastErr.Error()))
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// StatusResponse is the response for /api/v1/status
type StatusResponse struct {
	Hostname        string    `json:"hostname"`
	Version         string    `json:"version"`
	Uptime          string    `json:"uptime"`
	LastScrape      time.Time `json:"last_scrape"`
	MetricsCount    int       `json:"metrics_count"`
	LastError       string    `json:"last_error,omitempty"`
	ListenAddress   string    `json:"listen_address"`
	CollectionLevel int       `json:"collection_level"`
	LevelName       string    `json:"level_name"`
}

// handleStatus returns probe status
func (p *Probe) handleStatus(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	batch := p.latestBatch
	lastErr := p.lastScrapeErr
	p.mu.RUnlock()

	levelNames := map[int]string{
		1: "Basic",
		2: "Extended",
		3: "Deep",
		4: "Logs",
		5: "RCA (Root Cause Analysis)",
	}

	resp := StatusResponse{
		Version:         firstNonEmptyString(p.config.Version, "v0.5"),
		ListenAddress:   p.config.ListenAddr,
		CollectionLevel: p.config.CollectionLevel,
		LevelName:       levelNames[p.config.CollectionLevel],
	}

	if batch != nil {
		resp.Hostname = batch.Hostname
		resp.LastScrape = batch.CollectedAt
		resp.MetricsCount = len(batch.Metrics)
	}

	if lastErr != nil {
		resp.LastError = lastErr.Error()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// scrapeLoop runs periodic metric collection
func (p *Probe) scrapeLoop() {
	ticker := time.NewTicker(p.config.ScrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.scrape()
		}
	}
}

// scrape performs a single metric collection
func (p *Probe) scrape() {
	batch, err := p.collector.Collect()

	// Apply Noise Filter
	if err == nil && batch != nil {
		batch = p.filter.Apply(batch)
	}

	p.mu.Lock()
	if err != nil {
		p.lastScrapeErr = err
		p.logger.Warn("scrape failed", zap.Error(err))
	} else {
		p.latestBatch = batch
		p.lastScrapeErr = nil
		p.logger.Debug("scrape completed", zap.Int("metrics", len(batch.Metrics)))
	}
	p.mu.Unlock()
}
