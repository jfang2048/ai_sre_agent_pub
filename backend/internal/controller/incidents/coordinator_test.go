package incidents

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/analysis"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestNewCoordinator validates coordinator initialization
func TestNewCoordinator(t *testing.T) {
	testCases := []struct {
		name         string
		cfg          Config
		orchestrator *Orchestrator
		analysis     *analysis.Engine
		sink         func(AggregatedContext)
		logger       *zap.Logger
	}{
		{
			name:         "full coordinator",
			cfg:          DefaultConfig(),
			orchestrator: &Orchestrator{},
			analysis:     &analysis.Engine{},
			sink: func(ctx AggregatedContext) {
				// Sink function
			},
			logger: zap.NewNop(),
		},
		{
			name:         "coordinator without analysis",
			cfg:          DefaultConfig(),
			orchestrator: &Orchestrator{},
			analysis:     nil,
			sink:         func(ctx AggregatedContext) {},
			logger:       zap.NewNop(),
		},
		{
			name:         "coordinator with nil logger",
			cfg:          DefaultConfig(),
			orchestrator: &Orchestrator{},
			analysis:     nil,
			sink:         nil,
			logger:       nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			coord := NewCoordinator(tc.cfg, tc.orchestrator, tc.analysis, tc.sink, tc.logger)

			require.NotNil(t, coord)
			require.NotNil(t, coord.logger)
			require.NotNil(t, coord.seen)
			require.NotNil(t, coord.shutdown)
			require.False(t, coord.running)
		})
	}
}

// TestCoordinatorStartStop validates coordinator lifecycle
func TestCoordinatorStartStop(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PollInterval = 100 * time.Millisecond // Short interval for testing

	coord := NewCoordinator(cfg, nil, nil, nil, zap.NewNop())
	require.NotNil(t, coord)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start coordinator
	coord.Start(ctx)

	// Verify running state
	require.True(t, coord.running)

	// Stop coordinator
	coord.Stop()

	// Verify stopped state
	require.False(t, coord.running)
}

