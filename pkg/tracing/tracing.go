// Package tracing wires OpenTelemetry traces to an OTLP/gRPC collector (the
// all-in-one Jaeger from docker-compose). A transfer becomes one trace spanning
// switch -> rail -> ledger (ARCHITECTURE §7).
//
// When the endpoint is empty, Init installs nothing and returns a no-op
// shutdown, so a service runs fine with tracing disabled.
package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ShutdownFunc flushes and stops the tracer provider. Always safe to call.
type ShutdownFunc func(context.Context) error

// Init configures the global tracer provider and propagators for serviceName,
// exporting to the OTLP/gRPC endpoint (e.g. "localhost:4317"). An empty endpoint
// disables tracing and returns a no-op ShutdownFunc.
func Init(ctx context.Context, serviceName, endpoint string) (ShutdownFunc, error) {
	noop := func(context.Context) error { return nil }
	if endpoint == "" {
		return noop, nil
	}

	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return noop, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", serviceName)),
	)
	if err != nil {
		return noop, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}
