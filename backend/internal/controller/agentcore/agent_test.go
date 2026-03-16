package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/gpuobs"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/ingest"
	"github.com/jfang2048/ai_sre_agent_pub/internal/controller/rag"
	telemetryv1 "github.com/jfang2048/ai_sre_agent_pub/pkg/telemetry/v1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func requireLocalTCPBind(t *testing.T) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("skipping due to listen permission error: %v", err)
		}
		t.Fatalf("listen: %v", err)
	}
	_ = listener.Close()
}

type stubKnowledgeBase struct {
	result  rag.QueryResult
	lastReq rag.QueryRequest
	queries atomic.Int32
}

func (s *stubKnowledgeBase) Search(context.Context, string, int) ([]rag.Document, error) {
	return nil, nil
}

func (s *stubKnowledgeBase) Stats() rag.Stats {
	return rag.Stats{Enabled: true, Ready: true, RetrievalMode: s.result.RetrievalMode}
}

func (s *stubKnowledgeBase) Query(_ context.Context, req rag.QueryRequest) (rag.QueryResult, error) {
	s.lastReq = req
	s.queries.Add(1)
	return s.result, nil
}

func (s *stubKnowledgeBase) Rebuild(context.Context) (rag.Stats, error) { return s.Stats(), nil }

func (s *stubKnowledgeBase) Update(context.Context) (rag.Stats, error) { return s.Stats(), nil }

func (s *stubKnowledgeBase) Document(string) (rag.DocumentRecord, bool) {
	return rag.DocumentRecord{}, false
}

func TestQueryReturnsResponseWithActions(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-a", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 92},
		{Name: "node_gpu_utilization_sm_avg_percent", Value: 95},
		{Name: "node_gpu_memory_used_total_mib", Value: 31000},
	}, now)
	store.StoreProcesses("collector-a", []*telemetryv1.ProcessSample{
		{Pid: 1001, Name: "trainer", CpuPercent: 97},
	}, now)

	gpuStore := gpuobs.New(gpuobs.DefaultConfig())
	gpuStore.ProcessBatch("collector-a", &telemetryv1.TelemetryBatch{
		Collector: &telemetryv1.CollectorInfo{
			CollectorId: "collector-a",
			Hostname:    "node-a",
		},
		Metrics: []*telemetryv1.Metric{
			{Name: "node_gpu_count", Value: 1},
			{Name: "node_gpu_utilization_sm_percent", Value: 96, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_memory_total_mib", Value: 32768, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
			{Name: "node_gpu_memory_used_mib", Value: 31800, Labels: []*telemetryv1.Label{{Key: "gpu_id", Value: "0"}}},
		},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	service, err := NewQueryService(cfg, store, gpuStore, zap.NewNop())
	require.NoError(t, err)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "RCA for high GPU on fleet",
		Node:  "collector-a",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.QueryID)
	require.Equal(t, "collector-a", resp.Node)
	require.NotEmpty(t, resp.Summary)
	require.NotEmpty(t, resp.TelemetryContext.Evidence.GPU)
	require.NotEmpty(t, resp.Explainability.TopSignals)
	require.NotEmpty(t, resp.Explainability.Evidence)
	require.NotZero(t, resp.ActionsExpireAt)
	for i, action := range resp.Actions {
		require.Equal(t, resp.QueryID+"-a"+strconv.Itoa(i+1), action.ID)
	}
}

func TestQueryExposesRAGIntentAndRetrievedDocs(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-rag", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 88},
		{Name: "node_network_tcp_retransmit_ratio", Value: 0.11},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	kb := &stubKnowledgeBase{
		result: rag.QueryResult{
			Query:                "how to fix deployment timeout after rollout",
			Intent:               "runbook",
			RetrievalMode:        "hybrid",
			Summary:              "retrieved 1 knowledge hits across 1 documents (runbook=1)",
			Confidence:           0.83,
			RetrievalEvidenceIDs: []string{"rag-1"},
			Hits: []rag.SearchHit{
				{
					DocID:            "doc-1",
					ChunkID:          "chunk-1",
					Score:            0.92,
					SourcePath:       "cases/timeout-runbook.md",
					SourceType:       "markdown",
					KnowledgeType:    "runbook",
					CaseType:         "runbook",
					Title:            "Timeout Runbook",
					Summary:          "Check retry rates and deployment timing.",
					Snippet:          "Inspect retries and cache credentials after rollout.",
					LikelyCauses:     []string{"stale cache credential after rollout"},
					RemediationSteps: []string{"inspect retry rate", "validate cache credentials"},
					Signals:          []string{"deployment", "network"},
				},
			},
		},
	}
	service.SetRetriever(kb)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "how to fix deployment timeout after rollout",
		Node:  "collector-rag",
	})
	require.NoError(t, err)
	require.Equal(t, "runbook", kb.lastReq.Intent)
	require.Contains(t, kb.lastReq.KnowledgeTypes, "runbook")
	require.Contains(t, kb.lastReq.CaseTypes, "runbook")
	require.Equal(t, "runbook", resp.RetrievalIntent)
	require.Equal(t, "hybrid", resp.RetrievalMode)
	require.Contains(t, resp.RetrievalSummary, "runbook=1")
	require.NotEmpty(t, resp.RetrievedDocs)
	require.Equal(t, "runbook", resp.RetrievedDocs[0].KnowledgeType)
	require.Contains(t, resp.TelemetryContext.RAGContext[0], "summary=Check retry rates and deployment timing.")
}

