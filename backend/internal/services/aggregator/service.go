package aggregator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Config holds aggregator configuration
type Config struct {
	ListenAddr    string
	FederationURL string
	Region        string
}

// Service is the aggregator service
type Service struct {
	config Config
	logger *zap.Logger
	server *http.Server

	// Stats for federation
	mu        sync.Mutex
	nodeStats map[string]NodeSummary
	lastPush  time.Time
}

// NodeSummary holds aggregated stats for a node
type NodeSummary struct {
	CPUUsage    float64
	MemoryUsage float64
	LastSeen    time.Time
}

// New creates a new aggregator service
func New(cfg Config, logger *zap.Logger) *Service {
	return &Service{
		config:    cfg,
		logger:    logger.With(zap.String("service", "aggregator")),
		nodeStats: make(map[string]NodeSummary),
	}
}

// Start starts the service
func (s *Service) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/ingest", s.handleIngest)
	mux.HandleFunc("/health", s.handleHealth)

	s.server = &http.Server{
		Addr:    s.config.ListenAddr,
		Handler: mux,
	}

	// Start federation background task if enabled
	if s.config.FederationURL != "" {
		go s.federationLoop()
	}

	s.logger.Info("aggregator starting", zap.String("addr", s.config.ListenAddr))
	return s.server.ListenAndServe()
}

// Stop stops the service
func (s *Service) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// handleIngest accepts metrics from probes
func (s *Service) handleIngest(w http.ResponseWriter, r *http.Request) {
	// Simple JSON ingestion
	var data map[string]interface{} // Generic for now
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Process for federation (extract key metrics)
	s.updateStats(data)

	// In a real system, we would forward this to Kafka or the Analyzer Service here.
	// For now, we just Ack.

	w.WriteHeader(http.StatusAccepted)
}

func (s *Service) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// updateStats updates internal stats for federation
func (s *Service) updateStats(data map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, _ := data["node_name"].(string)
	if node == "" {
		return
	}

	// Extract CPU/Mem if present (simplified parsing)
	metrics, _ := data["metrics"].([]interface{})
	var cpu, mem float64

	for _, m := range metrics {
		mm, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := mm["name"].(string)
		val, _ := mm["value"].(float64)

		if name == "node_cpu_usage_percent" {
			cpu = val
		} else if name == "node_memory_usage_percent" {
			mem = val
		}
	}

	s.nodeStats[node] = NodeSummary{
		CPUUsage:    cpu,
		MemoryUsage: mem,
		LastSeen:    time.Now(),
	}
}

// federationLoop periodically pushes summaries to global controller
func (s *Service) federationLoop() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		s.pushSummary()
	}
}

func (s *Service) pushSummary() {
	s.mu.Lock()
	stats := s.nodeStats
	// Reset or keep? Let's keep for running average (simplified here)
	s.mu.Unlock()

	// Calculate cluster averages
	var totalCPU, totalMem float64
	count := 0
	for _, stat := range stats {
		// Only counting active nodes
		if time.Since(stat.LastSeen) < 5*time.Minute {
			totalCPU += stat.CPUUsage
			totalMem += stat.MemoryUsage
			count++
		}
	}

	if count == 0 {
		return
	}

	summary := map[string]interface{}{
		"region":        s.config.Region,
		"timestamp":     time.Now(),
		"active_nodes":  count,
		"avg_cpu_usage": totalCPU / float64(count),
		"avg_mem_usage": totalMem / float64(count),
	}

	body, _ := json.Marshal(summary)

	resp, err := http.Post(s.config.FederationURL+"/api/v1/federation/ingest", "application/json", bytes.NewReader(body))
	if err != nil {
		s.logger.Error("federation push failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	s.logger.Info("federation summary pushed", zap.String("region", s.config.Region))
}
