// Package telemetry configures the application's OpenTelemetry SDK.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName      = "github.com/jrduncans/nwsl-season"
	defaultHoneycombEndpoint = "https://api.honeycomb.io"
	defaultTelemetryService  = "nwsl-season-server"
	honeycombAPIKeyEnv       = "HONEYCOMB_API_KEY" // #nosec G101 -- this is an environment-variable name, not a credential.
	honeycombAPIEndpointEnv  = "HONEYCOMB_API_ENDPOINT"
	legacyMetricsDatasetEnv  = "HONEYCOMB_METRICS_DATASET"

	// SlowOperationThreshold retains a child span for work whose individual
	// timing is useful in a trace waterfall. Faster repeated work is recorded
	// as timing attributes on its parent span instead.
	SlowOperationThreshold = 25 * time.Millisecond

	// Error types are deliberately broad and low-cardinality. Combine them
	// with nwsl.error.code to answer both why an operation failed and where the
	// failure was detected.
	ErrorTypeCanceled           = "canceled"
	ErrorTypeTimeout            = "timeout"
	ErrorTypeInvalidArgument    = "invalid_argument"
	ErrorTypeInvalidData        = "invalid_data"
	ErrorTypeConflict           = "conflict"
	ErrorTypeUpstreamFailure    = "upstream_failure"
	ErrorTypeStorageFailure     = "storage_failure"
	ErrorTypeCalculationFailure = "calculation_failure"
	ErrorTypeOther              = "_OTHER"
)

type classifiedError struct {
	err       error
	errorType string
}

func (e classifiedError) Error() string     { return e.err.Error() }
func (e classifiedError) Unwrap() error     { return e.err }
func (e classifiedError) ErrorType() string { return e.errorType }

// Providers owns the configured SDK providers and flushes their batches during
// graceful process shutdown.
type Providers struct {
	traces  *trace.TracerProvider
	metrics *metric.MeterProvider
	logs    *sdklog.LoggerProvider
}

// Configure installs OpenTelemetry providers for the process. Supplying a
// HONEYCOMB_API_KEY exports traces and exception logs directly to Honeycomb.
// The standard OTLP endpoint variables are also supported for collector-based
// deployments.
//
// Metrics are opt-in through OTEL_METRICS_EXPORTER=otlp. The legacy
// HONEYCOMB_METRICS_DATASET variable is also accepted as an enablement signal,
// but is not sent as an OTLP header. When no trace or log exporter is
// configured, telemetry remains local no-op work so development does not need
// credentials or a collector.
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

	logs, logExporterName, err := newLoggerProvider(ctx, res)
	if err != nil {
		_ = traces.Shutdown(ctx)
		return nil, err
	}

	metrics, err := newMeterProvider(ctx, res)
	if err != nil {
		_ = logs.Shutdown(ctx)
		_ = traces.Shutdown(ctx)
		return nil, err
	}

	otel.SetTracerProvider(traces)
	global.SetLoggerProvider(logs)
	if metrics != nil {
		otel.SetMeterProvider(metrics)
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	if traceExporter == nil {
		logger.Info("OpenTelemetry trace export disabled; set HONEYCOMB_API_KEY or OTEL_EXPORTER_OTLP_ENDPOINT to enable it", "logs", logExporterName != "", "metrics", metrics != nil)
	} else {
		logger.Info("OpenTelemetry trace export enabled", "exporter", exporterName, "log_exporter", logExporterName, "service", serviceName, "metrics", metrics != nil)
	}
	return &Providers{traces: traces, metrics: metrics, logs: logs}, nil
}

// Shutdown flushes trace and metric batches. Call it with a fresh, bounded
// context because a process signal may have already canceled the application
// context.
func (p *Providers) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var errs []error
	if p.logs != nil {
		errs = append(errs, p.logs.Shutdown(ctx))
	}
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

// Logger returns the common application logger for manually emitted log records.
func Logger() otellog.Logger { return global.GetLoggerProvider().Logger(instrumentationName) }

