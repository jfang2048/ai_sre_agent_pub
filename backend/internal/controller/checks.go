package controller

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"go.uber.org/zap"
)

// ChecksConfig controls external checks executed by the controller.
type ChecksConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Interval   time.Duration `yaml:"interval"`
	Timeout    time.Duration `yaml:"timeout"`
	MaxHistory int           `yaml:"max_history"`
	Checks     []CheckConfig `yaml:"checks"`
}

// CheckConfig defines a single external check.
type CheckConfig struct {
	ID            string            `yaml:"id"`
	Type          string            `yaml:"type"`   // http, tls, dns
	Target        string            `yaml:"target"` // URL/host:port/name
	Method        string            `yaml:"method"`
	Enabled       bool              `yaml:"enabled"`
	Timeout       time.Duration     `yaml:"timeout"`
	ExpectStatus  []int             `yaml:"expect_status"`
	Headers       map[string]string `yaml:"headers"`
	HeaderEnv     map[string]string `yaml:"header_env"`
	AuthBearerEnv string            `yaml:"auth_bearer_env"`
	JSONPath      string            `yaml:"json_path"`
	JSONEquals    string            `yaml:"json_equals"`
	TLSMinDays    int               `yaml:"tls_min_days"`
	DNSResolver   string            `yaml:"dns_resolver"`
}

// DefaultChecksConfig provides sensible defaults.
func DefaultChecksConfig() ChecksConfig {
	return ChecksConfig{
		Enabled:    false,
		Interval:   60 * time.Second,
		Timeout:    10 * time.Second,
		MaxHistory: 50,
		Checks:     []CheckConfig{},
	}
}

// CheckStatus represents the outcome of a check.
type CheckStatus string

const (
	CheckStatusPass  CheckStatus = "pass"
	CheckStatusFail  CheckStatus = "fail"
	CheckStatusError CheckStatus = "error"
)