// TestCoordinatorIdempotentStart validates multiple start calls
func TestCoordinatorIdempotentStart(t *testing.T) {
	cfg := DefaultConfig()
	coord := NewCoordinator(cfg, nil, nil, nil, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start multiple times
	coord.Start(ctx)
	coord.Start(ctx)
	coord.Start(ctx)

	// Should still be running
	require.True(t, coord.running)

	// Stop should still work
	coord.Stop()
	require.False(t, coord.running)
}

// TestCoordinatorIdempotentStop validates multiple stop calls
func TestCoordinatorIdempotentStop(t *testing.T) {
	cfg := DefaultConfig()
	coord := NewCoordinator(cfg, nil, nil, nil, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	coord.Start(ctx)
	require.True(t, coord.running)

	// Stop multiple times
	coord.Stop()
	coord.Stop()
	coord.Stop()

	// Should still be stopped
	require.False(t, coord.running)
}

// TestCoordinatorHandleExternalAlert validates external alert handling
func TestCoordinatorHandleExternalAlert(t *testing.T) {
	sink := func(ctx AggregatedContext) {
		// Sink function
	}

	store := ingest.NewMemoryStore()
	now := time.Now()

	// Add some data to store
	store.StoreMetrics("test-node", []*telemetryv1.Metric{
		{Name: "cpu_usage", Value: 85.0},
	}, now)

	orchestrator, err := NewOrchestrator(DefaultConfig(), store, zap.NewNop())
	require.NoError(t, err)

	coord := NewCoordinator(DefaultConfig(), orchestrator, nil, sink, zap.NewNop())

	alert := InputAlert{
		ID:       "external-alert-1",
		Title:    "Test Alert",
		Service:  "test-service",
		Severity: "P1",
		StartsAt: now.Add(-5 * time.Minute),
		Labels:   map[string]string{"service": "test-service"},
	}

	ctx, err := coord.HandleExternalAlert(context.Background(), alert)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.Equal(t, "external-alert-1", ctx.AlertID)
}

// TestCoordinatorHandleExternalAlertWithoutID generates ID
func TestCoordinatorHandleExternalAlertWithoutID(t *testing.T) {
	sink := func(ctx AggregatedContext) {
		// Sink function
	}

	orchestrator, err := NewOrchestrator(DefaultConfig(), ingest.NewMemoryStore(), zap.NewNop())
	require.NoError(t, err)

	coord := NewCoordinator(DefaultConfig(), orchestrator, nil, sink, zap.NewNop())

	alert := InputAlert{
		Title:    "Test Alert",
		Service:  "test-service",
		Severity: "P1",
		StartsAt: time.Now(),
	}

	ctx, err := coord.HandleExternalAlert(context.Background(), alert)
	require.NoError(t, err)
	require.NotNil(t, ctx)
	require.NotEmpty(t, ctx.AlertID)
	// ID should start with "ext-"
	require.Contains(t, ctx.AlertID, "ext-")
}

// TestCoordinatorAlreadySeen validates seen tracking
func TestCoordinatorAlreadySeen(t *testing.T) {
	coord := NewCoordinator(DefaultConfig(), nil, nil, nil, zap.NewNop())

	// Initially not seen
	require.False(t, coord.alreadySeen("alert-1"))

	// Mark as seen
	coord.markSeen("alert-1")

	// Now should be seen
	require.True(t, coord.alreadySeen("alert-1"))

	// Different alert still not seen
	require.False(t, coord.alreadySeen("alert-2"))
}

// TestCoordinatorConcurrentMarkSeen validates concurrent seen tracking
func TestCoordinatorConcurrentMarkSeen(t *testing.T) {
	coord := NewCoordinator(DefaultConfig(), nil, nil, nil, zap.NewNop())

	const numGoroutines = 50
	var wg sync.WaitGroup

	// Concurrently mark alerts as seen
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			alertID := "alert-" + string(rune('A'+id%26))
			coord.markSeen(alertID)
		}(i)
	}

	wg.Wait()

	// Verify some alerts were marked
	seenCount := 0
	for i := 0; i < numGoroutines; i++ {
		alertID := "alert-" + string(rune('A'+i%26))
		if coord.alreadySeen(alertID) {
			seenCount++
		}
	}

	require.Greater(t, seenCount, 0, "at least some alerts should be marked as seen")
}

// TestCoordinatorSinkInvocation validates sink is called
func TestCoordinatorSinkInvocation(t *testing.T) {
	sinkCalled := make(chan bool, 1)
	var receivedCtx AggregatedContext

	sink := func(ctx AggregatedContext) {
		receivedCtx = ctx
		sinkCalled <- true
	}

	store := ingest.NewMemoryStore()
	now := time.Now()

	store.StoreMetrics("test", []*telemetryv1.Metric{
		{Name: "cpu", Value: 90.0},
	}, now)

	orchestrator, err := NewOrchestrator(DefaultConfig(), store, zap.NewNop())
	require.NoError(t, err)

	coord := NewCoordinator(DefaultConfig(), orchestrator, nil, sink, zap.NewNop())

	alert := InputAlert{
		ID:       "alert-1",
		Title:    "Test Alert",
		Service:  "test",
		Severity: "P1",
		StartsAt: now,
	}

	_, err = coord.HandleExternalAlert(context.Background(), alert)
	require.NoError(t, err)

	// Wait for sink to be called
	select {
	case <-sinkCalled:
		// Verify context
		require.NotEmpty(t, receivedCtx.IncidentID)
		require.NotEmpty(t, receivedCtx.AlertID)
	case <-time.After(1 * time.Second):
		t.Fatal("sink was not called within timeout")
	}
}

