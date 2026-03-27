package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestEvaluateJointRiskDetectsCooccurrence(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-joint-risk")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetKnowledgeBase(newWorkflowTestKnowledgeBase(t))

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-joint-risk",
		Window:      50 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.WorkflowID)
	require.Equal(t, "collector-joint-risk", report.CollectorID)
	require.NotEmpty(t, report.Signals)
	require.NotEmpty(t, report.ScopeRisks)
	require.NotEmpty(t, report.Series)
	require.NotEmpty(t, report.BehavioralAssessments)
	require.NotEmpty(t, report.Recommendations)
	require.NotEmpty(t, report.Stages)
	require.NotEmpty(t, report.ToolCalls)
	require.True(t, strings.Contains(strings.ToLower(report.ActionableWhy), "co-occurred") || len(report.Cooccurrences) > 0)
	require.NotEqual(t, "", report.Insights.Mode)
	recommendationText := strings.ToLower(strings.Join(recommendationSummaries(report.Recommendations), " | "))
	require.Contains(t, recommendationText, "trend")
	require.True(t, strings.Contains(recommendationText, "weak-signal") || strings.Contains(recommendationText, "cluster"))
}

func TestBuildRCAWorkflowProducesStructuredHypothesesAndEvidence(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-rca")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	cfg.WorkflowDataPath = t.TempDir()
	cfg.WorkflowStorePath = filepath.Join(cfg.WorkflowDataPath, "workflow_runs.db")
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetKnowledgeBase(newWorkflowTestKnowledgeBase(t))

	report, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-rca",
		Window:      50 * time.Minute,
		Trigger:     "incident_alert",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.WorkflowID)
	require.Equal(t, "collector-rca", report.CollectorID)
	require.NotEmpty(t, report.Context.TopMetrics)
	require.NotEmpty(t, report.Hypotheses)
	require.NotEmpty(t, report.Evidence)
	require.Equal(t, "ai_sre_agent/evidence/v1", report.EvidenceSchemaVersion)
	require.NotEmpty(t, report.NormalizedEvidence)
	require.NotEmpty(t, report.Recommendations)
	require.NotEmpty(t, report.ToolCalls)
	require.NotEmpty(t, report.Stages)
	require.NotEmpty(t, report.Reproducibility)
	require.NotEmpty(t, report.AgentLoop.PlanSteps)
	require.NotEmpty(t, report.StructuredReport.Timeline)
	require.NotEmpty(t, report.StructuredReport.SupportingSignals)
	require.NotEmpty(t, report.SynthesizedIncident.GroupedSignals)
	require.NotEmpty(t, report.SynthesizedIncident.ImpactedScope)
	require.NotEmpty(t, report.ProposedActions)
	require.NotEmpty(t, report.TraceID)
	require.NotEmpty(t, report.ChangeLinks)
	require.NotEmpty(t, report.BehavioralAssessments)
	require.NotEmpty(t, report.AdaptiveBaselines)
	require.NotEmpty(t, report.SuspectedRootCauseEntity)
	require.NotEmpty(t, report.StructuredReport.CausalPath)
	require.NotEmpty(t, report.StructuredReport.EvidenceProvenance)
	require.NotEmpty(t, report.StructuredReport.Uncertainty)
	require.Equal(t, "analysis_agent", report.AnalysisHandoff.Agent)
	require.Equal(t, "validation_action_agent", report.Validation.Agent)
	require.NotEmpty(t, report.AnalysisHandoff.SuggestedValidationTargets)
	require.NotEmpty(t, report.Validation.Results)
	require.NotEmpty(t, report.Validation.LoopRecords)
	require.Equal(t, report.AnalysisHandoff.CollectorID, report.CollectorID)
	require.Equal(t, "bounded_react", report.AgentLoop.Mode)

	hasEvidenceLinked := false
	for _, hypothesis := range report.Hypotheses {
		if len(hypothesis.EvidenceIDs) > 0 {
			hasEvidenceLinked = true
			break
		}
	}
	require.True(t, hasEvidenceLinked)
	require.True(t, len(report.UnresolvedGaps) >= 0)

	foundKinds := map[string]bool{}
	for _, item := range report.NormalizedEvidence {
		require.Equal(t, "ai_sre_agent/evidence/v1", item.SchemaVersion)
		foundKinds[item.Kind] = true
	}
	require.True(t, foundKinds["host_inventory"])
	require.True(t, foundKinds["knowledge_hit"])
	require.True(t, foundKinds["runtime_event"] || foundKinds["security_finding"])
	require.True(t, foundKinds["remediation_plan"])

	audits := engine.AuditRecords(200, report.WorkflowID)
	require.NotEmpty(t, audits)

	trace, ok := engine.traceStore.GetTrace(report.TraceID)
	require.True(t, ok)
	require.NotNil(t, trace.ReasoningReview)
	require.NotEmpty(t, trace.ReasoningReview.Final.Plan)
	require.Equal(t, report.EvidenceSchemaVersion, trace.EvidenceSchemaVersion)
	require.NotEmpty(t, trace.NormalizedEvidence)
	require.NotEmpty(t, report.EvidencePackagePath)

	rawEvidence, err := os.ReadFile(report.EvidencePackagePath)
	require.NoError(t, err)
	require.Contains(t, string(rawEvidence), report.WorkflowID)
	require.Contains(t, string(rawEvidence), "\"analysis_handoff\"")
	require.Contains(t, string(rawEvidence), "\"validation\"")

	run, err := engine.DurableRun(context.Background(), report.WorkflowID)
	require.NoError(t, err)
	require.Equal(t, RunStatusCompleted, run.Status)
	require.Equal(t, "collector-rca", run.Request.CollectorID)
	require.NotEmpty(t, run.ToolCalls)
	require.NotEmpty(t, run.Steps)
	require.NotNil(t, run.WorldModel)
	require.NotNil(t, run.EvidencePackage)
	require.Equal(t, report.EvidencePackagePath, run.EvidencePackage.Path)
	require.NotEmpty(t, run.MemoryRecords)
	require.NotEmpty(t, run.Events)
	require.NotNil(t, run.AnalysisHandoff)
	require.NotNil(t, run.Validation)
	require.NotEmpty(t, run.ValidationLoops)
	require.Equal(t, "analysis_agent", run.AnalysisHandoff.Agent)
	require.Equal(t, "validation_action_agent", run.Validation.Agent)
}

