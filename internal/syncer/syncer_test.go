package syncer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

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
	if got, want := client.gamesFilters.Status, "Abandoned,FullTime,PreMatch"; got != want {
		t.Fatalf("game status filter = %q, want %q", got, want)
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

	count := cachedGameCount(t, ctx, db, "2024", "Regular Season")
	if count != 2 {
		t.Fatalf("cached game count = %d, want 2", count)
	}

	game := cachedGame(t, ctx, db, "2024", "Regular Season", "game-1")
	if !game.HomeScore.Valid || game.HomeScore.Int64 != 2 {
		t.Fatalf("home score = %+v, want 2", game.HomeScore)
	}
	if !game.AwayScore.Valid || game.AwayScore.Int64 != 2 {
		t.Fatalf("away score = %+v, want 2", game.AwayScore)
	}
}

func TestRunRefreshesXGBeforeDerivedCalculations(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	order := []string{}
	client := traceASA{fakeASA: fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}, order: &order, xg: []asa.GameXGoals{{GameID: "game-1", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 1.2, AwayTeamXGoals: 0.6}}}
	service := Service{
		ASA:           &client,
		Store:         db,
		Qualification: orderedRefresher{order: &order, label: "qualification"},
		Scenarios:     orderedRefresher{order: &order, label: "scenarios"},
	}
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"xg", "qualification", "scenarios"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("refresh order = %v, want %v", order, want)
	}
}

func TestMapXGoalsPreservesExpectedPoints(t *testing.T) {
	homePoints, awayPoints := 2.47, .367
	values, err := mapXGoals([]asa.GameXGoals{{GameID: "game-1", HomeTeamID: "home", AwayTeamID: "away", HomeTeamXGoals: 2.36, AwayTeamXGoals: 1.11, HomeXPoints: &homePoints, AwayXPoints: &awayPoints}})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || !values[0].HomeXPoints.Valid || !values[0].AwayXPoints.Valid {
		t.Fatalf("mapped values = %+v, want expected points", values)
	}
	if values[0].HomeXPoints.Float64 != homePoints || values[0].AwayXPoints.Float64 != awayPoints {
		t.Fatalf("expected points = %+v, want %.3f / %.3f", values[0], homePoints, awayPoints)
	}
}