func TestQuerySuppressesLowConfidenceRAGHits(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-rag-low-confidence", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 88},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.RAGMinConfidence = 0.5

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	service.SetRetriever(&stubKnowledgeBase{
		result: rag.QueryResult{
			Query:         "why is rollout slow",
			Intent:        "runbook",
			RetrievalMode: "hybrid",
			Summary:       "retrieved 1 knowledge hits across 1 documents (runbook=1)",
			Confidence:    0.21,
			Hits: []rag.SearchHit{{
				DocID:         "doc-1",
				ChunkID:       "chunk-1",
				SourcePath:    "cases/rollout-runbook.md",
				SourceType:    "markdown",
				KnowledgeType: "runbook",
				CaseType:      "runbook",
				Title:         "Rollout Runbook",
				Snippet:       "Check retries after rollout.",
			}},
		},
	})

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "why is rollout slow",
		Node:  "collector-rag-low-confidence",
	})
	require.NoError(t, err)
	require.Empty(t, resp.RetrievedDocs)
	require.Empty(t, resp.TelemetryContext.RAGContext)
	require.Contains(t, resp.RetrievalSummary, "retrieval suppressed because confidence")
	require.Equal(t, uint64(1), service.Metrics().RAGLowConfidenceTotal)
}

func TestBuildQueryServiceRAGRequestCompactsFindingsAndChars(t *testing.T) {
	req := buildQueryServiceRAGRequest(
		"why is deployment timeout still happening after rollout",
		[]string{
			"CPU utilization is above 85%",
			"CPU utilization is above 85%",
			"Memory utilization is above 85%",
			"Disk I/O pressure is elevated",
			"Network retransmits or timeout bursts are active",
			"Collector replay backlog is still draining",
			"Extra low-priority finding that should be trimmed away",
		},
		nil,
		4,
		3,
		120,
	)
	require.Equal(t, 4, req.TopK)
	require.LessOrEqual(t, len(req.Query), 120)
	require.Contains(t, req.Query, "CPU utilization is above 85%")
	require.Contains(t, req.Query, "Memory utilization is above 85%")
	require.NotContains(t, req.Query, "Disk I/O pressure is elevated")
	require.NotContains(t, req.Query, "Extra low-priority finding")
	require.NotContains(t, req.Query, "Collector replay backlog is still draining")
}

func TestQuerySkipsRAGWhenSymptomContextIsTooWeak(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-rag-skip", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 42},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)
	kb := &stubKnowledgeBase{
		result: rag.QueryResult{
			Query: "generic rca",
			Hits:  []rag.SearchHit{{Title: "should not be used"}},
		},
	}
	service.SetRetriever(kb)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "what is happening here",
		Node:  "collector-rag-skip",
	})
	require.NoError(t, err)
	require.Zero(t, kb.queries.Load())
	require.Empty(t, resp.RetrievedDocs)
	require.Equal(t, uint64(1), service.Metrics().RAGSkippedContextTotal)
}

