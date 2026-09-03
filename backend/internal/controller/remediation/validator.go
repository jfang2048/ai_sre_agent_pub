package remediation

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/safeconv"
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
	if logger == nil {
		logger = zap.NewNop()
	}
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
	if action == nil {
		return fmt.Errorf("action is required")
	}
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
	if action == nil {
		return fmt.Errorf("action is required")
	}

	environment, _, _ := actionStringParameter(action, "environment")

	// Don't allow scaling to zero
	if action.Type == "scale_deployment" {
		if replicas, ok, err := actionIntParameter(action, "replicas"); err != nil {
			return err
		} else if ok {
			if replicas == 0 {
				return fmt.Errorf("cannot scale to zero replicas")
			}
			maxSafeReplicas := 16
			if configured, ok, err := actionIntParameter(action, "max_safe_replicas"); err != nil {
				return err
			} else if ok && configured > 0 {
				maxSafeReplicas = configured
			}
			costApproved, _, err := actionBoolParameter(action, "cost_approved")
			if err != nil {
				return err
			}
			if replicas > maxSafeReplicas && !costApproved {
				return fmt.Errorf("scale to %d replicas exceeds safe cap %d without cost approval", replicas, maxSafeReplicas)
			}
			if currentReplicas, ok, err := actionIntParameter(action, "current_replicas"); err != nil {
				return err
			} else if ok && currentReplicas > 0 && replicas > currentReplicas*2 && !costApproved {
				return fmt.Errorf("scale from %d to %d replicas requires cost approval", currentReplicas, replicas)
			}
		}
	}

	// Don't allow deleting production services
	if action.Type == "delete_service" {
		if strings.EqualFold(environment, "production") {
			return fmt.Errorf("cannot delete production service")
		}
	}

	if strings.EqualFold(environment, "production") && requiresProductionChangeRecord(action.Type) {
		changeID, _, _ := actionStringParameter(action, "change_id")
		complianceTicket, _, _ := actionStringParameter(action, "compliance_ticket")
		if strings.TrimSpace(changeID) == "" && strings.TrimSpace(complianceTicket) == "" {
			return fmt.Errorf("production action requires change_id or compliance_ticket")
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
	if action == nil {
		return fmt.Errorf("action is required")
	}
	if !r.allowedActions[action.Type] {
		return fmt.Errorf("action type %s is not allowed", action.Type)
	}
	return nil
}

// Name returns the rule name
func (r *PermissionRule) Name() string {
	return "permission"
}

func requiresProductionChangeRecord(actionType string) bool {
	switch strings.TrimSpace(strings.ToLower(actionType)) {
	case "scale_deployment", "restart_pod", "failover_traffic", "rollback_deployment", "delete_service":
		return true
	default:
		return false
	}
}

func actionIntParameter(action *Action, key string) (int, bool, error) {
	if action == nil || action.Parameters == nil {
		return 0, false, nil
	}
	raw, ok := action.Parameters[key]
	if !ok {
		return 0, false, nil
	}
	switch value := raw.(type) {
	case int:
		return value, true, nil
	case int8:
		return safeconv.Int64ToInt(int64(value)), true, nil
	case int16:
		return safeconv.Int64ToInt(int64(value)), true, nil
	case int32:
		return safeconv.Int64ToInt(int64(value)), true, nil
	case int64:
		converted, err := checkedSignedInt(value, key)
		return converted, true, err
	case uint:
		converted, err := checkedUnsignedInt(uint64(value), key)
		return converted, true, err
	case uint8:
		return safeconv.Uint64ToInt(uint64(value)), true, nil
	case uint16:
		return safeconv.Uint64ToInt(uint64(value)), true, nil
	case uint32:
		converted, err := checkedUnsignedInt(uint64(value), key)
		return converted, true, err
	case uint64:
		converted, err := checkedUnsignedInt(value, key)
		return converted, true, err
	case float32:
		converted, err := checkedFloatInt(float64(value), key)
		return converted, true, err
	case float64:
		converted, err := checkedFloatInt(value, key)
		return converted, true, err
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, true, fmt.Errorf("%s must be an integer: %w", key, err)
		}
		return parsed, true, nil
	default:
		return 0, true, fmt.Errorf("%s has unsupported type %T", key, raw)
	}
}

func checkedSignedInt(value int64, key string) (int, error) {
	if value > int64(math.MaxInt) || value < int64(math.MinInt) {
		return 0, fmt.Errorf("%s is outside the supported integer range", key)
	}
	return safeconv.Int64ToInt(value), nil
}

func checkedUnsignedInt(value uint64, key string) (int, error) {
	if value > uint64(math.MaxInt) {
		return 0, fmt.Errorf("%s is outside the supported integer range", key)
	}
	return safeconv.Uint64ToInt(value), nil
}

func checkedFloatInt(value float64, key string) (int, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
		return 0, fmt.Errorf("%s must be a finite whole number", key)
	}
	// The valid integer interval is [MinInt, MaxInt+1). Expressing the upper
	// bound through -MinInt avoids float64 rounding MaxInt up to 2^63 on
	// 64-bit platforms.
	if value < float64(math.MinInt) || value >= -float64(math.MinInt) {
		return 0, fmt.Errorf("%s is outside the supported integer range", key)
	}
	return int(value), nil // #nosec G115 -- finite value is bounded by the platform int limits.
}

func actionStringParameter(action *Action, key string) (string, bool, error) {
	if action == nil || action.Parameters == nil {
		return "", false, nil
	}
	raw, ok := action.Parameters[key]
	if !ok {
		return "", false, nil
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value), true, nil
	case []byte:
		return strings.TrimSpace(string(value)), true, nil
	default:
		return "", true, fmt.Errorf("%s has unsupported type %T", key, raw)
	}
}

func actionBoolParameter(action *Action, key string) (bool, bool, error) {
	if action == nil || action.Parameters == nil {
		return false, false, nil
	}
	raw, ok := action.Parameters[key]
	if !ok {
		return false, false, nil
	}
	switch value := raw.(type) {
	case bool:
		return value, true, nil
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return false, true, fmt.Errorf("%s must be a boolean: %w", key, err)
		}
		return parsed, true, nil
	default:
		return false, true, fmt.Errorf("%s has unsupported type %T", key, raw)
	}
}
