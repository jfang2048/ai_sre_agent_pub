package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	artifactstore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/artifacts"
	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestWorkflowEngineReadArtifactUsesSharedMetadataReference(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.WorkflowDataPath = t.TempDir()
	cfg.WorkflowStorePath = filepath.Join(cfg.WorkflowDataPath, "workflow_runs.db")

	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	require.NotNil(t, engine.artifactManager)

	record, err := engine.artifactManager.Write(context.Background(), artifactstore.WriteRequest{
		ArtifactID:    "evidence-run-read",
		ArtifactType:  artifactstore.ArtifactTypeEvidencePackage,
		OwnerType:     artifactstore.OwnerTypeWorkflowRun,
		OwnerID:       "run-read",
		RunID:         "run-read",
		ContentType:   "application/json",
		FileExtension: ".json",
		Payload:       []byte(`{"run_id":"run-read","mode":"metadata_lookup"}`),
	})
	require.NoError(t, err)

	ref := durableArtifactRefFromRecord(record)
	ref.Path = ""
	ref.LocalCachePath = ""

	raw, err := engine.ReadArtifact(context.Background(), &ref)
	require.NoError(t, err)
	require.JSONEq(t, `{"run_id":"run-read","mode":"metadata_lookup"}`, string(raw))
}

func TestNormalizeWorkflowConfigDerivesArtifactPathsFromWorkflowDataPath(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.WorkflowDataPath = t.TempDir()
	cfg = normalizeWorkflowConfig(cfg)

	require.Equal(t, filepath.Join(cfg.WorkflowDataPath, "artifacts.db"), cfg.ArtifactMetadataPath)
	require.Equal(t, cfg.WorkflowDataPath, cfg.ArtifactPayloadRootPath)
	require.Equal(t, filepath.Join(cfg.WorkflowDataPath, "messages"), cfg.AgentMessageDir)
}

func TestNormalizeWorkflowConfigKeepsS3PayloadConfig(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.WorkflowDataPath = t.TempDir()
	cfg.ArtifactPayloadBackend = "s3"
	cfg.ArtifactPayloadS3Bucket = "artifacts-prod"
	cfg.ArtifactPayloadS3Prefix = "controller-a"
	cfg = normalizeWorkflowConfig(cfg)

	require.Equal(t, "s3", cfg.ArtifactPayloadBackend)
	require.Equal(t, "artifacts-prod", cfg.ArtifactPayloadS3Bucket)
	require.Equal(t, "controller-a", cfg.ArtifactPayloadS3Prefix)
}

func TestWorkflowArtifactBaseChainKindOrderIsStable(t *testing.T) {
	chain := buildWorkflowArtifactChain(nil, RCAWorkflowReport{
		WorkflowID:  "run-order",
		IncidentID:  "inc-order",
		TraceID:     "trace-order",
		GeneratedAt: time.Unix(1700000000, 0).UTC(),
		Status:      "completed",
	}, "completed")
	manifest := buildWorkflowArtifactManifest(chain)

	kinds := make([]WorkflowArtifactKind, 0, len(manifest.Artifacts))
	for _, ref := range manifest.Artifacts {
		kinds = append(kinds, ref.Kind)
	}

	require.Equal(t, []WorkflowArtifactKind{
		WorkflowArtifactObservationSummary,
		WorkflowArtifactAnomalyFinding,
		WorkflowArtifactRootCauseHypothesis,
		WorkflowArtifactRemediationProposal,
		WorkflowArtifactExecutionPlan,
		WorkflowArtifactExecutionResult,
		WorkflowArtifactVerificationResult,
		WorkflowArtifactIncidentReport,
	}, kinds)
	require.True(t, chain.ExecutionPlan.Meta.Replayable)
	require.True(t, chain.ExecutionPlan.Meta.Retryable)
	require.False(t, chain.ExecutionResult.Meta.Replayable)
	require.False(t, chain.ExecutionResult.Meta.Retryable)
}

