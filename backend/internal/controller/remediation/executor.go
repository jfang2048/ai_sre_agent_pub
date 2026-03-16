package remediation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Executor executes remediation actions safely
type Executor struct {
	mu              sync.RWMutex
	runningActions  map[string]*ActionExecution
	config          Config
	logger          *zap.Logger
	validator       Validator
	rollbackManager *RollbackManager
}

// Config configures the executor
type Config struct {
	DryRun            bool          `json:"dry_run"`
	MaxConcurrent     int           `json:"max_concurrent"`
	CooldownPeriod    time.Duration `json:"cooldown_period"`
	RequireApproval   bool          `json:"require_approval"`
	MaxActionsPerHour int           `json:"max_actions_per_hour"`
}

// ActionExecution represents a running action
type ActionExecution struct {
	ID        string
	Type      string
	Target    string
	StartedAt time.Time
	Status    string
	Result    *ActionResult
}

// ActionResult is the result of an action
type ActionResult struct {
	Success  bool             `json:"success"`
	Message  string           `json:"message"`
	Changes  []ResourceChange `json:"changes"`
	Duration time.Duration    `json:"duration"`
	Error    error            `json:"error,omitempty"`
}

// ResourceChange represents a resource change
type ResourceChange struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	OldValue string `json:"old_value,omitempty"`
	NewValue string `json:"new_value,omitempty"`
}

// Validator validates actions before execution
type Validator interface {
	Validate(ctx context.Context, action *Action) error
}

// Action represents a remediation action
type Action struct {
	Type        string                 `json:"type"`
	Target      string                 `json:"target"`
	Parameters  map[string]interface{} `json:"parameters"`
	Reason      string                 `json:"reason"`
	CanRollback bool                   `json:"can_rollback"`
}

// NewExecutor creates a new action executor
func NewExecutor(config Config, logger *zap.Logger) *Executor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Executor{
		runningActions:  make(map[string]*ActionExecution),
		config:          config,
		logger:          logger.With(zap.String("component", "executor")),
		rollbackManager: NewRollbackManager(logger),
	}
}

// Execute executes a remediation action
func (e *Executor) Execute(ctx context.Context, action *Action) (*ActionResult, error) {
	if action == nil {
		return nil, fmt.Errorf("action is required")
	}
	// Generate execution ID
	execID := fmt.Sprintf("exec-%d", time.Now().UnixNano())

	// Validate action
	if e.validator != nil {
		if err := e.validator.Validate(ctx, action); err != nil {
			return nil, fmt.Errorf("validation failed: %w", err)
		}
	}

	// Check concurrency limit
	if e.isAtConcurrencyLimit() {
		return nil, fmt.Errorf("concurrency limit reached")
	}

	// Create execution record
	execution := &ActionExecution{
		ID:        execID,
		Type:      action.Type,
		Target:    action.Target,
		StartedAt: time.Now(),
		Status:    "running",
	}

	e.mu.Lock()
	e.runningActions[execID] = execution
	e.mu.Unlock()

	e.logger.Info("executing action",
		zap.String("id", execID),
		zap.String("type", action.Type),
		zap.String("target", action.Target),
		zap.Bool("dry_run", e.config.DryRun))

	// Execute the action
	start := time.Now()
	result := &ActionResult{}

	if e.config.DryRun {
		result = e.executeDryRun(ctx, action)
	} else {
		result = e.executeReal(ctx, action, execID)
	}

	result.Duration = time.Since(start)
	execution.Result = result
	execution.Status = "completed"

	// Store rollback info if applicable
	if action.CanRollback && result.Success {
		e.rollbackManager.Record(action, result)
	}

	// Cleanup
	e.mu.Lock()
	delete(e.runningActions, execID)
	e.mu.Unlock()

	return result, nil
}

// executeDryRun simulates action execution
func (e *Executor) executeDryRun(ctx context.Context, action *Action) *ActionResult {
	e.logger.Info("dry run action",
		zap.String("type", action.Type),
		zap.String("target", action.Target))

	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Dry run: would execute %s on %s", action.Type, action.Target),
		Changes: []ResourceChange{
			{
				Type:     action.Type,
				Name:     action.Target,
				NewValue: "[simulated]",
			},
		},
	}
}

// executeReal executes the actual action
func (e *Executor) executeReal(ctx context.Context, action *Action, execID string) *ActionResult {
	// Route to appropriate handler
	switch action.Type {
	case "scale_deployment":
		return e.scaleDeployment(ctx, action)
	case "restart_pod":
		return e.restartPod(ctx, action)
	case "failover_traffic":
		return e.failoverTraffic(ctx, action)
	case "clear_cache":
		return e.clearCache(ctx, action)
	case "rollback_deployment":
		return e.rollbackDeployment(ctx, action)
	default:
		return &ActionResult{
			Success: false,
			Message: fmt.Sprintf("unknown action type: %s", action.Type),
		}
	}
}