// RecordError emits a correlated exception log record and marks span as failed.
// Prefer RecordErrorWithCode for a stable identifier of the failure site.
func RecordError(ctx context.Context, span oteltrace.Span, err error) {
	RecordErrorWithCode(ctx, span, err, "")
}

// MarkError marks a span as failed without emitting an exception log record.
// Use it when an error has propagated from a child operation that already
// recorded the exception. This keeps every failed span queryable without
// duplicating the exception detail in the trace.
func MarkError(span oteltrace.Span, err error) {
	markError(span, err, "")
}

// ClassifyError attaches a stable error.type to err while preserving its
// errors.Is/errors.As chain. Context cancellation and timeouts take precedence
// over the supplied fallback because they are more useful failure classes.
func ClassifyError(err error, fallback string) error {
	if err == nil {
		return nil
	}
	errorType := resolveErrorType(err, fallback)
	if typed, ok := err.(interface{ ErrorType() string }); ok && typed.ErrorType() == errorType {
		return err
	}
	return classifiedError{err: err, errorType: errorType}
}

// RecordErrorWithCode records an error without using its potentially
// high-cardinality message as a span dimension. Codes are stable identifiers
// (for example, "sync.fetch_asa") that make failures easy to group and connect
// back to a code path.
func RecordErrorWithCode(ctx context.Context, span oteltrace.Span, err error, code string) {
	recordErrorWithCode(ctx, span, err, code, time.Time{})
}

// RecordErrorWithType classifies err, records its exception, and returns the
// classified error for propagation to parent operations. It uses ERROR
// severity because the exception terminates the operation.
func RecordErrorWithType(ctx context.Context, span oteltrace.Span, err error, code, errorType string) error {
	return recordExceptionWithType(ctx, span, err, code, errorType, otellog.SeverityError)
}

// RecordWarningWithType classifies err, records its exception, and returns the
// classified error for propagation. It uses WARN severity because the caller
// handles the failure through retry, degradation, or a partial result.
func RecordWarningWithType(ctx context.Context, span oteltrace.Span, err error, code, errorType string) error {
	return recordExceptionWithType(ctx, span, err, code, errorType, otellog.SeverityWarn)
}

func recordExceptionWithType(ctx context.Context, span oteltrace.Span, err error, code, errorType string, severity otellog.Severity) error {
	err = ClassifyError(err, errorType)
	recordExceptionWithCode(ctx, span, err, code, time.Time{}, severity)
	return err
}

func recordErrorWithCode(ctx context.Context, span oteltrace.Span, err error, code string, timestamp time.Time) {
	recordExceptionWithCode(ctx, span, err, code, timestamp, otellog.SeverityError)
}

func recordExceptionWithCode(ctx context.Context, span oteltrace.Span, err error, code string, timestamp time.Time, severity otellog.Severity) {
	if err == nil {
		return
	}
	record := otellog.Record{}
	record.SetEventName(exceptionEventName(code))
	record.SetSeverity(exceptionSeverity(err, severity))
	record.SetBody(attribute.StringValue(err.Error()))
	record.SetErr(err)
	if !timestamp.IsZero() {
		record.SetTimestamp(timestamp)
	}
	attributes := []attribute.KeyValue{
		semconv.ExceptionTypeKey.String(resolveErrorType(err, "")),
		attribute.String("exception.stacktrace", string(debug.Stack())),
	}
	if code != "" {
		attributes = append(attributes, attribute.String("nwsl.error.code", code))
	}
	record.AddAttributes(attributes...)
	Logger().Emit(ctx, record)

	markError(span, err, code)
}

func exceptionEventName(code string) string {
	if code == "" {
		return "exception"
	}
	return "nwsl." + code + ".exception"
}

func exceptionSeverity(err error, severity otellog.Severity) otellog.Severity {
	// In this application context cancellation normally means a caller went
	// away or the process is shutting down, not that the operation needs
	// operator attention. DeadlineExceeded remains at the caller-selected
	// severity because exhausting an operation budget can be a real failure.
	if errors.Is(err, context.Canceled) {
		return otellog.SeverityDebug
	}
	return severity
}

