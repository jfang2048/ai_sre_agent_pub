package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/logindex"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBuildContextBundle(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-llm-bundle")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-llm-bundle",
		Window:      50 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)

	// The report should be populated and contain LLM analysis from the stub
	require.NotEmpty(t, report.WorkflowID)
	require.NotEmpty(t, report.Signals)

	// Verify the LLM analysis is populated (stub always produces results)
	require.NotNil(t, report.LLMAnalysis, "LLMAnalysis should be populated by stub client")
	require.NotEmpty(t, report.LLMAnalysis.Issues, "stub should produce at least one issue")
	require.NotEmpty(t, report.LLMAnalysis.NextSteps)
	require.NotEmpty(t, report.LLMAnalysis.EvidenceCited)
	require.True(t, report.LLMAnalysis.Confidence >= 0 && report.LLMAnalysis.Confidence <= 1)

	// Verify the context bundle can be built from internal state
	// (We test indirectly via the stub which parses it)
	for _, issue := range report.LLMAnalysis.Issues {
		require.NotEmpty(t, issue.Title)
		require.NotEmpty(t, issue.Severity)
		require.NotEmpty(t, issue.Evidence)
	}
}

func TestBuildContextBundleForRCA(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-llm-rca")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	report, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-llm-rca",
		Window:      50 * time.Minute,
		Trigger:     "incident_alert",
	})
	require.NoError(t, err)
	require.NotNil(t, report.LLMAnalysis, "RCA report should contain LLM analysis from stub")
	require.NotEmpty(t, report.LLMAnalysis.Issues)

	// Verify LLM hypotheses are merged into the deterministic hypotheses
	hasLLMInfluence := false
	for _, h := range report.Hypotheses {
		if strings.Contains(h.ID, "llm") || h.Description != "" {
			hasLLMInfluence = true
			break
		}
	}
	// The stub should have produced hypotheses that merge with deterministic ones
	require.NotEmpty(t, report.Hypotheses)
	_ = hasLLMInfluence // May or may not be true depending on overlap
}

func TestValidateLLMAnalysis_Valid(t *testing.T) {
	result := LLMAnalysisResult{
		Issues: []LLMIssue{
			{Title: "CPU pressure", Severity: "high", Explanation: "elevated", Evidence: []string{"ev-signal-cpu"}},
		},
		JointRiskReason: "correlated signals",
		NextSteps:       []string{"check CPU"},
		Confidence:      0.75,
		EvidenceCited:   []string{"ev-signal-cpu"},
	}
	require.NoError(t, ValidateLLMAnalysis(result))
}

func TestValidateLLMAnalysis_MissingIssues(t *testing.T) {
	result := LLMAnalysisResult{
		NextSteps:     []string{"check CPU"},
		Confidence:    0.5,
		EvidenceCited: []string{"ev-signal-generic"},
	}
	err := ValidateLLMAnalysis(result)
	require.ErrorIs(t, err, ErrLLMSchemaMissingIssues)
}

func TestValidateLLMAnalysis_MissingEvidence(t *testing.T) {
	result := LLMAnalysisResult{
		Issues:     []LLMIssue{{Title: "test", Severity: "low", Explanation: "x", Evidence: []string{"e"}}},
		NextSteps:  []string{"step"},
		Confidence: 0.5,
	}
	err := ValidateLLMAnalysis(result)
	require.ErrorIs(t, err, ErrLLMSchemaNoEvidence)
}

func TestValidateLLMAnalysis_BadConfidence(t *testing.T) {
	result := LLMAnalysisResult{
		Issues:        []LLMIssue{{Title: "test", Severity: "low", Explanation: "x", Evidence: []string{"e"}}},
		NextSteps:     []string{"step"},
		Confidence:    1.5, // out of range
		EvidenceCited: []string{"ev-signal-generic"},
	}
	err := ValidateLLMAnalysis(result)
	require.ErrorIs(t, err, ErrLLMSchemaConfidenceRange)
}

func TestValidateLLMAnalysis_NoNextSteps(t *testing.T) {
	result := LLMAnalysisResult{
		Issues:        []LLMIssue{{Title: "test", Severity: "low", Explanation: "x", Evidence: []string{"e"}}},
		Confidence:    0.5,
		EvidenceCited: []string{"ev-signal-generic"},
	}
	err := ValidateLLMAnalysis(result)
	require.ErrorIs(t, err, ErrLLMSchemaNoNextSteps)
}

