package apptest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/app"
	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
)

// TestSeedStoresEveryScenario checks that each scenario's fixtures and xG
// reach the cache and that the pages render from it.
func TestSeedStoresEveryScenario(t *testing.T) {
	for _, name := range apptest.ScenarioNames() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := cache.Open(ctx, filepath.Join(t.TempDir(), "cache.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			apptest.Seed(t, db, name)

			want, selection := apptest.Scenario(t, name)
			stored, err := db.HistoricalRegularSeasons(ctx)
			if err != nil {
				t.Fatal(err)
			}
			seasons := map[string]cache.SeasonData{}
			for _, season := range stored {
				seasons[season.Entry.Season] = season.Data
			}
			for _, season := range want {
				got := seasons[season.Entry.Season]
				if len(got.Games) != len(season.Data.Games) {
					t.Errorf("%s games = %d, want %d", season.Entry.Season, len(got.Games), len(season.Data.Games))
				}
				available := 0
				for _, observation := range got.XGoals {
					if observation.Availability == cache.XGAvailable {
						available++
					}
				}
				if available == 0 && len(season.Data.XGoals) > 0 {
					t.Errorf("%s stored no available xG, want some of %d", season.Entry.Season, len(season.Data.XGoals))
				}
			}

			handler := app.NewHandler(db)
			for _, path := range []string{"/history/scoring?metric=xg&season=" + selection, "/explore", "/explore?view=team-history", "/explore?view=season-trend"} {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
				if response.Code != http.StatusOK {
					t.Errorf("GET %s = %d, want 200", path, response.Code)
				}
			}
		})
	}
}

func TestScenarioNamesAreAllAccepted(t *testing.T) {
	for _, name := range apptest.ScenarioNames() {
		if archive, selection := apptest.Scenario(t, name); len(archive) == 0 || selection == "" {
			t.Errorf("scenario %q = %d seasons, selection %q", name, len(archive), selection)
		}
	}
}
