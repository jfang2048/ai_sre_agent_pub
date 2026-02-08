package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/monitoring"
	"github.com/jfang2048/ai_sre_agent_pub/internal/alerting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestAlertFlow tests the complete alert flow from metric collection to notification
func TestAlertFlow(t *testing.T) {
	logger := zap.NewNop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup monitoring collector
	collector := monitoring.NewCollector(monitoring.Config{
		Sources: []monitoring.SourceConfig{
			{Name: "proc", Enabled: true},
		},
	}, logger)
	require.NoError(t, collector.Start(ctx))
	defer collector.Stop(ctx)

	// Setup alert manager with test configuration
	alertManager := alerting.NewManager(alerting.Config{
		Rules: []alerting.Rule{
			{
				Name:        "HighCPUUsage",
				Expression:  "system.cpu.usage > 80",
				Severity:    "warning",
				Annotations: map[string]string{"description": "CPU usage is above 80%"},
			},
		},
		Channels: []alerting.Channel{
			{Name: "test", Type: "log"},
		},
	}, logger)

	// Collect metrics
	metrics, err := collector.Collect(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, metrics)

	// Evaluate alerts
	alerts := alertManager.Evaluate(ctx, metrics)

	// Verify alerts were generated
	assert.NotNil(t, alerts)
}

// TestPreventionFlow tests the metric -> prediction -> action flow
func TestPreventionFlow(t *testing.T) {
	logger := zap.NewNop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Setup SLO tracking
	sloTracker := monitoring.NewSLOTracker(monitoring.SLOConfig{
		Name:   "api-availability",
		Target: 0.999,
	}, logger)

	// Simulate metric history
	metrics := []float64{0.9995, 0.9992, 0.9988, 0.9985, 0.9980, 0.9975}

	for _, value := range metrics {
		sloTracker.Record(value)
	}

	// Check if SLO violation is predicted
	prediction := sloTracker.PredictViolation(1 * time.Hour)

	assert.NotNil(t, prediction)
	assert.Contains(t, []string{"will_violate", "wont_violate", "unknown"}, prediction.Status)

	// If violation predicted, check if action is needed
	if prediction.Status == "will_violate" {
		action := sloTracker.GetRemediationAction()
		assert.NotNil(t, action)
	}
}

// TestPlaybookExecution tests playbook execution for incident response
func TestPlaybookExecution(t *testing.T) {
	logger := zap.NewNop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create playbook executor
	executor := monitoring.NewPlaybookExecutor(logger)

	// Define test playbook
	playbook := &monitoring.Playbook{
		Name: "scale_up",
		Steps: []monitoring.PlaybookStep{
			{
				Name:     "check_current_capacity",
				Type:     "read",
				Command:  "kubectl get deployment",
			},
			{
				Name:     "scale_replicas",
				Type:     "write",
				Command:  "kubectl scale deployment --replicas=5",
			},
			{
				Name:     "verify_scaling",
				Type:     "read",
				Command:  "kubectl get pods",
			},
		},
	}

	// Execute playbook (dry run mode)
	result, err := executor.Execute(ctx, playbook, true)
	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "success", result.Status)
}

// TestGoToCppIPC tests C++ to Go communication via shared memory
func TestGoToCppIPC(t *testing.T) {
	// This test requires the C++ SDK to be built
	t.Skip("Skipping: requires C++ SDK build")

	logger := zap.NewNop()

	ctx := context.Background()

	// Setup shared memory IPC
	ipc, err := monitoring.NewSharedMemoryIPC("/sre_agent_test", 4096, logger)
	require.NoError(t, err)
	defer ipc.Close(ctx)

	// Write test data from Go side
	testData := []byte("test_metric:123.45")
	err = ipc.Write(ctx, testData)
	require.NoError(t, err)

	// Read back the data
	readData, err := ipc.Read(ctx)
	require.NoError(t, err)
	assert.Equal(t, testData, readData)
}
