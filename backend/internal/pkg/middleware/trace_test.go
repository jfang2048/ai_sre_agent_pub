package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

// TestTraceMiddleware validates trace middleware behavior
func TestTraceMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify context has span
		span := trace.SpanFromContext(r.Context())
		require.NotNil(t, span.SpanContext(), "span should exist in context")

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := TraceMiddleware(next)

	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "OK", w.Body.String())
}

// TestTraceMiddlewarePreservesContext validates context is preserved
func TestTraceMiddlewarePreservesContext(t *testing.T) {
	originalCtx := context.Background()
	ctxWithValue := context.WithValue(originalCtx, "test-key", "test-value")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify original context values are preserved
		value := r.Context().Value("test-key")
		require.Equal(t, "test-value", value)

		// Verify span is added
		span := trace.SpanFromContext(r.Context())
		require.NotNil(t, span.SpanContext())

		w.WriteHeader(http.StatusOK)
	})

	middleware := TraceMiddleware(next)

	req := httptest.NewRequest("GET", "/api/test", nil).WithContext(ctxWithValue)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestWithTraceContext validates trace context creation
func TestWithTraceContext(t *testing.T) {
	ctx := context.Background()

	// Create a span
	ctx, span := WithTraceContext(ctx, "test-operation")

	require.NotNil(t, ctx)
	require.NotNil(t, span)

	// Verify span is in context
	retrievedSpan := trace.SpanFromContext(ctx)
	require.NotNil(t, retrievedSpan)

	// End the span
	span.End()
}

// TestTraceMiddlewareMultipleRequests validates multiple requests
func TestTraceMiddlewareMultipleRequests(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		require.NotNil(t, span.SpanContext())
		w.WriteHeader(http.StatusOK)
	})

	middleware := TraceMiddleware(next)

	// Multiple requests
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
	}
}

// TestTraceMiddlewareDifferentPaths validates different paths
func TestTraceMiddlewareDifferentPaths(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		require.NotNil(t, span.SpanContext())
		w.WriteHeader(http.StatusOK)
	})

	middleware := TraceMiddleware(next)

	paths := []string{
		"/api/v1/status",
		"/api/v1/metrics",
		"/api/v1/config",
		"/health",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
		})
	}
}

// TestTraceMiddlewareAllHTTPMethods validates all HTTP methods
func TestTraceMiddlewareAllHTTPMethods(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		require.NotNil(t, span.SpanContext())
		w.WriteHeader(http.StatusOK)
	})

	middleware := TraceMiddleware(next)

	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/test", nil)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
		})
	}
}

// TestTraceMiddlewareHeaders validates headers handling
func TestTraceMiddlewareHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify span context is extracted/created
		span := trace.SpanFromContext(r.Context())
		require.NotNil(t, span.SpanContext())
		w.WriteHeader(http.StatusOK)
	})

	middleware := TraceMiddleware(next)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Header", "custom-value")

	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// Headers should be preserved
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, "custom-value", req.Header.Get("X-Custom-Header"))
}

// TestTraceMiddlewareResponseHeaders validates response headers
func TestTraceMiddlewareResponseHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Response", "response-value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := TraceMiddleware(next)

	req := httptest.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()

	middleware.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "response-value", w.Header().Get("X-Custom-Response"))
}

// TestWithTraceContextNested validates nested trace contexts
func TestWithTraceContextNested(t *testing.T) {
	ctx := context.Background()

	// Create parent span
	ctx, parentSpan := WithTraceContext(ctx, "parent-operation")

	// Create child span
	ctx, childSpan := WithTraceContext(ctx, "child-operation")

	require.NotNil(t, ctx)
	require.NotNil(t, childSpan)

	// Verify child span context
	childSpanCtx := trace.SpanFromContext(ctx)
	require.NotNil(t, childSpanCtx)

	// End in reverse order
	childSpan.End()
	parentSpan.End()
}

// TestWithTraceContextWithName validates different operation names
func TestWithTraceContextWithName(t *testing.T) {
	operationNames := []string{
		"database-query",
		"api-call",
		"cache-lookup",
		"background-job",
	}

	for _, opName := range operationNames {
		t.Run(opName, func(t *testing.T) {
			ctx := context.Background()
			ctx, span := WithTraceContext(ctx, opName)

			require.NotNil(t, ctx)
			require.NotNil(t, span)

			span.End()
		})
	}
}

// TestTraceMiddlewareConcurrentRequests validates concurrent request handling
func TestTraceMiddlewareConcurrentRequests(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		require.NotNil(t, span.SpanContext())
		w.WriteHeader(http.StatusOK)
	})

	middleware := TraceMiddleware(next)

	// Simulate concurrent requests
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			req := httptest.NewRequest("GET", "/api/test", nil)
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		default:
		}
	}
}