// TestCoordinatorNilOrchestrator graceful degradation
func TestCoordinatorNilOrchestrator(t *testing.T) {
	sinkCalled := false

	sink := func(ctx AggregatedContext) {
		sinkCalled = true
	}

	// Coordinator with nil orchestrator
	coord := NewCoordinator(DefaultConfig(), nil, nil, sink, zap.NewNop())

	alert := InputAlert{
		ID:       "alert-1",
		Title:    "Test Alert",
		Service:  "test",
		Severity: "P1",
		StartsAt: time.Now(),
	}

	ctx, err := coord.HandleExternalAlert(context.Background(), alert)
	require.NoError(t, err)
	require.Nil(t, ctx)          // Should return nil context with nil orchestrator
	require.False(t, sinkCalled) // Sink should not be called
}

// TestCoordinatorContextCancellation validates context cancellation
func TestCoordinatorContextCancellation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PollInterval = 50 * time.Millisecond

	orchestrator, err := NewOrchestrator(cfg, ingest.NewMemoryStore(), zap.NewNop())
	require.NoError(t, err)

	coord := NewCoordinator(cfg, orchestrator, nil, nil, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	coord.Start(ctx)

	// Let it run briefly
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Stop coordinator explicitly
	coord.Stop()

	// Wait for shutdown
	select {
	case <-coord.shutdown:
		// Gracefully shutdown
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not shutdown within timeout")
	}

	// Coordinator should be stopped
	require.False(t, coord.running)
}

// TestCoordinatorConcurrentStartStop validates concurrent lifecycle operations
func TestCoordinatorConcurrentStartStop(t *testing.T) {
	cfg := DefaultConfig()
	coord := NewCoordinator(cfg, nil, nil, nil, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Concurrent start operations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			coord.Start(ctx)
		}()
	}

	wg.Wait()
	require.True(t, coord.running)

	// Concurrent stop operations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			coord.Stop()
		}()
	}

	wg.Wait()
	require.False(t, coord.running)
}

// TestCoordinatorPollWithDisabledConfig validates poll respects enabled flag
func TestCoordinatorPollWithDisabledConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false // Disabled

	coord := NewCoordinator(cfg, nil, nil, nil, zap.NewNop())

	// Poll should return immediately without error
	coord.poll()

	// Should not crash or hang
	require.False(t, coord.running)
}

// TestCoordinatorSeenTrackingPersistence validates seen map persists
func TestCoordinatorSeenTrackingPersistence(t *testing.T) {
	coord := NewCoordinator(DefaultConfig(), nil, nil, nil, zap.NewNop())

	// Mark alerts as seen
	coord.markSeen("alert-1")
	coord.markSeen("alert-2")
	coord.markSeen("alert-3")

	// Verify all are marked
	require.True(t, coord.alreadySeen("alert-1"))
	require.True(t, coord.alreadySeen("alert-2"))
	require.True(t, coord.alreadySeen("alert-3"))

	// Mark one again (idempotent)
	coord.markSeen("alert-1")

	// Still should be seen
	require.True(t, coord.alreadySeen("alert-1"))
}

// TestCoordinatorHandleMultipleAlertsInSequence validates sequential alert handling
func TestCoordinatorHandleMultipleAlertsInSequence(t *testing.T) {
	alertCount := 0
	var mu sync.Mutex

	sink := func(ctx AggregatedContext) {
		mu.Lock()
		defer mu.Unlock()
		alertCount++
	}

	store := ingest.NewMemoryStore()
	now := time.Now()

	orchestrator, err := NewOrchestrator(DefaultConfig(), store, zap.NewNop())
	require.NoError(t, err)

	coord := NewCoordinator(DefaultConfig(), orchestrator, nil, sink, zap.NewNop())

	// Handle multiple alerts
	for i := 0; i < 5; i++ {
		alert := InputAlert{
			ID:       "alert-" + string(rune('1'+i)),
			Title:    "Test Alert",
			Service:  "test",
			Severity: "P1",
			StartsAt: now,
		}

		_, err := coord.HandleExternalAlert(context.Background(), alert)
		require.NoError(t, err)
	}

	// Give time for all sinks to be called
	time.Sleep(100 * time.Millisecond)

	// All alerts should be processed
	require.Equal(t, 5, alertCount)
}