func TestQueryUsesTrendAnomaliesToDriveRAGWhenFindingsAreThin(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	for i := 0; i < 4; i++ {
		store.StoreMetrics("collector-rag-anomaly", []*telemetryv1.Metric{
			{Name: "node_memory_Used_bytes", Value: float64(40+i*10) * 1024 * 1024 * 1024},
			{Name: "node_memory_MemTotal_bytes", Value: 128 * 1024 * 1024 * 1024},
			{Name: "node_cpu_usage_percent", Value: 30},
		}, now.Add(time.Duration(i)*5*time.Minute))
	}

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)
	kb := &stubKnowledgeBase{
		result: rag.QueryResult{
			Query:         "memory growth",
			Intent:        "historical_incident",
			RetrievalMode: "hybrid",
			Summary:       "retrieved 1 knowledge hits across 1 documents (historical_incident=1)",
			Confidence:    0.77,
			Hits: []rag.SearchHit{{
				Title:         "Memory Leak Case",
				KnowledgeType: "historical_incident",
				CaseType:      "historical_incident",
				Snippet:       "Sustained memory growth with retry activity pointed to leak amplification.",
			}},
		},
	}
	service.SetRetriever(kb)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "similar incidents for this memory growth pattern",
		Node:  "collector-rag-anomaly",
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), kb.queries.Load())
	require.Contains(t, kb.lastReq.Query, "Memory usage is climbing steadily")
	require.NotEmpty(t, resp.RetrievedDocs)
}

func TestQueryRateLimited(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.RateLimitRPS = 0.1
	cfg.RateBurst = 1

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)

	_, err = service.Query(context.Background(), QueryRequest{Query: "first"})
	require.NoError(t, err)

	_, err = service.Query(context.Background(), QueryRequest{Query: "second"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrRateLimited))
}

func TestExecuteMissingActionReturnsNotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)

	_, err = service.Execute(context.Background(), ExecuteRequest{ActionID: "missing"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrActionNotFound))
}

func TestExecuteRequiresApprovalTokenWhenNotDryRun(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.DryRun = false
	cfg.RequireApprovalToken = true

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)

	actions, _ := service.cachePending("q-1", []ActionSpec{{
		ID:      "a-1",
		Type:    "shell",
		Command: "echo hi",
		Safe:    true,
	}})
	require.Len(t, actions, 1)
	require.True(t, actions[0].ApprovalRequired)
	require.NotEmpty(t, actions[0].ApprovalToken)

	_, err = service.Execute(context.Background(), ExecuteRequest{ActionID: "a-1"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrApprovalRequired))

	_, err = service.Execute(context.Background(), ExecuteRequest{
		ActionID:      "a-1",
		ApprovalToken: "invalid-token",
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrApprovalInvalid))

	resp, err := service.Execute(context.Background(), ExecuteRequest{
		ActionID:      "a-1",
		ApprovalToken: actions[0].ApprovalToken,
	})
	require.NoError(t, err)
	require.Equal(t, ActionResultExecuted, resp.Result.Status)
}

func TestExecuteReturnsExpiredForExpiredAction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.DryRun = false
	cfg.RequireApprovalToken = true
	cfg.PendingActionTTL = 1 * time.Millisecond

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)

	actions, _ := service.cachePending("q-2", []ActionSpec{{
		ID:      "a-expire",
		Type:    "shell",
		Command: "echo hi",
		Safe:    true,
	}})
	require.Len(t, actions, 1)

	time.Sleep(10 * time.Millisecond)
	_, err = service.Execute(context.Background(), ExecuteRequest{
		ActionID:      "a-expire",
		ApprovalToken: actions[0].ApprovalToken,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrActionExpired))
}

func TestMergeActionsUsesQueryScopedIDs(t *testing.T) {
	actions := mergeActions(
		[]ActionSpec{{ID: "external-a", Type: "shell", Command: "echo one", Safe: true}},
		[]ActionSpec{{ID: "external-b", Type: "shell", Command: "echo two", Safe: true}},
		"q-123",
	)

	require.Len(t, actions, 2)
	require.Equal(t, "q-123-a1", actions[0].ID)
	require.Equal(t, "q-123-a2", actions[1].ID)
}

