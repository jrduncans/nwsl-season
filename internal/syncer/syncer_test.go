package syncer

import (
	"context"
	"errors"
	"testing"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/cache"
)

func TestRunIsIdempotentAndUpdatesGames(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{
			testGame("game-1", "FullTime", ptr(1), ptr(0)),
			testGame("game-2", "PreMatch", nil, nil),
		},
	}
	service := Service{ASA: &client, Store: db}

	first, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if first.GamesUpserted != 2 || first.GamesDeleted != 0 {
		t.Fatalf("first run counts = %+v, want 2 upserted and 0 deleted", first)
	}

	client.games[0] = testGame("game-1", "FullTime", ptr(2), ptr(2))
	second, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if second.GamesUpserted != 2 || second.GamesDeleted != 0 {
		t.Fatalf("second run counts = %+v, want 2 upserted and 0 deleted", second)
	}

	count, err := db.CountGames(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("cached game count = %d, want 2", count)
	}

	game, err := db.GameByID(ctx, "game-1")
	if err != nil {
		t.Fatal(err)
	}
	if !game.HomeScore.Valid || game.HomeScore.Int64 != 2 {
		t.Fatalf("home score = %+v, want 2", game.HomeScore)
	}
	if !game.AwayScore.Valid || game.AwayScore.Int64 != 2 {
		t.Fatalf("away score = %+v, want 2", game.AwayScore)
	}
}

func TestRunHardDeletesMissingGamesAfterSuccessfulFetch(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{
			testGame("game-1", "FullTime", ptr(1), ptr(0)),
			testGame("game-2", "PreMatch", nil, nil),
		},
	}
	service := Service{ASA: &client, Store: db}

	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}

	client.games = []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))}
	run, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if run.GamesDeleted != 1 {
		t.Fatalf("deleted games = %d, want 1", run.GamesDeleted)
	}

	count, err := db.CountGames(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("cached game count = %d, want 1", count)
	}
}

func TestRunRejectsEmptyGamesAndPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	service := Service{ASA: &client, Store: db}

	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}

	client.games = []asa.Game{}
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("err = nil, want validation error")
	}

	count, err := db.CountGames(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("cached game count = %d, want existing row preserved", count)
	}

	status, err := db.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.LastAttempt == nil || status.LastAttempt.Outcome != "failure" {
		t.Fatalf("last attempt = %+v, want failure", status.LastAttempt)
	}
	if status.LastSuccess == nil || status.LastSuccess.Outcome != "success" {
		t.Fatalf("last success = %+v, want success", status.LastSuccess)
	}
}

func TestRunRecordsFetchFailureAndPreservesExistingRows(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	service := Service{ASA: &client, Store: db}

	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}

	client.gamesErr = errors.New("upstream down")
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("err = nil, want fetch error")
	}

	count, err := db.CountGames(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("cached game count = %d, want existing row preserved", count)
	}
}

func newTestDB(t *testing.T) *cache.DB {
	t.Helper()
	db, err := cache.Open(context.Background(), t.TempDir()+"/cache.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return db
}

type fakeASA struct {
	teams    []asa.Team
	teamsErr error
	games    []asa.Game
	gamesErr error
}

func (f *fakeASA) Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error) {
	if f.teamsErr != nil {
		return nil, f.teamsErr
	}
	return f.teams, nil
}

func (f *fakeASA) Games(context.Context, asa.GamesFilters) ([]asa.Game, error) {
	if f.gamesErr != nil {
		return nil, f.gamesErr
	}
	return f.games, nil
}

func testTeams() []asa.Team {
	return []asa.Team{
		{TeamID: "home", TeamName: "Home FC", TeamShortName: "Home", TeamAbbreviation: "HOM", RawJSON: `{"team_id":"home","extra":"kept"}`},
		{TeamID: "away", TeamName: "Away FC", TeamShortName: "Away", TeamAbbreviation: "AWY", RawJSON: `{"team_id":"away"}`},
	}
}

func testGame(id, status string, homeScore, awayScore *int) asa.Game {
	return asa.Game{
		GameID:         id,
		DateTimeUTC:    "2024-11-03 22:30:00 UTC",
		HomeScore:      homeScore,
		AwayScore:      awayScore,
		HomeTeamID:     "home",
		AwayTeamID:     "away",
		SeasonName:     "2024",
		Status:         status,
		LastUpdatedUTC: "2024-11-07 15:57:43 UTC",
		RawJSON:        `{"game_id":"` + id + `","extra":"kept"}`,
	}
}

func ptr(value int) *int {
	return &value
}
