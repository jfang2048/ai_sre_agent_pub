package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	agentcore "github.com/jfang2048/ai_sre_agent_pub/internal/controller/agentcore"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func productionLikeControllerConfig(t *testing.T) Config {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Deployment.Mode = "standalone"
	cfg.API.AllowedOrigins = nil
	cfg.Auth.Enabled = true
	cfg.Auth.TokenSecretEnv = "TEST_PRODUCTION_TOKEN_SECRET"
	t.Setenv("TEST_PRODUCTION_TOKEN_SECRET", "prod-secret")
	cfg.Ingest.Persistence.Enabled = true
	cfg.Ingest.Persistence.Path = filepath.Join(t.TempDir(), "ingest", "store.db")
	cfg.Agent.PersistDir = filepath.Join(t.TempDir(), "agent")
	return cfg
}

func closeControllerForTest(t *testing.T, ctrl *Controller) {
	t.Helper()
	if ctrl == nil {
		return
	}
	if ctrl.timeseriesService != nil {
		_ = ctrl.timeseriesService.Close()
	}
	if ctrl.ingestStore != nil {
		_ = ctrl.ingestStore.Close()
	}
	if ctrl.agentWorkflow != nil {
		_ = ctrl.agentWorkflow.Close()
	}
}

func TestControllerRejectsDisabledAuthOutsideLocalDev(t *testing.T) {
	cfg := productionLikeControllerConfig(t)
	cfg.Auth.Enabled = false

	ctrl, err := New(cfg, zap.NewNop())
	require.Nil(t, ctrl)
	require.Error(t, err)
	require.Contains(t, err.Error(), "controller auth is disabled")
	require.Contains(t, err.Error(), "deployment mode")
}

func TestControllerRejectsProductionLikeMemoryOnlyIngest(t *testing.T) {
	cfg := productionLikeControllerConfig(t)
	cfg.Ingest.Persistence.Enabled = false

	ctrl, err := New(cfg, zap.NewNop())
	require.Nil(t, ctrl)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ingest persistence is disabled")
}

func TestControllerRejectsWorkflowDurabilityFallbackOutsideLocalDev(t *testing.T) {
	cfg := productionLikeControllerConfig(t)
	t.Setenv("SRE_AGENT_WORKFLOW_STORE_PATH", t.TempDir())

	ctrl, err := New(cfg, zap.NewNop())
	require.Nil(t, ctrl)
	require.Error(t, err)
	require.Contains(t, err.Error(), "workflow durable store")
	require.Contains(t, err.Error(), "fell back to in-memory")
}

func TestControllerRejectsClusterLiteLocalWorkflowStoreWithoutSharedBackend(t *testing.T) {
	cfg := productionLikeControllerConfig(t)
	cfg.Deployment.Mode = "cluster-lite"

	ctrl, err := New(cfg, zap.NewNop())
	require.Nil(t, ctrl)
	require.Error(t, err)
	require.Contains(t, err.Error(), "workflow durable state is controller-local")
}

func TestControllerRejectsDisabledIngestAuthOutsideLocalDev(t *testing.T) {
	cfg := productionLikeControllerConfig(t)
	cfg.Auth.IngestAuthEnabled = false

	ctrl, err := New(cfg, zap.NewNop())
	require.Nil(t, ctrl)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gRPC ingest authentication is disabled")
}

func TestControllerAllowsExplicitInsecureOverrideAndReportsDegradedPosture(t *testing.T) {
	cfg := productionLikeControllerConfig(t)
	cfg.Ingest.Persistence.Enabled = false
	cfg.Deployment.Mode = "cluster-lite"
	cfg.Deployment.InsecureOverride = true

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { closeControllerForTest(t, ctrl) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	ctrl.handleStatus(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var payload map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&payload))

	deploymentBlock, ok := payload["deployment"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "cluster-lite", deploymentBlock["mode"])
	require.Equal(t, true, deploymentBlock["production_like"])
	require.Equal(t, true, deploymentBlock["insecure_override"])
	require.Equal(t, true, deploymentBlock["degraded"])

	var reasons []string
	for _, raw := range deploymentBlock["degraded_reasons"].([]any) {
		reasons = append(reasons, raw.(string))
	}
	require.NotEmpty(t, reasons)
	require.Contains(t, strings.Join(reasons, " | "), "ingest persistence is disabled")

	apiBlock, ok := payload["api"].(map[string]any)
	require.True(t, ok)
	corsBlock, ok := apiBlock["cors"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "same_origin_only", corsBlock["mode"])

	authBlock, ok := payload["auth"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, authBlock["enabled"])
	require.Equal(t, false, authBlock["insecure_override"])

	durabilityBlock, ok := payload["durability"].(map[string]any)
	require.True(t, ok)
	ingestBlock, ok := durabilityBlock["ingest"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, ingestBlock["persistence_configured"])
	require.Equal(t, "memory_only", ingestBlock["backend"])

	workflowBlock, ok := durabilityBlock["workflow"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "embedded_bbolt", workflowBlock["backend"])
	require.Equal(t, true, workflowBlock["persistent"])
	require.Equal(t, false, workflowBlock["shared"])
	require.Equal(t, "local", workflowBlock["mode"])
	require.Equal(t, "embedded_bbolt", workflowBlock["approval_state_backend"])
	require.Equal(t, "embedded_bbolt", workflowBlock["idempotency_backend"])

	artifactBlock, ok := durabilityBlock["artifacts"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "bbolt", artifactBlock["metadata_backend"])
	require.Equal(t, "filesystem", artifactBlock["payload_backend"])
	require.Equal(t, false, artifactBlock["metadata_shared"])
	require.Equal(t, false, artifactBlock["payload_shared"])
	require.Equal(t, false, artifactBlock["payload_shared_survivable"])
	require.Equal(t, true, artifactBlock["gc_enabled"])
	require.Equal(t, "stable_keys", artifactBlock["addressing_mode"])

	hotStateBlock, ok := durabilityBlock["hot_state"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, hotStateBlock["ingest_in_process"])
	require.Equal(t, false, hotStateBlock["shared_across_failover"])
}

