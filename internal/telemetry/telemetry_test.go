package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type inMemoryLogExporter struct {
	records []sdklog.Record
}

type concreteTestError struct{}

func (concreteTestError) Error() string { return "concrete failure" }

func (e *inMemoryLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.records = append(e.records, records...)
	return nil
}

func (*inMemoryLogExporter) Shutdown(context.Context) error   { return nil }
func (*inMemoryLogExporter) ForceFlush(context.Context) error { return nil }

func TestHoneycombSignalEndpoint(t *testing.T) {
	tests := []struct {
		name, configured, signal, want string
	}{
		{"default traces", "", "/v1/traces", "https://api.honeycomb.io/v1/traces"},
		{"EU endpoint", "https://api.eu1.honeycomb.io", "/v1/metrics", "https://api.eu1.honeycomb.io/v1/metrics"},
		{"endpoint with path", "https://collector.example.test/ingest", "/v1/traces", "https://collector.example.test/ingest/v1/traces"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(honeycombAPIEndpointEnv, test.configured)
			got, err := honeycombSignalEndpoint(test.signal)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Errorf("honeycombSignalEndpoint() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHoneycombSignalEndpointRejectsInvalidValue(t *testing.T) {
	for _, configured := range []string{"api.honeycomb.io", "https://api.honeycomb.io?token=secret", "https://api.honeycomb.io#fragment"} {
		t.Run(configured, func(t *testing.T) {
			t.Setenv(honeycombAPIEndpointEnv, configured)
			if _, err := honeycombSignalEndpoint("/v1/traces"); err == nil {
				t.Fatal("honeycombSignalEndpoint() error = nil, want error")
			}
		})
	}
}

func TestConfigureExportsHoneycombTrace(t *testing.T) {
	received := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		received <- request
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	t.Setenv(honeycombAPIKeyEnv, "test-ingest-key")
	t.Setenv(honeycombAPIEndpointEnv, server.URL)
	t.Setenv(legacyMetricsDatasetEnv, "")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_SERVICE_NAME", "telemetry-test")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	providers, err := Configure(context.Background(), nil, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	_, span := Tracer().Start(context.Background(), "test.operation")
	span.End()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := providers.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}

	select {
	case request := <-received:
		if request.URL.Path != "/v1/traces" {
			t.Errorf("request path = %q, want /v1/traces", request.URL.Path)
		}
		if got := request.Header.Get("x-honeycomb-team"); got != "test-ingest-key" {
			t.Errorf("x-honeycomb-team = %q, want test ingest key", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Honeycomb trace export was not received")
	}
}

func TestConfigureExportsNativeHoneycombMetrics(t *testing.T) {
	received := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		received <- request
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	t.Setenv(honeycombAPIKeyEnv, "test-ingest-key")
	t.Setenv(honeycombAPIEndpointEnv, server.URL)
	t.Setenv(legacyMetricsDatasetEnv, "")
	t.Setenv("OTEL_METRICS_EXPORTER", "otlp")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	providers, err := Configure(context.Background(), nil, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	counter, err := Meter().Int64Counter("test.metric")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 1)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := providers.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}

	select {
	case request := <-received:
		if request.URL.Path != "/v1/metrics" {
			t.Errorf("request path = %q, want /v1/metrics", request.URL.Path)
		}
		if got := request.Header.Get("x-honeycomb-team"); got != "test-ingest-key" {
			t.Errorf("x-honeycomb-team = %q, want test ingest key", got)
		}
		if got := request.Header.Get("x-honeycomb-dataset"); got != "" {
			t.Errorf("x-honeycomb-dataset = %q, want no legacy dataset header", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Honeycomb metric export was not received")
	}
}

func TestConfigureExportsHoneycombLogs(t *testing.T) {
	received := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		received <- request
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	t.Setenv(honeycombAPIKeyEnv, "test-ingest-key")
	t.Setenv(honeycombAPIEndpointEnv, server.URL)
	t.Setenv(legacyMetricsDatasetEnv, "")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	providers, err := Configure(context.Background(), nil, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := Tracer().Start(context.Background(), "test.operation")
	RecordErrorWithCode(ctx, span, errors.New("test failure"), "test.operation")
	span.End()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := providers.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}

	select {
	case request := <-received:
		if request.URL.Path != "/v1/logs" {
			t.Errorf("request path = %q, want /v1/logs", request.URL.Path)
		}
		if got := request.Header.Get("x-honeycomb-team"); got != "test-ingest-key" {
			t.Errorf("x-honeycomb-team = %q, want test ingest key", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Honeycomb log export was not received")
	}
}

func TestRecordErrorWithCode(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	defer func() { _ = traceProvider.Shutdown(context.Background()) }()
	logExporter := &inMemoryLogExporter{}
	logProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
	previousLogProvider := global.GetLoggerProvider()
	global.SetLoggerProvider(logProvider)
	t.Cleanup(func() {
		global.SetLoggerProvider(previousLogProvider)
		_ = logProvider.Shutdown(context.Background())
	})

	ctx, span := traceProvider.Tracer("test").Start(context.Background(), "test.operation")
	RecordErrorWithCode(ctx, span, errors.New("test failure"), nwslconv.ErrorCodeCacheSeasonLoad)
	span.End()

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	attributes := map[attribute.Key]attribute.Value{}
	for _, value := range spans[0].Attributes {
		attributes[value.Key] = value.Value
	}
	if _, found := attributes["error"]; found {
		t.Error("non-standard error attribute was recorded")
	}
	if got := attributes["nwsl.error.code"].AsString(); got != nwslconv.ErrorCodeCacheSeasonLoad {
		t.Errorf("nwsl.error.code = %q, want %q", got, nwslconv.ErrorCodeCacheSeasonLoad)
	}
	if got := attributes["error.type"].AsString(); got != ErrorTypeOther {
		t.Errorf("error.type = %q, want %q", got, ErrorTypeOther)
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v, want error", spans[0].Status.Code)
	}
	if got := spans[0].Status.Description; got != "test failure" {
		t.Errorf("span status description = %q, want test failure", got)
	}
	if len(logExporter.records) != 1 {
		t.Fatalf("exported log records = %d, want 1", len(logExporter.records))
	}
	record := logExporter.records[0]
	if got := record.EventName(); got != nwslconv.EventCacheSeasonLoadException {
		t.Errorf("event name = %q, want %q", got, nwslconv.EventCacheSeasonLoadException)
	}
	if got := record.Severity(); got != otellog.SeverityError {
		t.Errorf("severity = %v, want ERROR", got)
	}
	if got := record.SeverityText(); got != "" {
		t.Errorf("severity text = %q, want empty native-event value", got)
	}
	if record.TraceID() != span.SpanContext().TraceID() || record.SpanID() != span.SpanContext().SpanID() {
		t.Error("log record is not correlated with its span")
	}
	logAttributes := map[string]string{}
	record.WalkAttributes(func(value attribute.KeyValue) bool {
		logAttributes[string(value.Key)] = value.Value.AsString()
		return true
	})
	if got := logAttributes["exception.message"]; got != "test failure" {
		t.Errorf("exception.message = %q, want test failure", got)
	}
	if got := logAttributes["exception.type"]; got != ErrorTypeOther {
		t.Errorf("exception.type = %q, want %q", got, ErrorTypeOther)
	}
	if got := logAttributes["exception.stacktrace"]; got == "" {
		t.Error("exception.stacktrace is empty")
	}
	if got := logAttributes["nwsl.error.code"]; got != nwslconv.ErrorCodeCacheSeasonLoad {
		t.Errorf("nwsl.error.code = %q, want %q", got, nwslconv.ErrorCodeCacheSeasonLoad)
	}
}

func TestRecordErrorWithUnknownCodeUsesGenericEvent(t *testing.T) {
	logExporter := &inMemoryLogExporter{}
	logProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
	previousLogProvider := global.GetLoggerProvider()
	global.SetLoggerProvider(logProvider)
	t.Cleanup(func() {
		global.SetLoggerProvider(previousLogProvider)
		_ = logProvider.Shutdown(context.Background())
	})

	RecordErrorWithCode(context.Background(), oteltrace.SpanFromContext(context.Background()), errors.New("test failure"), "uncataloged.operation")
	if len(logExporter.records) != 1 {
		t.Fatalf("exported log records = %d, want 1", len(logExporter.records))
	}
	record := logExporter.records[0]
	if got := record.EventName(); got != nwslconv.EventException {
		t.Errorf("event name = %q, want %q", got, nwslconv.EventException)
	}
	record.WalkAttributes(func(value attribute.KeyValue) bool {
		if value.Key == nwslconv.ErrorCodeKey {
			t.Errorf("unexpected nwsl.error.code = %q", value.Value.AsString())
		}
		return true
	})
}

func TestExceptionSeverityReflectsDisposition(t *testing.T) {
	tests := []struct {
		name string
		err  error
		emit func(context.Context, oteltrace.Span, error) error
		want otellog.Severity
	}{
		{
			name: "terminal operation failure",
			err:  errors.New("terminal failure"),
			emit: func(ctx context.Context, span oteltrace.Span, err error) error {
				return RecordErrorWithType(ctx, span, err, "test.terminal", ErrorTypeCalculationFailure)
			},
			want: otellog.SeverityError,
		},
		{
			name: "handled partial failure",
			err:  context.DeadlineExceeded,
			emit: func(ctx context.Context, span oteltrace.Span, err error) error {
				return RecordWarningWithType(ctx, span, err, "test.partial", ErrorTypeCalculationFailure)
			},
			want: otellog.SeverityWarn,
		},
		{
			name: "normal cancellation",
			err:  context.Canceled,
			emit: func(ctx context.Context, span oteltrace.Span, err error) error {
				return RecordErrorWithType(ctx, span, err, "test.canceled", ErrorTypeCalculationFailure)
			},
			want: otellog.SeverityDebug,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			traceProvider := sdktrace.NewTracerProvider()
			defer func() { _ = traceProvider.Shutdown(context.Background()) }()
			logExporter := &inMemoryLogExporter{}
			logProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
			previousLogProvider := global.GetLoggerProvider()
			global.SetLoggerProvider(logProvider)
			t.Cleanup(func() {
				global.SetLoggerProvider(previousLogProvider)
				_ = logProvider.Shutdown(context.Background())
			})

			ctx, span := traceProvider.Tracer("test").Start(context.Background(), "test.operation")
			if got := test.emit(ctx, span, test.err); !errors.Is(got, test.err) {
				t.Fatalf("returned error = %v, want wrapping %v", got, test.err)
			}
			span.End()
			if len(logExporter.records) != 1 {
				t.Fatalf("exported log records = %d, want 1", len(logExporter.records))
			}
			if got := logExporter.records[0].Severity(); got != test.want {
				t.Errorf("severity = %v, want %v", got, test.want)
			}
			if got := logExporter.records[0].SeverityText(); got != "" {
				t.Errorf("severity text = %q, want empty native-event value", got)
			}
		})
	}
}

func TestRecordErrorWithTypeAlignsWrappedJoinedErrorAcrossSignals(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	defer func() { _ = traceProvider.Shutdown(context.Background()) }()
	logExporter := &inMemoryLogExporter{}
	logProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
	previousLogProvider := global.GetLoggerProvider()
	global.SetLoggerProvider(logProvider)
	t.Cleanup(func() {
		global.SetLoggerProvider(previousLogProvider)
		_ = logProvider.Shutdown(context.Background())
	})

	parentCtx, parent := traceProvider.Tracer("test").Start(context.Background(), "parent.operation")
	childCtx, child := traceProvider.Tracer("test").Start(parentCtx, "child.operation")
	cause := errors.Join(
		fmt.Errorf("fetch teams: %w", context.DeadlineExceeded),
		errors.New("secondary failure"),
	)
	recorded := RecordErrorWithType(childCtx, child, cause, "sync.fetch_asa", ErrorTypeUpstreamFailure)
	child.End()
	parentErr := fmt.Errorf("sync failed: %w", recorded)
	MarkError(parent, parentErr)
	parent.End()

	if !errors.Is(recorded, context.DeadlineExceeded) {
		t.Error("classified error no longer matches context deadline")
	}
	spans := make(map[string]tracetest.SpanStub)
	for _, span := range traceExporter.GetSpans() {
		spans[span.Name] = span
	}
	for name, wantDescription := range map[string]string{
		"child.operation":  cause.Error(),
		"parent.operation": parentErr.Error(),
	} {
		span, ok := spans[name]
		if !ok {
			t.Fatalf("span %q was not exported", name)
		}
		attributes := map[attribute.Key]attribute.Value{}
		for _, value := range span.Attributes {
			attributes[value.Key] = value.Value
		}
		if got := attributes["error.type"].AsString(); got != ErrorTypeTimeout {
			t.Errorf("%s error.type = %q, want %q", name, got, ErrorTypeTimeout)
		}
		if got := span.Status.Description; got != wantDescription {
			t.Errorf("%s status description = %q, want %q", name, got, wantDescription)
		}
	}
	if len(logExporter.records) != 1 {
		t.Fatalf("exported log records = %d, want 1", len(logExporter.records))
	}
	logAttributes := map[string]string{}
	logExporter.records[0].WalkAttributes(func(value attribute.KeyValue) bool {
		logAttributes[string(value.Key)] = value.Value.AsString()
		return true
	})
	if got := logAttributes["exception.type"]; got != ErrorTypeTimeout {
		t.Errorf("exception.type = %q, want %q", got, ErrorTypeTimeout)
	}
	if got := logAttributes["exception.message"]; got != cause.Error() {
		t.Errorf("exception.message = %q, want %q", got, cause.Error())
	}
}

func TestResolveErrorTypeFindsMeaningfulJoinedChild(t *testing.T) {
	want := resolveErrorType(concreteTestError{}, "")
	got := resolveErrorType(errors.Join(errors.New("generic failure"), concreteTestError{}), "")
	if got != want {
		t.Errorf("resolveErrorType(joined error) = %q, want %q", got, want)
	}
}

func TestMarkErrorDoesNotEmitExceptionLog(t *testing.T) {
	traceExporter := tracetest.NewInMemoryExporter()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
	defer func() { _ = traceProvider.Shutdown(context.Background()) }()
	logExporter := &inMemoryLogExporter{}
	logProvider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logExporter)))
	previousLogProvider := global.GetLoggerProvider()
	global.SetLoggerProvider(logProvider)
	t.Cleanup(func() {
		global.SetLoggerProvider(previousLogProvider)
		_ = logProvider.Shutdown(context.Background())
	})

	_, span := traceProvider.Tracer("test").Start(context.Background(), "parent.operation")
	MarkError(span, errors.New("child operation failed"))
	span.End()

	spans := traceExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	attributes := map[attribute.Key]attribute.Value{}
	for _, value := range spans[0].Attributes {
		attributes[value.Key] = value.Value
	}
	if got := attributes["error.type"].AsString(); got == "" {
		t.Error("error.type is empty")
	}
	if _, found := attributes["nwsl.error.code"]; found {
		t.Error("mark-only span has nwsl.error.code")
	}
	if spans[0].Status.Code != codes.Error {
		t.Errorf("span status = %v, want error", spans[0].Status.Code)
	}
	if len(logExporter.records) != 0 {
		t.Errorf("exported log records = %d, want 0", len(logExporter.records))
	}
}

func TestRecordCompletedSpanPreservesOperationTiming(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	started := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	finished := started.Add(37 * time.Millisecond)
	ctx, parent := provider.Tracer("test").Start(context.Background(), "parent")
	RecordCompletedSpan(ctx, "slow.work", started, finished, []attribute.KeyValue{attribute.String("nwsl.work.id", "work-123")}, nil, "")
	parent.End()

	for _, span := range exporter.GetSpans() {
		if span.Name != "slow.work" {
			continue
		}
		if !span.StartTime.Equal(started) || !span.EndTime.Equal(finished) {
			t.Errorf("span timing = %s-%s, want %s-%s", span.StartTime, span.EndTime, started, finished)
		}
		attributes := map[attribute.Key]attribute.Value{}
		for _, value := range span.Attributes {
			attributes[value.Key] = value.Value
		}
		if got := attributes["nwsl.work.id"].AsString(); got != "work-123" {
			t.Errorf("nwsl.work.id = %q, want work-123", got)
		}
		return
	}
	t.Fatal("completed span was not exported")
}
