package remediation

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ActionValidator validates actions before execution
type ActionValidator struct {
	rules       []ValidationRule
	logger      *zap.Logger
	cooldowns   map[string]time.Time
	cooldownsMu sync.RWMutex
}

// ValidationRule validates an action
type ValidationRule interface {
	Validate(ctx context.Context, action *Action) error
	Name() string
}

// NewActionValidator creates a new action validator
func NewActionValidator(logger *zap.Logger) *ActionValidator {
	return &ActionValidator{
		logger:    logger.With(zap.String("component", "validator")),
		cooldowns: make(map[string]time.Time),
	}
}

// AddRule adds a validation rule
func (v *ActionValidator) AddRule(rule ValidationRule) {
	v.rules = append(v.rules, rule)
}

// Validate validates an action against all rules
func (v *ActionValidator) Validate(ctx context.Context, action *Action) error {
	// Check cooldown
	if v.isInCooldown(action.Target) {
		return fmt.Errorf("target %s is in cooldown period", action.Target)
	}

	// Run all validation rules
	for _, rule := range v.rules {
		if err := rule.Validate(ctx, action); err != nil {
			return fmt.Errorf("%s: %w", rule.Name(), err)
		}
	}

	return nil
}

// isInCooldown checks if target is in cooldown
func (v *ActionValidator) isInCooldown(target string) bool {
	v.cooldownsMu.RLock()
	defer v.cooldownsMu.RUnlock()

	if lastAction, ok := v.cooldowns[target]; ok {
		if time.Since(lastAction) < 5*time.Minute {
			return true
		}
	}
	return false
}

// RecordAction records an action for cooldown tracking
func (v *ActionValidator) RecordAction(target string) {
	v.cooldownsMu.Lock()
	defer v.cooldownsMu.Unlock()
	v.cooldowns[target] = time.Now()
}

// Common validation rules

// SafetyRule ensures safety constraints are met
type SafetyRule struct {
	logger *zap.Logger
}

// Validate checks safety constraints
func (r *SafetyRule) Validate(ctx context.Context, action *Action) error {
	// Don't allow scaling to zero
	if action.Type == "scale_deployment" {
		if replicas, ok := action.Parameters["replicas"]; ok {
			if int(replicas.(float64)) == 0 {
				return fmt.Errorf("cannot scale to zero replicas")
			}
		}
	}

	// Don't allow deleting production services
	if action.Type == "delete_service" {
		if env, ok := action.Parameters["environment"]; ok {
			if env == "production" {
				return fmt.Errorf("cannot delete production service")
			}
		}
	}

	return nil
}

// Name returns the rule name
func (r *SafetyRule) Name() string {
	return "safety"
}

// RateLimitRule checks rate limits
type RateLimitRule struct {
	maxActions int
	window     time.Duration
	history    map[string][]time.Time
	logger     *zap.Logger
	mu         sync.RWMutex
}

// NewRateLimitRule creates a new rate limit rule
func NewRateLimitRule(maxActions int, window time.Duration, logger *zap.Logger) *RateLimitRule {
	return &RateLimitRule{
		maxActions: maxActions,
		window:     window,
		history:    make(map[string][]time.Time),
		logger:     logger,
	}
}

// Validate checks rate limits
func (r *RateLimitRule) Validate(ctx context.Context, action *Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	key := action.Target

	// Clean old history
	cutoff := now.Add(-r.window)
	var valid []time.Time
	for _, t := range r.history[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	r.history[key] = valid

	// Check limit
	if len(r.history[key]) >= r.maxActions {
		return fmt.Errorf("rate limit exceeded: %d actions in %v for %s",
			len(r.history[key]), r.window, key)
	}

	// Record this action
	r.history[key] = append(r.history[key], now)
	return nil
}

// Name returns the rule name
func (r *RateLimitRule) Name() string {
	return "rate_limit"
}

// PermissionRule checks if action is permitted
type PermissionRule struct {
	allowedActions map[string]bool
	logger         *zap.Logger
}

// NewPermissionRule creates a new permission rule
func NewPermissionRule(allowedActions []string, logger *zap.Logger) *PermissionRule {
	allowed := make(map[string]bool)
	for _, a := range allowedActions {
		allowed[a] = true
	}
	return &PermissionRule{
		allowedActions: allowed,
		logger:         logger,
	}
}

// Validate checks permissions
func (r *PermissionRule) Validate(ctx context.Context, action *Action) error {
	if !r.allowedActions[action.Type] {
		return fmt.Errorf("action type %s is not allowed", action.Type)
	}
	return nil
}

// Name returns the rule name
func (r *PermissionRule) Name() string {
	return "permission"
}
