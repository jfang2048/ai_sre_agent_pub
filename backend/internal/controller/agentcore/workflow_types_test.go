package agent

import (
	"testing"
	"time"
)

func TestNormalizeWorkflowConfigAppliesDefaultsAndBounds(t *testing.T) {
	def := DefaultWorkflowConfig()
	cfg := WorkflowConfig{
		AdvancedReasoningAmbiguityThreshold: 1.5,
		RefineConfidenceThreshold:           -0.1,
		HighRiskScoreThreshold:              2,
		MediumRiskThreshold:                 0.9,
		WorkflowDataPath:                    "  /tmp/workflow-data  ",
		ArtifactMetadataPath:                def.ArtifactMetadataPath,
		ArtifactPayloadRootPath:             def.ArtifactPayloadRootPath,
		AgentMessageDir:                     def.AgentMessageDir,
		WorkflowStoreBackend:                "postgresql",
		ArtifactMetadataBackend:             "workflow",
		ArtifactPayloadBackend:              "localfs",
		ValidationDegradedFallback:          "invalid",
		BehaviorMemory:                      BehavioralMemoryConfig{},
	}

	got := normalizeWorkflowConfig(cfg)

	if got.DefaultWindow != def.DefaultWindow {
		t.Fatalf("DefaultWindow = %v, want %v", got.DefaultWindow, def.DefaultWindow)
	}
	if got.AdvancedReasoningAmbiguityThreshold != def.AdvancedReasoningAmbiguityThreshold {
		t.Fatalf("AdvancedReasoningAmbiguityThreshold = %v, want %v", got.AdvancedReasoningAmbiguityThreshold, def.AdvancedReasoningAmbiguityThreshold)
	}
	if got.RefineConfidenceThreshold != def.RefineConfidenceThreshold {
		t.Fatalf("RefineConfidenceThreshold = %v, want %v", got.RefineConfidenceThreshold, def.RefineConfidenceThreshold)
	}
	if got.HighRiskScoreThreshold != def.HighRiskScoreThreshold {
		t.Fatalf("HighRiskScoreThreshold = %v, want %v", got.HighRiskScoreThreshold, def.HighRiskScoreThreshold)
	}
	if got.MediumRiskThreshold != got.HighRiskScoreThreshold*0.75 {
		t.Fatalf("MediumRiskThreshold = %v, want %v", got.MediumRiskThreshold, got.HighRiskScoreThreshold*0.75)
	}
	if got.WorkflowStoreBackend != "postgres" {
		t.Fatalf("WorkflowStoreBackend = %q, want postgres", got.WorkflowStoreBackend)
	}
	if got.ArtifactMetadataBackend != "postgres" {
		t.Fatalf("ArtifactMetadataBackend = %q, want postgres", got.ArtifactMetadataBackend)
	}
	if got.ArtifactPayloadBackend != "filesystem" {
		t.Fatalf("ArtifactPayloadBackend = %q, want filesystem", got.ArtifactPayloadBackend)
	}
	if got.ArtifactMetadataPath != "/tmp/workflow-data/artifacts.db" {
		t.Fatalf("ArtifactMetadataPath = %q, want /tmp/workflow-data/artifacts.db", got.ArtifactMetadataPath)
	}
	if got.ArtifactPayloadRootPath != "/tmp/workflow-data" {
		t.Fatalf("ArtifactPayloadRootPath = %q, want /tmp/workflow-data", got.ArtifactPayloadRootPath)
	}
	if got.AgentMessageDir != "/tmp/workflow-data/messages" {
		t.Fatalf("AgentMessageDir = %q, want /tmp/workflow-data/messages", got.AgentMessageDir)
	}
	if got.ValidationDegradedFallback != def.ValidationDegradedFallback {
		t.Fatalf("ValidationDegradedFallback = %q, want %q", got.ValidationDegradedFallback, def.ValidationDegradedFallback)
	}
	if got.BehaviorMemory != def.BehaviorMemory {
		t.Fatalf("BehaviorMemory = %#v, want %#v", got.BehaviorMemory, def.BehaviorMemory)
	}
}

func TestNormalizeWorkflowConfigKeepsExplicitBehaviorMemorySettings(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.BehaviorMemory = BehavioralMemoryConfig{
		Enabled:            false,
		LongWindow:         72 * time.Hour,
		MinSamples:         6,
		MinRecurringBursts: 3,
		CacheEntries:       4,
		CacheTTL:           30 * time.Minute,
	}

	got := normalizeWorkflowConfig(cfg)

	if got.BehaviorMemory != cfg.BehaviorMemory {
		t.Fatalf("BehaviorMemory = %#v, want %#v", got.BehaviorMemory, cfg.BehaviorMemory)
	}
}

func TestNormalizeWorkflowConfigSupportsCanonicalAdaptiveRuntimeModes(t *testing.T) {
	cfg := WorkflowConfig{
		RuntimeMode:                   "full_adaptive",
		AdaptiveMaxNoProgressRounds:   5,
		AdaptiveMaxPlateauRounds:      4,
		MaxNoProgressRounds:           6,
		MaxUncertaintyPlateauRounds:   3,
		AdaptiveParallelReadOnlyLimit: 4,
	}

	got := normalizeWorkflowConfig(cfg)
	if got.RuntimeMode != WorkflowRuntimeModeFullAdaptive {
		t.Fatalf("RuntimeMode = %q, want %q", got.RuntimeMode, WorkflowRuntimeModeFullAdaptive)
	}
	if !got.AdaptiveRuntimeEnabled {
		t.Fatalf("AdaptiveRuntimeEnabled = false, want true")
	}
	if !got.AutonomousToolSelectionEnabled {
		t.Fatalf("AutonomousToolSelectionEnabled = false, want true")
	}
	if !got.PlannerCriticEnabled {
		t.Fatalf("PlannerCriticEnabled = false, want true")
	}
	if got.MaxNoProgressRounds != 6 {
		t.Fatalf("MaxNoProgressRounds = %d, want 6", got.MaxNoProgressRounds)
	}
	if got.MaxUncertaintyPlateauRounds != 3 {
		t.Fatalf("MaxUncertaintyPlateauRounds = %d, want 3", got.MaxUncertaintyPlateauRounds)
	}
	if got.AdaptiveParallelReadOnlyLimit != 4 {
		t.Fatalf("AdaptiveParallelReadOnlyLimit = %d, want 4", got.AdaptiveParallelReadOnlyLimit)
	}
}
