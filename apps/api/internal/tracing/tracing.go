// Package tracing wires the OpenTelemetry SDK with an OTLP/HTTP
// exporter. The provider is built only when an exporter endpoint is
// configured (env: OTEL_EXPORTER_OTLP_ENDPOINT) so unconfigured
// deployments get a real `trace.NoopTracerProvider` and pay zero cost
// per span.
//
// All settings come from the standard OTEL_* env vars rather than a
// custom set, so dashboards and Collector configs the operator already
// uses Just Work.
package tracing

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Shutdown is a function the caller defers to flush + tear down the
// provider on process exit. A no-op shutdown is returned when tracing
// is disabled.
type Shutdown func(context.Context) error

// Setup configures the global OpenTelemetry tracer provider when the
// standard OTLP endpoint env vars are set, otherwise it leaves the
// global provider as the SDK default (a no-op). Either way the same
// `otel.Tracer(...)` call works in instrumented code.
//
// version flows into the resource as service.version so traces carry
// the same deploy tag we expose in the build_info Prometheus gauge.
func Setup(ctx context.Context, serviceName, version string) (Shutdown, bool, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		// No tracing backend configured. Leave the global provider as
		// the SDK no-op and return a Shutdown that does nothing.
		return func(context.Context) error { return nil }, false, nil
	}

	exp, err := otlptrace.New(ctx, otlptracehttp.NewClient())
	if err != nil {
		return nil, false, fmt.Errorf("tracing: build OTLP exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return nil, false, fmt.Errorf("tracing: merge resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp,
			sdktrace.WithBatchTimeout(5*time.Second),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	shutdown := func(ctx context.Context) error {
		// Flush + force a final batch out before the exporter shuts down.
		if err := tp.ForceFlush(ctx); err != nil {
			// Returning the flush error masks any later shutdown error,
			// but they're equivalent operationally.
			_ = tp.Shutdown(ctx)
			return err
		}
		return tp.Shutdown(ctx)
	}
	return shutdown, true, nil
}

// Tracer returns the named tracer from the global provider. When
// tracing is disabled this is a no-op tracer; spans started from it
// allocate nothing measurable.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
