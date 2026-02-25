package monitoring

import (
	"context"
	"math"
	"sync"
	"time"

	"go.uber.org/zap"
)

// SLOConfig configures SLO tracking
type SLOConfig struct {
	// SLO definitions file path
	ConfigPath string `yaml:"config_path"`

	// Evaluation interval
	EvaluationInterval time.Duration `yaml:"evaluation_interval"`

	// Alerting thresholds
	ErrorBudgetWarningPercent  float64 `yaml:"error_budget_warning_percent"`
	ErrorBudgetCriticalPercent float64 `yaml:"error_budget_critical_percent"`

	// Burn rate alerting thresholds
	BurnRateAlertThreshold int `yaml:"burn_rate_alert_threshold"` // e.g., 1 for 1x, 2 for 2x
}

// SLODefinition defines a Service Level Objective
type SLODefinition struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Target SLI
	SLIID string `yaml:"sli_id"`

	// SLO target (e.g., 99.9 for 99.9%)
	Target float64 `yaml:"target"`

	// Time window for SLO calculation
	Window time.Duration `yaml:"window"`

	// Rolling vs calendar window
	IsRolling bool `yaml:"is_rolling"`

	// SLO Tier (Google SRE: availability target for user-facing services)
	Tier SLOTier `yaml:"tier"`

	Labels map[string]string `yaml:"labels"`
}

// SLOTier defines the reliability tier (Google SRE principles)
type SLOTier int

const (
	TierUnknown SLOTier = iota
	Tier1               // 99.99+% (critical user-facing services)
	Tier2               // 99.95% (important services)
	Tier3               // 99.9% (internal tools)
	Tier4               // 99.5% (experimental features)
)

// TierTarget returns the default target for each tier
func (t SLOTier) TierTarget() float64 {
	switch t {
	case Tier1:
		return 99.99
	case Tier2:
		return 99.95
	case Tier3:
		return 99.9
	case Tier4:
		return 99.5
	default:
		return 99.9
	}
}

// BurnRateMeasurement captures burn rate at a point in time
type BurnRateMeasurement struct {
	Timestamp        time.Time `json:"timestamp"`
	BurnRate         float64   `json:"burn_rate"`          // Multiple of acceptable error rate
	Window           float64   `json:"window"`             // Window in hours
	TimeToExhaustion float64   `json:"time_to_exhaustion"` // Hours until budget exhausted
}

// SLOStatus represents the current status of an SLO with Google SRE metrics
type SLOStatus struct {
	SLOID     string    `json:"slo_id"`
	Timestamp time.Time `json:"timestamp"`

	// Current SLI value
	CurrentValue float64 `json:"current_value"`

	// SLO target
	Target float64 `json:"target"`

	// Compliance status
	Compliant bool `json:"compliant"`

	// Error budget (Google SRE)
	ErrorBudgetRemaining   float64 `json:"error_budget_remaining"` // Percentage
	ErrorBudgetRemainingMs int64   `json:"error_budget_remaining_ms"`
	ErrorBudgetStatus      string  `json:"error_budget_status"` // ok, warning, critical, exhausted

	// Burn rate metrics (Google SRE)
	BurnRate         float64               `json:"burn_rate"`        // Current burn rate
	BurnRateStatus   string                `json:"burn_rate_status"` // ok, warning, critical
	BurnRate1h       float64               `json:"burn_rate_1h"`     // 1-hour burn rate
	BurnRate12h      float64               `json:"burn_rate_12h"`    // 12-hour burn rate
	BurnRateHistory  []BurnRateMeasurement `json:"burn_rate_history"`
	TimeToExhaustion float64               `json:"time_to_exhaustion_hours"` // Hours until exhaustion

	// Window information
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
}

// SLOTracker tracks Service Level Objectives with Google SRE principles
type SLOTracker struct {
	config     SLOConfig
	logger     *zap.Logger
	sliTracker *SLITracker

	// SLO definitions
	slos map[string]*SLODefinition

	// Current status cache
	status map[string]*SLOStatus

	// Burn rate history for trend analysis
	burnRateHistory map[string][]BurnRateMeasurement

	mu      sync.RWMutex
	stopCh  chan struct{}
	wg      sync.WaitGroup
	running bool
}

// NewSLOTracker creates a new SLO tracker with Google SRE defaults
func NewSLOTracker(config SLOConfig, logger *zap.Logger, sliTracker *SLITracker) *SLOTracker {
	if config.EvaluationInterval == 0 {
		config.EvaluationInterval = 1 * time.Minute
	}
	if config.ErrorBudgetWarningPercent == 0 {
		config.ErrorBudgetWarningPercent = 20 // 20% remaining triggers warning
	}
	if config.ErrorBudgetCriticalPercent == 0 {
		config.ErrorBudgetCriticalPercent = 10 // 10% remaining triggers critical
	}
	if config.BurnRateAlertThreshold == 0 {
		config.BurnRateAlertThreshold = 2 // Alert at 2x burn rate
	}

	return &SLOTracker{
		config:          config,
		logger:          logger.With(zap.String("component", "slo_tracker")),
		sliTracker:      sliTracker,
		slos:            make(map[string]*SLODefinition),
		status:          make(map[string]*SLOStatus),
		burnRateHistory: make(map[string][]BurnRateMeasurement),
		stopCh:          make(chan struct{}),
	}
}

