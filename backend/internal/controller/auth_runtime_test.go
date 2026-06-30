package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/identity"
	"github.com/jfang2048/ai_sre_agent_pub/internal/pkg/middleware"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func mintControllerTestToken(t *testing.T, secret string, roles []identity.Role, audience string) string {
	t.Helper()
	token, err := identity.MintToken(
		[]byte(secret),
		"test-subject",
		identity.ActorTypeUser,
		roles,
		"ai-sre-agent-controller",
		audience,
		"",
		time.Hour,
		time.Now().UTC(),
	)
	require.NoError(t, err)
	return token
}

func TestResolveAuthConfigModes(t *testing.T) {
	logger := zap.NewNop()

	t.Run("token mode requires signing secret", func(t *testing.T) {
		t.Setenv("TEST_CONTROLLER_TOKEN_SECRET", "controller-secret")
		resolved, err := ResolveAuthConfig(AuthConfig{
			Enabled:        true,
			Mode:           string(ControllerAuthModeToken),
			TokenSecretEnv: "TEST_CONTROLLER_TOKEN_SECRET",
		}, logger)
		require.NoError(t, err)
		require.Equal(t, ControllerAuthModeToken, resolved.Mode)
		require.Equal(t, ControllerAPIKeyModeDisabled, resolved.APIKeyMode)
		require.Equal(t, "controller-secret", resolved.TokenSecret)
		require.Equal(t, "controller-api", resolved.TokenAudience)
		require.Equal(t, "controller-ingest", resolved.IngestTokenAudience)
	})

	t.Run("mixed mode keeps compatibility api key support explicit", func(t *testing.T) {
		t.Setenv("TEST_MIXED_TOKEN_SECRET", "controller-secret")
		t.Setenv("TEST_MIXED_API_KEY", "shared-secret")
		resolved, err := ResolveAuthConfig(AuthConfig{
			Enabled:        true,
			Mode:           string(ControllerAuthModeMixed),
			TokenSecretEnv: "TEST_MIXED_TOKEN_SECRET",
			APIKeyEnv:      "TEST_MIXED_API_KEY",
		}, logger)
		require.NoError(t, err)
		require.Equal(t, ControllerAuthModeMixed, resolved.Mode)
		require.Equal(t, ControllerAPIKeyModeShared, resolved.APIKeyMode)
		require.Equal(t, "shared-secret", resolved.ReadKey)
		require.Equal(t, "shared-secret", resolved.ActionKey)
	})

	t.Run("api key mode accepts split compatibility keys", func(t *testing.T) {
		t.Setenv("TEST_READ_KEY", "read-secret")
		t.Setenv("TEST_ACTION_KEY", "action-secret")
		resolved, err := ResolveAuthConfig(AuthConfig{
			Enabled:         true,
			Mode:            string(ControllerAuthModeAPIKey),
			ReadAPIKeyEnv:   "TEST_READ_KEY",
			ActionAPIKeyEnv: "TEST_ACTION_KEY",
		}, logger)
		require.NoError(t, err)
		require.Equal(t, ControllerAuthModeAPIKey, resolved.Mode)
		require.Equal(t, ControllerAPIKeyModeSplit, resolved.APIKeyMode)
		require.Equal(t, "read-secret", resolved.ReadKey)
		require.Equal(t, "action-secret", resolved.ActionKey)
	})

	t.Run("missing signing secret fails fast", func(t *testing.T) {
		_, err := ResolveAuthConfig(AuthConfig{
			Enabled:        true,
			Mode:           string(ControllerAuthModeToken),
			TokenSecretEnv: "TEST_MISSING_CONTROLLER_TOKEN_SECRET",
		}, logger)
		require.Error(t, err)
		require.Contains(t, err.Error(), "TEST_MISSING_CONTROLLER_TOKEN_SECRET")
	})
}

func TestControllerRequestScopeClassification(t *testing.T) {
	testCases := []struct {
		name   string
		method string
		path   string
		want   middleware.APIKeyAuthScope
	}{
		{name: "public health", method: http.MethodGet, path: "/healthz", want: middleware.APIKeyAuthScopePublic},
		{name: "read status", method: http.MethodGet, path: "/api/v1/status", want: middleware.APIKeyAuthScopeRead},
		{name: "read agent query", method: http.MethodPost, path: "/api/v1/agent/query", want: middleware.APIKeyAuthScopeRead},
		{name: "read rag query", method: http.MethodPost, path: "/api/v1/rag/query", want: middleware.APIKeyAuthScopeRead},
		{name: "action execute", method: http.MethodPost, path: "/api/v1/controller/actions/execute", want: middleware.APIKeyAuthScopeAction},
		{name: "action config reload", method: http.MethodPost, path: "/api/v1/controller/config/reload", want: middleware.APIKeyAuthScopeAction},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			require.Equal(t, tc.want, controllerRequestScope(req))
		})
	}
}

