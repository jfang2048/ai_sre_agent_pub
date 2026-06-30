package agent

import (
	"fmt"
	"testing"
	"time"

	evidencev1 "github.com/jfang2048/ai_sre_agent_pub/internal/controller/evidence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceStore_RecordAndGet(t *testing.T) {
	ts := NewTraceStore(100)
	now := time.Now()

	trace := &AgentTrace{
		TraceID:               "trace-001",
		WorkflowType:          "joint-risk",
		CollectorID:           "host-1",
		EvidenceSchemaVersion: evidencev1.SchemaVersionV1,
		NormalizedEvidence: []evidencev1.Record{
			{SchemaVersion: evidencev1.SchemaVersionV1, ID: "ev-1", Kind: "metric_signal", Summary: "cpu rising"},
		},
		StartedAt: now,
		Status:    "running",
	}
	ts.RecordTrace(trace)

	got, ok := ts.GetTrace("trace-001")
	require.True(t, ok)
	assert.Equal(t, "joint-risk", got.WorkflowType)
	assert.Equal(t, "host-1", got.CollectorID)
	require.NotEmpty(t, got.NormalizedEvidence)
	got.NormalizedEvidence[0].Summary = "mutated"

	again, ok := ts.GetTrace("trace-001")
	require.True(t, ok)
	assert.Equal(t, "cpu rising", again.NormalizedEvidence[0].Summary)

	_, ok = ts.GetTrace("nonexistent")
	assert.False(t, ok)
}

func TestTraceStore_Update(t *testing.T) {
	ts := NewTraceStore(100)
	now := time.Now()

	trace := &AgentTrace{TraceID: "trace-001", Status: "running", StartedAt: now}
	ts.RecordTrace(trace)

	updated := &AgentTrace{TraceID: "trace-001", Status: "completed", StartedAt: now, CompletedAt: now.Add(time.Minute)}
	ts.RecordTrace(updated)

	got, ok := ts.GetTrace("trace-001")
	require.True(t, ok)
	assert.Equal(t, "completed", got.Status)
	assert.Equal(t, 1, ts.Size()) // no duplicate
}

