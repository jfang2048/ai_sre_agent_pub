package agent

import (
	"context"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type preflightTestTopology struct{}

func (preflightTestTopology) Snapshot(context.Context) TopologySnapshot {
	return TopologySnapshot{Edges: []TopologyEdge{{Source: "checkout", Target: "payments", Kind: "calls"}}}
}

func TestRemediationValidationPlansReadOnlyPreflight(t *testing.T) {
	tools := suggestedToolsForTargetFamilies(ValidationTargetRemediation, []string{"gpu", "kubernetes", "change"})
	require.GreaterOrEqual(t, len(tools), 6)
	require.Equal(t, ToolOperatorPreflight, tools[0])
	require.Contains(t, tools, ToolActionOutcome)
	require.Contains(t, tools, ToolNetworkBlastRadius)
	require.Contains(t, tools, ToolGPU)
	require.Contains(t, tools, ToolKubernetesResource)
	require.Contains(t, tools, ToolContainerRevision)
	require.Contains(t, tools, ToolDeploymentHistory)
	for _, tool := range tools {
		require.NotEqual(t, ToolRemediation, tool)
	}
}

func TestOperatorPreflightDistinguishesFreshEvidenceFromMissingChecks(t *testing.T) {
	now := time.Now().UTC()
	store := ingest.NewMemoryStore()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Hostname: "host-a", Labels: []*telemetryv1.Label{
		{Key: "service", Value: "checkout"}, {Key: "namespace", Value: "shop"},
		{Key: "workload", Value: "checkout"}, {Key: "revision", Value: "42"},
	}}, now)
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{{Name: "service_error_rate", Value: 0.04, TimestampUnixNano: now.UnixNano()}}, now)
	tool := &operatorPreflightTool{store: store, topology: preflightTestTopology{}, maxAge: 2 * time.Minute}
	result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
	require.NoError(t, err)
	data, ok := result.Data.(operatorPreflightData)
	require.True(t, ok)
	require.True(t, data.Fresh)
	require.True(t, data.HealthAvailable)
	require.Equal(t, "checkout", data.Identity.Service)
	require.Equal(t, "42", data.Revision.Revision)
	require.Contains(t, data.Impact.Downstream, "payments")
	require.Contains(t, data.EvidenceGaps, "live replicas and disruption budget require separate operator verification")
	require.NotEmpty(t, data.OperatorChecks)
	require.True(t, describeWorkflowTool(tool).ReadOnly)
	replayed, ok := decodeWorkflowToolPayload(ToolOperatorPreflight, marshalToolResultPayload(data))
	require.True(t, ok)
	require.Equal(t, data.Identity.Service, replayed.(operatorPreflightData).Identity.Service)
}

func TestOperatorPreflightDoesNotCallMissingTelemetryHealthy(t *testing.T) {
	store := ingest.NewMemoryStore()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-stale"}, time.Now().Add(-10*time.Minute))
	tool := &operatorPreflightTool{store: store, maxAge: time.Minute}
	result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: "collector-stale"})
	require.NoError(t, err)
	data := result.Data.(operatorPreflightData)
	require.False(t, data.Fresh)
	require.False(t, data.HealthAvailable)
	require.False(t, data.Health.Healthy)
	require.Contains(t, data.EvidenceGaps, "service metric snapshot is missing a timestamp or is stale")
	require.Contains(t, data.EvidenceGaps, "service health metrics unavailable or incomplete")
}

func TestOperatorPreflightCandidateOnlyForRemediation(t *testing.T) {
	engine := NewWorkflowEngineFromConfig(DefaultWorkflowConfig(), ingest.NewMemoryStore(), logindex.NewIndex(logindex.DefaultConfig()), nil, zap.NewNop())
	state := engine.newWorkflowState("rca", WorkflowRequest{CollectorID: "collector-a", Window: 5 * time.Minute})
	base := ToolSelectionContext{Stage: "validation_action_react_loop", ReadOnlyOnly: true}
	for _, candidate := range generateToolCandidates(state, base) {
		require.NotEqual(t, ToolOperatorPreflight, candidate.Tool)
	}
	base.Target = &ValidationTarget{Type: ValidationTargetRemediation, SuggestedTools: []ToolName{ToolOperatorPreflight}}
	base.PreferredTools = []ToolName{ToolOperatorPreflight}
	found := false
	for _, candidate := range generateToolCandidates(state, base) {
		found = found || candidate.Tool == ToolOperatorPreflight
	}
	require.True(t, found)
}