func TestBuildRCAWorkflowRetrievesIncidentMemoryOnSubsequentRun(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-rca-memory")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	cfg.WorkflowDataPath = t.TempDir()
	cfg.WorkflowStorePath = filepath.Join(cfg.WorkflowDataPath, "workflow_runs.db")
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.SetKnowledgeBase(newWorkflowTestKnowledgeBase(t))

	_, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-rca-memory",
		Window:      50 * time.Minute,
		Trigger:     "incident_alert_first",
	})
	require.NoError(t, err)

	second, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-rca-memory",
		Window:      50 * time.Minute,
		Trigger:     "incident_alert_second",
	})
	require.NoError(t, err)
	require.NotEmpty(t, second.IncidentMemoryMatches)
	require.Equal(t, "incident_memory", second.IncidentMemoryMatches[0].SourceType)
	require.NotEmpty(t, second.StructuredReport.IncidentMemoryMatches)
}

func TestEvaluateJointRiskDedupesIdenticalRequests(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-joint-risk-dedupe")

	cfg := DefaultWorkflowConfig()
	cfg.RequestDedupeTTL = time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	req := WorkflowRequest{
		CollectorID: "collector-joint-risk-dedupe",
		Window:      45 * time.Minute,
		Trigger:     "api_refresh",
	}
	first, err := engine.EvaluateJointRisk(context.Background(), req)
	require.NoError(t, err)
	second, err := engine.EvaluateJointRisk(context.Background(), req)
	require.NoError(t, err)

	require.Equal(t, first.WorkflowID, second.WorkflowID)
	reports := engine.JointRiskReports(10, "collector-joint-risk-dedupe")
	require.Len(t, reports, 1)
	audits := engine.AuditRecords(200, first.WorkflowID)
	require.NotEmpty(t, audits)
	require.Equal(t, "workflow.cache_hit", audits[0].Action)
}

func TestBuildRCAWorkflowDedupesIdenticalRequests(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-rca-dedupe")

	cfg := DefaultWorkflowConfig()
	cfg.RequestDedupeTTL = time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	req := WorkflowRequest{
		CollectorID: "collector-rca-dedupe",
		Window:      45 * time.Minute,
		Trigger:     "api_refresh",
	}
	first, err := engine.BuildRCAWorkflow(context.Background(), req)
	require.NoError(t, err)
	second, err := engine.BuildRCAWorkflow(context.Background(), req)
	require.NoError(t, err)

	require.Equal(t, first.WorkflowID, second.WorkflowID)
	require.Equal(t, first.IncidentID, second.IncidentID)
	require.Len(t, engine.RCAReports(10, "collector-rca-dedupe"), 1)
	require.Len(t, engine.IncidentReports(10, "", "collector-rca-dedupe"), 1)
	audits := engine.AuditRecords(200, first.WorkflowID)
	require.NotEmpty(t, audits)
	require.Equal(t, "workflow.cache_hit", audits[0].Action)
}

