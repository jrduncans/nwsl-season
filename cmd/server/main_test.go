package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
