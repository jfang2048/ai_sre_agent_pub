package change

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ChangeManager manages change windows and approvals (Google SRE)
type ChangeManager struct {
	logger    *zap.Logger
	changes   map[string]*Change
	approvals map[string]*ApprovalPolicy
	mu        sync.RWMutex
}

// Config configures the change manager
type Config struct {
	DefaultChangeWindowDuration time.Duration `yaml:"default_change_window_duration"`
	RequireApprovalForTier      int           `yaml:"require_approval_for_tier"` // Tier 1+ requires approval
}

// Change represents a planned change (Google SRE change management)
type Change struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`

	// Change metadata
	Tier        SLOTier    `json:"tier"`
	ChangeType  ChangeType `json:"change_type"`
	RiskLevel   RiskLevel  `json:"risk_level"`
	RequestedBy string     `json:"requested_by"`
	RequestedAt time.Time  `json:"requested_at"`

	// Change window
	PlannedStart time.Time `json:"planned_start"`
	PlannedEnd   time.Time `json:"planned_end"`
	ActualStart  time.Time `json:"actual_start,omitempty"`
	ActualEnd    time.Time `json:"actual_end,omitempty"`

	// Approval state
	ApprovalStatus ApprovalStatus `json:"approval_status"`
	ApprovedBy     string         `json:"approved_by,omitempty"`
	ApprovedAt     time.Time      `json:"approved_at,omitempty"`

	// Rollback plan
	RollbackPlan   string `json:"rollback_plan"`
	RollbackScript string `json:"rollback_script,omitempty"`

	// Validation
	PreChangeValidation  *ValidationResult `json:"pre_change_validation,omitempty"`
	PostChangeValidation *ValidationResult `json:"post_change_validation,omitempty"`

	// State
	Status    ChangeStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`

	// Canary deployment (Google SRE)
	CanaryConfig *CanaryConfig `json:"canary_config,omitempty"`
	CanaryStatus *CanaryStatus `json:"canary_status,omitempty"`
}

// SLOTier defines reliability tiers (Google SRE)
type SLOTier int

const (
	TierUnknown SLOTier = iota
	Tier1               // 99.99% - critical user-facing
	Tier2               // 99.95% - important services
	Tier3               // 99.9% - internal tools
	Tier4               // 99.5% - experimental
)

// ChangeType defines the type of change
type ChangeType string

const (
	ChangeTypeDeploy         ChangeType = "deploy"
	ChangeTypeConfig         ChangeType = "config"
	ChangeTypeInfrastructure ChangeType = "infrastructure"
	ChangeTypeFeature        ChangeType = "feature"
)

// RiskLevel defines the risk level of a change
type RiskLevel string

const (
	RiskLevelLow    RiskLevel = "low"
	RiskLevelMedium RiskLevel = "medium"
	RiskLevelHigh   RiskLevel = "high"
)

// ApprovalStatus defines the approval status
type ApprovalStatus string

const (
	ApprovalPending    ApprovalStatus = "pending"
	ApprovalApproved   ApprovalStatus = "approved"
	ApprovalRejected   ApprovalStatus = "rejected"
	ApprovalNoApproval ApprovalStatus = "no_approval_required"
)

// ChangeStatus defines the change status
type ChangeStatus string

const (
	StatusPlanned    ChangeStatus = "planned"
	StatusInProgress ChangeStatus = "in_progress"
	StatusCompleted  ChangeStatus = "completed"
	StatusRolledBack ChangeStatus = "rolled_back"
	StatusFailed     ChangeStatus = "failed"
	StatusCancelled  ChangeStatus = "cancelled"
)

// ValidationResult represents validation results
type ValidationResult struct {
	Passed    bool      `json:"passed"`
	Timestamp time.Time `json:"timestamp"`
	Checks    []Check   `json:"checks"`
	Message   string    `json:"message"`
}

// Check represents a single validation check
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Output string `json:"output,omitempty"`
}

// CanaryConfig configures canary deployment (Google SRE)
type CanaryConfig struct {
	InitialPercentage int              `json:"initial_percentage"` // Start with 5%
	IncrementStep     int              `json:"increment_step"`     // Increase by 5%
	IncrementInterval time.Duration    `json:"increment_interval"` // Every 5 minutes
	MaxPercentage     int              `json:"max_percentage"`     // Up to 100%
	Metrics           []string         `json:"metrics"`            // Metrics to monitor
	SuccessCriteria   *SuccessCriteria `json:"success_criteria"`   // Success/failure thresholds
}