func TestQuerySuppressesActionsWhenTelemetryStale(t *testing.T) {
	store := ingest.NewMemoryStore()
	old := time.Now().UTC().Add(-10 * time.Minute)
	store.StoreMetrics("collector-stale", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 91},
	}, old)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.MaxTelemetryAge = 1 * time.Minute
	cfg.AllowActionsOnStaleData = false

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "RCA on stale telemetry",
		Node:  "collector-stale",
	})
	require.NoError(t, err)
	require.Empty(t, resp.Actions)
	require.True(t, resp.ActionsExpireAt.IsZero())
	require.True(t, resp.ActionsSuppressed)
	require.Contains(t, resp.SuppressionReason, "stale")
	require.True(t, resp.Explainability.DataCoverage.TelemetryStale)
	require.NotZero(t, resp.Explainability.DataCoverage.TelemetryAgeSeconds)
	require.Contains(t, strings.Join(resp.Recommendations, " | "), "Telemetry is stale")
	require.Equal(t, uint64(1), service.Metrics().StaleTelemetryTotal)
	require.Equal(t, uint64(1), service.Metrics().ActionsSuppressedTotal)
}

func TestQueryAllowsActionsWhenConfiguredOnStaleTelemetry(t *testing.T) {
	store := ingest.NewMemoryStore()
	old := time.Now().UTC().Add(-10 * time.Minute)
	store.StoreMetrics("collector-stale-ok", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 91},
	}, old)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.MaxTelemetryAge = 1 * time.Minute
	cfg.AllowActionsOnStaleData = true
	cfg.SkipLLMOnStaleTelemetry = false

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "RCA on stale telemetry with action override",
		Node:  "collector-stale-ok",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Actions)
	require.False(t, resp.ActionsExpireAt.IsZero())
	require.False(t, resp.ActionsSuppressed)
	require.True(t, resp.Explainability.DataCoverage.TelemetryStale)
}

type blockingClient struct {
	started chan struct{}
	release chan struct{}
}

func (c blockingClient) Provider() string { return "test" }
func (c blockingClient) Model() string    { return "blocking" }

func (c blockingClient) Complete(ctx context.Context, _, _ string) (string, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return `{"summary":"blocked complete","root_cause":"test","confidence":0.6,"findings":["f"],"recommendations":["r"],"actions":[]}`, nil
}

func TestQueryReturnsBusyWhenConcurrencySaturated(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.MaxConcurrentQueries = 1
	cfg.RateLimitRPS = 100
	cfg.RateBurst = 100
	cfg.SkipLLMOnNoTelemetry = false

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	service.client = blockingClient{
		started: started,
		release: release,
	}

	errCh := make(chan error, 1)
	go func() {
		_, qErr := service.Query(context.Background(), QueryRequest{Query: "first"})
		errCh <- qErr
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first query to start")
	}

	_, err = service.Query(context.Background(), QueryRequest{Query: "second"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrBusy))

	close(release)
	require.NoError(t, <-errCh)
	require.Equal(t, uint64(1), service.Metrics().BusyRejectedTotal)
}

type countingClient struct {
	calls atomic.Int32
}

func (c *countingClient) Provider() string { return "test" }
func (c *countingClient) Model() string    { return "counting" }

func (c *countingClient) Complete(_ context.Context, _, _ string) (string, error) {
	c.calls.Add(1)
	return `{"summary":"counting","root_cause":"ok","confidence":0.7,"findings":["f"],"recommendations":["r"],"actions":[]}`, nil
}

type errorClient struct{}

func (errorClient) Provider() string { return "test" }
func (errorClient) Model() string    { return "error" }

func (errorClient) Complete(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("llm failed")
}

func TestQuerySkipsLLMWhenStaleTelemetryConfigured(t *testing.T) {
	store := ingest.NewMemoryStore()
	old := time.Now().UTC().Add(-10 * time.Minute)
	store.StoreMetrics("collector-skip-stale", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 90},
	}, old)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.MaxTelemetryAge = 1 * time.Minute
	cfg.SkipLLMOnStaleTelemetry = true
	cfg.AllowActionsOnStaleData = false

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)
	cc := &countingClient{}
	service.client = cc

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "stale llm bypass",
		Node:  "collector-skip-stale",
	})
	require.NoError(t, err)
	require.True(t, resp.UsedFallback)
	require.Contains(t, resp.FallbackReason, ErrStaleTelemetry.Error())
	require.Zero(t, cc.calls.Load())
	require.Equal(t, uint64(1), service.Metrics().LLMBypassedStaleTotal)
}

