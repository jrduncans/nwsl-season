//go:build e2e

package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asatest"
	"github.com/jrduncans/nwsl-season/internal/cache"
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
	fake.Load(seedPenalties(season.PlayThrough(start.AddDate(0, 0, 105), halfSeasonScore).WithXG(1)))

	now := start.AddDate(0, 0, 22)
	cfg := testConfig(t)
	srv, err := server.Build(context.Background(), cfg, server.Options{
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
	assertXGCoverage(t, cfg.DBPath)
	return f
}

// seedPenalties gives some completed games a converted penalty so the
// non-penalty basis in Explore and history, and the fixture npxG lines, differ
// from their all-shots values.
func seedPenalties(season *asatest.Scenario) *asatest.Scenario {
	for i, game := range season.Games {
		if game.Status != "FullTime" || game.HomeScore == nil || game.AwayScore == nil {
			continue
		}
		switch {
		case i%5 == 0 && *game.HomeScore > 0:
			season.WithPenalty(game.GameID, asatest.PenaltyPattern{HomeXG: .76, HomeGoals: 1, HomeShots: 1})
		case i%7 == 3 && *game.AwayScore > 0:
			season.WithPenalty(game.GameID, asatest.PenaltyPattern{AwayXG: .76, AwayGoals: 1, AwayShots: 1})
		}
	}
	return season
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

// assertXGCoverage checks the fixture cache directly so page layout changes
// cannot weaken the guarantee that every completed match has xG available.
func assertXGCoverage(t *testing.T, path string) {
	t.Helper()
	db, err := cache.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	data, err := db.Season(context.Background(), currentSeason, "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	available := 0
	for _, xg := range data.XGoals {
		if xg.Availability == cache.XGAvailable {
			available++
		}
	}
	if available != fixturePlayedGames {
		t.Fatalf("fixture xG available for %d matches, want %d", available, fixturePlayedGames)
	}
}