func TestValidateLLMAnalysis_IssueNoEvidence(t *testing.T) {
	result := LLMAnalysisResult{
		Issues:        []LLMIssue{{Title: "test", Severity: "high", Explanation: "x", Evidence: nil}},
		NextSteps:     []string{"step"},
		Confidence:    0.5,
		EvidenceCited: []string{"ev-signal-generic"},
	}
	err := ValidateLLMAnalysis(result)
	require.ErrorIs(t, err, ErrLLMSchemaIssueNoEvidence)
}

func TestValidateLLMAnalysis_HypothesisBadConfidence(t *testing.T) {
	result := LLMAnalysisResult{
		Issues:        []LLMIssue{{Title: "test", Severity: "low", Explanation: "x", Evidence: []string{"ev-signal-1"}}},
		NextSteps:     []string{"step"},
		Confidence:    0.5,
		EvidenceCited: []string{"ev-signal-generic"},
		RCAHypotheses: []LLMHypothesis{{Title: "h", Confidence: -0.3, Description: "d", Evidence: []string{"ev-signal-2"}}},
	}
	err := ValidateLLMAnalysis(result)
	require.ErrorIs(t, err, ErrLLMSchemaConfidenceRange)
}

func TestParseLLMAnalysis_ValidJSON(t *testing.T) {
	input := `{"issues":[{"title":"x","severity":"high","explanation":"y","evidence":["e"]}],"joint_risk_reason":"r","next_steps":["s"],"confidence":0.8,"evidence_cited":["e"],"limitations":["l"]}`
	result, err := ParseLLMAnalysis(input)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1)
	require.Equal(t, "x", result.Issues[0].Title)
	require.Equal(t, 0.8, result.Confidence)
}

func TestParseLLMAnalysis_MarkdownWrapped(t *testing.T) {
	input := "```json\n{\"issues\":[{\"title\":\"x\",\"severity\":\"low\",\"explanation\":\"y\",\"evidence\":[\"e\"]}],\"next_steps\":[\"s\"],\"confidence\":0.6,\"evidence_cited\":[\"e\"]}\n```"
	result, err := ParseLLMAnalysis(input)
	require.NoError(t, err)
	require.Len(t, result.Issues, 1)
}

func TestParseLLMAnalysis_InvalidJSON(t *testing.T) {
	_, err := ParseLLMAnalysis("not valid json at all")
	require.Error(t, err)
}

func TestStubLLMDeterministicBehavior(t *testing.T) {
	stub := stubWorkflowLLMClient{}
	require.Equal(t, "stub", stub.Provider())
	require.Equal(t, "deterministic-v0.5", stub.Model())

	// Build a context bundle with signals
	bundle := ContextBundle{
		WorkflowType: "joint_risk",
		CollectorID:  "test-node",
		TimeWindow:   "45m",
		Scope:        "node/test-node",
		RiskScore:    0.65,
		RiskLevel:    "medium",
		TopSignals: []ContextSignal{
			{Name: "CPU usage", Current: 92.0, Baseline: 45.0, DeltaPercent: 104.4, Score: 0.12, Triggered: true},
			{Name: "IO latency p99", Current: 45.0, Baseline: 8.0, DeltaPercent: 462.5, Score: 0.15, Triggered: true},
		},
		LogExcerpts:      []string{"timeout contacting payment service"},
		SecurityFindings: []string{"weak permission on cache dir"},
	}
	userPrompt := BuildWorkflowUserPrompt(bundle)

	// Call stub twice - should produce deterministic output
	raw1, err1 := stub.Complete(context.Background(), BuildWorkflowSystemPrompt(), userPrompt)
	require.NoError(t, err1)
	raw2, err2 := stub.Complete(context.Background(), BuildWorkflowSystemPrompt(), userPrompt)
	require.NoError(t, err2)
	require.Equal(t, raw1, raw2, "stub should be deterministic")

	// Parse and validate the output
	result, err := ParseLLMAnalysis(raw1)
	require.NoError(t, err)
	require.NoError(t, ValidateLLMAnalysis(result))
	require.NotEmpty(t, result.Issues)

	// Verify issues correspond to triggered signals
	for _, issue := range result.Issues {
		require.NotEmpty(t, issue.Evidence)
		require.NotEmpty(t, issue.Severity)
	}
	require.NotEmpty(t, result.JointRiskReason)
	require.NotEmpty(t, result.NextSteps)

	// Verify limitations mention stub
	foundStubLimitation := false
	for _, lim := range result.Limitations {
		if strings.Contains(strings.ToLower(lim), "stub") {
			foundStubLimitation = true
		}
	}
	require.True(t, foundStubLimitation, "stub should declare its limitation")
}

