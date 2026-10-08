package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/scheduler"
)

// countingASA is a stand-in ASA that records every request and tracks
// requests still being served, so tests can show both that traffic reached
// only this server and that callers waited for it.
type countingASA struct {
	server   *httptest.Server
	total    atomic.Int64
	inFlight atomic.Int64
	started  chan struct{}
	once     sync.Once
	// block holds each request until its context ends, simulating a slow ASA.
	block bool
	delay time.Duration
}

func newCountingASA(t *testing.T, block bool, delay time.Duration) *countingASA {
	t.Helper()
	fake := &countingASA{started: make(chan struct{}), block: block, delay: delay}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.inFlight.Add(1)
		defer fake.inFlight.Add(-1)
		fake.total.Add(1)
		fake.once.Do(func() { close(fake.started) })
		if fake.block {
			<-r.Context().Done()
			return
		}
		if fake.delay > 0 {
			time.Sleep(fake.delay)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "[]")
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// hostRecordingTransport records the host of every outgoing ASA request and
// refuses any host other than the fake, so a stray real-ASA request fails the
// test instead of reaching the network.
type hostRecordingTransport struct {
	allowed string
	mu      sync.Mutex
	hosts   []string
}

func (t *hostRecordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.hosts = append(t.hosts, request.URL.Host)
	t.mu.Unlock()
	if request.URL.Host != t.allowed {
		return nil, errors.New("unexpected ASA host " + request.URL.Host)
	}
	return http.DefaultTransport.RoundTrip(request)
}

func (t *hostRecordingTransport) recorded() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.hosts...)
}

func testConfig(t *testing.T, asaBaseURL string) config.Config {
	t.Helper()
	t.Setenv("NWSL_DATA_DIR", t.TempDir())
	t.Setenv("NWSL_ASA_BASE_URL", asaBaseURL)
	t.Setenv("NWSL_SYNC_SEASON", "2026")
	t.Setenv("NWSL_SYNC_STAGE", "Regular Season")
	t.Setenv("NWSL_SYNC_TIMEOUT", "10s")
	cfg, err := config.FromEnvironment()
	if err != nil {
		t.Fatalf("config.FromEnvironment: %v", err)
	}
	return cfg
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func buildTestServer(t *testing.T, cfg config.Config, opts Options) *Server {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = quietLogger()
	}
	srv, err := Build(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		srv.Wait()
		if err := srv.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return srv
}

func waitWithTimeout(t *testing.T, what string, wait func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not return", what)
	}
}

func TestBuildWithoutSchedulerServesHealthzWithoutASARequests(t *testing.T) {
	fake := newCountingASA(t, false, 0)
	srv := buildTestServer(t, testConfig(t, fake.server.URL), Options{StartScheduler: false})
	srv.Start()

	for _, path := range []string{"/healthz", "/"} {
		recorder := httptest.NewRecorder()
		srv.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if path == "/healthz" && recorder.Code != http.StatusOK {
			t.Fatalf("GET /healthz = %d, want 200", recorder.Code)
		}
		if recorder.Code >= http.StatusInternalServerError {
			t.Fatalf("GET %s = %d, want a cache-only page response", path, recorder.Code)
		}
	}
	// Give a wrongly started scheduler time to issue its startup tick.
	time.Sleep(100 * time.Millisecond)
	if got := fake.total.Load(); got != 0 {
		t.Fatalf("ASA requests = %d, want none without the scheduler", got)
	}
}

