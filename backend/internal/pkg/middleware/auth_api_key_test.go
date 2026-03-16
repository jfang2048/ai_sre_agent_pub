package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAPIKeyAuthValidKey validates valid API key authentication
func TestAPIKeyAuthValidKey(t *testing.T) {
	testCases := []struct {
		name         string
		apiKey       string
		authHeader   string
		expectStatus int
	}{
		{
			name:         "valid Bearer token",
			apiKey:       "secret-key-123",
			authHeader:   "Bearer secret-key-123",
			expectStatus: http.StatusOK,
		},
		{
			name:         "valid key without Bearer",
			apiKey:       "secret-key-123",
			authHeader:   "secret-key-123",
			expectStatus: http.StatusOK,
		},
		{
			name:         "valid key with extra spaces",
			apiKey:       "secret-key-123",
			authHeader:   "Bearer  secret-key-123 ",
			expectStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			middleware := APIKeyAuth(tc.apiKey, next)

			req := httptest.NewRequest("GET", "/api/test", nil)
			req.Header.Set("Authorization", tc.authHeader)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, tc.expectStatus, w.Code)
		})
	}
}

// TestAPIKeyAuthInvalidKey validates invalid API key rejection
func TestAPIKeyAuthInvalidKey(t *testing.T) {
	testCases := []struct {
		name       string
		apiKey     string
		authHeader string
	}{
		{
			name:       "wrong key",
			apiKey:     "correct-key",
			authHeader: "Bearer wrong-key",
		},
		{
			name:       "empty key",
			apiKey:     "secret",
			authHeader: "Bearer ",
		},
		{
			name:       "no authorization header",
			apiKey:     "secret",
			authHeader: "",
		},
		{
			name:       "completely different",
			apiKey:     "abc123",
			authHeader: "Bearer xyz789",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			middleware := APIKeyAuth(tc.apiKey, next)

			req := httptest.NewRequest("GET", "/api/test", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code)
			require.Contains(t, w.Body.String(), "Unauthorized")
		})
	}
}

// TestAPIKeyAuthSkipPaths validates path skipping
func TestAPIKeyAuthSkipPaths(t *testing.T) {
	apiKey := "secret-key"

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	middleware := APIKeyAuth(apiKey, next)

	testCases := []struct {
		path         string
		authHeader   string
		expectStatus int
	}{
		{
			path:         "/health",
			authHeader:   "",
			expectStatus: http.StatusOK, // Skips auth
		},
		{
			path:         "/web/index.html",
			authHeader:   "",
			expectStatus: http.StatusOK, // Skips auth
		},
		{
			path:         "/web/js/app.js",
			authHeader:   "",
			expectStatus: http.StatusOK, // Skips auth
		},
		{
			path:         "/readyz",
			authHeader:   "",
			expectStatus: http.StatusOK, // K8s readiness must not require auth
		},
		{
			path:         "/metrics",
			authHeader:   "",
			expectStatus: http.StatusOK, // Prometheus scraping should stay simple
		},
		{
			path:         "/api/v1/health",
			authHeader:   "",
			expectStatus: http.StatusOK, // JSON health should not require auth
		},
		{
			path:         "/api/test",
			authHeader:   "",
			expectStatus: http.StatusUnauthorized, // Requires auth
		},
		{
			path:         "/api/status",
			authHeader:   "Bearer wrong-key",
			expectStatus: http.StatusUnauthorized, // Requires valid auth
		},
		{
			path:         "/health",
			authHeader:   "Bearer secret-key",
			expectStatus: http.StatusOK, // Skips auth even with key
		},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, tc.expectStatus, w.Code)
		})
	}
}

// TestAPIKeyAuthMethods validates all HTTP methods
func TestAPIKeyAuthMethods(t *testing.T) {
	apiKey := "secret-key"

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := APIKeyAuth(apiKey, next)

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/test", nil)
			req.Header.Set("Authorization", "Bearer "+apiKey)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
		})
	}
}

// TestAPIKeyAuthCaseSensitivity validates key case sensitivity
func TestAPIKeyAuthCaseSensitivity(t *testing.T) {
	apiKey := "Secret-Key-123"

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := APIKeyAuth(apiKey, next)

	testCases := []struct {
		name         string
		authHeader   string
		expectStatus int
	}{
		{
			name:         "exact case match",
			authHeader:   "Bearer Secret-Key-123",
			expectStatus: http.StatusOK,
		},
		{
			name:         "lowercase",
			authHeader:   "Bearer secret-key-123",
			expectStatus: http.StatusUnauthorized,
		},
		{
			name:         "uppercase",
			authHeader:   "Bearer SECRET-KEY-123",
			expectStatus: http.StatusUnauthorized,
		},
		{
			name:         "mixed case",
			authHeader:   "Bearer SeCrEt-KeY-123",
			expectStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/test", nil)
			req.Header.Set("Authorization", tc.authHeader)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, tc.expectStatus, w.Code)
		})
	}
}

// TestAPIKeyAuthEmptyAPIKeyFailsClosed validates empty configured API key behavior.
func TestAPIKeyAuthEmptyAPIKeyFailsClosed(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := APIKeyAuth("", next)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAPIKeyAuthHeaderFormats validates various header formats
func TestAPIKeyAuthHeaderFormats(t *testing.T) {
	apiKey := "secret-key"

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := APIKeyAuth(apiKey, next)

	testCases := []struct {
		name         string
		authHeader   string
		expectStatus int
	}{
		{
			name:         "Bearer with single space",
			authHeader:   "Bearer secret-key",
			expectStatus: http.StatusOK,
		},
		{
			name:         "Bearer with multiple spaces",
			authHeader:   "Bearer  secret-key",
			expectStatus: http.StatusOK,
		},
		{
			name:         "no Bearer prefix",
			authHeader:   "secret-key",
			expectStatus: http.StatusOK,
		},
		{
			name:         "lowercase bearer - TrimPrefix is case-sensitive",
			authHeader:   "bearer secret-key",
			expectStatus: http.StatusUnauthorized, // "bearer secret-key" != "secret-key"
		},
		{
			name:         "mixed case bearer - TrimPrefix is case-sensitive",
			authHeader:   "BeArEr secret-key",
			expectStatus: http.StatusUnauthorized, // "BeArEr secret-key" != "secret-key"
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/test", nil)
			req.Header.Set("Authorization", tc.authHeader)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, tc.expectStatus, w.Code)
		})
	}
}

// TestAPIKeyAuthSpecialCharacters validates special characters in keys
func TestAPIKeyAuthSpecialCharacters(t *testing.T) {
	testCases := []struct {
		name         string
		apiKey       string
		authHeader   string
		expectStatus int
	}{
		{
			name:         "key with hyphens",
			apiKey:       "secret-key-123",
			authHeader:   "Bearer secret-key-123",
			expectStatus: http.StatusOK,
		},
		{
			name:         "key with underscores",
			apiKey:       "secret_key_123",
			authHeader:   "Bearer secret_key_123",
			expectStatus: http.StatusOK,
		},
		{
			name:         "key with dots",
			apiKey:       "secret.key.123",
			authHeader:   "Bearer secret.key.123",
			expectStatus: http.StatusOK,
		},
		{
			name:         "key with special chars",
			apiKey:       "k3y@#$%^&*()",
			authHeader:   "Bearer k3y@#$%^&*()",
			expectStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			middleware := APIKeyAuth(tc.apiKey, next)

			req := httptest.NewRequest("GET", "/api/test", nil)
			req.Header.Set("Authorization", tc.authHeader)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, tc.expectStatus, w.Code)
		})
	}
}
