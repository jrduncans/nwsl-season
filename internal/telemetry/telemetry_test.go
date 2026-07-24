package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
	t.Setenv(honeycombMetricsDatasetEnv, "")
	t.Setenv("OTEL_SERVICE_NAME", "telemetry-test")
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