func TestBuildWorkflowArtifactChainKeepsTheHandoffCompactAndVersioned(t *testing.T) {
	report := RCAWorkflowReport{
		WorkflowID:  "run-42",
		IncidentID:  "inc-42",
		TraceID:     "trace-42",
		CollectorID: "collector-a",
		Trigger:     "disk_latency",
		GeneratedAt: time.Unix(1700000000, 0).UTC(),
		Status:      "investigating",
		SynthesizedIncident: IncidentSynthesis{
			Summary: "Disk latency climbed after a config push",
		},
		Context:   RCAContext{Window: "45m"},
		Anomalies: []string{"disk wait rose", "queue depth tracked the spike"},
		Hypotheses: []RCAHypothesis{
			{
				ID:                 "hyp-1",
				Rank:               1,
				Title:              "recent config change",
				Confidence:         0.84,
				Description:        "A rollout likely changed queue behavior",
				EvidenceIDs:        []string{"e-1"},
				RecommendedActions: []string{"rollback the change"},
			},
		},
		Evidence: []RCAEvidence{{ID: "e-1", Summary: "disk wait"}},
		NormalizedEvidence: []evidencev1.Record{
			{
				SchemaVersion: evidencev1.SchemaVersionV1,
				ID:            "e-1",
				Kind:          "metric",
				Summary:       "disk wait rose",
				RawReferences: []evidencev1.RawReference{{Kind: "metric", ID: "node_disk_wait"}},
			},
		},
		Recommendations: []WorkflowRecommendation{
			{
				ID:               "rec-1",
				Priority:         "high",
				Summary:          "rollback the change",
				RiskLevel:        "medium",
				RequiresApproval: true,
				RollbackHint:     "restore the last known good config",
				EvidenceIDs:      []string{"e-1"},
			},
		},
		Validation: ValidationActionReport{
			Mode:                       "bounded_react",
			ReadOnlyOnly:               true,
			ValidatedRecommendationIDs: []string{"rec-1"},
			RejectedRecommendationIDs:  []string{"rec-9"},
			UnresolvedUncertainty:      []string{"need post-change logs"},
			Governance: &ValidationGovernanceTrace{
				ActionSummary:     "rollback plan only",
				PolicyStatus:      "proposal_only",
				StepStatus:        "planned",
				ActionCandidateID: "cand-1",
			},
			SelectedAction: &ValidationActionCandidate{
				ID:               "cand-1",
				RequiresApproval: true,
				RollbackHint:     "restore the last known good config",
			},
			SelectedActionContract: &ValidationActionContract{ID: "contract-1"},
			PostActionValidation: &PostActionValidationSummary{
				Verdict:                  ValidationVerdictConfirmed,
				Summary:                  "risk dropped after rollback",
				BeforeRisk:               0.9,
				AfterRisk:                0.3,
				SupportingEvidenceIDs:    []string{"e-1"},
				ContradictingEvidenceIDs: []string{"e-9"},
			},
		},
	}

	chain := buildWorkflowArtifactChain(nil, report, "investigating")
	require.Equal(t, workflowArtifactSchemaVersion, chain.SchemaVersion)
	require.Equal(t, "run-42", chain.RunID)
	require.Equal(t, "inc-42", chain.IncidentID)
	require.Equal(t, "trace-42", chain.CorrelationID)
	require.Equal(t, "ai_sre_agent/workflow_artifacts/v1", chain.Observation.Meta.SchemaVersion)
	require.Equal(t, WorkflowArtifactObservationSummary, chain.Observation.Meta.Kind)
	require.Equal(t, "analysis_agent", chain.Observation.Meta.Consumer)
	require.NotEmpty(t, chain.Observation.EvidenceIDs)
	require.Equal(t, []string{artifactIDForWorkflow(WorkflowArtifactObservationSummary, "run-42")}, chain.Anomaly.Meta.InputArtifacts)
	require.True(t, chain.Hypothesis.Meta.Replayable)
	require.False(t, chain.ExecutionResult.Meta.Replayable)
	require.LessOrEqual(t, len(chain.Observation.RawEvidenceRefs), 8)
	require.Equal(t, "recent config change", chain.Hypothesis.Title)
	require.Equal(t, "rollback the change", chain.Proposal.Summary)
	require.Equal(t, "contract-1", chain.ExecutionPlan.ActionContractID)
	require.Equal(t, "proposal_only", chain.ExecutionPlan.PolicyStatus)
	require.Equal(t, "confirmed", chain.Verification.Verdict)
	require.Len(t, chain.Incident.MessageIDs, 0)
}

