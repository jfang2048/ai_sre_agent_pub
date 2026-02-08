package go_test

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgentLifecycle(t *testing.T) {
	logger := zap.NewNop()

	config := &core.Config{
		Name:           "test-agent",
		MetricsPort:    9090,
		CollectionInterval: 10 * time.Second,
	}

	agent := core.NewAgent(config, logger)
	assert.NotNil(t, agent)
	assert.Equal(t, "test-agent", agent.Name())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test initialization
	err := agent.Initialize(ctx)
	require.NoError(t, err)

	// Test starting
	err = agent.Start(ctx)
	require.NoError(t, err)

	// Verify agent is running
	assert.True(t, agent.IsRunning())

	// Test stopping
	err = agent.Stop(ctx)
	require.NoError(t, err)

	// Verify agent is stopped
	assert.False(t, agent.IsRunning())
}

func TestAgentStateTransitions(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name        string
		fromState   core.State
		toState     core.State
		shouldError bool
	}{
		{
			name:        "idle to initializing",
			fromState:   core.StateIdle,
			toState:     core.StateInitializing,
			shouldError: false,
		},
		{
			name:        "initializing to running",
			fromState:   core.StateInitializing,
			toState:     core.StateRunning,
			shouldError: false,
		},
		{
			name:        "running to stopping",
			fromState:   core.StateRunning,
			toState:     core.StateStopping,
			shouldError: false,
		},
		{
			name:        "stopping to stopped",
			fromState:   core.StateStopping,
			toState:     core.StateStopped,
			shouldError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := core.NewAgent(&core.Config{Name: "test"}, logger)

			// Set initial state
			agent.SetState(tt.fromState)

			// Try to transition
			err := agent.TransitionTo(tt.toState)

			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.toState, agent.GetState())
			}
		})
	}
}

func TestAgentHealthCheck(t *testing.T) {
	logger := zap.NewNop()
	config := &core.Config{
		Name:           "health-test-agent",
		MetricsPort:    9091,
		CollectionInterval: 10 * time.Second,
	}

	agent := core.NewAgent(config, logger)
	ctx := context.Background()

	// Initialize agent
	err := agent.Initialize(ctx)
	require.NoError(t, err)

	// Check health before start
	health := agent.HealthCheck(ctx)
	assert.Equal(t, core.HealthStatusStopped, health.Status)

	// Start agent
	err = agent.Start(ctx)
	require.NoError(t, err)

	// Check health after start
	health = agent.HealthCheck(ctx)
	assert.Equal(t, core.HealthStatusHealthy, health.Status)

	// Stop agent
	err = agent.Stop(ctx)
	require.NoError(t, err)

	// Check health after stop
	health = agent.HealthCheck(ctx)
	assert.Equal(t, core.HealthStatusStopped, health.Status)
}

func TestAgentErrorHandling(t *testing.T) {
	logger := zap.NewNop()

	// Test with invalid configuration
	invalidConfig := &core.Config{
		Name:           "",
		MetricsPort:    -1,
		CollectionInterval: 0,
	}

	agent := core.NewAgent(invalidConfig, logger)
	ctx := context.Background()

	err := agent.Initialize(ctx)
	assert.Error(t, err)
}

func TestAgentMetricCollection(t *testing.T) {
	logger := zap.NewNop()
	config := &core.Config{
		Name:           "collection-test-agent",
		MetricsPort:    9092,
		CollectionInterval: 100 * time.Millisecond,
		Collectors: []string{"proc", "ebpf"},
	}

	agent := core.NewAgent(config, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := agent.Initialize(ctx)
	require.NoError(t, err)

	err = agent.Start(ctx)
	require.NoError(t, err)
	defer agent.Stop(ctx)

	// Wait for some collections
	time.Sleep(500 * time.Millisecond)

	// Get collected metrics
	metrics := agent.GetMetrics()
	assert.NotEmpty(t, metrics)
}
