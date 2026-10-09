// Command preview serves the application on a loopback port from a cache
// seeded with a named apptest scenario, for local browser checks. It uses a
// temporary directory that it removes on exit, never reads the user's
// configuration file or cache, never starts the scheduler, and makes no ASA
// request.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/server"
)

// mountPrefix is the path the app is served under, as behind the production
// reverse proxy.
const mountPrefix = "/nwsl-season"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("preview", flag.ContinueOnError)
	flags.SetOutput(stderr)
	scenario := flags.String("scenario", apptest.ScenarioDefault, "cache scenario: "+strings.Join(apptest.ScenarioNames(), ", "))
	addr := flags.String("addr", "127.0.0.1:0", "loopback listen address")
	metric := flags.String("metric", "xg", "history scoring metric in the printed URL")
	noScript := flags.Bool("no-script", false, "send a Content-Security-Policy that blocks page scripts")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if err := serve(*scenario, *addr, *metric, *noScript, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "preview: %v\n", err)
		return 1
	}
	return 0
}

func serve(scenario, addr, metric string, noScript bool, stdout, stderr io.Writer) (err error) {
	if !validScenario(scenario) {
		return fmt.Errorf("unknown scenario %q; want one of %s", scenario, strings.Join(apptest.ScenarioNames(), ", "))
	}
	host, _, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		return fmt.Errorf("listen address %q: %w", addr, splitErr)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q is not a loopback IP address", addr)
	}

	dir, err := os.MkdirTemp("", "nwsl-preview-")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()

	cfg, err := config.FromEnvironment()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	cfg.DataDir = dir
	cfg.DBPath = filepath.Join(dir, "nwsl-season.sqlite")

	selection, err := seed(cfg.DBPath, scenario)
	if err != nil {
		return err
	}

	// The unroutable base URL guarantees that nothing here reaches ASA even if
	// a page were to try.
	srv, err := server.Build(context.Background(), cfg, server.Options{
		Logger:         slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		ASABaseURL:     "http://127.0.0.1:1",
		StartScheduler: false,
	})
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}
	defer func() {
		srv.Stop()
		srv.Wait()
		err = errors.Join(err, srv.Close())
	}()

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle(mountPrefix+"/", http.StripPrefix(mountPrefix, srv.Handler()))
	var handler http.Handler = mux
	if noScript {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "script-src 'none'")
			mux.ServeHTTP(w, r)
		})
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	query := url.Values{"metric": {metric}, "season": {selection}}
	fmt.Fprintf(stdout, "http://%s%s/history/scoring?%s\n", listener.Addr(), mountPrefix, query.Encode())
	fmt.Fprintf(stderr, "preview: scenario %q; press Ctrl-C to stop\n", scenario)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func validScenario(name string) bool {
	for _, known := range apptest.ScenarioNames() {
		if name == known {
			return true
		}
	}
	return false
}

// seed fills a new cache at path with the scenario and returns the season the
// history scoring page should preselect.
func seed(path, scenario string) (selection string, err error) {
	ctx := context.Background()
	db, err := cache.Open(ctx, path)
	if err != nil {
		return "", fmt.Errorf("open cache: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	reporter := &reporter{}
	defer func() {
		if recovered := recover(); recovered != nil {
			fatal, ok := recovered.(fatalError)
			if !ok {
				panic(recovered)
			}
			err = fmt.Errorf("seed %s: %s", scenario, fatal)
		}
	}()
	apptest.Seed(reporter, db, scenario)
	_, selection = apptest.Scenario(reporter, scenario)
	return selection, nil
}

// fatalError carries a failed test-helper message out of apptest.
type fatalError string

// reporter satisfies testing.TB for the apptest helpers, which only call
// Helper and Fatalf. Embedding the interface leaves every other method
// unimplemented, which would panic if apptest started to use it.
type reporter struct{ testing.TB }

func (*reporter) Helper() {}

func (*reporter) Fatalf(format string, args ...any) {
	panic(fatalError(fmt.Sprintf(format, args...)))
}
