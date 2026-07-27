package backtest

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/forecast"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestEvaluateWalksForwardAndIsByteStable(t *testing.T) {
	season := tinySeason()
	cfg := Config{
		Models: []forecast.Model{forecast.NewCurrentPaceV1(), forecast.NewResultsPoissonV1()}, IncumbentModelID: "results-poisson-v1",
		Iterations: 100, BootstrapResamples: 100, BootstrapSeed: 42, GeneratedAt: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
	}
	first, err := Evaluate(context.Background(), []Season{season}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluate(context.Background(), []Season{season}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := JSON(first)
	secondJSON, _ := JSON(second)
	if string(firstJSON) != string(secondJSON) {
		index := 0
		for index < len(firstJSON) && index < len(secondJSON) && firstJSON[index] == secondJSON[index] {
			index++
		}
		start, end := index-80, index+120
		if start < 0 {
			start = 0
		}
		if end > len(firstJSON) {
			end = len(firstJSON)
		}
		t.Fatalf("fixed inputs differed at byte %d\nfirst: %s\nsecond: %s", index, firstJSON[start:end], secondJSON[start:end])
	}
	if first.Status != "complete" || len(first.Seasons) != 1 || !first.Seasons[0].Included {
		t.Fatalf("report audit = %+v", first.Seasons)
	}
	if got := first.Models[0].Windows[HeldoutWindow].Metrics.MatchLogLoss.Count; got != 6 {
		t.Fatalf("match count = %d, want 6", got)
	}
	if got := first.Models[0].Windows[HeldoutWindow].Metrics.PlayoffBrier.Count; got != 9 {
		t.Fatalf("team/cutoff count = %d, want 9", got)
	}
	if len(first.Models[0].Windows[HeldoutWindow].Metrics.PlayoffCalibration) != 10 {
		t.Fatal("calibration must contain ten stable bins")
	}
	if len(first.Comparisons) != 1 || first.Comparisons[0].Metrics["match_log_loss"].Blocks != 3 {
		t.Fatalf("comparisons = %+v", first.Comparisons)
	}
}

func TestCutoffHidesSameDayAndFutureResultsAndXG(t *testing.T) {
	season := tinySeason()
	date := time.Date(2025, 3, 8, 0, 0, 0, 0, time.UTC)
	games, today, completed := cutoff(season.Games, date)
	if completed != 2 || len(today) != 2 {
		t.Fatalf("completed=%d today=%d", completed, len(today))
	}
	xg := cutoffXG(season.XGoals, games)
	if len(xg) != 2 {
		t.Fatalf("xG rows = %d, want 2", len(xg))
	}

	mutated := tinySeason()
	*mutated.Games[2].HomeScore = 99
	mutated.XGoals[mutated.Games[2].ID] = forecast.ExpectedGoals{GameID: mutated.Games[2].ID, Home: 99, Away: 99}
	mutatedGames, _, _ := cutoff(mutated.Games, date)
	mutatedXG := cutoffXG(mutated.XGoals, mutatedGames)
	if !reflect.DeepEqual(games, mutatedGames) || !reflect.DeepEqual(xg, mutatedXG) {
		t.Fatal("same-day values leaked into cutoff training input")
	}
}

func TestEvaluateReportsInvalidSeasonWithoutRunningIt(t *testing.T) {
	season := tinySeason()
	season.Games[0].Status = "PreMatch"
	season.Games[0].HomeScore, season.Games[0].AwayScore = nil, nil
	report, err := Evaluate(context.Background(), []Season{season}, Config{
		Models: []forecast.Model{forecast.NewResultsPoissonV1()}, IncumbentModelID: "results-poisson-v1",
		Iterations: 10, BootstrapResamples: 10, GeneratedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "incomplete" || report.Seasons[0].Included || !strings.Contains(report.Seasons[0].ExclusionReason, "remains PreMatch") {
		t.Fatalf("report = %+v", report)
	}
}

func TestHistoricalPlayoffPlaces(t *testing.T) {
	for season, want := range map[string]int{"2016": 4, "2019": 4, "2021": 6, "2023": 6, "2024": 8, "2025": 8} {
		got, ok := HistoricalPlayoffPlaces(season)
		if !ok || got != want {
			t.Fatalf("%s: got %d, %t; want %d", season, got, ok, want)
		}
	}
	for _, season := range []string{"2020", "2026", "bad"} {
		if _, ok := HistoricalPlayoffPlaces(season); ok {
			t.Fatalf("unexpected rules for %s", season)
		}
	}
}

func TestSelectionChoosesLowestLogLossAmongQualifyingCandidates(t *testing.T) {
	report := Report{
		IncumbentModel: "incumbent",
		Models: []ModelResult{
			{ID: "incumbent", Windows: map[string]WindowResult{HeldoutWindow: {Metrics: selectionMetrics(1)}}},
			{ID: "candidate-a", Windows: map[string]WindowResult{HeldoutWindow: {Metrics: selectionMetrics(.9)}}},
			{ID: "candidate-b", Windows: map[string]WindowResult{HeldoutWindow: {Metrics: selectionMetrics(.8)}}},
		},
		Comparisons: []Comparison{
			{Candidate: "candidate-a", Metrics: map[string]Interval{"match_log_loss": {High: -.01, Blocks: 10}}},
			{Candidate: "candidate-b", Metrics: map[string]Interval{"match_log_loss": {High: -.02, Blocks: 10}}},
		},
	}
	selection := selectModel(report, true)
	if selection.SelectedModel != "candidate-b" || !selection.Candidates[0].Qualified || !selection.Candidates[1].Qualified {
		t.Fatalf("selection = %+v", selection)
	}
}

func selectionMetrics(logLoss float64) MetricSet {
	return MetricSet{
		MatchLogLoss: Score{Mean: logLoss}, PlayoffBrier: Score{Mean: .1}, ShieldBrier: Score{Mean: .1},
		PointsCRPS: Score{Mean: 1}, PositionRPS: Score{Mean: .1},
	}
}

func tinySeason() Season {
	teams := []standings.Team{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}, {ID: "c", Name: "C"}}
	games := []Game{
		historicalGame("1", "2025-03-01T18:00:00Z", "a", "b", 1, 0),
		historicalGame("2", "2025-03-01T21:00:00Z", "c", "a", 1, 1),
		historicalGame("3", "2025-03-08T18:00:00Z", "b", "c", 0, 2),
		historicalGame("4", "2025-03-08T21:00:00Z", "b", "a", 2, 2),
		historicalGame("5", "2025-03-15T18:00:00Z", "a", "c", 0, 1),
		historicalGame("6", "2025-03-15T21:00:00Z", "c", "b", 3, 1),
	}
	xg := map[string]forecast.ExpectedGoals{}
	for _, game := range games {
		xg[game.ID] = forecast.ExpectedGoals{GameID: game.ID, Home: 1.2, Away: .9}
	}
	return Season{ID: "2025", Window: HeldoutWindow, PlayoffPlaces: 1, Teams: teams, Games: games, XGoals: xg}
}

func historicalGame(id, kickoff, home, away string, homeScore, awayScore int) Game {
	parsed, _ := time.Parse(time.RFC3339, kickoff)
	h, a := homeScore, awayScore
	return Game{Game: standings.Game{ID: id, Status: standings.CompletedStatus, HomeTeamID: home, AwayTeamID: away, HomeScore: &h, AwayScore: &a}, Kickoff: parsed}
}
