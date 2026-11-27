package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// serveTraced serves one request through otelhttp, as production does, and
// returns the response with the request's server span.
func serveTraced(t *testing.T, handler http.Handler, target string) (*httptest.ResponseRecorder, tracetest.SpanStub) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	response := httptest.NewRecorder()
	otelhttp.NewHandler(handler, "HTTP server", otelhttp.WithTracerProvider(provider)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	return response, spans[0]
}

// requireServerErrorSpan checks the classification a failed request keeps.
// otelhttp sets the status again after the handler returns, which clears the
// description, so the message lives on the correlated exception log.
func requireServerErrorSpan(t *testing.T, span tracetest.SpanStub, code, errorType string) {
	t.Helper()
	if span.Status.Code != codes.Error {
		t.Errorf("span status = %v, want error", span.Status.Code)
	}
	attributes := spanAttributes(span)
	if got := attributes["nwsl.error.code"].AsString(); got != code {
		t.Errorf("nwsl.error.code = %q, want %q", got, code)
	}
	if got := attributes["error.type"].AsString(); got != errorType {
		t.Errorf("error.type = %q, want %q", got, errorType)
	}
}

func TestSeasonPageServerErrorRecordsItsCause(t *testing.T) {
	handler := NewHandlerWithOptions(fakeStore{}, Options{CurrentSeason: "2026", Location: time.UTC})
	response, span := serveTraced(t, handler, "/seasons/2026/regular-season")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	requireServerErrorSpan(t, span, nwslconv.ErrorCodeHTTPPageLoad, telemetry.ErrorTypeInvalidData)
}

func TestUncatalogedSeasonNotFoundIsNotASpanError(t *testing.T) {
	handler := NewHandlerWithOptions(fakeStore{}, Options{CurrentSeason: "2026", Location: time.UTC})
	response, span := serveTraced(t, handler, "/seasons/2026../regular-season")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
	if span.Status.Code == codes.Error {
		t.Errorf("span status = error %q, want unset for a 404", span.Status.Description)
	}
	if _, found := spanAttributes(span)["nwsl.error.code"]; found {
		t.Error("404 span carries nwsl.error.code")
	}
}

func TestUnexplainedServerErrorIsRecordedBySafetyNet(t *testing.T) {
	handler := withServerErrorTelemetry(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	response, span := serveTraced(t, handler, "/anything")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", response.Code)
	}
	requireServerErrorSpan(t, span, nwslconv.ErrorCodeHTTPUnrecordedServerError, telemetry.ErrorTypeOther)
}

func TestSafetyNetKeepsTheHandlersRecordedCause(t *testing.T) {
	handler := withServerErrorTelemetry(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordPageLoadError(r, context.DeadlineExceeded, telemetry.ErrorTypeOther)
		http.Error(w, "slow", http.StatusServiceUnavailable)
	}))
	_, span := serveTraced(t, handler, "/anything")
	requireServerErrorSpan(t, span, nwslconv.ErrorCodeHTTPPageLoad, telemetry.ErrorTypeTimeout)
}

func TestSafetyNetRecordsPanicsAndRepanics(t *testing.T) {
	handler := withServerErrorTelemetry(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("broken handler")
	}))
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed, want it re-raised for net/http")
			}
		}()
		otelhttp.NewHandler(handler, "HTTP server", otelhttp.WithTracerProvider(provider)).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/anything", nil))
	}()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	requireServerErrorSpan(t, spans[0], nwslconv.ErrorCodeHTTPUnrecordedServerError, telemetry.ErrorTypeOther)
}

func TestSuccessfulResponseIsNotRecorded(t *testing.T) {
	handler := withServerErrorTelemetry(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	_, span := serveTraced(t, handler, "/anything")
	if span.Status.Code == codes.Error {
		t.Errorf("span status = error, want unset")
	}
}
