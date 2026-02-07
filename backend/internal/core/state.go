package core

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// State represents an agent state
type State int

const (
	StateUnknown State = iota
	StateStopped
	StateStarting
	StateRunning
	StateStopping
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Machine manages agent state transitions
type Machine struct {
	current   State
	startedAt time.Time
	mu        sync.RWMutex
	logger    *zap.Logger
}

// NewMachine creates a new state machine
func NewMachine(logger *zap.Logger) *Machine {
	return &Machine{
		current: StateStopped,
		logger:  logger.With(zap.String("component", "state_machine")),
	}
}

// Current returns the current state
func (m *Machine) Current() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// StartedAt returns when the agent was started
func (m *Machine) StartedAt() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.startedAt
}

// Transition transitions to a new state
func (m *Machine) Transition(newState State) {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldState := m.current
	if oldState == newState {
		return
	}

	// Validate transition
	if !m.isValidTransition(oldState, newState) {
		m.logger.Warn("invalid state transition",
			zap.String("from", oldState.String()),
			zap.String("to", newState.String()))
		return
	}

	m.current = newState
	if newState == StateRunning && m.startedAt.IsZero() {
		m.startedAt = time.Now()
	}

	m.logger.Info("state transition",
		zap.String("from", oldState.String()),
		zap.String("to", newState.String()))
}

// isValidTransition checks if a state transition is valid
func (m *Machine) isValidTransition(from, to State) bool {
	// Valid transitions:
	// Stopped -> Starting
	// Starting -> Running, Failed
	// Running -> Stopping, Failed
	// Stopping -> Stopped
	// Failed -> Stopped, Starting

	switch from {
	case StateStopped:
		return to == StateStarting
	case StateStarting:
		return to == StateRunning || to == StateFailed
	case StateRunning:
		return to == StateStopping || to == StateFailed
	case StateStopping:
		return to == StateStopped
	case StateFailed:
		return to == StateStopped || to == StateStarting
	default:
		return false
	}
}
