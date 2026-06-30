package agent

import "strings"

const (
	WorkflowRuntimeModeLegacyDeterministic = "legacy_deterministic"
	WorkflowRuntimeModeHybridAdaptive      = "hybrid_adaptive"
	WorkflowRuntimeModeFullAdaptive        = "full_adaptive"
)

func canonicalWorkflowRuntimeMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", WorkflowRuntimeModeDeterministic, WorkflowRuntimeModeLegacyDeterministic:
		return WorkflowRuntimeModeLegacyDeterministic
	case WorkflowRuntimeModeHybrid, WorkflowRuntimeModeHybridAdaptive:
		return WorkflowRuntimeModeHybridAdaptive
	case WorkflowRuntimeModeAdaptive, WorkflowRuntimeModeFullAdaptive:
		return WorkflowRuntimeModeFullAdaptive
	default:
		return WorkflowRuntimeModeLegacyDeterministic
	}
}

func runtimeModeEnablesAdaptiveLoop(value string) bool {
	switch canonicalWorkflowRuntimeMode(value) {
	case WorkflowRuntimeModeHybridAdaptive, WorkflowRuntimeModeFullAdaptive:
		return true
	default:
		return false
	}
}

func runtimeModeEnablesAutonomousToolSelection(cfg WorkflowConfig) bool {
	if cfg.AutonomousToolSelectionEnabled {
		return true
	}
	return runtimeModeEnablesAdaptiveLoop(cfg.RuntimeMode)
}

func runtimeModeEnablesPlannerCritic(cfg WorkflowConfig) bool {
	if cfg.PlannerCriticEnabled {
		return true
	}
	return runtimeModeEnablesAdaptiveLoop(cfg.RuntimeMode)
}

func runtimeModeEnablesExperienceMemory(cfg WorkflowConfig) bool {
	if cfg.ToolExperienceMemoryEnabled {
		return true
	}
	return runtimeModeEnablesAdaptiveLoop(cfg.RuntimeMode)
}

func runtimeModeEnablesAdaptiveRuntime(cfg WorkflowConfig) bool {
	if cfg.AdaptiveRuntimeEnabled {
		return true
	}
	return runtimeModeEnablesAdaptiveLoop(cfg.RuntimeMode)
}

func runtimeModePrefersCheapFirst(cfg WorkflowConfig) bool {
	if cfg.CheapFirstSelectionEnabled {
		return true
	}
	return runtimeModeEnablesAdaptiveLoop(cfg.RuntimeMode)
}
