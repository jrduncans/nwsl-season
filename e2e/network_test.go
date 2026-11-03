//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Only application fixture servers belong here. Allowing all loopback ports
// would also expose unrelated local services to the test browser.
var browserOrigins sync.Map

func newBrowserServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	browserOrigins.Store(server.URL, struct{}{})
	t.Cleanup(func() {
		browserOrigins.Delete(server.URL)
		server.Close()
	})
	return server
}

// newBrowserProxy checks every HTTP request, including redirect destinations.
// HTTPS tunnels and WebSocket upgrades are denied. Club logos are fulfilled
// by the existing Playwright stub before they reach the proxy.
// This limits browser traffic; it is not an OS-level network sandbox.
func newBrowserProxy() (*httptest.Server, func()) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, allowed := browserOrigins.Load(r.URL.Scheme + "://" + r.URL.Host)
		if !allowed || r.URL.Scheme != "http" || r.URL.User != nil || r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" {
			http.Error(w, "E2E browser destination is not a registered fixture", http.StatusForbidden)
			return
		}
		out := r.Clone(r.Context())
		out.RequestURI = ""
		response, err := transport.RoundTrip(out)
		if err != nil {
			http.Error(w, "E2E fixture request failed", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	return proxy, func() {
		proxy.Close()
		transport.CloseIdleConnections()
	}
}

// These checks use raw contexts because the usual page helper intentionally
// fails on the HTTP and console errors produced by blocked requests.
func TestBrowserNetwork(t *testing.T) {
	t.Parallel()
	var unexpectedRequests atomic.Int64
	unregistered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		unexpectedRequests.Add(1)
		_, _ = io.WriteString(w, "unexpected access")
	}))
	t.Cleanup(unregistered.Close)
	fixture := newBrowserServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, unregistered.URL, http.StatusFound)
			return
		}
		if r.URL.Path == "/local-redirect" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "fixture")
	}))
	for _, tc := range []struct {
		name      string
		url       string
		status    int
		wantError bool
	}{
		{"fixture", fixture.URL, http.StatusOK, false},
		{"fixture redirect", fixture.URL + "/local-redirect", http.StatusOK, false},
		{"unregistered loopback", unregistered.URL, http.StatusForbidden, false},
		{"redirect to unregistered loopback", fixture.URL + "/redirect", http.StatusForbidden, false},
		{"public hostname", "http://example.invalid/", http.StatusForbidden, false},
		{"HTTPS tunnel", strings.Replace(unregistered.URL, "http:", "https:", 1), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			page, err := browser.NewPage()
			if err != nil {
				t.Fatalf("new page: %v", err)
			}
			t.Cleanup(func() { _ = page.Context().Close() })
			response, err := page.Goto(tc.url)
			if tc.wantError {
				if err == nil {
					t.Error("navigation unexpectedly succeeded")
				}
				return
			}
			if err != nil {
				t.Fatalf("navigate: %v", err)
			}
			if response.Status() != tc.status {
				t.Errorf("status = %d, want %d", response.Status(), tc.status)
			}
		})
	}
	t.Cleanup(func() {
		if got := unexpectedRequests.Load(); got != 0 {
			t.Errorf("unregistered server received %d requests", got)
		}
	})
}

// Block capabilities that otherwise bypass ordinary request interception.
func TestBrowserBlockedCapabilities(t *testing.T) {
	t.Parallel()
	var workerRequests, socketRequests atomic.Int64
	fixture := newBrowserServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Upgrade") != "":
			socketRequests.Add(1)
			http.Error(w, "unexpected WebSocket", http.StatusBadRequest)
		case r.URL.Path == "/worker.js":
			workerRequests.Add(1)
			w.Header().Set("Content-Type", "application/javascript")
			_, _ = io.WriteString(w, "self.addEventListener('fetch', () => {});")
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<!doctype html><title>Browser guards</title>")
		}
	}))
	page := newPage(t, Desktop)
	if _, err := page.Goto(fixture.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	blocked, err := page.Evaluate(`async () => {
		try {
			const registration = await navigator.serviceWorker.register('/worker.js');
			return !registration;
		} catch {
			return true;
		}
	}`)
	if err != nil || blocked != true {
		t.Fatalf("service worker registration: blocked=%v, err=%v", blocked, err)
	}
	// Use a separate raw context for the expected WebSocket console error.
	socketPage, err := browser.NewPage()
	if err != nil {
		t.Fatalf("new socket page: %v", err)
	}
	t.Cleanup(func() { _ = socketPage.Context().Close() })
	if _, err := socketPage.Goto(fixture.URL); err != nil {
		t.Fatalf("navigate socket page: %v", err)
	}
	blocked, err = socketPage.Evaluate(`url => new Promise(resolve => {
		const socket = new WebSocket(url);
		socket.onopen = () => { socket.close(); resolve(false); };
		socket.onerror = () => resolve(true);
	})`, strings.Replace(fixture.URL, "http:", "ws:", 1))
	if err != nil || blocked != true {
		t.Fatalf("WebSocket: blocked=%v, err=%v", blocked, err)
	}
	if got := workerRequests.Load(); got != 0 {
		t.Errorf("service worker script received %d requests", got)
	}
	if got := socketRequests.Load(); got != 0 {
		t.Errorf("fixture received %d WebSocket upgrade requests", got)
	}
}
