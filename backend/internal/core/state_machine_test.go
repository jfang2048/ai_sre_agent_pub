package core

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestStateString validates state string representations
func TestStateString(t *testing.T) {
	testCases := []struct {
		state    State
		expected string
	}{
		{StateUnknown, "unknown"},
		{StateStopped, "stopped"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateStopping, "stopping"},
		{StateFailed, "failed"},
		{State(99), "unknown"}, // Invalid state
	}

	for _, tc := range testCases {
		t.Run(tc.expected, func(t *testing.T) {
			result := tc.state.String()
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestNewMachine validates machine creation
func TestNewMachine(t *testing.T) {
	logger := zap.NewNop()
	m := NewMachine(logger)

	require.NotNil(t, m)
	require.Equal(t, StateStopped, m.Current())
	require.True(t, m.StartedAt().IsZero())
}

// TestMachineCurrent validates current state retrieval
func TestMachineCurrent(t *testing.T) {
	m := NewMachine(zap.NewNop())

	// Initial state
	require.Equal(t, StateStopped, m.Current())

	// Transition to starting
	m.Transition(StateStarting)
	require.Equal(t, StateStarting, m.Current())

	// Transition to running
	m.Transition(StateRunning)
	require.Equal(t, StateRunning, m.Current())
}

// TestMachineStartedAt validates started time tracking
func TestMachineStartedAt(t *testing.T) {
	m := NewMachine(zap.NewNop())

	// Initially zero
	require.True(t, m.StartedAt().IsZero())

	// Transition to running should set started time
	m.Transition(StateStarting)
	m.Transition(StateRunning)

	startedAt := m.StartedAt()
	require.False(t, startedAt.IsZero())
	require.True(t, time.Since(startedAt) < time.Second)

	// Stop and restart - startedAt is not cleared (only set if zero)
	m.Transition(StateStopping)
	m.Transition(StateStopped)
	m.Transition(StateStarting)
	m.Transition(StateRunning)

	newStartedAt := m.StartedAt()
	require.False(t, newStartedAt.IsZero())
	// startedAt is the same because it's only set once
	require.Equal(t, startedAt, newStartedAt)
}

// TestMachineTransitionValidTransitions validates all valid state transitions
func TestMachineTransitionValidTransitions(t *testing.T) {
	validTransitions := []struct {
		from State
		to   State
	}{
		// From stopped
		{StateStopped, StateStarting},
		// From starting
		{StateStarting, StateRunning},
		{StateStarting, StateFailed},
		// From running
		{StateRunning, StateStopping},
		{StateRunning, StateFailed},
		// From stopping
		{StateStopping, StateStopped},
		// From failed
		{StateFailed, StateStopped},
		{StateFailed, StateStarting},
	}

	for _, tc := range validTransitions {
		t.Run(tc.from.String()+"_to_"+tc.to.String(), func(t *testing.T) {
			m := NewMachine(zap.NewNop())
			m.current = tc.from // Set initial state directly for testing

			m.Transition(tc.to)

			require.Equal(t, tc.to, m.Current())
		})
	}
}

// TestMachineTransitionInvalidTransitions validates invalid transitions are rejected
func TestMachineTransitionInvalidTransitions(t *testing.T) {
	invalidTransitions := []struct {
		from State
		to   State
	}{
		// From stopped (can only go to starting)
		{StateStopped, StateRunning},
		{StateStopped, StateStopping},
		{StateStopped, StateFailed},
		// From running (can't go back to starting)
		{StateRunning, StateStarting},
		{StateRunning, StateStopped},
		// From stopping (can only go to stopped)
		{StateStopping, StateStarting},
		{StateStopping, StateRunning},
		{StateStopping, StateFailed},
	}

	for _, tc := range invalidTransitions {
		t.Run(tc.from.String()+"_to_"+tc.to.String(), func(t *testing.T) {
			m := NewMachine(zap.NewNop())
			m.current = tc.from // Set initial state directly for testing

			m.Transition(tc.to)

			// State should not change for invalid transitions
			require.Equal(t, tc.from, m.Current())
		})
	}
}

// TestMachineTransitionIdempotent validates same-state transitions are idempotent
func TestMachineTransitionIdempotent(t *testing.T) {
	m := NewMachine(zap.NewNop())

	states := []State{
		StateStopped,
		StateStarting,
		StateRunning,
		StateStopping,
		StateFailed,
	}

	for _, state := range states {
		t.Run(state.String(), func(t *testing.T) {
			m.current = state
			initialState := m.Current()

			// Transition to same state multiple times
			m.Transition(state)
			require.Equal(t, initialState, m.Current())

			m.Transition(state)
			require.Equal(t, initialState, m.Current())

			m.Transition(state)
			require.Equal(t, initialState, m.Current())
		})
	}
}

// TestMachineTransitionFullLifecycle validates complete lifecycle
func TestMachineTransitionFullLifecycle(t *testing.T) {
	m := NewMachine(zap.NewNop())

	// Stopped -> Starting -> Running
	m.Transition(StateStarting)
	require.Equal(t, StateStarting, m.Current())
	m.Transition(StateRunning)
	require.Equal(t, StateRunning, m.Current())

	// Running -> Stopping -> Stopped
	m.Transition(StateStopping)
	require.Equal(t, StateStopping, m.Current())
	m.Transition(StateStopped)
	require.Equal(t, StateStopped, m.Current())

	// StartedAt is preserved after stopping (only set once when zero)
	startedAt := m.StartedAt()
	require.False(t, startedAt.IsZero())

	// Restart cycle
	m.Transition(StateStarting)
	m.Transition(StateRunning)
	startedAt = m.StartedAt()
	require.False(t, startedAt.IsZero())
}

// TestMachineTransitionFailureRecovery validates failure recovery
func TestMachineTransitionFailureRecovery(t *testing.T) {
	m := NewMachine(zap.NewNop())

	// Stopped -> Starting -> Failed
	m.Transition(StateStarting)
	m.Transition(StateFailed)
	require.Equal(t, StateFailed, m.Current())

	// Failed -> Stopped
	m.Transition(StateStopped)
	require.Equal(t, StateStopped, m.Current())

	// Failed -> Starting (retry)
	m.Transition(StateStarting)
	m.Transition(StateRunning)
	require.Equal(t, StateRunning, m.Current())
}

// TestMachineConcurrentTransitions validates concurrent state transitions
func TestMachineConcurrentTransitions(t *testing.T) {
	m := NewMachine(zap.NewNop())

	const numGoroutines = 50
	var wg sync.WaitGroup

	// All goroutines try to transition from stopped to starting
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			m.Transition(StateStarting)
		}(i)
	}

	wg.Wait()

	// Should be in starting state (one of the transitions succeeded)
	state := m.Current()
	// Either still in starting or moved beyond
	require.Contains(t, []State{StateStarting, StateRunning, StateFailed}, state)
}

// TestMachineConcurrentReads validates concurrent reads during transitions
func TestMachineConcurrentReads(t *testing.T) {
	m := NewMachine(zap.NewNop())

	const numGoroutines = 100
	var wg sync.WaitGroup

	// Concurrently read and transition
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if id%2 == 0 {
				// Even: read current state
				_ = m.Current()
			} else {
				// Odd: transition state
				m.Transition(StateStarting)
			}
		}(i)
	}

	wg.Wait()

	// Should be in starting state
	require.Equal(t, StateStarting, m.Current())
}

