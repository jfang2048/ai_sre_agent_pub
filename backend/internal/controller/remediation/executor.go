package remediation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

type Executor struct {
	mu              sync.RWMutex
	inflight        map[string]ActionExecution
	slots           chan struct{}
	config          Config
	logger          *zap.Logger
	validator       Validator
	rollbackManager *RollbackManager
}

type Config struct {
	DryRun            bool          `json:"dry_run"`
	MaxConcurrent     int           `json:"max_concurrent"`
	CooldownPeriod    time.Duration `json:"cooldown_period"`
	RequireApproval   bool          `json:"require_approval"`
	MaxActionsPerHour int           `json:"max_actions_per_hour"`
}

type ActionExecution struct {
	ID        string        `json:"id"`
	Type      string        `json:"type"`
	Target    string        `json:"target"`
	StartedAt time.Time     `json:"started_at"`
	Status    string        `json:"status"`
	Result    *ActionResult `json:"result,omitempty"`
}

type ActionResult struct {
	Success  bool             `json:"success"`
	Message  string           `json:"message"`
	Changes  []ResourceChange `json:"changes,omitempty"`
	Duration time.Duration    `json:"duration"`
	Error    error            `json:"error,omitempty"`
}

type ResourceChange struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	OldValue string `json:"old_value,omitempty"`
	NewValue string `json:"new_value,omitempty"`
}

type Validator interface {
	Validate(ctx context.Context, action *Action) error
}

type Action struct {
	Type        string                 `json:"type"`
	Target      string                 `json:"target"`
	Parameters  map[string]interface{} `json:"parameters"`
	Reason      string                 `json:"reason"`
	CanRollback bool                   `json:"can_rollback"`
}

func NewExecutor(config Config, logger *zap.Logger) *Executor {
	if logger == nil {
		logger = zap.NewNop()
	}
	var slots chan struct{}
	if config.MaxConcurrent > 0 {
		slots = make(chan struct{}, config.MaxConcurrent)
	}
	return &Executor{
		inflight:        make(map[string]ActionExecution),
		slots:           slots,
		config:          config,
		logger:          logger.With(zap.String("component", "remediation_executor")),
		rollbackManager: NewRollbackManager(logger),
	}
}

func (e *Executor) Execute(ctx context.Context, action *Action) (_ *ActionResult, err error) {
	if action == nil {
		return nil, fmt.Errorf("action is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.validator != nil {
		if err := e.validator.Validate(ctx, action); err != nil {
			return nil, fmt.Errorf("validation failed: %w", err)
		}
	}
	if !e.tryAcquireSlot() {
		return nil, fmt.Errorf("concurrency limit reached")
	}
	defer e.releaseSlot()

	execID := fmt.Sprintf("exec-%d", time.Now().UnixNano())
	startedAt := time.Now().UTC()
	e.trackStart(execID, action, startedAt)
	defer func() {
		e.trackDone(execID, err)
	}()

	result, execErr := e.run(ctx, action)
	if execErr != nil {
		err = execErr
		return nil, err
	}
	result.Duration = time.Since(startedAt)
	e.trackResult(execID, result)
	if action.CanRollback && result.Success {
		e.rollbackManager.Record(action, result)
	}
	if tracker, ok := e.validator.(*ActionValidator); ok && action.Target != "" && result.Success {
		tracker.RecordAction(action.Target)
	}
	return result, nil
}

func (e *Executor) run(ctx context.Context, action *Action) (*ActionResult, error) {
	e.logger.Info("run action",
		zap.String("type", action.Type),
		zap.String("target", action.Target),
		zap.Bool("dry_run", e.config.DryRun))
	if e.config.DryRun {
		return e.runDry(action), nil
	}
	return e.runLive(ctx, action), nil
}

func (e *Executor) runDry(action *Action) *ActionResult {
	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Dry run: would execute %s on %s", action.Type, action.Target),
		Changes: []ResourceChange{{
			Type:     action.Type,
			Name:     action.Target,
			NewValue: "[simulated]",
		}},
	}
}

func (e *Executor) runLive(ctx context.Context, action *Action) *ActionResult {
	if err := ctx.Err(); err != nil {
		return &ActionResult{Success: false, Message: err.Error(), Error: err}
	}
	switch action.Type {
	case "scale_deployment":
		return e.scaleDeployment(action)
	case "restart_pod":
		return e.restartPod(action)
	case "failover_traffic":
		return e.failoverTraffic(action)
	case "clear_cache":
		return e.clearCache(action)
	case "rollback_deployment":
		return e.rollbackDeployment(action)
	default:
		return &ActionResult{Success: false, Message: fmt.Sprintf("unknown action type: %s", action.Type)}
	}
}

