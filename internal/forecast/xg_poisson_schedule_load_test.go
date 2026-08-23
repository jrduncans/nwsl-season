package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/jrduncans/nwsl-season/internal/fixtures"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestRecoveryPressureSaturatesBetweenSixAndFiveDays(t *testing.T) {
	for _, test := range []struct {
		name string
		rest time.Duration
		want float64
	}{
		{name: "fully rested", rest: 7 * 24 * time.Hour, want: 0},
		{name: "just above six days", rest: recoveryStart + time.Nanosecond, want: 0},
		{name: "six days", rest: recoveryStart, want: 0},
		{name: "just below six days", rest: recoveryStart - time.Nanosecond, want: float64(time.Nanosecond) / float64(24*time.Hour)},
		{name: "five and a half days", rest: 11 * 12 * time.Hour, want: 0.5},
		{name: "Friday to late Wednesday", rest: 5*24*time.Hour + 90*time.Minute, want: 0.9375},
		{name: "five days", rest: recoveryFull, want: 1},
		{name: "just above five days", rest: recoveryFull + time.Nanosecond, want: 1 - float64(time.Nanosecond)/float64(24*time.Hour)},
		{name: "shorter rest", rest: 3 * 24 * time.Hour, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := recoveryPressure(test.rest); math.Abs(got-test.want) > 1e-12 {
				t.Fatalf("recoveryPressure(%s) = %.3f, want %.3f", test.rest, got, test.want)
			}
		})
	}
}

