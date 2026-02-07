package middleware

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TraceMiddleware adds OpenTelemetry tracing to requests
func TraceMiddleware(next http.Handler) http.Handler {
	tracer := otel.Tracer("ai-sre-controller")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract context from headers (B3, W3C)
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// Start span
		ctx, span := tracer.Start(ctx, r.URL.Path, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()

		// Inject context back into request
		r = r.WithContext(ctx)

		// Add attributes
		span.SetAttributes(
		// Standard HTTP attributes would go here
		)

		// Call next
		next.ServeHTTP(w, r)
	})
}

// WithTraceContext wraps a context with a span for internal work
func WithTraceContext(ctx context.Context, name string) (context.Context, trace.Span) {
	tracer := otel.Tracer("ai-sre-internal")
	return tracer.Start(ctx, name)
}
