package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthSharedMode(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := APIKeyAuth(APIKeyAuthConfig{
		ReadKey:   "shared-key",
		ActionKey: "shared-key",
		ScopeForRequest: func(r *http.Request) APIKeyAuthScope {
			return APIKeyAuthScopeAction
		},
	}, next)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/actions/execute", nil)
	req.Header.Set("Authorization", "Bearer shared-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAPIKeyAuthSplitMode(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	handler := APIKeyAuth(APIKeyAuthConfig{
		ReadKey:   "read-key",
		ActionKey: "action-key",
		ScopeForRequest: func(r *http.Request) APIKeyAuthScope {
			if r.Method == http.MethodGet {
				return APIKeyAuthScopeRead
			}
			return APIKeyAuthScopeAction
		},
	}, next)

	t.Run("read key can read", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("Authorization", "Bearer read-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("read key cannot act", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/actions/execute", nil)
		req.Header.Set("Authorization", "Bearer read-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("action key can read and act", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("Authorization", "Bearer action-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNoContent, rec.Code)

		req = httptest.NewRequest(http.MethodPost, "/api/v1/controller/actions/execute", nil)
		req.Header.Set("X-API-Key", "action-key")
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNoContent, rec.Code)
	})
}

func TestAPIKeyAuthReadOnlyModeBlocksActions(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	handler := APIKeyAuth(APIKeyAuthConfig{
		ReadKey: "read-key",
		ScopeForRequest: func(r *http.Request) APIKeyAuthScope {
			if r.Method == http.MethodGet {
				return APIKeyAuthScopeRead
			}
			return APIKeyAuthScopeAction
		},
	}, next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer read-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusAccepted, rec.Code)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/controller/config/reload", nil)
	req.Header.Set("Authorization", "Bearer read-key")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAPIKeyAuthPublicScopeSkipsCredentials(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := APIKeyAuth(APIKeyAuthConfig{
		ReadKey:   "read-key",
		ActionKey: "action-key",
		ScopeForRequest: func(r *http.Request) APIKeyAuthScope {
			return APIKeyAuthScopePublic
		},
	}, next)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
}

func TestAPIKeyAuthRejectsQueryStringKey(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := APIKeyAuth(APIKeyAuthConfig{
		ReadKey: "read-key",
		ScopeForRequest: func(r *http.Request) APIKeyAuthScope {
			return APIKeyAuthScopeRead
		},
	}, next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status?api_key=read-key", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
