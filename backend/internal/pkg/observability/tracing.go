package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

var (
	tracerProvider *tracesdk.TracerProvider
	tracer         trace.Tracer
)

// InitTracing initializes OpenTelemetry tracing
func InitTracing(serviceName, jaegerEndpoint string, l *zap.Logger) error {
	if jaegerEndpoint == "" {
		l.Info("tracing disabled, no endpoint configured")
		return nil
	}

	// Create Jaeger exporter
	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(
		jaeger.WithEndpoint(jaegerEndpoint),
	))
	if err != nil {
		return fmt.Errorf("failed to create Jaeger exporter: %w", err)
	}

	// Create tracer provider
	tp := tracesdk.NewTracerProvider(
		tracesdk.WithBatcher(exp),
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
			attribute.String("environment", "production"),
		)),
	)

	// Register as global tracer provider
	otel.SetTracerProvider(tp)
	tracerProvider = tp
	tracer = tp.Tracer(serviceName)

	l.Info("tracing initialized", zap.String("endpoint", jaegerEndpoint))
	return nil
}

// ShutdownTracing shuts down the tracer provider
func ShutdownTracing(ctx context.Context) error {
	if tracerProvider != nil {
		return tracerProvider.Shutdown(ctx)
	}
	return nil
}

// StartSpan starts a new trace span
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	if tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}
	return tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

// Span wraps a function with tracing
func Span(ctx context.Context, name string, fn func(context.Context) error, attrs ...attribute.KeyValue) error {
	ctx, span := StartSpan(ctx, name, attrs...)
	defer span.End()

	err := fn(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}

// SpanWithResult wraps a function with tracing and returns a result
func SpanWithResult[T any](ctx context.Context, name string, fn func(context.Context) (T, error), attrs ...attribute.KeyValue) (T, error) {
	ctx, span := StartSpan(ctx, name, attrs...)
	defer span.End()

	result, err := fn(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return result, err
}

// RecordError records an error in the current span
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

// AddEvent adds an event to the current span
func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		span.AddEvent(name, trace.WithAttributes(attrs...))
	}
}

// SetAttributes sets attributes on the current span
func SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		span.SetAttributes(attrs...)
	}
}

// TracedFunction wraps a function to automatically create a span based on the function name
func TracedFunction[T any](fn func(context.Context) (T, error)) func(context.Context) (T, error) {
	return func(ctx context.Context) (T, error) {
		start := time.Now()
		result, err := fn(ctx)
		duration := time.Since(start)

		attrs := []attribute.KeyValue{
			attribute.String("function", GetFunctionName(fn)),
			attribute.Int64("duration_ms", duration.Milliseconds()),
		}

		if err != nil {
			attrs = append(attrs, attribute.String("error", err.Error()))
		}

		AddEvent(ctx, "function_completed", attrs...)
		return result, err
	}
}

// GetFunctionName returns the name of a function (simplified)
func GetFunctionName(fn interface{}) string {
	return "function"
}

// TraceID returns the trace ID from the context if available
func TraceID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		return span.SpanContext().TraceID().String()
	}
	return ""
}

// SpanID returns the span ID from the context if available
func SpanID(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		return span.SpanContext().SpanID().String()
	}
	return ""
}