func TestQueryReusesRecentAnalysisForUnchangedEvidence(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-reuse", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 91},
		{Name: "node_memory_usage_percent", Value: 86},
		{Name: "node_network_tcp_retransmit_ratio", Value: 0.14},
	}, now)
	store.StoreProcesses("collector-reuse", []*telemetryv1.ProcessSample{
		{Pid: 2001, Name: "trainer", CpuPercent: 88, RssBytes: 9 * 1024 * 1024 * 1024},
	}, now)
	store.StoreLogs("collector-reuse", []*telemetryv1.LogFingerprint{
		{Fingerprint: "timeout burst", Example: "trainer timeout while reading shard", Count: 4},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.AnalysisReuseEnabled = true
	cfg.AnalysisReuseWindow = 1 * time.Minute
	cfg.AnalysisReuseMaxKeys = 32

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	cc := &countingClient{}
	service.client = cc
	kb := &stubKnowledgeBase{
		result: rag.QueryResult{
			Query:         "timeout burst after deployment",
			Intent:        "runbook",
			RetrievalMode: "hybrid",
			Summary:       "retrieved 1 knowledge hits across 1 documents (runbook=1)",
			Confidence:    0.82,
			Hits: []rag.SearchHit{{
				DocID:         "doc-1",
				ChunkID:       "chunk-1",
				SourcePath:    "cases/timeout-runbook.md",
				SourceType:    "markdown",
				KnowledgeType: "runbook",
				CaseType:      "runbook",
				Title:         "Timeout Runbook",
				Snippet:       "Inspect retries and cache credentials after rollout.",
			}},
		},
	}
	service.SetRetriever(kb)

	resp1, err := service.Query(context.Background(), QueryRequest{
		Query: "why did this node start timing out after rollout",
		Node:  "collector-reuse",
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), cc.calls.Load())
	require.Equal(t, int32(1), kb.queries.Load())
	require.NotEmpty(t, resp1.RetrievedDocs)

	resp2, err := service.Query(context.Background(), QueryRequest{
		Query: "why did this node start timing out after rollout",
		Node:  "collector-reuse",
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), cc.calls.Load())
	require.Equal(t, int32(1), kb.queries.Load())
	require.Equal(t, uint64(1), service.Metrics().AnalysisReusedTotal)
	require.Equal(t, resp1.Summary, resp2.Summary)
	require.NotEmpty(t, resp2.RetrievedDocs)
	require.Equal(t, "hybrid", resp2.RetrievalMode)
}

func TestQueryDoesNotReuseAnalysisWhenPromptEvidenceChanges(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-reuse-miss", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 72},
		{Name: "node_memory_usage_percent", Value: 68},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.AnalysisReuseEnabled = true
	cfg.AnalysisReuseWindow = 1 * time.Minute

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	cc := &countingClient{}
	service.client = cc
	kb := &stubKnowledgeBase{
		result: rag.QueryResult{
			Query:         "cpu pressure",
			Intent:        "runbook",
			RetrievalMode: "hybrid",
			Summary:       "retrieved 1 knowledge hits across 1 documents (runbook=1)",
			Confidence:    0.79,
			Hits: []rag.SearchHit{{
				DocID:         "doc-cpu",
				ChunkID:       "chunk-cpu",
				SourcePath:    "cases/cpu-runbook.md",
				SourceType:    "markdown",
				KnowledgeType: "runbook",
				CaseType:      "runbook",
				Title:         "CPU Runbook",
				Snippet:       "Inspect top CPU consumers.",
			}},
		},
	}
	service.SetRetriever(kb)

	_, err = service.Query(context.Background(), QueryRequest{
		Query: "why is cpu pressure rising",
		Node:  "collector-reuse-miss",
	})
	require.NoError(t, err)

	store.StoreMetrics("collector-reuse-miss", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 94},
		{Name: "node_memory_usage_percent", Value: 69},
	}, now.Add(5*time.Second))

	_, err = service.Query(context.Background(), QueryRequest{
		Query: "why is cpu pressure rising",
		Node:  "collector-reuse-miss",
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), cc.calls.Load())
	require.Equal(t, int32(2), kb.queries.Load())
	require.Equal(t, uint64(0), service.Metrics().AnalysisReusedTotal)
}

