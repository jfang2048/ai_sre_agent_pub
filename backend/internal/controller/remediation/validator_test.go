package remediation

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestActionIntParameterRejectsUnsafeNumericConversions(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{name: "unsigned overflow", value: uint64(math.MaxUint64)},
		{name: "rounded signed overflow", value: float64(math.MaxInt64)},
		{name: "infinity", value: math.Inf(1)},
		{name: "fraction", value: 1.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := &Action{Parameters: map[string]interface{}{"replicas": tc.value}}
			_, found, err := actionIntParameter(action, "replicas")
			require.True(t, found)
			require.Error(t, err)
		})
	}
}

func TestActionIntParameterAcceptsExactWholeNumber(t *testing.T) {
	action := &Action{Parameters: map[string]interface{}{"replicas": float64(42)}}
	value, found, err := actionIntParameter(action, "replicas")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 42, value)
}

// ── SafetyRule tests ──────────────────────────────────────────────────────

func TestSafetyRuleBlocksScaleToZero(t *testing.T) {
	rule := &SafetyRule{logger: zap.NewNop()}
	action := &Action{
		Type:   "scale_deployment",
		Target: "my-deploy",
		Parameters: map[string]interface{}{
			"replicas": float64(0),
		},
	}

	err := rule.Validate(context.Background(), action)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot scale to zero")
}

func TestSafetyRuleAllowsNonZeroScale(t *testing.T) {
	rule := &SafetyRule{logger: zap.NewNop()}
	action := &Action{
		Type:   "scale_deployment",
		Target: "my-deploy",
		Parameters: map[string]interface{}{
			"replicas": float64(3),
		},
	}

	err := rule.Validate(context.Background(), action)
	assert.NoError(t, err)
}

func TestSafetyRuleRequiresCostApprovalForLargeScaleUp(t *testing.T) {
	rule := &SafetyRule{logger: zap.NewNop()}
	action := &Action{
		Type:   "scale_deployment",
		Target: "trainer-a",
		Parameters: map[string]interface{}{
			"replicas":         float64(24),
			"current_replicas": float64(8),
		},
	}

	err := rule.Validate(context.Background(), action)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cost approval")
}

func TestSafetyRuleRequiresChangeRecordForProductionAction(t *testing.T) {
	rule := &SafetyRule{logger: zap.NewNop()}
	action := &Action{
		Type:   "restart_pod",
		Target: "trainer-pod-0",
		Parameters: map[string]interface{}{
			"environment": "production",
		},
	}

	err := rule.Validate(context.Background(), action)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "change_id or compliance_ticket")
}

func TestSafetyRuleAllowsProductionActionWithComplianceTicket(t *testing.T) {
	rule := &SafetyRule{logger: zap.NewNop()}
	action := &Action{
		Type:   "restart_pod",
		Target: "trainer-pod-0",
		Parameters: map[string]interface{}{
			"environment":       "production",
			"compliance_ticket": "CAB-42",
		},
	}

	err := rule.Validate(context.Background(), action)
	assert.NoError(t, err)
}

func TestSafetyRuleBlocksDeleteProduction(t *testing.T) {
	rule := &SafetyRule{logger: zap.NewNop()}
	action := &Action{
		Type:   "delete_service",
		Target: "api-svc",
		Parameters: map[string]interface{}{
			"environment": "production",
		},
	}

	err := rule.Validate(context.Background(), action)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot delete production")
}

func TestSafetyRuleAllowsDeleteStaging(t *testing.T) {
	rule := &SafetyRule{logger: zap.NewNop()}
	action := &Action{
		Type:   "delete_service",
		Target: "api-svc",
		Parameters: map[string]interface{}{
			"environment": "staging",
		},
	}

	err := rule.Validate(context.Background(), action)
	assert.NoError(t, err)
}

func TestSafetyRulePassesUnrelatedAction(t *testing.T) {
	rule := &SafetyRule{logger: zap.NewNop()}
	action := &Action{
		Type:   "restart_pod",
		Target: "some-pod",
	}

	err := rule.Validate(context.Background(), action)
	assert.NoError(t, err)
}

