package app

import (
	"testing"

	"github.com/jrduncans/nwsl-season/internal/simulation"
	"github.com/jrduncans/nwsl-season/internal/standings"
)

func TestBoundedPercentKeepsRoundingFromImplyingCertainty(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  string
	}{
		{1, ">99.9%"},
		{.9996, ">99.9%"},
		{.9994, "99.9%"},
		{.5, "50.0%"},
		{.0005, "0.1%"},
		{.0004, "<0.1%"},
		{0, "<0.1%"},
	} {
		if got := boundedPercent(tc.value); got != tc.want {
			t.Errorf("boundedPercent(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestForecastChanceReservesExactValuesForProofs(t *testing.T) {
	if got := forecastChance(.9999, true, false); got != "100%" {
		t.Fatalf("clinched chance = %q, want 100%%", got)
	}
	if got := forecastChance(0, false, true); got != "0%" {
		t.Fatalf("eliminated chance = %q, want 0%%", got)
	}
	if got := forecastChance(1, false, false); got != ">99.9%" {
		t.Fatalf("unproved certain simulation = %q, want >99.9%%", got)
	}
}

func TestPointsEliminationsCountOnlyTeamsStrictlyOutOfReach(t *testing.T) {
	score := func(value int) *int { return &value }
	completed := func(id, home, away string, homeScore, awayScore int) standings.Game {
		return standings.Game{ID: id, Status: standings.CompletedStatus, HomeTeamID: home, AwayTeamID: away, HomeScore: score(homeScore), AwayScore: score(awayScore)}
	}
	teams := []standings.Team{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	games := []standings.Game{
		completed("1", "a", "d", 1, 0),
		completed("2", "a", "c", 1, 0),
		completed("3", "b", "d", 1, 0),
		completed("4", "b", "c", 1, 0),
		completed("5", "c", "d", 1, 0),
		// d (0 points) and c (3 points) each have one match left.
		{ID: "6", Status: "PreMatch", HomeTeamID: "c", AwayTeamID: "d"},
	}

	got := pointsEliminations(teams, games, []int{1, 2})
	// a and b have 6 points. d can reach 3, so both stay ahead of it.
	if !got["d"][1] || !got["d"][2] {
		t.Fatalf("d eliminations = %v, want places 1 and 2", got["d"])
	}
	// c can reach 6 and tie a and b, so the points bound cannot rule it out.
	if got["c"][1] || got["c"][2] {
		t.Fatalf("c eliminations = %v, want none while a tie remains possible", got["c"])
	}
	if len(got["a"]) != 0 || len(got["b"]) != 0 {
		t.Fatalf("leaders were eliminated: a=%v b=%v", got["a"], got["b"])
	}
}

func TestForecastRowsApplyCertaintyToEveryColumn(t *testing.T) {
	result := simulation.Result{Teams: []simulation.TeamResult{
		{Team: standings.Team{ID: "leader"}, ShieldProbability: .8, TopFourProbability: 1, PlayoffProbability: 1, ChampionshipProbability: .2},
		{Team: standings.Team{ID: "chaser"}, ShieldProbability: 0, TopFourProbability: .3, PlayoffProbability: 1, ChampionshipProbability: .1},
		{Team: standings.Team{ID: "out"}, ShieldProbability: 0, TopFourProbability: 0, PlayoffProbability: 0, ChampionshipProbability: 0},
	}}
	certainty := forecastCertainty{
		clinched:   map[string]map[int]bool{"leader": {4: true, 8: true}},
		eliminated: map[string]map[int]bool{"chaser": {1: true}, "out": {8: true}},
	}

	rows, _ := forecastRows(result, 8, certainty)
	leader, chaser, out := rows[0], rows[1], rows[2]
	if leader.TopFourChance != "100%" || leader.PlayoffChance != "100%" || leader.ShieldChance != "80.0%" {
		t.Fatalf("leader = top four %q, playoffs %q, shield %q", leader.TopFourChance, leader.PlayoffChance, leader.ShieldChance)
	}
	if chaser.PlayoffChance != ">99.9%" || chaser.ShieldChance != "0%" {
		t.Fatalf("chaser = playoffs %q, shield %q; want unproved >99.9%% and proved 0%%", chaser.PlayoffChance, chaser.ShieldChance)
	}
	for name, value := range map[string]string{"shield": out.ShieldChance, "top four": out.TopFourChance, "playoffs": out.PlayoffChance, "championship": out.ChampionshipChance} {
		if value != "0%" {
			t.Fatalf("playoff-eliminated %s chance = %q, want 0%%", name, value)
		}
	}
}