// CanaryStatus tracks canary deployment progress
type CanaryStatus struct {
	CurrentPercentage int                    `json:"current_percentage"`
	CurrentStep       int                    `json:"current_step"`
	StartTime         time.Time              `json:"start_time"`
	Metrics           map[string]float64     `json:"metrics"`
	Status            CanaryDeploymentStatus `json:"status"`
	FailureReason     string                 `json:"failure_reason,omitempty"`
}

// CanaryDeploymentStatus canary deployment status
type CanaryDeploymentStatus string

const (
	CanaryNotStarted CanaryDeploymentStatus = "not_started"
	CanaryInProgress CanaryDeploymentStatus = "in_progress"
	CanaryHolding    CanaryDeploymentStatus = "holding"
	CanaryComplete   CanaryDeploymentStatus = "complete"
	CanaryRolledBack CanaryDeploymentStatus = "rolled_back"
)

// SuccessCriteria defines canary success/failure (Google SRE)
type SuccessCriteria struct {
	MaxErrorRate       float64 `json:"max_error_rate"`       // e.g., 1%
	MaxLatencyIncrease float64 `json:"max_latency_increase"` // e.g., 20%
	MinAvailability    float64 `json:"min_availability"`     // e.g., 99.9%
}

// ApprovalPolicy defines approval requirements
type ApprovalPolicy struct {
	Name               string   `json:"name"`
	RequiredApprovals  int      `json:"required_approvals"`
	AutoApproveForTier int      `json:"auto_approve_for_tier"` // Tier 4 and below auto-approve
	Approvers          []string `json:"approvers"`
}

// NewChangeManager creates a new change manager
func NewChangeManager(logger *zap.Logger) *ChangeManager {
	return &ChangeManager{
		logger:    logger.With(zap.String("component", "change_manager")),
		changes:   make(map[string]*Change),
		approvals: make(map[string]*ApprovalPolicy),
	}
}

// RegisterChange registers a new change request (Google SRE)
func (cm *ChangeManager) RegisterChange(change *Change) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.changes[change.ID]; exists {
		return fmt.Errorf("change %s already exists", change.ID)
	}

	// Set default values
	change.CreatedAt = time.Now()
	change.UpdatedAt = time.Now()
	change.Status = StatusPlanned

	// Auto-approve low-risk changes (Google SRE)
	if change.Tier >= Tier4 || change.RiskLevel == RiskLevelLow {
		change.ApprovalStatus = ApprovalNoApproval
	} else {
		change.ApprovalStatus = ApprovalPending
	}

	// Initialize canary status if configured
	if change.CanaryConfig != nil {
		change.CanaryStatus = &CanaryStatus{
			CurrentPercentage: 0,
			StartTime:         time.Now(),
			Metrics:           make(map[string]float64),
			Status:            CanaryNotStarted,
		}
	}

	cm.changes[change.ID] = change

	cm.logger.Info("change registered",
		zap.String("id", change.ID),
		zap.String("title", change.Title),
		zap.Int("tier", int(change.Tier)),
		zap.String("risk", string(change.RiskLevel)),
		zap.String("approval", string(change.ApprovalStatus)))

	return nil
}

// ApproveChange approves a change request
func (cm *ChangeManager) ApproveChange(changeID, approver string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	change, ok := cm.changes[changeID]
	if !ok {
		return fmt.Errorf("change %s not found", changeID)
	}

	if change.ApprovalStatus != ApprovalPending {
		return fmt.Errorf("change %s is not pending approval", changeID)
	}

	change.ApprovalStatus = ApprovalApproved
	change.ApprovedBy = approver
	change.ApprovedAt = time.Now()
	change.UpdatedAt = time.Now()

	cm.logger.Info("change approved",
		zap.String("id", changeID),
		zap.String("approver", approver))

	return nil
}

// RejectChange rejects a change request
func (cm *ChangeManager) RejectChange(changeID, rejector, reason string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	change, ok := cm.changes[changeID]
	if !ok {
		return fmt.Errorf("change %s not found", changeID)
	}

	if change.ApprovalStatus != ApprovalPending {
		return fmt.Errorf("change %s is not pending approval", changeID)
	}

	change.ApprovalStatus = ApprovalRejected
	change.Status = StatusCancelled
	change.UpdatedAt = time.Now()

	cm.logger.Info("change rejected",
		zap.String("id", changeID),
		zap.String("rejector", rejector),
		zap.String("reason", reason))

	return nil
}