// scaleDeployment scales a Kubernetes deployment
func (e *Executor) scaleDeployment(ctx context.Context, action *Action) *ActionResult {
	replicas := int(3)
	if parsed, ok, err := actionIntParameter(action, "replicas"); err != nil {
		return &ActionResult{
			Success: false,
			Message: fmt.Sprintf("invalid replicas parameter: %v", err),
			Error:   err,
		}
	} else if ok {
		replicas = parsed
	}

	e.logger.Info("scaling deployment",
		zap.String("target", action.Target),
		zap.Int("replicas", replicas))

	// In production, would use Kubernetes client API
	// kubectl scale deployment <target> --replicas=<replicas>

	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Scaled %s to %d replicas", action.Target, replicas),
		Changes: []ResourceChange{
			{
				Type:     "deployment",
				Name:     action.Target,
				NewValue: fmt.Sprintf("%d replicas", replicas),
			},
		},
	}
}

// restartPod restarts a pod
func (e *Executor) restartPod(ctx context.Context, action *Action) *ActionResult {
	e.logger.Info("restarting pod",
		zap.String("target", action.Target))

	// In production, would use Kubernetes client API
	// kubectl delete pod <target>

	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Restarted pod %s", action.Target),
		Changes: []ResourceChange{
			{
				Type:     "pod",
				Name:     action.Target,
				NewValue: "restarted",
			},
		},
	}
}

// failoverTraffic fails over traffic
func (e *Executor) failoverTraffic(ctx context.Context, action *Action) *ActionResult {
	region := ""
	if parsed, ok, err := actionStringParameter(action, "region"); err != nil {
		return &ActionResult{
			Success: false,
			Message: fmt.Sprintf("invalid region parameter: %v", err),
			Error:   err,
		}
	} else if ok {
		region = parsed
	}

	e.logger.Info("failing over traffic",
		zap.String("target", action.Target),
		zap.String("region", region))

	// In production, would update DNS or load balancer

	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Failed over traffic to %s", region),
		Changes: []ResourceChange{
			{
				Type:     "dns",
				Name:     action.Target,
				NewValue: region,
			},
		},
	}
}

// clearCache clears application cache
func (e *Executor) clearCache(ctx context.Context, action *Action) *ActionResult {
	e.logger.Info("clearing cache",
		zap.String("target", action.Target))

	// In production, would call cache invalidation API

	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Cleared cache for %s", action.Target),
	}
}

// rollbackDeployment rolls back a deployment
func (e *Executor) rollbackDeployment(ctx context.Context, action *Action) *ActionResult {
	e.logger.Info("rolling back deployment",
		zap.String("target", action.Target))

	// In production, would use Kubernetes rollback API

	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Rolled back deployment %s", action.Target),
	}
}

// isAtConcurrencyLimit checks if concurrency limit is reached
func (e *Executor) isAtConcurrencyLimit() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return len(e.runningActions) >= e.config.MaxConcurrent
}

// GetRunningActions returns all running actions
func (e *Executor) GetRunningActions() []*ActionExecution {
	e.mu.RLock()
	defer e.mu.RUnlock()

	actions := make([]*ActionExecution, 0, len(e.runningActions))
	for _, a := range e.runningActions {
		actions = append(actions, a)
	}
	return actions
}

// SetValidator sets the action validator
func (e *Executor) SetValidator(v Validator) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.validator = v
}

// RollbackManager manages rollback state
type RollbackManager struct {
	mu      sync.RWMutex
	history map[string][]*ActionRecord
	logger  *zap.Logger
}

// ActionRecord records an action for potential rollback
type ActionRecord struct {
	Action    *Action
	Result    *ActionResult
	Timestamp time.Time
}

// NewRollbackManager creates a new rollback manager
func NewRollbackManager(logger *zap.Logger) *RollbackManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RollbackManager{
		history: make(map[string][]*ActionRecord),
		logger:  logger.With(zap.String("component", "rollback")),
	}
}

// Record records an action for rollback
func (rm *RollbackManager) Record(action *Action, result *ActionResult) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	key := action.Target
	rm.history[key] = append(rm.history[key], &ActionRecord{
		Action:    action,
		Result:    result,
		Timestamp: time.Now(),
	})
}

// GetHistory gets rollback history for a target
func (rm *RollbackManager) GetHistory(target string) []*ActionRecord {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	return rm.history[target]
}

// Rollback performs a rollback
func (rm *RollbackManager) Rollback(ctx context.Context, target string) error {
	rm.mu.RLock()
	history := rm.history[target]
	rm.mu.RUnlock()

	if len(history) == 0 {
		return fmt.Errorf("no rollback history for %s", target)
	}

	// Get the most recent action
	lastAction := history[len(history)-1]

	rm.logger.Info("rolling back action",
		zap.String("target", target),
		zap.String("action_type", lastAction.Action.Type))

	// In production, would execute actual rollback logic
	return nil
}