func TestQuerySkipsRAGWhenStaleTelemetryBypassesLLM(t *testing.T) {
	store := ingest.NewMemoryStore()
	old := time.Now().UTC().Add(-10 * time.Minute)
	store.StoreMetrics("collector-stale-rag", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 90},
	}, old)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.MaxTelemetryAge = 1 * time.Minute
	cfg.SkipLLMOnStaleTelemetry = true

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)
	stub := &stubKnowledgeBase{result: rag.QueryResult{Hits: []rag.SearchHit{{Title: "should not be used"}}}}
	service.SetRetriever(stub)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "stale telemetry should skip rag too",
		Node:  "collector-stale-rag",
	})
	require.NoError(t, err)
	require.True(t, resp.UsedFallback)
	require.Zero(t, stub.queries.Load())
	require.Empty(t, resp.RetrievedDocs)
	require.Empty(t, resp.RetrievalSummary)
}

func TestQueryDoesNotCacheFallbackAnalysis(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-fallback-reuse", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 89},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.051},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.SkipLLMOnNoTelemetry = false
	cfg.SkipLLMOnStaleTelemetry = false
	cfg.AnalysisReuseEnabled = true
	cfg.AnalysisReuseWindow = 1 * time.Minute

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)
	service.client = errorClient{}

	kb := &stubKnowledgeBase{
		result: rag.QueryResult{
			Query:         "disk latency investigation",
			Intent:        "runbook",
			RetrievalMode: "hybrid",
			Summary:       "retrieved 1 knowledge hits across 1 documents (runbook=1)",
			Confidence:    0.72,
			Hits: []rag.SearchHit{{
				DocID:         "doc-disk",
				ChunkID:       "chunk-disk",
				SourcePath:    "cases/disk-runbook.md",
				SourceType:    "markdown",
				KnowledgeType: "runbook",
				CaseType:      "runbook",
				Title:         "Disk Runbook",
				Snippet:       "Inspect device latency and queue depth.",
			}},
		},
	}
	service.SetRetriever(kb)

	resp1, err := service.Query(context.Background(), QueryRequest{
		Query: "why is disk latency growing",
		Node:  "collector-fallback-reuse",
	})
	require.NoError(t, err)
	require.True(t, resp1.UsedFallback)

	resp2, err := service.Query(context.Background(), QueryRequest{
		Query: "why is disk latency growing",
		Node:  "collector-fallback-reuse",
	})
	require.NoError(t, err)
	require.True(t, resp2.UsedFallback)
	require.Equal(t, int32(2), kb.queries.Load())
	require.Equal(t, uint64(0), service.Metrics().AnalysisReusedTotal)
}

func TestQueryFallbackCarriesOperationalReasoning(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-operational", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 87},
		{Name: "node_cpu_iowait_percent", Value: 14},
		{Name: "node_disk_request_latency_p99_seconds", Value: 0.065},
		{Name: "node_disk_queue_depth_total", Value: 12},
		{Name: "node_memory_MemTotal_bytes", Value: 1000},
		{Name: "node_memory_Used_bytes", Value: 860},
	}, now)
	store.StoreLogs("collector-operational", []*telemetryv1.LogFingerprint{
		{Fingerprint: "timeout burst", Count: 8, Example: "trainer timeout while reading shard"},
	}, now)

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.SkipLLMOnNoTelemetry = false
	cfg.SkipLLMOnStaleTelemetry = false

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)
	service.client = errorClient{}

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "why is this training node degrading",
		Node:  "collector-operational",
	})
	require.NoError(t, err)
	require.True(t, resp.UsedFallback)
	require.Contains(t, resp.FallbackReason, "llm failed")
	require.Contains(t, strings.Join(resp.Findings, " | "), "storage bottleneck")
	require.Contains(t, strings.Join(resp.Recommendations, " | "), "Inspect the hottest disk/device")
	require.Contains(t, strings.Join(resp.Explainability.Evidence, " | "), "storage bottleneck")
	require.Equal(t, "degraded", resp.Explainability.DataCoverage.TelemetryState)
	require.Greater(t, resp.Explainability.DataCoverage.TelemetryCoveragePct, 0.0)
	require.False(t, containsString(resp.Explainability.DataCoverage.BlindSpots, ""))
}

