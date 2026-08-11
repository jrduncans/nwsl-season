package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
)

func TestRunStopsWhenSourceScopeSeedingFails(t *testing.T) {
	originalEnsure := ensureSourceScopeRegistry
	ensureSourceScopeRegistry = func(context.Context, *cache.DB, string, string, time.Time) error {
		return errors.New("source scope registry unavailable")
	}
	t.Cleanup(func() { ensureSourceScopeRegistry = originalEnsure })
	err := run(context.Background(), config.Config{
		DBPath:     t.TempDir() + "/cache.sqlite",
		SyncSeason: "2026",
		SyncStage:  "Regular Season",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "seed source scope registry") {
		t.Fatalf("run error = %v, want source-scope seeding failure", err)
	}
}

func TestForecastInputsChanged(t *testing.T) {
	tests := []struct {
		name string
		run  cache.SyncRun
		want bool
	}{
		{name: "unchanged", run: cache.SyncRun{}, want: false},
		{name: "team inserted", run: cache.SyncRun{TeamsInserted: 1}, want: true},
		{name: "team updated", run: cache.SyncRun{TeamsUpdated: 1}, want: true},
		{name: "game inserted", run: cache.SyncRun{GamesInserted: 1}, want: true},
		{name: "game updated", run: cache.SyncRun{GamesUpdated: 1}, want: true},
		{name: "game deleted", run: cache.SyncRun{GamesDeleted: 1}, want: true},
		{name: "xg inserted", run: cache.SyncRun{XGRun: &cache.XGSyncRun{RowsInserted: 1}}, want: true},
		{name: "xg updated", run: cache.SyncRun{XGRun: &cache.XGSyncRun{RowsUpdated: 1}}, want: true},
		{name: "xg unchanged", run: cache.SyncRun{XGRun: &cache.XGSyncRun{RowsUnchanged: 1}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := forecastInputsChanged(test.run); got != test.want {
				t.Errorf("forecastInputsChanged(%+v) = %t, want %t", test.run, got, test.want)
			}
		})
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