// TestMachineStartedAtPreservation validates StartedAt preservation
func TestMachineStartedAtPreservation(t *testing.T) {
	m := NewMachine(zap.NewNop())

	// Start the agent
	m.Transition(StateStarting)
	m.Transition(StateRunning)

	firstStart := m.StartedAt()
	require.False(t, firstStart.IsZero())

	// Do various state changes that don't reach stopped
	m.Transition(StateStopping)
	m.Transition(StateRunning)

	// StartedAt should remain the same
	secondStart := m.StartedAt()
	require.Equal(t, firstStart, secondStart)
}

// TestMachineAllStatesTransitionsBackToStopped validates all paths lead to stopped
func TestMachineAllStatesTransitionsBackToStopped(t *testing.T) {
	states := []State{StateRunning, StateStopping, StateFailed}

	for _, initialState := range states {
		t.Run(initialState.String()+"_back_to_stopped", func(t *testing.T) {
			m := NewMachine(zap.NewNop())
			m.current = initialState

			// Find path back to stopped
			switch initialState {
			case StateRunning:
				m.Transition(StateStopping)
				m.Transition(StateStopped)
			case StateStopping:
				m.Transition(StateStopped)
			case StateFailed:
				m.Transition(StateStopped)
			}

			require.Equal(t, StateStopped, m.Current())
		})
	}
}

// TestStateTransitionOrdering validates state must follow proper sequence
func TestStateTransitionOrdering(t *testing.T) {
	m := NewMachine(zap.NewNop())

	// Try to skip from stopped to running (should fail)
	m.Transition(StateRunning)
	require.Equal(t, StateStopped, m.Current())

	// Proper sequence
	m.Transition(StateStarting)
	require.Equal(t, StateStarting, m.Current())
	m.Transition(StateRunning)
	require.Equal(t, StateRunning, m.Current())

	// Can't go back to starting from running
	m.Transition(StateStarting)
	require.Equal(t, StateRunning, m.Current())
}

// TestMachineStoppedToStartingTwice validates stopped to starting can be called multiple times
func TestMachineStoppedToStartingTwice(t *testing.T) {
	m := NewMachine(zap.NewNop())

	// First transition
	m.Transition(StateStarting)
	require.Equal(t, StateStarting, m.Current())

	// Second transition to same state (idempotent)
	m.Transition(StateStarting)
	require.Equal(t, StateStarting, m.Current())
}

// TestMachineRunningToStoppingAndBack validates running/stopping cycle
func TestMachineRunningToStoppingAndBack(t *testing.T) {
	m := NewMachine(zap.NewNop())

	// Stopped -> Starting -> Running
	m.Transition(StateStarting)
	m.Transition(StateRunning)

	// Running -> Stopping -> Stopped
	m.Transition(StateStopping)
	m.Transition(StateStopped)

	// Stopped -> Starting -> Running again
	m.Transition(StateStarting)
	m.Transition(StateRunning)

	require.Equal(t, StateRunning, m.Current())
}

// TestMachineUnknownStateCannotTransition validates unknown state cannot transition
func TestMachineUnknownStateCannotTransition(t *testing.T) {
	m := NewMachine(zap.NewNop())
	m.current = StateUnknown

	// Try to transition from unknown (should fail)
	m.Transition(StateStarting)
	require.Equal(t, StateUnknown, m.Current())
}