func TestBuildStructuredRCAReportMergesTimelineChronologically(t *testing.T) {
	base := time.Now().UTC()
	state := &workflowState{
		rca: RCAWorkflowReport{
			Anomalies: []string{"gpu latency spike"},
		},
		stages: []PipelineStageResult{
			{Name: "finalize", Summary: "done", CompletedAt: base.Add(3 * time.Minute)},
			{Name: "collect_signals", Summary: "collected", CompletedAt: base.Add(1 * time.Minute)},
		},
		planSteps: []AgentPlanStep{
			{Title: "Inspect hot process", Status: "verified", VerificationNote: "confirmed", CompletedAt: base.Add(2 * time.Minute)},
		},
	}

	report := buildStructuredRCAReport(state)
	require.Len(t, report.Timeline, 3)
	require.Equal(t, "collect_signals", report.Timeline[0].Phase)
	require.Equal(t, "plan_step", report.Timeline[1].Phase)
	require.Equal(t, "finalize", report.Timeline[2].Phase)
}

func TestEvaluateActionPolicyRequiresRollbackAndApproval(t *testing.T) {
	rec := WorkflowRecommendation{
		ID:               "contain-1",
		Category:         "probable_containment",
		Summary:          "Isolate noisy traffic source",
		Safe:             false,
		DryRunDefault:    true,
		RequiresApproval: true,
		Confidence:       0.81,
	}
	policy := EvaluateActionPolicy(rec, 0.81)
	require.Equal(t, "missing_rollback", policy.Status)
	require.Equal(t, "suggest_only", policy.ExecutionLevel)

	rec.RollbackHint = "remove temporary traffic rule"
	policy = EvaluateActionPolicy(rec, 0.81)
	require.Equal(t, "allowed_with_approval", policy.Status)
	require.True(t, policy.RequiresApproval)
	require.Equal(t, "approval_required", policy.ExecutionLevel)
}

func TestIncidentConfidenceCapsAgainstTelemetryQuality(t *testing.T) {
	state := &workflowState{
		risk: JointRiskAssessment{
			RiskScore: 0.90,
		},
		cooccurrences: []JointRiskCooccurrence{
			{Signals: []string{"cpu", "io"}},
		},
		retrievedDocs: []RetrievedDocumentEvidence{
			{EvidenceID: "doc-1", Score: 0.82},
		},
		telemetryQuality: PromptTelemetryQuality{
			State:      "stale",
			Confidence: 0.25,
		},
	}

	confidence := incidentConfidence(state, []IncidentGroupedSignal{{}, {}, {}})
	require.InDelta(t, 0.40, confidence, 0.001)
}

func TestUnresolvedGapsIncludeTelemetryBlindSpots(t *testing.T) {
	state := &workflowState{
		limitations: []string{"change intelligence unavailable"},
		telemetryQuality: PromptTelemetryQuality{
			State:           "degraded",
			CoveragePercent: 60,
			Confidence:      0.52,
			MissingSignals:  []string{"telemetry integrity"},
			BlindSpots:      []string{"process attribution is missing"},
		},
	}

	gaps := unresolvedGapsFromState(state)
	require.Contains(t, gaps, "no ranked hypothesis reached the evidence threshold")
	require.Contains(t, gaps, "RAG retrieval did not return corroborating evidence")
	require.Contains(t, gaps, "change intelligence unavailable")
	require.Contains(t, gaps, "process attribution is missing")
	require.Contains(t, gaps, "Missing critical signals: telemetry integrity")
}

func TestPotentialRiskFindingsIncludesStructuredFields(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-potential-risk")

	cfg := DefaultWorkflowConfig()
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	err := engine.RefreshPotentialRiskFindings(context.Background(), WorkflowRequest{
		Window: 45 * time.Minute,
		Limit:  4,
	})
	require.NoError(t, err)

	findings := engine.PotentialRiskFindings(10, "collector-potential-risk")
	require.NotEmpty(t, findings)
	require.NotEmpty(t, findings[0].RiskSummary)
	require.NotEmpty(t, findings[0].ContributingSignals)
	require.NotEmpty(t, findings[0].Scope)
	require.NotEmpty(t, findings[0].SuggestedInvestigationSteps)
	require.Contains(t, strings.ToLower(findings[0].SuggestedInvestigationSteps[0]), "current")
}