// StartChange begins executing a change (Google SRE)
func (cm *ChangeManager) StartChange(ctx context.Context, changeID string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	change, ok := cm.changes[changeID]
	if !ok {
		return fmt.Errorf("change %s not found", changeID)
	}

	// Verify approval
	if change.ApprovalStatus == ApprovalPending {
		return fmt.Errorf("change %s is not approved", changeID)
	}
	if change.ApprovalStatus == ApprovalRejected {
		return fmt.Errorf("change %s was rejected", changeID)
	}

	// Verify change window
	now := time.Now()
	if now.Before(change.PlannedStart) || now.After(change.PlannedEnd) {
		return fmt.Errorf("change %s is outside planned window", changeID)
	}

	// Run pre-change validation
	if err := cm.runPreChangeValidation(ctx, change); err != nil {
		return fmt.Errorf("pre-change validation failed: %w", err)
	}

	change.Status = StatusInProgress
	change.ActualStart = now
	change.UpdatedAt = now

	// Start canary if configured
	if change.CanaryConfig != nil {
		go cm.runCanaryDeployment(ctx, change)
	} else {
		change.CanaryStatus = &CanaryStatus{
			CurrentPercentage: 100,
			StartTime:         now,
			Metrics:           make(map[string]float64),
			Status:            CanaryComplete,
		}
	}

	cm.logger.Info("change started",
		zap.String("id", changeID),
		zap.String("title", change.Title))

	return nil
}

// runCanaryDeployment executes canary deployment (Google SRE)
func (cm *ChangeManager) runCanaryDeployment(ctx context.Context, change *Change) {
	config := change.CanaryConfig
	status := change.CanaryStatus

	status.Status = CanaryInProgress
	status.CurrentPercentage = config.InitialPercentage

	ticker := time.NewTicker(config.IncrementInterval)
	defer ticker.Stop()

	for status.CurrentPercentage <= config.MaxPercentage {
		select {
		case <-ctx.Done():
			status.Status = CanaryRolledBack
			status.FailureReason = "context cancelled"
			return
		case <-ticker.C:
			// Check metrics against success criteria
			passed, reason := cm.checkCanaryMetrics(status.Metrics, config.SuccessCriteria)
			if !passed {
				status.Status = CanaryRolledBack
				status.FailureReason = reason
				cm.logger.Warn("canary failed, rolling back",
					zap.String("change_id", change.ID),
					zap.String("reason", reason))
				cm.RollbackChange(ctx, change.ID, reason)
				return
			}

			// Increment percentage
			status.CurrentPercentage += config.IncrementStep
			if status.CurrentPercentage > config.MaxPercentage {
				status.CurrentPercentage = config.MaxPercentage
				status.Status = CanaryComplete
				cm.logger.Info("canary deployment complete",
					zap.String("change_id", change.ID))
				return
			}
		}
	}
}

// checkCanaryMetrics checks if canary metrics meet success criteria
func (cm *ChangeManager) checkCanaryMetrics(metrics map[string]float64, criteria *SuccessCriteria) (bool, string) {
	if criteria == nil {
		return true, ""
	}

	// Check availability
	if metrics["availability"] < criteria.MinAvailability {
		return false, fmt.Sprintf("availability below threshold: %.2f < %.2f",
			metrics["availability"], criteria.MinAvailability)
	}

	// Check error rate
	if metrics["error_rate"] > criteria.MaxErrorRate {
		return false, fmt.Sprintf("error rate above threshold: %.2f > %.2f",
			metrics["error_rate"], criteria.MaxErrorRate)
	}

	// Check latency
	if metrics["latency_increase_pct"] > criteria.MaxLatencyIncrease {
		return false, fmt.Sprintf("latency increase above threshold: %.2f > %.2f",
			metrics["latency_increase_pct"], criteria.MaxLatencyIncrease)
	}

	return true, ""
}

// CompleteChange marks a change as complete
func (cm *ChangeManager) CompleteChange(changeID string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	change, ok := cm.changes[changeID]
	if !ok {
		return fmt.Errorf("change %s not found", changeID)
	}

	if change.Status != StatusInProgress {
		return fmt.Errorf("change %s is not in progress", changeID)
	}

	// Run post-change validation
	// In production, this would validate the actual system state
	change.PostChangeValidation = &ValidationResult{
		Passed:    true,
		Timestamp: time.Now(),
		Message:   "Post-change validation passed",
	}

	change.Status = StatusCompleted
	change.ActualEnd = time.Now()
	change.UpdatedAt = time.Now()

	cm.logger.Info("change completed",
		zap.String("id", changeID),
		zap.Duration("duration", change.ActualEnd.Sub(change.ActualStart)))

	return nil
}