func TestQuerySkipsLLMWhenTelemetryInsufficientConfigured(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.SkipLLMOnNoTelemetry = true

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)
	cc := &countingClient{}
	service.client = cc

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "no telemetry bypass",
	})
	require.NoError(t, err)
	require.True(t, resp.UsedFallback)
	require.Contains(t, resp.FallbackReason, ErrNoTelemetry.Error())
	require.Zero(t, cc.calls.Load())
	require.Equal(t, uint64(1), service.Metrics().LLMBypassedEmptyTotal)
}

func TestQuerySkipsRAGWhenTelemetryIsMissingAndLLMBypassed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.SkipLLMOnNoTelemetry = true

	service, err := NewQueryService(cfg, ingest.NewMemoryStore(), nil, zap.NewNop())
	require.NoError(t, err)
	stub := &stubKnowledgeBase{result: rag.QueryResult{Hits: []rag.SearchHit{{Title: "should not be used"}}}}
	service.SetRetriever(stub)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "no telemetry should skip rag",
	})
	require.NoError(t, err)
	require.True(t, resp.UsedFallback)
	require.Zero(t, stub.queries.Load())
	require.Empty(t, resp.RetrievedDocs)
}

type webhookEnvelope struct {
	Type string `json:"type"`
}

type webhookCall struct {
	AuthHeader string
	Type       string
}

type slackCall struct {
	Text string
}

type pagerDutyCall struct {
	RoutingKey string
	EventType  string
	Summary    string
	Severity   string
	DedupKey   string
}

func TestQueryAndExecutePublishWebhookEvents(t *testing.T) {
	requireLocalTCPBind(t)

	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-webhook", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 94},
	}, now)

	calls := make(chan webhookCall, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var env webhookEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		calls <- webhookCall{
			AuthHeader: r.Header.Get("Authorization"),
			Type:       env.Type,
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.DryRun = true
	cfg.EventWebhookURL = server.URL
	cfg.EventWebhookToken = "test-event-token"
	cfg.EventWebhookTimeout = 1 * time.Second

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "Analyze node pressure and propose action",
		Node:  "collector-webhook",
	})
	require.NoError(t, err)

	cached, _ := service.cachePending(resp.QueryID, []ActionSpec{{
		ID:      "manual-action",
		Type:    "shell",
		Command: "echo ok",
		Safe:    true,
	}})
	require.Len(t, cached, 1)

	_, err = service.Execute(context.Background(), ExecuteRequest{
		ActionID: cached[0].ID,
	})
	require.NoError(t, err)

	receivedTypes := map[string]bool{}
	receivedAuth := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(receivedTypes) < 2 {
		select {
		case call := <-calls:
			receivedTypes[call.Type] = true
			receivedAuth[call.AuthHeader] = true
		case <-deadline:
			t.Fatalf("timed out waiting for webhook events, got types=%v", receivedTypes)
		}
	}

	require.True(t, receivedTypes["agent.query.completed"])
	require.True(t, receivedTypes["agent.execute.completed"])
	require.True(t, receivedAuth["Bearer test-event-token"])
	require.Eventually(t, func() bool {
		stats := service.Metrics()
		return stats.EventsPublishedTotal >= 2 && stats.EventsPublishFailTotal == 0
	}, 2*time.Second, 20*time.Millisecond)
}

func TestQueryWebhookPublishFailureIncrementsCounter(t *testing.T) {
	requireLocalTCPBind(t)

	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-webhook-fail", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 76},
	}, now)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.EventWebhookURL = server.URL
	cfg.EventWebhookTimeout = 1 * time.Second

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	_, err = service.Query(context.Background(), QueryRequest{
		Query: "Analyze node",
		Node:  "collector-webhook-fail",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		stats := service.Metrics()
		return stats.EventsPublishFailTotal >= 1
	}, 2*time.Second, 20*time.Millisecond)
}