func TestBuildWorkflowArtifactChainIncludesSceneFields(t *testing.T) {
	report := RCAWorkflowReport{
		WorkflowID:  "run-scene",
		IncidentID:  "inc-scene",
		TraceID:     "trace-scene",
		CollectorID: "node-a",
		GeneratedAt: time.Unix(1700001000, 0).UTC(),
		Status:      "escalated",
		Trigger:     "latency",
		SceneClassification: SceneClassification{
			SceneFamily:     SceneFamilyDatabaseLikeLatencyPath,
			Confidence:      0.74,
			MissingEvidence: []string{"latency_path_metrics"},
		},
		CollectionPlan: CollectionPlan{
			PlanID:                    "plan-scene",
			SceneFamily:               SceneFamilyDatabaseLikeLatencyPath,
			RoundIndex:                1,
			TargetCollectorsOrModules: []string{"metrics", "storage", "logs"},
			SamplingInterval:          2 * time.Second,
			CollectionWindow:          4 * time.Minute,
			TTL:                       6 * time.Minute,
		},
		RecollectionResults: []RecollectionResult{{
			PlanID:         "plan-scene",
			SceneFamily:    SceneFamilyDatabaseLikeLatencyPath,
			RoundIndex:     1,
			AppliedModules: []string{"metrics", "storage"},
			Converged:      false,
		}},
		EvidenceGapState: EvidenceGapState{
			SceneFamily:             SceneFamilyDatabaseLikeLatencyPath,
			MissingEvidence:         []string{"latency_path_metrics"},
			EvidenceGoalsStillUnmet: []string{"stable_ranked_hypothesis"},
		},
		EscalationDecision: EscalationDecision{
			Escalate: true,
			Reason:   "confidence remained low",
		},
	}

	chain := buildWorkflowArtifactChain(nil, report, "escalated")
	require.NotNil(t, chain.Scene.SceneClassification)
	require.Equal(t, SceneFamilyDatabaseLikeLatencyPath, chain.Scene.SceneClassification.SceneFamily)
	require.NotNil(t, chain.Scene.CollectionPlan)
	require.Equal(t, "plan-scene", chain.Scene.CollectionPlan.PlanID)
	require.NotNil(t, chain.Scene.EvidenceGapState)
	require.NotNil(t, chain.Scene.EscalationDecision)
	require.True(t, chain.Scene.EscalationDecision.Escalate)
}

func TestWorkflowEvidenceBuilderIncludesArtifactChain(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.WorkflowDataPath = t.TempDir()
	cfg.WorkflowStorePath = filepath.Join(cfg.WorkflowDataPath, "workflow_runs.db")
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	run := &DurableRun{RunID: "run-chain", WorkflowType: "rca", CollectorID: "node-a", Status: RunStatusCompleted}
	report := RCAWorkflowReport{
		WorkflowID:  "run-chain",
		IncidentID:  "inc-chain",
		TraceID:     "trace-chain",
		CollectorID: "node-a",
		GeneratedAt: time.Unix(1700000000, 0).UTC(),
		Status:      "completed",
		Trigger:     "latency",
		SynthesizedIncident: IncidentSynthesis{
			Summary:    "latency spiked after rollout",
			TimeWindow: TimeWindow{Start: time.Unix(1700000000, 0).UTC(), End: time.Unix(1700000300, 0).UTC()},
		},
		Context:            RCAContext{Window: "5m"},
		Hypotheses:         []RCAHypothesis{{ID: "hyp-1", Rank: 1, Title: "bad rollout", Confidence: 0.9, Description: "config rollout changed queue depth", EvidenceIDs: []string{"ev-1"}}},
		Recommendations:    []WorkflowRecommendation{{ID: "rec-1", Priority: "high", Summary: "rollback the rollout", EvidenceIDs: []string{"ev-1"}}},
		Validation:         ValidationActionReport{Mode: "bounded_react", Governance: &ValidationGovernanceTrace{PolicyStatus: "proposal_only"}},
		NormalizedEvidence: []evidencev1.Record{{SchemaVersion: evidencev1.SchemaVersionV1, ID: "ev-1", Kind: "metric", Summary: "queue depth rose"}},
	}

	writeResult, err := engine.evidenceBuilder.Write(run, nil, nil, report)
	require.NoError(t, err)
	raw, err := engine.ReadArtifact(context.Background(), (*DurableArtifactRef)(&writeResult.PackageRef))
	require.NoError(t, err)
	require.Contains(t, string(raw), "artifact_chain")
	require.Contains(t, string(raw), "execution_plan")
	require.NotNil(t, writeResult.ArtifactManifest)
}