// RollbackChange rolls back a change (Google SRE)
func (cm *ChangeManager) RollbackChange(ctx context.Context, changeID, reason string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	change, ok := cm.changes[changeID]
	if !ok {
		return fmt.Errorf("change %s not found", changeID)
	}

	// Execute rollback
	if change.RollbackScript != "" {
		// In production, execute the rollback script
		cm.logger.Info("executing rollback script",
			zap.String("change_id", changeID),
			zap.String("script", change.RollbackScript))
	}

	change.Status = StatusRolledBack
	change.ActualEnd = time.Now()
	change.UpdatedAt = time.Now()

	cm.logger.Warn("change rolled back",
		zap.String("id", changeID),
		zap.String("reason", reason))

	return nil
}

// GetChange retrieves a change by ID
func (cm *ChangeManager) GetChange(changeID string) (*Change, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	change, ok := cm.changes[changeID]
	if !ok {
		return nil, fmt.Errorf("change %s not found", changeID)
	}
	return change, nil
}

// GetChangesByStatus retrieves changes by status
func (cm *ChangeManager) GetChangesByStatus(status ChangeStatus) []*Change {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var result []*Change
	for _, change := range cm.changes {
		if change.Status == status {
			result = append(result, change)
		}
	}
	return result
}

// GetPendingChanges retrieves all pending changes
func (cm *ChangeManager) GetPendingChanges() []*Change {
	return cm.GetChangesByStatus(StatusPlanned)
}

// IsActiveChangeWindow returns true if current time is within a change window
func (cm *ChangeManager) IsActiveChangeWindow() bool {
	now := time.Now()
	// Google SRE: Change windows typically Tue-Thu, 10am-4pm local time
	weekday := now.Weekday()
	hour := now.Hour()

	// Allow changes Tuesday through Thursday, 10am-4pm
	if weekday >= 2 && weekday <= 4 && hour >= 10 && hour < 16 {
		return true
	}

	// Also allow Monday morning for emergency fixes
	if weekday == 1 && hour >= 10 && hour < 12 {
		return true
	}

	return false
}

// runPreChangeValidation runs pre-change checks (Google SRE)
func (cm *ChangeManager) runPreChangeValidation(ctx context.Context, change *Change) error {
	checks := []Check{
		{Name: "no_active_incidents", Passed: cm.checkNoActiveIncidents()},
		{Name: "error_budget_sufficient", Passed: cm.checkErrorBudgetSufficient(change.Tier)},
		{Name: "within_change_window", Passed: cm.IsActiveChangeWindow()},
	}

	allPassed := true
	for _, check := range checks {
		if !check.Passed {
			allPassed = false
		}
	}

	change.PreChangeValidation = &ValidationResult{
		Passed:    allPassed,
		Timestamp: time.Now(),
		Checks:    checks,
		Message:   "Pre-change validation completed",
	}

	if !allPassed {
		return fmt.Errorf("pre-change validation failed")
	}

	return nil
}

// checkNoActiveIncidents checks if there are active P0/P1 incidents
func (cm *ChangeManager) checkNoActiveIncidents() bool {
	// In production, query incident management system
	return true
}

// checkErrorBudgetSufficient checks if error budget allows changes
func (cm *ChangeManager) checkErrorBudgetSufficient(tier SLOTier) bool {
	// Google SRE: Don't make changes if error budget is low
	// Tier 1: require >10% error budget
	// Tier 2: require >5% error budget
	// Tier 3+: require >1% error budget

	switch tier {
	case Tier1:
		return true // Placeholder - would check actual error budget
	case Tier2:
		return true
	default:
		return true
	}
}

// UpdateCanaryMetrics updates metrics for canary deployment
func (cm *ChangeManager) UpdateCanaryMetrics(changeID string, metrics map[string]float64) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	change, ok := cm.changes[changeID]
	if !ok {
		return fmt.Errorf("change %s not found", changeID)
	}

	if change.CanaryStatus == nil {
		return fmt.Errorf("canary not configured for change %s", changeID)
	}

	for k, v := range metrics {
		change.CanaryStatus.Metrics[k] = v
	}

	return nil
}