func TestControlPlaneSummaryIncludesEventizedEvidence(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-summary")

	cfg := DefaultWorkflowConfig()
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	_, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-summary",
		Window:      45 * time.Minute,
		Trigger:     "api_refresh",
	})
	require.NoError(t, err)

	_, err = engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-summary",
		Window:      45 * time.Minute,
		Trigger:     "incident_alert",
	})
	require.NoError(t, err)

	summary := engine.ControlPlaneSummary()
	require.True(t, summary.Enabled)
	require.GreaterOrEqual(t, summary.JointRiskReports, 1)
	require.GreaterOrEqual(t, summary.RCAReports, 1)
	require.Equal(t, "collector-summary", summary.LatestCollectorID)
	require.NotZero(t, summary.LatestJointRiskAt)
	require.NotZero(t, summary.LatestRCAAt)
	require.NotEmpty(t, summary.LatestIncidentSummary)
	require.True(t, summary.TriggeredTrends >= 0)
	require.True(t, summary.InvestigationEvents >= 0)
	require.True(t, summary.WeakSignalClusters >= 0)
	require.GreaterOrEqual(t, summary.RetrievalDecisions, 1)
	require.GreaterOrEqual(t, summary.RecommendationCount, 1)
	require.NotEmpty(t, summary.TopRecommendation)
}

func TestBuildRCAWorkflowSurfacesTelemetryQuality(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-telemetry-quality")

	cfg := DefaultWorkflowConfig()
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	report, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-telemetry-quality",
		Window:      45 * time.Minute,
		Trigger:     "incident_alert",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.TelemetryQuality.State)
	require.Equal(t, report.TelemetryQuality.State, report.Context.TelemetryQuality.State)
	require.Equal(t, report.TelemetryQuality.Confidence, report.Context.TelemetryQuality.Confidence)
}

func TestWorkflowMetricsTrackReasoningRetrievalAndLatency(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-metrics")

	engine := NewWorkflowEngine(DefaultWorkflowConfig(), store, index, nil, zap.NewNop())
	engine.SetKnowledgeBase(newWorkflowTestKnowledgeBase(t))

	_, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-metrics",
		Window:      45 * time.Minute,
		Trigger:     "metrics-test",
	})
	require.NoError(t, err)

	_, err = engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-metrics",
		Window:      45 * time.Minute,
		Trigger:     "metrics-test",
	})
	require.NoError(t, err)

	stats := engine.Metrics()
	require.Greater(t, stats.ReasoningStepsTotal, uint64(0))
	require.Greater(t, stats.RetrievalHitsTotal, uint64(0))
	require.Greater(t, stats.WorkflowRunsTotal, uint64(0))
	require.Greater(t, stats.IncidentRCARunsTotal, uint64(0))
	require.Greater(t, stats.WorkflowLatencySeconds, 0.0)
	require.Greater(t, stats.IncidentRCALatencySeconds, 0.0)
	require.Greater(t, stats.TokenCostTotal, uint64(0))
	require.Greater(t, stats.AvgConfidence, 0.0)
}

func TestBuildInitialPlanStepsSkipsIrrelevantSecuritySteps(t *testing.T) {
	state := &workflowState{
		workflowType: "rca",
		collectorID:  "collector-plan",
		riskSignals: []JointRiskSignal{
			{
				ID:        "cpu_pressure",
				Name:      "CPU usage",
				Scope:     "node",
				Entity:    "collector-plan",
				Severity:  "high",
				Triggered: true,
			},
		},
		scopeRisks: []ScopeRisk{{Scope: "node", Entity: "collector-plan", Score: 0.73}},
		topoData: topologyToolData{
			Snapshot: TopologySnapshot{
				Nodes: []TopologyNode{{ID: "collector-plan", Name: "collector-plan", Type: "node"}},
			},
		},
	}

	steps := buildInitialPlanSteps(state)
	tools := make([]ToolName, 0, len(steps))
	for _, step := range steps {
		tools = append(tools, step.Tool)
	}

	require.Contains(t, tools, ToolMetrics)
	require.Contains(t, tools, ToolSimilarCase)
	require.Contains(t, tools, ToolRunbookRetrieval)
	require.NotContains(t, tools, ToolSecurity)
	require.NotContains(t, tools, ToolEBPFQuery)
	require.NotContains(t, tools, ToolSecurityGraph)
	require.NotContains(t, tools, ToolProcessLineage)
}