// ── PermissionRule tests ────────────────────────────────────────────────

func TestPermissionRuleAllowsListed(t *testing.T) {
	rule := NewPermissionRule([]string{"restart_pod", "scale_deployment"}, zap.NewNop())
	action := &Action{Type: "restart_pod", Target: "pod-1"}

	err := rule.Validate(context.Background(), action)
	assert.NoError(t, err)
}

func TestPermissionRuleRejectsUnlisted(t *testing.T) {
	rule := NewPermissionRule([]string{"restart_pod"}, zap.NewNop())
	action := &Action{Type: "delete_service", Target: "svc-1"}

	err := rule.Validate(context.Background(), action)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
}

// ── RateLimitRule tests ─────────────────────────────────────────────────

func TestRateLimitRuleAllowsUnderLimit(t *testing.T) {
	rule := NewRateLimitRule(3, 1*time.Minute, zap.NewNop())
	ctx := context.Background()
	action := &Action{Type: "restart_pod", Target: "pod-1"}

	for i := 0; i < 3; i++ {
		err := rule.Validate(ctx, action)
		assert.NoError(t, err, "action %d should be within limit", i+1)
	}
}

func TestRateLimitRuleRejectsOverLimit(t *testing.T) {
	rule := NewRateLimitRule(2, 1*time.Minute, zap.NewNop())
	ctx := context.Background()
	action := &Action{Type: "restart_pod", Target: "pod-1"}

	// First two should pass
	require.NoError(t, rule.Validate(ctx, action))
	require.NoError(t, rule.Validate(ctx, action))

	// Third should fail
	err := rule.Validate(ctx, action)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
}

func TestRateLimitRuleIndependentTargets(t *testing.T) {
	rule := NewRateLimitRule(1, 1*time.Minute, zap.NewNop())
	ctx := context.Background()

	err := rule.Validate(ctx, &Action{Type: "restart_pod", Target: "pod-1"})
	assert.NoError(t, err, "first target should pass")

	err = rule.Validate(ctx, &Action{Type: "restart_pod", Target: "pod-2"})
	assert.NoError(t, err, "different target should have independent limit")
}

// ── ActionValidator (composite) tests ───────────────────────────────────

func TestValidatorRunsAllRules(t *testing.T) {
	v := NewActionValidator(zap.NewNop())
	v.AddRule(&SafetyRule{logger: zap.NewNop()})
	v.AddRule(NewPermissionRule([]string{"restart_pod"}, zap.NewNop()))

	// This action is allowed by both rules
	err := v.Validate(context.Background(), &Action{Type: "restart_pod", Target: "pod-1"})
	assert.NoError(t, err)
}

func TestValidatorRejectsOnAnyRuleFailure(t *testing.T) {
	v := NewActionValidator(zap.NewNop())
	v.AddRule(&SafetyRule{logger: zap.NewNop()})
	v.AddRule(NewPermissionRule([]string{"restart_pod"}, zap.NewNop()))

	// scale_deployment is not in allowed list → permission rule fails
	err := v.Validate(context.Background(), &Action{
		Type:       "scale_deployment",
		Target:     "deploy-1",
		Parameters: map[string]interface{}{"replicas": float64(3)},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission")
}

func TestValidatorCooldownBlocksRepeatedActions(t *testing.T) {
	v := NewActionValidator(zap.NewNop())

	// Record an action, then immediately try to validate another for the same target
	v.RecordAction("pod-1")

	err := v.Validate(context.Background(), &Action{Type: "restart_pod", Target: "pod-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cooldown")
}

func TestValidatorCooldownDoesNotBlockDifferentTarget(t *testing.T) {
	v := NewActionValidator(zap.NewNop())
	v.RecordAction("pod-1")

	err := v.Validate(context.Background(), &Action{Type: "restart_pod", Target: "pod-2"})
	assert.NoError(t, err, "cooldown should be per-target")
}