func TestTraceStore_ListTraces(t *testing.T) {
	ts := NewTraceStore(100)
	now := time.Now()

	for i := 0; i < 5; i++ {
		ts.RecordTrace(&AgentTrace{
			TraceID:   fmt.Sprintf("trace-%03d", i),
			StartedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}

	list := ts.ListTraces(3)
	require.Len(t, list, 3)
	assert.Equal(t, "trace-004", list[0].TraceID)
	assert.Equal(t, "trace-003", list[1].TraceID)
}

func TestTraceStore_Eviction(t *testing.T) {
	ts := NewTraceStore(5)
	now := time.Now()

	for i := 0; i < 6; i++ {
		ts.RecordTrace(&AgentTrace{
			TraceID:   fmt.Sprintf("trace-%03d", i),
			StartedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}

	assert.Equal(t, 5, ts.Size())
	_, ok := ts.GetTrace("trace-000")
	assert.False(t, ok, "oldest trace should be evicted")
	_, ok = ts.GetTrace("trace-005")
	assert.True(t, ok)
}

func TestTraceStore_AppendToolCall(t *testing.T) {
	ts := NewTraceStore(100)
	ts.RecordTrace(&AgentTrace{TraceID: "trace-001", StartedAt: time.Now()})

	ts.AppendToolCall("trace-001", WorkflowToolCall{
		ID:   "tc-1",
		Tool: ToolMetrics,
	})
	ts.AppendToolCall("trace-001", WorkflowToolCall{
		ID:   "tc-2",
		Tool: ToolLogs,
	})

	got, ok := ts.GetTrace("trace-001")
	require.True(t, ok)
	assert.Len(t, got.ToolCalls, 2)
}

func TestTraceStore_AppendRiskEntry(t *testing.T) {
	ts := NewTraceStore(100)
	ts.RecordTrace(&AgentTrace{TraceID: "trace-001", StartedAt: time.Now()})

	ts.AppendRiskEntry("trace-001", RiskTimelineEntry{
		Timestamp: time.Now(),
		RiskScore: 42.5,
		RiskLevel: "medium",
		Source:    "joint_risk",
	})

	got, ok := ts.GetTrace("trace-001")
	require.True(t, ok)
	assert.Len(t, got.RiskTimeline, 1)
	assert.InDelta(t, 42.5, got.RiskTimeline[0].RiskScore, 0.01)
}

func TestTraceStore_TracesByCollector(t *testing.T) {
	ts := NewTraceStore(100)
	now := time.Now()

	ts.RecordTrace(&AgentTrace{TraceID: "t1", CollectorID: "host-1", StartedAt: now})
	ts.RecordTrace(&AgentTrace{TraceID: "t2", CollectorID: "host-2", StartedAt: now})
	ts.RecordTrace(&AgentTrace{TraceID: "t3", CollectorID: "host-1", StartedAt: now})

	list := ts.TracesByCollector("host-1", 10)
	assert.Len(t, list, 2)
}

// ----- ProposedAction tests -----

func TestProposedActionStore_RecordAndGet(t *testing.T) {
	pas := NewProposedActionStore(100)

	action := &ProposedAction{
		ID:                    "act-001",
		RiskReference:         "wf-001",
		CommandPreview:        "kubectl rollout restart deployment/web",
		ImpactScope:           "deployment/web",
		RiskLevel:             "medium",
		ExecutionLevel:        "approval_required",
		Preconditions:         []string{"dry-run reviewed"},
		BlastRadius:           "deployment/web",
		IdempotencyNote:       "repeat only after rollout check",
		Timeout:               "2m",
		RollbackPlan:          "kubectl rollout undo deployment/web",
		ApprovalRequired:      true,
		OperatorJustification: "restart changes live traffic handling",
		Status:                "proposed",
		ProposedAt:            time.Now(),
	}
	pas.RecordAction(action)

	got, ok := pas.GetAction("act-001")
	require.True(t, ok)
	assert.Equal(t, "proposed", got.Status)
	assert.True(t, got.ApprovalRequired)
	assert.Equal(t, "approval_required", got.ExecutionLevel)
}

func TestProposedActionStore_UpdateStatus(t *testing.T) {
	pas := NewProposedActionStore(100)
	pas.RecordAction(&ProposedAction{ID: "act-001", Status: "proposed", ProposedAt: time.Now()})

	ok := pas.UpdateStatus("act-001", "approved")
	assert.True(t, ok)

	got, _ := pas.GetAction("act-001")
	assert.Equal(t, "approved", got.Status)

	ok = pas.UpdateStatus("nonexistent", "approved")
	assert.False(t, ok)
}

func TestProposedActionStore_ListByStatus(t *testing.T) {
	pas := NewProposedActionStore(100)
	now := time.Now()

	pas.RecordAction(&ProposedAction{ID: "a1", Status: "proposed", ProposedAt: now})
	pas.RecordAction(&ProposedAction{ID: "a2", Status: "approved", ProposedAt: now})
	pas.RecordAction(&ProposedAction{ID: "a3", Status: "proposed", ProposedAt: now})

	proposed := pas.ListActions(10, "proposed")
	assert.Len(t, proposed, 2)

	all := pas.ListActions(10, "")
	assert.Len(t, all, 3)
}

func TestProposedActionStore_Eviction(t *testing.T) {
	pas := NewProposedActionStore(3)
	now := time.Now()

	for i := 0; i < 4; i++ {
		pas.RecordAction(&ProposedAction{
			ID:         fmt.Sprintf("a%d", i),
			Status:     "proposed",
			ProposedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}

	assert.Equal(t, 3, pas.Size())
	_, ok := pas.GetAction("a0")
	assert.False(t, ok, "oldest action should be evicted")
}

func TestGenerateProposedActions(t *testing.T) {
	recs := []WorkflowRecommendation{
		{
			ID:               "rec-1",
			Category:         "probable_containment",
			Summary:          "Restart web service",
			Scope:            "service/web",
			Safe:             false,
			RequiresApproval: true,
			RollbackHint:     "kubectl rollout undo",
			Confidence:       0.8,
		},
		{
			ID:               "rec-2",
			Category:         "immediate_investigation",
			Summary:          "Scale up replicas",
			Scope:            "deployment/web",
			Safe:             true,
			RequiresApproval: false,
			RollbackHint:     "kubectl scale --replicas=2",
			Confidence:       0.7,
		},
	}

	actions := GenerateProposedActions("wf-001", "host-1", recs, 75.0)
	require.Len(t, actions, 2)
	assert.Equal(t, "high", actions[0].RiskLevel)
	assert.True(t, actions[0].ApprovalRequired)
	assert.False(t, actions[1].ApprovalRequired)
	assert.Equal(t, "allowed_with_approval", actions[0].Policy.Status)
	assert.Equal(t, "approval_required", actions[0].ExecutionLevel)
	assert.NotEmpty(t, actions[0].Preconditions)
	assert.Equal(t, "allowed", actions[1].Policy.Status)
}
