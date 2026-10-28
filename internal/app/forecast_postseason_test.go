package app

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/cache"
	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/forecaststate"
	"github.com/jrduncans/nwsl-season/internal/simulation"
)

func forecastPlayoffGame(seeds playoffSeeds, id string, home, away int, played bool) cache.Game {
	game := cache.Game{ASAID: id, Season: "2026", Stage: "Playoffs", HomeTeamID: seeds.Seeds[home], AwayTeamID: seeds.Seeds[away], Status: fixtures.PreMatchStatus, KickoffUTC: "2026-11-01T20:00:00Z"}
	if played {
		game.Status = fixtures.CompletedStatus
		game.HomeScore, game.AwayScore = sql.NullInt64{Int64: 2, Valid: true}, sql.NullInt64{Valid: true}
	}
	return game
}

func TestForecastPostseasonPublicationAndResultSafety(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	game := forecastPlayoffGame(seeds, "qf", 1, 8, true)
	for _, kind := range []string{"empty", "placeholder", "partial", "quarterfinal", "extra-time", "shootout", "tied", "missing-score", "one-sided-shootout", "equal-shootout", "negative-score", "live", "abandoned", "mismatch", "duplicate", "semifinal-missing-prior", "unidentified-result", "failed-refresh"} {
		t.Run(kind, func(t *testing.T) {
			store := handoffStore{regular: regular}
			g := game
			wantAvailable := false
			wantWinner := ""
			switch kind {
			case "empty":
				wantAvailable = true
			case "placeholder":
				store.playoffs.Games = []cache.Game{{ASAID: "placeholder", Season: "2026", Stage: "Playoffs"}}
				wantAvailable = true
			case "partial":
				g = forecastPlayoffGame(seeds, "qf", 3, 6, false)
				wantAvailable = true
			case "quarterfinal", "extra-time":
				g.ExtraTime = sql.NullBool{Bool: kind == "extra-time", Valid: true}
				wantAvailable, wantWinner = true, g.HomeTeamID
			case "shootout":
				g.AwayScore = g.HomeScore
				g.Penalties = sql.NullBool{Bool: true, Valid: true}
				g.HomePenalties, g.AwayPenalties = sql.NullInt64{Int64: 3, Valid: true}, sql.NullInt64{Int64: 4, Valid: true}
				wantAvailable, wantWinner = true, g.AwayTeamID
			case "tied":
				g.AwayScore = g.HomeScore
			case "missing-score":
				g.HomeScore = sql.NullInt64{}
			case "one-sided-shootout", "equal-shootout":
				g.AwayScore = g.HomeScore
				g.Penalties = sql.NullBool{Bool: true, Valid: true}
				g.HomePenalties = sql.NullInt64{Int64: 4, Valid: true}
				if kind == "equal-shootout" {
					g.AwayPenalties = g.HomePenalties
				}
			case "negative-score":
				g.AwayScore.Int64 = -1
			case "live":
				g.Status = "InProgress"
			case "abandoned":
				g.Status = fixtures.AbandonedStatus
			case "mismatch":
				g.AwayTeamID = seeds.Seeds[7]
			case "semifinal-missing-prior":
				g.AwayTeamID = seeds.Seeds[4]
			case "unidentified-result":
				g.AwayTeamID = ""
			case "failed-refresh":
				store.status.LastAttempt = &cache.SyncRun{Outcome: "failure"}
			}
			if kind != "empty" && kind != "placeholder" {
				store.playoffs.Games = []cache.Game{g}
			}
			if kind == "duplicate" {
				g.ASAID = "duplicate"
				store.playoffs.Games = append(store.playoffs.Games, g)
			}
			a := &application{store: store}
			state, err := a.forecastPostseason(context.Background(), "2026", regular, 8)
			if err != nil || state == nil || (state.Unavailable == "") != wantAvailable {
				t.Fatalf("state = %+v, error = %v", state, err)
			}
			if wantWinner != "" && state.Slots["quarterfinal-1"].Winner != wantWinner {
				t.Fatalf("actual advancement = %+v, want %s", state.Slots, wantWinner)
			}
			if wantAvailable {
				_, err := simulation.Run(context.Background(), simulation.Request{Teams: regular.Teams, Games: standingsGames(regular.Games), Model: forecast.NewResultsPoissonV1(), Iterations: 5, PlayoffPlaces: 8, PlayoffBracket: forecastPlayoffBracket("2026", 8), Postseason: state})
				if err != nil {
					t.Fatalf("usable source state rejected by simulator: %v", err)
				}
			}
		})
	}
}