func (e *Executor) scaleDeployment(action *Action) *ActionResult {
	replicas := 3
	if parsed, ok, err := actionIntParameter(action, "replicas"); err != nil {
		return &ActionResult{Success: false, Message: fmt.Sprintf("invalid replicas parameter: %v", err), Error: err}
	} else if ok {
		replicas = parsed
	}
	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Scaled %s to %d replicas", action.Target, replicas),
		Changes: []ResourceChange{{Type: "deployment", Name: action.Target, NewValue: fmt.Sprintf("%d replicas", replicas)}},
	}
}

func (e *Executor) restartPod(action *Action) *ActionResult {
	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Restarted pod %s", action.Target),
		Changes: []ResourceChange{{Type: "pod", Name: action.Target, NewValue: "restarted"}},
	}
}

func (e *Executor) failoverTraffic(action *Action) *ActionResult {
	region := ""
	if parsed, ok, err := actionStringParameter(action, "region"); err != nil {
		return &ActionResult{Success: false, Message: fmt.Sprintf("invalid region parameter: %v", err), Error: err}
	} else if ok {
		region = parsed
	}
	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Failed over traffic to %s", region),
		Changes: []ResourceChange{{Type: "dns", Name: action.Target, NewValue: region}},
	}
}

func (e *Executor) clearCache(action *Action) *ActionResult {
	return &ActionResult{Success: true, Message: fmt.Sprintf("Cleared cache for %s", action.Target)}
}

func (e *Executor) rollbackDeployment(action *Action) *ActionResult {
	return &ActionResult{Success: true, Message: fmt.Sprintf("Rolled back deployment %s", action.Target)}
}

func (e *Executor) tryAcquireSlot() bool {
	if e.slots == nil {
		return false
	}
	select {
	case e.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (e *Executor) releaseSlot() {
	if e.slots == nil {
		return
	}
	select {
	case <-e.slots:
		return
	default:
		return
	}
}

func (e *Executor) trackStart(execID string, action *Action, startedAt time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inflight[execID] = ActionExecution{
		ID:        execID,
		Type:      action.Type,
		Target:    action.Target,
		StartedAt: startedAt,
		Status:    "running",
	}
}

func (e *Executor) trackResult(execID string, result *ActionResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current, ok := e.inflight[execID]
	if !ok {
		return
	}
	copy := *result
	copy.Changes = append([]ResourceChange(nil), result.Changes...)
	current.Result = &copy
	current.Status = statusFromResult(result)
	e.inflight[execID] = current
}

func (e *Executor) trackDone(execID string, runErr error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current, ok := e.inflight[execID]
	if !ok {
		return
	}
	if runErr != nil {
		current.Status = "failed"
		e.inflight[execID] = current
	}
	delete(e.inflight, execID)
}

func statusFromResult(result *ActionResult) string {
	if result == nil {
		return "failed"
	}
	if result.Success {
		return "completed"
	}
	return "failed"
}

func (e *Executor) GetRunningActions() []*ActionExecution {
	e.mu.RLock()
	defer e.mu.RUnlock()
	actions := make([]*ActionExecution, 0, len(e.inflight))
	for _, item := range e.inflight {
		copy := item
		if item.Result != nil {
			result := *item.Result
			result.Changes = append([]ResourceChange(nil), item.Result.Changes...)
			copy.Result = &result
		}
		actions = append(actions, &copy)
	}
	return actions
}

func (e *Executor) SetValidator(v Validator) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.validator = v
}

type RollbackManager struct {
	mu      sync.RWMutex
	history map[string][]*ActionRecord
	logger  *zap.Logger
}

type ActionRecord struct {
	Action    *Action
	Result    *ActionResult
	Timestamp time.Time
}

func NewRollbackManager(logger *zap.Logger) *RollbackManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RollbackManager{
		history: make(map[string][]*ActionRecord),
		logger:  logger.With(zap.String("component", "rollback")),
	}
}

func (rm *RollbackManager) Record(action *Action, result *ActionResult) {
	if action == nil || result == nil {
		return
	}
	record := &ActionRecord{Action: action, Result: result, Timestamp: time.Now().UTC()}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.history[action.Target] = append(rm.history[action.Target], record)
}

func (rm *RollbackManager) GetHistory(target string) []*ActionRecord {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	src := rm.history[target]
	out := make([]*ActionRecord, 0, len(src))
	for _, item := range src {
		if item == nil {
			continue
		}
		copy := *item
		out = append(out, &copy)
	}
	return out
}

func (rm *RollbackManager) Rollback(ctx context.Context, target string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rm.mu.RLock()
	history := rm.history[target]
	rm.mu.RUnlock()
	if len(history) == 0 {
		return fmt.Errorf("no rollback history for %s", target)
	}
	last := history[len(history)-1]
	rm.logger.Info("rollback action", zap.String("target", target), zap.String("action_type", last.Action.Type))
	return nil
}