func markError(span oteltrace.Span, err error, code string) {
	if err == nil {
		return
	}
	spanAttributes := []attribute.KeyValue{semconv.ErrorTypeKey.String(resolveErrorType(err, ""))}
	if code != "" {
		spanAttributes = append(spanAttributes, attribute.String("nwsl.error.code", code))
	}
	span.SetAttributes(spanAttributes...)
	span.SetStatus(codes.Error, err.Error())
}

func resolveErrorType(err error, fallback string) string {
	if err == nil {
		return ErrorTypeOther
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorTypeTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ErrorTypeCanceled
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return ErrorTypeTimeout
	}
	var typed interface{ ErrorType() string }
	if errors.As(err, &typed) {
		if value := strings.TrimSpace(typed.ErrorType()); value != "" {
			return value
		}
	}
	if fallback = strings.TrimSpace(fallback); fallback != "" {
		return fallback
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if value := resolveErrorType(child, ""); value != ErrorTypeOther {
				return value
			}
		}
	}
	value := semconv.ErrorType(err).Value.AsString()
	switch value {
	case "", "*errors.errorString", "*errors.joinError", "*fmt.wrapError":
		return ErrorTypeOther
	default:
		return value
	}
}

// RecordCompletedSpan creates a child span after an operation has completed.
// It is useful for exceptional or slow loop work: the ordinary fast path can
// remain a wide parent span, while the retained child keeps its real timing
// and trace placement.
func RecordCompletedSpan(ctx context.Context, name string, started, finished time.Time, attributes []attribute.KeyValue, err error, code string) {
	recordCompletedSpan(ctx, name, started, finished, attributes, err, code, otellog.SeverityError)
}

// RecordCompletedWarningSpan records completed work whose local operation
// failed but whose caller handles the failure through retry or degradation.
func RecordCompletedWarningSpan(ctx context.Context, name string, started, finished time.Time, attributes []attribute.KeyValue, err error, code string) {
	recordCompletedSpan(ctx, name, started, finished, attributes, err, code, otellog.SeverityWarn)
}

func recordCompletedSpan(ctx context.Context, name string, started, finished time.Time, attributes []attribute.KeyValue, err error, code string, severity otellog.Severity) {
	ctx, span := Tracer().Start(ctx, name,
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithTimestamp(started),
		oteltrace.WithAttributes(attributes...),
	)
	if err != nil {
		recordExceptionWithCode(ctx, span, err, code, finished, severity)
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

func newLoggerProvider(ctx context.Context, res *resource.Resource) (*sdklog.LoggerProvider, string, error) {
	exporter, exporterName, err := newLogExporter(ctx)
	if err != nil {
		return nil, "", err
	}
	options := []sdklog.LoggerProviderOption{sdklog.WithResource(res)}
	if exporter != nil {
		options = append(options, sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)))
	}
	return sdklog.NewLoggerProvider(options...), exporterName, nil
}

func newLogExporter(ctx context.Context) (*otlploghttp.Exporter, string, error) {
	if logsExportDisabled() {
		return nil, "", nil
	}
	if err := validateOTLPHTTPProtocol("logs"); err != nil {
		return nil, "", err
	}
	if apiKey := strings.TrimSpace(os.Getenv(honeycombAPIKeyEnv)); apiKey != "" {
		endpoint, err := honeycombSignalEndpoint("/v1/logs")
		if err != nil {
			return nil, "", err
		}
		exporter, err := otlploghttp.New(ctx,
			otlploghttp.WithEndpointURL(endpoint),
			otlploghttp.WithHeaders(map[string]string{"x-honeycomb-team": apiKey}),
		)
		if err != nil {
			return nil, "", fmt.Errorf("create Honeycomb log exporter: %w", err)
		}
		return exporter, "Honeycomb", nil
	}
	if !otlpLogsEndpointConfigured() {
		return nil, "", nil
	}
	exporter, err := otlploghttp.New(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("create OTLP log exporter: %w", err)
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

func logsExportDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("OTEL_LOGS_EXPORTER")), "none")
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

func otlpLogsEndpointConfigured() bool {
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" ||
		strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")) != ""
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
