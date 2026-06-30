package agent

import (
	"context"
	"testing"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestClassifyScenePrefersDeploymentRollout(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "node-a"})
	state.changeLinks = []RCAChangeLink{{
		ChangeID:         "chg-1",
		Category:         "deployment",
		Summary:          "checkout rollout",
		CorrelationScore: 0.86,
	}}
	state.logsData.RecentDeploys = []string{"deployment/checkout rollout completed"}
	state.incident.Summary = "latency spiked after rollout"

	scene := classifyScene(state)
	require.Equal(t, SceneFamilyDeploymentRollout, scene.SceneFamily)
	require.Greater(t, scene.Confidence, 0.5)
	require.Contains(t, scene.MissingEvidence, "metric_baseline")
}

func TestCompileCollectionPlanCarriesBudgetsAndTTL(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	t.Cleanup(func() { require.NoError(t, engine.Close()) })

	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "node-a"})
	state.sceneClassification = SceneClassification{
		SceneFamily:     SceneFamilyNetworkConnectivity,
		Confidence:      0.68,
		MissingEvidence: []string{"runtime_network_events"},
		CollectionHints: []string{"shorter interval"},
	}
	state.incident.ImpactedScope = []string{"service/checkout", "node/node-a"}

	plan := compileCollectionPlan(state, 1)
	require.NotEmpty(t, plan.PlanID)
	require.Equal(t, SceneFamilyNetworkConnectivity, plan.SceneFamily)
	require.NotEmpty(t, plan.TargetCollectorsOrModules)
	require.Greater(t, plan.MaxBytes, int64(0))
	require.Greater(t, plan.MaxEvents, 0)
	require.Greater(t, plan.MaxOverheadPercent, 0.0)
	require.NotZero(t, plan.TTL)
	require.True(t, plan.Replayable)
}

type fakeCollectorProfileApplier struct {
	lastCollector string
	lastProfile   CollectorProfileRequest
}

func (f *fakeCollectorProfileApplier) ApplyRuntimeProfile(_ context.Context, collectorID string, profile CollectorProfileRequest) (CollectorProfileStatus, error) {
	f.lastCollector = collectorID
	f.lastProfile = profile
	return CollectorProfileStatus{
		ProfileID:          profile.ProfileID,
		SceneFamily:        profile.SceneFamily,
		State:              "active",
		AllowedModules:     append([]string(nil), profile.AllowedModules...),
		SamplingInterval:   profile.SamplingInterval,
		ProcessTopK:        profile.ProcessTopK,
		LogBudget:          profile.LogBudget,
		MaxOverheadPercent: profile.MaxOverheadPercent,
	}, nil
}

func TestStepTargetedRecollectionAppliesProfileAndRecordsResult(t *testing.T) {
	engine := NewWorkflowEngine(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	applier := &fakeCollectorProfileApplier{}
	engine.SetCollectorProfileApplier(applier)

	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "node-a"})
	state.sceneClassification = SceneClassification{
		SceneFamily:     SceneFamilyResourceContention,
		Confidence:      0.62,
		MissingEvidence: []string{"metric_baseline"},
	}
	state.collectionPlan = compileCollectionPlan(state, 1)

	require.NoError(t, engine.stepTargetedRecollection(context.Background(), state))
	require.NotEmpty(t, state.recollectionResults)
	require.Equal(t, "node-a", applier.lastCollector)
	require.Equal(t, state.collectionPlan.PlanID+"-profile", applier.lastProfile.ProfileID)
	require.NotNil(t, state.sceneProfileStatus)
}

func TestBuildEscalationDecisionTriggersOnLowConfidence(t *testing.T) {
	state := &workflowState{
		sceneClassification: SceneClassification{SceneFamily: SceneFamilyStorageIO, Confidence: 0.32},
		evidenceGapState: EvidenceGapState{
			MissingEvidence:         []string{"io_pressure"},
			EvidenceGoalsStillUnmet: []string{"stable_ranked_hypothesis"},
		},
		hypotheses: []RCAHypothesis{{
			ID:         "h-1",
			Title:      "storage latency",
			Confidence: 0.41,
		}},
	}

	decision := buildEscalationDecision(state)
	require.True(t, decision.Escalate)
	require.Contains(t, decision.Reason, "scene confidence")
	require.Contains(t, decision.Reason, "evidence goals")
}
