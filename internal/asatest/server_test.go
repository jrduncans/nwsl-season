package asatest_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/asa"
	"github.com/jrduncans/nwsl-season/internal/asatest"
)

var base = time.Date(2026, time.March, 14, 18, 0, 0, 0, time.UTC)

func ids(games []asa.Game) []string {
	out := make([]string, len(games))
	for i, g := range games {
		out[i] = g.GameID
	}
	return out
}

func game(id, home, away, season, kickoff, status string) asa.Game {
	return asa.Game{GameID: id, HomeTeamID: home, AwayTeamID: away, SeasonName: season, DateTimeUTC: kickoff, Status: status}
}

func readFixture[T any](t *testing.T, name string) []T {
	t.Helper()
	root, err := os.OpenRoot("../asa/testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var values []T
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestTeamsFilteringAndEmptyResult(t *testing.T) {
	fake := asatest.New(t)
	fake.SetTeams([]asa.Team{{TeamID: "a", TeamName: "A"}, {TeamID: "b", TeamName: "B"}, {TeamID: "c", TeamName: "C"}})
	client := fake.Client()

	all, err := client.Teams(context.Background(), asa.TeamsFilters{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all teams = %v, %v", all, err)
	}
	some, err := client.Teams(context.Background(), asa.TeamsFilters{TeamID: "a,c"})
	if err != nil || len(some) != 2 || some[0].TeamID != "a" || some[1].TeamID != "c" {
		t.Fatalf("filtered teams = %v, %v", some, err)
	}
	none, err := client.Teams(context.Background(), asa.TeamsFilters{TeamID: "zzz"})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("unknown team = %#v, %v", none, err)
	}
}

func TestGamesFilters(t *testing.T) {
	fake := asatest.New(t)
	fake.SetGames([]asa.Game{
		game("g1", "a", "b", "2025", "2025-05-01 20:00:00 UTC", "FullTime"),
		game("g2", "b", "c", "2026", "2026-05-01 20:00:00 UTC", "FullTime"),
		game("g3", "c", "a", "2026", "2026-05-08 20:00:00 UTC", "PreMatch"),
		game("g4", "a", "d", "2026", "2026-05-15 03:00:00 UTC", "PreMatch"),
	})
	fake.SetStage("g4", "Playoffs")

	tests := []struct {
		name    string
		filters asa.GamesFilters
		want    []string
	}{
		{"unfiltered", asa.GamesFilters{}, []string{"g1", "g2", "g3", "g4"}},
		{"game id list", asa.GamesFilters{GameID: "g1,g3"}, []string{"g1", "g3"}},
		{"team home or away", asa.GamesFilters{TeamID: "a"}, []string{"g1", "g3", "g4"}},
		{"season", asa.GamesFilters{SeasonName: "2026"}, []string{"g2", "g3", "g4"}},
		{"seasons list", asa.GamesFilters{SeasonName: "2025,2026"}, []string{"g1", "g2", "g3", "g4"}},
		{"default stage", asa.GamesFilters{StageName: "Regular Season"}, []string{"g1", "g2", "g3"}},
		{"set stage", asa.GamesFilters{StageName: "Playoffs"}, []string{"g4"}},
		{"status", asa.GamesFilters{Status: "PreMatch"}, []string{"g3", "g4"}},
		{"start date inclusive", asa.GamesFilters{StartDate: "2026-05-08"}, []string{"g3", "g4"}},
		{"end date inclusive", asa.GamesFilters{EndDate: "2026-05-08"}, []string{"g1", "g2", "g3"}},
		{"date range", asa.GamesFilters{StartDate: "2026-05-02", EndDate: "2026-05-14"}, []string{"g3"}},
		{"combined", asa.GamesFilters{SeasonName: "2026", TeamID: "a", Status: "PreMatch", StageName: "Regular Season"}, []string{"g3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fake.Client().Games(context.Background(), tt.filters)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(ids(got), tt.want) {
				t.Fatalf("games = %v, want %v", ids(got), tt.want)
			}
		})
	}
}

func TestGamesRejectInvalidQueries(t *testing.T) {
	fake := asatest.New(t)
	fake.SetGames([]asa.Game{game("g1", "a", "b", "2026", "2026-05-01 20:00:00 UTC", "FullTime")})
	for name, filters := range map[string]asa.GamesFilters{
		"bad date":               {StartDate: "05/01/2026"},
		"season with date range": {SeasonName: "2026", StartDate: "2026-01-01"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fake.Client().Games(context.Background(), filters)
			if err == nil || !strings.Contains(err.Error(), "status 400") {
				t.Fatalf("err = %v, want HTTP 400", err)
			}
		})
	}
}

