// Package telemetry configures the application's OpenTelemetry SDK.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName        = "github.com/jrduncans/nwsl-season"
	defaultHoneycombEndpoint   = "https://api.honeycomb.io"
	defaultTelemetryService    = "nwsl-season-server"
	honeycombAPIKeyEnv         = "HONEYCOMB_API_KEY"
	honeycombAPIEndpointEnv    = "HONEYCOMB_API_ENDPOINT"
	honeycombMetricsDatasetEnv = "HONEYCOMB_METRICS_DATASET"
)

// Providers owns the configured SDK providers and flushes their batches during
// graceful process shutdown.
type Providers struct {
	traces  *trace.TracerProvider
	metrics *metric.MeterProvider
}

// Configure installs OpenTelemetry providers for the process. Supplying a
// HONEYCOMB_API_KEY exports traces directly to Honeycomb. The standard OTLP
// trace endpoint variables are also supported for collector-based deployments.
//
// HONEYCOMB_METRICS_DATASET enables HTTP metrics in addition to traces. When
// no exporter is configured, spans remain no-ops so local development does not
// need credentials or a collector.
func Configure(ctx context.Context, logger *slog.Logger, fallbackServiceName string) (*Providers, error) {
	if logger == nil {
		logger = slog.Default()
	}
	serviceName := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if serviceName == "" {
		serviceName = fallbackServiceName
	}
	if serviceName == "" {
		serviceName = defaultTelemetryService
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(attribute.String("service.name", serviceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("build OpenTelemetry resource: %w", err)
	}

	traceExporter, exporterName, err := newTraceExporter(ctx)
	if err != nil {
		return nil, err
	}
	traceOptions := []trace.TracerProviderOption{trace.WithResource(res)}
	if traceExporter == nil {
		traceOptions = append(traceOptions, trace.WithSampler(trace.NeverSample()))
	} else {
		traceOptions = append(traceOptions, trace.WithBatcher(traceExporter))
	}
	traces := trace.NewTracerProvider(traceOptions...)

	metrics, err := newMeterProvider(ctx, res)
	if err != nil {
		_ = traces.Shutdown(ctx)
		return nil, err
	}

	otel.SetTracerProvider(traces)
	if metrics != nil {
		otel.SetMeterProvider(metrics)
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	if traceExporter == nil {
		logger.Info("OpenTelemetry trace export disabled; set HONEYCOMB_API_KEY or OTEL_EXPORTER_OTLP_ENDPOINT to enable it")
	} else {
		logger.Info("OpenTelemetry trace export enabled", "exporter", exporterName, "service", serviceName, "metrics", metrics != nil)
	}
	return &Providers{traces: traces, metrics: metrics}, nil
}

// Shutdown flushes trace and metric batches. Call it with a fresh, bounded
// context because a process signal may have already canceled the application
// context.
func (p *Providers) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var errs []error
	if p.metrics != nil {
		errs = append(errs, p.metrics.Shutdown(ctx))
	}
	if p.traces != nil {
		errs = append(errs, p.traces.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

// Tracer returns the common application tracer for manually instrumented work.
func Tracer() oteltrace.Tracer { return otel.Tracer(instrumentationName) }

// RecordError gives a span the conventional OpenTelemetry error event and
// status without leaking implementation details into callers.
func RecordError(span oteltrace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

func newTraceExporter(ctx context.Context) (trace.SpanExporter, string, error) {
	if tracesExportDisabled() {
		return nil, "", nil
	}
	if apiKey := strings.TrimSpace(os.Getenv(honeycombAPIKeyEnv)); apiKey != "" {
		endpoint, err := honeycombSignalEndpoint("/v1/traces")
		if err != nil {
			return nil, "", err
		}
		exporter, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(endpoint),
			otlptracehttp.WithHeaders(map[string]string{"x-honeycomb-team": apiKey}),
		)
		if err != nil {
			return nil, "", fmt.Errorf("create Honeycomb trace exporter: %w", err)
		}
		return exporter, "Honeycomb", nil
	}
	if !otlpTraceEndpointConfigured() {
		return nil, "", nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	return exporter, "OTLP", nil
}

func newMeterProvider(ctx context.Context, res *resource.Resource) (*metric.MeterProvider, error) {
	apiKey := strings.TrimSpace(os.Getenv(honeycombAPIKeyEnv))
	dataset := strings.TrimSpace(os.Getenv(honeycombMetricsDatasetEnv))
	if apiKey == "" || dataset == "" || tracesExportDisabled() {
		return nil, nil
	}
	endpoint, err := honeycombSignalEndpoint("/v1/metrics")
	if err != nil {
		return nil, err
	}
	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(endpoint),
		otlpmetrichttp.WithHeaders(map[string]string{
			"x-honeycomb-team":    apiKey,
			"x-honeycomb-dataset": dataset,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("create Honeycomb metric exporter: %w", err)
	}
	return metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(exporter)),
	), nil
}

func tracesExportDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_TRACES_EXPORTER")), "none")
}

func otlpTraceEndpointConfigured() bool {
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
}

func honeycombSignalEndpoint(signalPath string) (string, error) {
	raw := strings.TrimSpace(os.Getenv(honeycombAPIEndpointEnv))
	if raw == "" {
		raw = defaultHoneycombEndpoint
	}
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return "", fmt.Errorf("%s must be an absolute URL, got %q", honeycombAPIEndpointEnv, raw)
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", fmt.Errorf("%s must not include a query or fragment", honeycombAPIEndpointEnv)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + signalPath
	return endpoint.String(), nil
}
