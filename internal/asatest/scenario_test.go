package asatest_test

import (
	"context"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/asatest"
)

func TestSeasonIsDoubleRoundRobin(t *testing.T) {
	for _, teams := range []int{2, 3, 6, 7, 14} {
		scenario := asatest.Season(teams, base)
		if len(scenario.Teams) != teams {
			t.Fatalf("%d teams: got %d", teams, len(scenario.Teams))
		}
		wantGames := teams * (teams - 1)
		if len(scenario.Games) != wantGames {
			t.Fatalf("%d teams: %d games, want %d", teams, len(scenario.Games), wantGames)
		}

		ordered := map[[2]string]int{}
		kickoffs := map[string]bool{}
		gameIDs := map[string]bool{}
		perRound := map[int]map[string]bool{}
		for _, g := range scenario.Games {
			if g.HomeTeamID == g.AwayTeamID {
				t.Fatalf("%d teams: %s plays itself", teams, g.HomeTeamID)
			}
			ordered[[2]string{g.HomeTeamID, g.AwayTeamID}]++
			if kickoffs[g.DateTimeUTC] || gameIDs[g.GameID] {
				t.Fatalf("%d teams: duplicate kickoff or id on %+v", teams, g)
			}
			kickoffs[g.DateTimeUTC], gameIDs[g.GameID] = true, true
			if g.Status != "PreMatch" || g.HomeScore != nil || g.SeasonName != "2026" || g.Matchday == nil {
				t.Fatalf("%d teams: unexpected game %+v", teams, g)
			}
			if perRound[*g.Matchday] == nil {
				perRound[*g.Matchday] = map[string]bool{}
			}
			for _, team := range []string{g.HomeTeamID, g.AwayTeamID} {
				if perRound[*g.Matchday][team] {
					t.Fatalf("%d teams: %s plays twice in matchday %d", teams, team, *g.Matchday)
				}
				perRound[*g.Matchday][team] = true
			}
		}
		for _, home := range scenario.Teams {
			for _, away := range scenario.Teams {
				want := 1
				if home.TeamID == away.TeamID {
					want = 0
				}
				if got := ordered[[2]string{home.TeamID, away.TeamID}]; got != want {
					t.Fatalf("%d teams: %s hosts %s %d times, want %d", teams, home.TeamID, away.TeamID, got, want)
				}
			}
		}
	}
}

func TestSeasonSpacesKickoffsFromBase(t *testing.T) {
	scenario := asatest.Season(4, base)
	first, err := time.Parse("2006-01-02 15:04:05 MST", scenario.Games[0].DateTimeUTC)
	if err != nil || !first.Equal(base) {
		t.Fatalf("first kickoff = %v, %v; want %v", first, err, base)
	}
	last, _ := time.Parse("2006-01-02 15:04:05 MST", scenario.Games[len(scenario.Games)-1].DateTimeUTC)
	if !last.After(base.AddDate(0, 0, 7*5)) {
		t.Fatalf("last kickoff %v not in the final round", last)
	}
}

func TestSeasonPanicsWithoutEnoughTeams(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Season(1) did not panic")
		}
	}()
	asatest.Season(1, base)
}

func TestPlayThroughCompletesOnlyKickedOffGames(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	scenario := asatest.Season(4, now.AddDate(0, 0, -21)).PlayThrough(now, func(g asa.Game) (int, int) {
		return 2, 1
	})

	played, pending := 0, 0
	for _, g := range scenario.Games {
		kickoff, err := time.Parse("2006-01-02 15:04:05 MST", g.DateTimeUTC)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case kickoff.Before(now):
			played++
			if g.Status != "FullTime" || g.HomeScore == nil || *g.HomeScore != 2 || *g.AwayScore != 1 {
				t.Fatalf("kicked-off game not completed: %+v", g)
			}
		default:
			pending++
			if g.Status != "PreMatch" || g.HomeScore != nil || g.AwayScore != nil {
				t.Fatalf("future game completed: %+v", g)
			}
		}
	}
	if played == 0 || pending == 0 {
		t.Fatalf("played = %d, pending = %d; want both non-zero", played, pending)
	}

	// Playing through a later time keeps earlier results.
	scenario.PlayThrough(now.AddDate(1, 0, 0), func(asa.Game) (int, int) { return 0, 0 })
	for _, g := range scenario.Games {
		if g.Status != "FullTime" {
			t.Fatalf("game still pending: %+v", g)
		}
	}
	if *scenario.Games[0].HomeScore != 2 {
		t.Fatal("PlayThrough overwrote an existing result")
	}
}

