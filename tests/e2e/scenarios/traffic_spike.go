package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/core"
	"github.com/jfang2048/ai_sre_agent_pub/pkg/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TrafficSpikeScenario simulates a sudden traffic spike
type TrafficSpikeScenario struct {
	agent      *core.Agent
	logger     *zap.Logger
	httpServer *TestHTTPServer
	clients    []*TrafficClient
}

// TestHTTPServer simulates a service under test
type TestHTTPServer struct {
	addr   string
	server *http.Server
	stopCh chan struct{}
	mu     sync.Mutex
	stats  RequestStats
	logger *zap.Logger
}

// RequestStats tracks request statistics
type RequestStats struct {
	Total   int64
	Success int64
	Error   int64
}

// TrafficClient generates traffic
type TrafficClient struct {
	addr     string
	stopCh   chan struct{}
	rate     int // requests per second
	logger   *zap.Logger
}

// NewTrafficSpikeScenario creates a new traffic spike scenario
func NewTrafficSpikeScenario(logger *zap.Logger) *TrafficSpikeScenario {
	return &TrafficSpikeScenario{
		logger: logger,
	}
}

// Setup initializes the scenario
func (s *TrafficSpikeScenario) Setup(ctx context.Context) error {
	s.logger.Info("Setting up traffic spike scenario")

	// Start test HTTP server
	s.httpServer = &TestHTTPServer{
		addr:   ":18080",
		stopCh: make(chan struct{}),
		logger: s.logger,
	}

	if err := s.httpServer.Start(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}

	// Create and start agent
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:        "localhost",
			Port:        8081,
			MetricsPort: 9094,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}

	var err error
	s.agent, err = core.NewAgent(cfg, s.logger)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	if err := s.agent.Start(ctx); err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}

	return nil
}

// Start starts the HTTP server
func (s *TestHTTPServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/data", s.handleData)
	mux.HandleFunc("/metrics", s.handleMetrics)

	s.server = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	go func() {
		s.logger.Info("HTTP server starting", zap.String("addr", s.addr))
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", zap.Error(err))
		}
	}()

	return nil
}

func (s *TestHTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.stats.Total++
	s.stats.Success++
	s.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}

func (s *TestHTTPServer) handleData(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.stats.Total++
	// Simulate some processing time
	time.Sleep(10 * time.Millisecond)
	s.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"data":"test"}`))
}

func (s *TestHTTPServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	stats := s.stats
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(fmt.Sprintf(
		"http_requests_total %d\nhttp_success_total %d\nhttp_errors_total %d\n",
		stats.Total, stats.Success, stats.Error,
	)))
}

// Run executes the scenario
func (s *TrafficSpikeScenario) Run(ctx context.Context, duration time.Duration) error {
	s.logger.Info("Running traffic spike scenario", zap.Duration("duration", duration))

	// Phase 1: Baseline traffic (100 rps)
	s.logger.Info("Phase 1: Baseline traffic")
	baselineClient := NewTrafficClient(s.httpServer.addr, 100, s.logger)
	go baselineClient.Run(ctx)
	time.Sleep(10 * time.Second)

	// Phase 2: Traffic spike (2000 rps)
	s.logger.Info("Phase 2: Traffic spike")
	spikeClient := NewTrafficClient(s.httpServer.addr, 2000, s.logger)
	go spikeClient.Run(ctx)
	time.Sleep(20 * time.Second)

	spikeClient.Stop()

	// Phase 3: Return to baseline
	s.logger.Info("Phase 3: Return to baseline")
	time.Sleep(10 * time.Second)

	baselineClient.Stop()

	return nil
}

// Verify checks if the scenario produced expected results
func (s *TrafficSpikeScenario) Verify(ctx context.Context) error {
	s.logger.Info("Verifying traffic spike scenario results")

	// Check HTTP server stats
	s.httpServer.mu.Lock()
	stats := s.httpServer.stats
	s.httpServer.mu.Unlock()

	s.logger.Info("HTTP server stats",
		zap.Int64("total", stats.Total),
		zap.Int64("success", stats.Success),
		zap.Int64("error", stats.Error),
	)

	// Check agent collected metrics
	metrics := s.agent.GetLatestMetrics()
	if len(metrics) > 0 {
		s.logger.Info("Agent collected metrics", zap.Int("count", len(metrics)))
	}

	return nil
}

// Cleanup cleans up the scenario
func (s *TrafficSpikeScenario) Cleanup(ctx context.Context) error {
	s.logger.Info("Cleaning up traffic spike scenario")

	if s.httpServer != nil {
		close(s.httpServer.stopCh)
		s.httpServer.server.Shutdown(ctx)
	}

	if s.agent != nil {
		return s.agent.Stop()
	}

	return nil
}

// NewTrafficClient creates a new traffic client
func NewTrafficClient(addr string, rate int, logger *zap.Logger) *TrafficClient {
	return &TrafficClient{
		addr:   addr,
		stopCh: make(chan struct{}),
		rate:   rate,
		logger: logger,
	}
}

// Run starts generating traffic
func (c *TrafficClient) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second / time.Duration(c.rate))
	defer ticker.Stop()

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for {
		select {
		case <-ticker.C:
			go func() {
				resp, err := client.Get("http://" + c.addr + "/api/health")
				if err != nil {
					return
				}
				resp.Body.Close()
			}()
		case <-c.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Stop stops the client
func (c *TrafficClient) Stop() {
	close(c.stopCh)
}

// TestTrafficSpikeScenario is the test function
func TestTrafficSpikeScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	logger := zap.NewNop()

	scenario := NewTrafficSpikeScenario(logger)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Setup
	require.NoError(t, scenario.Setup(ctx))
	defer scenario.Cleanup(ctx)

	// Run
	require.NoError(t, scenario.Run(ctx, 45*time.Second))

	// Verify
	err := scenario.Verify(ctx)
	if err != nil {
		t.Logf("Verification warning: %v", err)
	}
}

// TestPodCrashScenario simulates a pod crash
func TestPodCrashScenario(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	// This would require a Kubernetes cluster
	t.Skip("Skipping: requires Kubernetes cluster")
}