func TestServiceHealthDistinguishesMissingFromObservedHealth(t *testing.T) {
	now := time.Now().UTC()
	store := ingest.NewMemoryStore()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-health"}, now)
	tool := &serviceHealthTool{store: store}
	read := func() serviceHealthToolData {
		result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: "collector-health"})
		require.NoError(t, err)
		return result.Data.(serviceHealthToolData)
	}
	require.False(t, read().Available)
	require.False(t, read().Healthy)

	store.StoreMetrics("collector-health", []*telemetryv1.Metric{{Name: "service_error_rate", Value: 0.10, TimestampUnixNano: now.UnixNano()}}, now)
	bad := read()
	require.True(t, bad.Available)
	require.False(t, bad.Healthy)
	require.False(t, bad.LatencyAvailable)
	require.True(t, bad.ErrorRateAvailable)

	store.StoreMetrics("collector-health", []*telemetryv1.Metric{
		{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano()},
		{Name: "service_latency_p95_ms", Value: 200, TimestampUnixNano: now.UnixNano()},
	}, now)
	good := read()
	require.True(t, good.Available)
	require.True(t, good.Healthy)
	require.NotEmpty(t, good.ScopeKey, "unlabeled telemetry remains scoped to its collector")
	require.True(t, good.LatencyAvailable)
	require.True(t, good.ErrorRateAvailable)
}

func TestServiceHealthRejectsMixedServiceAndWorkloadScopes(t *testing.T) {
	now := time.Now().UTC()
	labels := func(service, workload string) []*telemetryv1.Label {
		return []*telemetryv1.Label{{Key: "service", Value: service}, {Key: "workload", Value: workload}}
	}
	for _, tc := range []struct {
		name            string
		collectorLabels []*telemetryv1.Label
		latencyLabels   []*telemetryv1.Label
		errorLabels     []*telemetryv1.Label
		wantAmbiguous   bool
	}{
		{name: "different services", latencyLabels: labels("checkout", "api"), errorLabels: labels("payments", "api"), wantAmbiguous: true},
		{name: "different workloads", latencyLabels: labels("checkout", "api"), errorLabels: labels("checkout", "worker"), wantAmbiguous: true},
		{name: "labeled and unlabeled with no collector identity", latencyLabels: labels("checkout", "api"), wantAmbiguous: true},
		{name: "conflicts with collector identity", collectorLabels: labels("checkout", "api"), latencyLabels: labels("payments", "api"), errorLabels: labels("payments", "api"), wantAmbiguous: true},
		{name: "conflicting repeated labels", latencyLabels: []*telemetryv1.Label{{Key: "service", Value: "checkout"}, {Key: "service", Value: "payments"}}, errorLabels: labels("checkout", "api"), wantAmbiguous: true},
		{name: "same scope with reversed label order", latencyLabels: []*telemetryv1.Label{{Key: "service", Value: "checkout"}, {Key: "workload", Value: "api"}}, errorLabels: []*telemetryv1.Label{{Key: "workload", Value: "api"}, {Key: "service", Value: "checkout"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := ingest.NewMemoryStore()
			store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Labels: tc.collectorLabels}, now)
			store.StoreMetrics("collector-a", []*telemetryv1.Metric{
				{Name: "service_latency_p95_ms", Value: 200, TimestampUnixNano: now.UnixNano(), Labels: tc.latencyLabels},
				{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano(), Labels: tc.errorLabels},
			}, now)
			result, err := (&serviceHealthTool{store: store, maxAge: time.Minute}).Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
			require.NoError(t, err)
			health := result.Data.(serviceHealthToolData)
			require.Equal(t, tc.wantAmbiguous, health.ScopeAmbiguous)
			require.Equal(t, !tc.wantAmbiguous, health.Available)
			require.Equal(t, !tc.wantAmbiguous, health.Fresh)
			require.Equal(t, !tc.wantAmbiguous, health.Healthy)
			if !tc.wantAmbiguous {
				require.NotEmpty(t, health.ScopeKey)
			}
		})
	}
}