func TestQueryAndExecutePublishSlackAndPagerDutyEvents(t *testing.T) {
	requireLocalTCPBind(t)

	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-native-sinks", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 88},
	}, now)

	slackCalls := make(chan slackCall, 8)
	slackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		slackCalls <- slackCall{Text: stringFromAny(payload["text"])}
		w.WriteHeader(http.StatusOK)
	}))
	defer slackServer.Close()

	pagerCalls := make(chan pagerDutyCall, 8)
	pagerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload struct {
			RoutingKey string `json:"routing_key"`
			EventType  string `json:"event_action"`
			DedupKey   string `json:"dedup_key"`
			Payload    struct {
				Summary  string `json:"summary"`
				Severity string `json:"severity"`
			} `json:"payload"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		pagerCalls <- pagerDutyCall{
			RoutingKey: payload.RoutingKey,
			EventType:  payload.EventType,
			Summary:    payload.Payload.Summary,
			Severity:   payload.Payload.Severity,
			DedupKey:   payload.DedupKey,
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer pagerServer.Close()

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.DryRun = true
	cfg.EventSlackWebhookURL = slackServer.URL
	cfg.EventPagerDutyRoutingKey = "pagerduty-test-key"
	cfg.EventPagerDutyEventsURL = pagerServer.URL
	cfg.EventWebhookTimeout = 1 * time.Second

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	resp, err := service.Query(context.Background(), QueryRequest{
		Query: "Evaluate node pressure",
		Node:  "collector-native-sinks",
	})
	require.NoError(t, err)

	cached, _ := service.cachePending(resp.QueryID, []ActionSpec{{
		ID:      "native-sink-action",
		Type:    "shell",
		Command: "echo ok",
		Safe:    true,
	}})
	require.Len(t, cached, 1)

	_, err = service.Execute(context.Background(), ExecuteRequest{
		ActionID: cached[0].ID,
	})
	require.NoError(t, err)

	slackCount := 0
	pagerCount := 0
	deadline := time.After(3 * time.Second)
	for slackCount < 2 || pagerCount < 2 {
		select {
		case call := <-slackCalls:
			slackCount++
			require.Contains(t, call.Text, "AGENT")
		case call := <-pagerCalls:
			pagerCount++
			require.Equal(t, "pagerduty-test-key", call.RoutingKey)
			require.Equal(t, "trigger", call.EventType)
			require.NotEmpty(t, call.Summary)
			require.NotEmpty(t, call.DedupKey)
			require.Contains(t, []string{"info", "warning", "critical"}, call.Severity)
		case <-deadline:
			t.Fatalf("timed out waiting for native sink events (slack=%d pager=%d)", slackCount, pagerCount)
		}
	}

	require.Eventually(t, func() bool {
		stats := service.Metrics()
		return stats.EventsPublishedTotal >= 4 && stats.EventsPublishFailTotal == 0
	}, 2*time.Second, 20*time.Millisecond)
}

func TestQueryWebhookPublishRetriesBeforeSuccess(t *testing.T) {
	requireLocalTCPBind(t)

	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	store.StoreMetrics("collector-retry", []*telemetryv1.Metric{
		{Name: "node_cpu_usage_percent", Value: 64},
	}, now)

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	cfg.Provider = "mock"
	cfg.PlaybookFile = ""
	cfg.EventWebhookURL = server.URL
	cfg.EventWebhookTimeout = 1 * time.Second
	cfg.EventPublishRetries = 1
	cfg.EventRetryBackoff = 5 * time.Millisecond

	service, err := NewQueryService(cfg, store, nil, zap.NewNop())
	require.NoError(t, err)

	_, err = service.Query(context.Background(), QueryRequest{
		Query: "retry publish",
		Node:  "collector-retry",
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return attempts.Load() >= 2
	}, 2*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		stats := service.Metrics()
		return stats.EventsPublishedTotal >= 1 && stats.EventsPublishFailTotal == 0
	}, 2*time.Second, 20*time.Millisecond)
}
