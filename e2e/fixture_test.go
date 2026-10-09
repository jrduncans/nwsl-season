//go:build e2e

package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/asatest"
	"github.com/jrduncans/nwsl-season/internal/config"
	"github.com/jrduncans/nwsl-season/internal/server"
)

// mountPrefix is the path the app is served under, as behind the production
// reverse proxy.
const mountPrefix = "/nwsl-season"

// currentSeason matches the default NWSL_SYNC_SEASON.
const currentSeason = "2026"

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
}

// URL returns the absolute URL for an app path such as "seasons".
func (f *fixture) URL(path string) string { return f.BaseURL + path }

// newFixture builds a 16-team season with half of its games played, fills the
// cache with one CheckNow, and serves the handler under /nwsl-season/.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("NWSL_DATA_DIR", t.TempDir())
	t.Setenv("NWSL_SYNC_SEASON", currentSeason)
	t.Setenv("NWSL_SYNC_STAGE", "Regular Season")
	t.Setenv("NWSL_SYNC_TIMEOUT", "30s")

	// The 2026 Regular Season rules require a full inventory (16 teams, 240
	// games), so the fixture is a 16-team double round robin: 30 rounds a week
	// apart. Kickoffs are relative to the wall clock because the syncer stamps
	// its operations with real time. The first round was 105 days ago, so the
	// first 15 rounds are played and the other half is ahead.
	start := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -105).Add(19 * time.Hour)
	fake := asatest.New(t)
	season := asatest.Season(16, start)
	for i := range season.Games {
		// Season names the games after start's year, which may differ.
		season.Games[i].SeasonName = currentSeason
	}
	fake.Load(season.
		PlayThrough(start.AddDate(0, 0, 105), func(game asa.Game) (int, int) {
			// Deterministic results with wins, draws and losses.
			switch game.GameID[len(game.GameID)-1] % 3 {
			case 0:
				return 2, 1
			case 1:
				return 1, 1
			default:
				return 0, 1
			}
		}).
		WithXG(1))

	cfg, err := config.FromEnvironment()
	if err != nil {
		t.Fatalf("config.FromEnvironment: %v", err)
	}
	now := start.AddDate(0, 0, 22)
	srv, err := server.Build(context.Background(), cfg, server.Options{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		ASABaseURL:     fake.URL(),
		StartScheduler: false,
		Now:            func() time.Time { return now },
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
	if err := srv.CheckNow(context.Background()); err != nil {
		t.Fatalf("CheckNow: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle(mountPrefix+"/", http.StripPrefix(mountPrefix, srv.Handler()))
	web := httptest.NewServer(mux)
	t.Cleanup(web.Close)

	return &fixture{ASA: fake, Server: srv, BaseURL: web.URL + mountPrefix + "/"}
}
