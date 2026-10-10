//go:build e2e

package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asatest"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/server"
)

// mountPrefix is the path the app is served under, as behind the production
// reverse proxy.
const mountPrefix = "/nwsl-season"

// currentSeason matches the default NWSL_SYNC_SEASON.
const currentSeason = "2026"

// The fixture is a 16-team double round robin with its first half played.
const (
	fixtureTeams       = 16
	fixtureGames       = fixtureTeams * (fixtureTeams - 1)
	fixturePlayedGames = fixtureGames / 2
)

// fixture is a running app backed by a temporary cache and a fake ASA.
type fixture struct {
	// ASA is the fake ASA API. Its recorded requests show whether a page
	// contacted the source.
	ASA *asatest.Server
	// Server is the composition under test; the scheduler is not started.
	Server *server.Server
	// BaseURL is the app's root, including the mount prefix and a trailing
	// slash.
	BaseURL string
	// Season is what newFixture loaded into the fake. Fixtures seeded without
	// a fake leave it nil, and journeys track their own changing copy.
	Season *asatest.Scenario
}

// URL returns the absolute URL for an app path such as "seasons".
func (f *fixture) URL(path string) string { return f.BaseURL + path }

// e2eForecastIterations is the number of simulated seasons per forecast.
// server.Build and every sync that changes forecast inputs warm every catalog
// model; at the production default (50,000) that warming dominated fixture
// setup. Simulations are seeded, so the pages stay deterministic.
const e2eForecastIterations = 1000

// testConfig returns a configuration for the current season's Regular Season
// with a fresh data directory. It sets fields rather than environment
// variables, because parallel tests cannot use t.Setenv.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.FromEnvironment()
	if err != nil {
		t.Fatalf("config.FromEnvironment: %v", err)
	}
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "nwsl-season.sqlite")
	cfg.SyncSeason = currentSeason
	cfg.SyncStage = "Regular Season"
	cfg.SyncTimeout = 30 * time.Second
	return cfg
}

// newFixture builds a 16-team season with half of its games played, fills the
// cache with repeated CheckNow calls (see fillCache), and serves the handler under /nwsl-season/.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	// The 2026 Regular Season rules require a full inventory (16 teams, 240
	// games), so the fixture is a 16-team double round robin: 30 rounds a week
	// apart. Kickoffs are relative to the wall clock because the syncer stamps
	// its operations with real time. The first round was 105 days ago, so the
	// first 15 rounds are played and the other half is ahead.
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -105).Add(19 * time.Hour)
	fake := asatest.New(t)
	season := asatest.Season(16, start)
	for i := range season.Games {
		// Season names the games after start's year, which may differ.
		season.Games[i].SeasonName = currentSeason
	}
	fake.Load(season.PlayThrough(start.AddDate(0, 0, 105), halfSeasonScore).WithXG(1))

	now := start.AddDate(0, 0, 22)
	srv, err := server.Build(context.Background(), testConfig(t), server.Options{
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		ASABaseURL:         fake.URL(),
		StartScheduler:     false,
		Now:                func() time.Time { return now },
		ForecastIterations: e2eForecastIterations,
	})
	if err != nil {
		t.Fatalf("server.Build: %v", err)
	}
	t.Cleanup(func() {
		srv.Stop()
		srv.Wait()
		if err := srv.Close(); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	fillCache(t, srv, fake)

	mux := http.NewServeMux()
	mux.Handle(mountPrefix+"/", http.StripPrefix(mountPrefix, srv.Handler()))
	web := httptest.NewServer(mux)
	t.Cleanup(web.Close)

	f := &fixture{ASA: fake, Server: srv, BaseURL: web.URL + mountPrefix + "/", Season: season}
	assertXGCoverage(t, f)
	return f
}

// maxFillChecks bounds fillCache. The fixture needs about ten checks.
const maxFillChecks = 40

// fillCache runs CheckNow until a check makes no further ASA request. Each
// check is bounded by the scheduler's per-tick source request budget (3 by
// default), and the planner works through the archived seasons' inventories
// before the current season's initial xG load. A single CheckNow therefore
// loads only games and teams, leaving every page without xG.
func fillCache(t *testing.T, srv *server.Server, fake *asatest.Server) {
	t.Helper()
	for range maxFillChecks {
		before := len(fake.Requests())
		if err := srv.CheckNow(context.Background()); err != nil {
			t.Fatalf("CheckNow: %v", err)
		}
		if len(fake.Requests()) == before {
			return
		}
	}
	t.Fatalf("the cache was still loading after %d CheckNow calls", maxFillChecks)
}

var xgCoverage = regexp.MustCompile(`xG coverage:\s*(?:<[^>]*>\s*)?(\d+) of (\d+) completed matches`)

// assertXGCoverage fails the fixture unless the forecast page reports xG for
// every completed match. It reads the page over plain HTTP, so a regression
// surfaces here instead of as a confusing browser assertion in a later test.
func assertXGCoverage(t *testing.T, f *fixture) {
	t.Helper()
	resp, err := http.Get(f.URL("seasons/" + currentSeason + "/forecast"))
	if err != nil {
		t.Fatalf("GET forecast: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("forecast page: status %d, read error %v", resp.StatusCode, err)
	}
	match := xgCoverage.FindStringSubmatch(string(body))
	want := strconv.Itoa(fixturePlayedGames)
	if match == nil || match[1] != want || match[2] != want {
		t.Fatalf("forecast xG coverage = %q, want %s of %s completed matches", match, want, want)
	}
}