// Start starts the SLO tracker
func (t *SLOTracker) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return nil
	}
	t.running = true
	t.stopCh = make(chan struct{})

	t.wg.Add(1)
	go t.loop(ctx)

	t.logger.Info("SLO tracker started")
	return nil
}

// Stop stops the SLO tracker
func (t *SLOTracker) Stop() error {
	t.mu.Lock()
	if !t.running {
		t.mu.Unlock()
		return nil
	}
	t.running = false
	close(t.stopCh)
	t.mu.Unlock()

	t.wg.Wait()
	t.logger.Info("SLO tracker stopped")
	return nil
}

// loop runs the periodic evaluation
func (t *SLOTracker) loop(ctx context.Context) {
	defer t.wg.Done()

	ticker := time.NewTicker(t.config.EvaluationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.Evaluate()
		}
	}
}

// RegisterSLO registers a new SLO definition
func (t *SLOTracker) RegisterSLO(slo *SLODefinition) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Set default target if tier is specified but target is not
	if slo.Target == 0 && slo.Tier != TierUnknown {
		slo.Target = slo.Tier.TierTarget()
	}

	t.slos[slo.ID] = slo

	t.logger.Info("registered SLO",
		zap.String("id", slo.ID),
		zap.String("name", slo.Name),
		zap.String("sli_id", slo.SLIID),
		zap.Float64("target", slo.Target),
		zap.Int("tier", int(slo.Tier)))
}

// Evaluate evaluates all SLOs and returns their status with burn rate
func (t *SLOTracker) Evaluate() map[string]*SLOStatus {
	t.mu.Lock()
	defer t.mu.Unlock()

	results := make(map[string]*SLOStatus)

	for id, slo := range t.slos {
		status := t.evaluateSLO(slo)
		t.status[id] = status
		results[id] = status

		// Store burn rate measurement
		measurement := BurnRateMeasurement{
			Timestamp:        time.Now(),
			BurnRate:         status.BurnRate,
			Window:           slo.Window.Hours(),
			TimeToExhaustion: status.TimeToExhaustion,
		}
		t.burnRateHistory[id] = append(t.burnRateHistory[id], measurement)

		// Keep only last 100 measurements
		if len(t.burnRateHistory[id]) > 100 {
			t.burnRateHistory[id] = t.burnRateHistory[id][1:]
		}
	}

	return results
}

// evaluateSLO evaluates a single SLO with Google SRE burn rate calculation
func (t *SLOTracker) evaluateSLO(slo *SLODefinition) *SLOStatus {
	now := time.Now()
	var windowStart, windowEnd time.Time

	if slo.IsRolling {
		windowEnd = now
		windowStart = now.Add(-slo.Window)
	} else {
		windowEnd = now
		windowStart = now.Add(-slo.Window)
	}

	// Get SLI value
	sliValue := t.sliTracker.Calculate(slo.SLIID)
	if !sliValue.Valid {
		return &SLOStatus{
			SLOID:        slo.ID,
			Timestamp:    now,
			CurrentValue: 0,
			Target:       slo.Target,
			Compliant:    false,
			WindowStart:  windowStart,
			WindowEnd:    windowEnd,
		}
	}

	// Check compliance
	compliant := sliValue.Value >= slo.Target

	// Calculate error budget
	errorBudgetPercent := t.calculateErrorBudget(sliValue.Value, slo.Target)
	errorBudgetMs := t.calculateErrorBudgetMs(sliValue.Value, slo.Target, slo.Window)

	// Calculate burn rates (Google SRE)
	burnRate1h := t.calculateBurnRate(slo, time.Hour)
	burnRate12h := t.calculateBurnRate(slo, 12*time.Hour)
	currentBurnRate := burnRate1h // Use 1h as current burn rate

	// Calculate time to exhaustion
	timeToExhaustion := t.calculateTimeToExhaustion(errorBudgetPercent, currentBurnRate, slo.Window)

	// Determine error budget status
	errorBudgetStatus := t.getErrorBudgetStatus(errorBudgetPercent)

	// Determine burn rate status (Google SRE alerting)
	burnRateStatus := t.getBurnRateStatus(currentBurnRate)

	// Get burn rate history
	history := t.burnRateHistory[slo.ID]
	if len(history) == 0 {
		history = []BurnRateMeasurement{}
	}

	return &SLOStatus{
		SLOID:                  slo.ID,
		Timestamp:              now,
		CurrentValue:           sliValue.Value,
		Target:                 slo.Target,
		Compliant:              compliant,
		ErrorBudgetRemaining:   errorBudgetPercent,
		ErrorBudgetRemainingMs: errorBudgetMs,
		ErrorBudgetStatus:      errorBudgetStatus,
		BurnRate:               currentBurnRate,
		BurnRateStatus:         burnRateStatus,
		BurnRate1h:             burnRate1h,
		BurnRate12h:            burnRate12h,
		BurnRateHistory:        history,
		TimeToExhaustion:       timeToExhaustion,
		WindowStart:            windowStart,
		WindowEnd:              windowEnd,
	}
}

