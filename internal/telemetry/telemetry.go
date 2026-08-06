// Package telemetry configures the application's OpenTelemetry SDK.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName      = "github.com/jrduncans/nwsl-season"
	defaultHoneycombEndpoint = "https://api.honeycomb.io"
	defaultTelemetryService  = "nwsl-season-server"
	honeycombAPIKeyEnv       = "HONEYCOMB_API_KEY"
	honeycombAPIEndpointEnv  = "HONEYCOMB_API_ENDPOINT"
	legacyMetricsDatasetEnv  = "HONEYCOMB_METRICS_DATASET"

	// SlowOperationThreshold retains a child span for work whose individual
	// timing is useful in a trace waterfall. Faster repeated work is recorded
	// as timing attributes on its parent span instead.
	SlowOperationThreshold = 25 * time.Millisecond
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
// Metrics are opt-in through OTEL_METRICS_EXPORTER=otlp. The legacy
// HONEYCOMB_METRICS_DATASET variable is also accepted as an enablement signal,
// but is not sent as an OTLP header. When no trace exporter is configured,
// spans remain no-ops so local development does not need credentials or a
// collector.
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

	baseResource, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("build OpenTelemetry resource: %w", err)
	}
	resourceAttributes := []attribute.KeyValue{attribute.String("service.name", serviceName)}
	if _, configured := baseResource.Set().Value(attribute.Key("service.version")); !configured {
		resourceAttributes = append(resourceAttributes, attribute.String("service.version", buildVersion()))
	}
	res, err := resource.Merge(baseResource, resource.NewSchemaless(resourceAttributes...))
	if err != nil {
		return nil, fmt.Errorf("merge OpenTelemetry resource: %w", err)
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
		logger.Info("OpenTelemetry trace export disabled; set HONEYCOMB_API_KEY or OTEL_EXPORTER_OTLP_ENDPOINT to enable it", "metrics", metrics != nil)
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

// Meter returns the common application meter for manually instrumented work.
func Meter() otelmetric.Meter { return otel.Meter(instrumentationName) }

// RecordError gives a span an exception event and queryable failure dimensions.
// Prefer RecordErrorWithSlug for a static identifier of the failure site.
func RecordError(span oteltrace.Span, err error) {
	RecordErrorWithSlug(span, err, "")
}

// RecordErrorWithSlug records an error without using its potentially
// high-cardinality message as a span dimension. Slugs are static identifiers
// (for example, "err-sync-fetch-asa") that make failures easy to group and
// connect back to a code path.
func RecordErrorWithSlug(span oteltrace.Span, err error, slug string) {
	recordErrorWithSlug(span, err, slug)
}

func recordErrorWithSlug(span oteltrace.Span, err error, slug string, options ...oteltrace.EventOption) {
	if err == nil {
		return
	}
	span.RecordError(err, options...)
	attributes := []attribute.KeyValue{
		attribute.Bool("error", true),
		attribute.String("error.type", reflect.TypeOf(err).String()),
	}
	if slug != "" {
		attributes = append(attributes, attribute.String("exception.slug", slug))
	}
	span.SetAttributes(attributes...)
	span.SetStatus(codes.Error, "error")
}

// RecordCompletedSpan creates a child span after an operation has completed.
// It is useful for exceptional or slow loop work: the ordinary fast path can
// remain a wide parent span, while the retained child keeps its real timing
// and trace placement.
func RecordCompletedSpan(ctx context.Context, name string, started, finished time.Time, attributes []attribute.KeyValue, err error, slug string) {
	_, span := Tracer().Start(ctx, name,
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithTimestamp(started),
		oteltrace.WithAttributes(attributes...),
	)
	if err != nil {
		recordErrorWithSlug(span, err, slug, oteltrace.WithTimestamp(finished))
	}
	span.End(oteltrace.WithTimestamp(finished))
}

func newTraceExporter(ctx context.Context) (trace.SpanExporter, string, error) {
	if tracesExportDisabled() {
		return nil, "", nil
	}
	if err := validateOTLPHTTPProtocol("traces"); err != nil {
		return nil, "", err
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
	if !metricsExportEnabled() {
		return nil, nil
	}
	if err := validateOTLPHTTPProtocol("metrics"); err != nil {
		return nil, err
	}
	var (
		exporter *otlpmetrichttp.Exporter
		err      error
	)
	if apiKey != "" {
		endpoint, endpointErr := honeycombSignalEndpoint("/v1/metrics")
		if endpointErr != nil {
			return nil, endpointErr
		}
		exporter, err = otlpmetrichttp.New(ctx,
			otlpmetrichttp.WithEndpointURL(endpoint),
			otlpmetrichttp.WithHeaders(map[string]string{"x-honeycomb-team": apiKey}),
		)
	} else {
		if !otlpMetricsEndpointConfigured() {
			return nil, errors.New("OTEL_METRICS_EXPORTER=otlp requires HONEYCOMB_API_KEY or an OTEL_EXPORTER_OTLP_ENDPOINT/OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
		}
		exporter, err = otlpmetrichttp.New(ctx)
	}
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

func metricsExportEnabled() bool {
	value := strings.TrimSpace(os.Getenv("OTEL_METRICS_EXPORTER"))
	if strings.EqualFold(value, "none") {
		return false
	}
	return strings.EqualFold(value, "otlp") || strings.TrimSpace(os.Getenv(legacyMetricsDatasetEnv)) != ""
}

func otlpTraceEndpointConfigured() bool {
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) != ""
}

func otlpMetricsEndpointConfigured() bool {
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")) != ""
}

func validateOTLPHTTPProtocol(signal string) error {
	for _, name := range []string{
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_" + strings.ToUpper(signal) + "_PROTOCOL",
	} {
		protocol := strings.TrimSpace(os.Getenv(name))
		if protocol == "" || strings.EqualFold(protocol, "http/protobuf") {
			continue
		}
		return fmt.Errorf("%s=%q is unsupported for %s; configure http/protobuf", name, protocol, signal)
	}
	return nil
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	var revision string
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if revision == "" {
		return "devel"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		return "git-" + revision + "-dirty"
	}
	return "git-" + revision
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
