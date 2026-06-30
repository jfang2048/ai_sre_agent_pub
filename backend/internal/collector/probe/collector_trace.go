// Package probe implements OpenTelemetry trace collection (Level 3/4).
// This enables application-level visibility alongside system metrics.
package probe

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// TraceCollector manages OpenTelemetry tracing
type TraceCollector struct {
	hostname string
	tp       *sdktrace.TracerProvider
	exporter *jaeger.Exporter
	enabled  bool
}

// NewTraceCollector creates a new trace collector
func NewTraceCollector(hostname string, endpoint string) (*TraceCollector, error) {
	if endpoint == "" {
		return &TraceCollector{hostname: hostname, enabled: false}, nil
	}

	// Create Jaeger exporter
	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(endpoint)))
	if err != nil {
		return nil, err
	}

	// Create TracerProvider resource
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName("sre-collector-probe"),
			semconv.HostName(hostname),
		),
	)
	if err != nil {
		return nil, err
	}

	// Create TracerProvider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)

	// Set as global
	otel.SetTracerProvider(tp)

	return &TraceCollector{
		hostname: hostname,
		tp:       tp,
		exporter: exp,
		enabled:  true,
	}, nil
}

// StartTrace starts a new trace span
func (tc *TraceCollector) StartTrace(ctx context.Context, name string, attrs map[string]string) (context.Context, func()) {
	if !tc.enabled {
		return ctx, func() {}
	}

	tracer := tc.tp.Tracer("sre-collector")

	// Convert attributes
	var kv []attribute.KeyValue
	for k, v := range attrs {
		kv = append(kv, attribute.String(k, v))
	}

	newCtx, span := tracer.Start(ctx, name)
	span.SetAttributes(kv...)

	return newCtx, func() {
		span.End()
	}
}

// Shutdown flushes and stops the collector
func (tc *TraceCollector) Shutdown(ctx context.Context) error {
	if !tc.enabled {
		return nil
	}
	return tc.tp.Shutdown(ctx)
}

// Metric helper to expose trace count as a metric
func (tc *TraceCollector) GetMetrics(now time.Time) []Metric {
	if !tc.enabled {
		return nil
	}
	// Placeholder: real implementation would track span counts
	return []Metric{
		{
			Name:      "node_trace_provider_up",
			Type:      "gauge",
			Value:     1,
			Timestamp: now,
		},
	}
}