func TestServiceHealthRejectsTwoPodSeriesEvenUnderSameWorkload(t *testing.T) {
	now := time.Now().UTC()
	labels := func(pod string) []*telemetryv1.Label {
		return []*telemetryv1.Label{
			{Key: "service", Value: "checkout"}, {Key: "workload", Value: "api"}, {Key: "pod_uid", Value: pod},
		}
	}
	for _, tc := range []struct {
		name             string
		metrics          []*telemetryv1.Metric
		wantNameRepeated bool
		wantAvailable    bool
	}{
		{name: "sick pod then healthy pod, same metric name", metrics: []*telemetryv1.Metric{
			{Name: "service_error_rate", Value: 0.10, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-a")},
			{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-b")},
		}, wantNameRepeated: true},
		{name: "healthy pod then sick pod, same metric name", metrics: []*telemetryv1.Metric{
			{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-b")},
			{Name: "service_error_rate", Value: 0.10, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-a")},
		}, wantNameRepeated: true},
		{name: "different metric names from different pods", metrics: []*telemetryv1.Metric{
			{Name: "service_latency_p95_ms", Value: 200, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-a")},
			{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-b")},
		}},
		{name: "repeated name from same pod", metrics: []*telemetryv1.Metric{
			{Name: "service_error_rate", Value: 0.10, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-a")},
			{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-a")},
		}, wantNameRepeated: true},
		{name: "distinct metric names from one pod", metrics: []*telemetryv1.Metric{
			{Name: "service_latency_p95_ms", Value: 200, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-a")},
			{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano(), Labels: labels("pod-a")},
		}, wantAvailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := ingest.NewMemoryStore()
			store.StoreMetrics("collector-a", tc.metrics, now)
			result, err := (&serviceHealthTool{store: store, maxAge: time.Minute}).Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
			require.NoError(t, err)
			health := result.Data.(serviceHealthToolData)
			require.Equal(t, tc.wantNameRepeated, health.NameRepeated)
			require.Equal(t, tc.wantAvailable, health.Available)
			require.Equal(t, !tc.wantAvailable, health.ScopeAmbiguous)
			if !tc.wantAvailable {
				require.False(t, health.Fresh)
				require.False(t, health.Healthy)
			}
		})
	}
}

func TestServiceHealthScopeChangesAcrossBatchesOnSameCollector(t *testing.T) {
	now := time.Now().UTC()
	store := ingest.NewMemoryStore()
	tool := &serviceHealthTool{store: store, maxAge: time.Minute}
	read := func(service, podUID string, reverseLabels bool) serviceHealthToolData {
		labels := []*telemetryv1.Label{{Key: "service", Value: service}, {Key: "workload", Value: "api"}, {Key: "pod_uid", Value: podUID}}
		otherLabels := labels
		if reverseLabels {
			otherLabels = []*telemetryv1.Label{{Key: "pod_uid", Value: podUID}, {Key: "workload", Value: "api"}, {Key: "service", Value: service}}
		}
		store.StoreMetrics("collector-a", []*telemetryv1.Metric{
			{Name: "service_latency_p95_ms", Value: 200, TimestampUnixNano: now.UnixNano(), Labels: labels},
			{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano(), Labels: otherLabels},
		}, now)
		result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
		require.NoError(t, err)
		return result.Data.(serviceHealthToolData)
	}
	before := read("checkout", "pod-a", false)
	reordered := read("checkout", "pod-a", true)
	otherPod := read("checkout", "pod-b", false)
	otherService := read("payments", "pod-b", false)
	require.True(t, before.Available)
	require.True(t, reordered.Available)
	require.True(t, otherPod.Available)
	require.True(t, otherService.Available)
	require.Equal(t, before.ScopeKey, reordered.ScopeKey)
	require.NotEqual(t, before.ScopeKey, otherPod.ScopeKey)
	require.NotEqual(t, otherPod.ScopeKey, otherService.ScopeKey)
}

func TestCollectorIdentityChangeInvalidatesExistingServiceSamples(t *testing.T) {
	now := time.Now().UTC()
	store := ingest.NewMemoryStore()
	metrics := []*telemetryv1.Metric{
		{Name: "service_latency_p95_ms", Value: 200, TimestampUnixNano: now.UnixNano()},
		{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: now.UnixNano()},
	}
	tool := &serviceHealthTool{store: store, maxAge: time.Minute}
	read := func() serviceHealthToolData {
		result, err := tool.Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
		require.NoError(t, err)
		return result.Data.(serviceHealthToolData)
	}
	store.StoreMetrics("collector-a", metrics, now)
	require.True(t, read().Available)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a", Labels: []*telemetryv1.Label{{Key: "service", Value: "checkout"}}}, now)
	require.False(t, read().Available)
	require.True(t, read().ScopeAmbiguous)
	store.StoreMetrics("collector-a", metrics, now)
	require.True(t, read().Available)
}

func TestRemediationReportCarriesPreflightEvidenceGaps(t *testing.T) {
	cfg := DefaultWorkflowConfig()
	cfg.ValidationMaxIterations = 1
	cfg.ValidationMaxToolCalls = 1
	store := ingest.NewMemoryStore()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-stale"}, time.Now().Add(-10*time.Minute))
	engine := &WorkflowEngine{cfg: cfg, tools: newWorkflowToolManager(zap.NewNop(), &operatorPreflightTool{store: store, maxAge: time.Minute})}
	state := &workflowState{engine: engine, workflowID: "wf-preflight-gaps", workflowType: "rca", collectorID: "collector-stale", window: 5 * time.Minute}
	handoff := AnalysisHandoff{SuggestedValidationTargets: []ValidationTarget{{
		ID: "target-remediation", Type: ValidationTargetRemediation, Title: "check proposed restart", SuggestedTools: []ToolName{ToolOperatorPreflight},
	}}}

	report := newValidationActionAgent(cfg, zap.NewNop()).RunDecoded(context.Background(), state, handoff)
	require.Len(t, report.Results, 1)
	require.Equal(t, ValidationVerdictInsufficientEvidence, report.Results[0].Verdict)
	require.Contains(t, report.Results[0].EvidenceGaps, "service metric snapshot is missing a timestamp or is stale")
	require.Contains(t, report.Results[0].EvidenceGaps, "no executed action or before/after comparison")
	require.Empty(t, report.Results[0].SupportingEvidenceIDs)
	require.NotEmpty(t, report.UnresolvedUncertainty)
}

func TestFreshHeartbeatCannotMakeOldServiceMetricsCurrent(t *testing.T) {
	now := time.Now().UTC()
	stale := now.Add(-10 * time.Minute)
	store := ingest.NewMemoryStore()
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: stale.UnixNano()},
		{Name: "service_latency_p95_ms", Value: 200, TimestampUnixNano: stale.UnixNano()},
	}, stale)
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a"}, now)
	require.WithinDuration(t, now, store.Node("collector-a").UpdatedAt, time.Second)
	require.WithinDuration(t, stale, store.Node("collector-a").LastMetricAt, time.Second)

	healthResult, err := (&serviceHealthTool{store: store, maxAge: time.Minute}).Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
	require.NoError(t, err)
	health := healthResult.Data.(serviceHealthToolData)
	require.False(t, health.Fresh)
	require.False(t, health.Available)
	require.False(t, health.Healthy)

	preflightResult, err := (&operatorPreflightTool{store: store, maxAge: time.Minute}).Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
	require.NoError(t, err)
	preflight := preflightResult.Data.(operatorPreflightData)
	require.False(t, preflight.Fresh)
	require.False(t, preflight.HealthAvailable)
	require.Contains(t, preflight.EvidenceGaps, "service metric snapshot is missing a timestamp or is stale")
}

func TestServiceHealthRequiresFreshIndividualSamples(t *testing.T) {
	now := time.Now().UTC()
	stale := now.Add(-10 * time.Minute)
	for _, tc := range []struct {
		name           string
		latencySample  time.Time
		errorSample    time.Time
		wantObservedAt time.Time
		wantFresh      bool
		wantAvailable  bool
	}{
		{name: "old latency in fresh batch", latencySample: stale, errorSample: now, wantObservedAt: stale},
		{name: "missing latency sample time", errorSample: now},
		{name: "all health samples fresh", latencySample: now, errorSample: now, wantObservedAt: now, wantFresh: true, wantAvailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			latencyUnixNano := int64(0)
			if !tc.latencySample.IsZero() {
				latencyUnixNano = tc.latencySample.UnixNano()
			}
			store := ingest.NewMemoryStore()
			store.StoreBatchMeta("collector-a", &telemetryv1.TelemetryBatch{WallTimeUnixNano: now.UnixNano()}, now)
			store.StoreMetrics("collector-a", []*telemetryv1.Metric{
				{Name: "service_latency_p95_ms", Value: 200, TimestampUnixNano: latencyUnixNano},
				{Name: "service_error_rate", Value: 0.01, TimestampUnixNano: tc.errorSample.UnixNano()},
			}, now)
			store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: "collector-a"}, now)

			result, err := (&serviceHealthTool{store: store, maxAge: time.Minute}).Run(context.Background(), workflowToolRequest{CollectorID: "collector-a"})
			require.NoError(t, err)
			health := result.Data.(serviceHealthToolData)
			require.Equal(t, tc.wantFresh, health.Fresh)
			require.Equal(t, tc.wantAvailable, health.Available)
			require.Equal(t, tc.wantAvailable, health.Healthy)
			if tc.wantObservedAt.IsZero() {
				require.True(t, health.ObservedAt.IsZero())
			} else {
				require.WithinDuration(t, tc.wantObservedAt, health.ObservedAt, time.Second)
			}
		})
	}
}
