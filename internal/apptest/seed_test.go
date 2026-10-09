package apptest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/app"
	"github.com/jrduncans/nwsl-season/internal/apptest"
	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

// pageExpectation names a page and text that only a correctly seeded scenario
// renders; an empty or unseeded cache returns 200 for the same routes.
type pageExpectation struct {
	path string
	text string
}

func scenarioPages(selection string) map[string][]pageExpectation {
	selected := `<h2 id="selected-season-heading">` + selection + `</h2>`
	scoring := "/history/scoring?metric=xg&season=" + selection
	return map[string][]pageExpectation{
		apptest.ScenarioDefault:     {{scoring, selected}, {scoring, "<strong>Selected 2022:</strong> 1.00 goals per match"}},
		apptest.ScenarioOverview:    {{scoring, selected}},
		apptest.ScenarioSingle:      {{scoring, selected}, {scoring, "xG available for 0 of 20 completed matches"}},
		apptest.ScenarioEmpty:       {{scoring, selected}, {scoring, "No eligible seasons currently have complete xG coverage to plot."}},
		apptest.ScenarioTeams:       {{"/explore", "Angel City FC"}},
		apptest.ScenarioNoXG:        {{"/explore", "Angel City FC"}},
		apptest.ScenarioTeamHistory: {{"/explore?view=team-history", "Angel City FC"}},
		apptest.ScenarioSeasonTrend: {{"/explore?view=season-trend", "Angel City FC"}},
	}
}

// TestSeedStoresEveryScenario checks that each scenario's teams, fixtures and
// xG reach the cache exactly and that the pages render scenario-specific
// content from it.
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
				year := season.Entry.Season
				got := seasons[year]
				assertGames(t, year, got.Games, season.Data.Games)
				assertTeams(t, year, got.Teams, season.Data)

				wantXG := 0
				for _, observation := range season.Data.XGoals {
					if observation.HomeXG.Valid && observation.AwayXG.Valid {
						wantXG++
					}
				}
				gotXG := 0
				for _, observation := range got.XGoals {
					if observation.Availability == cache.XGAvailable {
						gotXG++
					}
				}
				if gotXG != wantXG {
					t.Errorf("%s available xG = %d, want %d", year, gotXG, wantXG)
				}
			}

			handler := app.NewHandler(db)
			for _, page := range scenarioPages(selection)[name] {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, page.path, nil))
				if response.Code != http.StatusOK {
					t.Errorf("GET %s = %d, want 200", page.path, response.Code)
				}
				if !strings.Contains(response.Body.String(), page.text) {
					t.Errorf("GET %s lacks %q", page.path, page.text)
				}
			}
		})
	}
}

func assertGames(t *testing.T, year string, got, want []cache.Game) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s games = %d, want %d", year, len(got), len(want))
		return
	}
	byID := make(map[string]cache.Game, len(got))
	for _, game := range got {
		byID[game.ASAID] = game
	}
	for _, game := range want {
		stored, ok := byID[game.ASAID]
		if !ok {
			t.Errorf("%s game %s was not stored", year, game.ASAID)
			continue
		}
		if stored.HomeTeamID != game.HomeTeamID || stored.AwayTeamID != game.AwayTeamID ||
			stored.HomeScore != game.HomeScore || stored.AwayScore != game.AwayScore ||
			stored.KickoffUTC != game.KickoffUTC || stored.Status != game.Status {
			t.Errorf("%s game %s = %+v, want teams %s/%s score %v-%v kickoff %q status %q", year, game.ASAID, stored,
				game.HomeTeamID, game.AwayTeamID, game.HomeScore, game.AwayScore, game.KickoffUTC, game.Status)
		}
	}
}

func assertTeams(t *testing.T, year string, got []standings.Team, want cache.SeasonData) {
	t.Helper()
	names := make(map[string]string, len(got))
	for _, team := range got {
		names[team.ID] = team.Name
	}
	for _, team := range want.Teams {
		if names[team.ID] != team.Name {
			t.Errorf("%s team %s = %q, want %q", year, team.ID, names[team.ID], team.Name)
		}
	}
	for _, game := range want.Games {
		if _, ok := names[game.HomeTeamID]; !ok {
			t.Errorf("%s home team %s was not stored", year, game.HomeTeamID)
		}
		if _, ok := names[game.AwayTeamID]; !ok {
			t.Errorf("%s away team %s was not stored", year, game.AwayTeamID)
		}
	}
}

func TestScenarioNamesAreAllAccepted(t *testing.T) {
	for _, name := range apptest.ScenarioNames() {
		if archive, selection := apptest.Scenario(t, name); len(archive) == 0 || selection == "" {
			t.Errorf("scenario %q = %d seasons, selection %q", name, len(archive), selection)
		}
	}
}
