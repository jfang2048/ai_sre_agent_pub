package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRateLimitRejectsExcessRequests(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := RateLimit(RateLimitConfig{
		Enabled: true,
		RPS:     1,
		Burst:   1,
	}, next)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	require.Equal(t, http.StatusOK, first.Code)

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	require.Equal(t, http.StatusTooManyRequests, second.Code)
}

func TestRateLimitSkipBypassesLimiter(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := RateLimit(RateLimitConfig{
		Enabled: true,
		RPS:     1,
		Burst:   1,
		Skip: func(r *http.Request) bool {
			return r != nil && r.URL.Path == "/readyz"
		},
	}, next)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
}
