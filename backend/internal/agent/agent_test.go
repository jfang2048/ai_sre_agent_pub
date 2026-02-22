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