func TestCheckNowRequestsOnlyConfiguredASAAndWaitsForThem(t *testing.T) {
	fake := newCountingASA(t, false, 20*time.Millisecond)
	fakeURL, err := url.Parse(fake.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &hostRecordingTransport{allowed: fakeURL.Host}
	// The configured base URL is the real ASA default; the option override is
	// what must take effect.
	cfg := testConfig(t, "https://app.americansocceranalysis.com/api/v1")
	srv := buildTestServer(t, cfg, Options{
		ASABaseURL:    fake.server.URL,
		ASAHTTPClient: &http.Client{Transport: transport, Timeout: 10 * time.Second},
	})
	if got := fake.total.Load(); got != 0 {
		t.Fatalf("ASA requests after Build = %d, want none", got)
	}

	if err := srv.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}

	if fake.total.Load() == 0 {
		t.Fatal("CheckNow made no ASA requests, want at least one due job")
	}
	if inFlight := fake.inFlight.Load(); inFlight != 0 {
		t.Fatalf("ASA requests still in flight after CheckNow = %d, want 0", inFlight)
	}
	hosts := transport.recorded()
	if len(hosts) != int(fake.total.Load()) {
		t.Fatalf("transport saw %d requests, fake served %d", len(hosts), fake.total.Load())
	}
	for _, host := range hosts {
		if host != fakeURL.Host {
			t.Fatalf("ASA request host = %q, want only %q", host, fakeURL.Host)
		}
	}
}

func TestBuildUsesConfiguredASABaseURL(t *testing.T) {
	fake := newCountingASA(t, false, 0)
	srv := buildTestServer(t, testConfig(t, fake.server.URL), Options{})
	_ = srv.CheckNow(context.Background())
	if fake.total.Load() == 0 {
		t.Fatal("CheckNow made no requests to NWSL_ASA_BASE_URL")
	}
}

func TestStopThenWaitReturnsWithInFlightASARequest(t *testing.T) {
	fake := newCountingASA(t, true, 0)
	srv := buildTestServer(t, testConfig(t, fake.server.URL), Options{StartScheduler: true})
	srv.Start()
	srv.Start() // A second Start must not launch another scheduler.
	select {
	case <-fake.started:
	case <-time.After(10 * time.Second):
		t.Fatal("started scheduler made no ASA request")
	}
	waited := make(chan struct{})
	go func() {
		srv.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("Wait returned while the scheduler still had an ASA request in flight")
	case <-time.After(100 * time.Millisecond):
	}
	srv.Stop()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("Wait did not return after Stop")
	}
	waitWithTimeout(t, "second Wait", srv.Wait)
}

func TestStopThenWaitReturnsWithoutScheduler(t *testing.T) {
	fake := newCountingASA(t, false, 0)
	srv := buildTestServer(t, testConfig(t, fake.server.URL), Options{StartScheduler: false})
	srv.Start()
	srv.Stop()
	waitWithTimeout(t, "Wait after Stop", srv.Wait)
	if err := srv.CheckNow(context.Background()); !errors.Is(err, scheduler.ErrStopped) {
		t.Fatalf("CheckNow after Stop error = %v, want scheduler.ErrStopped", err)
	}
	if got := fake.total.Load(); got != 0 {
		t.Fatalf("ASA requests = %d, want none", got)
	}
}

func TestBuildStopsWhenSourceScopeSeedingFails(t *testing.T) {
	originalEnsure := ensureSourceScopeRegistry
	ensureSourceScopeRegistry = func(context.Context, *cache.DB, string, string, time.Time) error {
		return errors.New("source scope registry unavailable")
	}
	t.Cleanup(func() { ensureSourceScopeRegistry = originalEnsure })
	_, err := Build(context.Background(), config.Config{
		DBPath:     filepath.Join(t.TempDir(), "cache.sqlite"),
		SyncSeason: "2026",
		SyncStage:  "Regular Season",
	}, Options{Logger: quietLogger()})
	if err == nil || !strings.Contains(err.Error(), "seed source scope registry") {
		t.Fatalf("Build error = %v, want source-scope seeding failure", err)
	}
}

func TestBuildReportsCacheOpenFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Build(context.Background(), config.Config{
		DBPath:     filepath.Join(blocker, "cache.sqlite"),
		SyncSeason: "2026",
		SyncStage:  "Regular Season",
	}, Options{Logger: quietLogger()})
	if err == nil || !strings.Contains(err.Error(), "open cache database") {
		t.Fatalf("Build error = %v, want cache open failure", err)
	}
}