func TestXGoalsFilters(t *testing.T) {
	fake := asatest.New(t)
	fake.SetGames([]asa.Game{
		game("g1", "a", "b", "2025", "2025-05-01 20:00:00 UTC", "FullTime"),
		game("g2", "b", "c", "2026", "2026-05-01 20:00:00 UTC", "FullTime"),
		game("g3", "c", "a", "2026", "2026-05-08 20:00:00 UTC", "FullTime"),
	})
	fake.SetStage("g3", "Playoffs")
	fake.SetXGoals([]asa.GameXGoals{
		{GameID: "g1", HomeTeamID: "a", AwayTeamID: "b", HomeTeamXGoals: 1},
		{GameID: "g2", HomeTeamID: "b", AwayTeamID: "c", HomeTeamXGoals: 2},
		{GameID: "g3", HomeTeamID: "c", AwayTeamID: "a", HomeTeamXGoals: 3},
		{GameID: "orphan", HomeTeamID: "x", AwayTeamID: "y"},
	})

	tests := []struct {
		name    string
		filters asa.XGoalsFilters
		want    []string
	}{
		{"unfiltered includes unmatched rows", asa.XGoalsFilters{}, []string{"g1", "g2", "g3", "orphan"}},
		{"game id", asa.XGoalsFilters{GameID: "g2,orphan"}, []string{"g2", "orphan"}},
		{"season via game", asa.XGoalsFilters{SeasonName: "2026"}, []string{"g2", "g3"}},
		{"stage via game", asa.XGoalsFilters{StageName: "Playoffs"}, []string{"g3"}},
		{"season and stage", asa.XGoalsFilters{SeasonName: "2025", StageName: "Playoffs"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fake.Client().GameXGoals(context.Background(), tt.filters)
			if err != nil {
				t.Fatal(err)
			}
			var gotIDs []string
			for _, xg := range got {
				gotIDs = append(gotIDs, xg.GameID)
			}
			if !slices.Equal(gotIDs, tt.want) {
				t.Fatalf("xgoals = %v, want %v", gotIDs, tt.want)
			}
		})
	}
}