func TestWorkflowEngineArtifactChainReadsFromEvidencePackage(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.WorkflowDataPath = t.TempDir()
	cfg.WorkflowStorePath = filepath.Join(cfg.WorkflowDataPath, "workflow_runs.db")
	engine := NewWorkflowEngine(cfg, ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	run, err := engine.orchestrator.StartRun(context.Background(), "run-chain-read", "rca", "node-a")
	require.NoError(t, err)

	report := RCAWorkflowReport{
		WorkflowID:  "run-chain-read",
		IncidentID:  "inc-chain-read",
		TraceID:     "trace-chain-read",
		CollectorID: "node-a",
		GeneratedAt: time.Unix(1700000000, 0).UTC(),
		Status:      "completed",
		Trigger:     "latency",
		SynthesizedIncident: IncidentSynthesis{
			Summary:    "latency spiked after rollout",
			TimeWindow: TimeWindow{Start: time.Unix(1700000000, 0).UTC(), End: time.Unix(1700000300, 0).UTC()},
		},
		Context:            RCAContext{Window: "5m"},
		Hypotheses:         []RCAHypothesis{{ID: "hyp-1", Rank: 1, Title: "bad rollout", Confidence: 0.9, Description: "config rollout changed queue depth", EvidenceIDs: []string{"ev-1"}}},
		Recommendations:    []WorkflowRecommendation{{ID: "rec-1", Priority: "high", Summary: "rollback the rollout", EvidenceIDs: []string{"ev-1"}}},
		Validation:         ValidationActionReport{Mode: "bounded_react", Governance: &ValidationGovernanceTrace{PolicyStatus: "proposal_only"}},
		NormalizedEvidence: []evidencev1.Record{{SchemaVersion: evidencev1.SchemaVersionV1, ID: "ev-1", Kind: "metric", Summary: "queue depth rose"}},
	}

	writeResult, err := engine.evidenceBuilder.Write(run, nil, nil, report)
	require.NoError(t, err)
	require.NoError(t, engine.orchestrator.AttachEvidencePackage(context.Background(), run.RunID, writeResult.PackageRef))
	require.NotNil(t, writeResult.ArtifactManifest)
	require.NoError(t, engine.orchestrator.AttachArtifactManifest(context.Background(), run.RunID, *writeResult.ArtifactManifest))

	chain, evidenceRef, err := engine.ArtifactChain(context.Background(), run.RunID)
	require.NoError(t, err)
	require.NotNil(t, evidenceRef)
	require.Equal(t, run.RunID, chain.RunID)
	require.Equal(t, WorkflowArtifactExecutionPlan, chain.ExecutionPlan.Meta.Kind)

	manifest, manifestRef, err := engine.ArtifactManifest(context.Background(), run.RunID)
	require.NoError(t, err)
	require.NotNil(t, manifestRef)
	require.Equal(t, run.RunID, manifest.RunID)
	require.Len(t, manifest.Artifacts, 8)
}
