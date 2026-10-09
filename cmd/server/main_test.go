package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/server"
)

func TestRunReportsServerBuildFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), config.Config{
		DBPath:     filepath.Join(blocker, "cache.sqlite"),
		SyncSeason: "2026",
		SyncStage:  "Regular Season",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "open cache database") {
		t.Fatalf("run error = %v, want cache open failure", err)
	}
}

// lingeringASATransport stands in for ASA. Each request blocks until it is
// canceled and then takes a while longer to unwind, so a caller that returns
// without waiting for the scheduler is observable.
type lingeringASATransport struct {
	requested chan struct{}
	once      sync.Once
	requests  atomic.Int64
	inFlight  atomic.Int64
	linger    time.Duration
}

func (t *lingeringASATransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	t.inFlight.Add(1)
	defer t.inFlight.Add(-1)
	t.once.Do(func() { close(t.requested) })
	<-request.Context().Done()
	time.Sleep(t.linger)
	return nil, request.Context().Err()
}

// TestRunStartsSchedulerAndShutsDownOnCancel drives run end to end: the
// scheduler must start (its first ASA request arrives), and canceling the
// context must stop the scheduler, wait for its in-flight request to unwind,
// shut down the listener, and return without error.
func TestRunStartsSchedulerAndShutsDownOnCancel(t *testing.T) {
	transport := &lingeringASATransport{requested: make(chan struct{}), linger: 300 * time.Millisecond}
	originalOptions := serverOptions
	serverOptions = func(logger *slog.Logger) server.Options {
		options := originalOptions(logger)
		options.ASAHTTPClient = &http.Client{Transport: transport}
		return options
	}
	t.Cleanup(func() { serverOptions = originalOptions })

	t.Setenv("NWSL_DATA_DIR", t.TempDir())
	// The transport never dials; this address only has to be valid.
	t.Setenv("NWSL_ASA_BASE_URL", "http://127.0.0.1:1/api/v1")
	t.Setenv("NWSL_HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("NWSL_SYNC_TIMEOUT", "1m")
	cfg, err := config.FromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	select {
	case <-transport.requested:
	case err := <-done:
		t.Fatalf("run returned before the scheduler requested ASA: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler made no ASA request after run started")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run error = %v, want clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after its context was canceled")
	}
	if inFlight := transport.inFlight.Load(); inFlight != 0 {
		t.Fatalf("ASA requests still in flight when run returned = %d, want run to wait for the scheduler", inFlight)
	}
	if transport.requests.Load() == 0 {
		t.Fatal("transport saw no ASA requests")
	}
}

func TestNewHTTPServerAppliesConnectionLimits(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	server := newHTTPServer("127.0.0.1:8080", handler, 40*time.Second)

	if server.Addr != "127.0.0.1:8080" || server.Handler == nil {
		t.Fatalf("server = %+v, want configured address and handler", server)
	}
	if server.ReadHeaderTimeout != serverReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, serverReadHeaderTimeout)
	}
	if server.WriteTimeout != 45*time.Second {
		t.Errorf("WriteTimeout = %s, want 45s", server.WriteTimeout)
	}
	if server.IdleTimeout != serverIdleTimeout {
		t.Errorf("IdleTimeout = %s, want %s", server.IdleTimeout, serverIdleTimeout)
	}
	if server.MaxHeaderBytes != serverMaxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, serverMaxHeaderBytes)
	}
}

func TestHTTPServerTerminatesSlowHeader(t *testing.T) {
	server := newHTTPServer("", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("handler must not run for an incomplete request header")
	}), time.Second)
	server.ReadHeaderTimeout = 50 * time.Millisecond
	listener := startHTTPServer(t, server)

	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: example.test\r\n"); err != nil {
		t.Fatalf("write incomplete header: %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	response, err := io.ReadAll(connection)
	if err != nil {
		t.Fatalf("read timed-out response: %v", err)
	}
	if len(response) > 0 && !strings.Contains(string(response), "408 Request Timeout") {
		t.Fatalf("response = %q, want connection close or 408 timeout", response)
	}
}

func TestShutdownHTTPServerWaitsForActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := newHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}), time.Second)
	listener := startHTTPServer(t, server)

	requestDone := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			err = response.Body.Close()
		}
		requestDone <- err
	}()
	<-started

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- shutdownHTTPServer(server) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before active request completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdown server: %v", err)
	}
	if err := <-requestDone; err != nil {
		t.Fatalf("request: %v", err)
	}
}

func startHTTPServer(t *testing.T, server *http.Server) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		if err := <-serverDone; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("server stopped with %v, want %v", err, http.ErrServerClosed)
		}
	})
	return listener
}