func TestControllerTokenAuthProtectedRoutesAndStatus(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.Mode = string(ControllerAuthModeToken)
	cfg.Auth.TokenSecretEnv = "TEST_STATUS_TOKEN_SECRET"
	t.Setenv("TEST_STATUS_TOKEN_SECRET", "controller-secret")

	ctrl, err := New(cfg, logger)
	require.NoError(t, err)

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)
	handler := ctrl.wrapHTTPAuthentication(ctrl.wrapHTTPHandler(mux))

	viewerToken := mintControllerTestToken(t, "controller-secret", []identity.Role{identity.RoleViewer}, ctrl.auth.TokenAudience)
	operatorToken := mintControllerTestToken(t, "controller-secret", []identity.Role{identity.RoleOperator}, ctrl.auth.TokenAudience)
	approverToken := mintControllerTestToken(t, "controller-secret", []identity.Role{identity.RoleApprover}, ctrl.auth.TokenAudience)
	adminToken := mintControllerTestToken(t, "controller-secret", []identity.Role{identity.RoleAdmin}, ctrl.auth.TokenAudience)

	t.Run("viewer can read status and status exposes auth posture", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("Authorization", "Bearer "+viewerToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var payload map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
		authBlock, ok := payload["auth"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, string(ControllerAuthModeToken), authBlock["mode"])
		require.Equal(t, string(ControllerAPIKeyModeDisabled), authBlock["api_key_mode"])
		require.Equal(t, "TEST_STATUS_TOKEN_SECRET", authBlock["token_secret_env"])
		require.Equal(t, "controller-api", authBlock["token_audience"])
		require.Equal(t, "controller-ingest", authBlock["ingest_token_audience"])
		require.Equal(t, true, authBlock["ingest_auth_enabled"])
		require.Equal(t, false, authBlock["compatibility_api_keys_enabled"])
		require.Equal(t, false, authBlock["anonymous_http_allowed"])
		require.Equal(t, false, authBlock["anonymous_ingest_allowed"])
	})

	t.Run("viewer cannot trigger admin mutation", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/config/reload", nil)
		req.Header.Set("Authorization", "Bearer "+viewerToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("operator can reach execution path but not approval path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/actions/execute", nil)
		req.Header.Set("Authorization", "Bearer "+operatorToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusUnauthorized, rec.Code)
		require.NotEqual(t, http.StatusForbidden, rec.Code)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/controller/actions/approve", nil)
		req.Header.Set("Authorization", "Bearer "+operatorToken)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("approver can approve but cannot execute", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/actions/approve", nil)
		req.Header.Set("Authorization", "Bearer "+approverToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusUnauthorized, rec.Code)
		require.NotEqual(t, http.StatusForbidden, rec.Code)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/controller/actions/execute", nil)
		req.Header.Set("Authorization", "Bearer "+approverToken)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("admin can cross both approval and execution paths", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/actions/approve", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusUnauthorized, rec.Code)
		require.NotEqual(t, http.StatusForbidden, rec.Code)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/controller/config/reload", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusUnauthorized, rec.Code)
		require.NotEqual(t, http.StatusForbidden, rec.Code)
	})

	t.Run("status exposes authorization failures", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/config/reload", nil)
		req.Header.Set("Authorization", "Bearer "+viewerToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)

		req = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		var payload map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&payload))
		authBlock := payload["auth"].(map[string]any)
		require.GreaterOrEqual(t, authBlock["http_authorization_failures"].(float64), float64(1))
	})
}

func TestControllerMixedModeCompatibilityAPIKeys(t *testing.T) {
	logger := zap.NewNop()
	cfg := DefaultConfig()
	cfg.Auth.Enabled = true
	cfg.Auth.Mode = string(ControllerAuthModeMixed)
	cfg.Auth.TokenSecretEnv = "TEST_MIXED_TOKEN_SECRET"
	cfg.Auth.ReadAPIKeyEnv = "TEST_MIXED_READ_KEY"
	cfg.Auth.ActionAPIKeyEnv = "TEST_MIXED_ACTION_KEY"
	t.Setenv("TEST_MIXED_TOKEN_SECRET", "controller-secret")
	t.Setenv("TEST_MIXED_READ_KEY", "read-secret")
	t.Setenv("TEST_MIXED_ACTION_KEY", "action-secret")

	ctrl, err := New(cfg, logger)
	require.NoError(t, err)

	mux := http.NewServeMux()
	ctrl.registerHandlers(mux)
	handler := ctrl.wrapHTTPAuthentication(ctrl.wrapHTTPHandler(mux))

	t.Run("read compatibility key stays read only", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("X-API-Key", "read-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/controller/config/reload", nil)
		req.Header.Set("X-API-Key", "read-secret")
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("action compatibility key can mutate", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/config/reload", nil)
		req.Header.Set("X-API-Key", "action-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusUnauthorized, rec.Code)
		require.NotEqual(t, http.StatusForbidden, rec.Code)
	})
}