func TestBuildInitialPlanStepsAddsProfilingForDerivedHighPriority(t *testing.T) {
	state := &workflowState{
		workflowType: "rca",
		collectorID:  "collector-high-plan",
		riskSignals: []JointRiskSignal{
			{ID: "cpu_pressure", Name: "CPU usage", Severity: "high", Triggered: true},
			{ID: "io_latency", Name: "IO latency p99", Severity: "high", Triggered: true},
		},
	}

	steps := buildInitialPlanSteps(state)
	foundProfiling := false
	for _, step := range steps {
		if step.Tool == ToolProfiling {
			foundProfiling = true
			require.False(t, step.Required)
			break
		}
	}
	require.True(t, foundProfiling, "high-priority RCA plan should include bounded profiling prep even before joint-risk scoring materializes")
}

func TestRCAAgentLoopReportsVerificationGapsWhenRequiredEvidenceFails(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	collectorID := "collector-rca-gap"
	now := time.Now().UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: "gap-host"}, now)
	store.StoreMetrics(collectorID, []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 93, TimestampUnixNano: now.UnixNano()},
	}, now)

	cfg := DefaultWorkflowConfig()
	cfg.MaxPlanIterations = 1
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	report, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: collectorID,
		Window:      30 * time.Minute,
		Trigger:     "gap-check",
	})
	require.NoError(t, err)
	require.False(t, report.AgentLoop.Completed)
	require.Equal(t, "confidence remained too low", report.AgentLoop.StopReason)
	require.NotEmpty(t, report.AgentLoop.PlanSteps)
	require.Equal(t, ToolMetrics, report.AgentLoop.PlanSteps[0].Tool)
	require.False(t, report.AgentLoop.PlanSteps[0].Verified)
	require.True(t, strings.Contains(strings.ToLower(report.AgentLoop.PlanSteps[0].VerificationNote), "insufficient metric history"))
}

func seedAgentWorkflowData(t *testing.T, store *ingest.MemoryStore, index *logindex.Index, collectorID string) {
	t.Helper()

	base := time.Now().Add(-55 * time.Minute).UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: collectorID + "-host"}, base)

	for i := 0; i < 44; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		cpu := 40.0 + float64(i)*1.15
		if cpu > 95 {
			cpu = 95
		}
		memPct := 60.0 + float64(i)*0.65
		ioLatencyMs := 5.0 + float64(i)*1.7
		retrans := 0.001 + float64(i)*0.0004
		if retrans > 0.03 {
			retrans = 0.03
		}
		softnet := float64(i) * 0.75
		ioPressure := 1.2 + float64(i)*0.32
		if ioPressure > 26 {
			ioPressure = 26
		}
		memTotal := float64(16 * 1024 * 1024 * 1024)
		memUsed := memTotal * (memPct / 100)

		store.StoreMetrics(collectorID, []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: cpu, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_MemTotal_bytes", Value: memTotal, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_Used_bytes", Value: memUsed, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_disk_request_latency_p99_seconds", Value: ioLatencyMs / 1000.0, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_tcp_retransmit_ratio", Value: retrans, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_softnet_dropped_per_second", Value: softnet, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_pressure_io_full_avg10", Value: ioPressure, TimestampUnixNano: ts.UnixNano()},
			{Name: "rca_net_process_connections", Value: 380 + float64(i), TimestampUnixNano: ts.UnixNano(), Labels: []*telemetryv1.Label{{Key: "pid", Value: "2100"}, {Key: "name", Value: "checkout-api"}}},
			{Name: "rca_memory_process_rss_bytes", Value: 350*1024*1024 + float64(i)*1024*1024, TimestampUnixNano: ts.UnixNano(), Labels: []*telemetryv1.Label{{Key: "pid", Value: "2100"}, {Key: "name", Value: "checkout-api"}, {Key: "pod_uid", Value: "pod-checkout-a"}, {Key: "job", Value: "checkout"}}},
		}, ts)

		if i%4 == 0 {
			store.StoreLogs(collectorID, []*telemetryv1.LogFingerprint{
				{Fingerprint: "timeout", Count: 2 + uint64(i/4), Example: "error timeout contacting payment service after deploy"},
				{Fingerprint: "permissions", Count: 1, Example: "warning weak permission chmod 777 in cache directory"},
			}, ts)
		}

		if index != nil {
			level := "info"
			message := "healthy request"
			count := uint64(1)
			if i%3 == 0 {
				level = "error"
				count = 2
				message = "timeout contacting payment dependency after rollout checkout-v2"
			}
			if i%6 == 0 {
				level = "warn"
				message = "weak permission warning world-writable cache directory"
			}
			index.AddBatch([]logindex.RawEvent{{
				Timestamp:   ts,
				CollectorID: collectorID,
				Hostname:    collectorID + "-host",
				Service:     "checkout",
				Process:     "checkout-api",
				PID:         "2100",
				Level:       level,
				Source:      "app",
				Message:     message,
				Count:       count,
			}})
		}
	}
}
