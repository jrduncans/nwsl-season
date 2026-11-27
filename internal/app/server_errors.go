package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/jrduncans/nwsl-season/internal/telemetry"
	"github.com/jrduncans/nwsl-season/internal/telemetry/nwslconv"
	"go.opentelemetry.io/otel/trace"
)

// A server-error response must say why on its request span. Handlers that
// know the cause call recordPageLoadError or recordPageRenderError, or
// markServerError when a child span already logged the exception.
// withServerErrorTelemetry is the safety net: any 5xx or panic left
// unexplained is recorded as http.unrecorded_server_error, so a failed
// request is never silent.

type serverErrorStateKey struct{}

type serverErrorState struct {
	recorded atomic.Bool
}

// recordPageLoadError emits the correlated exception log for a failure to
// load or compute a response's data and marks the request span failed.
// errorType is the fallback classification when err carries none of its own.
func recordPageLoadError(r *http.Request, err error, errorType string) {
	ctx := r.Context()
	_ = telemetry.RecordErrorWithType(ctx, trace.SpanFromContext(ctx), err, nwslconv.ErrorCodeHTTPPageLoad, errorType)
	markServerErrorRecorded(ctx)
}

// recordPageRenderError does the same for a failed template execution.
func recordPageRenderError(r *http.Request, err error) {
	ctx := r.Context()
	_ = telemetry.RecordErrorWithType(ctx, trace.SpanFromContext(ctx), err, nwslconv.ErrorCodeHTTPPageRender, telemetry.ErrorTypeOther)
	markServerErrorRecorded(ctx)
}

// markServerError marks the request span failed for a cause a child span
// normally records already; telemetry logs it here only if none did.
func markServerError(r *http.Request, err error, errorType string) {
	ctx := r.Context()
	_ = telemetry.MarkError(trace.SpanFromContext(ctx), telemetry.ClassifyError(err, errorType))
	markServerErrorRecorded(ctx)
}

func markServerErrorRecorded(ctx context.Context) {
	if state, ok := ctx.Value(serverErrorStateKey{}).(*serverErrorState); ok {
		state.recorded.Store(true)
	}
}

func withServerErrorTelemetry(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := &serverErrorState{}
		ctx := context.WithValue(r.Context(), serverErrorStateKey{}, state)
		writer := &serverErrorWriter{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				// ErrAbortHandler is the deliberate way to abort a response.
				if abort, ok := recovered.(error); (!ok || !errors.Is(abort, http.ErrAbortHandler)) && !state.recorded.Load() {
					recordUnexplained(ctx, fmt.Errorf("handler panicked: %v", recovered))
				}
				panic(recovered)
			}
			if writer.status >= http.StatusInternalServerError && !state.recorded.Load() {
				recordUnexplained(ctx, fmt.Errorf("handler returned HTTP %d without recording a cause", writer.status))
			}
		}()
		next.ServeHTTP(writer, r.WithContext(ctx))
	})
}

func recordUnexplained(ctx context.Context, err error) {
	_ = telemetry.RecordErrorWithType(ctx, trace.SpanFromContext(ctx), err, nwslconv.ErrorCodeHTTPUnrecordedServerError, telemetry.ErrorTypeOther)
}

type serverErrorWriter struct {
	http.ResponseWriter
	status int
}

func (w *serverErrorWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *serverErrorWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *serverErrorWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