func TestBuildControllerDeploymentPostureFlagsSharedWorkflowWithLocalArtifactPayload(t *testing.T) {
	cfg := productionLikeControllerConfig(t)
	cfg.Deployment.Mode = "cluster-lite"

	posture := buildControllerDeploymentPosture(
		cfg,
		ResolvedAuthConfig{Enabled: true, IngestAuthEnabled: true},
		"cluster-lite",
		"same_origin_only",
		agentcore.WorkflowDurabilityStatus{
			Enabled:              true,
			Backend:              "postgres",
			ConfiguredBackend:    "postgres",
			Persistent:           true,
			LocalFirst:           false,
			Shared:               true,
			Mode:                 "shared",
			ApprovalStateBackend: "postgres",
			IdempotencyBackend:   "postgres",
		},
		agentcore.ArtifactDurabilityStatus{
			Enabled:                       true,
			MetadataBackend:               "postgres",
			MetadataPersistent:            true,
			MetadataShared:                true,
			PayloadBackend:                "filesystem",
			PayloadRootPath:               "/controller/workflows",
			PayloadShared:                 false,
			PayloadSharedSurvivable:       false,
			AddressingMode:                "stable_keys",
			LocalCacheActive:              true,
			MessageHistoryMetadataBackend: "postgres",
			EvidenceMetadataBackend:       "postgres",
			IncidentMemoryMetadataBackend: "postgres",
			RAGMetadataBackend:            "local_runtime_only",
			RAGIndexBackend:               "local_file",
		},
		controllerIngestDurabilityStatus{PersistenceConfigured: true, PersistenceEnabled: true, Backend: "embedded_bbolt", LocalFirst: true},
		controllerIngestTransportStatus{TLSEnabled: true},
	)
	require.True(t, posture.Degraded)
	require.Contains(t, strings.Join(posture.Reasons, " | "), "artifact payload backend is not shared-survivable")
}

func TestBuildControllerDeploymentPostureAcceptsSharedArtifactPayloadBackend(t *testing.T) {
	cfg := productionLikeControllerConfig(t)
	cfg.Deployment.Mode = "cluster-lite"

	posture := buildControllerDeploymentPosture(
		cfg,
		ResolvedAuthConfig{Enabled: true, IngestAuthEnabled: true},
		"cluster-lite",
		"same_origin_only",
		agentcore.WorkflowDurabilityStatus{
			Enabled:              true,
			Backend:              "postgres",
			ConfiguredBackend:    "postgres",
			Persistent:           true,
			LocalFirst:           false,
			Shared:               true,
			Mode:                 "shared",
			ApprovalStateBackend: "postgres",
			IdempotencyBackend:   "postgres",
		},
		agentcore.ArtifactDurabilityStatus{
			Enabled:                       true,
			MetadataBackend:               "postgres",
			MetadataPersistent:            true,
			MetadataShared:                true,
			PayloadBackend:                "s3",
			PayloadShared:                 true,
			PayloadSharedSurvivable:       true,
			PayloadContainer:              "artifacts-prod",
			PayloadPrefix:                 "controller-a",
			AddressingMode:                "stable_keys",
			LocalCacheActive:              false,
			MessageHistoryMetadataBackend: "postgres",
			EvidenceMetadataBackend:       "postgres",
			IncidentMemoryMetadataBackend: "postgres",
			RAGMetadataBackend:            "local_runtime_only",
			RAGIndexBackend:               "local_file",
		},
		controllerIngestDurabilityStatus{PersistenceConfigured: true, PersistenceEnabled: true, Backend: "embedded_bbolt", LocalFirst: true},
		controllerIngestTransportStatus{TLSEnabled: true},
	)
	require.False(t, posture.Degraded)
	require.Empty(t, posture.Reasons)
}

func TestControllerCORSProductionLikeSameOriginOnly(t *testing.T) {
	cfg := productionLikeControllerConfig(t)

	ctrl, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { closeControllerForTest(t, ctrl) })

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Host = "controller.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://controller.example.com")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "https://controller.example.com", w.Header().Get("Access-Control-Allow-Origin"))

	req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Host = "controller.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://other.example.com")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}