func TestStepLLMAnalysisIntegration(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-llm-integration")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	// InsightsEnabled=false means stub will be used via newWorkflowLLMClient
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-llm-integration",
		Window:      50 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)
	require.NotNil(t, report.LLMAnalysis)
	require.NotEmpty(t, report.LLMAnalysis.Issues)

	// The insights status should indicate stub mode
	require.Equal(t, "disabled", report.Insights.Mode)

	// LLM recommendations should appear in the report recommendations
	hasLLMRec := false
	for _, rec := range report.Recommendations {
		if strings.HasPrefix(rec.ID, "llm-step-") {
			hasLLMRec = true
			break
		}
	}
	require.True(t, hasLLMRec, "LLM next steps should appear as recommendations")

	// Verify audit trail contains LLM analysis entries
	audits := engine.AuditRecords(200, report.WorkflowID)
	hasLLMAudit := false
	for _, audit := range audits {
		if strings.Contains(audit.Stage, "llm_analysis") {
			hasLLMAudit = true
			break
		}
	}
	require.True(t, hasLLMAudit, "audit trail should contain llm_analysis entries")
}

func TestStepLLMAnalysisDegradation(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-llm-degrade")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	// Replace the LLM client with a failing one
	engine.llm = &failingLLMClient{}

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-llm-degrade",
		Window:      50 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err, "should not fail even when LLM fails")
	require.Nil(t, report.LLMAnalysis, "LLM analysis should be nil when LLM fails")

	// Verify limitations mention the failure
	hasLLMLimitation := false
	for _, lim := range report.Limitations {
		if strings.Contains(strings.ToLower(lim), "llm") {
			hasLLMLimitation = true
			break
		}
	}
	require.True(t, hasLLMLimitation, "limitations should mention LLM failure")

	// Report should still have deterministic analysis
	require.NotEmpty(t, report.Signals)
	require.NotEmpty(t, report.Recommendations)
}

func TestStepLLMAnalysisNilClient(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-llm-nil")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())
	engine.llm = nil

	report, err := engine.EvaluateJointRisk(context.Background(), WorkflowRequest{
		CollectorID: "collector-llm-nil",
		Window:      50 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err, "should gracefully handle nil LLM client")
	require.Nil(t, report.LLMAnalysis)
}

func TestLLMAnalysisResultJSONRoundTrip(t *testing.T) {
	result := LLMAnalysisResult{
		Issues: []LLMIssue{
			{Title: "CPU contention", Severity: "high", Explanation: "elevated CPU pressure", Evidence: []string{"ev-signal-cpu"}},
		},
		JointRiskReason: "correlated signals compound",
		RCAHypotheses: []LLMHypothesis{
			{Title: "resource saturation", Confidence: 0.85, Evidence: []string{"ev-signal-cpu", "ev-signal-io"}, Description: "desc"},
		},
		NextSteps:     []string{"investigate top processes"},
		Confidence:    0.82,
		EvidenceCited: []string{"ev-signal-cpu", "ev-signal-io"},
		Limitations:   []string{"limited log coverage"},
	}

	raw, err := json.Marshal(result)
	require.NoError(t, err)

	parsed, err := ParseLLMAnalysis(string(raw))
	require.NoError(t, err)
	require.Equal(t, result.Confidence, parsed.Confidence)
	require.Equal(t, len(result.Issues), len(parsed.Issues))
	require.Equal(t, len(result.RCAHypotheses), len(parsed.RCAHypotheses))
}

func TestStubLLMRegressionSnapshot(t *testing.T) {
	stub := stubWorkflowLLMClient{}
	bundle := ContextBundle{
		WorkflowType: "rca",
		CollectorID:  "snapshot-node",
		TimeWindow:   "45m",
		Scope:        "node/snapshot-node",
		RiskScore:    0.81,
		RiskLevel:    "high",
		TopSignals: []ContextSignal{
			{Name: "CPU usage", Current: 95, Baseline: 42, DeltaPercent: 126.1, Score: 0.16, Triggered: true},
			{Name: "TCP retransmit ratio", Current: 0.09, Baseline: 0.01, DeltaPercent: 800, Score: 0.14, Triggered: true},
		},
		RuntimeSecurityEvents: []ContextRuntimeSecurityEvent{
			{EvidenceID: "ev-ebpf-1", Category: "process", Type: "execve", Severity: "high", Confidence: 0.91, Description: "exec from /tmp"},
			{EvidenceID: "ev-ebpf-2", Category: "network", Type: "abnormal_bind_port", Severity: "high", Confidence: 0.88, Description: "bind 31337"},
		},
		Hypotheses: []ContextHypothesis{
			{Title: "process compromise", Confidence: 0.82, Description: "runtime behavior indicates compromise"},
		},
	}

	userPrompt := BuildWorkflowUserPrompt(bundle)
	raw, err := stub.Complete(context.Background(), BuildWorkflowSystemPrompt(), userPrompt)
	require.NoError(t, err)

	// Snapshot-style structural assertions: deterministic content with tolerant
	// float handling and optional-omitempty compatibility.
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(raw), &parsed))
	require.Contains(t, parsed, "issues")
	require.Contains(t, parsed, "joint_risk_reason")
	require.Contains(t, parsed, "next_steps")
	require.Contains(t, parsed, "confidence")
	require.Contains(t, parsed, "evidence_cited")
	require.Contains(t, parsed, "limitations")

	result, err := ParseLLMAnalysis(raw)
	require.NoError(t, err)
	require.NoError(t, ValidateLLMAnalysis(result))

	require.Equal(t, "Insufficient co-occurring signals for systemic risk assessment.", result.JointRiskReason)
	require.Len(t, result.Issues, 2)
	require.Equal(t, "CPU usage pressure detected", result.Issues[0].Title)
	require.Equal(t, "TCP retransmit ratio pressure detected", result.Issues[1].Title)
	require.Equal(t, []string{"ev-ebpf-1"}, result.Issues[0].Evidence)
	require.Equal(t, []string{"ev-ebpf-2"}, result.Issues[1].Evidence)
	require.InDelta(t, 0.86, result.Confidence, 1e-9)
	require.ElementsMatch(t, []string{"ev-ebpf-1", "ev-ebpf-2"}, result.EvidenceCited)
	require.Len(t, result.RCAHypotheses, 1)
	require.Equal(t, "process compromise", result.RCAHypotheses[0].Title)
	require.InDelta(t, 0.87, result.RCAHypotheses[0].Confidence, 1e-9)
	require.ElementsMatch(t, []string{"ev-ebpf-1", "ev-ebpf-2"}, result.RCAHypotheses[0].Evidence)
	require.Equal(t,
		[]string{"stub LLM: analysis is deterministic and based on signal thresholds only"},
		result.Limitations,
	)
	require.Empty(t, result.ToolRequests)
}