func TestFixturesRoundTripThroughClient(t *testing.T) {
	teams := readFixture[asa.Team](t, "teams.json")
	games := readFixture[asa.Game](t, "games.json")
	xgoals := readFixture[asa.GameXGoals](t, "game_xgoals.json")
	fake := asatest.New(t)
	fake.SetTeams(teams)
	fake.SetGames(games)
	fake.SetXGoals(xgoals)
	client := fake.Client()

	gotTeams, err := client.Teams(context.Background(), asa.TeamsFilters{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range gotTeams {
		gotTeams[i].RawJSON = ""
	}
	gotGames, err := client.Games(context.Background(), asa.GamesFilters{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range gotGames {
		gotGames[i].RawJSON = ""
	}
	gotXG, err := client.GameXGoals(context.Background(), asa.XGoalsFilters{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range gotXG {
		gotXG[i].RawJSON = ""
	}

	// The fixtures carry fields the wire types omit (such as xG player
	// columns), so compare decoded values rather than raw JSON.
	if !equalJSON(t, gotTeams, teams) || !equalJSON(t, gotGames, games) || !equalJSON(t, gotXG, xgoals) {
		t.Fatalf("fixtures changed in transit:\nteams %v\ngames %v\nxg %v", gotTeams, gotGames, gotXG)
	}
	if len(gotTeams) == 0 || len(gotGames) == 0 || len(gotXG) == 0 {
		t.Fatal("fixtures were empty")
	}
}

func equalJSON(t *testing.T, a, b any) bool {
	t.Helper()
	left, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(left) == string(right)
}

func TestFailNextReturnsStatusThenRecovers(t *testing.T) {
	fake := asatest.New(t)
	fake.SetTeams([]asa.Team{{TeamID: "a"}})
	fake.FailNext(asatest.PathTeams, http.StatusInternalServerError, 2)
	fake.FailNext(asatest.PathTeams, http.StatusTooManyRequests, 1)
	client := fake.Client()

	for _, want := range []string{"status 500", "status 500", "status 429"} {
		if _, err := client.Teams(context.Background(), asa.TeamsFilters{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want %s", err, want)
		}
	}
	if teams, err := client.Teams(context.Background(), asa.TeamsFilters{}); err != nil || len(teams) != 1 {
		t.Fatalf("after failures: %v, %v", teams, err)
	}
	// Failures are per path.
	fake.FailNext(asatest.PathGames, http.StatusBadGateway, 1)
	if _, err := client.Teams(context.Background(), asa.TeamsFilters{}); err != nil {
		t.Fatalf("teams affected by games failure: %v", err)
	}
	if _, err := client.Games(context.Background(), asa.GamesFilters{}); err == nil || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("games err = %v, want 502", err)
	}
}

func TestClientRetriesThroughInjectedFailure(t *testing.T) {
	fake := asatest.New(t)
	fake.SetTeams([]asa.Team{{TeamID: "a"}})
	fake.FailNext(asatest.PathTeams, http.StatusServiceUnavailable, 1)
	client := fake.Client()
	client.RetryDelays = []time.Duration{time.Millisecond}

	teams, err := client.Teams(context.Background(), asa.TeamsFilters{})
	if err != nil || len(teams) != 1 {
		t.Fatalf("teams = %v, %v", teams, err)
	}
	if got := len(fake.Requests()); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestSetDownFailsTransportThenRecovers(t *testing.T) {
	fake := asatest.New(t)
	fake.SetTeams([]asa.Team{{TeamID: "a"}})
	client := fake.Client()

	fake.SetDown(true)
	for _, call := range []func() error{
		func() error { _, err := client.Teams(context.Background(), asa.TeamsFilters{}); return err },
		func() error { _, err := client.Games(context.Background(), asa.GamesFilters{}); return err },
		func() error { _, err := client.GameXGoals(context.Background(), asa.XGoalsFilters{}); return err },
	} {
		if err := call(); err == nil || strings.Contains(err.Error(), "unexpected HTTP status") {
			t.Fatalf("err = %v, want a transport error", err)
		}
	}
	if got := len(fake.Requests()); got != 3 {
		t.Fatalf("down requests recorded = %d, want 3", got)
	}

	fake.SetDown(false)
	if teams, err := client.Teams(context.Background(), asa.TeamsFilters{}); err != nil || len(teams) != 1 {
		t.Fatalf("after recovery: %v, %v", teams, err)
	}
}

func TestRequestsRecordedAndReset(t *testing.T) {
	fake := asatest.New(t)
	client := fake.Client()
	ctx := context.Background()

	_, _ = client.Teams(ctx, asa.TeamsFilters{TeamID: "a"})
	_, _ = client.Games(ctx, asa.GamesFilters{SeasonName: "2026", Status: "FullTime"})
	_, _ = client.GameXGoals(ctx, asa.XGoalsFilters{})

	got := fake.Requests()
	if len(got) != 3 {
		t.Fatalf("requests = %v", got)
	}
	if got[0].Path != asatest.PathTeams || got[0].Query.Get("team_id") != "a" {
		t.Fatalf("request 0 = %+v", got[0])
	}
	if got[1].Path != asatest.PathGames || got[1].Query.Get("season_name") != "2026" || got[1].Query.Get("status") != "FullTime" {
		t.Fatalf("request 1 = %+v", got[1])
	}
	if got[2].Path != asatest.PathXGoals || len(got[2].Query) != 0 {
		t.Fatalf("request 2 = %+v", got[2])
	}

	// Mutating the returned copy must not change the recorded traffic.
	got[0].Query.Set("team_id", "tampered")
	if fake.Requests()[0].Query.Get("team_id") != "a" {
		t.Fatal("Requests exposed internal state")
	}

	fake.ResetRequests()
	if n := len(fake.Requests()); n != 0 {
		t.Fatalf("requests after reset = %d", n)
	}
}

func TestUnknownPathsReturn404AndAreRecorded(t *testing.T) {
	fake := asatest.New(t)
	for _, path := range []string{"/nwsl/players", "/nwsl/games/extra", "/"} {
		response, err := http.Get(fake.URL() + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, response.StatusCode)
		}
	}
	requests := fake.Requests()
	if len(requests) != 3 || requests[0].Path != "/nwsl/players" {
		t.Fatalf("requests = %+v", requests)
	}

	// A client call to a real path is distinguishable from the unknown ones.
	fake.ResetRequests()
	_, _ = fake.Client().Teams(context.Background(), asa.TeamsFilters{})
	for _, r := range fake.Requests() {
		if r.Path != asatest.PathTeams {
			t.Fatalf("unexpected path %q", r.Path)
		}
	}
}

func TestServesWithoutBasePrefixAndRejectsNonGet(t *testing.T) {
	fake := asatest.New(t)
	fake.SetTeams([]asa.Team{{TeamID: "a"}})
	root := strings.TrimSuffix(fake.URL(), "/api/v1")

	response, err := http.Get(root + asatest.PathTeams)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unprefixed status = %d", response.StatusCode)
	}

	response, err = http.Post(fake.URL()+asatest.PathTeams, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", response.StatusCode)
	}
}

func TestUpsertGameReplacesOrAppends(t *testing.T) {
	fake := asatest.New(t)
	fake.SetGames([]asa.Game{game("g1", "a", "b", "2026", "2026-05-01 20:00:00 UTC", "PreMatch")})
	three := 3
	played := game("g1", "a", "b", "2026", "2026-05-01 20:00:00 UTC", "FullTime")
	played.HomeScore = &three
	fake.UpsertGame(played)
	fake.UpsertGame(game("g2", "b", "a", "2026", "2026-05-08 20:00:00 UTC", "PreMatch"))

	got, err := fake.Client().Games(context.Background(), asa.GamesFilters{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(got), []string{"g1", "g2"}) || got[0].Status != "FullTime" || got[0].HomeScore == nil || *got[0].HomeScore != 3 {
		t.Fatalf("games = %+v", got)
	}
}

func TestSettersCopyInput(t *testing.T) {
	fake := asatest.New(t)
	games := []asa.Game{game("g1", "a", "b", "2026", "2026-05-01 20:00:00 UTC", "PreMatch")}
	fake.SetGames(games)
	games[0].GameID = "mutated"

	got, err := fake.Client().Games(context.Background(), asa.GamesFilters{})
	if err != nil || len(got) != 1 || got[0].GameID != "g1" {
		t.Fatalf("games = %+v, %v", got, err)
	}
}

func TestConcurrentUseIsRaceFree(t *testing.T) {
	fake := asatest.New(t)
	fake.SetTeams([]asa.Team{{TeamID: "a"}})
	client := fake.Client()
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 20 {
				id := fmt.Sprintf("g-%d-%d", worker, i)
				fake.UpsertGame(game(id, "a", "b", "2026", "2026-05-01 20:00:00 UTC", "PreMatch"))
				fake.SetStage(id, "Playoffs")
				fake.SetXGoals([]asa.GameXGoals{{GameID: id}})
				fake.FailNext(asatest.PathXGoals, http.StatusInternalServerError, 1)
				_, _ = client.Games(context.Background(), asa.GamesFilters{StageName: "Playoffs"})
				_, _ = client.GameXGoals(context.Background(), asa.XGoalsFilters{SeasonName: "2026"})
				_ = fake.Requests()
				fake.SetDown(i%7 == 0)
				if i%10 == 0 {
					fake.ResetRequests()
				}
			}
		}()
	}
	wg.Wait()
}