func TestRecalculateUsesCachedFixturesWithoutCallingASA(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	if _, err := (Service{ASA: &client, Store: db}).Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err != nil {
		t.Fatal(err)
	}
	lastSync, err := db.LastAttempt(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}

	order := []string{}
	qualificationForced, scenariosForced := false, false
	service := Service{
		Store:         db,
		Qualification: orderedRefresher{order: &order, label: "qualification", forced: &qualificationForced},
		Scenarios:     orderedRefresher{order: &order, label: "scenarios", forced: &scenariosForced},
	}
	run, err := service.Recalculate(ctx, RecalculateOptions{Season: "2024", Stage: "Regular Season", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"qualification", "scenarios"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("refresh order = %v, want %v", order, want)
	}
	if !run.QualificationRecalculated || !run.ScenarioRecalculated {
		t.Fatalf("recalculation flags = qualification %t scenarios %t, want both true", run.QualificationRecalculated, run.ScenarioRecalculated)
	}
	if !qualificationForced || !scenariosForced {
		t.Fatalf("force flags = qualification %t scenarios %t, want both true", qualificationForced, scenariosForced)
	}
	if client.teamsCalls != 1 || client.gamesCalls != 1 {
		t.Fatalf("ASA calls = teams %d games %d, want unchanged at 1 each", client.teamsCalls, client.gamesCalls)
	}
	after, err := db.LastAttempt(ctx, "2024", "Regular Season")
	if err != nil {
		t.Fatal(err)
	}
	if after == nil || lastSync == nil || after.ID != lastSync.ID {
		t.Fatalf("last sync changed from %+v to %+v", lastSync, after)
	}
}

func TestRunSkipsRecentSuccessfulSync(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	client := fakeASA{
		teams: testTeams(),
		games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))},
	}
	service := Service{ASA: &client, Store: db}

	first, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"})
	if err != nil {
		t.Fatal(err)
	}
	if client.teamsCalls != 1 || client.gamesCalls != 1 {
		t.Fatalf("ASA calls after first run = teams %d games %d, want 1 and 1", client.teamsCalls, client.gamesCalls)
	}

	client.games[0] = testGame("game-1", "FullTime", ptr(3), ptr(0))
	second, err := service.Run(ctx, RunOptions{
		Season:                 "2024",
		Stage:                  "Regular Season",
		MinimumAttemptInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("second run ID = %d, want skipped run to return previous ID %d", second.ID, first.ID)
	}
	if !second.Skipped {
		t.Fatal("second run was not marked skipped")
	}
	if client.teamsCalls != 1 || client.gamesCalls != 1 {
		t.Fatalf("ASA calls after skipped run = teams %d games %d, want still 1 and 1", client.teamsCalls, client.gamesCalls)
	}

	game := cachedGame(t, ctx, db, "2024", "Regular Season", "game-1")
	if !game.HomeScore.Valid || game.HomeScore.Int64 != 1 {
		t.Fatalf("home score = %+v, want cached score preserved", game.HomeScore)
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

	count := cachedGameCount(t, ctx, db, "2024", "Regular Season")
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

	count := cachedGameCount(t, ctx, db, "2024", "Regular Season")
	if count != 1 {
		t.Fatalf("cached game count = %d, want existing row preserved", count)
	}

	status, err := db.Status(ctx, "2024", "Regular Season")
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

	count := cachedGameCount(t, ctx, db, "2024", "Regular Season")
	if count != 1 {
		t.Fatalf("cached game count = %d, want existing row preserved", count)
	}
}

func TestRunRateLimitsFailedAttemptAndForceBypassesIt(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	client := fakeASA{teams: testTeams(), games: []asa.Game{testGame("game-1", "FullTime", ptr(1), ptr(0))}}
	service := Service{ASA: &client, Store: db}

	client.teamsErr = errors.New("upstream down")
	if _, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season"}); err == nil {
		t.Fatal("first run error = nil, want failure")
	}
	if client.teamsCalls != 1 {
		t.Fatalf("teams calls = %d, want 1", client.teamsCalls)
	}

	client.teamsErr = nil
	skipped, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season", MinimumAttemptInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if !skipped.Skipped || skipped.Outcome != "failure" || client.teamsCalls != 1 {
		t.Fatalf("rate-limited run = %+v; teams calls = %d, want skipped failed attempt and 1 call", skipped, client.teamsCalls)
	}

	forced, err := service.Run(ctx, RunOptions{Season: "2024", Stage: "Regular Season", MinimumAttemptInterval: time.Hour, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if forced.Skipped || client.teamsCalls != 2 || client.gamesCalls != 1 {
		t.Fatalf("forced run = %+v; calls teams=%d games=%d, want completed refresh", forced, client.teamsCalls, client.gamesCalls)
	}
}

func cachedGameCount(t *testing.T, ctx context.Context, db *cache.DB, season, stage string) int {
	t.Helper()
	data, err := db.Season(ctx, season, stage)
	if err != nil {
		t.Fatal(err)
	}
	return len(data.Games)
}

func cachedGame(t *testing.T, ctx context.Context, db *cache.DB, season, stage, id string) cache.Game {
	t.Helper()
	data, err := db.Season(ctx, season, stage)
	if err != nil {
		t.Fatal(err)
	}
	for _, game := range data.Games {
		if game.ASAID == id {
			return game
		}
	}
	t.Fatalf("cached game %q not found", id)
	return cache.Game{}
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
	teams        []asa.Team
	teamsErr     error
	teamsCalls   int
	games        []asa.Game
	gamesErr     error
	gamesCalls   int
	gamesFilters asa.GamesFilters
}

type traceASA struct {
	fakeASA
	order *[]string
	xg    []asa.GameXGoals
}

func (f *traceASA) GameXGoals(context.Context, asa.XGoalsFilters) ([]asa.GameXGoals, error) {
	*f.order = append(*f.order, "xg")
	return f.xg, nil
}

type orderedRefresher struct {
	order  *[]string
	label  string
	forced *bool
}

func (r orderedRefresher) Refresh(_ context.Context, _ cache.SyncRun, _ []cache.Team, _ []cache.Game, force bool) (bool, error) {
	*r.order = append(*r.order, r.label)
	if r.forced != nil {
		*r.forced = force
	}
	return true, nil
}

func (f *fakeASA) Teams(context.Context, asa.TeamsFilters) ([]asa.Team, error) {
	f.teamsCalls++
	if f.teamsErr != nil {
		return nil, f.teamsErr
	}
	return f.teams, nil
}

func (f *fakeASA) Games(_ context.Context, filters asa.GamesFilters) ([]asa.Game, error) {
	f.gamesCalls++
	f.gamesFilters = filters
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