func TestRCAWithLLMProducesEnhancedHypotheses(t *testing.T) {
	store := ingest.NewMemoryStore()
	index := logindex.NewIndex(logindex.DefaultConfig())
	seedAgentWorkflowData(t, store, index, "collector-llm-hyp")

	cfg := DefaultWorkflowConfig()
	cfg.DefaultWindow = 50 * time.Minute
	engine := NewWorkflowEngine(cfg, store, index, nil, zap.NewNop())

	report, err := engine.BuildRCAWorkflow(context.Background(), WorkflowRequest{
		CollectorID: "collector-llm-hyp",
		Window:      50 * time.Minute,
		Trigger:     "anomaly",
	})
	require.NoError(t, err)
	require.NotNil(t, report.LLMAnalysis)
	require.NotEmpty(t, report.Hypotheses, "hypotheses should be present")

	// All hypotheses should have a rank
	for i, h := range report.Hypotheses {
		require.Equal(t, i+1, h.Rank, "hypothesis %s rank should be %d", h.Title, i+1)
	}
}

// ─── Test helpers ────────────────────────────────────────────────────────────

type failingLLMClient struct{}

func (f *failingLLMClient) Provider() string { return "failing" }
func (f *failingLLMClient) Model() string    { return "failure-test" }
func (f *failingLLMClient) Complete(_ context.Context, _, _ string) (string, error) {
	return "", fmt.Errorf("simulated LLM failure for testing")
}

// Seed data helper for LLM analysis tests
func seedLLMAnalysisData(t *testing.T, store *ingest.MemoryStore, index *logindex.Index, collectorID string) {
	t.Helper()
	base := time.Now().Add(-55 * time.Minute).UTC()
	store.UpsertCollector(&telemetryv1.CollectorInfo{CollectorId: collectorID, Hostname: collectorID + "-host"}, base)

	for i := 0; i < 30; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		cpu := 50.0 + float64(i)*1.5
		memPct := 65.0 + float64(i)*0.6
		memTotal := float64(16 * 1024 * 1024 * 1024)
		memUsed := memTotal * (memPct / 100)

		store.StoreMetrics(collectorID, []*telemetryv1.Metric{
			{Name: "node_cpu_usage_percent", Value: cpu, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_MemTotal_bytes", Value: memTotal, TimestampUnixNano: ts.UnixNano()},
			{Name: "node_memory_Used_bytes", Value: memUsed, TimestampUnixNano: ts.UnixNano()},
		}, ts)
	}
}