// calculateBurnRate calculates the burn rate over a given window
// Burn rate = current error rate / acceptable error rate
// (Google SRE: 1x = healthy, >2x = trigger incident response)
func (t *SLOTracker) calculateBurnRate(slo *SLODefinition, window time.Duration) float64 {
	sliValue := t.sliTracker.Calculate(slo.SLIID)
	if !sliValue.Valid {
		return 0
	}

	allowedBadRate := (100 - slo.Target) / 100
	currentBadRate := (100 - sliValue.Value) / 100

	if allowedBadRate == 0 {
		return 0
	}

	burnRate := currentBadRate / allowedBadRate
	return math.Round(burnRate*100) / 100 // Round to 2 decimal places
}

// calculateTimeToExhaustion calculates hours until error budget is exhausted
// Based on current burn rate (Google SRE)
func (t *SLOTracker) calculateTimeToExhaustion(errorBudgetPercent, burnRate float64, window time.Duration) float64 {
	if errorBudgetPercent <= 0 {
		return 0 // Already exhausted
	}
	if burnRate <= 1 {
		return -1 // Not burning, infinite time
	}

	// Time to exhaustion = remaining budget / (burn rate - 1) * window
	// This is an approximation
	remainingBudgetFraction := errorBudgetPercent / 100
	windowHours := window.Hours()

	// If burning at 2x, we use budget at (burnRate - 1) = 1x of allowed rate per unit time
	// Time to exhaust = remaining / (burnRate - 1) * window
	timeToExhaustionHours := (remainingBudgetFraction / (burnRate - 1)) * windowHours

	return math.Round(timeToExhaustionHours*100) / 100
}

// getBurnRateStatus determines the burn rate alert status (Google SRE)
// Google SRE: Alert if burn rate > 2 for any significant window
func (t *SLOTracker) getBurnRateStatus(burnRate float64) string {
	if burnRate >= 5 {
		return "critical" // 5x or higher = page immediately
	}
	if burnRate >= float64(t.config.BurnRateAlertThreshold) {
		return "warning" // 2x or higher = alert
	}
	return "ok"
}

// calculateErrorBudget calculates the remaining error budget as a percentage
func (t *SLOTracker) calculateErrorBudget(sliValue, target float64) float64 {
	allowedBadRate := (100 - target) / 100
	currentBadRate := (100 - sliValue) / 100

	if currentBadRate >= allowedBadRate {
		return 0 // Budget exhausted
	}

	remainingBudget := allowedBadRate - currentBadRate
	remainingPercent := (remainingBudget / allowedBadRate) * 100

	return remainingPercent
}

// calculateErrorBudgetMs calculates the remaining error budget in milliseconds
func (t *SLOTracker) calculateErrorBudgetMs(sliValue, target float64, window time.Duration) int64 {
	allowedBadRate := (100 - target) / 100
	currentBadRate := (100 - sliValue) / 100

	remainingBadRate := allowedBadRate - currentBadRate
	if remainingBadRate < 0 {
		remainingBadRate = 0
	}

	windowMs := window.Milliseconds()
	errorBudgetMs := int64(float64(windowMs) * remainingBadRate)

	return errorBudgetMs
}

// getErrorBudgetStatus determines the error budget status
func (t *SLOTracker) getErrorBudgetStatus(remainingPercent float64) string {
	if remainingPercent <= 0 {
		return "exhausted"
	}
	if remainingPercent <= t.config.ErrorBudgetCriticalPercent {
		return "critical"
	}
	if remainingPercent <= t.config.ErrorBudgetWarningPercent {
		return "warning"
	}
	return "ok"
}

// GetStatus returns the current status of all SLOs
func (t *SLOTracker) GetStatus() map[string]*SLOStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]*SLOStatus, len(t.status))
	for k, v := range t.status {
		result[k] = v
	}
	return result
}

// GetSLO returns a specific SLO definition
func (t *SLOTracker) GetSLO(id string) (*SLODefinition, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	slo, ok := t.slos[id]
	return slo, ok
}

// GetAllSLOs returns all registered SLOs
func (t *SLOTracker) GetAllSLOs() []*SLODefinition {
	t.mu.RLock()
	defer t.mu.RUnlock()

	slos := make([]*SLODefinition, 0, len(t.slos))
	for _, slo := range t.slos {
		slos = append(slos, slo)
	}
	return slos
}

// ShouldTriggerIncident returns true if burn rate warrants incident response
// Google SRE: Alert if burn rate > 2 for sustained period
func (t *SLOTracker) ShouldTriggerIncident(sloID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	status, ok := t.status[sloID]
	if !ok {
		return false
	}

	// Check if burn rate is above threshold
	return status.BurnRate >= float64(t.config.BurnRateAlertThreshold)
}