func TestScheduleLoadsNineDayBoundaryAndFourthMatch(t *testing.T) {
	start := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	loadAt := func(span time.Duration) teamScheduleLoad {
		t.Helper()
		loads, err := scheduleLoads([]standings.Game{
			{ID: "first", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: start},
			{ID: "second", Status: standings.CompletedStatus, HomeTeamID: "charlie", AwayTeamID: "alpha", Kickoff: start.Add(4 * 24 * time.Hour)},
			{ID: "target", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "delta", Kickoff: start.Add(span)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return loads["target"].home
	}
	if load := loadAt(loadWindow); !load.third {
		t.Fatal("third match at exactly nine days was not accumulated load")
	}
	if load := loadAt(loadWindow + time.Nanosecond); load.third {
		t.Fatal("third match beyond nine days was accumulated load")
	}

	loads, err := scheduleLoads([]standings.Game{
		{ID: "one", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: start},
		{ID: "two", Status: standings.CompletedStatus, HomeTeamID: "charlie", AwayTeamID: "alpha", Kickoff: start.Add(3 * 24 * time.Hour)},
		{ID: "three", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "delta", Kickoff: start.Add(6 * 24 * time.Hour)},
		{ID: "four", Status: fixtures.PreMatchStatus, HomeTeamID: "echo", AwayTeamID: "alpha", Kickoff: start.Add(9 * 24 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !loads["four"].away.third {
		t.Fatal("fourth match with two prior appearances inside nine days was not accumulated load")
	}
}

func TestScheduleLoadsUsePriorPlayableFixturesAndNineDaySpan(t *testing.T) {
	start := time.Date(2026, 4, 24, 21, 30, 0, 0, time.UTC)
	games := []standings.Game{
		{ID: "third", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "delta", Kickoff: start.Add(8*24*time.Hour + 23*time.Hour)},
		{ID: "abandoned", Status: fixtures.AbandonedStatus, HomeTeamID: "alpha", AwayTeamID: "echo", Kickoff: start.Add(4 * 24 * time.Hour)},
		{ID: "first", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: start},
		{ID: "second", Status: fixtures.PreMatchStatus, HomeTeamID: "charlie", AwayTeamID: "alpha", Kickoff: start.Add(5*24*time.Hour + 12*time.Hour)},
	}

	loads, err := scheduleLoads(games)
	if err != nil {
		t.Fatal(err)
	}
	second := loads["second"].away
	if math.Abs(second.recovery-0.5) > 1e-12 || second.third {
		t.Fatalf("second appearance load = %+v, want 0.5 recovery without accumulated load", second)
	}
	third := loads["third"].home
	if third.recovery != 1 || !third.third {
		t.Fatalf("third appearance load = %+v, want full recovery pressure and third-in-nine-days", third)
	}
	if _, exists := loads["abandoned"]; exists {
		t.Fatal("abandoned fixture contributed schedule load")
	}
}

func TestXGPoissonScheduleLoadAdjustsAttackAndDefence(t *testing.T) {
	if got := NewXGPoissonScheduleLoadV1().Info().ID; got != "xg-poisson-schedule-load-v1" {
		t.Fatalf("model ID = %q; changing frozen behavior requires a new version", got)
	}
	start := time.Date(2026, 7, 24, 20, 0, 0, 0, time.UTC)
	input := FitInput{
		Teams: []standings.Team{{ID: "alpha"}, {ID: "bravo"}, {ID: "charlie"}, {ID: "delta"}},
		Games: []standings.Game{
			{ID: "first", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "charlie", Kickoff: start},
			{ID: "second", Status: fixtures.PreMatchStatus, HomeTeamID: "delta", AwayTeamID: "alpha", Kickoff: start.Add(5 * 24 * time.Hour)},
			{ID: "target", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: start.Add(8 * 24 * time.Hour)},
		},
	}
	predictor, err := NewXGPoissonScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	distribution, err := predictor.Distribution(input.Games[2])
	if err != nil {
		t.Fatal(err)
	}
	poisson := distribution.(poissonDistribution)
	// Pin the v1 coefficient sum: full recovery pressure (0.04) plus a
	// third-in-nine-days load (0.075), split symmetrically between teams.
	const congestion = 0.115
	wantHome := priorHomeGoals * math.Exp(-congestion/2)
	wantAway := priorAwayGoals * math.Exp(congestion/2)
	if math.Abs(poisson.homeRate-wantHome) > 1e-12 || math.Abs(poisson.awayRate-wantAway) > 1e-12 {
		t.Fatalf("rates = %.6f/%.6f, want %.6f/%.6f", poisson.homeRate, poisson.awayRate, wantHome, wantAway)
	}
}

func TestXGPoissonScheduleLoadSeedChangesWithScheduledKickoff(t *testing.T) {
	start := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	input := FitInput{
		Teams: teams(),
		Games: []standings.Game{{ID: "future", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: start}},
	}
	first, err := NewXGPoissonScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Games[0].Kickoff = start.Add(time.Hour)
	second, err := NewXGPoissonScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.SeedMaterial()) == string(second.SeedMaterial()) {
		t.Fatal("seed material did not change with a scheduled kickoff")
	}
}

func TestXGPoissonScheduleLoadIsTeamSpecificAndSymmetric(t *testing.T) {
	base := xgPredictor{
		teams:    map[string]xgTotals{"alpha": {}, "bravo": {}},
		homeRate: priorHomeGoals, awayRate: priorAwayGoals,
		leagueRate: (priorHomeGoals + priorAwayGoals) / 2,
	}
	loaded := teamScheduleLoad{recovery: 1, third: true}
	fixture := standings.Game{ID: "target", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "bravo"}
	newPredictor := func(load fixtureScheduleLoad) scheduleLoadXGPredictor {
		return scheduleLoadXGPredictor{base: base, fixtures: map[string]fixtureScheduleLoad{"target": load}}
	}
	congestion := recoveryLogShift + accumulatedLoadLogShift
	for _, test := range []struct {
		name           string
		load           fixtureScheduleLoad
		homeMultiplier float64
		awayMultiplier float64
	}{
		{name: "home loaded", load: fixtureScheduleLoad{home: loaded}, homeMultiplier: math.Exp(-congestion / 2), awayMultiplier: math.Exp(congestion / 2)},
		{name: "away loaded", load: fixtureScheduleLoad{away: loaded}, homeMultiplier: math.Exp(congestion / 2), awayMultiplier: math.Exp(-congestion / 2)},
		{name: "both equally loaded", load: fixtureScheduleLoad{home: loaded, away: loaded}, homeMultiplier: 1, awayMultiplier: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			distribution, err := newPredictor(test.load).Distribution(fixture)
			if err != nil {
				t.Fatal(err)
			}
			poisson := distribution.(poissonDistribution)
			if math.Abs(poisson.homeRate-priorHomeGoals*test.homeMultiplier) > 1e-12 || math.Abs(poisson.awayRate-priorAwayGoals*test.awayMultiplier) > 1e-12 {
				t.Fatalf("rates = %.6f/%.6f", poisson.homeRate, poisson.awayRate)
			}
		})
	}
}

func TestXGPoissonScheduleLoadValidatesScheduleContext(t *testing.T) {
	start := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		games []standings.Game
	}{
		{name: "missing kickoff", games: []standings.Game{{ID: "missing", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "bravo"}}},
		{name: "duplicate team kickoff", games: []standings.Game{
			{ID: "one", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: start},
			{ID: "two", Status: fixtures.PreMatchStatus, HomeTeamID: "charlie", AwayTeamID: "alpha", Kickoff: start},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewXGPoissonScheduleLoadV1().Fit(FitInput{
				Teams: []standings.Team{{ID: "alpha"}, {ID: "bravo"}, {ID: "charlie"}}, Games: test.games,
			})
			if err == nil {
				t.Fatal("Fit accepted invalid schedule context")
			}
		})
	}

	predictor := scheduleLoadXGPredictor{base: xgPredictor{teams: map[string]xgTotals{"alpha": {}, "bravo": {}}, leagueRate: 1, homeRate: 1, awayRate: 1}, fixtures: map[string]fixtureScheduleLoad{}}
	if _, err := predictor.Distribution(standings.Game{ID: "absent", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "bravo"}); err == nil {
		t.Fatal("Distribution accepted a fixture absent from schedule context")
	}
}

func TestXGPoissonScheduleLoadIsInputOrderInvariant(t *testing.T) {
	start := time.Date(2026, 4, 1, 20, 0, 0, 0, time.UTC)
	games := []standings.Game{
		{ID: "first", Status: standings.CompletedStatus, HomeTeamID: "alpha", AwayTeamID: "charlie", Kickoff: start},
		{ID: "second", Status: fixtures.PreMatchStatus, HomeTeamID: "delta", AwayTeamID: "alpha", Kickoff: start.Add(5 * 24 * time.Hour)},
		{ID: "target", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "bravo", Kickoff: start.Add(8 * 24 * time.Hour)},
	}
	input := FitInput{Teams: []standings.Team{{ID: "alpha"}, {ID: "bravo"}, {ID: "charlie"}, {ID: "delta"}}, Games: games}
	first, err := NewXGPoissonScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Games = []standings.Game{games[2], games[0], games[1]}
	second, err := NewXGPoissonScheduleLoadV1().Fit(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.SeedMaterial()) != string(second.SeedMaterial()) {
		t.Fatal("input order changed seed material")
	}
	firstDistribution, _ := first.Distribution(games[2])
	secondDistribution, _ := second.Distribution(games[2])
	if firstDistribution != secondDistribution {
		t.Fatalf("input order changed distribution: %#v != %#v", firstDistribution, secondDistribution)
	}
}

func TestXGPoissonScheduleLoadClampsAfterAdjustment(t *testing.T) {
	loaded := teamScheduleLoad{recovery: 1, third: true}
	predictor := scheduleLoadXGPredictor{
		base: xgPredictor{
			teams:    map[string]xgTotals{"alpha": {}, "bravo": {}},
			homeRate: maximumRate - 0.01, awayRate: minimumRate + 0.01, leagueRate: 1,
		},
		fixtures: map[string]fixtureScheduleLoad{"target": {away: loaded}},
	}
	distribution, err := predictor.Distribution(standings.Game{ID: "target", Status: fixtures.PreMatchStatus, HomeTeamID: "alpha", AwayTeamID: "bravo"})
	if err != nil {
		t.Fatal(err)
	}
	poisson := distribution.(poissonDistribution)
	if poisson.homeRate != maximumRate || poisson.awayRate != minimumRate {
		t.Fatalf("adjusted rates = %.3f/%.3f, want clamps %.3f/%.3f", poisson.homeRate, poisson.awayRate, maximumRate, minimumRate)
	}
}