func TestWithXGCoversRequestedShareOfCompletedGames(t *testing.T) {
	scenario := asatest.Season(6, base).PlayThrough(base.AddDate(1, 0, 0), func(g asa.Game) (int, int) { return 1, 0 })
	total := len(scenario.Games)

	for _, tt := range []struct {
		fraction float64
		want     int
	}{{0, 0}, {0.5, total / 2}, {1, total}, {2, total}, {-1, 0}} {
		scenario.WithXG(tt.fraction)
		if len(scenario.XGoals) != tt.want {
			t.Fatalf("WithXG(%v): %d rows, want %d of %d", tt.fraction, len(scenario.XGoals), tt.want, total)
		}
	}

	scenario.WithXG(1)
	first := scenario.XGoals[0]
	if first.GameID != scenario.Games[0].GameID || first.HomeTeamXGoals <= first.AwayTeamXGoals ||
		first.HomeXPoints == nil || *first.HomeXPoints <= *first.AwayXPoints {
		t.Fatalf("xG row inconsistent with a 1-0 home win: %+v", first)
	}

	// Pending games never get xG.
	partial := asatest.Season(4, base).PlayThrough(base.AddDate(0, 0, 8), func(asa.Game) (int, int) { return 0, 0 }).WithXG(1)
	completed := 0
	for _, g := range partial.Games {
		if g.Status == "FullTime" {
			completed++
		}
	}
	if completed == 0 || len(partial.XGoals) != completed {
		t.Fatalf("xG rows = %d, completed = %d", len(partial.XGoals), completed)
	}
}

func TestLoadServesScenarioThroughClient(t *testing.T) {
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	scenario := asatest.Season(4, now.AddDate(0, 0, -30)).
		PlayThrough(now, func(asa.Game) (int, int) { return 1, 1 }).
		WithXG(0.5)
	scenario.Stages["game-001"] = "Playoffs"
	fake := asatest.New(t)
	fake.Load(scenario)
	client := fake.Client()
	ctx := context.Background()

	teams, err := client.Teams(ctx, asa.TeamsFilters{})
	if err != nil || len(teams) != 4 {
		t.Fatalf("teams = %v, %v", teams, err)
	}
	finished, err := client.Games(ctx, asa.GamesFilters{Status: "FullTime", SeasonName: "2026"})
	if err != nil || len(finished) == 0 || len(finished) >= len(scenario.Games) {
		t.Fatalf("finished = %d of %d, %v", len(finished), len(scenario.Games), err)
	}
	playoffs, err := client.Games(ctx, asa.GamesFilters{StageName: "Playoffs"})
	if err != nil || len(playoffs) != 1 || playoffs[0].GameID != "game-001" {
		t.Fatalf("playoffs = %+v, %v", playoffs, err)
	}
	xg, err := client.GameXGoals(ctx, asa.XGoalsFilters{SeasonName: "2026"})
	if err != nil || len(xg) != len(scenario.XGoals) || len(xg) == 0 {
		t.Fatalf("xg = %d, want %d, %v", len(xg), len(scenario.XGoals), err)
	}

	// A completed game arriving mid-run is visible on the next request.
	var next asa.Game
	for _, g := range scenario.Games {
		if g.Status == "PreMatch" {
			next = g
			break
		}
	}
	one := 1
	next.Status, next.HomeScore, next.AwayScore = "FullTime", &one, &one
	fake.UpsertGame(next)
	after, err := client.Games(ctx, asa.GamesFilters{Status: "FullTime"})
	if err != nil || len(after) != len(finished)+1 {
		t.Fatalf("after upsert = %d, want %d, %v", len(after), len(finished)+1, err)
	}
}