func TestPostseasonCannotReplayResultsWithAStaleRegularSeason(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	regular.Games[0].Status = fixtures.PreMatchStatus
	regular.Games[0].HomeScore, regular.Games[0].AwayScore = sql.NullInt64{}, sql.NullInt64{}
	store := handoffStore{regular: regular, playoffs: cache.SeasonData{Games: []cache.Game{forecastPlayoffGame(seeds, "played-quarterfinal", 1, 8, true)}}}
	a := &application{store: store}
	state, err := a.forecastPostseason(context.Background(), "2026", regular, 8)
	if err != nil || state == nil || state.Unavailable == "" {
		t.Fatalf("stale regular-season data permitted replay: %+v, %v", state, err)
	}
	result, err := simulation.Run(context.Background(), simulation.Request{Teams: regular.Teams, Games: standingsGames(regular.Games), Model: forecast.NewResultsPoissonV1(), Iterations: 5, PlayoffPlaces: 8, PlayoffBracket: forecastPlayoffBracket("2026", 8), Postseason: state})
	if err != nil || result.ChampionshipSimulated {
		t.Fatalf("known playoff results replayed: %+v, %v", result, err)
	}
}

func TestPostseasonCacheAndWarmingUseTheSameInputs(t *testing.T) {
	store := &handoffStore{regular: resolvedHandoffSeason()}
	seeds := playoffSeedsFrom("2026", store.regular)
	app := NewApplication(store, Options{ForecastIterations: 5, Location: time.UTC})
	if err := app.PrecacheForecasts(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := forecaststate.State{ModelID: forecast.Default().Model.Info().ID, Fixed: map[string]simulation.Outcome{}}
	before, err := app.app.forecastPostseason(context.Background(), "2026", store.regular, 8)
	if err != nil {
		t.Fatal(err)
	}
	key := forecastResultKey(store.regular, state, state.ModelID, 5, 8, before)
	// Precache uses empty ModelID state, while live state has the default ID;
	// Values encodes assumptions only, so both must select the same result.
	if _, ok := app.app.forecasts.cache[key]; !ok {
		t.Fatal("warming did not cache the live postseason inputs")
	}
	store.playoffs.Games = []cache.Game{forecastPlayoffGame(seeds, "qf", 1, 8, true)}
	after, err := app.app.forecastPostseason(context.Background(), "2026", store.regular, 8)
	if err != nil {
		t.Fatal(err)
	}
	if key == forecastResultKey(store.regular, state, state.ModelID, 5, 8, after) {
		t.Fatal("playoff result reused a stale forecast")
	}
	shootout := &store.playoffs.Games[0]
	shootout.AwayScore = shootout.HomeScore
	shootout.Penalties = sql.NullBool{Bool: true, Valid: true}
	shootout.HomePenalties, shootout.AwayPenalties = sql.NullInt64{Int64: 4, Valid: true}, sql.NullInt64{Int64: 3, Valid: true}
	before, err = app.app.forecastPostseason(context.Background(), "2026", store.regular, 8)
	if err != nil {
		t.Fatal(err)
	}
	shootout.HomePenalties.Int64, shootout.AwayPenalties.Int64 = 3, 4
	after, err = app.app.forecastPostseason(context.Background(), "2026", store.regular, 8)
	if err != nil {
		t.Fatal(err)
	}
	if string(before.Material()) == string(after.Material()) || forecastResultKey(store.regular, state, state.ModelID, 5, 8, before) == forecastResultKey(store.regular, state, state.ModelID, 5, 8, after) {
		t.Fatal("a shootout-only correction did not invalidate the forecast")
	}
}

func TestWithheldPostseasonOddsKeepRecordedEliminations(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	played := forecastPlayoffGame(seeds, "qf-1", 1, 8, true)
	for _, kind := range []string{"failed-refresh", "live", "missing-score"} {
		t.Run(kind, func(t *testing.T) {
			store := handoffStore{regular: regular}
			other := forecastPlayoffGame(seeds, "qf-2", 4, 5, true)
			switch kind {
			case "failed-refresh":
				store.status.LastAttempt = &cache.SyncRun{Outcome: "failure"}
			case "live":
				other.Status = "InProgress"
			case "missing-score":
				other.HomeScore = sql.NullInt64{}
			}
			store.playoffs.Games = []cache.Game{played, other}
			a := &application{store: store}
			state, err := a.forecastPostseason(context.Background(), "2026", regular, 8)
			if err != nil || state == nil || state.Unavailable == "" {
				t.Fatalf("state = %+v, error = %v", state, err)
			}
			want := []string{seeds.Seeds[8]}
			if kind == "failed-refresh" {
				want = append(want, seeds.Seeds[5])
			}
			slices.Sort(want)
			if !slices.Equal(state.Eliminated, want) {
				t.Fatalf("eliminated = %v, want %v", state.Eliminated, want)
			}
		})
	}

}

func TestPublishedPlayoffFixturesKeepTheRegularSeasonForecast(t *testing.T) {
	regular := resolvedHandoffSeason()
	seeds := playoffSeedsFrom("2026", regular)
	regular.Games[0].Status = fixtures.PreMatchStatus
	regular.Games[0].HomeScore, regular.Games[0].AwayScore = sql.NullInt64{}, sql.NullInt64{}
	store := handoffStore{regular: regular, playoffs: cache.SeasonData{Games: []cache.Game{forecastPlayoffGame(seeds, "published-quarterfinal", 1, 8, false)}}}
	a := &application{store: store}
	state, err := a.forecastPostseason(context.Background(), "2026", regular, 8)
	if err != nil || state != nil {
		t.Fatalf("an unplayed published fixture switched to postseason mode: %+v, %v", state, err)
	}
}