// CheckResult captures the outcome of a single check execution.
type CheckResult struct {
	ID              string                 `json:"id"`
	Type            string                 `json:"type"`
	Target          string                 `json:"target"`
	Status          CheckStatus            `json:"status"`
	StatusCode      int                    `json:"status_code,omitempty"`
	StartedAt       time.Time              `json:"started_at"`
	FinishedAt      time.Time              `json:"finished_at"`
	Latency         time.Duration          `json:"latency"`
	Error           string                 `json:"error,omitempty"`
	Diagnosis       string                 `json:"diagnosis,omitempty"`
	Recommendations []string               `json:"recommendations,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// CheckManager executes external checks on a schedule.
type CheckManager struct {
	config   ChecksConfig
	logger   *zap.Logger
	client   *http.Client
	onResult func(CheckResult)

	mu      sync.RWMutex
	latest  map[string]CheckResult
	history map[string][]CheckResult

	ctx     context.Context
	cancel  context.CancelFunc
	running bool
}

// NewCheckManager creates a new CheckManager.
func NewCheckManager(cfg ChecksConfig, logger *zap.Logger, onResult func(CheckResult)) *CheckManager {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	return &CheckManager{
		config:   cfg,
		logger:   logger.With(zap.String("component", "check_manager")),
		client:   &http.Client{Timeout: timeout},
		onResult: onResult,
		latest:   make(map[string]CheckResult),
		history:  make(map[string][]CheckResult),
	}
}

// Start begins the scheduled check loop.
func (m *CheckManager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("checks already running")
	}
	m.running = true
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()

	m.runAll()
	go m.loop()
	return nil
}

// Stop halts the check loop.
func (m *CheckManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.running = false
	return nil
}

// Latest returns the latest check results.
func (m *CheckManager) Latest() []CheckResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]CheckResult, 0, len(m.latest))
	for _, res := range m.latest {
		results = append(results, res)
	}
	return results
}

// History returns the check history.
func (m *CheckManager) History() map[string][]CheckResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string][]CheckResult, len(m.history))
	for id, history := range m.history {
		copyHistory := make([]CheckResult, len(history))
		copy(copyHistory, history)
		out[id] = copyHistory
	}
	return out
}

func (m *CheckManager) loop() {
	interval := m.config.Interval
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.runAll()
		}
	}
}

func (m *CheckManager) runAll() {
	if len(m.config.Checks) == 0 {
		return
	}

	var wg sync.WaitGroup
	for _, check := range m.config.Checks {
		if !check.Enabled {
			continue
		}
		wg.Add(1)
		go func(cfg CheckConfig) {
			defer wg.Done()
			result := m.runCheck(cfg)
			m.recordResult(result)
		}(check)
	}
	wg.Wait()
}

func (m *CheckManager) runCheck(cfg CheckConfig) CheckResult {
	start := time.Now()
	result := CheckResult{
		ID:        ensureCheckID(cfg),
		Type:      strings.ToLower(cfg.Type),
		Target:    cfg.Target,
		Status:    CheckStatusError,
		StartedAt: start,
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = m.config.Timeout
	}
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	var err error
	switch result.Type {
	case "http":
		result, err = m.runHTTPCheck(ctx, cfg, result)
	case "tls":
		result, err = m.runTLSCheck(ctx, cfg, result)
	case "dns":
		result, err = m.runDNSCheck(ctx, cfg, result)
	default:
		err = fmt.Errorf("unsupported check type: %s", cfg.Type)
	}

	result.FinishedAt = time.Now()
	result.Latency = result.FinishedAt.Sub(start)

	if err != nil {
		result.Status = CheckStatusError
		result.Error = err.Error()
		if result.Diagnosis == "" {
			result.Diagnosis = "check execution failed"
		}
		return result
	}

	return result
}

func (m *CheckManager) runHTTPCheck(ctx context.Context, cfg CheckConfig, result CheckResult) (CheckResult, error) {
	method := cfg.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, cfg.Target, nil)
	if err != nil {
		return result, err
	}

	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	for k, envVar := range cfg.HeaderEnv {
		if envVar == "" {
			continue
		}
		val := os.Getenv(envVar)
		if val != "" {
			req.Header.Set(k, val)
		}
	}
	if cfg.AuthBearerEnv != "" {
		if token := os.Getenv(cfg.AuthBearerEnv); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	resp, err := m.client.Do(req)
	if err != nil {
		result.Status = CheckStatusFail
		result.Diagnosis = "http request failed"
		result.Recommendations = []string{"verify endpoint is reachable", "check network policy or firewall rules"}
		return result, err
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	expected := expectedStatuses(cfg.ExpectStatus)
	if _, ok := expected[resp.StatusCode]; !ok {
		result.Status = CheckStatusFail
		result.Diagnosis = fmt.Sprintf("unexpected status code %d", resp.StatusCode)
		result.Recommendations = []string{"inspect upstream health", "check recent deployments for regressions"}
		return result, nil
	}

	if cfg.JSONPath != "" {
		var payload interface{}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			result.Status = CheckStatusFail
			result.Diagnosis = "failed to parse json response"
			return result, err
		}
		value, err := jsonLookup(payload, cfg.JSONPath)
		if err != nil {
			result.Status = CheckStatusFail
			result.Diagnosis = "json path lookup failed"
			return result, err
		}
		if cfg.JSONEquals != "" && fmt.Sprint(value) != cfg.JSONEquals {
			result.Status = CheckStatusFail
			result.Diagnosis = fmt.Sprintf("json path %s mismatch", cfg.JSONPath)
			result.Metadata = map[string]interface{}{
				"observed": value,
				"expected": cfg.JSONEquals,
			}
			result.Recommendations = []string{"validate upstream dependencies", "review feature flags or configuration drift"}
			return result, nil
		}
	}

	result.Status = CheckStatusPass
	result.Diagnosis = "endpoint healthy"
	return result, nil
}

func (m *CheckManager) runTLSCheck(ctx context.Context, cfg CheckConfig, result CheckResult) (CheckResult, error) {
	target := cfg.Target
	if strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://") {
		parsed, err := url.Parse(target)
		if err != nil {
			return result, err
		}
		target = parsed.Host
	}
	if !strings.Contains(target, ":") {
		target = target + ":443"
	}

	dialer := &net.Dialer{}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Timeout = time.Until(deadline)
	}
	conn, err := tls.DialWithDialer(dialer, "tcp", target, &tls.Config{MinVersion: tls.VersionTLS12})
	if err != nil {
		result.Status = CheckStatusFail
		result.Diagnosis = "tls handshake failed"
		return result, err
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return result, errors.New("no peer certificates")
	}

	cert := state.PeerCertificates[0]
	daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
	result.Metadata = map[string]interface{}{
		"expires_at": cert.NotAfter,
		"days_left":  daysLeft,
		"issuer":     cert.Issuer.CommonName,
	}

	minDays := cfg.TLSMinDays
	if minDays == 0 {
		minDays = 30
	}

	if daysLeft <= minDays {
		result.Status = CheckStatusFail
		result.Diagnosis = "tls certificate expiring soon"
		result.Recommendations = []string{"rotate certificate", "verify automated renewal"}
		return result, nil
	}

	result.Status = CheckStatusPass
	result.Diagnosis = "tls certificate valid"
	return result, nil
}

func (m *CheckManager) runDNSCheck(ctx context.Context, cfg CheckConfig, result CheckResult) (CheckResult, error) {
	resolver := net.DefaultResolver
	if cfg.DNSResolver != "" {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{}
				return d.DialContext(ctx, network, cfg.DNSResolver)
			},
		}
	}

	hosts, err := resolver.LookupHost(ctx, cfg.Target)
	if err != nil {
		result.Status = CheckStatusFail
		result.Diagnosis = "dns lookup failed"
		result.Recommendations = []string{"verify dns resolver availability", "check service discovery records"}
		return result, err
	}

	result.Status = CheckStatusPass
	result.Diagnosis = "dns resolved"
	result.Metadata = map[string]interface{}{
		"records": hosts,
	}
	return result, nil
}

func (m *CheckManager) recordResult(result CheckResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.latest[result.ID] = result
	history := m.history[result.ID]
	history = append(history, result)

	maxHistory := m.config.MaxHistory
	if maxHistory <= 0 {
		maxHistory = 50
	}
	if len(history) > maxHistory {
		history = history[len(history)-maxHistory:]
	}
	m.history[result.ID] = history

	if m.onResult != nil {
		m.onResult(result)
	}
}

func ensureCheckID(cfg CheckConfig) string {
	if cfg.ID != "" {
		return cfg.ID
	}
	if cfg.Target == "" {
		return strings.ToLower(cfg.Type)
	}
	sanitized := strings.NewReplacer("://", "-", "/", "-", ":", "-").Replace(cfg.Target)
	return fmt.Sprintf("%s-%s", strings.ToLower(cfg.Type), sanitized)
}

func expectedStatuses(custom []int) map[int]struct{} {
	if len(custom) == 0 {
		return map[int]struct{}{
			200: {}, 201: {}, 202: {}, 204: {},
			300: {}, 301: {}, 302: {}, 307: {}, 308: {},
		}
	}
	out := make(map[int]struct{}, len(custom))
	for _, code := range custom {
		out[code] = struct{}{}
	}
	return out
}

func jsonLookup(payload interface{}, path string) (interface{}, error) {
	segments := strings.Split(path, ".")
	current := payload
	for _, segment := range segments {
		switch typed := current.(type) {
		case map[string]interface{}:
			val, ok := typed[segment]
			if !ok {
				return nil, fmt.Errorf("path segment %s not found", segment)
			}
			current = val
		default:
			return nil, fmt.Errorf("unsupported json structure at %s", segment)
		}
	}
	return current, nil
}

// RegisterCheckHandlers wires the checks endpoints.
func (c *Controller) RegisterCheckHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/checks", c.withCORS(c.handleChecks))
	mux.HandleFunc("/api/v1/checks/history", c.withCORS(c.handleCheckHistory))
	c.logger.Info("external checks API endpoints registered")
}

func (c *Controller) handleChecks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	results := c.checks.Latest()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"checks":    results,
		"count":     len(results),
		"timestamp": time.Now(),
	})
}

func (c *Controller) handleCheckHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	history := c.checks.History()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"history":   history,
		"timestamp": time.Now(),
	})
}

func (c *Controller) onCheckResult(result CheckResult) {
	if c.analysisExt == nil {
		return
	}
	value := 0.0
	if result.Status == CheckStatusPass {
		value = 1.0
	}
	samples := []analysis.MetricSample{
		{
			Name:      "external.check.up",
			Value:     value,
			Timestamp: result.FinishedAt,
			Labels: map[string]string{
				"check_id": result.ID,
				"type":     result.Type,
				"target":   result.Target,
				"status":   string(result.Status),
			},
		},
	}
	c.analysisExt.engine.IngestMetrics("external-checks", samples)
}
